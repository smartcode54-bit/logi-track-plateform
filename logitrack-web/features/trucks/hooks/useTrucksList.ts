"use client";

import { useEffect, useMemo, useState } from "react";
import { useIsFetching, useQueryClient } from "@tanstack/react-query";
import { queryKeys } from "@/lib/queryKeys";
import { useDebouncedValue } from "@/hooks/useDebouncedValue";
import { useActiveAssignments, useFleetStats, useTruckPages } from "../api/useTrucks";
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

    useEffect(() => setCurrentPage(1), [q, typeFilter, statusFilter, groupFilter]);

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
        return pages.trucks.map((t) => ({ ...t, drivers: byTruck.get(t.id) ?? [] }));
    }, [pages.trucks, assignments.assignments]);

    const totalPages = Math.ceil(filteredTrucks.length / itemsPerPage);

    // The card filters set the list filters (the server applies them).
    const handleCardFilterChange = ({ type, status }: { type: string | null; status: string | null }) => {
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
            setGroupFilter("own");
            setStatusFilter("all");
        }
    };

    const clearFilters = () => {
        setSearchQuery("");
        setTypeFilter("all");
        setStatusFilter("all");
        setGroupFilter("all");
        setCurrentPage(1);
    };

    const isRefreshing = useIsFetching({ queryKey: queryKeys.trucks.all() }) > 0;
    const refresh = () => {
        void queryClient.invalidateQueries({ queryKey: queryKeys.trucks.all() });
        void queryClient.invalidateQueries({ queryKey: queryKeys.truckAssignments() });
    };

    return {
        trucks: filteredTrucks,
        loading: pages.isPending,
        error: pages.isError,
        isRefreshing,
        refresh,
        searchQuery,
        setSearchQuery,
        typeFilter,
        setTypeFilter,
        statusFilter,
        setStatusFilter,
        groupFilter,
        setGroupFilter,
        currentPage,
        setCurrentPage,
        itemsPerPage,
        uniqueTypes,
        filteredTrucks,
        totalPages,
        hasNextPage: pages.hasNextPage,
        isFetchingNextPage: pages.isFetchingNextPage,
        fetchNextPage: () => void pages.fetchNextPage(),
        handleCardFilterChange,
        clearFilters,
    };
}
