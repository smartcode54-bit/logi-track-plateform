/**
 * Truck assignments (the home truck of a driver) for the web (T25; Appendix B §B.2.8 as built by T23;
 * Appendix E §E.2.2 rows `/app/truck-assignment`, `/app/trucks/view`, `/app/drivers/view`, §E.6).
 *
 * Keys (`['truckAssignments', …]`; `assignment.*` and the other fleet events invalidate the prefix):
 * - `['truckAssignments', {status:'active'}]`: every active row (the page's table, the trucks list's
 *   driver column).
 * - `['truckAssignments', {view:'history', status, q}]` (infinite, 100 per page): the page's history,
 *   searched and filtered on the server (`?status&q`, TW8), rendered by the virtualised table.
 * - `['truckAssignments', {truckId}]` / `{driverId}`: one truck's or driver's history.
 *
 * Writes: `POST /v1/truck-assignments` and `POST /v1/truck-assignments/{id}/revoke`, one transaction
 * each on Go (the driver's home truck and status move with the row). Go is the only source (TW7
 * removed the Firestore adapter and the `masterdata` flag switch of T25).
 */
import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import { goFetch, goFetchEnvelope, goPath } from "@/lib/goFetch";
import { fetchAllPages } from "@/lib/goPages";
import { goNextPageParam } from "@/lib/goQuery";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";

export const TRUCK_ASSIGNMENTS_PATH = "/v1/truck-assignments";
export const ASSIGNMENTS_PAGE_SIZE = 100;

export type AssignmentStatus = "active" | "revoked" | "cancelled";

/** `GET /v1/truck-assignments` item (internal/fleet `Assignment` list row). */
export interface TruckAssignmentDTO {
    id: string;
    tenantId: string | null;
    truckId: string;
    truckPlate: string | null;
    truckModel: string | null;
    driverId: string;
    driverName: string | null;
    status: AssignmentStatus | string;
    adminUserId: string | null;
    /** Who made the assignment (the legacy `adminName`). */
    adminLabel: string | null;
    createdAt: string | null;
    revokedAt: string | null;
}

type ListFilter = { status?: AssignmentStatus; truckId?: string; driverId?: string };

function listAll(filter: ListFilter, signal?: AbortSignal) {
    return fetchAllPages<TruckAssignmentDTO>(TRUCK_ASSIGNMENTS_PATH, filter, { signal, maxPages: 100 });
}

/** `['truckAssignments', {status:'active'}]`. */
export const activeAssignmentsQuery = queryOptions({
    queryKey: queryKeys.truckAssignments({ status: "active" }),
    queryFn: ({ signal }) => listAll({ status: "active" }, signal),
    ...QUERY_POLICY.entity,
});

/** The history's server filters: `status` (`""` = every status) and the search `q` (`GET /v1/truck-assignments?status&q`). */
export type AssignmentHistoryFilter = {
    status: AssignmentStatus | "";
    q: string;
};

type HistoryParams = AssignmentHistoryFilter & { view: "history" };
type HistoryKey = readonly ["truckAssignments", HistoryParams];

/**
 * `['truckAssignments', {view:'history', status, q}]` (infinite): newest first, keyset, 100 per page.
 * The filters are in the key and the request (W9): a search reaches rows the browser has not loaded.
 */
export function assignmentHistoryQuery(filter: AssignmentHistoryFilter) {
    const params: HistoryParams = { view: "history", status: filter.status, q: filter.q.trim() };
    return infiniteQueryOptions({
        queryKey: queryKeys.truckAssignments(params) as HistoryKey,
        queryFn: ({ queryKey: [, p], signal, pageParam }) =>
            goFetchEnvelope<TruckAssignmentDTO[]>(TRUCK_ASSIGNMENTS_PATH, {
                signal,
                query: { status: p.status || undefined, q: p.q || undefined, limit: ASSIGNMENTS_PAGE_SIZE, cursor: pageParam },
            }),
        initialPageParam: undefined as string | undefined,
        getNextPageParam: goNextPageParam,
        ...QUERY_POLICY.entity,
    });
}

/** `['truckAssignments', {truckId}]` or `{driverId}`: one truck's or one driver's assignments. */
export function assignmentsOfQuery(of: { truckId: string } | { driverId: string }) {
    const id = "truckId" in of ? of.truckId : of.driverId;
    return queryOptions({
        queryKey: queryKeys.truckAssignments(of),
        queryFn: ({ signal }) => listAll(of, signal),
        ...QUERY_POLICY.entity,
        enabled: id !== "",
    });
}

export interface AssignInput {
    truckId: string;
    driverId: string;
    /** Legacy (the denormalised fields the retired Firestore row carried): never sent to Go. */
    driverName: string;
    truckPlate: string;
    truckModel: string;
    adminName: string;
}

/** Assigns a home truck (`409 driver_assigned` / `failed_precondition driver_inactive` on Go). */
export async function assignTruck({ truckId, driverId }: AssignInput): Promise<void> {
    await goFetch(TRUCK_ASSIGNMENTS_PATH, { method: "POST", body: { truckId, driverId } });
}

export async function revokeAssignment({ id }: Pick<TruckAssignmentDTO, "id" | "truckId" | "driverId">): Promise<void> {
    await goFetch(goPath`/v1/truck-assignments/${id}/revoke`, { method: "POST", body: {} });
}

/** The keys an assignment write changes: the rows, the trucks' driver column, the drivers' home truck. */
export const ASSIGNMENT_WRITE_KEYS = [queryKeys.truckAssignments(), queryKeys.trucks.all(), queryKeys.drivers.all()] as const;
