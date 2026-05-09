import { describe, it, expect } from "vitest";
import {
  getRole,
  hasRole,
  can,
  canAccessRoute,
  canViewDriverMonitor,
  canEditTripDetails,
  isAdmin,
} from "./permissions";
import { CAPABILITIES } from "./capabilities";

describe("permissions", () => {
  describe("getRole", () => {
    it("returns 'user' when claims is null", () => {
      expect(getRole(null)).toBe("user");
    });

    it("returns 'user' when claims is undefined (empty object)", () => {
      expect(getRole({})).toBe("user");
    });

    it("returns 'admin' when claims.admin === true", () => {
      expect(getRole({ admin: true })).toBe("admin");
    });

    it("returns role from claims.role when valid", () => {
      expect(getRole({ role: "manager" })).toBe("manager");
      expect(getRole({ role: "operation_staff" })).toBe("operation_staff");
      expect(getRole({ role: "operator" })).toBe("operator");
      expect(getRole({ role: "customer" })).toBe("customer");
      expect(getRole({ role: "partner" })).toBe("partner");
      expect(getRole({ role: "user" })).toBe("user");
      expect(getRole({ role: "driver" })).toBe("driver");
    });

    it("returns 'user' when claims.role is unknown", () => {
      expect(getRole({ role: "unknown_role" })).toBe("user");
      expect(getRole({ role: "" })).toBe("user");
    });
  });

  describe("hasRole", () => {
    it("returns true when user has the given role", () => {
      expect(hasRole({ admin: true }, "admin")).toBe(true);
      expect(hasRole({ role: "manager" }, "manager")).toBe(true);
    });

    it("returns false when user has different role", () => {
      expect(hasRole({ role: "manager" }, "admin")).toBe(false);
      expect(hasRole(null, "admin")).toBe(false);
    });
  });

  describe("can", () => {
    it("admin has all capabilities", () => {
      expect(can({ admin: true }, CAPABILITIES.fleet_view_trucks)).toBe(true);
      expect(can({ admin: true }, CAPABILITIES.security_manage_users)).toBe(true);
      expect(can({ admin: true }, CAPABILITIES.hr_manage_holidays)).toBe(true);
    });

    it("null claims has no capabilities beyond default user", () => {
      expect(can(null, CAPABILITIES.fleet_view_trucks)).toBe(true);
      expect(can(null, CAPABILITIES.drivers_view)).toBe(true);
      expect(can(null, CAPABILITIES.security_manage_users)).toBe(false);
    });

    it("manager has manager capabilities", () => {
      expect(can({ role: "manager" }, CAPABILITIES.fleet_view_trucks)).toBe(true);
      expect(can({ role: "manager" }, CAPABILITIES.drivers_create)).toBe(true);
      expect(can({ role: "manager" }, CAPABILITIES.waitlist_view)).toBe(true);
      expect(can({ role: "manager" }, CAPABILITIES.hr_manage_holidays)).toBe(false);
    });

    it("customer has only driver monitor view", () => {
      expect(can({ role: "customer" }, CAPABILITIES.operations_view_driver_monitor)).toBe(true);
      expect(can({ role: "customer" }, CAPABILITIES.fleet_view_trucks)).toBe(false);
    });

    it("driver has no web capabilities", () => {
      expect(can({ role: "driver" }, CAPABILITIES.fleet_view_trucks)).toBe(false);
      expect(can({ role: "driver" }, CAPABILITIES.drivers_view)).toBe(false);
    });
  });

  describe("canAccessRoute", () => {
    it("allows access when route has no capability requirement", () => {
      expect(canAccessRoute(null, "/")).toBe(true);
      expect(canAccessRoute(null, "/login")).toBe(true);
    });

    it("admin can access all admin routes", () => {
      expect(canAccessRoute({ admin: true }, "/app/dashboard")).toBe(true);
      expect(canAccessRoute({ admin: true }, "/app/security-center/users")).toBe(true);
      expect(canAccessRoute({ admin: true }, "/app/trucks/new")).toBe(true);
    });

    it("user with fleet_view_trucks can access /admin/dashboard and /admin/trucks", () => {
      expect(canAccessRoute({ role: "user" }, "/app/dashboard")).toBe(true);
      expect(canAccessRoute({ role: "user" }, "/app/trucks")).toBe(true);
      expect(canAccessRoute({ role: "user" }, "/app/trucks/new")).toBe(false);
    });

    it("normalizes trailing slash", () => {
      expect(canAccessRoute({ admin: true }, "/app/dashboard/")).toBe(true);
    });

    it("nested route uses parent capability", () => {
      expect(canAccessRoute({ role: "customer" }, "/app/driver-monitor")).toBe(true);
      expect(canAccessRoute({ role: "driver" }, "/app/drivers")).toBe(false);
    });
  });

  describe("canViewDriverMonitor", () => {
    it("returns true for admin", () => {
      expect(canViewDriverMonitor({ admin: true })).toBe(true);
    });

    it("returns true for operation_staff and customer", () => {
      expect(canViewDriverMonitor({ role: "operation_staff" })).toBe(true);
      expect(canViewDriverMonitor({ role: "customer" })).toBe(true);
    });

    it("returns false for driver with no capability", () => {
      expect(canViewDriverMonitor({ role: "driver" })).toBe(false);
    });
  });

  describe("canEditTripDetails", () => {
    it("returns true for admin", () => {
      expect(canEditTripDetails({ admin: true })).toBe(true);
    });

    it("returns true for operation_staff and operator", () => {
      expect(canEditTripDetails({ role: "operation_staff" })).toBe(true);
      expect(canEditTripDetails({ role: "operator" })).toBe(true);
    });

    it("returns false for customer (view only)", () => {
      expect(canEditTripDetails({ role: "customer" })).toBe(false);
    });
  });

  describe("isAdmin", () => {
    it("returns true when claims.admin === true", () => {
      expect(isAdmin({ admin: true })).toBe(true);
    });

    it("returns true when role is admin", () => {
      expect(isAdmin({ role: "admin" })).toBe(true);
    });

    it("returns false for non-admin", () => {
      expect(isAdmin(null)).toBe(false);
      expect(isAdmin({})).toBe(false);
      expect(isAdmin({ role: "manager" })).toBe(false);
    });
  });
});
