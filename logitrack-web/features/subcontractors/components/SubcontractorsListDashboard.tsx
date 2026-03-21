"use client";

import { useState, useEffect, useMemo } from "react";
import { getSubcontractors, SubcontractorData } from "../services/subcontractorService";
import { Button } from "@/components/ui/button";
import { Plus } from "lucide-react";
import Link from "next/link";
import { useLanguage } from "@/context/language";

// Sub-components
import { SubcontractorStats } from "./SubcontractorStats";
import { SubcontractorFilter } from "./SubcontractorFilter";
import { SubcontractorTable } from "./SubcontractorTable";

export default function SubcontractorsListDashboard() {
    const { t } = useLanguage();
    const [subcontractors, setSubcontractors] = useState<SubcontractorData[]>([]);
    const [loading, setLoading] = useState(true);

    // Filter States
    const [searchQuery, setSearchQuery] = useState("");
    const [statusFilter, setStatusFilter] = useState("all");
    const [regionFilter, setRegionFilter] = useState("all");
    const [fleetSizeFilter, setFleetSizeFilter] = useState("all");

    // Pagination State
    const [currentPage, setCurrentPage] = useState(1);
    const itemsPerPage = 10;

    // Sort State
    const [sortConfig, setSortConfig] = useState<{ key: keyof SubcontractorData; direction: 'asc' | 'desc' } | null>(null);

    useEffect(() => {
        const fetchData = async () => {
            setLoading(true);
            try {
                const data = await getSubcontractors();
                setSubcontractors(data);
            } catch (error) {
                console.error("Failed to load subcontractors", error);
            } finally {
                setLoading(false);
            }
        };
        fetchData();
    }, []);

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
                sub.name.toLowerCase().includes(searchQuery.toLowerCase()) ||
                sub.contactPerson.toLowerCase().includes(searchQuery.toLowerCase()) ||
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

    const handleSort = (key: keyof SubcontractorData) => {
        let direction: 'asc' | 'desc' = 'asc';
        if (sortConfig && sortConfig.key === key && sortConfig.direction === 'asc') {
            direction = 'desc';
        }
        setSortConfig({ key, direction });
    };

    return (
        <div className="min-h-screen bg-background text-foreground p-6 space-y-6">
            {/* Header */}
            <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">{t("subcontractors.title")}</h1>
                    <p className="text-muted-foreground mt-1">
                        {t("subcontractors.subtitle")}
                    </p>
                </div>
                <Button asChild className="bg-blue-600 hover:bg-blue-700 text-white gap-2 shadow-lg shadow-blue-900/20">
                    <Link href="/admin/subcontractors/new" prefetch={false}>
                        <Plus className="h-4 w-4" />
                        {t("subcontractors.add")}
                    </Link>
                </Button>
            </div>

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
