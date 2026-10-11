package etl

import (
	"bytes"
	"strings"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
)

// Appendix C §C.9.4: every legacy key of §C.2.6 maps as listed. The 36 matrix row ids of
// logitrack-web/app/app/security-center/roles/page.tsx and the code keys of logitrack-web/lib/capabilities.ts
// (written by the Role Matrix as {key: boolean}, in either form).
func TestTranslateCapability(t *testing.T) {
	type want struct {
		key    authz.Cap
		action string
	}
	staff := map[string]want{
		// matrix row ids
		"create_truck": {authz.FleetCreateTruck, ""}, "edit_telemetry": {authz.FleetEditTruck, ""},
		"view_live_map": {authz.FleetViewLiveMap, ""}, "assign_driver": {authz.FleetManageAssignments, ""},
		"maintenance_logs": {authz.FleetManageMaintenance, ""}, "delete_records": {"", RBACDropped},
		"view_assigned_tasks": {authz.MobileViewTasks, ""}, "checkin_task": {authz.MobileCheckin, ""},
		"create_trip_record": {authz.MobileSubmitTrip, ""}, "update_trip_record": {authz.MobileSubmitTrip, ""},
		"submit_delivery": {authz.MobileSubmitTrip, ""}, "send_chat": {authz.MobileChat, ""},
		"read_broadcasts": {authz.MobileBroadcasts, ""}, "report_incident": {authz.MobileReportIncident, ""},
		"submit_vehicle_expense": {authz.MobileSubmitExpense, ""}, "users_view": {authz.UsersView, ""},
		"users_create": {authz.UsersManage, ""}, "users_edit": {authz.UsersManage, ""},
		"users_assign_role": {authz.UsersAssignRole, ""}, "chat_view": {authz.ChatView, ""}, "chat_send": {authz.ChatSend, ""},
		"broadcasts_send": {authz.BroadcastsSend, ""}, "broadcasts_view": {authz.BroadcastsView, ""},
		"accounting_view_fuel": {authz.AccountingViewFuel, ""}, "accounting_edit_fuel": {authz.AccountingEditFuel, ""},
		"accounting_view_other": {authz.AccountingViewOther, ""}, "accounting_edit_other": {authz.AccountingEditOther, ""},
		"accounting_audit_expense": {authz.AccountingAuditExpense, ""},
		"security_view_overview":   {authz.SecurityViewOverview, ""}, "security_manage_users": {authz.UsersManage, ""},
		"security_manage_roles": {authz.SecurityManageRoles, ""}, "security_view_audit": {authz.SecurityViewAudit, ""},
		"security_manage_api_keys": {authz.SecurityManageAPIKeys, ""}, "security_view_status": {authz.SecurityViewStatus, ""},
		"security_view_mobile_clients":   {authz.SecurityViewMobileClients, ""},
		"security_manage_mobile_release": {authz.SecurityManageMobileRel, ""},
		// code keys of lib/capabilities.ts not yet named above, in both forms
		"fleet_view_trucks": {authz.FleetViewTrucks, ""}, "fleet:edit_truck": {authz.FleetEditTruck, ""},
		"fleet_view_renewals": {authz.FleetViewRenewals, ""}, "fleet_manage_subcontractors": {authz.FleetManageSubcontractors, ""},
		"fleet_manage_customers": {authz.FleetManageCustomers, ""}, "fleet_view_assignments": {authz.FleetViewAssignments, ""},
		"drivers_view": {authz.DriversView, ""}, "drivers:create": {authz.DriversCreate, ""}, "drivers_edit": {authz.DriversEdit, ""},
		"operations_view_first_mile": {authz.OperationsViewFirstMile, ""}, "operations_view_line_haul": {authz.OperationsViewLineHaul, ""},
		"operations_manage_sources": {authz.OperationsManageSources, ""}, "operations_calculate_distances": {authz.OperationsCalcDistances, ""},
		"operations_view_driver_monitor": {authz.OperationsViewDriverMon, ""}, "operations_edit_trip_details": {authz.OperationsEditTripDetails, ""},
		"operations_view_incidents": {authz.OperationsViewIncidents, ""}, "operations_create_standby": {authz.OperationsCreateStandby, ""},
		"security:manage_users": {authz.UsersManage, ""}, "accounting_view_rate_card": {authz.AccountingViewRateCard, ""},
		"accounting_edit_rate_card": {authz.AccountingEditRateCard, ""}, "accounting_view_income": {authz.AccountingViewIncome, ""},
		"accounting_billing_document": {authz.AccountingBillingDocument, ""}, "accounting_billing_result": {authz.AccountingBillingResult, ""},
		"accounting_shopee_report": {authz.AccountingShopeeReport, ""}, "reporting_view_analytics": {authz.ReportingViewAnalytics, ""},
		"hr_view_payroll": {authz.HRViewPayroll, ""}, "hr_manage_payroll": {authz.HRManagePayroll, ""}, "hr_view_leave": {authz.HRViewLeave, ""},
		"hr_manage_leave": {authz.HRManageLeave, ""}, "hr_manage_holidays": {authz.HRManageHolidays, ""},
		"company_view": {authz.CompanyView, ""}, "company_manage": {authz.CompanyManage, ""}, "waitlist_view": {authz.WaitlistView, ""},
		"packages_view": {authz.PackagesView, ""}, " Fleet_View_Trucks ": {authz.FleetViewTrucks, ""},
		// unknown
		"view_secret_things": {"", RBACUnknownKey}, "fleet_fly": {"", RBACUnknownKey}, "": {"", RBACUnknownKey},
	}
	for key, w := range staff {
		if got, action := translateCapability("manager", key); got != w.key || action != w.action {
			t.Errorf("manager %q -> %q %q, want %q %q", key, got, action, w.key, w.action)
		}
	}
	// Per-role rows: the driver column of maintenance_logs and chat_*, and chat for customer and partner.
	for _, c := range []struct {
		role, key string
		want      authz.Cap
		action    string
	}{
		{"driver", "maintenance_logs", authz.MobileSubmitMaintenance, ""},
		{"driver", "chat_view", authz.MobileChat, ""},
		{"driver", "chat_send", authz.MobileChat, ""},
		{"customer", "chat_view", "", RBACDropped},
		{"partner", "chat_send", "", RBACDropped},
		{"operator", "chat_send", authz.ChatSend, ""},
	} {
		if got, action := translateCapability(c.role, c.key); got != c.want || action != c.action {
			t.Errorf("%s %q -> %q %q, want %q %q", c.role, c.key, got, action, c.want, c.action)
		}
	}
}

func doc(id string, caps map[string]any) dump.Doc {
	return dump.Doc{ID: id, Path: "permissions_config/" + id, Fields: map[string]any{"capabilities": caps}}
}

func TestPlanPermissionsConfig(t *testing.T) {
	plan := planPermissionsConfig([]dump.Doc{
		doc("manager", map[string]any{
			"create_truck":         false, // default true: override false
			"accounting_view_fuel": true,  // default true: same
			"users_create":         false, // users:manage from two keys, any true
			"users_edit":           true,
			"users_assign_role":    true, // tenant rows never hold it
			"view_assigned_tasks":  true, // a driver key in a staff doc
			"made_up_key":          true,
			"chat_view":            "yes", // not a boolean
		}),
		doc("driver", map[string]any{"create_trip_record": false, "submit_delivery": false, "update_trip_record": false,
			"send_chat": true, "maintenance_logs": true}),
		doc("admin", map[string]any{"security_manage_roles": false}),
		doc("customer", map[string]any{"chat_view": true, "operations_view_first_mile": true}),
		doc("ghost", map[string]any{"fleet_view_trucks": true}),
	})
	byKey := map[string]RBACLine{}
	for _, d := range plan {
		for _, l := range d.lines {
			byKey[l.LegacyRole+"/"+l.LegacyKey] = l
		}
	}
	for k, action := range map[string]string{
		"manager/create_truck": RBACOverride, "manager/accounting_view_fuel": RBACSameAsDefault,
		"manager/users_create": RBACOverride, "manager/users_edit": RBACOverride, "manager/users_assign_role": RBACNotOverridable,
		"manager/view_assigned_tasks": RBACNotOverridable, "manager/made_up_key": RBACUnknownKey, "manager/chat_view": RBACNotBoolean,
		"driver/create_trip_record": RBACOverride, "driver/send_chat": RBACSameAsDefault, "driver/maintenance_logs": RBACSameAsDefault,
		"admin/security_manage_roles": RBACNotOverridable, "customer/chat_view": RBACDropped,
		"customer/operations_view_first_mile": RBACScopeFixed, "ghost/fleet_view_trucks": RBACUnknownRole,
	} {
		if got := byKey[k].Action; got != action {
			t.Errorf("%s: %s, want %s", k, got, action)
		}
	}
	mgr, drv := plan[0], plan[1]
	if mgr.role != authz.Manager || len(mgr.keys) != 3 || mgr.keys[authz.FleetCreateTruck] || !mgr.keys[authz.UsersManage] {
		t.Errorf("manager keys %v", mgr.keys)
	}
	if len(mgr.findings) != 1 || mgr.findings[0].Reason != ReasonUnknownCapabilityKey || mgr.findings[0].Field != "capabilities.made_up_key" {
		t.Errorf("findings %+v", mgr.findings)
	}
	if drv.keys[authz.MobileSubmitTrip] {
		t.Error("mobile:submit_trip is allowed if any of its three rows is true; all three are false")
	}

	rep := &RBACReport{}
	for _, d := range plan {
		rep.Lines = append(rep.Lines, d.lines...)
	}
	var buf bytes.Buffer
	if err := rep.WriteCSV(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(buf.String(), "legacy_role,role,legacy_key,canonical_key,saved,new_default,action\n") ||
		!strings.Contains(buf.String(), "manager,manager,create_truck,fleet:create_truck,false,true,override\n") {
		t.Fatalf("csv:\n%s", buf.String())
	}
}
