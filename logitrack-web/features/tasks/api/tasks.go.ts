/**
 * The task boards over Go (T34, P2; routes of T30 and T31, Appendix B §B.2.11; developer-spec.md §10.7 rows
 * `['tasks', …]`; Appendix E §E.2.1 rows first-mile, line-haul, job-assign, §E.5 rows 5, 9, 11).
 *
 * `['tasks',{date,type,soc,hub}]` is one keyset list (`GET /v1/tasks?taskType&date&destination&sourceHub`, 200 per
 * page, "load more"): the plan day, the destination SOC and the source hub are the server's filters (W9: a filter is
 * in the key and the request, so a SOCN task past row 200 of a busy day is on the first page; a day listener with a
 * browser-side window of 100 or 150 rows is gone), the customer scope and the dispatcher projection are RLS (the
 * browser no longer filters by `customerScopeId`), and each row carries its driver's and helper's names, its
 * resolved places and its newest trip, so the board reads nothing else. Task numbers come from the server
 * (`next_task_seq()`, R10): the 500 ms count query is gone.
 *
 * Writes (`POST /v1/tasks`, `PATCH /v1/tasks/{id}`, `POST /v1/tasks/{id}/cancel`, `POST /v1/tasks/import`)
 * invalidate `['tasks']` and the monitor; the FCM push of an assignment comes from Go's outbox (the browser's
 * `notifyTaskUpdate` call is gone). SSE `task.*` on `tenant:{tid}:tasks` (dispatchers: `dispatch:{partyId}:tasks`)
 * makes `['tasks']` stale in every tab (lib/realtime/eventMap.ts), so a mobile check-in reaches the board within
 * the 500 ms window plus one fetch.
 */
import { infiniteQueryOptions, queryOptions, type QueryClient } from "@tanstack/react-query";
import { ApiError } from "@/lib/apiError";
import { goFetch, goPath } from "@/lib/goFetch";
import { goInfiniteQueryFn, goNextPageParam, type GoPage } from "@/lib/goQuery";
import { getQueryClient } from "@/lib/queryClient";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";

export const TASKS_PATH = "/v1/tasks";
export const TASK_BOARD_PAGE_SIZE = 200;

export type TaskType = "first_mile" | "line_haul";
export type TaskBoardType = TaskType | "all";
export const TASK_STATUSES = ["pending", "assigned", "checked_in", "in_transit", "completed", "cancelled"] as const;
export type TaskStatus = (typeof TASK_STATUSES)[number];
export type JobCategory = "PRIMARY" | "SUPPLEMENTARY";
export const VEHICLE_CLASSES = ["4W", "4WJ", "6WH", "10WH", "18WH", "VAN"] as const;

/** A resolved place (`{raw, key, label, resolved}`, the place rule of the monitor, ADR 0006). */
export interface PlaceDTO {
    raw: string | null;
    key: string;
    label: string;
    resolved: boolean;
}

/** A signed file of a task or trip (`{fileId, key, url}`, 15 min). */
export interface FileRefDTO {
    fileId: string;
    key: string | null;
    url: string | null;
}

/** One planned stop of a multi-drop task (stop 1 is the task's own destination). */
export interface TaskStopDTO {
    index: number;
    destination: string;
    destinationHubId: string | null;
    place: PlaceDTO;
    status: "pending" | "delivered" | "failed" | string;
    deliveredAt: string | null;
    sequence: number | null;
    sourceId: string | null;
    isCustom: boolean;
    destinationLinkedPartyId?: string | null;
}

/** `GET /v1/tasks` item (internal/operations `TaskView` for staff, `TaskBase` for a scope principal). */
export interface TaskDTO {
    id: string;
    tenantId: string;
    taskNo: string | null;
    taskType: TaskType;
    jobCategory: JobCategory | null;
    status: TaskStatus;
    planAt: string;
    /** The Bangkok plan day, `yyyy-MM-dd`. */
    planDate: string | null;
    planTime: string | null;
    actualPickupAt: string | null;
    sourceHub: string;
    sourceHubId: string | null;
    source: PlaceDTO;
    destination: string;
    destinationHubId: string | null;
    destinationSocKey: string | null;
    destinationPlace: PlaceDTO;
    truckId: string | null;
    truckType: string | null;
    licensePlate: string | null;
    driverId: string | null;
    driverName: string | null;
    helperDriverId: string | null;
    helperName: string | null;
    runOrder: number | null;
    checkInAt: string | null;
    checkInLat: number | null;
    checkInLng: number | null;
    isMultiDelivery: boolean;
    stops: TaskStopDTO[];
    trip: { id: string; tripNo: string; status: string } | null;
    createdAt: string;
    updatedAt: string;
    /** Detail only (`GET /v1/tasks/{id}`): the signed check-in photo and app screenshot. */
    checkInPhoto?: FileRefDTO;
    checkInAppScreenshot?: FileRefDTO;
    // staff only
    tenantSource?: string;
    billingPartyId?: string | null;
    sourceLinkedPartyId?: string | null;
    destinationLinkedPartyId?: string | null;
    cancelledAt?: string | null;
    cancelReason?: string | null;
}

/** The "no filter" value of the board's SOC and source-hub filters. */
export const BOARD_ALL = "all";

export interface TaskBoardFilter {
    /** A Bangkok plan day `yyyy-MM-dd`, or "" for the newest tasks of any day. */
    date: string;
    type: TaskBoardType;
    /** The destination SOC key (`SOCE` / `SOCN` / `SOCW`), or "all". */
    soc: string;
    /** The source hub's code, or "all". */
    hub: string;
}

type TaskBoardKey = readonly ["tasks", TaskBoardFilter];

/** A board filter value as Go's place filter (`code:<CODE>`, the monitor's vocabulary), undefined for "all". */
function placeParam(v: string): string | undefined {
    const code = v.trim();
    return code && code !== BOARD_ALL ? `code:${code}` : undefined;
}

/**
 * `['tasks',{date,type,soc,hub}]`: the board's keyset list, newest first (a day lists on the plan axis, no day on
 * `created_at`). `type: 'all'` (job-assign) sends no `taskType`: Go lists the types the caller reads. The SOC and
 * the source hub are Go's `destination` / `sourceHub` place filters, matched on the task's own resolved places.
 */
export function taskBoardQuery(filter: TaskBoardFilter, enabled = true) {
    return infiniteQueryOptions({
        queryKey: queryKeys.tasks.list({ date: filter.date, type: filter.type, soc: filter.soc, hub: filter.hub }) as TaskBoardKey,
        queryFn: goInfiniteQueryFn<TaskDTO, TaskBoardKey>(TASKS_PATH, {
            query: ([, f]) => ({
                taskType: f.type === "all" ? undefined : f.type,
                date: f.date || undefined,
                destination: placeParam(f.soc),
                sourceHub: placeParam(f.hub),
                limit: TASK_BOARD_PAGE_SIZE,
            }),
        }),
        initialPageParam: undefined as string | undefined,
        getNextPageParam: goNextPageParam,
        ...QUERY_POLICY.live,
        enabled,
    });
}

/** `['tasks','detail',id]`: the task with its signed check-in photo (`enabled` while its dialog is open). */
export function taskDetailQuery(id: string, enabled = true) {
    return queryOptions({
        queryKey: queryKeys.tasks.detail(id),
        queryFn: ({ signal }) => goFetch<TaskDTO>(goPath`/v1/tasks/${id}`, { signal }),
        ...QUERY_POLICY.live,
        enabled: enabled && Boolean(id),
    });
}

/**
 * `['tasks','trip',taskId]`: the task's newest trip as `GET /v1/trips/{id}` answers it, or `null` when the task
 * has none (Go answers 404). The type is the monitor's trip detail (features/drivers/api/monitor.go.ts).
 */
export function taskTripQuery<T>(taskId: string, enabled = true) {
    return queryOptions({
        queryKey: queryKeys.tasks.trip(taskId),
        queryFn: async ({ signal }): Promise<T | null> => {
            try {
                return await goFetch<T>(goPath`/v1/tasks/${taskId}/trip`, { signal });
            } catch (err) {
                if (err instanceof ApiError && err.status === 404) return null;
                throw err;
            }
        },
        ...QUERY_POLICY.live,
        enabled: enabled && Boolean(taskId),
    });
}

export function pageItems<T>(pages: GoPage<T>[] | undefined): T[] {
    return pages?.flatMap((p) => p.data ?? []) ?? [];
}

// --- writes ----------------------------------------------------------------------------------------

/** A planned stop of a task write (`deliveryStops[]`). */
export interface TaskStopInput {
    destination: string;
    destinationHubId?: string;
}

/** The body of `POST /v1/tasks` (and the fields `PATCH /v1/tasks/{id}` takes). */
export interface TaskInput {
    taskType: TaskType;
    /** The Bangkok plan day `yyyy-MM-dd`. */
    date: string;
    time?: string;
    actualPickupAt?: string | null;
    sourceHub: string;
    sourceHubId?: string;
    destination: string;
    destinationHubId?: string;
    /** The billing party (ADR 0027: chosen at assign). */
    billingPartyId: string;
    jobCategory: JobCategory;
    truckId?: string | null;
    truckType?: string | null;
    driverId?: string | null;
    helperDriverId?: string | null;
    isMultiDelivery?: boolean;
    deliveryStops?: TaskStopInput[];
}

/** `PATCH /v1/tasks/{id}`: the named fields only; `null` clears a link. `taskType` is not settable (422). */
export type TaskPatch = Partial<Omit<TaskInput, "taskType">> & { status?: "pending" | "assigned"; runOrder?: number };

/** Drops `undefined` (absent) and `""` (no value) fields; `null` stays (it clears). */
export function compactBody<T extends object>(v: T): T {
    return Object.fromEntries(Object.entries(v).filter(([, x]) => x !== undefined && x !== "")) as T;
}

/** The keys a task write makes stale: the boards and their details, and the monitor rows that join tasks. */
export async function invalidateTasks(client: QueryClient = getQueryClient()): Promise<void> {
    await Promise.all([
        client.invalidateQueries({ queryKey: queryKeys.tasks.all() }),
        client.invalidateQueries({ queryKey: ["trips", "monitor"] }),
    ]);
}

export async function createTask(input: TaskInput, client: QueryClient = getQueryClient()): Promise<TaskDTO> {
    const out = await goFetch<TaskDTO>(TASKS_PATH, { method: "POST", body: compactBody(input), idempotencyKey: true });
    await invalidateTasks(client);
    return out;
}

export async function patchTask(id: string, patch: TaskPatch, client: QueryClient = getQueryClient()): Promise<TaskDTO> {
    const out = await goFetch<TaskDTO>(goPath`/v1/tasks/${id}`, { method: "PATCH", body: patch });
    await invalidateTasks(client);
    return out;
}

export async function cancelTask(id: string, reason: string | undefined, client: QueryClient = getQueryClient()): Promise<TaskDTO> {
    const out = await goFetch<TaskDTO>(goPath`/v1/tasks/${id}/cancel`, {
        method: "POST",
        body: reason ? { reason } : {},
        idempotencyKey: true,
    });
    await invalidateTasks(client);
    return out;
}

/** One row of `POST /v1/tasks/import` (the board's sheet; Go validates and numbers each row). */
export interface ImportRowInput {
    date: string;
    time?: string;
    sourceHub: string;
    destination: string;
    billingPartyId: string;
    /** The sheet's cell as typed (หลัก / เสริม / primary / supplementary; blank = PRIMARY). */
    jobCategory?: string;
    truckType?: string;
    plate?: string;
    taskNo?: string;
    note?: string;
}

export interface ImportFieldViolation {
    field: string;
    reason: string;
    params?: Record<string, unknown>;
}

export interface ImportResult {
    created: { row: number; id?: string; taskNo?: string }[];
    errors: { row: number; code: string; fields?: ImportFieldViolation[] }[];
    dryRun: boolean;
    pending?: boolean;
}

/** Go's cap of one import request (internal/operations `MaxImportRows`). */
export const IMPORT_MAX_ROWS = 1000;

/**
 * `POST /v1/tasks/import` of one type's rows. A write carries an Idempotency-Key: `idempotencyKey` (the dialog keeps one
 * per type group and body, so a retry of a group whose answer was lost replays Go's stored answer instead of creating
 * the tasks twice), else a fresh one.
 */
export async function importTasks(
    taskType: TaskType,
    rows: ImportRowInput[],
    dryRun: boolean,
    client: QueryClient = getQueryClient(),
    idempotencyKey?: string
): Promise<ImportResult> {
    const out = await goFetch<ImportResult>(`${TASKS_PATH}/import`, {
        method: "POST",
        body: { taskType, rows: rows.map((r) => compactBody(r)), dryRun },
        idempotencyKey: dryRun ? undefined : (idempotencyKey ?? true),
    });
    if (!dryRun) await invalidateTasks(client);
    return out;
}

// --- pure helpers ------------------------------------------------------------------------------------

/** `FM` / `LH`, the legacy prefix of a type (the number already carries it). */
export function taskTypeShort(type: TaskType | string): string {
    return type === "line_haul" ? "LH" : "FM";
}

/** The legacy literal of a status (`Checked in`, ...), the keys of `firstMile.status.*`. */
export const LEGACY_TASK_STATUS: Record<TaskStatus, string> = {
    pending: "Pending",
    assigned: "Assigned",
    checked_in: "Checked in",
    in_transit: "In-Transit",
    completed: "Completed",
    cancelled: "Cancelled",
};

/** A plan instant's `HH:mm` (the plan time, else the actual pickup), as the board's time column shows it. */
export function boardTime(task: Pick<TaskDTO, "planTime" | "actualPickupAt">): string {
    if (task.planTime) return task.planTime.slice(0, 5);
    if (task.actualPickupAt) {
        const d = new Date(task.actualPickupAt);
        if (!Number.isNaN(d.getTime())) return d.toTimeString().slice(0, 5);
    }
    return "";
}
