/**
 * Permission helpers for RBAC.
 * Use with customClaims from useAuth().
 */

import type { CapabilityId } from "./capabilities";
import {
  DEFAULT_ROLE_CAPABILITIES,
  type RoleId,
} from "./roles";
import { CAPABILITIES } from "./capabilities";
import { ROUTE_CAPABILITIES } from "./capabilities";

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

/** Check if user has a capability (sync, uses default role mapping) */
export function can(claims: CustomClaims, capability: CapabilityId): boolean {
  const role = getRole(claims);
  const caps = DEFAULT_ROLE_CAPABILITIES[role];
  if (caps === "*") return true;
  return caps.includes(capability);
}

/** Check if user can access a route (by pathname) */
export function canAccessRoute(claims: CustomClaims, pathname: string): boolean {
  const path = pathname.replace(/\/$/, "") || "/";

  // Dashboard — open to all authenticated users
  if (path === "/admin/dashboard") {
    return !!claims;
  }

  // Security center — admin only
  if (path.startsWith("/admin/security-center")) {
    return isAdmin(claims);
  }

  let capability: CapabilityId | undefined = ROUTE_CAPABILITIES[path];

  if (!capability) {
    const parts = path.split("/");
    for (let i = parts.length; i >= 1; i--) {
      const prefix = parts.slice(0, i).join("/");
      if (ROUTE_CAPABILITIES[prefix]) {
        capability = ROUTE_CAPABILITIES[prefix];
        break;
      }
    }
  }

  if (!capability) return false;
  return can(claims, capability);
}

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

/** Get default redirect route for role (used when access denied) */
export function getDefaultRouteForRole(claims: CustomClaims): string {
  const role = getRole(claims);
  switch (role) {
    case "admin":
    case "manager":
    case "operation_staff":
      return "/admin/dashboard";
    case "customer":
    case "operator":
    case "partner":
      return "/admin/driver-monitor";
    default:
      return "/admin/dashboard";
  }
}
