package etl

import (
	"cmp"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security"
)

// `etl auth-rbac` (T51, Appendix C §C.2.6): the permissions_config/{role} documents of a dump become override rows
// of the own-fleet tenant in role_capability_overrides. Every key of every document is translated to the colon
// catalog (matrix row ids, underscore code keys and colon keys alike); a key the translation does not know goes to
// etl.quarantine with reason unknown_capability_key. Only the manager, operation_staff, operator and driver
// documents produce rows (admin and partner are tenant_admin, which is never overridden; customer is a fixed scope
// set), and a row is written only where the saved boolean differs from the new default: the owner signs off
// migration_rbac_report.csv before the Security Center cut-over. The documents stay pending in etl.source_docs
// (`load` never maps them), as the users documents do for auth-import.

// PermissionsConfigCollection is the legacy collection of the role matrix.
const PermissionsConfigCollection = "permissions_config"

// Actions of migration_rbac_report.csv.
const (
	RBACOverride       = "override"               // saved differs from the new default: an own-fleet override row
	RBACSameAsDefault  = "same_as_default"        // saved equals the new default: no row (an earlier one is removed)
	RBACNotOverridable = "not_overridable"        // tenant_admin (admin, partner), users:assign_role, the other role class
	RBACScopeFixed     = "scope_fixed"            // the customer document: the customer scope set is fixed
	RBACDropped        = "dropped"                // delete_records; chat rows of customer and partner
	RBACUnknownKey     = "unknown_capability_key" // not in the translation: etl.quarantine
	RBACUnknownRole    = "unknown_role"           // a document whose id is no legacy role: report only
	RBACNotBoolean     = "not_boolean"            // the saved value is not a boolean: report only
)

// legacyRoles maps the permissions_config document ids to tenant roles (Appendix C §C.2.6, §C.5.5: partner is the
// carrier's tenant_admin, D2). customer is the scope (fixed set).
var legacyRoles = map[string]authz.TenantRole{
	"manager": authz.Manager, "operation_staff": authz.OperationStaff, "operator": authz.Operator, "driver": authz.Driver,
	"admin": authz.TenantAdmin, "partner": authz.TenantAdmin,
}

// legacyCustomerRole is the document of the customer scope.
const legacyCustomerRole = "customer"

// translateCapability maps one legacy key of the document of legacyRole to its canonical key (Appendix C §C.2.6). A
// colon key is read as its underscore twin first (security:manage_users is security_manage_users), so the matrix
// row ids, the code keys of lib/capabilities.ts and colon keys from an older writer all take the same path. action
// is "" for a translated key, else RBACDropped or RBACUnknownKey.
func translateCapability(legacyRole, key string) (authz.Cap, string) {
	k := strings.Replace(strings.ToLower(strings.TrimSpace(key)), ":", "_", 1)
	driver := legacyRoles[legacyRole] == authz.Driver
	switch k {
	case "create_truck":
		return authz.FleetCreateTruck, ""
	case "edit_telemetry":
		return authz.FleetEditTruck, ""
	case "view_live_map":
		return authz.FleetViewLiveMap, ""
	case "assign_driver":
		return authz.FleetManageAssignments, ""
	case "maintenance_logs":
		if driver {
			return authz.MobileSubmitMaintenance, ""
		}
		return authz.FleetManageMaintenance, ""
	case "delete_records":
		return "", RBACDropped
	case "view_assigned_tasks":
		return authz.MobileViewTasks, ""
	case "checkin_task":
		return authz.MobileCheckin, ""
	case "create_trip_record", "update_trip_record", "submit_delivery":
		return authz.MobileSubmitTrip, ""
	case "send_chat":
		return authz.MobileChat, ""
	case "read_broadcasts":
		return authz.MobileBroadcasts, ""
	case "report_incident":
		return authz.MobileReportIncident, ""
	case "submit_vehicle_expense":
		return authz.MobileSubmitExpense, ""
	case "users_create", "users_edit", "security_manage_users":
		return authz.UsersManage, ""
	case "chat_view", "chat_send":
		switch {
		case legacyRole == legacyCustomerRole || legacyRole == "partner":
			return "", RBACDropped // no customer or partner chat UI exists
		case driver:
			return authz.MobileChat, ""
		case k == "chat_view":
			return authz.ChatView, ""
		default:
			return authz.ChatSend, ""
		}
	}
	// Every other key: the underscore after the module becomes a colon (accounting_view_fuel ->
	// accounting:view_fuel); users_view, users_assign_role and broadcasts_* take this path too.
	if module, action, ok := strings.Cut(k, "_"); ok {
		if c := authz.Cap(module + ":" + action); authz.Known(c) {
			return c, ""
		}
	}
	return "", RBACUnknownKey
}

// RBACLine is one line of migration_rbac_report.csv: one key of one document.
type RBACLine struct {
	LegacyRole   string // the document id
	Role         string // the tenant role ("" for customer and unknown documents)
	LegacyKey    string
	CanonicalKey string
	Saved        *bool
	NewDefault   *bool
	Action       string
}

// RBACChange is one override row the run writes or removes (from/to null = no row).
type RBACChange struct {
	Role       string `json:"role"`
	Capability string `json:"capability"`
	From       *bool  `json:"from"`
	To         *bool  `json:"to"`
}

// RBACReport is the result of auth-rbac.
type RBACReport struct {
	Lines   []RBACLine
	Changes []RBACChange
	DryRun  bool
}

// Counts are the report lines per action.
func (r *RBACReport) Counts() map[string]int {
	out := map[string]int{}
	for _, l := range r.Lines {
		out[l.Action]++
	}
	return out
}

// WriteCSV writes migration_rbac_report.csv (Appendix C §C.2.6: role, legacy key, canonical key, saved, new
// default, action; plus the document id).
func (r *RBACReport) WriteCSV(w io.Writer) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"legacy_role", "role", "legacy_key", "canonical_key", "saved", "new_default", "action"}); err != nil {
		return err
	}
	b := func(v *bool) string {
		if v == nil {
			return ""
		}
		return strconv.FormatBool(*v)
	}
	for _, l := range r.Lines {
		if err := cw.Write([]string{csvSafe(l.LegacyRole), l.Role, csvSafe(l.LegacyKey), l.CanonicalKey, b(l.Saved), b(l.NewDefault), l.Action}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// rbacDoc is the translation of one permissions_config document.
type rbacDoc struct {
	path     string
	lines    []RBACLine
	findings []Finding
	// role and keys: the canonical keys of an overridable role (manager, operation_staff, operator, driver).
	role authz.TenantRole
	keys map[authz.Cap]bool // canonical key -> saved (OR of the legacy keys that map to it)
}

// planPermissionsConfig translates the documents without touching the database: the report lines, the findings
// per document and the saved value per canonical key of every overridable role.
func planPermissionsConfig(docs []dump.Doc) []rbacDoc {
	out := make([]rbacDoc, 0, len(docs))
	for _, d := range docs {
		legacy := strings.ToLower(strings.TrimSpace(d.ID))
		role, known := legacyRoles[legacy]
		rd := rbacDoc{path: d.Path, role: role, keys: map[authz.Cap]bool{}}
		caps, _ := d.Fields["capabilities"].(map[string]any)
		keys := make([]string, 0, len(caps))
		for k := range caps {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		type pending struct {
			line int
			key  authz.Cap
		}
		var resolved []pending
		for _, k := range keys {
			v := caps[k]
			line := RBACLine{LegacyRole: legacy, LegacyKey: k}
			if known {
				line.Role = string(role)
			}
			saved, isBool := v.(bool)
			if isBool {
				line.Saved = &saved
			}
			c, action := translateCapability(legacy, k)
			switch {
			case action == RBACUnknownKey:
				line.Action = RBACUnknownKey
				rd.findings = append(rd.findings, Finding{Field: "capabilities." + k, Reason: ReasonUnknownCapabilityKey,
					Detail: fmt.Sprintf("%s is no key of the capability catalog and has no translation (Appendix C §C.2.6)", k), Raw: v})
			case action != "":
				line.Action = action
			case !isBool:
				line.CanonicalKey, line.Action = string(c), RBACNotBoolean
			case !known && legacy == legacyCustomerRole:
				line.CanonicalKey, line.Action = string(c), RBACScopeFixed
			case !known:
				line.CanonicalKey, line.Action = string(c), RBACUnknownRole
			default:
				line.CanonicalKey = string(c)
				def := authz.TenantDefaults(role).Has(c)
				line.NewDefault = &def
				if role == authz.TenantAdmin || !authz.Overridable(role, c, true) {
					line.Action = RBACNotOverridable
					break
				}
				rd.keys[c] = rd.keys[c] || saved
				resolved = append(resolved, pending{len(rd.lines), c})
			}
			rd.lines = append(rd.lines, line)
		}
		// The action of a translated key is decided by the OR of every legacy key that maps to it.
		for _, p := range resolved {
			if rd.keys[p.key] == *rd.lines[p.line].NewDefault {
				rd.lines[p.line].Action = RBACSameAsDefault
			} else {
				rd.lines[p.line].Action = RBACOverride
			}
		}
		if !known && legacy != legacyCustomerRole && len(keys) == 0 {
			rd.lines = append(rd.lines, RBACLine{LegacyRole: legacy, Action: RBACUnknownRole})
		}
		out = append(out, rd)
	}
	return out
}

// AuthRBACOptions select what one auth-rbac run does.
type AuthRBACOptions struct {
	Dump *dump.Dump
	// ActorEmail is the users row recorded as updated_by and as the actor of role_matrix_saved (the operator, a
	// platform admin): role_capability_overrides.updated_by is NOT NULL.
	ActorEmail string
	DryRun     bool
}

// AuthRBAC applies the permissions_config documents of o.Dump to the own-fleet tenant in one transaction (a dry run
// rolls it back after the report): the override rows (upserted where the saved value differs from the default,
// removed where it now equals it), the unknown_capability_key findings per document, and one role_matrix_saved with
// the diff when a row changed. The own-fleet tenant must exist (seed or `etl load` creates it). rbac:ver is not
// incremented (cmd/etl has no Redis): the api's cached role sets refresh within their 10-minute TTL.
func (e *Engine) AuthRBAC(ctx context.Context, o AuthRBACOptions) (*RBACReport, error) {
	if o.Dump == nil {
		return nil, errors.New("etl: auth-rbac needs a dump")
	}
	if _, ok := o.Dump.Collection(PermissionsConfigCollection); !ok {
		return nil, fmt.Errorf("etl: the dump has no collection %q", PermissionsConfigCollection)
	}
	docs, err := o.Dump.Read(PermissionsConfigCollection)
	if err != nil {
		return nil, err
	}
	plan := planPermissionsConfig(docs)
	rep := &RBACReport{DryRun: o.DryRun}
	for _, d := range plan {
		rep.Lines = append(rep.Lines, d.lines...)
	}
	own := e.cfg.OwnFleetTenantID
	err = e.cfg.Tx(ctx, func(tx pgx.Tx) error {
		// The lock of the own fleet's layer, as PUT /v1/roles/matrix takes it (internal/iam LockOverrideLayer).
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('lt-rco:' || $1::uuid::text))`, own); err != nil {
			return fmt.Errorf("etl: lock the own fleet's overrides: %w", err)
		}
		var id uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id FROM tenants WHERE kind = 'own_fleet'`).Scan(&id)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return &AbortError{Detail: "no own-fleet tenant yet: run seed or `etl load` first"}
		case err != nil:
			return fmt.Errorf("etl: read the own-fleet tenant: %w", err)
		case id != own:
			return &AbortError{Detail: "the database's own-fleet tenant differs from OWN_FLEET_TENANT_ID"}
		}
		var actor uuid.UUID
		err = tx.QueryRow(ctx, `SELECT id FROM users WHERE email = $1::citext AND status <> 'deleted'`, o.ActorEmail).Scan(&actor)
		if errors.Is(err, pgx.ErrNoRows) {
			return &AbortError{Detail: "the actor (--actor, else the first PLATFORM_ADMIN_EMAILS address) has no users row"}
		}
		if err != nil {
			return fmt.Errorf("etl: read the actor: %w", err)
		}
		before := map[[2]string]bool{}
		rows, err := tx.Query(ctx, `SELECT role, capability, allowed FROM role_capability_overrides WHERE tenant_id = $1`, own)
		if err != nil {
			return fmt.Errorf("etl: read the own fleet's overrides: %w", err)
		}
		for rows.Next() {
			var role, capability string
			var allowed bool
			if err := rows.Scan(&role, &capability, &allowed); err != nil {
				rows.Close()
				return err
			}
			before[[2]string{role, capability}] = allowed
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, d := range plan {
			for c, saved := range d.keys {
				k := [2]string{string(d.role), string(c)}
				var from, to *bool
				if v, ok := before[k]; ok {
					from = &v
				}
				if saved != authz.TenantDefaults(d.role).Has(c) {
					v := saved
					to = &v
				}
				if (from == nil) == (to == nil) && (from == nil || *from == *to) {
					continue
				}
				rep.Changes = append(rep.Changes, RBACChange{Role: k[0], Capability: k[1], From: from, To: to})
			}
			if err := writeFindings(ctx, tx, PermissionsConfigCollection, d.path, d.findings); err != nil {
				return err
			}
		}
		slices.SortFunc(rep.Changes, func(a, b RBACChange) int {
			return cmp.Or(cmp.Compare(a.Role, b.Role), cmp.Compare(a.Capability, b.Capability))
		})
		for _, ch := range rep.Changes {
			if ch.To == nil {
				_, err = tx.Exec(ctx, `DELETE FROM role_capability_overrides WHERE tenant_id = $1 AND role = $2 AND capability = $3`,
					own, ch.Role, ch.Capability)
			} else {
				_, err = tx.Exec(ctx, `INSERT INTO role_capability_overrides (tenant_id, role, capability, allowed, updated_by)
					VALUES ($1, $2, $3, $4, $5)
					ON CONFLICT ON CONSTRAINT rco_key DO UPDATE SET allowed = EXCLUDED.allowed, updated_by = EXCLUDED.updated_by`,
					own, ch.Role, ch.Capability, *ch.To, actor)
			}
			if err != nil {
				return fmt.Errorf("etl: override %s %s: %w", ch.Role, ch.Capability, err)
			}
		}
		if len(rep.Changes) > 0 {
			ownID := own
			if err := security.Append(ctx, tx, security.Event{
				EventType: "role_matrix_saved", Severity: security.SeverityWarning,
				Summary:     fmt.Sprintf("role matrix imported from permissions_config: %d override row(s) changed", len(rep.Changes)),
				ActorUserID: &actor, TenantID: &ownID, OccurredAt: e.cfg.Now().UTC().Truncate(time.Microsecond),
				Details: map[string]any{"layer": "tenant", "tenantId": own, "source": "etl auth-rbac", "changes": rep.Changes},
			}); err != nil {
				return err
			}
		}
		if o.DryRun {
			return errDryRun
		}
		return nil
	})
	if errors.Is(err, errDryRun) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	return rep, nil
}
