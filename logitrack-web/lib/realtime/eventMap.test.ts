// TW5 (developer-spec.md §10.8; Appendix E §E.5.1 "Event -> cache"; Appendix B §B.4.2-§B.4.3): the
// static event map of the realtime bridge, as of the M1 slice (user, global and fleet topics).
import { describe, expect, it } from "vitest";
import { partialMatchKey, type QueryKey } from "@tanstack/react-query";

import { queryKeys } from "../queryKeys";
import {
    effectsOf,
    EXPENSE_EVENTS,
    FLEET_EVENTS,
    FLEET_KEYS,
    JOB_SUCCESS_KEYS,
    LIST_WINDOW_MS,
    MOBILE_SETTINGS_KEYS,
    MONITOR_WINDOW_MS,
    REALTIME_EVENTS,
    ROLES_CHANGED_KEYS,
    STATEMENT_EVENTS,
} from "./eventMap";

const keysOf = (event: string, data: unknown = {}) => effectsOf({ event, data }).invalidate.map((i) => i.queryKey);
const reaches = (keys: QueryKey[], key: QueryKey) => keys.some((k) => partialMatchKey(key, k));

// The events Appendix B §B.4.2 puts on the topics this slice wires (the catalogue's names).
const CATALOGUE = {
    user: ["session.revoked", "job.updated", "broadcast.created", "chat.message_created"],
    global: ["hubs.changed", "customers.changed", "companies.changed", "holidays.changed", "mobile_settings.changed"],
    fleet: [
        "truck.created",
        "truck.updated",
        "truck.renewed",
        "assignment.created",
        "assignment.revoked",
        "driver.updated",
        "maintenance.created",
        "maintenance.updated",
        "maintenance.driver_submitted",
    ],
    // tenant:{tid}:config (T54S pulls the roles row forward from M6).
    config: ["roles.changed", "holidays.changed"],
    // M3 (T42): tenant:{tid}:billing, tenant:{tid}:expenses and the priced events of tenant:{tid}:trips.
    billing: ["ratecard.changed", "statement.created", "statement.status_changed", "documents.rendered"],
    expenses: ["expense.created", "expense.updated", "expense.approved", "expense.status_changed"],
    trips: ["trip.created", "trip.updated", "trip.delivered", "trip.priced", "trip.repriced", "trip.cancelled", "trip.resubmitted", "standby.completed", "standby.priced", "incident.created"],
};

describe("event map (M1 slice)", () => {
    it("listens only to catalogued event names, in full (EventSource has no patterns)", () => {
        const catalogued = new Set(Object.values(CATALOGUE).flat());
        for (const name of REALTIME_EVENTS) {
            expect(catalogued.has(name), name).toBe(true);
            expect(name).not.toContain("*");
        }
        expect([...FLEET_EVENTS].sort()).toEqual([...CATALOGUE.fleet].sort());
        expect(REALTIME_EVENTS).not.toContain("session.revoked"); // the session stream handles it
    });

    it("global master data: hubs (maps included), customers, companies", () => {
        expect(keysOf("hubs.changed", { ids: ["h1"] })).toEqual([queryKeys.hubs.all()]);
        expect(reaches(keysOf("hubs.changed"), queryKeys.hubs.maps())).toBe(true);
        expect(keysOf("customers.changed")).toEqual([queryKeys.customers.all()]);
        expect(reaches(keysOf("customers.changed"), queryKeys.customers.detail("c1"))).toBe(true);
        expect(keysOf("companies.changed")).toEqual([queryKeys.companies.all()]);
        expect(reaches(keysOf("companies.changed"), queryKeys.companies.owner())).toBe(true);
        expect(reaches(keysOf("hubs.changed"), queryKeys.customers.all())).toBe(false);
    });

    it("Security Center (T54S): roles.changed refreshes ['me'] and every matrix; mobile_settings.changed the gate, releases and installations", () => {
        expect(keysOf("roles.changed", { tenantId: "t1" })).toEqual(ROLES_CHANGED_KEYS);
        expect(reaches(keysOf("roles.changed"), queryKeys.me())).toBe(true);
        expect(reaches(keysOf("roles.changed"), queryKeys.roles.matrix({ layer: "default", tenantId: "" }))).toBe(true);
        expect(reaches(keysOf("roles.changed"), queryKeys.users.all())).toBe(false);
        expect(keysOf("mobile_settings.changed", { key: "mobile_app", flavor: "prod" })).toEqual(MOBILE_SETTINGS_KEYS);
        expect(reaches(keysOf("mobile_settings.changed"), queryKeys.mobileSettings())).toBe(true);
        expect(reaches(keysOf("mobile_settings.changed"), queryKeys.mobileReleases({ flavor: "" }))).toBe(true);
        expect(reaches(keysOf("mobile_settings.changed"), queryKeys.installations.list({ flavor: "", reach: "all" }))).toBe(true);
        expect(reaches(keysOf("mobile_settings.changed"), queryKeys.installations.stats({ since: "7d" }))).toBe(true);
        expect(reaches(keysOf("mobile_settings.changed"), queryKeys.me())).toBe(false);
    });

    it("every fleet event makes the fleet lists stale (drivers and trucks rows 23 and 30, details included)", () => {
        for (const name of FLEET_EVENTS) {
            const keys = keysOf(name, { entity: "truck", id: "t1" });
            expect(keys).toEqual(FLEET_KEYS);
            expect(reaches(keys, queryKeys.trucks.list({ status: "active" }))).toBe(true);
            expect(reaches(keys, queryKeys.trucks.detail("t1"))).toBe(true);
            expect(reaches(keys, queryKeys.drivers.list({ status: "active", q: "" }))).toBe(true);
            expect(reaches(keys, queryKeys.truckAssignments({ truckId: "t1" }))).toBe(true);
            expect(reaches(keys, queryKeys.renewals({ due: "30d" }))).toBe(true);
            expect(reaches(keys, queryKeys.maintenance({ status: "open" }))).toBe(true);
            // Never the master data or the operational lists.
            expect(reaches(keys, queryKeys.hubs.all())).toBe(false);
            expect(reaches(keys, queryKeys.tasks.all())).toBe(false);
        }
    });

    it("list invalidations use the 500 ms window (the priced events use the 2 s window, below)", () => {
        const priced = new Set(["trip.priced", "trip.repriced", "standby.priced"]);
        for (const name of REALTIME_EVENTS.filter((n) => n !== "job.updated" && !priced.has(n))) {
            for (const i of effectsOf({ event: name, data: {} }).invalidate) expect(i.windowMs).toBe(LIST_WINDOW_MS);
        }
    });

    it("an event outside the map, or a prototype name, has no effect", () => {
        for (const name of ["task.assigned", "trip.delivered", "chat.created", "constructor", "__proto__", "toString", ""]) {
            expect(effectsOf({ event: name, data: {} })).toEqual({ invalidate: [], write: [] });
        }
    });
});

describe("job.updated (user:{uid}, full payload)", () => {
    const job = (over: Record<string, unknown>) => ({ id: "j-1", type: "distances.compute", status: "running", progress: { done: 1, total: 4 }, ...over });

    it("writes ['jobs', id] with the job; a running job invalidates nothing", () => {
        const fx = effectsOf({ event: "job.updated", data: job({}) });
        expect(fx.invalidate).toEqual([]);
        expect(fx.write).toHaveLength(1);
        expect(fx.write[0].queryKey).toEqual(queryKeys.jobs("j-1"));
        expect(fx.write[0].update(undefined)).toEqual(job({}));
    });

    it("a succeeded job makes its type's keys stale", () => {
        expect(keysOf("job.updated", job({ status: "succeeded" }))).toEqual(JOB_SUCCESS_KEYS.get("distances.compute"));
        expect(reaches(keysOf("job.updated", job({ status: "succeeded" })), queryKeys.distances.byHub("h1"))).toBe(true);
        expect(keysOf("job.updated", job({ status: "failed" }))).toEqual([]);
        expect(keysOf("job.updated", job({ status: "succeeded", type: "storage.gc" }))).toEqual([]);
    });

    it("a late progress frame never reopens a finished job in the cache", () => {
        const late = effectsOf({ event: "job.updated", data: job({ status: "running" }) }).write[0];
        const done = job({ status: "succeeded" });
        expect(late.update(done)).toBe(done);
        const failed = effectsOf({ event: "job.updated", data: job({ status: "failed", error: "x" }) }).write[0];
        expect(failed.update(done)).toEqual(job({ status: "failed", error: "x" }));
    });

    it("ignores a payload without an id", () => {
        for (const data of [null, "x", [], {}, { id: "" }, { id: 7 }]) {
            expect(effectsOf({ event: "job.updated", data })).toEqual({ invalidate: [], write: [] });
        }
    });
});

describe("billing, expenses and income (M3, T42)", () => {
    it("ratecard.changed makes only the touched table's key stale, for that customer and the all view (R51)", () => {
        expect(keysOf("ratecard.changed", { billingPartyId: "p1", customerId: "c1", table: "entries" })).toEqual([
            queryKeys.rateCard.entries("c1"),
            queryKeys.rateCard.entries("all"),
        ]);
        expect(keysOf("ratecard.changed", { customerId: "c1", table: "fuelAdj" })).toEqual([queryKeys.rateCard.fuelAdj("c1"), queryKeys.rateCard.fuelAdj("all")]);
        expect(keysOf("ratecard.changed", { customerId: "c1", table: "serviceFees" })).toEqual([queryKeys.rateCard.serviceFees()]);
        expect(keysOf("ratecard.changed", { customerId: "c1", table: "standby" })).toEqual([queryKeys.rateCard.standby()]);
        const entries = keysOf("ratecard.changed", { customerId: "c1", table: "entries" });
        expect(reaches(entries, queryKeys.rateCard.entries("c2"))).toBe(false);
        expect(reaches(entries, queryKeys.rateCard.fuelAdj("c1"))).toBe(false);
        // An unknown table, or no payload, makes the whole rate card stale rather than none of it.
        expect(keysOf("ratecard.changed", { table: "other" })).toEqual([queryKeys.rateCard.all()]);
        expect(keysOf("ratecard.changed", null)).toEqual([queryKeys.rateCard.all()]);
    });

    it("statement events and documents.rendered make every statement list and detail stale (R89)", () => {
        for (const name of STATEMENT_EVENTS) {
            const keys = keysOf(name, { statementId: "s1" });
            expect(keys).toEqual([queryKeys.statements.all()]);
            expect(reaches(keys, queryKeys.statements.detail("s1"))).toBe(true);
            expect(reaches(keys, queryKeys.billing.rows("c1", 2026, 9))).toBe(false);
        }
    });

    it("expense events make the expense lists and the badges stale", () => {
        for (const name of EXPENSE_EVENTS) {
            const keys = keysOf(name);
            expect(reaches(keys, queryKeys.expenses({ status: "pending" }))).toBe(true);
            expect(reaches(keys, queryKeys.badges())).toBe(true);
        }
    });

    it("priced events refresh income with the 2 s window, never the billing rows or missing-date keys", () => {
        for (const name of ["trip.priced", "trip.repriced", "standby.priced"]) {
            const fx = effectsOf({ event: name, data: {} }).invalidate;
            expect(fx.every((i) => i.windowMs === MONITOR_WINDOW_MS)).toBe(true);
            const keys = fx.map((i) => i.queryKey);
            expect(reaches(keys, queryKeys.income({ from: "a" }))).toBe(true);
            expect(reaches(keys, queryKeys.billing.rows("c1", 2026, 9))).toBe(false);
            expect(reaches(keys, queryKeys.billing.missingDate("c1", 2026, 9))).toBe(false);
        }
        expect(reaches(keysOf("standby.priced"), queryKeys.billing.standbyDiag("c1", 2026, 9))).toBe(true);
    });

    it("a succeeded billing, document or Bangchak job makes its keys stale", () => {
        const done = (type: string) => keysOf("job.updated", { id: "j", type, status: "succeeded" });
        expect(reaches(done("billing.backfill-trips"), queryKeys.income({}))).toBe(true);
        expect(reaches(done("billing.backfill-standby"), queryKeys.billing.rows("c1", 2026, 9))).toBe(true);
        expect(reaches(done("documents.regenerate"), queryKeys.statements.detail("s1"))).toBe(true);
        expect(reaches(done("documents.shopee-report"), queryKeys.billing.shopee("c1", 2026, 9, "full"))).toBe(true);
        expect(reaches(done("bangchak.snapshot"), queryKeys.fuel.monthly())).toBe(true);
    });
});
