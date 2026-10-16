// Package linenotify holds the routes that ask for a LINE card or close an evidence link (T49; main spec §5.6, §7.6,
// §9.6; Appendix B §B.2.11-§B.2.13, §B.2.21-§B.2.22): the staff force routes POST /v1/{tasks|trips|standby}/{id}/
// notify-line and the revocations POST /v1/{trips|standby}/{id}/evidence/revoke on the internal listener, and the shim
// targets of the sendCustomerLineNotification callable POST /v1/mobile/{tasks|trips|standby}/{id}/line-notify on the
// public one. A request never sends anything itself: it reads the record under the caller's RLS (db.WithPrincipal) and
// submits a notify.line.force job (jobs.Service.Submit, lock scope card:record), which the worker's notify.line
// consumer sends once (internal/notify). The gallery itself, GET /evidence/{token}, is internal/storage's.
//
// A revocation closes the links customers hold, or says it could not (Coexistence): a token Go minted is linked only
// from Go's gallery; a legacy one (16 characters) also from the legacy gallery (cloudfunctions.net/tripEvidence?k=),
// which looks the token up in Firestore and never reads PostgreSQL.
package linenotify

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/linenotify/linenotifydb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/notify"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/writeback"
)

// Submitter submits a job (jobs.Service).
type Submitter interface {
	Submit(ctx context.Context, in jobs.SubmitInput) (jobs.Job, error)
}

// Service serves the routes.
type Service struct {
	pool db.Beginner
	jobs Submitter
	now  func() time.Time
	co   atomic.Pointer[Coexistence]
}

// Coexistence is what a revocation knows of the legacy LINE path (main spec §9.6, §13.7, §19 Q10). Until comms is in
// PG_OWNED_DOMAINS (P5) the legacy callable sendCustomerLineNotification sends the cards and the FS->PG mirror owns
// evidence_token; every card it sent (before P5 and after) links the legacy gallery tripEvidence?k=, which looks the
// token up in Firestore trip_records, then standby_records, and has no revocation. Go mints only v1 tokens (34
// characters), so a legacy token (16 characters) is the sign of such a link.
type Coexistence struct {
	// CommsOwned is whether comms is in PG_OWNED_DOMAINS: the mirror then leaves evidence_token as PostgreSQL holds it.
	CommsOwned bool
	// Firestore is the api's write-back writer (WRITEBACK_FIRESTORE_PROJECT_ID; nil without one): once comms is owned a
	// revocation of a legacy token removes evidenceToken from the Firestore documents that hold it, so the legacy
	// gallery answers its own 404 too.
	Firestore writeback.Writer
}

// New builds the service.
func New(pool db.Beginner, j Submitter, co Coexistence) *Service {
	s := &Service{pool: pool, jobs: j, now: time.Now}
	s.co.Store(&co)
	return s
}

// SetClock replaces the clock (tests).
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// SetCoexistence replaces the coexistence state (tests; safe while the routes serve).
func (s *Service) SetCoexistence(co Coexistence) { s.co.Store(&co) }

// ForceInput is the body of a staff request: force resends a card the record's flag says was sent (and replaces a
// revoked evidence token, R47); without it the card is sent only if it was not.
type ForceInput struct {
	Force bool `json:"force"`
}

// Queued is the answer of a staff request: 202 with the job that sends the card (GET /v1/jobs/{id}).
type Queued struct {
	JobID  uuid.UUID `json:"jobId"`
	Status string    `json:"status"`
}

// ShimResult is the answer of a shim target, the callable's {ok, skipped?, reason?}.
type ShimResult struct {
	OK      bool   `json:"ok"`
	Skipped bool   `json:"skipped,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// ReasonAlreadyQueued is the shim answer while another request for the same card waits in the queue.
const ReasonAlreadyQueued = "already queued"

// record is a task, trip or standby record as the caller reads it.
type record struct {
	id, tenant uuid.UUID
	notifiedAt *time.Time
}

// read finds the record of card under the caller's RLS: by id, or (ref, for the shim targets) by the Firestore doc id
// and, for a trip, its number. found is false for a record the caller cannot read.
func (s *Service) read(ctx context.Context, p *authz.Principal, card string, id *uuid.UUID, ref string) (record, bool, error) {
	var r record
	found := false
	err := db.WithPrincipal(ctx, s.pool, p, func(tx pgx.Tx) error {
		q := linenotifydb.New(tx)
		var err error
		switch {
		case card == notify.LineCheckin && id != nil:
			var row linenotifydb.TaskForLineRow
			row, err = q.TaskForLine(ctx, *id)
			r = record{row.ID, row.TenantID, row.NotifiedAt}
		case card == notify.LineCheckin:
			var row linenotifydb.TaskForLineByLegacyRow
			row, err = q.TaskForLineByLegacy(ctx, ref)
			r = record{row.ID, row.TenantID, row.NotifiedAt}
		case card == notify.LineDelivered && id != nil:
			var row linenotifydb.TripForLineRow
			row, err = q.TripForLine(ctx, *id)
			r = record{row.ID, row.TenantID, row.NotifiedAt}
		case card == notify.LineDelivered:
			var row linenotifydb.TripForLineByRefRow
			row, err = q.TripForLineByRef(ctx, ref)
			r = record{row.ID, row.TenantID, row.NotifiedAt}
		case id != nil:
			var row linenotifydb.StandbyForLineRow
			row, err = q.StandbyForLine(ctx, *id)
			r = record{row.ID, row.TenantID, row.NotifiedAt}
		default:
			var row linenotifydb.StandbyForLineByLegacyRow
			row, err = q.StandbyForLineByLegacy(ctx, ref)
			r = record{row.ID, row.TenantID, row.NotifiedAt}
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		found = err == nil
		return err
	})
	return r, found, err
}

// lock reads a staff request's record FOR UPDATE in tx: PostgreSQL applies the UPDATE policies as well, so a record
// outside the caller's write reach (a carrier's staff on its contractor's trip, another tenant's) is not found.
func lock(ctx context.Context, tx pgx.Tx, card string, id uuid.UUID) (record, bool, error) {
	q := linenotifydb.New(tx)
	var r record
	var err error
	switch card {
	case notify.LineCheckin:
		var row linenotifydb.LockTaskForLineRow
		row, err = q.LockTaskForLine(ctx, id)
		r = record{row.ID, row.TenantID, row.NotifiedAt}
	case notify.LineDelivered:
		var row linenotifydb.LockTripForLineRow
		row, err = q.LockTripForLine(ctx, id)
		r = record{row.ID, row.TenantID, row.NotifiedAt}
	default:
		var row linenotifydb.LockStandbyForLineRow
		row, err = q.LockStandbyForLine(ctx, id)
		r = record{row.ID, row.TenantID, row.NotifiedAt}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return record{}, false, nil
	}
	return r, err == nil, err
}

// staffOnly refuses the principals that never send a card or revoke a link: an API key without an end user (people
// write operations rows, as operations.requireCap rules) and a cross-tenant read-only request (X-Act-On-Tenant: *).
func staffOnly(p *authz.Principal) error {
	if p.IsMachine() {
		return authz.ErrPermissionDenied("an API key does not send LINE cards or revoke evidence links", authz.OperationsEditTripDetails)
	}
	if p.RLS().ReadOnly {
		return authz.ErrPermissionDenied("a read-only cross-tenant request does not send LINE cards or revoke evidence links")
	}
	return nil
}

// Force is POST /v1/{tasks|trips|standby}/{id}/notify-line (operations:edit_trip_details): the record must be in the
// caller's write reach (else 404); the answer is 202 with the notify.line.force job, or 409 already_exists with the job
// that already waits for the same card (details.jobId). Without force the job sends the card only if the record's flag
// says it was not sent (the job then succeeds skipped, "already notified").
func (s *Service) Force(ctx context.Context, p *authz.Principal, card string, id uuid.UUID, in ForceInput) (Queued, error) {
	if err := staffOnly(p); err != nil {
		return Queued{}, err
	}
	var r record
	found := false
	if err := db.WithPrincipal(ctx, s.pool, p, func(tx pgx.Tx) error {
		var err error
		r, found, err = lock(ctx, tx, card, id)
		return err
	}); err != nil {
		return Queued{}, err
	}
	if !found {
		return Queued{}, httpx.ErrNotFound()
	}
	owner := p.UserID
	j, err := s.jobs.Submit(ctx, jobs.SubmitInput{Command: true, NewInput: jobs.NewInput{
		Type: jobs.TypeNotifyLineForce, OwnerUserID: &owner, TenantID: &r.tenant, Scope: card + ":" + r.id.String(),
		Params: notify.LineForceParams{Card: card, ID: &r.id, Force: in.Force},
	}})
	if err != nil {
		return Queued{}, err
	}
	return Queued{JobID: j.ID, Status: j.Status}, nil
}

// Shim is POST /v1/mobile/{tasks|trips|standby}/{id}/line-notify, the target of the sendCustomerLineNotification shim
// for a driver acting as itself (driver:self; never forced: staff use the force routes). {id} is the PostgreSQL id or
// the Firestore document id (a trip also its number). A record the driver reads whose card was sent answers
// {ok, skipped, reason: "already notified"} at once; otherwise the card is queued ({ok: true}) for notify.line, which
// waits for a record the mirror has not loaded yet (LineMirrorWait) and sends it only if the record is the driver's
// (the task's driver or helper; another driver's is never sent).
func (s *Service) Shim(ctx context.Context, p *authz.Principal, card, ref string, in ForceInput) (ShimResult, int, error) {
	if in.Force {
		return ShimResult{}, 0, httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "force", Reason: "not_allowed"})
	}
	if p.DriverID == nil || p.EffectiveRole() != authz.Driver {
		return ShimResult{}, 0, authz.ErrPermissionDenied("the shim target acts for a driver (driver:self)")
	}
	ref = strings.TrimSpace(ref)
	if ref == "" || len(ref) > 1500 {
		return ShimResult{}, 0, httpx.ErrNotFound()
	}
	var asID *uuid.UUID
	if u, err := uuid.Parse(ref); err == nil && u.String() == ref {
		asID = &u
	}
	r, found, err := s.read(ctx, p, card, asID, ref)
	if err == nil && !found && asID != nil {
		r, found, err = s.read(ctx, p, card, nil, ref) // a write-back mints uuid document ids (R25)
	}
	if err != nil {
		return ShimResult{}, 0, err
	}
	params := notify.LineForceParams{Card: card, DriverID: p.DriverID}
	tenant, scope := p.TenantID, card+":"+ref
	if found {
		if r.notifiedAt != nil {
			return ShimResult{OK: true, Skipped: true, Reason: notify.ReasonAlreadyNotified}, http.StatusOK, nil
		}
		params.ID, tenant, scope = &r.id, &r.tenant, card+":"+r.id.String()
	} else {
		params.Ref = ref
	}
	owner := p.UserID
	_, err = s.jobs.Submit(ctx, jobs.SubmitInput{Command: true, NewInput: jobs.NewInput{
		Type: jobs.TypeNotifyLineForce, OwnerUserID: &owner, TenantID: tenant, Scope: scope, Params: params,
	}})
	var he *httpx.Error
	if errors.As(err, &he) && he.Code == jobs.CodeAlreadyExists {
		return ShimResult{OK: true, Skipped: true, Reason: ReasonAlreadyQueued}, http.StatusOK, nil
	}
	if err != nil {
		return ShimResult{}, 0, err
	}
	return ShimResult{OK: true}, http.StatusAccepted, nil
}

// Revocation is the answer of a revocation that closed the gallery of this system but maybe not a link the legacy
// system sent: 200 {revoked: true, legacyLinkOpen: true}. A revocation that closed every link answers 204.
type Revocation struct {
	Revoked        bool `json:"revoked"`
	LegacyLinkOpen bool `json:"legacyLinkOpen"`
}

// ReasonLegacyEvidenceLink is details.reason of the 409 failed_precondition of a revocation that could not close the
// link customers hold: the record's token was sent by the legacy system while comms is not PostgreSQL-owned (P5).
const ReasonLegacyEvidenceLink = "legacy_evidence_link"

// Firestore collections the legacy gallery looks a token up in, in its order (fn:tripEvidence.ts:131-148).
var legacyGalleryCollections = []string{"trip_records", "standby_records"}

// Revoke is POST /v1/{trips|standby}/{id}/evidence/revoke (operations:edit_trip_details, R47): the record's evidence
// token stops opening the gallery (404) until the next forced LINE send mints a new one. The record is locked and
// revoked in one request transaction; a record outside the caller's write reach is 404; one without a live token is
// 204 as well (revoking twice keeps the first instant). A legacy token (Coexistence) is the legacy gallery's as well:
//   - while comms is not PostgreSQL-owned the revocation is refused, 409 failed_precondition
//     (details.reason legacy_evidence_link), and nothing changes: the legacy callable still sends the cards and the
//     mirror owns the token, so neither a PostgreSQL revocation nor a Firestore write here would close the link;
//   - once it is, with a Firestore writer, evidenceToken is removed from every Firestore document that holds it (each
//     write conditional on the version read; a failure is 503 unavailable, details.reason write_back, and nothing is
//     revoked), then the row is revoked: 204;
//   - without a writer the row is revoked (Go's gallery, and a §19 Q10 redirect of the legacy links to it, answer 404)
//     and the answer says the legacy link may still open: 200 {revoked: true, legacyLinkOpen: true}.
func (s *Service) Revoke(ctx context.Context, p *authz.Principal, kind storage.EvidenceKind, id uuid.UUID) (Revocation, error) {
	if err := staffOnly(p); err != nil {
		return Revocation{}, err
	}
	co := *s.co.Load()
	var out Revocation
	err := db.WithPrincipal(ctx, s.pool, p, func(tx pgx.Tx) error {
		token, found, err := lockEvidence(ctx, tx, kind, id)
		if err != nil {
			return err
		}
		if !found {
			return httpx.ErrNotFound()
		}
		out = Revocation{}
		if _, minted := storage.EvidenceIssuedAt(token); token != "" && !minted {
			switch {
			case !co.CommsOwned:
				return httpx.NewError(http.StatusConflict, "failed_precondition",
					"this record's photo link was sent by the legacy system while comms is not in PG_OWNED_DOMAINS: it opens "+
						"the legacy gallery, which this revocation cannot close").
					WithDetails(map[string]any{"reason": ReasonLegacyEvidenceLink})
			case co.Firestore != nil:
				if err := clearLegacyToken(ctx, co.Firestore, token); err != nil {
					return writeback.ErrUnavailable(err)
				}
			default:
				out.LegacyLinkOpen = true
			}
		}
		out.Revoked = true
		_, err = storage.RevokeEvidence(ctx, tx, kind, id, s.now())
		return err
	})
	if err != nil {
		return Revocation{}, err
	}
	return out, nil
}

// lockEvidence reads a revocation's record FOR UPDATE in tx (the caller's write reach, as lock) and its token ("" for
// none).
func lockEvidence(ctx context.Context, tx pgx.Tx, kind storage.EvidenceKind, id uuid.UUID) (string, bool, error) {
	q := linenotifydb.New(tx)
	var token *string
	var err error
	if kind == storage.EvidenceStandby {
		var row linenotifydb.LockStandbyEvidenceRow
		row, err = q.LockStandbyEvidence(ctx, id)
		token = row.EvidenceToken
	} else {
		var row linenotifydb.LockTripEvidenceRow
		row, err = q.LockTripEvidence(ctx, id)
		token = row.EvidenceToken
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil || token == nil {
		return "", err == nil, err
	}
	return *token, true, nil
}

// clearLegacyToken removes evidenceToken from the Firestore documents the legacy gallery would find token in, in one
// commit whose writes each hold only while the document keeps the version read (a document a legacy forced send
// changed meanwhile is read again; three attempts).
func clearLegacyToken(ctx context.Context, w writeback.Writer, token string) error {
	for attempt := 1; ; attempt++ {
		var writes []writeback.DocWrite
		for _, coll := range legacyGalleryCollections {
			docs, err := w.Find(ctx, coll, "evidenceToken", token)
			if err != nil {
				return err
			}
			for _, d := range docs {
				writes = append(writes, writeback.DocWrite{Collection: coll, DocID: d.ID,
					Fields: map[string]any{"evidenceToken": nil}, MustExist: true, UpdateTime: d.UpdateTime})
			}
		}
		if len(writes) == 0 {
			return nil
		}
		_, err := w.Commit(ctx, writes...)
		if errors.Is(err, writeback.ErrConflict) && attempt < 3 {
			continue
		}
		return err
	}
}

// --- routes ---------------------------------------------------------------------------------------------------------

// TaskRoutes are the task route below /v1/tasks (operations.Mounts.Tasks).
func TaskRoutes(s *Service, requireAuth fiber.Handler) func(r fiber.Router) {
	return func(r fiber.Router) {
		r = ingress.DenyRoutes(r, "P1.0")
		r.Post("/:id/notify-line", requireAuth, authz.RequireCap(authz.OperationsEditTripDetails), authz.RequireTenant(),
			force(s, notify.LineCheckin))
	}
}

// TripRoutes are the trip routes below /v1/trips (operations.Mounts.Trips).
func TripRoutes(s *Service, requireAuth fiber.Handler) func(r fiber.Router) {
	return func(r fiber.Router) {
		r = ingress.DenyRoutes(r, "P1.0")
		r.Post("/:id/notify-line", requireAuth, authz.RequireCap(authz.OperationsEditTripDetails), authz.RequireTenant(),
			force(s, notify.LineDelivered))
		r.Post("/:id/evidence/revoke", requireAuth, authz.RequireCap(authz.OperationsEditTripDetails), authz.RequireTenant(),
			revoke(s, storage.EvidenceTrip))
	}
}

// StandbyRoutes are the standby routes below /v1/standby (operations.Mounts.Standby).
func StandbyRoutes(s *Service, requireAuth fiber.Handler) func(r fiber.Router) {
	return func(r fiber.Router) {
		r = ingress.DenyRoutes(r, "P1.0")
		r.Post("/:id/notify-line", requireAuth, authz.RequireCap(authz.OperationsEditTripDetails), authz.RequireTenant(),
			force(s, notify.LineStandby))
		r.Post("/:id/evidence/revoke", requireAuth, authz.RequireCap(authz.OperationsEditTripDetails), authz.RequireTenant(),
			revoke(s, storage.EvidenceStandby))
	}
}

// MobileRoutes are the three shim targets of sendCustomerLineNotification below /v1/mobile, each behind its shim
// credential check (auth.RequireShim) and the capability of the step that sends the card.
func MobileRoutes(s *Service, requireShim fiber.Handler) func(r fiber.Router) {
	return func(r fiber.Router) {
		r = ingress.DenyRoutes(r, "P1.0")
		r.Post("/tasks/:id/line-notify", requireShim, authz.RequireCap(authz.MobileCheckin), shim(s, notify.LineCheckin))
		r.Post("/trips/:id/line-notify", requireShim, authz.RequireCap(authz.MobileSubmitTrip), shim(s, notify.LineDelivered))
		r.Post("/standby/:id/line-notify", requireShim, authz.RequireCap(authz.MobileSubmitStandby), shim(s, notify.LineStandby))
	}
}

func principal(c fiber.Ctx) (*authz.Principal, error) {
	p := authz.PrincipalFrom(c)
	if p == nil {
		return nil, httpx.ErrUnauthenticated()
	}
	return p, nil
}

func pathID(c fiber.Ctx) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, httpx.ErrNotFound()
	}
	return id, nil
}

// pathText decodes the {id} of a shim target (Fiber leaves path params percent-encoded; a Firestore id may need escaping).
func pathText(c fiber.Ctx) (string, error) {
	v, err := url.PathUnescape(c.Params("id"))
	if err != nil || strings.TrimSpace(v) == "" {
		return "", httpx.ErrNotFound()
	}
	return v, nil
}

func noQuery(c fiber.Ctx) error {
	if len(c.Queries()) > 0 {
		return httpx.ErrBadRequest("this route takes no query parameter")
	}
	return nil
}

func force(s *Service, card string) fiber.Handler {
	return func(c fiber.Ctx) error {
		p, err := principal(c)
		if err != nil {
			return err
		}
		id, err := pathID(c)
		if err != nil {
			return err
		}
		if err := noQuery(c); err != nil {
			return err
		}
		var in ForceInput
		if err := httpx.DecodeJSON(c, &in); err != nil {
			return err
		}
		out, err := s.Force(c.Context(), p, card, id, in)
		if err != nil {
			return err
		}
		return httpx.JSON(c, http.StatusAccepted, out)
	}
}

func shim(s *Service, card string) fiber.Handler {
	return func(c fiber.Ctx) error {
		p, err := principal(c)
		if err != nil {
			return err
		}
		if err := noQuery(c); err != nil {
			return err
		}
		ref, err := pathText(c)
		if err != nil {
			return err
		}
		var in ForceInput
		if err := httpx.DecodeJSON(c, &in); err != nil {
			return err
		}
		out, status, err := s.Shim(c.Context(), p, card, ref, in)
		if err != nil {
			return err
		}
		return httpx.JSON(c, status, out)
	}
}

func revoke(s *Service, kind storage.EvidenceKind) fiber.Handler {
	return func(c fiber.Ctx) error {
		p, err := principal(c)
		if err != nil {
			return err
		}
		id, err := pathID(c)
		if err != nil {
			return err
		}
		if err := noQuery(c); err != nil {
			return err
		}
		if len(strings.TrimSpace(string(c.Body()))) > 0 {
			var none struct{}
			if err := httpx.DecodeJSON(c, &none); err != nil {
				return err
			}
		}
		out, err := s.Revoke(c.Context(), p, kind, id)
		if err != nil {
			return err
		}
		if out.LegacyLinkOpen {
			return httpx.JSON(c, http.StatusOK, out)
		}
		return c.SendStatus(http.StatusNoContent)
	}
}
