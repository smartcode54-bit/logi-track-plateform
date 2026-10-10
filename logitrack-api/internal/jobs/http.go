package jobs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs/jobsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security"
)

// Caller is the authenticated principal of a jobs request, as the auth middleware resolved it.
type Caller struct {
	UserID uuid.UUID
	// PlatformAdmin holds platform_admin: every job, and the queue replay (Appendix B §B.1.4 `platform`).
	PlatformAdmin bool
	// PlatformSupport holds support: every job, read only.
	PlatformSupport bool
}

func (c Caller) readsAll() bool { return c.PlatformAdmin || c.PlatformSupport }

// HTTPOptions wire the endpoints to authentication: Auth answers 401 itself when the request has no
// valid principal and stores it for Caller.
type HTTPOptions struct {
	Auth     fiber.Handler
	Caller   func(c fiber.Ctx) (Caller, bool)
	Topology mq.Topology // the work queues a replay may name
}

// Paging of GET /v1/jobs (Appendix B §B.1.5).
const (
	DefaultLimit = 50
	MaxLimit     = 500
)

// Groups returns the route groups of Appendix B §B.2.20: GET /v1/jobs, GET /v1/jobs/{id} and
// POST /v1/admin/queues/{queue}/replay, all on the internal listener.
func (s *Service) Groups(o HTTPOptions) []ingress.Group {
	if o.Topology.Queues == nil {
		o.Topology = mq.Default
	}
	h := handlers{s: s, o: o}
	return []ingress.Group{
		{Prefix: "/v1/jobs", Mount: func(r fiber.Router) {
			r.Get("", o.Auth, h.list)
			r.Get("/:id", o.Auth, h.get)
		}},
		{Prefix: "/v1/admin/queues", Mount: func(r fiber.Router) {
			r.Post("/:queue/replay", o.Auth, h.replay)
		}},
	}
}

type handlers struct {
	s *Service
	o HTTPOptions
}

func (h handlers) caller(c fiber.Ctx) (Caller, error) {
	cl, ok := h.o.Caller(c)
	if !ok {
		return Caller{}, httpx.ErrUnauthenticated()
	}
	return cl, nil
}

func errPermissionDenied() *httpx.Error {
	return httpx.NewError(http.StatusForbidden, "permission_denied", "permission denied").
		WithDetails(map[string]any{"missingCapability": "platform"})
}

func errInvalid(field, reason string) *httpx.Error {
	return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: field, Reason: reason})
}

// cursor is the opaque keyset position (created_at, id), base64url JSON.
type cursor struct {
	CreatedAt time.Time `json:"t"`
	ID        uuid.UUID `json:"id"`
}

func encodeCursor(j Job) string {
	b, _ := json.Marshal(cursor{CreatedAt: j.CreatedAt, ID: j.ID})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (cursor, bool) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return cursor{}, false
	}
	var c cursor
	if json.Unmarshal(b, &c) != nil || c.ID == uuid.Nil || c.CreatedAt.IsZero() {
		return cursor{}, false
	}
	return c, true
}

// list is GET /v1/jobs?type&cursor&limit: the caller's own jobs, every job for platform principals.
func (h handlers) list(c fiber.Ctx) error {
	cl, err := h.caller(c)
	if err != nil {
		return err
	}
	for k := range c.Queries() {
		if k != "type" && k != "cursor" && k != "limit" {
			return httpx.ErrBadRequest("unknown query parameter " + k)
		}
	}
	p := jobsdb.ListJobsParams{RowLimit: DefaultLimit + 1}
	if !cl.readsAll() {
		p.OwnerUserID = &cl.UserID
	}
	if t := c.Query("type"); t != "" {
		if !typeRx.MatchString(t) {
			return errInvalid("type", "invalid")
		}
		p.JobType = &t
	}
	if v := c.Query("limit"); v != "" {
		n := fiber.Query[int](c, "limit", -1)
		if n < 1 || n > MaxLimit {
			return errInvalid("limit", "out_of_range")
		}
		p.RowLimit = int32(n) + 1
	}
	if v := c.Query("cursor"); v != "" {
		cur, ok := decodeCursor(v)
		if !ok {
			return errInvalid("cursor", "invalid")
		}
		p.BeforeCreatedAt, p.BeforeID = &cur.CreatedAt, &cur.ID
	}
	rows, err := jobsdb.New(h.s.query).ListJobs(c.Context(), p)
	if err != nil {
		return err
	}
	limit := int(p.RowLimit) - 1
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		next = encodeCursor(fromRow(rows[limit-1]))
	}
	out := make([]Job, len(rows))
	for i, r := range rows {
		out[i] = fromRow(r)
	}
	return httpx.Page(c, out, next, nil)
}

// get is GET /v1/jobs/{id}: 404 when the job does not exist or the caller may not see it.
func (h handlers) get(c fiber.Ctx) error {
	cl, err := h.caller(c)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return httpx.ErrNotFound()
	}
	j, ok, err := Get(c.Context(), h.s.query, id)
	if err != nil {
		return err
	}
	if !ok || (!cl.readsAll() && (j.OwnerUserID == nil || *j.OwnerUserID != cl.UserID)) {
		return httpx.ErrNotFound()
	}
	return httpx.JSON(c, http.StatusOK, j)
}

// TypeQueueReplay is the job that moves {queue}.dead back to {queue}; the scheduler runs it.
const TypeQueueReplay = "queue.replay"

// ReplayParams are the params of a queue.replay job.
type ReplayParams struct {
	Queue string `json:"queue"`
}

// replay is POST /v1/admin/queues/{queue}/replay (platform_admin): it records a queue.replay job and
// the queue_replayed audit row in one transaction and answers 202 {jobId}. The api never connects to
// RabbitMQ: the leader scheduler, which holds the broker connection, moves the messages.
func (h handlers) replay(c fiber.Ctx) error {
	cl, err := h.caller(c)
	if err != nil {
		return err
	}
	if !cl.PlatformAdmin {
		return errPermissionDenied()
	}
	queue := c.Params("queue")
	if _, ok := h.o.Topology.Queue(queue); !ok {
		return httpx.ErrNotFound()
	}
	requestID := httpx.RequestIDFrom(c)
	actor := cl.UserID
	j, err := h.s.Submit(c.Context(), SubmitInput{
		NewInput: NewInput{Type: TypeQueueReplay, OwnerUserID: &actor, Params: ReplayParams{Queue: queue}, Scope: queue},
		InTx: func(ctx context.Context, tx pgx.Tx, j Job) error {
			return security.Append(ctx, tx, security.Event{
				EventType: "queue_replayed", Severity: security.SeverityWarning,
				Summary: "dead-lettered messages replayed: " + queue, Details: map[string]any{"queue": queue, "jobId": j.ID},
				ActorUserID: &actor, RequestID: requestID, OccurredAt: time.Now(),
			})
		},
	})
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusAccepted, map[string]any{"jobId": j.ID})
}
