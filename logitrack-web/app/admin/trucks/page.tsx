"use client";

import { useState, useEffect, useMemo, type ReactNode, JSX } from "react";
import { useRouter } from "next/navigation";
import { collection, query, orderBy, onSnapshot, where, limit } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { TruckComplianceCards } from "./components/TruckComplianceCards";
import Link from "next/link";
import {
    Plus,
    Search,
    Download,
    MoreHorizontal,
    Loader2,
    Eye,
    Edit,
    Wrench,
    ChevronLeft,
    ChevronRight,
    FileText,
    ShieldAlert,
    ChevronDown,
    Users,
} from "lucide-react";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuLabel,
    DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { useLanguage } from "@/context/language";
import { TruckData } from "./actions.client";
import { formatLicensePlate } from "@/lib/utils";
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
    SelectValue,
} from "@/components/ui/select";
import { Badge } from "@/components/ui/badge";

export default function TrucksListPage() {
    const { t } = useLanguage();
    const router = useRouter();
    const [trucks, setTrucks] = useState<TruckData[]>([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);

    // Filters
    const [searchQuery, setSearchQuery] = useState("");
    const [typeFilter, setTypeFilter] = useState("all");
    const [statusFilter, setStatusFilter] = useState("all");
    const [groupFilter, setGroupFilter] = useState("all");
    const [complianceFilter, setComplianceFilter] = useState<{ type: string | null; status: string | null }>({ type: null, status: null });

    // Pagination
    const [currentPage, setCurrentPage] = useState(1);
    const itemsPerPage = 10;

    // Fetch trucks from Firestore
    useEffect(() => {
        setLoading(true);
        const trucksRef = collection(db, COLLECTIONS.TRUCKS);
        const q = query(trucksRef, orderBy("createdAt", "desc"), limit(100)); // Limit 100 for now

        const unsubscribe = onSnapshot(q, (snapshot) => {
            const trucksData: TruckData[] = [];
            snapshot.forEach((doc) => {
                const data = doc.data();
                const formatTimestamp = (timestamp: any): Date | null => {
                    if (!timestamp) return null;
                    if (timestamp.toDate) return timestamp.toDate();
                    if (timestamp.toMillis) return new Date(timestamp.toMillis());
                    if (timestamp.seconds) return new Date(timestamp.seconds * 1000);
                    return timestamp;
                };

                trucksData.push({
                    id: doc.id,
                    ownershipType: (data.ownershipType as "own" | "subcontractor") || "own",
                    subcontractorId: data.subcontractorId || undefined,
                    licensePlate: data.licensePlate || "",
                    province: data.province || "",
                    vin: data.vin || "",
                    engineNumber: data.engineNumber || "",
                    truckStatus: data.truckStatus || "",
                    brand: data.brand || "",
                    model: data.model || "",
                    year: data.year || "",
                    color: data.color || "",
                    type: data.type || "",
                    seats: data.seats || "",
                    fuelType: data.fuelType || "",
                    engineCapacity: data.engineCapacity,
                    fuelCapacity: data.fuelCapacity,
                    maxLoadWeight: data.maxLoadWeight,
                    registrationDate: data.registrationDate || "",
                    buyingDate: data.buyingDate || "",
                    notes: data.notes || "",
                    images: data.images || [],
                    // Compliance Mapping
                    taxExpiryDate: data.taxExpiryDate,
                    insuranceExpiryDate: data.insuranceExpiryDate,
                    lastServiceDate: data.lastServiceDate,
                    nextServiceDate: data.nextServiceDate,
                    nextServiceMileage: data.nextServiceMileage,
                    currentMileage: data.currentMileage,
                    createdBy: data.createdBy || "",
                    createdAt: formatTimestamp(data.createdAt),
                    updatedAt: formatTimestamp(data.updatedAt),

                    // Renewal Status Mappings
                    taxRenewalStatus: data.taxRenewalStatus,
                    insuranceRenewalStatus: data.insuranceRenewalStatus,

                    // Assignment - Denormalized
                    currentAssignments: data.currentAssignments ? (data.currentAssignments as any[]).map(assignment => ({
                        driverId: assignment.driverId,
                        driverName: assignment.driverName,
                        assignedAt: formatTimestamp(assignment.assignedAt) as Date,
                        assignmentId: assignment.assignmentId
                    })) : (data.currentAssignment ? [{
                        // Fallback for legacy single assignment data
                        driverId: data.currentAssignment.driverId,
                        driverName: data.currentAssignment.driverName,
                        assignedAt: formatTimestamp(data.currentAssignment.assignedAt) as Date,
                        assignmentId: data.currentAssignment.assignmentId
                    }] : []),
                });
            });
            setTrucks(trucksData);
            setLoading(false);
            setError(null);
        }, (err) => {
            console.error("Error fetching trucks:", err);
            setError("Failed to load trucks");
            setLoading(false);
        });

        return () => unsubscribe();
    }, []);

    // Unique Types for Filter
    const uniqueTypes = useMemo(() => {
        const types = new Set(trucks.map(t => t.type).filter(Boolean));
        return Array.from(types);
    }, [trucks]);

    // Filtering Logic
    const filteredTrucks = useMemo(() => {
        return trucks.filter(truck => {
            const matchSearch =
                truck.licensePlate.toLowerCase().includes(searchQuery.toLowerCase()) ||
                truck.model.toLowerCase().includes(searchQuery.toLowerCase()) ||
                truck.id.toLowerCase().includes(searchQuery.toLowerCase());

            const matchType = typeFilter === "all" || truck.type === typeFilter;
            const matchStatus = statusFilter === "all" || truck.truckStatus === statusFilter;
            const matchGroup = groupFilter === "all" ||
                (groupFilter === "own" && truck.ownershipType === "own") ||
                (groupFilter === "subcontractor" && truck.ownershipType === "subcontractor");

            let matchCompliance = true;
            if (complianceFilter.type && complianceFilter.status) {
                const now = new Date();
                const warningThresholdDays = 30;
                const incomingThresholdDays = 60;
                const warningThresholdKm = 2000; // User specified 2000km criteria
                const incomingThresholdKm = 5000;

                const getDaysDiff = (dateStr?: string) => {
                    if (!dateStr) return 999;
                    return Math.ceil((new Date(dateStr).getTime() - now.getTime()) / (1000 * 60 * 60 * 24));
                };

                const checkStatus = (days: number, kms?: number) => {
                    if (complianceFilter.status === "overdue") {
                        return days < 0 || (kms !== undefined && kms < 0);
                    }
                    if (complianceFilter.status === "expiring") {
                        return (days >= 0 && days <= warningThresholdDays) || (kms !== undefined && kms >= 0 && kms <= warningThresholdKm);
                    }
                    if (complianceFilter.status === "incoming") {
                        return (days > warningThresholdDays && days <= incomingThresholdDays) ||
                            (kms !== undefined && kms > warningThresholdKm && kms <= incomingThresholdKm);
                    }
                    return false;
                };

                if (complianceFilter.type === "tax") {
                    matchCompliance = checkStatus(getDaysDiff(truck.taxExpiryDate));
                } else if (complianceFilter.type === "insurance") {
                    matchCompliance = checkStatus(getDaysDiff(truck.insuranceExpiryDate));
                } else if (complianceFilter.type === "service") {
                    const days = getDaysDiff(truck.nextServiceDate);
                    let kms: number | undefined = undefined;
                    if (truck.nextServiceMileage && truck.currentMileage !== undefined) {
                        kms = truck.nextServiceMileage - truck.currentMileage;
                    }

                    // Service is special: it matches if EITHER date OR km is in the range
                    // But we only want to show it if it actually has service data
                    if (truck.nextServiceDate || truck.nextServiceMileage) {
                        matchCompliance = checkStatus(days, kms);
                    } else {
                        matchCompliance = false;
                    }
                }
            }

            return matchSearch && matchType && matchStatus && matchGroup && matchCompliance;
        });
    }, [trucks, searchQuery, typeFilter, statusFilter, groupFilter, complianceFilter]);

    // Pagination Logic
    const totalPages = Math.ceil(filteredTrucks.length / itemsPerPage);
    const paginatedTrucks = filteredTrucks.slice(
        (currentPage - 1) * itemsPerPage,
        currentPage * itemsPerPage
    );

    const getStatusColor = (status: string) => {
        switch (status) {
            case "active": return "bg-green-500/10 text-green-500 hover:bg-green-500/20 border-green-500/20";
            case "maintenance": return "bg-red-500/10 text-red-500 hover:bg-red-500/20 border-red-500/20";
            case "in-transit": return "bg-yellow-500/10 text-yellow-500 hover:bg-yellow-500/20 border-yellow-500/20"; // Example
            case "inactive": return "bg-gray-500/10 text-gray-500 hover:bg-gray-500/20 border-gray-500/20";
            default: return "bg-gray-500/10 text-gray-500 hover:bg-gray-500/20 border-gray-500/20";
        }
    };

    const getStatusLabel = (status: string) => {
        switch (status) {
            case "active": return "Active";
            case "maintenance": return "Maintenance";
            case "in-transit": return "In Transit";
            case "inactive": return "Inactive";
            default: return status;
        }
    };

    const getOwnershipBadge = (type: string) => {
        if (type === 'own') {
            return <Badge variant="secondary" className="bg-blue-900/40 text-blue-400 hover:bg-blue-900/60 border-blue-800">COMPANY</Badge>;
        }
        return <Badge variant="secondary" className="bg-slate-800 text-slate-400 hover:bg-slate-700">PARTNER</Badge>;
    };

    const clearFilters = () => {
        setSearchQuery("");
        setTypeFilter("all");
        setStatusFilter("all");
        setGroupFilter("all");
        setCurrentPage(1);
    };

    return (
        <div className="container mx-auto p-6 space-y-6 max-w-[1600px]">
            {/* Header */}
            <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">Truck Management</h1>
                    <p className="text-muted-foreground mt-1">
                        Monitor and manage all registered trucks in the system
                    </p>
                </div>
                <div className="flex gap-3">
                    <Button variant="outline" className="gap-2">
                        <Download className="h-4 w-4" />
                        Export
                    </Button>
                    <Button asChild className="bg-blue-600 hover:bg-blue-700 text-white gap-2">
                        <Link href="/admin/trucks/new">
                            <Plus className="h-4 w-4" />
                            Add New Truck
                        </Link>
                    </Button>
                </div>
            </div>

            {/* Compliance Cards */}
            <TruckComplianceCards onFilterChange={setComplianceFilter} />

            {/* Filter Bar */}
            <div className="flex flex-col xl:flex-row gap-4 bg-card/50 p-4 rounded-lg border border-border/50">
                <div className="relative flex-1">
                    <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
                    <Input
                        placeholder="Search by Plate Number, ID, or Model..."
                        className="pl-10 bg-background/50 border-border/50 focus-visible:ring-1"
                        value={searchQuery}
                        onChange={(e) => setSearchQuery(e.target.value)}
                    />
                </div>
                <div className="flex flex-wrap gap-3">
                    <Select value={typeFilter} onValueChange={setTypeFilter}>
                        <SelectTrigger className="w-[180px] bg-background/50 border-border/50">
                            <SelectValue placeholder="All Types" />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value="all">All Types</SelectItem>
                            {uniqueTypes.map(type => (
                                <SelectItem key={type} value={type}>{type}</SelectItem>
                            ))}
                        </SelectContent>
                    </Select>

                    <Select value={statusFilter} onValueChange={setStatusFilter}>
                        <SelectTrigger className="w-[180px] bg-background/50 border-border/50">
                            <SelectValue placeholder="Vehicle Status" />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value="all">All Statuses</SelectItem>
                            <SelectItem value="active">Active</SelectItem>
                            <SelectItem value="maintenance">Maintenance</SelectItem>
                            <SelectItem value="inactive">Inactive</SelectItem>
                        </SelectContent>
                    </Select>

                    <Select value={groupFilter} onValueChange={setGroupFilter}>
                        <SelectTrigger className="w-[180px] bg-background/50 border-border/50">
                            <SelectValue placeholder="Fleet Group" />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value="all">All Groups</SelectItem>
                            <SelectItem value="own">Company Fleet</SelectItem>
                            <SelectItem value="subcontractor">Partner Fleet</SelectItem>
                        </SelectContent>
                    </Select>

                    <Button variant="ghost" className="text-blue-500 hover:text-blue-600 hover:bg-blue-500/10" onClick={clearFilters}>
                        Clear Filters
                    </Button>
                </div>
            </div>

            {/* Content */}
            <div className="border rounded-lg bg-card overflow-hidden">
                <Table>
                    <TableHeader className="bg-muted/50">
                        <TableRow className="hover:bg-transparent border-b border-border/50">
                            <TableHead className="w-[100px] text-xs font-semibold tracking-wider text-muted-foreground uppercase">ID</TableHead>
                            <TableHead className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">Plate Number</TableHead>
                            <TableHead className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">Model</TableHead>
                            <TableHead className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">Current Driver</TableHead>
                            <TableHead className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">Ownership</TableHead>
                            <TableHead className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">Tax/Insu.</TableHead>
                            <TableHead className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">PM Status</TableHead>
                            <TableHead className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">Status</TableHead>
                            <TableHead className="text-right text-xs font-semibold tracking-wider text-muted-foreground uppercase">Actions</TableHead>
                        </TableRow>
                    </TableHeader>
                    <TableBody>
                        {loading ? (
                            <TableRow>
                                <TableCell colSpan={6} className="h-32 text-center">
                                    <div className="flex flex-col items-center justify-center gap-2">
                                        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
                                        <p className="text-sm text-muted-foreground">Loading trucks...</p>
                                    </div>
                                </TableCell>
                            </TableRow>
                        ) : paginatedTrucks.length === 0 ? (
                            <TableRow>
                                <TableCell colSpan={6} className="h-32 text-center">
                                    <p className="text-sm text-muted-foreground">No trucks found matching your filters.</p>
                                </TableCell>
                            </TableRow>
                        ) : (
                            paginatedTrucks.map((truck) => (
                                <TableRow key={truck.id} className="cursor-pointer hover:bg-muted/50 transition-colors border-b border-border/50" onClick={() => router.push(`/admin/trucks/view?id=${truck.id}`)}>
                                    <TableCell className="font-mono text-sm text-muted-foreground">
                                        {truck.id.slice(0, 8).toUpperCase()}
                                    </TableCell>
                                    <TableCell className="font-bold text-base">
                                        {formatLicensePlate(truck.licensePlate)}
                                    </TableCell>
                                    <TableCell>
                                        <div className="flex flex-col">
                                            <span className="text-sm font-medium">{truck.model}</span>
                                            <span className="text-xs text-muted-foreground">{truck.brand}</span>
                                        </div>
                                    </TableCell>
                                    <TableCell>
                                        {(() => {
                                            if (!truck.currentAssignments || truck.currentAssignments.length === 0) {
                                                return <span className="text-muted-foreground text-xs">-</span>;
                                            }

                                            const assignments = Array.from(new Map(truck.currentAssignments.map(item => [item.driverId, item])).values());

                                            if (assignments.length === 1) {
                                                const assignment = assignments[0];
                                                return (
                                                    <div className="flex items-center gap-2">
                                                        <div className="h-6 w-6 rounded-full bg-blue-100 flex items-center justify-center text-[10px] font-bold text-blue-600">
                                                            {(assignment.driverName || 'DR').substring(0, 2).toUpperCase()}
                                                        </div>
                                                        <span className="text-sm font-medium">{assignment.driverName}</span>
                                                    </div>
                                                );
                                            }

                                            return (
                                                <DropdownMenu>
                                                    <DropdownMenuTrigger asChild>
                                                        <Badge variant="secondary" className="cursor-pointer hover:bg-secondary/80 gap-1.5 py-1 px-2 h-auto w-fit transition-colors">
                                                            <Users className="h-3 w-3" />
                                                            {assignments.length} Drivers
                                                            <ChevronDown className="h-3 w-3 opacity-50" />
                                                        </Badge>
                                                    </DropdownMenuTrigger>
                                                    <DropdownMenuContent align="start" className="w-[200px]">
                                                        <DropdownMenuLabel>Assigned Drivers</DropdownMenuLabel>
                                                        {assignments.map((assignment, idx) => (
                                                            <div key={idx} className="flex items-center gap-2 px-2 py-1.5 text-sm">
                                                                <div className="h-6 w-6 rounded-full bg-blue-100 flex items-center justify-center text-[10px] font-bold text-blue-600 shrink-0">
                                                                    {(assignment.driverName || 'DR').substring(0, 2).toUpperCase()}
                                                                </div>
                                                                <span className="truncate font-medium">{assignment.driverName}</span>
                                                            </div>
                                                        ))}
                                                    </DropdownMenuContent>
                                                </DropdownMenu>
                                            );
                                        })()}
                                    </TableCell>
                                    <TableCell>
                                        {getOwnershipBadge(truck.ownershipType)}
                                    </TableCell>
                                    <TableCell>
                                        <div className="flex flex-col gap-1">
                                            {(() => {
                                                // Check if both Tax and Insurance are completed - show single OK
                                                const taxCompleted = truck.taxRenewalStatus === 'completed';
                                                const insuranceCompleted = truck.insuranceRenewalStatus === 'completed';

                                                // Collect non-completed badges
                                                const badges: ReactNode[] = [];

                                                // Tax Status (only show badge if NOT completed)
                                                if (truck.taxExpiryDate && !taxCompleted) {
                                                    let taxBadge: ReactNode = null;
                                                    if (truck.taxRenewalStatus === 'in_progress') {
                                                        taxBadge = <Badge variant="secondary" className="text-[10px] px-1 py-0 h-5 w-fit bg-blue-100 text-blue-700 border-blue-200">Tax: In-process</Badge>;
                                                    } else {
                                                        const days = Math.ceil((new Date(truck.taxExpiryDate).getTime() - new Date().getTime()) / (1000 * 60 * 60 * 24));
                                                        if (days < 0) taxBadge = <Badge variant="destructive" className="text-[10px] px-1 py-0 h-5 w-fit">Tax: Overdue</Badge>;
                                                        else if (days <= 30) taxBadge = <Badge variant="outline" className="text-[10px] px-1 py-0 h-5 w-fit text-orange-600 border-orange-600 bg-orange-50">Tax: {days}d</Badge>;
                                                        else if (days <= 60) taxBadge = <Badge variant="outline" className="text-[10px] px-1 py-0 h-5 w-fit text-blue-600 border-blue-600 bg-blue-50">Tax: {days}d</Badge>;
                                                    }
                                                    if (taxBadge) {
                                                        const tooltipText = truck.taxRenewalStatus === 'in_progress' ? "Update Progress" : "Renew Now";
                                                        badges.push(
                                                            <TooltipProvider key="tax">
                                                                <Tooltip>
                                                                    <TooltipTrigger asChild>
                                                                        <Link href={`/admin/trucks/renew?id=${truck.id}&type=tax`} onClick={(e) => e.stopPropagation()} className="hover:opacity-80 transition-opacity w-fit">
                                                                            {taxBadge}
                                                                        </Link>
                                                                    </TooltipTrigger>
                                                                    <TooltipContent>
                                                                        <p>{tooltipText}</p>
                                                                    </TooltipContent>
                                                                </Tooltip>
                                                            </TooltipProvider>
                                                        );
                                                    }
                                                }

                                                // Insurance Status (only show badge if NOT completed)
                                                if (truck.insuranceExpiryDate && !insuranceCompleted) {
                                                    let insuBadge: ReactNode = null;
                                                    if (truck.insuranceRenewalStatus === 'in_progress') {
                                                        insuBadge = <Badge variant="secondary" className="text-[10px] px-1 py-0 h-5 w-fit bg-purple-100 text-purple-700 border-purple-200">Insu: In-process</Badge>;
                                                    } else {
                                                        const days = Math.ceil((new Date(truck.insuranceExpiryDate).getTime() - new Date().getTime()) / (1000 * 60 * 60 * 24));
                                                        if (days < 0) insuBadge = <Badge variant="destructive" className="text-[10px] px-1 py-0 h-5 w-fit">Insu: Overdue</Badge>;
                                                        else if (days <= 30) insuBadge = <Badge variant="outline" className="text-[10px] px-1 py-0 h-5 w-fit text-orange-600 border-orange-600 bg-orange-50">Insu: {days}d</Badge>;
                                                        else if (days <= 60) insuBadge = <Badge variant="outline" className="text-[10px] px-1 py-0 h-5 w-fit text-blue-600 border-blue-600 bg-blue-50">Insu: {days}d</Badge>;
                                                    }
                                                    if (insuBadge) {
                                                        const tooltipText = truck.insuranceRenewalStatus === 'in_progress' ? "Update Progress" : "Renew Now";
                                                        badges.push(
                                                            <TooltipProvider key="insu">
                                                                <Tooltip>
                                                                    <TooltipTrigger asChild>
                                                                        <Link href={`/admin/trucks/renew?id=${truck.id}&type=insurance`} onClick={(e) => e.stopPropagation()} className="hover:opacity-80 transition-opacity w-fit">
                                                                            {insuBadge}
                                                                        </Link>
                                                                    </TooltipTrigger>
                                                                    <TooltipContent>
                                                                        <p>{tooltipText}</p>
                                                                    </TooltipContent>
                                                                </Tooltip>
                                                            </TooltipProvider>
                                                        );
                                                    }
                                                }

                                                // If there are badges to show, render them
                                                if (badges.length > 0) {
                                                    return badges;
                                                }

                                                // Otherwise show single OK (both completed or no issues)
                                                return (
                                                    <span className="text-xs text-green-600 flex items-center gap-1">
                                                        <div className="w-1.5 h-1.5 rounded-full bg-green-500" /> OK
                                                    </span>
                                                );
                                            })()}
                                        </div>
                                    </TableCell>
                                    <TableCell>
                                        <div className="flex flex-col gap-1">
                                            {(() => {
                                                const km = truck.nextServiceMileage && truck.currentMileage ? truck.nextServiceMileage - truck.currentMileage : null;
                                                const days = truck.nextServiceDate ? Math.ceil((new Date(truck.nextServiceDate).getTime() - new Date().getTime()) / (1000 * 60 * 60 * 24)) : null;

                                                let pmBadge: ReactNode = null;

                                                if (truck.truckStatus === 'maintenance') {
                                                    pmBadge = <Badge variant="secondary" className="text-[10px] px-1 py-0 h-5 w-fit bg-yellow-100 text-yellow-700 border-yellow-200">In Shop</Badge>;
                                                } else if (km !== null && km < 0) {
                                                    pmBadge = <Badge variant="destructive" className="text-[10px] px-1 py-0 h-5 w-fit">PM: {-km}km Over</Badge>;
                                                } else if (days !== null && days < 0) {
                                                    pmBadge = <Badge variant="destructive" className="text-[10px] px-1 py-0 h-5 w-fit">PM: Overdue</Badge>;
                                                } else if (km !== null && km <= 1000) {
                                                    pmBadge = <Badge variant="outline" className="text-[10px] px-1 py-0 h-5 w-fit text-orange-600 border-orange-600 bg-orange-50">PM: {km}km</Badge>;
                                                } else if (days !== null && days <= 30) {
                                                    pmBadge = <Badge variant="outline" className="text-[10px] px-1 py-0 h-5 w-fit text-orange-600 border-orange-600 bg-orange-50">PM: {days}d</Badge>;
                                                }

                                                if (pmBadge) {
                                                    return (
                                                        <TooltipProvider>
                                                            <Tooltip>
                                                                <TooltipTrigger asChild>
                                                                    <Link href={`/admin/trucks/maintenance?id=${truck.id}`} onClick={(e) => e.stopPropagation()} className="hover:opacity-80 transition-opacity w-fit">
                                                                        {pmBadge}
                                                                    </Link>
                                                                </TooltipTrigger>
                                                                <TooltipContent>
                                                                    <p>Record Maintenance</p>
                                                                </TooltipContent>
                                                            </Tooltip>
                                                        </TooltipProvider>
                                                    );
                                                }

                                                return (
                                                    <span className="text-xs text-green-600 flex items-center gap-1">
                                                        <div className="w-1.5 h-1.5 rounded-full bg-green-500" /> OK
                                                    </span>
                                                );
                                            })()}
                                        </div>
                                    </TableCell>
                                    <TableCell>
                                        <div className={`inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-xs font-medium border ${getStatusColor(truck.truckStatus)}`}>
                                            <span className={`w-1.5 h-1.5 rounded-full bg-current`} />
                                            {getStatusLabel(truck.truckStatus)}
                                        </div>
                                    </TableCell>
                                    <TableCell className="text-right">
                                        <DropdownMenu>
                                            <DropdownMenuTrigger asChild onClick={(e) => e.stopPropagation()}>
                                                <Button variant="ghost" className="h-8 w-8 p-0 hover:bg-muted">
                                                    <span className="sr-only">Open menu</span>
                                                    <MoreHorizontal className="h-4 w-4" />
                                                </Button>
                                            </DropdownMenuTrigger>
                                            <DropdownMenuContent align="end">
                                                <DropdownMenuLabel>Actions</DropdownMenuLabel>
                                                <DropdownMenuItem asChild>
                                                    <Link href={`/admin/trucks/view?id=${truck.id}`} className="flex items-center cursor-pointer" onClick={(e) => e.stopPropagation()}>
                                                        <Eye className="mr-2 h-4 w-4" />
                                                        View Details
                                                    </Link>
                                                </DropdownMenuItem>
                                                <DropdownMenuItem asChild>
                                                    <Link href={`/admin/trucks/edit?id=${truck.id}`} className="flex items-center cursor-pointer" onClick={(e) => e.stopPropagation()}>
                                                        <Edit className="mr-2 h-4 w-4" />
                                                        Edit Truck
                                                    </Link>
                                                </DropdownMenuItem>

                                                {/* Renew Actions - Only show if needed */}
                                                {truck.taxExpiryDate && (new Date(truck.taxExpiryDate).getTime() - new Date().getTime()) / (1000 * 60 * 60 * 24) <= 60 && (
                                                    <DropdownMenuItem asChild>
                                                        <Link href={`/admin/trucks/renew?id=${truck.id}&type=tax`} className="flex items-center cursor-pointer text-orange-600 focus:text-orange-700" onClick={(e) => e.stopPropagation()}>
                                                            <FileText className="mr-2 h-4 w-4" />
                                                            Renew - TAX
                                                        </Link>
                                                    </DropdownMenuItem>
                                                )}

                                                {truck.insuranceExpiryDate && (new Date(truck.insuranceExpiryDate).getTime() - new Date().getTime()) / (1000 * 60 * 60 * 24) <= 60 && (
                                                    <DropdownMenuItem asChild>
                                                        <Link href={`/admin/trucks/renew?id=${truck.id}&type=insurance`} className="flex items-center cursor-pointer text-blue-600 focus:text-blue-700" onClick={(e) => e.stopPropagation()}>
                                                            <ShieldAlert className="mr-2 h-4 w-4" />
                                                            Renew - Insurance
                                                        </Link>
                                                    </DropdownMenuItem>
                                                )}

                                                <DropdownMenuItem asChild>
                                                    <Link href={`/admin/trucks/maintenance?id=${truck.id}`} className="flex items-center cursor-pointer text-yellow-600 focus:text-yellow-700" onClick={(e) => e.stopPropagation()}>
                                                        <Wrench className="mr-2 h-4 w-4" />
                                                        Record Maintenance
                                                    </Link>
                                                </DropdownMenuItem>
                                            </DropdownMenuContent>
                                        </DropdownMenu>
                                    </TableCell>
                                </TableRow>
                            ))
                        )}
                    </TableBody>
                </Table>

                {/* Pagination */}
                <div className="flex items-center justify-between px-4 py-4 border-t border-border/50 bg-muted/20">
                    <div className="text-sm text-muted-foreground">
                        Showing {paginatedTrucks.length > 0 ? (currentPage - 1) * itemsPerPage + 1 : 0} to {Math.min(currentPage * itemsPerPage, filteredTrucks.length)} of {filteredTrucks.length} entries
                    </div>
                    <div className="flex items-center gap-2">
                        <Button
                            variant="outline"
                            size="icon"
                            className="h-8 w-8"
                            onClick={() => setCurrentPage(p => Math.max(1, p - 1))}
                            disabled={currentPage === 1}
                        >
                            <ChevronLeft className="h-4 w-4" />
                        </Button>
                        <div className="flex items-center gap-1">
                            {Array.from({ length: Math.min(5, totalPages) }, (_, i) => {
                                // Simple logic to show first 5 pages or context window
                                // For now, just show first 5 if total > 5 is complicated without component
                                let pageNum = i + 1;
                                if (totalPages > 5 && currentPage > 3) {
                                    pageNum = currentPage - 2 + i;
                                    if (pageNum > totalPages) pageNum = totalPages - (4 - i);
                                }

                                return (
                                    <Button
                                        key={pageNum}
                                        variant={currentPage === pageNum ? "default" : "outline"}
                                        size="sm"
                                        className={`h-8 w-8 p-0 ${currentPage === pageNum ? "bg-blue-600 hover:bg-blue-700" : ""}`}
                                        onClick={() => setCurrentPage(pageNum)}
                                    >
                                        {pageNum}
                                    </Button>
                                );
                            })}
                        </div>
                        <Button
                            variant="outline"
                            size="icon"
                            className="h-8 w-8"
                            onClick={() => setCurrentPage(p => Math.min(totalPages, p + 1))}
                            disabled={currentPage === totalPages || totalPages === 0}
                        >
                            <ChevronRight className="h-4 w-4" />
                        </Button>
                    </div>
                </div>
            </div>
        </div>
    );
}
