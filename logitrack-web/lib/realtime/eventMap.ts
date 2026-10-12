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
 * - the dashboard and waitlist slice of M6 (T54R): `vehicle_locations.updated` on
 *   `tenant:{tid}:vehicle_locations` (the Cartrack batch, data written into `['vehicleLocations']`; a
 *   refetch only when it names a truck the list lacks), `waitlist.created` on `platform:security`
 *   (`['waitlist']`, `['badges']`; tenant stewards poll), and the chat events of `tenant:{tid}:chats` for
 *   the dashboard's chat widget, which reads the chat counters of `['badges']` (Appendix E §E.5 row 21;
 *   the chat pages add their own keys in M5).
 * - the M2 slice (T34, the operations pages; producers T31 and T33): `task.*` on `tenant:{tid}:tasks`
 *   (dispatchers `dispatch:{partyId}:tasks`) makes the boards stale (`['tasks']`, 500 ms) and the monitor
 *   (`['trips','monitor']`, 2 s); `trip.*`, `standby.completed`, `incident.created` on `tenant:{tid}:trips`
 *   (`dispatch:{partyId}:trips`) make the monitor stale (2 s), the trip's detail and the boards' trip column
 *   (500 ms), the standby and incident lists (500 ms); the priced events also make the monitor stale (its
 *   stored price), besides `['income']`.
 * - the HR slice of M4 (T46, the payroll, penalty, leave and holiday pages): `tenant:{tid}:hr` (`leave.created`,
 *   `leave.decided` -> `['leave']`; `payroll.status_changed` -> `['payroll']`; `payroll.approved` -> `['payroll']`
 *   and `['penalties']`, whose installments the approval commits), `holidays.changed` (`global` for public rows,
 *   `tenant:{tid}:config` for a tenant's) -> `['holidays']`, and the `payroll.run` job's success -> `['payroll']`.
 *   The leave and payroll lists also poll every 60 s (Appendix E §E.5 rows 10, 12); holidays are a plain fetch.
 * - the comms slice of M5 (T50, the chat console; producer T47): `chat.created`, `chat.updated`,
 *   `chat.message_created` on `tenant:{tid}:chats` make the conversation lists (`['chats']`) and the badges
 *   stale, a chat's own change its `['chat', id]` (exact: never its loaded message pages); on the explicit
 *   `chat:{id}` of the open room (lib/realtime/topics.ts) `message.created` writes the whole message into
 *   `['chat', id, 'messages']` (deduped by id and `clientMessageId`, features/chat/api/chatCache.ts; an
 *   image message also refetches, the event carries no signed URL) and `read.updated` the reader's state
 *   into `['chat', id]`, both applied again on a fetch of the key that was running meanwhile
 *   (`reapplyAfterFetch`). When the open connection does not carry that `chat:{id}` (not joined yet, or Go
 *   refused it), the tenant topic's thin `chat.message_created` makes the room's messages and chat stale
 *   instead (`EffectsContext.hasTopic`), so the room never misses a message.
 * Still to come: `security.event` on `platform:security` (the security pages poll).
 */
import type { QueryKey } from "@tanstack/react-query";
import { queryKeys } from "../queryKeys";
import { isRateCardTable, rateCardChangedKeys } from "@/features/accounting/api/rateCardKeys";
import { mergeVehiclePositions, namesUnlistedTruck, parsePositions } from "@/features/dashboard/api/vehicleLocationEvents";
import { applyReadState, messageFromEvent, needsImageRefetch, readFromEvent, upsertMessage } from "@/features/chat/api/chatCache";
import type { RealtimeFrame } from "./realtimeStream";
import { chatTopic } from "./topics";

/** Coalescing window of a list invalidation (Appendix E §E.5.1 "Backpressure": 500 ms). */
export const LIST_WINDOW_MS = 500;
/** The window of the monitor trips and income keys (2 s, from M2 and M3). */
export const MONITOR_WINDOW_MS = 2_000;

export interface Invalidation {
    queryKey: QueryKey;
    /** Events for the same key inside this window cause one invalidation at its end. */
    windowMs: number;
    /** Only this key, not the keys below it (`['chat', id]` without `['chat', id, 'messages']`). */
    exact?: boolean;
}

/** What an event's effects may depend on besides its payload. */
export interface EffectsContext {
    /** Whether the open stream carries the explicit `topic` (Go accepted it at connect). */
    hasTopic?: (topic: string) => boolean;
}

export interface CacheWrite {
    queryKey: QueryKey;
    /** `setQueryData` updater: the new value from the cached one (`undefined` when none). */
    update: (cached: unknown) => unknown;
    /**
     * The payload carries only part of what the key holds, so the write is not a fresh answer: the
     * query keeps its `dataUpdatedAt`, and its staleness and `pollFromLastAnswer` interval still count
     * from the server's last answer (lib/realtime/polling.ts).
     */
    keepUpdatedAt?: boolean;
    /**
     * Whether the cached value (before the write) lacks something the payload names: the key is then
     * also made stale (one flush per `LIST_WINDOW_MS` window), so the refetch adds what the write cannot.
     */
    refetchWhen?: (cached: unknown) => boolean;
    /**
     * Apply the update again on the result of a fetch of the key that was running when the event arrived
     * (its server read may predate the event; lib/queryWrite.ts). Only for an idempotent, forward-only
     * update: a never-stale key the stream keeps fresh (T50: a chat's messages and read state).
     */
    reapplyAfterFetch?: boolean;
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
    | { effects: (data: unknown, ctx: EffectsContext) => CacheEffects };

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
    // T46 (M4): a generation rewrites the draft runs of its period and round.
    ["payroll.run", [queryKeys.payroll.all()]],
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

// --- dashboard and waitlist (T54R, M6) -------------------------------------------------------------

/**
 * `vehicle_locations.updated {positions: [...]}` (tenant:{tid}:vehicle_locations, every 3 min with the
 * Cartrack sync): the batch is written into `['vehicleLocations']` (Appendix E §E.5.1: vehicle-location
 * batches are trusted as data), each known truck taking the newer fix. The batch carries the position
 * only (not the address, engine, driver, odometer or fuel) and names no removed truck, so the write
 * keeps the query's `dataUpdatedAt` (its `pollFromLastAnswer` refetch still comes, useVehicleLocations),
 * and a batch naming a truck the cached list lacks (its first fix, a new GPS id) makes the key stale:
 * the batch has no plate or telemetry to add it with. A payload without a readable position makes the
 * key stale instead.
 */
function vehicleLocationEffects(data: unknown): CacheEffects {
    const positions = parsePositions(data);
    if (positions.length === 0) return { invalidate: invalidations([queryKeys.vehicleLocations()]), write: [] };
    return {
        invalidate: [],
        write: [
            {
                queryKey: queryKeys.vehicleLocations(),
                update: (cached) => mergeVehiclePositions(cached, positions),
                keepUpdatedAt: true,
                refetchWhen: (cached) => namesUnlistedTruck(cached, positions),
            },
        ],
    };
}

/** `waitlist.created` (platform:security): the waitlist page and the sidebar count. */
export const WAITLIST_KEYS: readonly QueryKey[] = [queryKeys.waitlist(), queryKeys.badges()];

/** `chat.*` (tenant:{tid}:chats): the chat counters of `['badges']` (the dashboard's chat widget) and the chat lists. */
export const CHAT_EVENTS = ["chat.created", "chat.updated", "chat.message_created"] as const;
export const CHAT_BADGE_KEYS: readonly QueryKey[] = [queryKeys.badges()];

// --- chats (T50, M5) ------------------------------------------------------------------------------

/** What every chat event of the tenant topic makes stale: both conversation lists and the badges. */
export const CHAT_LIST_KEYS: readonly QueryKey[] = [queryKeys.chats.all(), ...CHAT_BADGE_KEYS];

/** The events of the explicit `chat:{id}` topic (Appendix B §B.4.4: SSE names on that topic). */
export const CHAT_TOPIC_EVENTS = ["message.created", "read.updated"] as const;

const chatIdOf = (data: unknown, field: "id" | "chatId"): string | null =>
    isRecord(data) && typeof data[field] === "string" && data[field] !== "" ? (data[field] as string) : null;

/** `chat.created`, `chat.updated` {entity: chat, id, status, assignedAdminUserId, ...}: the lists, the badges, the chat. */
function chatChangedEffects(data: unknown): CacheEffects {
    const invalidate = invalidations(CHAT_LIST_KEYS);
    const id = chatIdOf(data, "id");
    if (id) invalidate.push({ queryKey: queryKeys.chat.detail(id), windowMs: LIST_WINDOW_MS, exact: true });
    return { invalidate, write: [] };
}

/**
 * `chat.message_created` (thin on tenant:{tid}:chats: `chatId`, sender role, no text): the lists and the
 * badges; and, when the stream does not carry the room's `chat:{id}` (whose `message.created` writes the
 * message itself), that chat's messages and read state, so an open room still shows it within the window.
 */
function chatMessageCreatedEffects(data: unknown, ctx: EffectsContext): CacheEffects {
    const invalidate = invalidations(CHAT_LIST_KEYS);
    const chatId = chatIdOf(data, "chatId");
    if (chatId && !ctx.hasTopic?.(chatTopic(chatId))) {
        invalidate.push(
            { queryKey: queryKeys.chat.messages(chatId), windowMs: LIST_WINDOW_MS },
            { queryKey: queryKeys.chat.detail(chatId), windowMs: LIST_WINDOW_MS, exact: true }
        );
    }
    return { invalidate, write: [] };
}

/** `message.created` (chat:{id}, the whole message): written into the open room's pages. */
function messageCreatedEffects(data: unknown): CacheEffects {
    const msg = messageFromEvent(data);
    if (!msg) {
        const chatId = chatIdOf(data, "chatId");
        return chatId ? { invalidate: invalidations([queryKeys.chat.messages(chatId)]), write: [] } : NONE;
    }
    return {
        invalidate: [],
        write: [
            {
                queryKey: queryKeys.chat.messages(msg.chatId),
                update: (cached) => upsertMessage(cached, msg),
                refetchWhen: (cached) => needsImageRefetch(cached, msg),
                reapplyAfterFetch: true,
            },
        ],
    };
}

/** `read.updated` (chat:{id}, `{userId, lastReadAt}`): the reader's state in `['chat', id]`. */
function readUpdatedEffects(data: unknown): CacheEffects {
    const read = readFromEvent(data);
    if (!read) return NONE;
    return {
        invalidate: [],
        write: [{ queryKey: queryKeys.chat.detail(read.chatId), update: (cached) => applyReadState(cached, read), reapplyAfterFetch: true }],
    };
}

// --- HR (T46, M4) ---------------------------------------------------------------------------------

/** `leave.*` (tenant:{tid}:hr): a request created in the driver app, or decided by staff. */
export const LEAVE_EVENTS = ["leave.created", "leave.decided"] as const;
/** `payroll.approved` commits the run's penalty installments in the same transaction: both lists are stale. */
export const PAYROLL_APPROVED_KEYS: readonly QueryKey[] = [queryKeys.payroll.all(), queryKeys.penalties()];

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
    // tenant:{tid}:vehicle_locations, platform:security, tenant:{tid}:chats (T54R)
    ["vehicle_locations.updated", { effects: vehicleLocationEffects }],
    ["waitlist.created", { invalidate: WAITLIST_KEYS }],
    // tenant:{tid}:chats and the explicit chat:{id} (T50)
    ["chat.created", { effects: chatChangedEffects }],
    ["chat.updated", { effects: chatChangedEffects }],
    ["chat.message_created", { effects: chatMessageCreatedEffects }],
    ["message.created", { effects: messageCreatedEffects }],
    ["read.updated", { effects: readUpdatedEffects }],
    // tenant:{tid}:hr, tenant:{tid}:config and global (T46)
    ...LEAVE_EVENTS.map((name) => [name, { invalidate: [queryKeys.leave()] }] as [string, EventRule]),
    ["payroll.status_changed", { invalidate: [queryKeys.payroll.all()] }],
    ["payroll.approved", { invalidate: PAYROLL_APPROVED_KEYS }],
    ["holidays.changed", { invalidate: [queryKeys.holidays.all()] }],
]);

/** Every event name the provider listens to (besides `session.revoked` and the control events). */
export const REALTIME_EVENTS: readonly string[] = [...RULES.keys()];

/** The cache effects of one frame; none for an event outside the map. */
export function effectsOf(frame: Pick<RealtimeFrame, "event" | "data">, ctx: EffectsContext = {}): CacheEffects {
    const rule = RULES.get(frame.event);
    if (!rule) return NONE;
    if ("effects" in rule) return rule.effects(frame.data, ctx);
    if ("windows" in rule) return { invalidate: [...rule.windows], write: [] };
    return { invalidate: invalidations(rule.invalidate, rule.windowMs), write: [] };
}
