/**
 * The event -> cache map of the realtime bridge (developer-spec.md §10.8; Appendix E §E.5.1 "Event ->
 * cache", Appendix B §B.4.2-§B.4.3, which name the events): which query keys an SSE event makes stale
 * (`invalidateQueries`, coalesced per key) and which cache entries it writes (`setQueryData`, for the
 * few events whose payload is the data: jobs; later chat messages and vehicle locations).
 *
 * Payloads are thin (`entity`, `id`, `changed[]`): an event only says that something changed, and the
 * observed queries of the key refetch through REST. Invalidation never refetches an unobserved query;
 * it is marked stale and fetched on its next use (lib/realtime/invalidator.ts).
 *
 * EventSource delivers a named event only to listeners of that exact name, so every row names its
 * events in full (no `task.*` patterns); `REALTIME_EVENTS` is what the provider listens to.
 *
 * Rows are added with the milestone that ships their producer and moves their pages (issue #85
 * per-phase checklist). As of the M1 slice (TW5):
 * - `user:{uid}`: `session.revoked` (lib/realtime/sessionRevoked.ts, not a cache row) and `job.updated`;
 * - `global`: `hubs.changed`, `customers.changed`, `companies.changed` (T20, T21);
 * - `tenant:{tid}:fleet`: the truck, assignment, driver and maintenance events (T22, T23; maintenance
 *   from T40), which make the fleet lists stale (drivers and trucks also poll, rows 23 and 30).
 * - the Security Center slice pulled forward from M6 (T54S, owner request 2026-10-11): `roles.changed`
 *   on `tenant:{tid}:config` (T51's matrix save) and `mobile_settings.changed` on `global` (T52S's floor
 *   write and release publish).
 * - the M3 slice (T42, the accounting pages): `tenant:{tid}:billing` (`ratecard.changed` makes only the
 *   touched `['rateCard', table, customer]` key stale; `statement.created`, `statement.status_changed`,
 *   `documents.rendered` the statements), `tenant:{tid}:expenses` (`expense.*`: the expense lists and
 *   the badges), the priced events on `tenant:{tid}:trips` (`trip.priced`, `trip.repriced`,
 *   `standby.priced`: `['income']` with the 2 s window, `standby.priced` also the standby diagnostics;
 *   never `['billing','rows' | 'missingDate']`, R89) and the success keys of the billing, document and
 *   Bangchak jobs. The priced events' producer is T38 (compute-on-delivery: billing.compute and the billing
 *   endpoints append them on the trips topic of the tenant that ran the record and of its billing carrier).
 * - the M2 slice (T34, the operations pages; producers T31 and T33): `task.*` on `tenant:{tid}:tasks`
 *   (dispatchers `dispatch:{partyId}:tasks`) makes the boards stale (`['tasks']`, 500 ms) and the monitor
 *   (`['trips','monitor']`, 2 s); `trip.*`, `standby.completed`, `incident.created` on `tenant:{tid}:trips`
 *   (`dispatch:{partyId}:trips`) make the monitor stale (2 s), the trip's detail and the boards' trip column
 *   (500 ms), the standby and incident lists (500 ms); the priced events also make the monitor stale (its
 *   stored price), besides `['income']`.
 * Still to come: HR and holidays (M4), chats and the explicit `chat:{id}` topic (M5), vehicle locations,
 * `platform:security` (no producer yet: the security pages poll) and badges (M6).
 */
import type { QueryKey } from "@tanstack/react-query";
import { queryKeys } from "../queryKeys";
import { isRateCardTable, rateCardChangedKeys } from "@/features/accounting/api/rateCardKeys";
import type { RealtimeFrame } from "./realtimeStream";

/** Coalescing window of a list invalidation (Appendix E §E.5.1 "Backpressure": 500 ms). */
export const LIST_WINDOW_MS = 500;
/** The window of the monitor trips and income keys (2 s, from M2 and M3). */
export const MONITOR_WINDOW_MS = 2_000;

export interface Invalidation {
    queryKey: QueryKey;
    /** Events for the same key inside this window cause one invalidation at its end. */
    windowMs: number;
}

export interface CacheWrite {
    queryKey: QueryKey;
    /** `setQueryData` updater: the new value from the cached one (`undefined` when none). */
    update: (cached: unknown) => unknown;
}

export interface CacheEffects {
    invalidate: Invalidation[];
    write: CacheWrite[];
}

const NONE: CacheEffects = { invalidate: [], write: [] };

type EventRule =
    | { invalidate: readonly QueryKey[]; windowMs?: number }
    /** Keys with their own windows (the monitor's 2 s beside the lists' 500 ms). */
    | { windows: readonly Invalidation[] }
    /** Effects that depend on the payload (`data` is the envelope's `data`, untrusted). */
    | { effects: (data: unknown) => CacheEffects };

function isRecord(v: unknown): v is Record<string, unknown> {
    return typeof v === "object" && v !== null && !Array.isArray(v);
}

const invalidations = (keys: readonly QueryKey[], windowMs = LIST_WINDOW_MS): Invalidation[] =>
    keys.map((queryKey) => ({ queryKey, windowMs }));

// --- user:{uid} ------------------------------------------------------------------------------------

/** Statuses after which a job never changes again (internal/jobs). */
const TERMINAL_JOB_STATUSES = new Set(["succeeded", "failed"]);

/**
 * Keys a succeeded job of each type makes stale (`job.updated`, Appendix E §E.5.1). Only jobs a
 * user starts reach the stream (scheduler jobs have no owner); each milestone adds the jobs it ships.
 */
export const JOB_SUCCESS_KEYS: ReadonlyMap<string, readonly QueryKey[]> = new Map<string, readonly QueryKey[]>([
    // POST /v1/distances/compute (T21): the hub-SOC distance matrix and its meta.
    ["distances.compute", [queryKeys.distances.all()]],
    // T42 (M3): the Bangchak snapshot (rate card, fuel pages), the billing backfills (income, billing
    // aggregates), a document regeneration (the statement) and the Shopee report render.
    ["bangchak.snapshot", [["fuel"]]],
    ["billing.backfill-trips", [["income"], ["billing"]]],
    ["billing.backfill-standby", [["income"], ["billing"]]],
    ["documents.regenerate", [queryKeys.statements.all()]],
    ["documents.shopee-report", [["billing", "shopee"]]],
]);

/**
 * `job.updated` carries the whole job (`{id, type, status, progress, result?, error?}`, Appendix B
 * §B.4.3): it becomes `['jobs', id]`, unless the cache already holds that job finished (a late
 * progress frame never reopens a finished job); a succeeded job also makes its type's keys stale.
 */
function jobEffects(data: unknown): CacheEffects {
    if (!isRecord(data) || typeof data.id !== "string" || data.id === "") return NONE;
    const job = data;
    const write: CacheWrite = {
        queryKey: queryKeys.jobs(job.id as string),
        update: (cached) =>
            isRecord(cached) && TERMINAL_JOB_STATUSES.has(String(cached.status)) && !TERMINAL_JOB_STATUSES.has(String(job.status))
                ? cached
                : job,
    };
    const keys = job.status === "succeeded" && typeof job.type === "string" ? (JOB_SUCCESS_KEYS.get(job.type) ?? []) : [];
    return { invalidate: invalidations(keys), write: [write] };
}

// --- tenant:{tid}:fleet ----------------------------------------------------------------------------

/** The fleet lists: trucks (detail included), drivers, truck assignments, renewals, maintenance. */
export const FLEET_KEYS: readonly QueryKey[] = [
    queryKeys.trucks.all(),
    queryKeys.drivers.all(),
    queryKeys.truckAssignments(),
    queryKeys.renewals(),
    queryKeys.maintenance(),
];

/** The events of `tenant:{tid}:fleet` (Appendix B §B.4.2). */
export const FLEET_EVENTS = [
    "truck.created",
    "truck.updated",
    "truck.renewed",
    "assignment.created",
    "assignment.revoked",
    "driver.updated",
    "maintenance.created",
    "maintenance.updated",
    "maintenance.driver_submitted",
] as const;

// --- Security Center (T54S) ------------------------------------------------------------------------

/**
 * `roles.changed` (`tenant:{tid}:config`, a matrix save of the tenant's layer or the platform-wide one):
 * the caller's own capabilities may have changed (`['me']`) and every matrix view is stale (`['roles']`).
 */
export const ROLES_CHANGED_KEYS: readonly QueryKey[] = [queryKeys.me(), queryKeys.roles.all()];

/**
 * `mobile_settings.changed` (`global`, a floor change or a publish): the gate (`['mobileSettings']`), the
 * release page that shows it (`['mobileReleases']`), and every installation, whose status follows it.
 */
export const MOBILE_SETTINGS_KEYS: readonly QueryKey[] = [
    queryKeys.mobileSettings(),
    queryKeys.mobileReleases(),
    queryKeys.installations.all(),
];

// --- billing and expenses (T42, M3) ---------------------------------------------------------------

/**
 * `ratecard.changed {billingPartyId, customerId, table}` (tenant:{tid}:billing): only the matching
 * `['rateCard', table, customer]` key (and that table's `all` view) is stale (R51); an unknown table
 * makes the whole rate card stale rather than none of it.
 */
function rateCardEffects(data: unknown): CacheEffects {
    if (!isRecord(data)) return { invalidate: invalidations([queryKeys.rateCard.all()]), write: [] };
    const customerId = typeof data.customerId === "string" && data.customerId !== "" ? data.customerId : null;
    const keys = isRateCardTable(data.table) ? rateCardChangedKeys(data.table, customerId) : [queryKeys.rateCard.all()];
    return { invalidate: invalidations(keys), write: [] };
}

/** `statement.*` and `documents.rendered`: every statement list and detail (R89). */
export const STATEMENT_EVENTS = ["statement.created", "statement.status_changed", "documents.rendered"] as const;

/** `expense.*` (tenant:{tid}:expenses): the expense lists and the audit badge. */
export const EXPENSE_EVENTS = ["expense.created", "expense.updated", "expense.approved", "expense.status_changed"] as const;
export const EXPENSE_KEYS: readonly QueryKey[] = [["expenses"], queryKeys.badges()];

/** The priced events reach `['income']` (2 s window); never the billing rows or missing-date keys. */
export const PRICED_EVENTS = ["trip.priced", "trip.repriced"] as const;

// --- operations (T34, M2) --------------------------------------------------------------------------

/** The monitor's rows (`['trips','monitor', …]`), refreshed at most once per 2 s under a steady stream. */
export const MONITOR_KEY: QueryKey = ["trips", "monitor"];

/** `task.*` (tenant:{tid}:tasks, dispatch:{partyId}:tasks; Appendix B §B.4.2). */
export const TASK_EVENTS = [
    "task.created",
    "task.updated",
    "task.assigned",
    "task.checked_in",
    "task.cancelled",
    "task.reassigned",
    "task.plan_date_changed",
] as const;

/** `trip.*` besides the priced ones (tenant:{tid}:trips, dispatch:{partyId}:trips). */
export const TRIP_EVENTS = ["trip.created", "trip.updated", "trip.delivered", "trip.cancelled", "trip.resubmitted"] as const;

const TASK_WINDOWS: readonly Invalidation[] = [
    { queryKey: queryKeys.tasks.all(), windowMs: LIST_WINDOW_MS },
    { queryKey: MONITOR_KEY, windowMs: MONITOR_WINDOW_MS },
];

/** A trip change: the monitor (2 s), the trip's detail, the boards' trip column, standby and incidents. */
const TRIP_WINDOWS: readonly Invalidation[] = [
    { queryKey: MONITOR_KEY, windowMs: MONITOR_WINDOW_MS },
    { queryKey: ["trips", "detail"], windowMs: LIST_WINDOW_MS },
    { queryKey: ["tasks", "trip"], windowMs: LIST_WINDOW_MS },
    { queryKey: queryKeys.tasks.all(), windowMs: LIST_WINDOW_MS },
    { queryKey: queryKeys.standby(), windowMs: LIST_WINDOW_MS },
    { queryKey: queryKeys.incidents(), windowMs: LIST_WINDOW_MS },
];

const STANDBY_WINDOWS: readonly Invalidation[] = [
    { queryKey: MONITOR_KEY, windowMs: MONITOR_WINDOW_MS },
    { queryKey: queryKeys.standby(), windowMs: LIST_WINDOW_MS },
    { queryKey: ["billing", "standbyDiag"], windowMs: LIST_WINDOW_MS },
];

const INCIDENT_WINDOWS: readonly Invalidation[] = [
    { queryKey: MONITOR_KEY, windowMs: MONITOR_WINDOW_MS },
    { queryKey: queryKeys.incidents(), windowMs: LIST_WINDOW_MS },
];

const PRICED_WINDOWS: readonly Invalidation[] = [
    { queryKey: ["income"], windowMs: MONITOR_WINDOW_MS },
    { queryKey: MONITOR_KEY, windowMs: MONITOR_WINDOW_MS },
];

// --- the map ---------------------------------------------------------------------------------------

const RULES = new Map<string, EventRule>([
    ["job.updated", { effects: jobEffects }],
    // global (R51): master data of every tenant.
    ["hubs.changed", { invalidate: [queryKeys.hubs.all()] }], // ['hubs','maps'] included
    ["customers.changed", { invalidate: [queryKeys.customers.all()] }],
    ["companies.changed", { invalidate: [queryKeys.companies.all()] }],
    ["mobile_settings.changed", { invalidate: MOBILE_SETTINGS_KEYS }],
    // tenant:{tid}:config
    ["roles.changed", { invalidate: ROLES_CHANGED_KEYS }],
    ...FLEET_EVENTS.map((name) => [name, { invalidate: FLEET_KEYS }] as [string, EventRule]),
    // tenant:{tid}:billing (T42)
    ["ratecard.changed", { effects: rateCardEffects }],
    ...STATEMENT_EVENTS.map((name) => [name, { invalidate: [queryKeys.statements.all()] }] as [string, EventRule]),
    // tenant:{tid}:expenses (T42)
    ...EXPENSE_EVENTS.map((name) => [name, { invalidate: EXPENSE_KEYS }] as [string, EventRule]),
    // tenant:{tid}:trips, the priced events (T42; producer T38): income, and the monitor's stored price (T34)
    ...PRICED_EVENTS.map((name) => [name, { windows: PRICED_WINDOWS }] as [string, EventRule]),
    [
        "standby.priced",
        {
            windows: [
                ...PRICED_WINDOWS,
                { queryKey: ["billing", "standbyDiag"], windowMs: MONITOR_WINDOW_MS },
                { queryKey: queryKeys.standby(), windowMs: MONITOR_WINDOW_MS },
            ],
        },
    ],
    // tenant:{tid}:tasks and tenant:{tid}:trips, the operations events (T34; producers T31, T33)
    ...TASK_EVENTS.map((name) => [name, { windows: TASK_WINDOWS }] as [string, EventRule]),
    ...TRIP_EVENTS.map((name) => [name, { windows: TRIP_WINDOWS }] as [string, EventRule]),
    ["standby.completed", { windows: STANDBY_WINDOWS }],
    ["incident.created", { windows: INCIDENT_WINDOWS }],
]);

/** Every event name the provider listens to (besides `session.revoked` and the control events). */
export const REALTIME_EVENTS: readonly string[] = [...RULES.keys()];

/** The cache effects of one frame; none for an event outside the map. */
export function effectsOf(frame: Pick<RealtimeFrame, "event" | "data">): CacheEffects {
    const rule = RULES.get(frame.event);
    if (!rule) return NONE;
    if ("effects" in rule) return rule.effects(frame.data);
    if ("windows" in rule) return { invalidate: [...rule.windows], write: [] };
    return { invalidate: invalidations(rule.invalidate, rule.windowMs), write: [] };
}
