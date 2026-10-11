// Package parties is the master data of who pays and who invoices (T20, Appendix B §B.2.10): customers with their
// billing_parties row (the single FK target of every billed or linked reference and the home of
// billing_date_basis, R6) and the companies that issue invoices (withholding_tax_rate, R18; logo, stamp and
// signature as private objects). Carrier profiles (/v1/subcontractors) live with the tenants in internal/iam.
//
// Every statement runs in db.WithPrincipal, so RLS decides (Appendix C §C.3.0): customers, billing_parties and
// customer_driver_id_types are global-read-steward-write (R60: only own-fleet staff or a platform admin acting as a
// tenant write them; a customer-scope principal reads only its own parties, 0012); companies are tenant rows of
// the caller's reach. Capabilities decide the verbs first (fleet:manage_customers, company:view, company:manage),
// and the list queries still name the reach (main spec §4.7: two layers; Appendix C §C.3.8): the company reads pass
// authz.Principal.TenantReach, the customer list a customer-scope principal's parties; single-row reads and writes
// name the id.
//
// Files go through internal/storage: a key from POST /v1/uploads/presign is committed in the entity transaction
// (Stat, size, type) and the payload carries a short-lived presigned URL, signed after the transaction closes; a
// PATCH that sends back a key the entity already links keeps that file; the objects themselves are private.
package parties

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/parties/partiesdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// Outbox routing keys (Appendix B §B.5.2) and the realtime topic of both (§B.4.2: every staff principal).
const (
	RouteCustomersChanged = "customers.changed"
	RouteCompaniesChanged = "companies.changed"
	TopicGlobal           = "global"
)

// Stable error codes besides the shared ones (Appendix B §B.1.5).
const CodeAlreadyExists = "already_exists"

// Paging (Appendix B §B.1.5).
const (
	DefaultLimit = 50
	MaxLimit     = 500
)

// Files is the storage service as this package uses it (internal/storage implements it). Commit runs in the request
// transaction; SignedFile and FileIDsByKey take their own pool connection, so they run before or after s.tx, never
// inside it (a request that holds one connection while it waits for a second deadlocks the pool under load).
type Files interface {
	Commit(ctx context.Context, tx pgx.Tx, in storage.CommitInput) (storage.Committed, error)
	SignedFile(ctx context.Context, fileID uuid.UUID, ttl time.Duration) (key, url string, err error)
	FileIDsByKey(ctx context.Context, keys []string) (map[string][]uuid.UUID, error)
}

// Invalidator drops the cache: keys an outbox event makes stale (cache.Cache implements it); the relay does the same
// for every relayed event, so a failure here only delays the drop (Appendix B §B.6.1).
type Invalidator interface {
	OnEvent(ctx context.Context, ev cache.Event) error
}

// Deps are the collaborators of the service.
type Deps struct {
	// Pool is the logitrack_app pool: every request runs in db.WithPrincipal.
	Pool db.Beginner
	// Files commits uploaded keys and signs download URLs; optional (without it a key is refused, not_found).
	Files Files
	// Cache drops cache:customer:{id} after a customer write; optional (the outbox relay drops it too).
	Cache Invalidator
	Log   zerolog.Logger
}

// Service serves /v1/customers* and /v1/companies*.
type Service struct {
	pool  db.Beginner
	files Files
	cache Invalidator
	log   zerolog.Logger
}

// New builds the service.
func New(d Deps) (*Service, error) {
	if d.Pool == nil {
		return nil, errors.New("parties: the service needs a pool")
	}
	return &Service{pool: d.Pool, files: d.Files, cache: d.Cache, log: d.Log}, nil
}

// tx runs fn in a request transaction of p: RLS decides every row it reads or writes.
func (s *Service) tx(ctx context.Context, p *authz.Principal, fn func(tx pgx.Tx, q *partiesdb.Queries) error) error {
	return mapDBError(db.WithPrincipal(ctx, s.pool, p, func(tx pgx.Tx) error { return fn(tx, partiesdb.New(tx)) }))
}

// emit appends an outbox event in the change's transaction.
func emit(ctx context.Context, tx pgx.Tx, routingKey, aggType string, id uuid.UUID, tenant *uuid.UUID, rid string) error {
	var headers map[string]string
	if rid != "" {
		headers = map[string]string{"requestId": rid}
	}
	_, err := outbox.Append(ctx, tx, outbox.Event{
		RoutingKey: routingKey, AggregateType: aggType, AggregateID: id.String(), TenantID: tenant,
		Payload: map[string]any{"ids": []string{id.String()}}, Topics: []string{TopicGlobal}, Headers: headers,
	})
	return err
}

// invalidate drops what a committed event made stale; the relay repeats it, so a Redis failure is only logged.
func (s *Service) invalidate(ctx context.Context, routingKey string, id uuid.UUID) {
	if s.cache == nil {
		return
	}
	payload, _ := json.Marshal(map[string]any{"ids": []string{id.String()}})
	if err := s.cache.OnEvent(ctx, cache.Event{RoutingKey: routingKey, AggregateID: id.String(), Payload: payload}); err != nil {
		s.log.Warn().Err(err).Str("event", routingKey).Msg("cache invalidation failed; the outbox relay retries it")
	}
}

// signed returns the key and a presigned GET of a committed file (nil, nil without a file or a storage service).
// Files.SignedFile takes its own pool connection: call it (and companyOf, customerOf) after s.tx returns.
func (s *Service) signed(ctx context.Context, fileID *uuid.UUID) (key, url *string) {
	if fileID == nil || s.files == nil {
		return nil, nil
	}
	k, u, err := s.files.SignedFile(ctx, *fileID, 0)
	if err != nil && !errors.Is(err, storage.ErrObjectNotFound) {
		s.log.Warn().Err(err).Str("file_id", fileID.String()).Msg("cannot sign a file URL")
	}
	if k != "" {
		key = &k
	}
	if u != "" {
		url = &u
	}
	return key, url
}

// commit links an uploaded key to its entity in tx (main spec §9.4); field names the body field in a 422.
func (s *Service) commit(ctx context.Context, tx pgx.Tx, p *authz.Principal, key, field, purpose string, owner uuid.UUID) (uuid.UUID, error) {
	if s.files == nil {
		return uuid.Nil, httpx.ErrInvalidArgument(httpx.FieldViolation{Field: field, Reason: "not_found", Params: map[string]any{"key": key}})
	}
	f, err := s.files.Commit(ctx, tx, storage.CommitInput{Key: key, Field: field, Purposes: []string{purpose}, OwnerID: owner,
		UserID: p.UserID, TenantID: p.EffectiveTenant()})
	if err != nil {
		return uuid.Nil, err
	}
	return f.ID, nil
}

// heldKeys looks up the file rows that hold the asset keys a PATCH carries, before its transaction (Files.FileIDsByKey
// takes its own pool connection). nil without keys or a storage service: then every key is committed.
func (s *Service) heldKeys(ctx context.Context, fields ...Field[string]) (map[string][]uuid.UUID, error) {
	var keys []string
	for _, f := range fields {
		if v := textOf(f); f.Set && v != nil {
			keys = append(keys, *v)
		}
	}
	if len(keys) == 0 || s.files == nil {
		return nil, nil
	}
	return s.files.FileIDsByKey(ctx, keys)
}

// assetFile is the file a PATCH asset field that is set resolves to in the request transaction: nil for null or
// blank (unlink; the object stays, main spec §9.3); the file the locked row already links in that slot when the key
// is that file's (kept as it is, whoever uploaded it: the payload's keys go back in a PATCH, and an editor in
// contractor reach or a steward acting as another tenant is neither the uploader nor in its tenant, the rule of
// storage.Commit); else the key committed to owner. held is heldKeys of the request.
func (s *Service) assetFile(ctx context.Context, tx pgx.Tx, p *authz.Principal, f Field[string], linked *uuid.UUID,
	held map[string][]uuid.UUID, field, purpose string, owner uuid.UUID) (*uuid.UUID, error) {
	v := textOf(f)
	if v == nil {
		return nil, nil
	}
	if linked != nil && slices.Contains(held[*v], *linked) {
		id := *linked
		return &id, nil
	}
	id, err := s.commit(ctx, tx, p, *v, field, purpose, owner)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// --- request helpers ---------------------------------------------------------------------------------------

// Field is an optional JSON field of a PATCH body (the type of the T19 administration): Set when the key is present,
// Null when its value is null.
type Field[T any] = iam.Field[T]

// textOf is the value a text field takes: the trimmed text, nil for null or blank.
func textOf(f Field[string]) *string {
	if f.Null {
		return nil
	}
	return opt(f.V)
}

func opt(s string) *string {
	if s = strings.TrimSpace(s); s == "" {
		return nil
	}
	return &s
}

func eq[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func violation(field, reason string) httpx.FieldViolation {
	return httpx.FieldViolation{Field: field, Reason: reason}
}

// checker collects the field violations of one request.
type checker struct{ bad []httpx.FieldViolation }

func (c *checker) add(field, reason string) { c.bad = append(c.bad, violation(field, reason)) }

func (c *checker) err() error {
	if len(c.bad) == 0 {
		return nil
	}
	return httpx.ErrInvalidArgument(c.bad...)
}

// text checks a free-text field: required, at most max characters, valid UTF-8.
func (c *checker) text(field, v string, max int, required bool) {
	v = strings.TrimSpace(v)
	switch n := utf8.RuneCountInString(v); {
	case !utf8.ValidString(v):
		c.add(field, "invalid")
	case n == 0 && required:
		c.add(field, "required")
	case n > max:
		c.bad = append(c.bad, httpx.FieldViolation{Field: field, Reason: "too_long", Params: map[string]any{"max": max}})
	}
}

// optText checks a PATCH text field that is set and not null.
func (c *checker) optText(field string, f Field[string], max int) {
	if f.Set && !f.Null {
		c.text(field, f.V, max, false)
	}
}

// thaiID checks a 13-digit Thai national ID / tax id (T20 acceptance: a bad checksum is 422 with field errors).
func (c *checker) thaiID(field, v string, required bool) {
	switch v = strings.TrimSpace(v); {
	case v == "" && required:
		c.add(field, "required")
	case v != "" && !iam.ValidThaiID(v):
		c.add(field, "invalid")
	}
}

// email checks an address without a display name.
func (c *checker) email(field, v string) {
	if v = strings.TrimSpace(v); v == "" {
		return
	}
	a, err := mail.ParseAddress(v)
	if err != nil || a.Address != v || len(v) > 254 {
		c.add(field, "invalid")
	}
}

// key checks an object key from POST /v1/uploads/presign.
func (c *checker) key(field string, f Field[string]) {
	if f.Set && !f.Null && (strings.TrimSpace(f.V) == "" || len(f.V) > storage.MaxKeyBytes) {
		c.add(field, "invalid")
	}
}

var (
	branchNumberRx = regexp.MustCompile(`^[0-9]{5}$`)
	lineGroupRx    = regexp.MustCompile(`^C[0-9a-f]{32}$`)
)

// --- keyset cursors ---------------------------------------------------------------------------------------

// cursor is the opaque keyset position (sort value, id) of Appendix B §B.1.5, base64url JSON: S for a text sort
// (customers by code), T for a time sort (companies by created_at).
type cursor struct {
	S  *string    `json:"s,omitempty"`
	T  *time.Time `json:"t,omitempty"`
	ID uuid.UUID  `json:"id"`
}

func (c cursor) encode() string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (cursor, bool) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return cursor{}, false
	}
	var c cursor
	if json.Unmarshal(b, &c) != nil || c.ID == uuid.Nil || (c.S == nil) == (c.T == nil) {
		return cursor{}, false
	}
	return c, true
}

// --- errors ---------------------------------------------------------------------------------------------

// uniqueFields maps the unique constraints a write can meet to the request field and reason.
var uniqueFields = map[string][2]string{
	"customers_code":           {"code", "taken"},
	"companies_owner_one":      {"companyType", "owner_exists"},
	"billing_parties_customer": {"billingPartyId", "taken"},
}

// mapDBError turns the database refusals a request can still meet into the API's codes: unique keys (409), checks
// and foreign keys (422), and RLS (42501: a row outside the caller's write reach, 403); anything else is returned as
// is (500 for an unknown database error).
func mapDBError(err error) error {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
		return err
	}
	switch pe.Code {
	case "23505":
		f, ok := uniqueFields[pe.ConstraintName]
		if !ok {
			f = [2]string{"", "taken"}
		}
		return httpx.NewError(http.StatusConflict, CodeAlreadyExists, "already exists").
			WithDetails(map[string]any{"field": f[0], "reason": f[1]}).Wrap(err)
	case "23503":
		return httpx.ErrInvalidArgument(violation(pe.ConstraintName, "not_found")).Wrap(err)
	case "23514":
		return httpx.ErrInvalidArgument(violation(pe.ConstraintName, "invalid")).Wrap(err)
	case "42501":
		return authz.ErrPermissionDenied("row-level security refused the write").Wrap(err)
	}
	return err
}
