// TW4: the W6 key factory (developer-spec.md §10.7, Appendix E §E.6).
import { describe, expect, it } from "vitest";
import { partialMatchKey } from "@tanstack/react-query";
import { QUERY_POLICY, queryKeys } from "./queryKeys";

describe("queryKeys (W6)", () => {
    it("builds the catalogued keys", () => {
        expect(queryKeys.me()).toEqual(["me"]);
        expect(queryKeys.webFlags()).toEqual(["webFlags"]);
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
    });

    it("carries the stale / gc rows of developer-spec.md §10.7", () => {
        expect(QUERY_POLICY.me).toEqual({ staleTime: 300_000, gcTime: 1_800_000 });
        expect(QUERY_POLICY.masterData).toEqual({ staleTime: 600_000, gcTime: 3_600_000 });
        expect(QUERY_POLICY.live).toEqual({ staleTime: 30_000, gcTime: 300_000 });
        expect(QUERY_POLICY.fuel).toEqual({ staleTime: 3_600_000, gcTime: 7_200_000 });
    });
});
