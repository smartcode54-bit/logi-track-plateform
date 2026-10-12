// TW4: the W6 key factory (developer-spec.md §10.7, Appendix E §E.6).
import { describe, expect, it } from "vitest";
import { partialMatchKey } from "@tanstack/react-query";
import { QUERY_POLICY, queryKeys } from "./queryKeys";

describe("queryKeys (W6)", () => {
    it("builds the catalogued keys", () => {
        expect(queryKeys.me()).toEqual(["me"]);
        expect(queryKeys.myTenants()).toEqual(["me", "tenants"]);
        expect(queryKeys.tenants.list({ all: true })).toEqual(["tenants", { all: true }]);
        expect(queryKeys.tenants.members("t1", { role: "tenant_admin" })).toEqual(["tenants", "t1", "members", { role: "tenant_admin" }]);
        expect(queryKeys.badges()).toEqual(["badges"]);
        expect(queryKeys.hubs.all()).toEqual(["hubs"]);
        expect(queryKeys.hubs.maps()).toEqual(["hubs", "maps"]);
        expect(queryKeys.customers.all()).toEqual(["customers"]);
        expect(queryKeys.customers.list({ q: "cj" })).toEqual(["customers", { q: "cj" }]);
        expect(queryKeys.customers.list()).toEqual(["customers"]);
        expect(queryKeys.companies.owner()).toEqual(["companies", { owner: true }]);
        expect(queryKeys.trips.monitor({ from: "a", to: "b" })).toEqual(["trips", "monitor", { from: "a", to: "b" }]);
        expect(queryKeys.billing.rows("c1", 2026, 9)).toEqual(["billing", "rows", "c1", 2026, 9]);
        expect(queryKeys.fuel.bangchak("th")).toEqual(["fuel", "bangchak", "th"]);
        expect(queryKeys.chat.messages("x")).toEqual(["chat", "x", "messages"]);
    });

    it("the domain prefix reaches every variant of the domain", () => {
        expect(partialMatchKey(queryKeys.hubs.maps(), queryKeys.hubs.all())).toBe(true);
        expect(partialMatchKey(queryKeys.companies.owner(), queryKeys.companies.all())).toBe(true);
        expect(partialMatchKey(queryKeys.trips.detail("t1"), queryKeys.trips.all())).toBe(true);
        expect(partialMatchKey(queryKeys.customers.all(), queryKeys.hubs.all())).toBe(false);
        expect(partialMatchKey(queryKeys.myTenants(), queryKeys.me())).toBe(true);
        expect(partialMatchKey(queryKeys.tenants.members("t1", {}), queryKeys.tenants.all())).toBe(true);
    });

    it("Security Center keys (T54S): the reach or layer in the params, the bare prefix for invalidation", () => {
        expect(queryKeys.security.overview()).toEqual(["security", "overview"]);
        expect(queryKeys.security.overview({ reach: "all" })).toEqual(["security", "overview", { reach: "all" }]);
        expect(queryKeys.security.usersByRole({ reach: "tenant" })).toEqual(["security", "usersByRole", { reach: "tenant" }]);
        expect(queryKeys.security.status()).toEqual(["security", "status"]);
        expect(queryKeys.roles.matrix()).toEqual(["roles", "matrix"]);
        expect(queryKeys.roles.matrix({ layer: "platform", tenantId: "" })).toEqual(["roles", "matrix", { layer: "platform", tenantId: "" }]);
        expect(queryKeys.apiKeys()).toEqual(["apiKeys"]);
        expect(queryKeys.apiKeys({ reach: "all" })).toEqual(["apiKeys", { reach: "all" }]);
        expect(queryKeys.mobileReleases()).toEqual(["mobileReleases"]);
        expect(partialMatchKey(queryKeys.security.events({ reach: "all" }), queryKeys.security.all())).toBe(true);
        expect(partialMatchKey(queryKeys.roles.matrix({ layer: "default" }), queryKeys.roles.all())).toBe(true);
        expect(partialMatchKey(queryKeys.apiKeys({ reach: "tenant" }), queryKeys.apiKeys())).toBe(true);
        expect(partialMatchKey(queryKeys.mobileReleases({ flavor: "" }), queryKeys.mobileReleases())).toBe(true);
        // Each call builds a fresh array (a cached key is never shared and mutated).
        expect(queryKeys.apiKeys()).not.toBe(queryKeys.apiKeys());
    });

    it("carries the stale / gc rows of developer-spec.md §10.7", () => {
        expect(QUERY_POLICY.me).toEqual({ staleTime: 300_000, gcTime: 1_800_000 });
        expect(QUERY_POLICY.masterData).toEqual({ staleTime: 600_000, gcTime: 3_600_000 });
        expect(QUERY_POLICY.live).toEqual({ staleTime: 30_000, gcTime: 300_000 });
        expect(QUERY_POLICY.fuel).toEqual({ staleTime: 3_600_000, gcTime: 7_200_000 });
    });
});
