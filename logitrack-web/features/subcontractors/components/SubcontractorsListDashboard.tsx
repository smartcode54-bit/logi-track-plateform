"use client";

import { useState, useMemo } from "react";
import type { SubcontractorSummaryDTO } from "../api/subcontractors";
import { useSubcontractorList } from "../api/useSubcontractors";
import { Button } from "@/components/ui/button";
import { Plus } from "lucide-react";
import Link from "next/link";
import { useLanguage } from "@/context/language";
import { PageHeader } from "@/components/page-header";
import { LoadingState, QueryErrorState } from "./PartnerQueryStates";

// Sub-components
import { SubcontractorStats } from "./SubcontractorStats";
import { SubcontractorFilter, UNSPECIFIED_REGION } from "./SubcontractorFilter";
import { SubcontractorTable } from "./SubcontractorTable";

export default function SubcontractorsListDashboard() {
    const { t } = useLanguage();
    const query = useSubcontractorList();
    const { rows: subcontractors } = query;

    // Filter States
    const [searchQuery, setSearchQuery] = useState("");
    const [statusFilter, setStatusFilter] = useState("all");
    const [regionFilter, setRegionFilter] = useState("all");
    const [fleetSizeFilter, setFleetSizeFilter] = useState("all");

    // Pagination State
    const [currentPage, setCurrentPage] = useState(1);
    const itemsPerPage = 10;
    const changeFilter = (setFilter: (value: string) => void) => (value: string) => {
        setFilter(value);
        setCurrentPage(1);
    };

    // Sort State
    const [sortConfig, setSortConfig] = useState<{ key: keyof SubcontractorSummaryDTO; direction: 'asc' | 'desc' } | null>(null);

    // Derived Stats
    const stats = useMemo(() => {
        const totalPartners = subcontractors.length;
        const activeTrucks = subcontractors.reduce((acc, sub) => acc + (sub.fleetSize || 0), 0);
        const pendingContracts = subcontractors.filter(sub => sub.status === 'pending').length;
        return { totalPartners, activeTrucks, pendingContracts };
    }, [subcontractors]);

    // Unique Regions for Filter
    const regions = useMemo(() => {
        const unique = new Set(subcontractors.flatMap(s => s.serviceRegions ?? []));
        return Array.from(unique).sort();
    }, [subcontractors]);

    // Filtering Logic
    const filteredSubs = useMemo(() => {
        return subcontractors.filter(sub => {
            const matchesSearch =
                sub.nameTh.toLowerCase().includes(searchQuery.toLowerCase()) ||
                (sub.contactPerson ?? "").toLowerCase().includes(searchQuery.toLowerCase()) ||
                sub.email?.toLowerCase().includes(searchQuery.toLowerCase()) ||
                sub.id.toLowerCase().includes(searchQuery.toLowerCase());

            const matchesStatus = statusFilter === 'all' || sub.status === statusFilter;
            const serviceRegions = sub.serviceRegions ?? [];
            const matchesRegion = regionFilter === 'all' || (regionFilter === UNSPECIFIED_REGION
                ? serviceRegions.length === 0 : serviceRegions.includes(regionFilter));

            let matchesFleet = true;
            if (fleetSizeFilter === 'small') matchesFleet = (sub.fleetSize || 0) < 20;
            if (fleetSizeFilter === 'medium') matchesFleet = (sub.fleetSize || 0) >= 20 && (sub.fleetSize || 0) <= 50;
            if (fleetSizeFilter === 'large') matchesFleet = (sub.fleetSize || 0) > 50;

            return matchesSearch && matchesStatus && matchesRegion && matchesFleet;
        });
    }, [subcontractors, searchQuery, statusFilter, regionFilter, fleetSizeFilter]);

    // Sorting Logic
    const sortedSubs = useMemo(() => {
        if (!sortConfig) return filteredSubs;
        return [...filteredSubs].sort((a, b) => {
            let aValue = a[sortConfig.key];
            let bValue = b[sortConfig.key];

            if (aValue === undefined || aValue === null) aValue = "";
            if (bValue === undefined || bValue === null) bValue = "";

            if (aValue < bValue) return sortConfig.direction === 'asc' ? -1 : 1;
            if (aValue > bValue) return sortConfig.direction === 'asc' ? 1 : -1;
            return 0;
        });
    }, [filteredSubs, sortConfig]);

    // Pagination Logic
    const page = Math.max(1, Math.min(currentPage, Math.ceil(sortedSubs.length / itemsPerPage)));
    if (currentPage !== page) setCurrentPage(page);
    const paginatedSubs = useMemo(() => {
        const startIndex = (page - 1) * itemsPerPage;
        return sortedSubs.slice(startIndex, startIndex + itemsPerPage);
    }, [sortedSubs, page]);

    const handleSort = (key: keyof SubcontractorSummaryDTO) => {
        let direction: 'asc' | 'desc' = 'asc';
        if (sortConfig && sortConfig.key === key && sortConfig.direction === 'asc') {
            direction = 'desc';
        }
        setSortConfig({ key, direction });
    };

    return (
        <div className="flex flex-col gap-section">
            <PageHeader
                title={t("nav.partners")}
                description={t("subcontractors.subtitle")}
                actions={
                    <Button asChild className="gap-2">
                        <Link href="/app/subcontractors/new" prefetch={false}>
                            <Plus className="h-4 w-4" aria-hidden />
                            {t("subcontractors.add")}
                        </Link>
                    </Button>
                }
            />

            {query.error && <QueryErrorState title={t("subcontractors.loadError.list")} error={query.error} onRetry={() => void query.refetch()} retrying={query.isFetching} />}
            {query.isPending && <LoadingState label={t("subcontractors.loading")} />}
            {query.data !== undefined && (
                <>
                    {/* Stats */}
                    <SubcontractorStats stats={stats} loading={false} />

                    {/* Filter */}
                    <SubcontractorFilter
                        searchQuery={searchQuery}
                        setSearchQuery={changeFilter(setSearchQuery)}
                        statusFilter={statusFilter}
                        setStatusFilter={changeFilter(setStatusFilter)}
                        regionFilter={regionFilter}
                        setRegionFilter={changeFilter(setRegionFilter)}
                        fleetSizeFilter={fleetSizeFilter}
                        setFleetSizeFilter={changeFilter(setFleetSizeFilter)}
                        regions={regions}
                        hasUnspecifiedRegion={subcontractors.some(sub => !sub.serviceRegions?.length)}
                    />

                    {/* Table */}
                    <SubcontractorTable
                        subcontractors={paginatedSubs}
                        loading={false}
                        currentPage={page}
                        setCurrentPage={setCurrentPage}
                        itemsPerPage={itemsPerPage}
                        filteredCount={filteredSubs.length}
                        sortConfig={sortConfig}
                        handleSort={handleSort}
                    />
                </>
            )}
        </div>
    );
}
