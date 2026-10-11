"use client";

import { useMemo } from "react";
import { keepPreviousData, useInfiniteQuery, useMutation, useQuery } from "@tanstack/react-query";
import { queryKeys } from "@/lib/queryKeys";
import { pollWhileVisible } from "@/lib/realtime/polling";
import {
    createTruck,
    fleetStatsQuery,
    importTrucks,
    renewalsQuery,
    renewTruck,
    truckOptionsQuery,
    truckPagesQuery,
    truckQuery,
    updateTruck,
    type RenewalDTO,
    type TruckDTO,
    type TruckListFilter,
    type TruckOption,
} from "./trucks";
import {
    activeAssignmentsQuery,
    assignmentHistoryQuery,
    assignmentsOfQuery,
    assignTruck,
    ASSIGNMENT_WRITE_KEYS,
    revokeAssignment,
    type TruckAssignmentDTO,
} from "./truckAssignments";

const NO_TRUCKS: TruckDTO[] = [];
const NO_OPTIONS: TruckOption[] = [];
const NO_RENEWALS: RenewalDTO[] = [];
const NO_ASSIGNMENTS: TruckAssignmentDTO[] = [];

/**
 * `['trucks', filters]` (infinite): keyset pages of 100, no cap. `poll` (the trucks page, Appendix E
 * §E.5 row 30) refetches every 60 s while the tab is visible and on focus once older than that.
 */
export function useTruckPages(filter: TruckListFilter, options: { enabled?: boolean; poll?: boolean } = {}) {
    const query = useInfiniteQuery({
        ...truckPagesQuery(filter),
        placeholderData: keepPreviousData,
        enabled: options.enabled ?? true,
        ...(options.poll ? pollWhileVisible() : {}),
    });
    const trucks = useMemo(() => query.data?.pages.flatMap((p) => p.data ?? []) ?? NO_TRUCKS, [query.data]);
    return { ...query, trucks };
}

/** `['trucks','fleetStats']`: every truck, for the counters. */
export function useFleetStats() {
    const query = useQuery(fleetStatsQuery);
    return { ...query, trucks: query.data ?? NO_TRUCKS };
}

/** `['trucks','options']`: own trucks for the truck-assignment picker. */
export function useTruckOptions(enabled = true) {
    const query = useQuery({ ...truckOptionsQuery, enabled });
    return { ...query, options: query.data ?? NO_OPTIONS };
}

/** `['trucks','detail',id]`. */
export function useTruck(id: string) {
    return useQuery(truckQuery(id));
}

/** `['renewals', {}]`. */
export function useRenewals() {
    const query = useQuery(renewalsQuery);
    return { ...query, renewals: query.data ?? NO_RENEWALS };
}

/** `['truckAssignments', {status:'active'}]`. */
export function useActiveAssignments(enabled = true) {
    const query = useQuery({ ...activeAssignmentsQuery, enabled });
    return { ...query, assignments: query.data ?? NO_ASSIGNMENTS };
}

/** `['truckAssignments', {view:'history'}]` (infinite). */
export function useAssignmentHistory() {
    const query = useInfiniteQuery(assignmentHistoryQuery);
    const rows = useMemo(() => query.data?.pages.flatMap((p) => p.data ?? []) ?? NO_ASSIGNMENTS, [query.data]);
    return { ...query, rows };
}

/** One truck's or one driver's assignments. */
export function useAssignmentsOf(of: { truckId: string } | { driverId: string }) {
    const query = useQuery(assignmentsOfQuery(of));
    return { ...query, assignments: query.data ?? NO_ASSIGNMENTS };
}

const FLEET_WRITE_KEYS = [queryKeys.trucks.all(), queryKeys.renewals()];

/** Truck create, update, renewal and import; each makes the truck and renewal keys stale. */
export function useTruckWrites() {
    const meta = { invalidates: FLEET_WRITE_KEYS };
    return {
        create: useMutation({ mutationFn: createTruck, meta }),
        update: useMutation({ mutationFn: updateTruck, meta }),
        renew: useMutation({ mutationFn: renewTruck, meta }),
        importRows: useMutation({ mutationFn: importTrucks, meta }),
    };
}

/** Assign and revoke; each makes the assignment, truck and driver keys stale. */
export function useAssignmentWrites() {
    const meta = { invalidates: ASSIGNMENT_WRITE_KEYS };
    return {
        assign: useMutation({ mutationFn: assignTruck, meta }),
        revoke: useMutation({ mutationFn: revokeAssignment, meta }),
    };
}
