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
 * Still to come: tasks and trips with the 2 s monitor window (M2), billing, expenses and income (M3),
 * HR and holidays (M4), chats and the explicit `chat:{id}` topic (M5), roles, mobile settings,
 * vehicle locations, `platform:security` and badges (M6).
 */
import type { QueryKey } from "@tanstack/react-query";
import { queryKeys } from "../queryKeys";
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

// --- the map ---------------------------------------------------------------------------------------

const RULES = new Map<string, EventRule>([
    ["job.updated", { effects: jobEffects }],
    // global (R51): master data of every tenant.
    ["hubs.changed", { invalidate: [queryKeys.hubs.all()] }], // ['hubs','maps'] included
    ["customers.changed", { invalidate: [queryKeys.customers.all()] }],
    ["companies.changed", { invalidate: [queryKeys.companies.all()] }],
    ...FLEET_EVENTS.map((name) => [name, { invalidate: FLEET_KEYS }] as [string, EventRule]),
]);

/** Every event name the provider listens to (besides `session.revoked` and the control events). */
export const REALTIME_EVENTS: readonly string[] = [...RULES.keys()];

/** The cache effects of one frame; none for an event outside the map. */
export function effectsOf(frame: Pick<RealtimeFrame, "event" | "data">): CacheEffects {
    const rule = RULES.get(frame.event);
    if (!rule) return NONE;
    if ("effects" in rule) return rule.effects(frame.data);
    return { invalidate: invalidations(rule.invalidate, rule.windowMs), write: [] };
}
