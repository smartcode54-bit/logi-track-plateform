/**
 * Truck assignments (the home truck of a driver) for the web (T25; Appendix B §B.2.8 as built by T23;
 * Appendix E §E.2.2 rows `/app/truck-assignment`, `/app/trucks/view`, `/app/drivers/view`, §E.6).
 *
 * Keys (`['truckAssignments', …]`; `assignment.*` and the other fleet events invalidate the prefix):
 * - `['truckAssignments', {status:'active'}]`: every active row (the page's table, the trucks list's
 *   driver column).
 * - `['truckAssignments', {view:'history'}]` (infinite, 100 per page): the page's history.
 * - `['truckAssignments', {truckId}]` / `{driverId}`: one truck's or driver's history.
 *
 * Writes: `POST /v1/truck-assignments` and `POST /v1/truck-assignments/{id}/revoke`, one transaction
 * each on Go (the driver's home truck and status move with the row). Flag `masterdata`; the Firestore
 * side is `./truckAssignments.firestore.ts`, loaded with `import()` only then.
 */
import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import { goFetch, goPath } from "@/lib/goFetch";
import { fetchAllPages } from "@/lib/goPages";
import { goInfiniteQueryFn, goNextPageParam, type GoPage } from "@/lib/goQuery";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
import { domainMutationFn, domainQueryFn } from "@/features/platform/api/webFlags";

const firestore = () => import("./truckAssignments.firestore");

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
    queryFn: domainQueryFn<TruckAssignmentDTO[]>("masterdata", {
        firestore: async () => (await firestore()).listActiveAssignmentsFirestore(),
        go: ({ signal }) => listAll({ status: "active" }, signal),
    }),
    ...QUERY_POLICY.entity,
});

type HistoryKey = ReturnType<typeof queryKeys.truckAssignments<{ view: "history" }>>;

/** `['truckAssignments', {view:'history'}]` (infinite): newest first, keyset. */
export const assignmentHistoryQuery = infiniteQueryOptions({
    queryKey: queryKeys.truckAssignments({ view: "history" }) as HistoryKey,
    queryFn: domainQueryFn<GoPage<TruckAssignmentDTO>, HistoryKey, string | undefined>("masterdata", {
        firestore: async () => ({ data: await (await firestore()).listAssignmentHistoryFirestore() }),
        go: goInfiniteQueryFn<TruckAssignmentDTO, HistoryKey>(TRUCK_ASSIGNMENTS_PATH, { query: { limit: ASSIGNMENTS_PAGE_SIZE } }),
    }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: goNextPageParam,
    ...QUERY_POLICY.entity,
});

/** `['truckAssignments', {truckId}]` or `{driverId}`: one truck's or one driver's assignments. */
export function assignmentsOfQuery(of: { truckId: string } | { driverId: string }) {
    const id = "truckId" in of ? of.truckId : of.driverId;
    return queryOptions({
        queryKey: queryKeys.truckAssignments(of),
        queryFn: domainQueryFn<TruckAssignmentDTO[]>("masterdata", {
            firestore: async () => (await firestore()).listAssignmentsOfFirestore(of),
            go: ({ signal }) => listAll(of, signal),
        }),
        ...QUERY_POLICY.entity,
        enabled: id !== "",
    });
}

export interface AssignInput {
    truckId: string;
    driverId: string;
    /** Firestore only: the legacy denormalised fields of the row. */
    driverName: string;
    truckPlate: string;
    truckModel: string;
    adminName: string;
}

/** Assigns a home truck (`409 driver_assigned` / `failed_precondition driver_inactive` on Go). */
export const assignTruck = domainMutationFn<void, AssignInput>("masterdata", {
    firestore: async (input) => (await firestore()).createAssignmentFirestore(input),
    go: async ({ truckId, driverId }) => {
        await goFetch(TRUCK_ASSIGNMENTS_PATH, { method: "POST", body: { truckId, driverId } });
    },
});

export const revokeAssignment = domainMutationFn<void, Pick<TruckAssignmentDTO, "id" | "truckId" | "driverId">>("masterdata", {
    firestore: async (a) => (await firestore()).revokeAssignmentFirestore(a),
    go: async ({ id }) => {
        await goFetch(goPath`/v1/truck-assignments/${id}/revoke`, { method: "POST", body: {} });
    },
});

/** The keys an assignment write changes: the rows, the trucks' driver column, the drivers' home truck. */
export const ASSIGNMENT_WRITE_KEYS = [queryKeys.truckAssignments(), queryKeys.trucks.all(), queryKeys.drivers.all()] as const;
