"use client";

import { useState, useMemo } from "react";
import type { SubcontractorSummaryDTO } from "../api/subcontractors";
import { useSubcontractorList } from "../api/useSubcontractors";
import { Button } from "@/components/ui/button";
import { Plus } from "lucide-react";
import Link from "next/link";
import { useLanguage } from "@/context/language";
import { PageHeader } from "@/components/page-header";

// Sub-components
import { SubcontractorStats } from "./SubcontractorStats";
import { SubcontractorFilter } from "./SubcontractorFilter";
import { SubcontractorTable } from "./SubcontractorTable";

export default function SubcontractorsListDashboard() {
    const { t } = useLanguage();
    // `['subcontractors', {view:'full'}]` (T25): Go `GET /v1/subcontractors` or Firestore by the flag.
    const { rows: subcontractors, isPending: loading } = useSubcontractorList();

    // Filter States
    const [searchQuery, setSearchQuery] = useState("");
    const [statusFilter, setStatusFilter] = useState("all");
    const [regionFilter, setRegionFilter] = useState("all");
    const [fleetSizeFilter, setFleetSizeFilter] = useState("all");

    // Pagination State
    const [currentPage, setCurrentPage] = useState(1);
    const itemsPerPage = 10;

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
        const unique = new Set(subcontractors.map(s => s.serviceArea || "Unknown"));
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
            const matchesRegion = regionFilter === 'all' || sub.serviceArea === regionFilter;

            let matchesFleet = true;
            if (fleetSizeFilter === 'small') matchesFleet = (sub.fleetSize || 0) < 20;
            if (fleetSizeFilter === 'medium') matchesFleet = (sub.fleetSize || 0) >= 20 && (sub.fleetSize || 0) < 50;
            if (fleetSizeFilter === 'large') matchesFleet = (sub.fleetSize || 0) >= 50;

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
    const paginatedSubs = useMemo(() => {
        const startIndex = (currentPage - 1) * itemsPerPage;
        return sortedSubs.slice(startIndex, startIndex + itemsPerPage);
    }, [sortedSubs, currentPage]);

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

            {/* Stats */}
            <SubcontractorStats stats={stats} loading={loading} />

            {/* Filter */}
            <SubcontractorFilter
                searchQuery={searchQuery}
                setSearchQuery={setSearchQuery}
                statusFilter={statusFilter}
                setStatusFilter={setStatusFilter}
                regionFilter={regionFilter}
                setRegionFilter={setRegionFilter}
                fleetSizeFilter={fleetSizeFilter}
                setFleetSizeFilter={setFleetSizeFilter}
                regions={regions}
            />

            {/* Table */}
            <SubcontractorTable
                subcontractors={paginatedSubs}
                loading={loading}
                currentPage={currentPage}
                setCurrentPage={setCurrentPage}
                itemsPerPage={itemsPerPage}
                filteredCount={filteredSubs.length}
                sortConfig={sortConfig}
                handleSort={handleSort}
            />
        </div>
    );
}
