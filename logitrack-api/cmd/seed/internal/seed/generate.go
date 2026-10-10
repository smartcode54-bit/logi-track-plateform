package seed

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Generators of the demo and load profiles. Every generated id is uuid v5 of the row's natural key in the seed
// namespace, like the fixture's, and every generated value comes from the generator's own PCG stream, so two
// runs write the same rows (invariant 12). T16 ships the identity and tenancy generator the owner asked for
// (multi-tenancy visible at once, issue #36); the volume generators of §D.1.2 join with T26, T35 and T43.

// persona is one generated user: a membership in a tenant, scopes, or a platform role.
type persona struct {
	email, name   string
	tenantSym     string // membership tenant ("" = none)
	role          string // membership role
	scopeKind     string // user_scopes kind ("" = none)
	scopePartySym string
	scopeCode     string // party code in the user_scopes natural key
	platform      string // user_platform_roles role ("" = none)
}

// demoPersonas complete the smoke identities so that every tenant shows its own principals (owner
// addition to issue #36): the own fleet and the two carriers (NWR works for the own fleet through
// contractor_tenant_id, TTP does not) each get a tenant_admin, staff and a dispatcher; customer-scope users
// cover every customer party with trips; platform support joins platform_admin. With the smoke users
// (wrt.admin, wrt.manager, wrt.ops, nwr.admin, ttp.dispatch, cjsf.viewer, the broker driver D6 moved from
// NWR to TTP, R24) that makes the matrix of Appendix D §D.1.2.
var demoPersonas = []persona{
	{email: "wrt.operator@logitrack.test", name: "WRT Operator (demo)", tenantSym: "TN_OWN", role: "operator"},
	{email: "wrt.dispatch@logitrack.test", name: "WRT Dispatcher (demo)", tenantSym: "TN_OWN", role: "operation_staff",
		scopeKind: "dispatcher", scopePartySym: "BP_SPX", scopeCode: "SPX"},
	{email: "nwr.manager@logitrack.test", name: "NWR Manager (demo)", tenantSym: "TN_NWR", role: "manager"},
	{email: "nwr.ops@logitrack.test", name: "NWR Operation Staff (demo)", tenantSym: "TN_NWR", role: "operation_staff"},
	{email: "nwr.operator@logitrack.test", name: "NWR Operator (demo)", tenantSym: "TN_NWR", role: "operator"},
	{email: "nwr.dispatch@logitrack.test", name: "NWR Dispatcher (demo)", tenantSym: "TN_NWR", role: "operation_staff",
		scopeKind: "dispatcher", scopePartySym: "BP_SPK", scopeCode: "SPK"},
	{email: "ttp.admin@logitrack.test", name: "TTP Tenant Admin (demo)", tenantSym: "TN_TTP", role: "tenant_admin"},
	{email: "ttp.ops@logitrack.test", name: "TTP Operation Staff (demo)", tenantSym: "TN_TTP", role: "operation_staff"},
	{email: "spx.viewer@logitrack.test", name: "SPX Viewer (demo)", scopeKind: "customer", scopePartySym: "BP_SPX", scopeCode: "SPX"},
	{email: "spk.viewer@logitrack.test", name: "SPK Viewer (demo)", scopeKind: "customer", scopePartySym: "BP_SPK", scopeCode: "SPK"},
	{email: "platform.support@logitrack.test", name: "Platform Support (demo)", platform: "support"},
}

// thaiFamily are family names for generated display names (synthetic, §D.1.9).
var thaiFamily = []string{"ใจดี", "ทองดี", "ศรีสุข", "บุญมา", "ดวงดี", "พงษ์พันธ์", "แสงทอง", "มีสุข", "รักไทย", "สายใจ"}

// ts formats an instant as the fixture does (+07:00).
func ts(t time.Time) string { return t.Format(time.RFC3339) }

// addDemo appends the demo identities.
func addDemo(p *Plan, o Options) {
	created := ts(o.Anchor.AddDate(0, 0, -90).Add(9 * time.Hour))
	r := rng(o, "demo-identities")
	hash := passwordValue(p)
	grantedBy := p.Symbols.MustID("U_PLAT").String()
	createdBy := p.Symbols.MustID("U_WRT_ADMIN").String()
	for _, ps := range demoPersonas {
		uid := ID(o.Namespace, "users", ps.email)
		name := ps.name
		if ps.tenantSym != "" {
			name += " " + thaiFamily[r.IntN(len(thaiFamily))]
		}
		p.Tables["users"] = append(p.Tables["users"], Row{
			"id": uid.String(), "email": ps.email, "email_verified": true, "display_name": name,
			"password_hash": hash, "status": "active", "auth_version": json.Number("1"), "created_at": created,
		})
		if ps.tenantSym != "" {
			p.Tables["memberships"] = append(p.Tables["memberships"], Row{
				"user_id": uid.String(), "tenant_id": p.Symbols.MustID(ps.tenantSym).String(), "role": ps.role,
				"status": "active", "created_by": createdBy, "created_at": created,
			})
		}
		if ps.scopeKind != "" {
			p.Tables["user_scopes"] = append(p.Tables["user_scopes"], Row{
				"id":      ID(o.Namespace, "user_scopes", ps.email+":"+ps.scopeKind+":"+ps.scopeCode).String(),
				"user_id": uid.String(), "kind": ps.scopeKind, "billing_party_id": p.Symbols.MustID(ps.scopePartySym).String(),
				"created_by": grantedBy, "created_at": created,
			})
		}
		if ps.platform != "" {
			p.Tables["user_platform_roles"] = append(p.Tables["user_platform_roles"], Row{
				"user_id": uid.String(), "role": ps.platform, "granted_by": grantedBy, "granted_at": created,
			})
		}
	}
}

// Load-profile scale: carriers beyond the two of smoke, and the users each one gets, so that the user
// count passes the legacy listUsers(1000) cap (keyset pagination, Appendix C §C.7 item 21).
const (
	loadCarriers         = 8
	loadUsersPerCarrier  = 147
	loadManagersPerCarry = 2
)

// addLoad appends the load-scale tenants and identities: eight more carriers (every other one works for the
// own fleet, R60), each with a tenant_admin, managers and operators.
func addLoad(p *Plan, o Options) {
	created := ts(o.Anchor.AddDate(-1, 0, 0).Add(9 * time.Hour))
	r := rng(o, "load-identities")
	hash := passwordValue(p)
	own := p.Symbols.MustID("TN_OWN").String()
	for c := 1; c <= loadCarriers; c++ {
		code := fmt.Sprintf("L%02d", c)
		tid := ID(o.Namespace, "tenants", code)
		t := Row{
			"id": tid.String(), "kind": "carrier", "code": code, "legal_type": "company",
			"name_th": fmt.Sprintf("บจก. ขนส่งทดสอบ %s (ตัวอย่าง)", code), "name_en": fmt.Sprintf("Load Carrier %s (sample)", code),
			"tax_id": thaiID(fmt.Sprintf("0999900001%02d", c)), "phone": fmt.Sprintf("000-000-0%03d", 200+c),
			"fleet_size": json.Number(fmt.Sprint(5 + r.IntN(40))), "status": "active", "created_at": created,
		}
		if c%2 == 0 {
			t["contractor_tenant_id"] = own
		}
		p.Tables["tenants"] = append(p.Tables["tenants"], t)
		for u := 0; u < loadUsersPerCarrier; u++ {
			role, local := "operator", fmt.Sprintf("%s.user%03d", strings.ToLower(code), u)
			switch {
			case u == 0:
				role, local = "tenant_admin", strings.ToLower(code)+".admin"
			case u <= loadManagersPerCarry:
				role, local = "manager", fmt.Sprintf("%s.manager%d", strings.ToLower(code), u)
			}
			email := local + "@logitrack.test"
			uid := ID(o.Namespace, "users", email)
			p.Tables["users"] = append(p.Tables["users"], Row{
				"id": uid.String(), "email": email, "email_verified": true,
				"display_name":  fmt.Sprintf("%s %s %s", code, role, thaiFamily[r.IntN(len(thaiFamily))]),
				"password_hash": hash, "status": "active", "auth_version": json.Number("1"), "created_at": created,
			})
			p.Tables["memberships"] = append(p.Tables["memberships"], Row{
				"user_id": uid.String(), "tenant_id": tid.String(), "role": role, "status": "active", "created_at": created,
			})
		}
	}
}

// passwordValue is the Argon2id hash every generated password user shares (that of SEED_DEFAULT_PASSWORD),
// taken from the fixture so the hash is computed once.
func passwordValue(p *Plan) any {
	for _, r := range p.Tables["users"] {
		if r.Str("email") == "wrt.admin@logitrack.test" {
			return r["password_hash"]
		}
	}
	return unresolved
}

// thaiID completes 12 digits with the Thai mod-11 check digit (§D.1.9: synthetic, validator-clean).
func thaiID(twelve string) string {
	sum := 0
	for i, c := range twelve {
		sum += int(c-'0') * (13 - i)
	}
	return twelve + fmt.Sprint((11-sum%11)%10)
}
