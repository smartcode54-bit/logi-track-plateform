"use client";

import { useEffect, useMemo, useState } from "react";
import { useIsFetching, useQueryClient } from "@tanstack/react-query";
import { queryKeys } from "@/lib/queryKeys";
import { useDebouncedValue } from "@/hooks/useDebouncedValue";
import { useActiveAssignments, useFleetStats, useTruckPages } from "../api/useTrucks";
import { classifyPM, type PMStatus } from "../utils/pm";
import type { Ownership, TruckDTO } from "../api/trucks";

export interface TruckRow extends TruckDTO {
    /** The truck's active assignments (`['truckAssignments',{status:'active'}]`). */
    drivers: Array<{ driverId: string; driverName: string | null }>;
}

/**
 * The trucks page (T25): `['trucks', filters]` keyset pages of 100 with the filters sent to the server
 * (`GET /v1/trucks?ownership&status&type&q`), polled every 60 s while visible (Appendix E §E.5 row 30,
 * replacing the `onSnapshot` listener capped at 100 rows); the drivers column joins the active
 * assignments. The table pages through the loaded rows; "load more" fetches the next keyset page.
 */
export function useTrucksList() {
    const queryClient = useQueryClient();

    // Filters
    const [searchQuery, setSearchQuery] = useState("");
    const [typeFilter, setTypeFilter] = useState("all");
    const [statusFilter, setStatusFilter] = useState("all");
    const [groupFilter, setGroupFilter] = useState("all");
    const [pmFilter, setPMFilter] = useState<PMStatus | null>(null);
    const q = useDebouncedValue(searchQuery.trim());

    // Pagination over the loaded rows
    const [currentPage, setCurrentPage] = useState(1);
    const itemsPerPage = 10;

    const pages = useTruckPages(
        {
            ownership: groupFilter === "own" || groupFilter === "subcontractor" ? (groupFilter as Ownership) : "",
            status: statusFilter === "all" ? "" : statusFilter,
            type: typeFilter === "all" ? "" : typeFilter,
            q,
        },
        { poll: true }
    );
    const fleet = useFleetStats();
    const assignments = useActiveAssignments();

    useEffect(() => setCurrentPage(1), [q, typeFilter, statusFilter, groupFilter, pmFilter]);

    const uniqueTypes = useMemo(() => {
        const types = new Set([...fleet.trucks, ...pages.trucks].map((t) => t.type).filter(Boolean));
        return Array.from(types).sort();
    }, [fleet.trucks, pages.trucks]);

    const filteredTrucks = useMemo<TruckRow[]>(() => {
        const byTruck = new Map<string, TruckRow["drivers"]>();
        for (const a of assignments.assignments) {
            const list = byTruck.get(a.truckId) ?? [];
            if (!list.some((d) => d.driverId === a.driverId)) list.push({ driverId: a.driverId, driverName: a.driverName });
            byTruck.set(a.truckId, list);
        }
        // PM has no Go list predicate: classify the complete fleet read, never just the loaded page.
        const source = pmFilter ? fleet.trucks.filter((truck) =>
            truck.ownershipType === "own" && classifyPM(truck).status === pmFilter &&
            (statusFilter === "all" || truck.status === statusFilter) &&
            (typeFilter === "all" || truck.type === typeFilter) &&
            (!q || [truck.licensePlate, truck.id, truck.model, truck.brand, truck.province].some((value) => value.toLowerCase().includes(q.toLowerCase())))
        ) : pages.trucks;
        return source.map((t) => ({ ...t, drivers: byTruck.get(t.id) ?? [] }));
    }, [pages.trucks, fleet.trucks, assignments.assignments, pmFilter, q, typeFilter, statusFilter]);

    const totalPages = Math.ceil(filteredTrucks.length / itemsPerPage);

    // The card filters set the list filters (the server applies them).
    const handleCardFilterChange = ({ type, status }: { type: string | null; status: string | null }) => {
        setPMFilter(null);
        if (!type && !status) {
            setGroupFilter("all");
            setStatusFilter("all");
            return;
        }
        const statusOf = (s: string | null) =>
            s === "active" ? "active" : s === "available" ? "inactive" : s === "corrective" || s === "pm" ? "maintenance" : "all";
        if (type === "sub") {
            setGroupFilter("subcontractor");
            setStatusFilter(statusOf(status));
        } else if (type === "own") {
            setGroupFilter("own");
            setStatusFilter(statusOf(status));
        } else if (type === "maintenance") {
            setPMFilter(status === "overdue" ? "overdue" : "due");
            setGroupFilter("own");
            setStatusFilter("all");
        }
    };

    // The pressed card describes the current list predicates, including changes made by other controls.
    const activeCardFilter = pmFilter && groupFilter === "own" && statusFilter === "all"
        ? { type: "maintenance", status: pmFilter }
        : !pmFilter && (groupFilter === "own" || groupFilter === "subcontractor") && ["active", "inactive", "maintenance"].includes(statusFilter)
            ? { type: groupFilter === "own" ? "own" : "sub", status: statusFilter === "inactive" ? "available" : statusFilter === "maintenance" ? "corrective" : "active" }
            : null;

    const clearFilters = () => {
        setPMFilter(null);
        setSearchQuery("");
        setTypeFilter("all");
        setStatusFilter("all");
        setGroupFilter("all");
        setCurrentPage(1);
    };

    useEffect(() => setCurrentPage((page) => Math.min(page, Math.max(1, totalPages))), [totalPages]);

    const source = pmFilter ? fleet : pages;
    const isRefreshing = useIsFetching({ queryKey: queryKeys.trucks.all() }) > 0;
    const refresh = () => {
        void queryClient.invalidateQueries({ queryKey: queryKeys.trucks.all() });
        void queryClient.invalidateQueries({ queryKey: queryKeys.truckAssignments() });
    };

    return {
        trucks: filteredTrucks,
        loading: source.isPending,
        error: source.isError,
        hasData: Boolean(source.data),
        assignmentsLoading: assignments.isPending,
        assignmentsError: assignments.isError,
        assignmentsHaveData: Boolean(assignments.data),
        isRefreshing,
        refresh,
        searchQuery,
        setSearchQuery,
        typeFilter,
        setTypeFilter,
        statusFilter,
        setStatusFilter,
        groupFilter,
        setGroupFilter: (value: string) => { setPMFilter(null); setGroupFilter(value); },
        currentPage,
        setCurrentPage,
        itemsPerPage,
        uniqueTypes,
        filteredTrucks,
        totalPages,
        hasNextPage: !pmFilter && pages.hasNextPage,
        isFetchingNextPage: pages.isFetchingNextPage,
        fetchNextPage: () => void pages.fetchNextPage(),
        handleCardFilterChange,
        activeCardFilter,
        clearFilters,
    };
}
