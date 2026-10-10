/**
 * Permission helpers for RBAC.
 * Use with customClaims from useAuth(), which TW4 synthesises from `['me']` (`GET /v1/me`): they
 * carry the Go-resolved `capabilities`, so `can()` answers from Go; the role defaults below remain
 * only for a claims object without that list (tests, legacy callers).
 */

import type { CapabilityId } from "./capabilities";
import {
  DEFAULT_ROLE_CAPABILITIES,
  type RoleId,
} from "./roles";
import { CAPABILITIES, toCatalogKey } from "./capabilities";

type CustomClaims = Record<string, unknown> | null;

/** Resolve effective role from customClaims */
export function getRole(claims: CustomClaims): RoleId {
  if (!claims) return "user";
  if (claims.admin === true) return "admin";
  const role = claims.role as string | undefined;
  if (role && ["admin", "manager", "operation_staff", "operator", "customer", "partner", "user", "driver"].includes(role)) {
    return role as RoleId;
  }
  return "user";
}

/** Check if user has a specific role */
export function hasRole(claims: CustomClaims, roleId: RoleId): boolean {
  return getRole(claims) === roleId;
}

/**
 * Check if user has a capability: the Go-resolved set when the claims carry one (`useAuth()`
 * claims, synthesised from `['me']`), else the legacy default role mapping.
 */
export function can(claims: CustomClaims, capability: CapabilityId): boolean {
  if (Array.isArray(claims?.capabilities)) {
    return (claims.capabilities as unknown[]).includes(toCatalogKey(capability));
  }
  const role = getRole(claims);
  const caps = DEFAULT_ROLE_CAPABILITIES[role];
  if (caps === "*") return true;
  return caps.includes(capability);
}

// Route access is decided by the proxy.ts edge gate over the Go capabilities (lib/routeCapabilities.ts,
// TW3); the client-side canAccessRoute over Firebase claims is gone.

/** Check if user can view Driver Monitor (admin, operation_staff, customer) */
export function canViewDriverMonitor(claims: CustomClaims): boolean {
  return can(claims, CAPABILITIES.operations_view_driver_monitor);
}

/** Check if user can edit trip details in Driver Monitor */
export function canEditTripDetails(claims: CustomClaims): boolean {
  return can(claims, CAPABILITIES.operations_edit_trip_details);
}

/** Check if user is admin */
export function isAdmin(claims: CustomClaims): boolean {
  return claims?.admin === true || getRole(claims) === "admin";
}

/**
 * Get default redirect route for role (used when access denied) from legacy Firebase claims. The
 * `/app` landing itself is decided by proxy.ts (lib/routeCapabilities.ts `homeRouteFor`, R89).
 */
export function getDefaultRouteForRole(claims: CustomClaims): string {
  const role = getRole(claims);
  switch (role) {
    case "admin":
    case "manager":
    case "operation_staff":
      return "/app/dashboard";
    case "customer":
    case "operator":
    case "partner":
      return "/app/driver-monitor";
    default:
      return "/app/dashboard";
  }
}
