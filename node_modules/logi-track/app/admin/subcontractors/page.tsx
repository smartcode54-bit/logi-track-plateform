"use client";

import { useState, useEffect, useMemo } from "react";
import { getSubcontractors, SubcontractorData } from "./actions.client";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from "@/components/ui/table";
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue
} from "@/components/ui/select";
import { Card, CardContent } from "@/components/ui/card";
import {
    Plus,
    Search,
    Loader2,
    MoreHorizontal,
    Phone,
    Mail,
    Truck,
    Users,
    FileText,
    Settings,
    MapPin,
    ArrowUpDown,
    ArrowUp,
    ArrowDown,
    Filter
} from "lucide-react";
import Link from "next/link";
import { Badge } from "@/components/ui/badge";
import { useRouter } from "next/navigation";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

export default function SubcontractorManagementPage() {
    const router = useRouter();
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
            // @ts-ignore
            let aValue = a[sortConfig.key];
            // @ts-ignore
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

    const getStatusBadge = (status: string) => {
        switch (status) {
            case 'active':
                return <Badge className="bg-green-500/10 text-green-600 hover:bg-green-500/20 border-green-500/20 shadow-none uppercase text-[10px] tracking-wider px-2 py-0.5">Active</Badge>;
            case 'pending':
                return <Badge className="bg-orange-500/10 text-orange-600 hover:bg-orange-500/20 border-orange-500/20 shadow-none uppercase text-[10px] tracking-wider px-2 py-0.5">On Trial</Badge>;
            case 'suspended':
                return <Badge className="bg-red-500/10 text-red-600 hover:bg-red-500/20 border-red-500/20 shadow-none uppercase text-[10px] tracking-wider px-2 py-0.5">Terminated</Badge>;
            default:
                return <Badge variant="secondary">{status}</Badge>;
        }
    };

    return (
        <div className="min-h-screen bg-background text-foreground p-6 space-y-6">
            {/* Header */}
            <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">Subcontractor Management</h1>
                    <p className="text-muted-foreground mt-1">
                        Monitor performance and compliance of third-party logistics partners
                    </p>
                </div>
                <Button asChild className="bg-blue-600 hover:bg-blue-700 text-white gap-2 shadow-lg shadow-blue-900/20">
                    <Link href="/admin/subcontractors/new">
                        <Plus className="h-4 w-4" />
                        Add Subcontractor
                    </Link>
                </Button>
            </div>

            {/* Stats Cards */}
            <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                <Card className="bg-card/50 border-border/50 shadow-sm relative overflow-hidden group">
                    <CardContent className="p-6">
                        <div className="flex justify-between items-start">
                            <div className="space-y-2">
                                <p className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">Total Partners</p>
                                <div className="flex items-baseline gap-2">
                                    <span className="text-3xl font-bold font-mono text-foreground">
                                        {loading ? "-" : stats.totalPartners}
                                    </span>
                                    <span className="text-xs font-medium text-green-500">↑5%</span>
                                </div>
                                <p className="text-xs text-muted-foreground">Active business entities</p>
                            </div>
                            <div className="p-2 bg-blue-500/10 rounded-lg text-blue-500 group-hover:scale-110 transition-transform">
                                <Users className="h-5 w-5" />
                            </div>
                        </div>
                    </CardContent>
                </Card>

                <Card className="bg-card/50 border-border/50 shadow-sm relative overflow-hidden group">
                    <CardContent className="p-6">
                        <div className="flex justify-between items-start">
                            <div className="space-y-2">
                                <p className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">Active Trucks</p>
                                <div className="flex items-baseline gap-2">
                                    <span className="text-3xl font-bold font-mono text-foreground">
                                        {loading ? "-" : stats.activeTrucks}
                                    </span>
                                    <span className="text-xs font-medium text-green-500">↑12%</span>
                                </div>
                                <p className="text-xs text-muted-foreground">Currently in circulation</p>
                            </div>
                            <div className="p-2 bg-blue-500/10 rounded-lg text-blue-500 group-hover:scale-110 transition-transform">
                                <Truck className="h-5 w-5" />
                            </div>
                        </div>
                    </CardContent>
                </Card>

                <Card className="bg-card/50 border-border/50 shadow-sm relative overflow-hidden group">
                    <CardContent className="p-6">
                        <div className="flex justify-between items-start">
                            <div className="space-y-2">
                                <p className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">Pending Contracts</p>
                                <div className="flex items-baseline gap-2">
                                    <span className="text-3xl font-bold font-mono text-foreground">
                                        {loading ? "-" : stats.pendingContracts}
                                    </span>
                                    <span className="text-xs font-medium text-orange-500">↓2%</span>
                                </div>
                                <p className="text-xs text-muted-foreground">Awaiting verification</p>
                            </div>
                            <div className="p-2 bg-blue-500/10 rounded-lg text-blue-500 group-hover:scale-110 transition-transform">
                                <FileText className="h-5 w-5" />
                            </div>
                        </div>
                    </CardContent>
                </Card>
            </div>

            {/* Filter Bar */}
            <div className="flex flex-col lg:flex-row gap-4 bg-card/50 p-4 rounded-lg border border-border/50">
                <div className="relative flex-1">
                    <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
                    <Input
                        placeholder="Search companies or contacts..."
                        className="pl-10 bg-background/50 border-border/50 focus-visible:ring-1"
                        value={searchQuery}
                        onChange={(e) => setSearchQuery(e.target.value)}
                    />
                </div>
                <div className="flex gap-3 overflow-x-auto pb-1 lg:pb-0">
                    <Button variant="outline" size="icon" className="shrink-0 lg:hidden">
                        <Filter className="h-4 w-4" />
                    </Button>
                    <Select value={statusFilter} onValueChange={setStatusFilter}>
                        <SelectTrigger className="w-[140px] bg-background/50 border-border/50">
                            <SelectValue placeholder="All Statuses" />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value="all">All Statuses</SelectItem>
                            <SelectItem value="active">Active</SelectItem>
                            <SelectItem value="pending">On Trial</SelectItem>
                            <SelectItem value="suspended">Terminated</SelectItem>
                        </SelectContent>
                    </Select>

                    <Select value={regionFilter} onValueChange={setRegionFilter}>
                        <SelectTrigger className="w-[160px] bg-background/50 border-border/50">
                            <SelectValue placeholder="Region" />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value="all">All Regions</SelectItem>
                            {regions.map(r => (
                                <SelectItem key={r} value={r}>{r}</SelectItem>
                            ))}
                        </SelectContent>
                    </Select>

                    <Select value={fleetSizeFilter} onValueChange={setFleetSizeFilter}>
                        <SelectTrigger className="w-[140px] bg-background/50 border-border/50">
                            <SelectValue placeholder="Fleet Size" />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value="all">All Sizes</SelectItem>
                            <SelectItem value="small">Small (&lt;20)</SelectItem>
                            <SelectItem value="medium">Medium (20-50)</SelectItem>
                            <SelectItem value="large">Large (&gt;50)</SelectItem>
                        </SelectContent>
                    </Select>
                </div>
            </div>

            {/* Table */}
            <div className="rounded-xl border border-border/50 bg-card overflow-hidden">
                <Table>
                    <TableHeader className="bg-muted/30">
                        <TableRow className="hover:bg-transparent border-border/50">
                            <TableHead className="text-xs font-semibold tracking-wider text-muted-foreground uppercase pl-6 cursor-pointer" onClick={() => handleSort('name')}>
                                <div className="flex items-center gap-1">company name {sortConfig?.key === 'name' && (sortConfig.direction === 'asc' ? <ArrowUp className="w-3 h-3" /> : <ArrowDown className="w-3 h-3" />)}</div>
                            </TableHead>
                            <TableHead className="text-xs font-semibold tracking-wider text-muted-foreground uppercase cursor-pointer" onClick={() => handleSort('contactPerson')}>
                                <div className="flex items-center gap-1">primary contact {sortConfig?.key === 'contactPerson' && (sortConfig.direction === 'asc' ? <ArrowUp className="w-3 h-3" /> : <ArrowDown className="w-3 h-3" />)}</div>
                            </TableHead>
                            <TableHead className="text-xs font-semibold tracking-wider text-muted-foreground uppercase cursor-pointer" onClick={() => handleSort('fleetSize')}>
                                <div className="flex items-center gap-1">fleet size {sortConfig?.key === 'fleetSize' && (sortConfig.direction === 'asc' ? <ArrowUp className="w-3 h-3" /> : <ArrowDown className="w-3 h-3" />)}</div>
                            </TableHead>
                            <TableHead className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">service area</TableHead>
                            <TableHead className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">status</TableHead>
                            <TableHead className="text-xs font-semibold tracking-wider text-muted-foreground uppercase text-right pr-6">actions</TableHead>
                        </TableRow>
                    </TableHeader>
                    <TableBody>
                        {loading ? (
                            <TableRow>
                                <TableCell colSpan={6} className="h-32 text-center text-muted-foreground">
                                    <Loader2 className="h-6 w-6 animate-spin mx-auto mb-2" />
                                    Loading partners...
                                </TableCell>
                            </TableRow>
                        ) : paginatedSubs.length === 0 ? (
                            <TableRow>
                                <TableCell colSpan={6} className="h-32 text-center text-muted-foreground">No subcontractors found matching your filters.</TableCell>
                            </TableRow>
                        ) : (
                            paginatedSubs.map((sub) => (
                                <TableRow key={sub.id} className="border-border/50 hover:bg-muted/30 transition-colors group">
                                    <TableCell className="pl-6 py-4">
                                        <div className="flex gap-3 items-center">
                                            <div className="h-9 w-9 rounded-lg bg-blue-100 dark:bg-blue-900/40 text-blue-600 flex items-center justify-center font-bold text-sm">
                                                {sub.id.substring(0, 2).toUpperCase()}
                                            </div>
                                            <div>
                                                <div className="font-semibold text-foreground text-sm">{sub.name}</div>
                                                <div className="text-xs text-muted-foreground font-mono">ID: {sub.id.substring(0, 6).toUpperCase()}</div>
                                            </div>
                                        </div>
                                    </TableCell>
                                    <TableCell>
                                        <div className="space-y-0.5">
                                            <div className="text-sm font-medium text-foreground">{sub.contactPerson}</div>
                                            <div className="text-xs text-muted-foreground">{sub.email || sub.phone}</div>
                                        </div>
                                    </TableCell>
                                    <TableCell>
                                        <div className="flex items-center gap-2">
                                            <span className="font-mono font-bold text-foreground">{sub.fleetSize || 0}</span>
                                            <span className="text-xs text-muted-foreground">Vehicles</span>
                                        </div>
                                    </TableCell>
                                    <TableCell>
                                        <span className="text-sm text-foreground/80">{sub.serviceArea || "Global"}</span>
                                    </TableCell>
                                    <TableCell>
                                        {getStatusBadge(sub.status)}
                                    </TableCell>
                                    <TableCell className="text-right pr-6">
                                        <DropdownMenu>
                                            <DropdownMenuTrigger asChild>
                                                <Button variant="ghost" size="icon" className="h-8 w-8 hover:bg-muted opacity-0 group-hover:opacity-100 transition-opacity">
                                                    <MoreHorizontal className="h-4 w-4" />
                                                </Button>
                                            </DropdownMenuTrigger>
                                            <DropdownMenuContent align="end">
                                                <DropdownMenuItem onClick={() => router.push(`/admin/subcontractors/${sub.id}`)}>
                                                    View Profile
                                                </DropdownMenuItem>
                                                <DropdownMenuItem>Manage Contracts</DropdownMenuItem>
                                                <DropdownMenuItem className="text-destructive focus:text-destructive">Suspend Partner</DropdownMenuItem>
                                            </DropdownMenuContent>
                                        </DropdownMenu>
                                    </TableCell>
                                </TableRow>
                            ))
                        )}
                    </TableBody>
                </Table>

                {/* Pagination */}
                <div className="flex items-center justify-between px-6 py-4 border-t border-border/50 bg-muted/20">
                    <div className="text-xs text-muted-foreground">
                        Showing <span className="font-medium text-foreground">{(currentPage - 1) * itemsPerPage + 1}</span> to <span className="font-medium text-foreground">{Math.min(currentPage * itemsPerPage, filteredSubs.length)}</span> of <span className="font-medium text-foreground">{filteredSubs.length}</span> results
                    </div>
                    <div className="flex items-center gap-2">
                        <Button
                            variant="outline"
                            size="sm"
                            className="h-8 bg-background/50"
                            onClick={() => setCurrentPage(p => Math.max(1, p - 1))}
                            disabled={currentPage === 1}
                        >
                            Previous
                        </Button>
                        <Button
                            variant="outline"
                            size="sm"
                            className="h-8 bg-background/50"
                            onClick={() => setCurrentPage(p => Math.min(Math.ceil(filteredSubs.length / itemsPerPage), p + 1))}
                            disabled={currentPage >= Math.ceil(filteredSubs.length / itemsPerPage)}
                        >
                            Next
                        </Button>
                    </div>
                </div>
            </div>
        </div>
    );
}
