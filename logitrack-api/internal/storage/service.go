package storage

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage/storagedb"
)

// PendingTTL is how long a pending upload lives before storage.gc removes the row and the object (main spec §9.3).
const PendingTTL = 24 * time.Hour

// RouteObjectCommitted is the outbox event of a commit (Appendix B §B.5.2); images.thumbnail consumes it.
const RouteObjectCommitted = "storage.object_committed"

// Defaults of main spec §16.1 for the settings a process leaves unset.
const (
	DefaultUploadMaxBytes     = 10 << 20         // UPLOAD_MAX_BYTES
	DefaultPresignPutTTL      = 15 * time.Minute // S3_PRESIGN_PUT_TTL
	DefaultPresignGetTTL      = time.Hour        // S3_PRESIGN_GET_TTL
	DefaultEvidencePresignTTL = 15 * time.Minute // EVIDENCE_PRESIGN_TTL
)

// Config is the service configuration; the app maps the §16.1 names onto it.
type Config struct {
	// Active is STORAGE_BACKEND: the backend of new uploads.
	Active string
	// Bucket and PublicBucket are S3_BUCKET and S3_PUBLIC_BUCKET (file_objects.bucket; a label on local rows).
	Bucket, PublicBucket string
	// PresignPutTTL (S3_PRESIGN_PUT_TTL) and PresignGetTTL (S3_PRESIGN_GET_TTL) bound every signed URL, local
	// ones included.
	PresignPutTTL, PresignGetTTL time.Duration
	// UploadMaxBytes is UPLOAD_MAX_BYTES: presign refuses more, the local upload route reads no more, commit checks.
	UploadMaxBytes int64
	// EvidenceTokenTTLDays is EVIDENCE_TOKEN_TTL_DAYS (0 = tokens never expire, R30).
	EvidenceTokenTTLDays int
	// EvidencePresignTTL is EVIDENCE_PRESIGN_TTL, the life of gallery image URLs.
	EvidencePresignTTL time.Duration
}

// Caller is the authenticated principal of a storage request as the auth middleware resolved it.
type Caller struct {
	UserID uuid.UUID
	// TenantID is the tenant the request acts in; nil for customer-scope and platform-only principals.
	TenantID *uuid.UUID
	// Staff holds a staff role in TenantID (tenant_admin, manager, operation_staff, operator, user): it reads the
	// files of its tenant and of the carriers working for it (R60), like the RLS policy p_read, except where the
	// file's purpose narrows that rule (Purpose.ReadCapability, NoStaffRead).
	Staff bool
	// Can reports a capability of the effective set (iam.RBAC); nil holds none.
	Can func(authz.Cap) bool
	// Steward (own-fleet staff or platform_admin, R60) may upload platform objects (tenant NULL) when it acts in no
	// tenant, like p_upload's steward branch.
	Steward bool
	// ReadAll is the audited read-only cross-tenant bypass (X-Act-On-Tenant: *, Appendix C §C.3.9): every file.
	ReadAll bool
	// Machine is an API-key principal: it has no users row, so it never uploads (uploaded_by references users).
	Machine bool
}

// FileRef is the committed file an Authorizer decides on.
type FileRef struct {
	ID       uuid.UUID
	Key      string
	Purpose  string
	OwnerID  uuid.UUID
	TenantID *uuid.UUID
}

// Authorizer decides whether the caller may read a file committed to an entity of its owner kind ("a file is
// readable iff its referencing row is", Appendix C §C.3.5). Domain services register one per owner kind as they
// land. A registered Authorizer is authoritative for its owner kind: it may narrow the staff rule (a capability
// the purpose needs) as well as widen it (the driver who owns the entity); only public objects, the caller's own
// uploads and the audited read-only bypass are decided before it. Without one, staff in reach read the file
// unless its purpose narrows the staff rule (Purpose.ReadCapability, NoStaffRead).
type Authorizer func(ctx context.Context, c Caller, f FileRef) (bool, error)

// Service is the storage service.
type Service struct {
	pool     db.Beginner
	cfg      Config
	backends map[string]Backend
	local    *Local
	log      zerolog.Logger
	now      func() time.Time

	mu          sync.RWMutex
	authorizers map[string]Authorizer
}

// New builds the service over the configured backends; the active one must be among them.
func New(pool db.Beginner, cfg Config, log zerolog.Logger, backends ...Backend) (*Service, error) {
	s := &Service{pool: pool, cfg: cfg, backends: map[string]Backend{}, log: log, now: time.Now, authorizers: map[string]Authorizer{}}
	for _, b := range backends {
		if b == nil {
			continue
		}
		s.backends[b.Name()] = b
		if l, ok := b.(*Local); ok {
			s.local = l
		}
	}
	if _, ok := s.backends[cfg.Active]; !ok {
		return nil, fmt.Errorf("storage: STORAGE_BACKEND %q has no configured backend", cfg.Active)
	}
	if cfg.Bucket == "" || cfg.PublicBucket == "" {
		return nil, errors.New("storage: S3_BUCKET and S3_PUBLIC_BUCKET are required")
	}
	// Zero values take the §16.1 defaults (the worker signs nothing and leaves them unset).
	if s.cfg.UploadMaxBytes <= 0 {
		s.cfg.UploadMaxBytes = DefaultUploadMaxBytes
	}
	if s.cfg.PresignPutTTL <= 0 {
		s.cfg.PresignPutTTL = DefaultPresignPutTTL
	}
	if s.cfg.PresignGetTTL <= 0 {
		s.cfg.PresignGetTTL = DefaultPresignGetTTL
	}
	if s.cfg.EvidencePresignTTL <= 0 {
		s.cfg.EvidencePresignTTL = DefaultEvidencePresignTTL
	}
	return s, nil
}

// SetClock replaces the clock (tests).
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// Active is the backend of new uploads.
func (s *Service) Active() string { return s.cfg.Active }

// Local is the local backend, nil when LOCAL_MEDIA_DIR is unset.
func (s *Service) Local() *Local { return s.local }

// Backend returns a configured backend by name.
func (s *Service) Backend(name string) (Backend, bool) {
	b, ok := s.backends[name]
	return b, ok
}

// RegisterAuthorizer installs the read check of one owner kind.
func (s *Service) RegisterAuthorizer(ownerKind string, a Authorizer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authorizers[ownerKind] = a
}

// Check is the readiness probe: the active backend answers (Appendix B §B.2.1).
func (s *Service) Check(ctx context.Context) error { return s.backends[s.cfg.Active].Check(ctx) }

// Close releases the local directory handle.
func (s *Service) Close() {
	if s.local != nil {
		_ = s.local.Close()
	}
}

func (s *Service) backend(name string) (Backend, error) {
	if b, ok := s.backends[name]; ok {
		return b, nil
	}
	return nil, httpx.ErrUnavailable("storage backend not configured").
		WithDetails(map[string]any{"storageBackend": name}).Wrap(fmt.Errorf("storage: backend %q is not configured", name))
}

func object(r storagedb.FileObject) Object { return Object{Bucket: r.Bucket, Key: r.ObjectKey} }

func errPermission(reason string) *httpx.Error {
	return httpx.NewError(http.StatusForbidden, "permission_denied", "permission denied").
		WithDetails(map[string]any{"reason": reason})
}

func errTenantRequired() *httpx.Error {
	return httpx.NewError(http.StatusForbidden, "tenant_required", "an active tenant is required to upload")
}

func errFailedPrecondition(reason string) *httpx.Error {
	return httpx.NewError(http.StatusConflict, "failed_precondition", "failed precondition").
		WithDetails(map[string]any{"reason": reason})
}

func violation(field, reason string, params map[string]any) *httpx.Error {
	return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: field, Reason: reason, Params: params})
}

// PresignInput is the body of POST /v1/uploads/presign (Appendix B §B.2.20). Variant (photo type, document kind,
// attachment index) and FileName (truck and tenant documents keep a readable name) feed the key template.
type PresignInput struct {
	Purpose     string `json:"purpose"`
	EntityID    string `json:"entityId"`
	ContentType string `json:"contentType"`
	SizeBytes   int64  `json:"sizeBytes"`
	SHA256      string `json:"sha256"`
	Key         string `json:"key"`
	Variant     string `json:"variant"`
	FileName    string `json:"fileName"`
}

// Presigned is the answer of a presign: send Method to URL with exactly Headers before ExpiresAt, then reference
// Key in the entity request. URL is absolute for the s3 backend (the S3_PRESIGN_ENDPOINT origin) and for the
// driver app's local uploads (LOCAL_MEDIA_PUBLIC_BASE_URL); for the web's local uploads it is a path on the Go API
// (/v1/uploads/local/...), which the browser calls through the BFF as /api/go + url.
type Presigned struct {
	Key            string            `json:"key"`
	URL            string            `json:"url"`
	Method         string            `json:"method"`
	Headers        map[string]string `json:"headers"`
	ExpiresAt      time.Time         `json:"expiresAt"`
	StorageBackend string            `json:"storageBackend"`
}

var sha256Rx = regexp.MustCompile(`^[0-9a-f]{64}$`)

// normalizeType lower-cases a media type and drops parameters; "" when it does not parse.
func normalizeType(t string) string {
	mt, _, err := mime.ParseMediaType(t)
	if err != nil {
		return ""
	}
	return mt
}

// Presign creates a pending upload and signs its PUT, or re-signs a pending key of the caller (offline retry after
// the URL expired). Validation failures are 422 invalid_argument naming the fields.
func (s *Service) Presign(ctx context.Context, c Caller, in PresignInput, opts PutOptions) (*Presigned, error) {
	if in.Key != "" {
		return s.resign(ctx, c, in, opts)
	}
	var v []httpx.FieldViolation
	p, ok := Purposes[in.Purpose]
	if !ok || !p.Presign {
		v = append(v, httpx.FieldViolation{Field: "purpose", Reason: "invalid"})
	}
	ct := normalizeType(in.ContentType)
	if ok && !p.AcceptsType(ct) {
		v = append(v, httpx.FieldViolation{Field: "contentType", Reason: "not_allowed", Params: map[string]any{"allowed": p.ContentTypes}})
	}
	if in.SizeBytes <= 0 || in.SizeBytes > s.cfg.UploadMaxBytes {
		v = append(v, httpx.FieldViolation{Field: "sizeBytes", Reason: "out_of_range", Params: map[string]any{"min": 1, "max": s.cfg.UploadMaxBytes}})
	}
	if in.SHA256 != "" && !sha256Rx.MatchString(in.SHA256) {
		v = append(v, httpx.FieldViolation{Field: "sha256", Reason: "invalid"})
	}
	if ok && !p.ValidVariant(in.Variant) {
		v = append(v, httpx.FieldViolation{Field: "variant", Reason: "invalid"})
	}
	if len(in.FileName) > 255 {
		v = append(v, httpx.FieldViolation{Field: "fileName", Reason: "length", Params: map[string]any{"max": 255}})
	}
	entity := uuid.Nil
	switch {
	case in.EntityID != "":
		id, err := uuid.Parse(in.EntityID)
		if err != nil {
			v = append(v, httpx.FieldViolation{Field: "entityId", Reason: "invalid"})
		}
		entity = id
	case ok && p.EntityOptional:
		entity = c.UserID
	case ok:
		v = append(v, httpx.FieldViolation{Field: "entityId", Reason: "required"})
	}
	if len(v) > 0 {
		return nil, httpx.ErrInvalidArgument(v...)
	}
	// The entity's own guard runs at commit, in the entity transaction; at presign only self-owned purposes are
	// checked here (a pending upload lives in the caller's tenant and can be committed only by its own uploader or
	// tenant, so a foreign entity id in a key grants nothing).
	if c.Machine {
		return nil, errPermission("machine_principal")
	}
	if p.OwnerKind == OwnerUser && entity != c.UserID {
		return nil, errPermission("not_own_profile")
	}
	if c.TenantID == nil && !c.Steward {
		return nil, errTenantRequired()
	}
	b, err := s.backend(s.cfg.Active)
	if err != nil {
		return nil, err
	}
	var sha *string
	if in.SHA256 != "" {
		sha = &in.SHA256
	}
	var out *Presigned
	for attempt := range 3 {
		now := s.now().Add(time.Duration(attempt) * time.Millisecond)
		key, err := p.Key(KeyInput{EntityID: entity, Variant: in.Variant, FileName: in.FileName, ContentType: ct, Now: now})
		if err != nil {
			return nil, err
		}
		err = db.WithSystem(ctx, s.pool, c.TenantID, func(tx pgx.Tx) error {
			row, err := storagedb.New(tx).InsertPending(ctx, storagedb.InsertPendingParams{
				Bucket: s.cfg.Bucket, ObjectKey: key, TenantID: c.TenantID, Purpose: p.Name, ContentType: ct,
				SizeBytes: in.SizeBytes, Sha256: sha, UploadedBy: c.UserID, ExpiresAt: now.Add(PendingTTL),
				StorageBackend: b.Name(),
			})
			if err != nil {
				return err
			}
			// Signed inside the transaction: a backend that cannot sign leaves no pending row behind.
			put, err := b.PresignPut(ctx, object(row), ct, in.SizeBytes, s.cfg.PresignPutTTL, opts)
			if err != nil {
				return httpx.ErrUnavailable("storage cannot sign the upload").Wrap(err)
			}
			out = &Presigned{Key: key, URL: put.URL, Method: put.Method, Headers: put.Headers, ExpiresAt: put.Expires.UTC(), StorageBackend: b.Name()}
			return nil
		})
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "23505" {
			continue // same key in the same millisecond: take the next one
		}
		return out, err
	}
	return nil, httpx.ErrUnavailable("could not allocate an object key")
}

// resign signs a new PUT for a pending key of the caller and gives the row another 24 h. The upload goes to the
// row's own backend, so a pending local upload stays local after a switch to s3.
func (s *Service) resign(ctx context.Context, c Caller, in PresignInput, opts PutOptions) (*Presigned, error) {
	notFound := violation("key", "not_found", map[string]any{"key": in.Key})
	if ValidateKey(in.Key) != nil {
		return nil, notFound
	}
	var out *Presigned
	err := db.WithSystem(ctx, s.pool, c.TenantID, func(tx pgx.Tx) error {
		q := storagedb.New(tx)
		row, err := q.LockFileByKey(ctx, in.Key)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && (row.UploadedBy == nil || *row.UploadedBy != c.UserID)) {
			return notFound
		}
		if err != nil {
			return err
		}
		if row.Status != "pending" {
			return violation("key", "not_pending", map[string]any{"key": in.Key})
		}
		var v []httpx.FieldViolation
		if in.Purpose != "" && in.Purpose != row.Purpose {
			v = append(v, httpx.FieldViolation{Field: "purpose", Reason: "mismatch"})
		}
		ct := deref(row.ContentType)
		if in.ContentType != "" && normalizeType(in.ContentType) != ct {
			v = append(v, httpx.FieldViolation{Field: "contentType", Reason: "mismatch"})
		}
		if in.SizeBytes != 0 && (row.SizeBytes == nil || in.SizeBytes != *row.SizeBytes) {
			v = append(v, httpx.FieldViolation{Field: "sizeBytes", Reason: "mismatch"})
		}
		if len(v) > 0 {
			return httpx.ErrInvalidArgument(v...)
		}
		b, err := s.backend(row.StorageBackend)
		if err != nil {
			return err
		}
		if row, err = q.ExtendPending(ctx, storagedb.ExtendPendingParams{ExpiresAt: s.now().Add(PendingTTL), ID: row.ID}); err != nil {
			return err
		}
		put, err := b.PresignPut(ctx, object(row), ct, deref(row.SizeBytes), s.cfg.PresignPutTTL, opts)
		if err != nil {
			return httpx.ErrUnavailable("storage cannot sign the upload").Wrap(err)
		}
		out = &Presigned{Key: row.ObjectKey, URL: put.URL, Method: put.Method, Headers: put.Headers, ExpiresAt: put.Expires.UTC(), StorageBackend: b.Name()}
		return nil
	})
	return out, err
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// CommitInput is one key an entity request references.
type CommitInput struct {
	// Key is the object key from the request body; Field names that body field in a 422 (e.g. "photoKey").
	Key, Field string
	// Purposes are the purposes the referencing column accepts.
	Purposes []string
	// OwnerID is the entity row the file is linked to (owner kind comes from the purpose).
	OwnerID uuid.UUID
	// UserID and TenantID are the caller's: only the uploader or a member of the uploading tenant commits a key.
	UserID   uuid.UUID
	TenantID *uuid.UUID
}

// Committed is the file a commit linked.
type Committed struct {
	ID          uuid.UUID
	Key         string
	ContentType string
	SizeBytes   int64
}

// Commit runs inside the entity transaction (main spec §9.4): it locks the pending row, Stats the object on the
// row's backend (it exists, its size equals the declared size and is at most UPLOAD_MAX_BYTES, its content type and,
// where the backend knows it, its sha256 equal the declared ones), marks the row committed with its owner, and
// appends storage.object_committed. Any failure is 422 invalid_argument naming the key; the caller rolls back, so
// the row stays pending. Committing the same key to the same owner again (an outbox replay) returns the row.
func (s *Service) Commit(ctx context.Context, tx pgx.Tx, in CommitInput) (Committed, error) {
	field := in.Field
	if field == "" {
		field = "key"
	}
	bad := func(reason string, params map[string]any) error {
		if params == nil {
			params = map[string]any{}
		}
		params["key"] = in.Key
		return violation(field, reason, params)
	}
	if ValidateKey(in.Key) != nil {
		return Committed{}, bad("not_found", nil)
	}
	q := storagedb.New(tx)
	row, err := q.LockFileByKey(ctx, in.Key)
	if errors.Is(err, pgx.ErrNoRows) {
		return Committed{}, bad("not_found", nil)
	}
	if err != nil {
		return Committed{}, err
	}
	sameUploader := row.UploadedBy != nil && *row.UploadedBy == in.UserID
	sameTenant := in.TenantID != nil && row.TenantID != nil && *row.TenantID == *in.TenantID
	if !sameUploader && !sameTenant {
		return Committed{}, bad("not_found", nil)
	}
	if !slices.Contains(in.Purposes, row.Purpose) {
		return Committed{}, bad("purpose_mismatch", map[string]any{"purpose": row.Purpose})
	}
	owner := Purposes[row.Purpose].OwnerKind
	done := Committed{ID: row.ID, Key: row.ObjectKey, ContentType: deref(row.ContentType), SizeBytes: deref(row.SizeBytes)}
	switch row.Status {
	case "committed":
		if deref(row.OwnerKind) == owner && row.OwnerID != nil && *row.OwnerID == in.OwnerID {
			return done, nil
		}
		return Committed{}, bad("already_committed", nil)
	case "pending":
	default:
		return Committed{}, bad("not_found", nil)
	}
	b, err := s.backend(row.StorageBackend)
	if err != nil {
		return Committed{}, err
	}
	info, err := b.Stat(ctx, object(row))
	if errors.Is(err, ErrObjectNotFound) {
		return Committed{}, bad("upload_missing", nil)
	}
	if err != nil {
		return Committed{}, httpx.ErrUnavailable("storage unavailable").Wrap(err)
	}
	if row.SizeBytes != nil && info.Size != *row.SizeBytes {
		return Committed{}, bad("size_mismatch", map[string]any{"expected": *row.SizeBytes, "actual": info.Size})
	}
	if info.Size > s.cfg.UploadMaxBytes {
		return Committed{}, bad("too_large", map[string]any{"max": s.cfg.UploadMaxBytes, "actual": info.Size})
	}
	if want := deref(row.ContentType); want != "" && normalizeType(info.ContentType) != want {
		return Committed{}, bad("content_type_mismatch", map[string]any{"expected": want, "actual": info.ContentType})
	}
	if row.Sha256 != nil && info.SHA256 != "" && info.SHA256 != *row.Sha256 {
		return Committed{}, bad("checksum_mismatch", nil)
	}
	now := s.now()
	if err := q.CommitFile(ctx, storagedb.CommitFileParams{
		CommittedAt: now, OwnerKind: owner, OwnerID: in.OwnerID, ContentType: normalizeType(info.ContentType),
		SizeBytes: info.Size, ID: row.ID,
	}); err != nil {
		return Committed{}, err
	}
	if _, err := outbox.Append(ctx, tx, outbox.Event{
		RoutingKey: RouteObjectCommitted, AggregateType: "file", AggregateID: row.ID.String(), TenantID: row.TenantID,
		Payload: map[string]any{
			"fileId": row.ID, "key": row.ObjectKey, "purpose": row.Purpose, "contentType": normalizeType(info.ContentType),
			"sizeBytes": info.Size, "ownerKind": owner, "ownerId": in.OwnerID, "storageBackend": row.StorageBackend,
		},
	}); err != nil {
		return Committed{}, err
	}
	done.ContentType, done.SizeBytes = normalizeType(info.ContentType), info.Size
	return done, nil
}

// mayRead decides GET /v1/files for a caller: public objects, the caller's own uploads and the audited
// read-only bypass first; then the owner kind's Authorizer, which is authoritative when registered; then the
// staff rule of the RLS policy p_read (staff of the file's tenant or of its contractor), narrowed by the
// purpose's read rule (a capability such as drivers:view_pii, or no staff rule at all).
func (s *Service) mayRead(ctx context.Context, q *storagedb.Queries, c Caller, row storagedb.FileObject) (bool, error) {
	if c.ReadAll || row.Visibility == "public" || (row.UploadedBy != nil && *row.UploadedBy == c.UserID) {
		return true, nil
	}
	if row.OwnerKind != nil && row.OwnerID != nil {
		s.mu.RLock()
		a := s.authorizers[*row.OwnerKind]
		s.mu.RUnlock()
		if a != nil {
			return a(ctx, c, FileRef{ID: row.ID, Key: row.ObjectKey, Purpose: row.Purpose, OwnerID: *row.OwnerID, TenantID: row.TenantID})
		}
	}
	rule := Purposes[row.Purpose].ReadRuleFor(row.ObjectKey)
	if !c.Staff || rule.NoStaffRead || c.TenantID == nil || row.TenantID == nil {
		return false, nil
	}
	if rule.Capability != "" && (c.Can == nil || !c.Can(rule.Capability)) {
		return false, nil
	}
	if *row.TenantID == *c.TenantID {
		return true, nil
	}
	return q.IsDirectSubtenant(ctx, storagedb.IsDirectSubtenantParams{TenantID: *row.TenantID, ContractorID: *c.TenantID})
}

// readTTL is the lifetime of a download URL of row: ttl (0 = S3_PRESIGN_GET_TTL), capped by its purpose's rule.
func (s *Service) readTTL(row storagedb.FileObject, ttl time.Duration) time.Duration {
	if ttl <= 0 {
		ttl = s.cfg.PresignGetTTL
	}
	if m := Purposes[row.Purpose].ReadRuleFor(row.ObjectKey).MaxTTL; m > 0 {
		ttl = min(ttl, m)
	}
	return ttl
}

// DownloadURL is GET /v1/files?key=: a short-lived URL of a committed object the caller may read (its own pending
// upload too, for a preview). Unknown, unreadable and missing-at-source keys are all 404.
func (s *Service) DownloadURL(ctx context.Context, c Caller, key string) (string, error) {
	if ValidateKey(key) != nil {
		return "", httpx.ErrNotFound()
	}
	var row storagedb.FileObject
	err := db.WithSystem(ctx, s.pool, nil, func(tx pgx.Tx) error {
		q := storagedb.New(tx)
		var err error
		if row, err = q.GetFileByKey(ctx, key); errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		} else if err != nil {
			return err
		}
		ok, err := s.mayRead(ctx, q, c, row)
		if err != nil {
			return err
		}
		own := row.UploadedBy != nil && *row.UploadedBy == c.UserID
		visible := row.Status == "committed" || (row.Status == "pending" && own)
		if !ok || !visible {
			return httpx.ErrNotFound()
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	u, _, err := s.signGet(ctx, row, s.readTTL(row, 0), GetOptions{})
	return u, err
}

func (s *Service) signGet(ctx context.Context, row storagedb.FileObject, ttl time.Duration, opts GetOptions) (string, time.Time, error) {
	b, err := s.backend(row.StorageBackend)
	if err != nil {
		return "", time.Time{}, err
	}
	if row.Visibility == "public" && IsPublicKey(row.ObjectKey) {
		u, err := b.PublicURL(object(row))
		if err != nil {
			return "", time.Time{}, httpx.ErrUnavailable("storage cannot build the URL").Wrap(err)
		}
		return u, time.Time{}, nil
	}
	u, exp, err := b.PresignGet(ctx, object(row), ttl, opts)
	if err != nil {
		return "", time.Time{}, httpx.ErrUnavailable("storage cannot sign the download").Wrap(err)
	}
	return u, exp, nil
}

// SignedURL is the URL an entity payload carries for a committed file (main spec §9.5: S3_PRESIGN_GET_TTL). The
// caller has already read the referencing row under its own rights; ttl 0 means S3_PRESIGN_GET_TTL, and the
// purpose caps it (driver PII 5 min, documents 15 min).
func (s *Service) SignedURL(ctx context.Context, fileID uuid.UUID, ttl time.Duration) (string, error) {
	var row storagedb.FileObject
	err := db.WithSystem(ctx, s.pool, nil, func(tx pgx.Tx) error {
		var err error
		row, err = storagedb.New(tx).GetFile(ctx, fileID)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && row.Status != "committed") {
		return "", ErrObjectNotFound
	}
	if err != nil {
		return "", err
	}
	u, _, err := s.signGet(ctx, row, s.readTTL(row, ttl), GetOptions{})
	return u, err
}

// PublishLocal makes a staged local upload visible while it holds the lock of the key's pending local row, the
// lock Commit takes: an upload that finds the row committed (or gone) is 409 failed_precondition not_pending and
// leaves the committed bytes untouched, and a commit that starts during the rename waits and then Stats the new
// bytes. The lock is held only for the renames, never while the body arrives.
func (s *Service) PublishLocal(ctx context.Context, key string, st *Staged) error {
	return db.WithSystem(ctx, s.pool, nil, func(tx pgx.Tx) error {
		_, err := storagedb.New(tx).LockPendingLocalUpload(ctx, key)
		if errors.Is(err, pgx.ErrNoRows) {
			return errFailedPrecondition("not_pending")
		}
		if err != nil {
			return err
		}
		return st.Publish()
	})
}

// PendingLocal reports whether key is a pending upload of the local backend: the upload route's early refusal
// before it writes to disk (PublishLocal decides under the row lock).
func (s *Service) PendingLocal(ctx context.Context, key string) (bool, error) {
	ok := false
	err := db.WithSystem(ctx, s.pool, nil, func(tx pgx.Tx) error {
		_, err := storagedb.New(tx).PendingLocalUpload(ctx, key)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		ok = err == nil
		return err
	})
	return ok, err
}
