/**
 * Logi-Track Capability Definitions
 * Used for Role & Permission Matrix and access control.
 *
 * Format: module:action_resource
 */

export const CAPABILITIES = {
  // Fleet
  fleet_view_trucks: "fleet:view_trucks",
  fleet_create_truck: "fleet:create_truck",
  fleet_edit_truck: "fleet:edit_truck",
  fleet_view_renewals: "fleet:view_renewals",
  fleet_manage_maintenance: "fleet:manage_maintenance",
  fleet_manage_subcontractors: "fleet:manage_subcontractors",
  fleet_manage_customers: "fleet:manage_customers",
  fleet_view_assignments: "fleet:view_assignments",

  // Drivers
  drivers_view: "drivers:view",
  drivers_create: "drivers:create",
  drivers_edit: "drivers:edit",

  // Chat
  chat_view: "chat:view",
  chat_send: "chat:send",

  // Operations
  operations_view_first_mile: "operations:view_first_mile",
  operations_view_line_haul: "operations:view_line_haul",
  operations_manage_sources: "operations:manage_sources",
  operations_calculate_distances: "operations:calculate_distances",
  operations_view_driver_monitor: "operations:view_driver_monitor",
  operations_edit_trip_details: "operations:edit_trip_details",
  operations_view_incidents: "operations:view_incidents",
  operations_view_custom_stops: "operations:view_custom_stops",
  operations_create_standby: "operations:create_standby",

  // Security
  security_view_overview: "security:view_overview",
  security_manage_users: "security:manage_users",
  security_manage_roles: "security:manage_roles",
  security_view_audit: "security:view_audit",
  security_manage_api_keys: "security:manage_api_keys",
  security_view_status: "security:view_status",
  security_view_mobile_clients: "security:view_mobile_clients",

  // Accounting
  accounting_view_fuel: "accounting:view_fuel",
  accounting_edit_fuel: "accounting:edit_fuel",
  accounting_view_other: "accounting:view_other",
  accounting_edit_other: "accounting:edit_other",
  accounting_audit_expense: "accounting:audit_expense",
  accounting_view_rate_card: "accounting:view_rate_card",
  accounting_edit_rate_card: "accounting:edit_rate_card",
  accounting_view_income: "accounting:view_income",
  accounting_billing_document: "accounting:billing_document",

  // Reporting
  reporting_view_analytics: "reporting:view_analytics",

  // HR & Payroll
  hr_view_payroll: "hr:view_payroll",
  hr_manage_payroll: "hr:manage_payroll",
  hr_view_leave: "hr:view_leave",
  hr_manage_leave: "hr:manage_leave",
  hr_manage_holidays: "hr:manage_holidays",

  // Other
  waitlist_view: "waitlist:view",
  packages_view: "packages:view",
} as const;

export type CapabilityId = (typeof CAPABILITIES)[keyof typeof CAPABILITIES];

/** Capability metadata for UI */
export const CAPABILITY_META: Record<
  CapabilityId,
  { title: string; description: string; module: string }
> = {
  [CAPABILITIES.fleet_view_trucks]: {
    title: "View Trucks",
    description: "View truck list",
    module: "Fleet",
  },
  [CAPABILITIES.fleet_create_truck]: {
    title: "Create Truck",
    description: "Add new truck",
    module: "Fleet",
  },
  [CAPABILITIES.fleet_edit_truck]: {
    title: "Edit Truck",
    description: "Edit truck details",
    module: "Fleet",
  },
  [CAPABILITIES.fleet_view_renewals]: {
    title: "View Renewals",
    description: "View renewals overview",
    module: "Fleet",
  },
  [CAPABILITIES.fleet_manage_maintenance]: {
    title: "Manage Maintenance",
    description: "Maintenance logs",
    module: "Fleet",
  },
  [CAPABILITIES.fleet_manage_subcontractors]: {
    title: "Manage Subcontractors",
    description: "Subcontractors CRUD",
    module: "Fleet",
  },
  [CAPABILITIES.fleet_manage_customers]: {
    title: "Manage Customers",
    description: "Customers CRUD",
    module: "Fleet",
  },
  [CAPABILITIES.fleet_view_assignments]: {
    title: "View Assignments",
    description: "Truck assignments",
    module: "Fleet",
  },
  [CAPABILITIES.drivers_view]: {
    title: "View Drivers",
    description: "View drivers list",
    module: "Drivers",
  },
  [CAPABILITIES.drivers_create]: {
    title: "Create Driver",
    description: "Add driver",
    module: "Drivers",
  },
  [CAPABILITIES.drivers_edit]: {
    title: "Edit Driver",
    description: "Edit driver",
    module: "Drivers",
  },
  [CAPABILITIES.chat_view]: {
    title: "View Chats",
    description: "View conversations",
    module: "Chat",
  },
  [CAPABILITIES.chat_send]: {
    title: "Send Chat",
    description: "Send messages",
    module: "Chat",
  },
  [CAPABILITIES.operations_view_first_mile]: {
    title: "View First Mile",
    description: "First Mile tasks",
    module: "Operations",
  },
  [CAPABILITIES.operations_view_line_haul]: {
    title: "View Line Haul",
    description: "Line Haul tasks",
    module: "Operations",
  },
  [CAPABILITIES.operations_manage_sources]: {
    title: "Manage Sources",
    description: "Hub/SOC management",
    module: "Operations",
  },
  [CAPABILITIES.operations_calculate_distances]: {
    title: "Calculate Distances",
    description: "Calculate hub-SOC distances (Admin)",
    module: "Operations",
  },
  [CAPABILITIES.operations_view_driver_monitor]: {
    title: "View Driver Monitor",
    description: "Live trip monitoring",
    module: "Operations",
  },
  [CAPABILITIES.operations_edit_trip_details]: {
    title: "Edit Trip Details",
    description: "Edit trip in Driver Monitor",
    module: "Operations",
  },
  [CAPABILITIES.operations_view_incidents]: {
    title: "View Incident Reports",
    description: "View and manage incident reports",
    module: "Operations",
  },
  [CAPABILITIES.operations_view_custom_stops]: {
    title: "View Custom Stops",
    description: "Review driver-added custom delivery stops",
    module: "Operations",
  },
  [CAPABILITIES.operations_create_standby]: {
    title: "Backfill Standby Record",
    description: "Manually create a standby record for past trips (Admin, Manager, Operation)",
    module: "Operations",
  },
  [CAPABILITIES.security_view_overview]: {
    title: "Security Overview",
    description: "Security overview",
    module: "Security",
  },
  [CAPABILITIES.security_manage_users]: {
    title: "Manage Users",
    description: "User Management",
    module: "Security",
  },
  [CAPABILITIES.security_manage_roles]: {
    title: "Manage Roles",
    description: "Role & Permission Matrix",
    module: "Security",
  },
  [CAPABILITIES.security_view_audit]: {
    title: "View Audit",
    description: "Security audit",
    module: "Security",
  },
  [CAPABILITIES.security_manage_api_keys]: {
    title: "Manage API Keys",
    description: "API keys",
    module: "Security",
  },
  [CAPABILITIES.security_view_status]: {
    title: "View Status",
    description: "System status",
    module: "Security",
  },
  [CAPABILITIES.security_view_mobile_clients]: {
    title: "View Mobile Clients",
    description: "Driver app versions and last seen (per device)",
    module: "Security",
  },
  [CAPABILITIES.accounting_view_fuel]: {
    title: "View Fuel",
    description: "Fuel accounting",
    module: "Accounting",
  },
  [CAPABILITIES.accounting_edit_fuel]: {
    title: "Edit Fuel",
    description: "Edit fuel records",
    module: "Accounting",
  },
  [CAPABILITIES.accounting_view_other]: {
    title: "View Other",
    description: "Other expenses",
    module: "Accounting",
  },
  [CAPABILITIES.accounting_edit_other]: {
    title: "Edit Other",
    description: "Edit other records",
    module: "Accounting",
  },
  [CAPABILITIES.accounting_audit_expense]: {
    title: "Audit Vehicle Expense",
    description: "Review and approve/reject vehicle expenses",
    module: "Accounting",
  },
  [CAPABILITIES.accounting_view_rate_card]: {
    title: "View Rate Card",
    description: "View customer rate cards and fuel adjustments",
    module: "Accounting",
  },
  [CAPABILITIES.accounting_edit_rate_card]: {
    title: "Edit Rate Card",
    description: "Import and manage customer rate cards",
    module: "Accounting",
  },
  [CAPABILITIES.accounting_view_income]: {
    title: "View Income",
    description: "View estimated income from delivered trips",
    module: "Accounting",
  },
  [CAPABILITIES.accounting_billing_document]: {
    title: "Billing Documents",
    description: "Generate and download invoice / receipt / Excel billing packages",
    module: "Accounting",
  },
  [CAPABILITIES.reporting_view_analytics]: {
    title: "View Analytics",
    description: "Analytics",
    module: "Reporting",
  },
  [CAPABILITIES.hr_view_payroll]: {
    title: "View Payroll",
    description: "View payroll records",
    module: "HR",
  },
  [CAPABILITIES.hr_manage_payroll]: {
    title: "Manage Payroll",
    description: "Generate and approve payroll",
    module: "HR",
  },
  [CAPABILITIES.hr_view_leave]: {
    title: "View Leave Requests",
    description: "View driver leave requests",
    module: "HR",
  },
  [CAPABILITIES.hr_manage_leave]: {
    title: "Manage Leave Requests",
    description: "Approve or reject leave requests",
    module: "HR",
  },
  [CAPABILITIES.hr_manage_holidays]: {
    title: "Manage Holidays",
    description: "Manage holiday calendar",
    module: "HR",
  },
  [CAPABILITIES.waitlist_view]: {
    title: "View Waitlist",
    description: "View waitlist",
    module: "Other",
  },
  [CAPABILITIES.packages_view]: {
    title: "View Packages",
    description: "Active shipments",
    module: "Other",
  },
};

/** Route to capability mapping for route guards */
export const ROUTE_CAPABILITIES: Record<string, CapabilityId> = {
  "/app/dashboard": CAPABILITIES.fleet_view_trucks, // Dashboard visible to any fleet viewer
  "/app/trucks": CAPABILITIES.fleet_view_trucks,
  "/app/trucks/new": CAPABILITIES.fleet_create_truck,
  "/app/trucks/view": CAPABILITIES.fleet_view_trucks,
  "/app/trucks/edit": CAPABILITIES.fleet_edit_truck,
  "/app/trucks/renew": CAPABILITIES.fleet_view_renewals,
  "/app/trucks/maintenance": CAPABILITIES.fleet_manage_maintenance,
  "/app/truck-assignment": CAPABILITIES.fleet_view_assignments,
  "/app/renewals": CAPABILITIES.fleet_view_renewals,
  "/app/maintenance": CAPABILITIES.fleet_manage_maintenance,
  "/app/subcontractors": CAPABILITIES.fleet_manage_subcontractors,
  "/app/subcontractors/new": CAPABILITIES.fleet_manage_subcontractors,
  "/app/customers": CAPABILITIES.fleet_manage_customers,
  "/app/customers/new": CAPABILITIES.fleet_manage_customers,
  "/app/drivers": CAPABILITIES.drivers_view,
  "/app/chat": CAPABILITIES.chat_view,
  "/app/first-mile": CAPABILITIES.operations_view_first_mile,
  "/app/line-haul": CAPABILITIES.operations_view_line_haul,
  "/app/sources": CAPABILITIES.operations_manage_sources,
  "/app/driver-monitor": CAPABILITIES.operations_view_driver_monitor,
  "/app/incident-reports": CAPABILITIES.operations_view_incidents,
  "/app/standby-records": CAPABILITIES.operations_view_driver_monitor,
  "/app/custom-stops-review": CAPABILITIES.operations_view_custom_stops,
  "/app/security-center": CAPABILITIES.security_view_overview,
  "/app/security-center/users": CAPABILITIES.security_manage_users,
  "/app/security-center/roles": CAPABILITIES.security_manage_roles,
  "/app/security-center/audit": CAPABILITIES.security_view_audit,
  "/app/security-center/api-keys": CAPABILITIES.security_manage_api_keys,
  "/app/security-center/status": CAPABILITIES.security_view_status,
  "/app/security-center/mobile-clients": CAPABILITIES.security_view_mobile_clients,
  "/app/utilities": CAPABILITIES.security_view_overview,
  "/app/utilities/backfill": CAPABILITIES.security_view_overview,
  "/app/accounting/fuel": CAPABILITIES.accounting_view_fuel,
  "/app/accounting/other": CAPABILITIES.accounting_view_other,
  "/app/accounting/audit": CAPABILITIES.accounting_audit_expense,
  "/app/accounting/rate-card": CAPABILITIES.accounting_view_rate_card,
  "/app/accounting/income": CAPABILITIES.accounting_view_income,
  "/app/accounting/billing-document": CAPABILITIES.accounting_billing_document,
  "/app/analytics": CAPABILITIES.reporting_view_analytics,
  "/app/payroll": CAPABILITIES.hr_view_payroll,
  "/app/leave-requests": CAPABILITIES.hr_view_leave,
  "/app/holidays": CAPABILITIES.hr_manage_holidays,
  "/app/waitlist": CAPABILITIES.waitlist_view,
  "/app/packages": CAPABILITIES.packages_view,
};
