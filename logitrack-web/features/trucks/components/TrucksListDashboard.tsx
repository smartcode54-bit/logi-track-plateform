"use client";

import { ReactNode } from "react";
import { useRouter } from "next/navigation";
import Link from "next/link";
import {
    Plus,
    Search,
    Download,
    MoreHorizontal,
    Loader2,
    Eye,
    Edit,
    ChevronLeft,
    ChevronRight,
    ChevronDown,
    Users,
    RefreshCcw,
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

import { useTrucksList } from "../hooks/useTrucksList";
import { TruckComplianceCards } from "./TruckComplianceCards";
import { TruckImportDialog } from "./TruckImportDialog";

export default function TrucksListDashboard() {
    const { t } = useLanguage();
    const router = useRouter();

    const {
        loading,
        refreshKey,
        setRefreshKey,
        isRefreshing,
        setIsRefreshing,
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
        handleCardFilterChange,
        clearFilters
    } = useTrucksList();

    const paginatedTrucks = filteredTrucks.slice(
        (currentPage - 1) * itemsPerPage,
        currentPage * itemsPerPage
    );

    const getStatusColor = (status: string) => {
        switch (status) {
            case "active": return "bg-green-500/10 text-green-500 hover:bg-green-500/20 border-green-500/20";
            case "maintenance": return "bg-red-500/10 text-red-500 hover:bg-red-500/20 border-red-500/20";
            case "in-transit": return "bg-yellow-500/10 text-yellow-500 hover:bg-yellow-500/20 border-yellow-500/20";
            case "inactive": return "bg-gray-500/10 text-gray-500 hover:bg-gray-500/20 border-gray-500/20";
            default: return "bg-gray-500/10 text-gray-500 hover:bg-gray-500/20 border-gray-500/20";
        }
    };

    const getStatusLabel = (status: string) => {
        switch (status) {
            case "active": return t('trucks.status.active');
            case "maintenance": return t('trucks.status.maintenance');
            case "in-transit": return t('trucks.status.inTransit');
            case "inactive": return t('trucks.status.inactive');
            default: return status;
        }
    };

    const getOwnershipBadge = (type: string) => {
        if (type === 'own') {
            return <Badge variant="secondary" className="bg-blue-900/40 text-blue-400 hover:bg-blue-900/60 border-blue-800">{t('trucks.badge.company')}</Badge>;
        }
        return <Badge variant="secondary" className="bg-slate-800 text-slate-400 hover:bg-slate-700">{t('trucks.badge.partner')}</Badge>;
    };

    return (
        <div className="container mx-auto p-6 space-y-6 max-w-[1600px]">
            {/* Header */}
            <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">{t('trucks.title')}</h1>
                    <p className="text-muted-foreground mt-1">
                        {t('trucks.monitor')}
                    </p>
                </div>
                <div className="flex gap-3">
                    <Button variant="outline" className="gap-2">
                        <Download className="h-4 w-4" />
                        {t('trucks.export')}
                    </Button>
                    <TruckImportDialog />
                    <Button asChild className="bg-blue-600 hover:bg-blue-700 text-white gap-2">
                        <Link href="/admin/trucks/new" prefetch={false}>
                            <Plus className="h-4 w-4" />
                            {t('trucks.addTruck')}
                        </Link>
                    </Button>
                </div>
            </div>

            {/* Compliance Cards */}
            <TruckComplianceCards
                onFilterChange={handleCardFilterChange}
                refreshKey={refreshKey}
                onLoadingChange={setIsRefreshing}
            />

            {/* Filter Bar */}
            <div className="flex flex-col xl:flex-row gap-4 bg-card/50 p-4 rounded-lg border border-border/50">
                <div className="relative flex-1">
                    <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
                    <Input
                        placeholder={t('trucks.filter.search')}
                        className="pl-10 bg-background/50 border-border/50 focus-visible:ring-1"
                        value={searchQuery}
                        onChange={(e) => setSearchQuery(e.target.value)}
                    />
                </div>
                <div className="flex flex-wrap gap-3">
                    <Select value={typeFilter} onValueChange={setTypeFilter}>
                        <SelectTrigger className="w-[180px] bg-background/50 border-border/50">
                            <SelectValue placeholder={t('trucks.filter.allTypes')} />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value="all">{t('trucks.filter.allTypes')}</SelectItem>
                            {uniqueTypes.map(type => (
                                <SelectItem key={type} value={type}>{type}</SelectItem>
                            ))}
                        </SelectContent>
                    </Select>

                    <Select value={statusFilter} onValueChange={setStatusFilter}>
                        <SelectTrigger className="w-[180px] bg-background/50 border-border/50">
                            <SelectValue placeholder={t('trucks.filter.allStatuses')} />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value="all">{t('trucks.filter.allStatuses')}</SelectItem>
                            <SelectItem value="active">{t('trucks.status.active')}</SelectItem>
                            <SelectItem value="maintenance">{t('trucks.status.maintenance')}</SelectItem>
                            <SelectItem value="inactive">{t('trucks.status.inactive')}</SelectItem>
                        </SelectContent>
                    </Select>

                    <Select value={groupFilter} onValueChange={setGroupFilter}>
                        <SelectTrigger className="w-[180px] bg-background/50 border-border/50">
                            <SelectValue placeholder={t('trucks.filter.allGroups')} />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value="all">{t('trucks.filter.allGroups')}</SelectItem>
                            <SelectItem value="own">{t('trucks.filter.own')}</SelectItem>
                            <SelectItem value="subcontractor">{t('trucks.filter.subcontractor')}</SelectItem>
                        </SelectContent>
                    </Select>

                    <Button variant="ghost" onClick={clearFilters} className="text-blue-500 hover:text-blue-700 hover:bg-transparent px-2">
                        {t('trucks.filter.clear')}
                    </Button>

                    <Button
                        variant="outline"
                        size="icon"
                        onClick={() => setRefreshKey(prev => prev + 1)}
                        title={t('common.refresh')}
                        disabled={isRefreshing}
                    >
                        <RefreshCcw className={`h-4 w-4 ${isRefreshing ? 'animate-spin' : ''}`} />
                    </Button>
                </div>
            </div>

            {/* Content */}
            <div className="border rounded-lg bg-card overflow-hidden">
                <Table>
                    <TableHeader className="bg-muted/50">
                        <TableRow className="hover:bg-transparent border-b border-border/50">
                            <TableHead className="w-[100px] text-xs font-semibold tracking-wider text-muted-foreground uppercase">{t('trucks.table.id')}</TableHead>
                            <TableHead className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">{t('trucks.table.plate')}</TableHead>
                            <TableHead className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">{t('trucks.table.model')}</TableHead>
                            <TableHead className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">{t('trucks.table.driver')}</TableHead>
                            <TableHead className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">{t('trucks.table.ownership')}</TableHead>
                            <TableHead className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">{t('trucks.table.pmStatus')}</TableHead>
                            <TableHead className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">{t('trucks.table.status')}</TableHead>
                            <TableHead className="text-right text-xs font-semibold tracking-wider text-muted-foreground uppercase">{t('common.actions')}</TableHead>
                        </TableRow>
                    </TableHeader>
                    <TableBody>
                        {loading ? (
                            <TableRow>
                                <TableCell colSpan={8} className="h-32 text-center">
                                    <div className="flex flex-col items-center justify-center gap-2">
                                        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
                                        <p className="text-sm text-muted-foreground">Loading trucks...</p>
                                    </div>
                                </TableCell>
                            </TableRow>
                        ) : paginatedTrucks.length === 0 ? (
                            <TableRow>
                                <TableCell colSpan={8} className="h-32 text-center">
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
                                                const km = truck.nextServiceMileage && truck.currentMileage ? truck.nextServiceMileage - truck.currentMileage : null;
                                                const days = truck.nextServiceDate ? Math.ceil((new Date(truck.nextServiceDate).getTime() - new Date().getTime()) / (1000 * 60 * 60 * 24)) : null;

                                                let pmBadge: ReactNode = null;

                                                if (truck.truckStatus === 'maintenance') {
                                                    pmBadge = <Badge variant="secondary" className="text-[10px] px-1 py-0 h-5 w-fit bg-yellow-100 text-yellow-700 border-yellow-200">{t('trucks.badge.pmInShop')}</Badge>;
                                                } else if (km !== null && km < 0) {
                                                    pmBadge = <Badge variant="destructive" className="text-[10px] px-1 py-0 h-5 w-fit">PM: {-km}km Over</Badge>;
                                                } else if (days !== null && days < 0) {
                                                    pmBadge = <Badge variant="destructive" className="text-[10px] px-1 py-0 h-5 w-fit">{t('trucks.badge.pmOverdue')}</Badge>;
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
                                                                    <Link href={`/admin/trucks/maintenance?id=${truck.id}`} prefetch={false} onClick={(e) => e.stopPropagation()} className="hover:opacity-80 transition-opacity w-fit">
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
                                                    <Link href={`/admin/trucks/view?id=${truck.id}`} prefetch={false} className="flex items-center cursor-pointer" onClick={(e) => e.stopPropagation()}>
                                                        <Eye className="mr-2 h-4 w-4" />
                                                        {t('trucks.action.view')}
                                                    </Link>
                                                </DropdownMenuItem>
                                                <DropdownMenuItem asChild>
                                                    <Link href={`/admin/trucks/edit?id=${truck.id}`} prefetch={false} className="flex items-center cursor-pointer" onClick={(e) => e.stopPropagation()}>
                                                        <Edit className="mr-2 h-4 w-4" />
                                                        {t('trucks.action.edit')}
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
