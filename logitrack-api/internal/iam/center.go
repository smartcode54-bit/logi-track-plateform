package iam

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/iamdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security"
)

// The Security Center read model of T51 (Appendix B §B.2.5, §B.2.6): GET /v1/security-events (the audit page and
// the overview's recent events), GET /v1/security/overview (the overview cards and the status page) and
// GET /v1/stats/users-by-role (the RBAC distribution). Every route needs a tenant context (authz.RequireTenant): a
// tenant principal reads its tenant, a platform principal reads everything through X-Act-On-Tenant: * (one
// platform_cross_tenant_access row per request, C.3.9) or one tenant through X-Act-On-Tenant: <uuid>.

// SchedulerLockName is the advisory lock of the scheduler leader (internal/scheduler LockName); the status section
// reports whether a session holds it.
const SchedulerLockName = "lt-scheduler"

// --- GET /v1/security-events -----------------------------------------------------------------------------

// EventsInput are the query parameters of GET /v1/security-events.
type EventsInput struct {
	Type     string // a type or a comma list of up to 20 (snake_case)
	Severity string
	From, To string // RFC 3339 instants: created_at >= from, < to
	Limit    int
	Cursor   string
}

// maxEventTypes bounds ?type.
const maxEventTypes = 20

// EventsMeta is the meta of GET /v1/security-events: the type catalogue for the audit page's filter.
type EventsMeta struct {
	Types []string `json:"types"`
}

// ListEvents is GET /v1/security-events (security:view_audit): a keyset page, newest first, read under the
// principal so RLS decides the rows (security.List).
func (a *Admin) ListEvents(c call, in EventsInput) ([]security.Row, string, error) {
	var bad []httpx.FieldViolation
	f := security.Filter{Limit: in.Limit}
	if s := strings.TrimSpace(in.Type); s != "" {
		for _, t := range strings.Split(s, ",") {
			t = strings.TrimSpace(t)
			if !security.ValidType(t) {
				bad = append(bad, violation("type", "invalid"))
				break
			}
			if !slices.Contains(f.Types, t) {
				f.Types = append(f.Types, t)
			}
		}
		if len(f.Types) > maxEventTypes {
			bad = append(bad, httpx.FieldViolation{Field: "type", Reason: "too_many", Params: map[string]any{"max": maxEventTypes}})
		}
	}
	if in.Severity != "" {
		if !security.ValidSeverity(in.Severity) {
			bad = append(bad, violation("severity", "invalid"))
		}
		f.Severity = in.Severity
	}
	parse := func(field, v string) *time.Time {
		if v == "" {
			return nil
		}
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			bad = append(bad, violation(field, "invalid"))
			return nil
		}
		return &t
	}
	f.From, f.To = parse("from", in.From), parse("to", in.To)
	if f.From != nil && f.To != nil && !f.From.Before(*f.To) {
		bad = append(bad, violation("to", "before_from"))
	}
	if f.Limit == 0 {
		f.Limit = DefaultLimit
	}
	if f.Limit < 1 || f.Limit > MaxLimit {
		bad = append(bad, violation("limit", "out_of_range"))
	}
	if in.Cursor != "" {
		cur, ok := security.DecodeCursor(in.Cursor)
		if !ok {
			bad = append(bad, violation("cursor", "invalid"))
		}
		f.After = &cur
	}
	if len(bad) > 0 {
		return nil, "", errInvalid(bad...)
	}
	var rows []security.Row
	var next string
	err := db.WithPrincipal(c.ctx, a.pool, c.p, func(tx pgx.Tx) (err error) {
		rows, next, err = security.List(c.ctx, tx, f)
		return err
	})
	if err != nil {
		return nil, "", err
	}
	return rows, next, nil
}

// --- GET /v1/security/overview ---------------------------------------------------------------------------

// Overview is GET /v1/security/overview (security:view_overview): the counters of the Security Center overview
// (Appendix B §B.2.6: users by status, failed logins 24 h, revoked sessions, outdated installs, open quarantine
// rows) and the status page. A section the caller may not see is null: installations without
// security:view_mobile_clients, quarantine outside X-Act-On-Tenant: * (platform:cross_tenant_read), system without
// security:view_status.
type Overview struct {
	GeneratedAt   time.Time         `json:"generatedAt"`
	Scope         OverviewScope     `json:"scope"`
	Users         UserCounts        `json:"users"`
	Sessions      SessionCounts     `json:"sessions"`
	Logins        LoginCounts       `json:"logins"`
	Events        security.Activity `json:"events"`
	Installations *InstallCounts    `json:"installations"`
	Quarantine    *QuarantineCounts `json:"quarantine"`
	System        *SystemStatus     `json:"system"`
}

// OverviewScope says what the counters cover: every tenant (X-Act-On-Tenant: *) or the tenant reach of TenantID
// (the effective tenant and, for staff, the carriers working for it).
type OverviewScope struct {
	All      bool    `json:"all"`
	TenantID *string `json:"tenantId"`
}

// UserCounts are the users in reach by status; Total leaves the deleted ones out.
type UserCounts struct {
	Total    int            `json:"total"`
	ByStatus map[string]int `json:"byStatus"` // active, disabled, reset_required, deleted
}

// SessionCounts are the live sessions of the users in reach and the sessions revoked in the last 24 hours.
type SessionCounts struct {
	Active             int            `json:"active"`
	Revoked24h         int            `json:"revoked24h"`
	RevokedByReason24h map[string]int `json:"revokedByReason24h"`
}

// LoginCounts are the failed sign-ins and lockouts of the last 24 hours (users in reach; under * every one,
// unknown emails included).
type LoginCounts struct {
	Failed24h   int `json:"failed24h"`
	Lockouts24h int `json:"lockouts24h"`
}

// InstallCounts are the driver-app installations in reach against settings('mobile_app') (ADR 0007): BelowFloor
// would be blocked by the version gate, BelowLatest run an older build than the latest release; an installation
// whose version is missing or not x.y.z counts in UnknownVersion only.
type InstallCounts struct {
	Total             int     `json:"total"`
	Seen7d            int     `json:"seen7d"`
	BelowFloor        int     `json:"belowFloor"`
	BelowLatest       int     `json:"belowLatest"`
	UnknownVersion    int     `json:"unknownVersion"`
	MinAllowedVersion *string `json:"minAllowedVersion"`
	LatestVersion     *string `json:"latestVersion"`
}

// QuarantineCounts are the rows still in the quarantine tenant (R11, C.3.10), per tenant-stamped table.
type QuarantineCounts struct {
	Rows    int            `json:"rows"`
	ByTable map[string]int `json:"byTable"` // only tables with rows
}

// SystemStatus is the status page (security:view_status, Appendix C §C.2.3 row 60): what the api can see of the
// platform from PostgreSQL and Redis. The broker's queue and DLQ depths are not here: the api does not reach the
// RabbitMQ management API (they are on /metrics and Grafana).
type SystemStatus struct {
	Database  string       `json:"database"` // ok (the overview was read from it)
	Redis     string       `json:"redis"`    // ok | down | unknown
	Scheduler SchedulerRef `json:"scheduler"`
	Outbox    OutboxStatus `json:"outbox"`
	Jobs      JobStatus    `json:"jobs"`
}

// SchedulerRef says whether a scheduler replica holds the leader lock (cron jobs, outbox relay, replays).
type SchedulerRef struct {
	LeaderPresent bool `json:"leaderPresent"`
}

// OutboxStatus is the relay backlog: unpublished outbox rows and the age of the oldest.
type OutboxStatus struct {
	Pending       int `json:"pending"`
	OldestSeconds int `json:"oldestSeconds"`
}

// JobStatus counts the jobs table: waiting and running now, finished in the last 24 hours.
type JobStatus struct {
	Queued       int `json:"queued"`
	Running      int `json:"running"`
	Failed24h    int `json:"failed24h"`
	Succeeded24h int `json:"succeeded24h"`
}

// userStatuses are listed in every byStatus map, zero included.
var overviewStatuses = []string{"active", "disabled", "reset_required", "deleted"}

// Overview is GET /v1/security/overview.
func (a *Admin) Overview(c call) (*Overview, error) {
	p := c.p
	now := a.clock()
	day := now.Add(-24 * time.Hour)
	r := readReach(p)
	o := &Overview{GeneratedAt: now, Scope: OverviewScope{All: r.all},
		Users:    UserCounts{ByStatus: map[string]int{}},
		Sessions: SessionCounts{RevokedByReason24h: map[string]int{}}}
	if t := p.EffectiveTenant(); t != nil && !r.all {
		s := t.String()
		o.Scope.TenantID = &s
	}
	for _, s := range overviewStatuses {
		o.Users.ByStatus[s] = 0
	}
	// Users, sessions and sign-in failures: identity tables, read in the system context with the reach explicit.
	err := db.WithSystem(c.ctx, a.pool, nil, func(tx pgx.Tx) error {
		q := iamdb.New(tx)
		us, err := q.OverviewUsersByStatus(c.ctx, iamdb.OverviewUsersByStatusParams{AllUsers: r.all, TenantIds: r.tenants, ScopeOnly: r.scopeOnly})
		if err != nil {
			return err
		}
		for _, u := range us {
			o.Users.ByStatus[u.Status] = int(u.Users)
			if u.Status != "deleted" {
				o.Users.Total += int(u.Users)
			}
		}
		ss, err := q.OverviewSessions(c.ctx, iamdb.OverviewSessionsParams{Now: now, Since: day, AllUsers: r.all, TenantIds: r.tenants, ScopeOnly: r.scopeOnly})
		if err != nil {
			return err
		}
		for _, s := range ss {
			if s.Reason == "" {
				o.Sessions.Active += int(s.Sessions)
				continue
			}
			o.Sessions.Revoked24h += int(s.Sessions)
			o.Sessions.RevokedByReason24h[s.Reason] += int(s.Sessions)
		}
		fl, err := q.OverviewFailedLogins(c.ctx, iamdb.OverviewFailedLoginsParams{Since: day, AllUsers: r.all, TenantIds: r.tenants, ScopeOnly: r.scopeOnly})
		if err != nil {
			return err
		}
		o.Logins = LoginCounts{Failed24h: int(fl.Failed), Lockouts24h: int(fl.Lockouts)}
		if r.all && p.Can(authz.PlatformCrossTenantRead) {
			if o.Quarantine, err = quarantineCounts(c.ctx, tx, q); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, mapDBError(err)
	}
	// Events and installations: RLS tables, read under the principal.
	err = db.WithPrincipal(c.ctx, a.pool, p, func(tx pgx.Tx) error {
		var err error
		if o.Events, err = security.ReadActivity(c.ctx, tx, now); err != nil {
			return err
		}
		if p.Can(authz.SecurityViewMobileClients) {
			if o.Installations, err = installCounts(c.ctx, iamdb.New(tx), now.Add(-7*24*time.Hour)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if p.Can(authz.SecurityViewStatus) {
		if o.System, err = a.systemStatus(c.ctx, now, day); err != nil {
			return nil, err
		}
	}
	return o, nil
}

// quarantineCounts counts the rows of the quarantine tenant in every tenant-stamped table the api may read.
func quarantineCounts(ctx context.Context, tx pgx.Tx, q *iamdb.Queries) (*QuarantineCounts, error) {
	tables, err := q.QuarantineTables(ctx)
	if err != nil {
		return nil, err
	}
	out := &QuarantineCounts{ByTable: map[string]int{}}
	if len(tables) == 0 {
		return out, nil
	}
	parts := make([]string, len(tables))
	for i, t := range tables {
		// Table names come from the catalog, quoted as identifiers; the tenant is the structural constant.
		parts[i] = fmt.Sprintf("SELECT %s::text, count(*)::int FROM public.%s WHERE tenant_id = app_quarantine_tenant_id()",
			quoteLiteral(t), pgx.Identifier{t}.Sanitize())
	}
	rows, err := tx.Query(ctx, strings.Join(parts, " UNION ALL "))
	if err != nil {
		return nil, fmt.Errorf("iam: quarantine rows: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var t string
		var n int
		if err := rows.Scan(&t, &n); err != nil {
			return nil, err
		}
		if n > 0 {
			out.ByTable[t] = n
			out.Rows += n
		}
	}
	return out, rows.Err()
}

// quoteLiteral quotes a catalog table name as an SQL string literal.
func quoteLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// installCounts counts the installations visible in q's transaction against settings('mobile_app').
func installCounts(ctx context.Context, q *iamdb.Queries, since time.Time) (*InstallCounts, error) {
	out := &InstallCounts{}
	set, err := q.MobileAppSetting(ctx)
	switch {
	case err == nil:
		if set.MinAllowedVersion != "" {
			out.MinAllowedVersion = &set.MinAllowedVersion
		}
		if set.LatestVersion != "" {
			out.LatestVersion = &set.LatestVersion
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, err
	}
	floor, hasFloor := parseVersion(set.MinAllowedVersion)
	latest, hasLatest := parseVersion(set.LatestVersion)
	vs, err := q.InstallationVersions(ctx, since)
	if err != nil {
		return nil, err
	}
	for _, v := range vs {
		n := int(v.Installs)
		out.Total += n
		out.Seen7d += int(v.Seen)
		ver, ok := parseVersion(v.AppVersion)
		if !ok {
			out.UnknownVersion += n
			continue
		}
		if hasFloor && versionLess(ver, floor) {
			out.BelowFloor += n
		}
		if hasLatest && versionLess(ver, latest) {
			out.BelowLatest += n
		}
	}
	return out, nil
}

// parseVersion reads a strict x.y.z version (the pub_semver gate refuses labels, ADR 0007); a build suffix after
// '+' is ignored.
func parseVersion(s string) ([3]int, bool) {
	var v [3]int
	s, _, _ = strings.Cut(strings.TrimSpace(s), "+")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p == "" || (len(p) > 1 && p[0] == '0') {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func versionLess(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// systemStatus reads the status page section.
func (a *Admin) systemStatus(ctx context.Context, now, since time.Time) (*SystemStatus, error) {
	s := &SystemStatus{Database: "ok", Redis: "unknown"}
	err := a.read(ctx, func(q *iamdb.Queries) error {
		ob, err := q.OutboxBacklog(ctx, now)
		if err != nil {
			return err
		}
		s.Outbox = OutboxStatus{Pending: int(ob.Pending), OldestSeconds: max(int(ob.OldestSeconds), 0)}
		jc, err := q.JobCounts(ctx, since)
		if err != nil {
			return err
		}
		s.Jobs = JobStatus{Queued: int(jc.Queued), Running: int(jc.Running), Failed24h: int(jc.Failed), Succeeded24h: int(jc.Succeeded)}
		s.Scheduler.LeaderPresent, err = q.SchedulerLeaderPresent(ctx, SchedulerLockName)
		return err
	})
	if err != nil {
		return nil, err
	}
	if a.rbac != nil {
		s.Redis = "down"
		if a.rbac.redisOK(ctx) {
			s.Redis = "ok"
		}
	}
	return s, nil
}

// --- GET /v1/stats/users-by-role -------------------------------------------------------------------------

// UsersByRole is GET /v1/stats/users-by-role (security:view_overview; replaces the seven getCountFromServer reads
// of logitrack-web/lib/fetchSecurityOverviewStats.ts): the users in reach that are not deleted, per tenant role
// (memberships in reach), scope kind and platform role, every value listed (zero included). A user with several
// roles counts once in each; Total counts every user once and None the users with no role on any axis.
type UsersByRole struct {
	Scope         OverviewScope  `json:"scope"`
	Total         int            `json:"total"`
	None          int            `json:"none"`
	TenantRoles   map[string]int `json:"tenantRoles"`
	Scopes        map[string]int `json:"scopes"`
	PlatformRoles map[string]int `json:"platformRoles"`
}

// StatsUsersByRole is GET /v1/stats/users-by-role.
func (a *Admin) StatsUsersByRole(c call) (*UsersByRole, error) {
	r := readReach(c.p)
	out := &UsersByRole{Scope: OverviewScope{All: r.all}, TenantRoles: map[string]int{}, Scopes: map[string]int{}, PlatformRoles: map[string]int{}}
	if t := c.p.EffectiveTenant(); t != nil && !r.all {
		s := t.String()
		out.Scope.TenantID = &s
	}
	for _, role := range authz.TenantRoles {
		out.TenantRoles[string(role)] = 0
	}
	for _, k := range []authz.ScopeKind{authz.ScopeCustomer, authz.ScopeDispatcher} {
		out.Scopes[string(k)] = 0
	}
	for _, role := range authz.PlatformRoles {
		out.PlatformRoles[string(role)] = 0
	}
	err := a.read(c.ctx, func(q *iamdb.Queries) error {
		rows, err := q.UsersByRole(c.ctx, iamdb.UsersByRoleParams{AllUsers: r.all, TenantIds: r.tenants, ScopeOnly: r.scopeOnly})
		if err != nil {
			return err
		}
		for _, row := range rows {
			n := int(row.Users)
			switch row.Axis {
			case "tenant":
				out.TenantRoles[row.Role] = n
			case "scope":
				out.Scopes[row.Role] = n
			case "platform":
				out.PlatformRoles[row.Role] = n
			case "total":
				out.Total = n
			case "none":
				out.None = n
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
