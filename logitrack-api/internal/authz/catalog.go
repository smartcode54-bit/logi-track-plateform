package authz

import "strings"

//go:generate go run ./tsgen -o ../../../shared-docs/schemas/capabilities.ts

// Cap is one capability key of the catalog, always in colon form module:action (R5). The catalog is
// defined here and nowhere else: the web gets it generated (shared-docs/schemas/capabilities.ts, R27),
// override rows name these keys, and GET /v1/roles serves it.
type Cap string

// Class decides where a key can be effective and whether a tenant may override it (Appendix C §C.2.2).
type Class string

// The five capability classes.
const (
	// ClassTenant acts on rows of the active tenant; tenant overrides apply.
	ClassTenant Class = "tenant"
	// ClassGlobal acts on platform-shared master or configuration data: effective only for a steward
	// (a staff role of the own-fleet tenant, or platform_admin; R60), overridable only own-fleet or
	// platform-wide.
	ClassGlobal Class = "global"
	// ClassSelf is a driver acting on its own rows (mobile:*).
	ClassSelf Class = "self"
	// ClassScope is the cross-tenant operational projection of a dispatcher grant; never overridable.
	ClassScope Class = "scope"
	// ClassPlatform is platform administration; never overridable.
	ClassPlatform Class = "platform"
)

// The 81 keys of Appendix C §C.2.3 (77 tenant/scope keys incl. mobile:create_hub, plus 4 platform keys;
// R73), in the order of that table.
const (
	FleetViewTrucks            Cap = "fleet:view_trucks"
	FleetCreateTruck           Cap = "fleet:create_truck"
	FleetEditTruck             Cap = "fleet:edit_truck"
	FleetViewRenewals          Cap = "fleet:view_renewals"
	FleetManageRenewals        Cap = "fleet:manage_renewals"
	FleetManageMaintenance     Cap = "fleet:manage_maintenance"
	FleetManageSubcontractors  Cap = "fleet:manage_subcontractors"
	FleetManageCustomers       Cap = "fleet:manage_customers"
	FleetViewAssignments       Cap = "fleet:view_assignments"
	FleetManageAssignments     Cap = "fleet:manage_assignments"
	FleetViewLiveMap           Cap = "fleet:view_live_map"
	DriversView                Cap = "drivers:view"
	DriversCreate              Cap = "drivers:create"
	DriversEdit                Cap = "drivers:edit"
	DriversViewPII             Cap = "drivers:view_pii"
	DriversSetPassword         Cap = "drivers:set_password"
	ChatView                   Cap = "chat:view"
	ChatSend                   Cap = "chat:send"
	BroadcastsSend             Cap = "broadcasts:send"
	BroadcastsView             Cap = "broadcasts:view"
	OperationsViewFirstMile    Cap = "operations:view_first_mile"
	OperationsViewLineHaul     Cap = "operations:view_line_haul"
	OperationsManageTasks      Cap = "operations:manage_tasks"
	OperationsManageSources    Cap = "operations:manage_sources"
	OperationsCalcDistances    Cap = "operations:calculate_distances"
	OperationsViewDriverMon    Cap = "operations:view_driver_monitor"
	OperationsEditTripDetails  Cap = "operations:edit_trip_details"
	OperationsViewIncidents    Cap = "operations:view_incidents"
	OperationsCreateStandby    Cap = "operations:create_standby"
	AccountingViewFuel         Cap = "accounting:view_fuel"
	AccountingEditFuel         Cap = "accounting:edit_fuel"
	AccountingViewOther        Cap = "accounting:view_other"
	AccountingEditOther        Cap = "accounting:edit_other"
	AccountingAuditExpense     Cap = "accounting:audit_expense"
	AccountingViewRateCard     Cap = "accounting:view_rate_card"
	AccountingEditRateCard     Cap = "accounting:edit_rate_card"
	AccountingViewIncome       Cap = "accounting:view_income"
	AccountingBillingDocument  Cap = "accounting:billing_document"
	AccountingBillingResult    Cap = "accounting:billing_result"
	AccountingShopeeReport     Cap = "accounting:shopee_report"
	AccountingRecomputeForce   Cap = "accounting:recompute_force"
	AccountingOverridePrice    Cap = "accounting:override_price"
	AccountingManageStatements Cap = "accounting:manage_statements"
	ReportingViewAnalytics     Cap = "reporting:view_analytics"
	HRViewPayroll              Cap = "hr:view_payroll"
	HRManagePayroll            Cap = "hr:manage_payroll"
	HRViewLeave                Cap = "hr:view_leave"
	HRManageLeave              Cap = "hr:manage_leave"
	HRManageHolidays           Cap = "hr:manage_holidays"
	CompanyView                Cap = "company:view"
	CompanyManage              Cap = "company:manage"
	UsersView                  Cap = "users:view"
	UsersManage                Cap = "users:manage"
	UsersAssignRole            Cap = "users:assign_role"
	UsersRevokeSessions        Cap = "users:revoke_sessions"
	SecurityViewOverview       Cap = "security:view_overview"
	SecurityManageRoles        Cap = "security:manage_roles"
	SecurityViewAudit          Cap = "security:view_audit"
	SecurityManageAPIKeys      Cap = "security:manage_api_keys"
	SecurityViewStatus         Cap = "security:view_status"
	SecurityViewMobileClients  Cap = "security:view_mobile_clients"
	SecurityManageMobileRel    Cap = "security:manage_mobile_release"
	WaitlistView               Cap = "waitlist:view"
	PackagesView               Cap = "packages:view"
	MobileViewTasks            Cap = "mobile:view_tasks"
	MobileCheckin              Cap = "mobile:checkin"
	MobileSubmitTrip           Cap = "mobile:submit_trip"
	MobileSubmitStandby        Cap = "mobile:submit_standby"
	MobileReportIncident       Cap = "mobile:report_incident"
	MobileSubmitExpense        Cap = "mobile:submit_expense"
	MobileSubmitMaintenance    Cap = "mobile:submit_maintenance"
	MobileLeaveRequest         Cap = "mobile:leave_request"
	MobileChat                 Cap = "mobile:chat"
	MobileBroadcasts           Cap = "mobile:broadcasts"
	MobileViewHistory          Cap = "mobile:view_history"
	MobileCreateHub            Cap = "mobile:create_hub"
	DispatchViewOperations     Cap = "dispatch:view_operations"
	PlatformManageTenants      Cap = "platform:manage_tenants"
	PlatformManageRoles        Cap = "platform:manage_platform_roles"
	PlatformCrossTenantRead    Cap = "platform:cross_tenant_read"
	PlatformCrossTenantWrite   Cap = "platform:cross_tenant_write"
)

// CapInfo is one catalog entry as GET /v1/roles serves it (Appendix B, /v1/roles row).
type CapInfo struct {
	Key     Cap    `json:"key"`
	Module  string `json:"module"`
	Class   Class  `json:"class"`
	TitleEn string `json:"titleEn"`
	TitleTh string `json:"titleTh"`
}

// catalog is the canonical list in the order of Appendix C §C.2.3; index i is bit i of a CapSet.
var catalog = []CapInfo{
	{FleetViewTrucks, "", ClassTenant, "View trucks", "ดูข้อมูลรถ"},
	{FleetCreateTruck, "", ClassTenant, "Create trucks", "เพิ่มรถ"},
	{FleetEditTruck, "", ClassTenant, "Edit trucks", "แก้ไขข้อมูลรถ"},
	{FleetViewRenewals, "", ClassTenant, "View renewals", "ดูการต่ออายุภาษีและประกัน"},
	{FleetManageRenewals, "", ClassTenant, "Record renewals", "บันทึกการต่ออายุภาษีและประกัน"},
	{FleetManageMaintenance, "", ClassTenant, "Manage maintenance", "จัดการงานซ่อมบำรุง"},
	{FleetManageSubcontractors, "", ClassGlobal, "Manage subcontractors", "จัดการผู้รับเหมาช่วง"},
	{FleetManageCustomers, "", ClassGlobal, "Manage customers", "จัดการลูกค้า"},
	{FleetViewAssignments, "", ClassTenant, "View truck assignments", "ดูการมอบหมายรถ"},
	{FleetManageAssignments, "", ClassTenant, "Manage truck assignments", "จัดการการมอบหมายรถและคนขับ"},
	{FleetViewLiveMap, "", ClassTenant, "View live map", "ดูแผนที่ติดตามรถ"},
	{DriversView, "", ClassTenant, "View drivers", "ดูข้อมูลคนขับ"},
	{DriversCreate, "", ClassTenant, "Create drivers", "เพิ่มคนขับ"},
	{DriversEdit, "", ClassTenant, "Edit drivers", "แก้ไขข้อมูลคนขับ"},
	{DriversViewPII, "", ClassTenant, "View driver personal data", "ดูข้อมูลส่วนบุคคลของคนขับ"},
	{DriversSetPassword, "", ClassTenant, "Issue driver passwords", "ออกรหัสผ่านชั่วคราวให้คนขับ"},
	{ChatView, "", ClassTenant, "View chats", "ดูแชท"},
	{ChatSend, "", ClassTenant, "Send chat messages", "ส่งข้อความแชท"},
	{BroadcastsSend, "", ClassTenant, "Send broadcasts", "ส่งประกาศ"},
	{BroadcastsView, "", ClassTenant, "View broadcasts", "ดูประกาศ"},
	{OperationsViewFirstMile, "", ClassTenant, "View First Mile", "ดูงาน First Mile"},
	{OperationsViewLineHaul, "", ClassTenant, "View Line Haul", "ดูงาน Line Haul"},
	{OperationsManageTasks, "", ClassTenant, "Manage tasks", "จัดการงานขนส่ง"},
	{OperationsManageSources, "", ClassGlobal, "Manage hubs and SOCs", "จัดการจุดรับส่งและ SOC"},
	{OperationsCalcDistances, "", ClassGlobal, "Calculate distances", "คำนวณระยะทาง"},
	{OperationsViewDriverMon, "", ClassTenant, "View driver monitor", "ดูการติดตามคนขับ"},
	{OperationsEditTripDetails, "", ClassTenant, "Edit trip details", "แก้ไขรายละเอียดเที่ยววิ่ง"},
	{OperationsViewIncidents, "", ClassTenant, "View incident reports", "ดูรายงานเหตุการณ์"},
	{OperationsCreateStandby, "", ClassTenant, "Backfill standby records", "บันทึกสแตนด์บายย้อนหลัง"},
	{AccountingViewFuel, "", ClassTenant, "View fuel expenses", "ดูค่าน้ำมัน"},
	{AccountingEditFuel, "", ClassTenant, "Edit fuel expenses", "แก้ไขค่าน้ำมัน"},
	{AccountingViewOther, "", ClassTenant, "View other expenses", "ดูค่าใช้จ่ายอื่น"},
	{AccountingEditOther, "", ClassTenant, "Edit other expenses", "แก้ไขค่าใช้จ่ายอื่น"},
	{AccountingAuditExpense, "", ClassTenant, "Audit vehicle expenses", "ตรวจสอบค่าใช้จ่ายรถ"},
	{AccountingViewRateCard, "", ClassTenant, "View rate cards", "ดูอัตราค่าขนส่ง"},
	{AccountingEditRateCard, "", ClassTenant, "Edit rate cards", "แก้ไขอัตราค่าขนส่ง"},
	{AccountingViewIncome, "", ClassTenant, "View income", "ดูรายได้"},
	{AccountingBillingDocument, "", ClassTenant, "Billing documents", "ออกเอกสารวางบิล"},
	{AccountingBillingResult, "", ClassTenant, "Billing statements", "ดูทะเบียนใบวางบิล"},
	{AccountingShopeeReport, "", ClassTenant, "Shopee Express report", "รายงาน Shopee Express"},
	{AccountingRecomputeForce, "", ClassTenant, "Force billing recompute", "บังคับคำนวณราคาใหม่"},
	{AccountingOverridePrice, "", ClassTenant, "Override a trip price", "กำหนดราคาเที่ยวเอง"},
	{AccountingManageStatements, "", ClassTenant, "Manage billing statements", "จัดการสถานะใบวางบิล"},
	{ReportingViewAnalytics, "", ClassTenant, "View analytics", "ดูรายงานวิเคราะห์"},
	{HRViewPayroll, "", ClassTenant, "View payroll", "ดูเงินเดือน"},
	{HRManagePayroll, "", ClassTenant, "Manage payroll", "จัดการเงินเดือน"},
	{HRViewLeave, "", ClassTenant, "View leave requests", "ดูคำขอลา"},
	{HRManageLeave, "", ClassTenant, "Manage leave requests", "อนุมัติคำขอลา"},
	{HRManageHolidays, "", ClassTenant, "Manage holidays", "จัดการวันหยุด"},
	{CompanyView, "", ClassTenant, "View companies", "ดูข้อมูลบริษัท"},
	{CompanyManage, "", ClassTenant, "Manage company profile", "จัดการข้อมูลบริษัท"},
	{UsersView, "", ClassTenant, "View users", "ดูผู้ใช้งาน"},
	{UsersManage, "", ClassTenant, "Manage users", "จัดการผู้ใช้งาน"},
	{UsersAssignRole, "", ClassTenant, "Assign roles", "กำหนดบทบาทผู้ใช้งาน"},
	{UsersRevokeSessions, "", ClassTenant, "Revoke sessions", "ยกเลิกเซสชันของผู้ใช้งาน"},
	{SecurityViewOverview, "", ClassTenant, "Security overview", "ภาพรวมความปลอดภัย"},
	{SecurityManageRoles, "", ClassTenant, "Manage roles", "จัดการบทบาทและสิทธิ์"},
	{SecurityViewAudit, "", ClassTenant, "View audit log", "ดูบันทึกความปลอดภัย"},
	{SecurityManageAPIKeys, "", ClassTenant, "Manage API keys", "จัดการ API key"},
	{SecurityViewStatus, "", ClassGlobal, "View system status", "ดูสถานะระบบ"},
	{SecurityViewMobileClients, "", ClassTenant, "View mobile clients", "ดูอุปกรณ์แอปคนขับ"},
	{SecurityManageMobileRel, "", ClassGlobal, "Manage mobile release", "กำหนดเวอร์ชันขั้นต่ำของแอปคนขับ"},
	{WaitlistView, "", ClassGlobal, "View waitlist", "ดูรายชื่อผู้สนใจ"},
	{PackagesView, "", ClassTenant, "View packages", "ดูแพ็กเกจ"},
	{MobileViewTasks, "", ClassSelf, "View own tasks", "ดูงานของตนเอง"},
	{MobileCheckin, "", ClassSelf, "Check in", "เช็คอินงาน"},
	{MobileSubmitTrip, "", ClassSelf, "Submit trips", "บันทึกเที่ยววิ่ง"},
	{MobileSubmitStandby, "", ClassSelf, "Submit standby", "บันทึกสแตนด์บาย"},
	{MobileReportIncident, "", ClassSelf, "Report incidents", "รายงานเหตุการณ์"},
	{MobileSubmitExpense, "", ClassSelf, "Submit expenses", "บันทึกค่าใช้จ่าย"},
	{MobileSubmitMaintenance, "", ClassSelf, "Update maintenance", "บันทึกงานซ่อมบำรุง"},
	{MobileLeaveRequest, "", ClassSelf, "Request leave", "ยื่นคำขอลา"},
	{MobileChat, "", ClassSelf, "Chat with admins", "แชทกับแอดมิน"},
	{MobileBroadcasts, "", ClassSelf, "Read broadcasts", "อ่านประกาศ"},
	{MobileViewHistory, "", ClassSelf, "View own history", "ดูประวัติของตนเอง"},
	{MobileCreateHub, "", ClassSelf, "Create a hub", "เพิ่มจุดรับส่ง"},
	{DispatchViewOperations, "", ClassScope, "Cross-tenant operations view", "ดูงานปฏิบัติการข้ามผู้ให้บริการ"},
	{PlatformManageTenants, "", ClassPlatform, "Manage tenants", "จัดการผู้ให้บริการขนส่ง"},
	{PlatformManageRoles, "", ClassPlatform, "Manage platform roles", "จัดการบทบาทระดับแพลตฟอร์ม"},
	{PlatformCrossTenantRead, "", ClassPlatform, "Cross-tenant read", "อ่านข้อมูลข้ามผู้ให้บริการ"},
	{PlatformCrossTenantWrite, "", ClassPlatform, "Cross-tenant write", "แก้ไขข้อมูลข้ามผู้ให้บริการ"},
}

// index maps a key to its position in catalog (its CapSet bit).
var index = func() map[Cap]int {
	m := make(map[Cap]int, len(catalog))
	for i := range catalog {
		module, _, _ := strings.Cut(string(catalog[i].Key), ":")
		catalog[i].Module = module // the module is the part before the colon
		m[catalog[i].Key] = i
	}
	return m
}()

// Catalog returns the 81 entries in the order of Appendix C §C.2.3. The slice is a copy.
func Catalog() []CapInfo { return append([]CapInfo(nil), catalog...) }

// Lookup returns the catalog entry of key, and false for a key the catalog does not have.
func Lookup(key Cap) (CapInfo, bool) {
	i, ok := index[key]
	if !ok {
		return CapInfo{}, false
	}
	return catalog[i], true
}

// Known reports whether key is a catalog key.
func Known(key Cap) bool { _, ok := index[key]; return ok }

// ClassOf is the class of a catalog key ("" for an unknown key).
func ClassOf(key Cap) Class {
	if i, ok := index[key]; ok {
		return catalog[i].Class
	}
	return ""
}

// GlobalCaps are the steward-only keys (R60): effective only for own-fleet staff and platform_admin.
func GlobalCaps() []Cap { return keysOfClass(ClassGlobal) }

func keysOfClass(c Class) []Cap {
	var out []Cap
	for _, e := range catalog {
		if e.Class == c {
			out = append(out, e.Key)
		}
	}
	return out
}
