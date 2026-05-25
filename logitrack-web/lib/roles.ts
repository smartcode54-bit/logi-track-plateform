/**
 * Role definitions and default permission mapping.
 * Maps customClaims.role to capabilities.
 * Can be overridden by Firestore permissions_config.
 */

import {
  CAPABILITIES,
  type CapabilityId,
} from "./capabilities";

/** Role IDs used in customClaims and User Management */
export const ROLE_IDS = [
  "admin",
  "manager",
  "operation_staff",
  "operator",
  "customer",
  "partner",
  "user",
  "driver",
] as const;

export type RoleId = (typeof ROLE_IDS)[number];

/** Matrix role keys (ADMIN, MANAGER, etc.) - maps to RoleId */
export const MATRIX_ROLE_IDS = [
  "admin",
  "manager",
  "operation_staff",
  "operator",
  "driver",
  "partner",
  "customer",
] as const;

export type MatrixRoleId = (typeof MATRIX_ROLE_IDS)[number];

/** Default capabilities per role. Admin gets all. */
export const DEFAULT_ROLE_CAPABILITIES: Record<
  RoleId,
  CapabilityId[] | "*"
> = {
  admin: "*",
  manager: [
    CAPABILITIES.fleet_view_trucks,
    CAPABILITIES.fleet_create_truck,
    CAPABILITIES.fleet_edit_truck,
    CAPABILITIES.fleet_view_renewals,
    CAPABILITIES.fleet_manage_maintenance,
    CAPABILITIES.fleet_manage_subcontractors,
    CAPABILITIES.fleet_manage_customers,
    CAPABILITIES.fleet_view_assignments,
    CAPABILITIES.drivers_view,
    CAPABILITIES.drivers_create,
    CAPABILITIES.drivers_edit,
    CAPABILITIES.chat_view,
    CAPABILITIES.chat_send,
    CAPABILITIES.operations_view_first_mile,
    CAPABILITIES.operations_view_line_haul,
    CAPABILITIES.operations_manage_sources,
    CAPABILITIES.operations_view_driver_monitor,
    CAPABILITIES.operations_edit_trip_details,
    CAPABILITIES.operations_view_incidents,
    CAPABILITIES.operations_create_standby,
    CAPABILITIES.accounting_view_fuel,
    CAPABILITIES.accounting_edit_fuel,
    CAPABILITIES.accounting_view_other,
    CAPABILITIES.accounting_edit_other,
    CAPABILITIES.accounting_audit_expense,
    CAPABILITIES.accounting_view_rate_card,
    CAPABILITIES.accounting_edit_rate_card,
    CAPABILITIES.accounting_view_income,
    CAPABILITIES.accounting_billing_document,
    CAPABILITIES.accounting_billing_result,
    CAPABILITIES.reporting_view_analytics,
    CAPABILITIES.packages_view,
    CAPABILITIES.waitlist_view,
    CAPABILITIES.company_view,
    CAPABILITIES.company_manage,
  ],
  operation_staff: [
    CAPABILITIES.fleet_view_trucks,
    CAPABILITIES.fleet_view_renewals,
    CAPABILITIES.fleet_manage_maintenance,
    CAPABILITIES.fleet_view_assignments,
    CAPABILITIES.drivers_view,
    CAPABILITIES.chat_view,
    CAPABILITIES.chat_send,
    CAPABILITIES.operations_view_first_mile,
    CAPABILITIES.operations_view_line_haul,
    CAPABILITIES.operations_manage_sources,
    CAPABILITIES.operations_view_driver_monitor,
    CAPABILITIES.operations_edit_trip_details,
    CAPABILITIES.operations_create_standby,
    CAPABILITIES.accounting_view_fuel,
    CAPABILITIES.accounting_view_other,
    CAPABILITIES.accounting_audit_expense,
    CAPABILITIES.accounting_view_rate_card,
    CAPABILITIES.accounting_view_income,
    CAPABILITIES.packages_view,
  ],
  operator: [
    CAPABILITIES.fleet_view_trucks,
    CAPABILITIES.fleet_view_renewals,
    CAPABILITIES.fleet_manage_maintenance,
    CAPABILITIES.fleet_view_assignments,
    CAPABILITIES.drivers_view,
    CAPABILITIES.chat_view,
    CAPABILITIES.chat_send,
    CAPABILITIES.operations_view_first_mile,
    CAPABILITIES.operations_view_line_haul,
    CAPABILITIES.operations_manage_sources,
    CAPABILITIES.operations_view_driver_monitor,
    CAPABILITIES.operations_edit_trip_details,
    CAPABILITIES.operations_view_incidents,
  ],
  customer: [
    CAPABILITIES.operations_view_driver_monitor,
    CAPABILITIES.operations_view_first_mile,
    CAPABILITIES.operations_view_line_haul,
    CAPABILITIES.operations_view_incidents,
  ],
  partner: [
    CAPABILITIES.operations_view_driver_monitor,
    CAPABILITIES.chat_view,
    CAPABILITIES.chat_send,
    CAPABILITIES.security_view_mobile_clients,
  ],
  user: [
    CAPABILITIES.fleet_view_trucks,
    CAPABILITIES.drivers_view,
  ],
  driver: [], // Mobile only - no web admin
};
