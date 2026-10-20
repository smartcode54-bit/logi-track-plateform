package jobs

import (
	"encoding/json"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs/jobsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"strconv"
	"time"
)

// Only tenant-attributed Fleet work is admitted. All existing document/report/
// billing/operations jobs are deferred; a ready result does not change its source.
var supportTypes = []string{TypeCartrackSync}

func supportType(t string) bool { return t == TypeCartrackSync }

type supportJob struct {
	ID         uuid.UUID       `json:"id"`
	Type       string          `json:"type"`
	Status     string          `json:"status"`
	Progress   *Progress       `json:"progress"`
	Params     json.RawMessage `json:"params"`
	Result     json.RawMessage `json:"result"`
	Error      *string         `json:"error"`
	CreatedAt  time.Time       `json:"createdAt"`
	StartedAt  *time.Time      `json:"startedAt"`
	FinishedAt *time.Time      `json:"finishedAt"`
}

var progressSchema = httpx.NewSchema[Progress]("jobs.progress", map[string]httpx.Field{"done": {Kind: httpx.Safe}, "total": {Kind: httpx.Safe}})
var _ = httpx.NewSchema[supportJob]("jobs.phase1", map[string]httpx.Field{
	"id": {Kind: httpx.Safe}, "type": {Kind: httpx.Safe}, "status": {Kind: httpx.Safe}, "progress": {Kind: httpx.Nested, Schema: progressSchema},
	"params": {Kind: httpx.Secret}, "result": {Kind: httpx.Secret}, "error": {Kind: httpx.Secret},
	"createdAt": {Kind: httpx.Safe}, "startedAt": {Kind: httpx.Safe}, "finishedAt": {Kind: httpx.Safe},
})

func jobPolicy(route string) httpx.EndpointPolicy {
	for _, p := range authz.SupportPolicySeed() {
		if p.Method == "GET" && p.Route == route {
			return httpx.TenantReadPolicy(p, "jobs.phase1", "", true, "type", "cursor", "limit")
		}
	}
	panic("jobs: missing seed")
}
func projectJob(j Job) supportJob {
	out := supportJob{ID: j.ID, Type: j.Type, Status: j.Status, CreatedAt: j.CreatedAt, StartedAt: j.StartedAt, FinishedAt: j.FinishedAt}
	// Arbitrary progress JSON may contain private keys or non-count values.
	var p struct {
		Done  *int `json:"done"`
		Total *int `json:"total"`
	}
	if json.Unmarshal(j.Progress, &p) == nil && p.Done != nil && p.Total != nil && *p.Done >= 0 && *p.Total >= 0 && *p.Done <= *p.Total && *p.Total <= 1_000_000_000 {
		out.Progress = &Progress{Done: *p.Done, Total: *p.Total}
	}
	return out
}
func (h handlers) supportList(c fiber.Ctx, cl Caller) error {
	if cl.TenantID == nil {
		return httpx.ErrSupportViewUnavailable()
	}
	typ := c.Query("type")
	if typ != "" && !supportType(typ) {
		return httpx.ErrSupportViewUnavailable()
	}
	cur, err := httpx.SupportCursor(c)
	if err != nil {
		return err
	}
	p := jobsdb.ListJobsParams{ReaderTenantID: cl.TenantID, ReaderTypes: supportTypes, RestrictTenant: true, RowLimit: DefaultLimit + 1}
	if typ != "" {
		p.JobType = &typ
	}
	if value := c.Query("limit"); value != "" {
		n, parseErr := strconv.Atoi(value)
		if parseErr != nil {
			return errInvalid("limit", "out_of_range")
		}
		if n < 1 || n > 100 {
			return errInvalid("limit", "out_of_range")
		}
		p.RowLimit = int32(n) + 1
	}
	if cur != "" {
		pos, ok := decodeCursor(cur)
		if !ok {
			return errInvalid("cursor", "invalid")
		}
		p.BeforeCreatedAt, p.BeforeID = &pos.CreatedAt, &pos.ID
	}
	// No OwnerUserID: own foreign/global jobs cannot escape the exact tenant predicate.
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
	out := make([]supportJob, len(rows))
	for i, row := range rows {
		out[i] = projectJob(fromRow(row))
	}
	return httpx.SupportPage(c, "jobs.phase1", out, next, nil)
}

// SupportInvalidation admits only the selected tenant's known Fleet job family.
// The event payload itself is discarded by SSE; its values never become a frame.
func SupportInvalidation(raw json.RawMessage, tenant string) bool {
	var source struct {
		Type     string     `json:"type"`
		TenantID *uuid.UUID `json:"tenantId"`
	}
	return json.Unmarshal(raw, &source) == nil && supportType(source.Type) && source.TenantID != nil && source.TenantID.String() == tenant
}
