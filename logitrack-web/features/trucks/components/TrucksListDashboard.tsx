"use client";

import { useRouter } from "next/navigation";
import dynamic from "next/dynamic";
import Link from "next/link";
import {
    Plus,
    Search,
    MoreHorizontal,
    Eye,
    Edit,
    ChevronLeft,
    ChevronRight,
    ChevronDown,
    Users,
    RefreshCcw,
    Upload,
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
import { useLanguage } from "@/context/language";
import { PageHeader } from "@/components/page-header";
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
import { truckStatusTone, toneBadgeVariant } from "@/lib/statusPresentation";

import { TruckQueryLoading } from "@/features/trucks/components/TruckQueryLoading";
import { TruckQueryError } from "@/features/trucks/components/TruckQueryError";
import { classifyPM, pmLabel } from "../utils/pm";
import { truckTypeLabel } from "../utils/labels";
import { useTrucksList } from "../hooks/useTrucksList";
import { TruckComplianceCards } from "./TruckComplianceCards";
import { LazyDialog, LazyDialogLoading } from "@/components/lazy-dialog";
import { LoadMore } from "@/components/load-more";

// The import dialog carries xlsx: loaded on the first open only (developer-spec.md §10.11).
const TruckImportDialog = dynamic(() => import("./TruckImportDialog").then((m) => m.TruckImportDialog), {
    ssr: false,
    loading: LazyDialogLoading,
});

export default function TrucksListDashboard() {
    const { t } = useLanguage();
    const router = useRouter();

    const {
        loading, error, hasData, assignmentsLoading, assignmentsError, assignmentsHaveData,
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
        hasNextPage,
        isFetchingNextPage,
        fetchNextPage,
        handleCardFilterChange,
        activeCardFilter,
        clearFilters
    } = useTrucksList();

    const paginatedTrucks = filteredTrucks.slice(
        (currentPage - 1) * itemsPerPage,
        currentPage * itemsPerPage
    );

    const getStatusLabel = (status: string) => {
        switch (status) {
            case "active": return t('trucks.status.active');
            case "maintenance": return t('trucks.status.maintenance');
            case "in-transit": return t('trucks.status.inTransit');
            case "inactive": return t('trucks.status.inactive');
            case "insurance_claim": return t('trucks.detail.status.insuranceClaim');
            case "sold": return t('trucks.detail.status.sold');
            default: return status;
        }
    };

    const getOwnershipBadge = (type: string) => {
        if (type === 'own') {
            return <Badge variant="secondary">{t('trucks.badge.company')}</Badge>;
        }
        return <Badge variant="secondary">{t('trucks.badge.partner')}</Badge>;
    };

    return (
        <div className="flex flex-col gap-section">
            <PageHeader
                title={t("nav.trucks")}
                description={t('trucks.monitor')}
                actions={
                    <>
                        <LazyDialog
                            trigger={
                                <Button variant="outline" className="gap-2">
                                    <Upload className="h-4 w-4" aria-hidden />
                                    {t('trucks.import')}
                                </Button>
                            }
                        >
                            {(dialog) => <TruckImportDialog {...dialog} />}
                        </LazyDialog>
                        <Button asChild className="gap-2">
                            <Link href="/app/trucks/new" prefetch={false}>
                                <Plus className="h-4 w-4" aria-hidden />
                                {t('trucks.addTruck')}
                            </Link>
                        </Button>
                    </>
                }
            />

            {/* Compliance Cards */}
            <TruckComplianceCards onFilterChange={handleCardFilterChange} activeFilter={activeCardFilter} />

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
                                <SelectItem key={type} value={type}>{truckTypeLabel(type, t)}</SelectItem>
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

                    <Button variant="ghost" onClick={clearFilters} className="px-2">
                        {t('trucks.filter.clear')}
                    </Button>

                    <Button
                        variant="outline"
                        size="icon"
                        onClick={refresh}
                        title={t('common.refresh')}
                        disabled={isRefreshing}
                    >
                        <RefreshCcw className={`h-4 w-4 ${isRefreshing ? 'animate-spin' : ''}`} />
                    </Button>
                </div>
            </div>

            {error && <TruckQueryError onRetry={refresh} retrying={isRefreshing} />}
            {assignmentsError && <TruckQueryError onRetry={refresh} retrying={isRefreshing} />}
            {/* Content */}
            <div className="border rounded-lg bg-card overflow-hidden">
                <Table>
                    <TableHeader className="bg-muted/50">
                        <TableRow className="hover:bg-transparent border-b border-border/50">
                            <TableHead className="w-[100px]">{t('trucks.table.id')}</TableHead>
                            <TableHead>{t('trucks.table.plate')}</TableHead>
                            <TableHead>{t('trucks.table.model')}</TableHead>
                            <TableHead>{t('trucks.table.driver')}</TableHead>
                            <TableHead>{t('trucks.table.ownership')}</TableHead>
                            <TableHead>{t('trucks.table.pmStatus')}</TableHead>
                            <TableHead>{t('trucks.table.status')}</TableHead>
                            <TableHead className="text-right">{t('common.actions')}</TableHead>
                        </TableRow>
                    </TableHeader>
                    <TableBody>
                        {loading ? (
                            <TableRow><TableCell colSpan={8}><TruckQueryLoading label={t("common.loading")} /></TableCell></TableRow>
                        ) : error && !hasData ? null : paginatedTrucks.length === 0 ? (
                            <TableRow>
                                <TableCell colSpan={8} className="h-32 text-center">
                                    <p className="text-sm text-muted-foreground">{t('masterData.trucks.noMatch')}</p>
                                </TableCell>
                            </TableRow>
                        ) : (
                            paginatedTrucks.map((truck) => (
                                <TableRow key={truck.id} className="cursor-pointer hover:bg-muted/50 transition-colors border-b border-border/50" onClick={() => router.push(`/app/trucks/view?id=${truck.id}`)}>
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
                                        {assignmentsLoading ? <span role="status">{t("common.loading")}</span> : assignmentsError && !assignmentsHaveData ? "-" : (() => {
                                            if (truck.drivers.length === 0) {
                                                return <span className="text-muted-foreground text-xs">-</span>;
                                            }

                                            const assignments = truck.drivers;

                                            if (assignments.length === 1) {
                                                const assignment = assignments[0];
                                                return (
                                                    <div className="flex items-center gap-2">
                                                        <div className="h-6 w-6 rounded-full bg-primary/10 flex items-center justify-center text-xs font-bold text-primary">
                                                            {assignment.driverName?.trim().substring(0, 2).toUpperCase() || "-"}
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
                                                            {t('masterData.trucks.driverCount', { count: assignments.length })}
                                                            <ChevronDown className="h-3 w-3 opacity-50" />
                                                        </Badge>
                                                    </DropdownMenuTrigger>
                                                    <DropdownMenuContent align="start" className="w-[200px]">
                                                        <DropdownMenuLabel>{t('masterData.trucks.assignedDrivers')}</DropdownMenuLabel>
                                                        {assignments.map((assignment, idx) => (
                                                            <div key={idx} className="flex items-center gap-2 px-2 py-1.5 text-sm">
                                                                <div className="h-6 w-6 rounded-full bg-primary/10 flex items-center justify-center text-xs font-bold text-primary shrink-0">
                                                                    {assignment.driverName?.trim().substring(0, 2).toUpperCase() || "-"}
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
                                                const pm = classifyPM(truck);
                                                const label = pmLabel(pm, t);
                                                return <Link className="inline-flex items-center max-md:min-h-11 max-md:min-w-11" href={`/app/trucks/maintenance?id=${truck.id}`} prefetch={false} onClick={(e) => e.stopPropagation()}>
                                                    <Badge variant={pm.status === "overdue" ? "destructive" : pm.status === "due" ? "warning" : pm.status === "normal" ? "success" : "secondary"}>{label}</Badge>
                                                </Link>;
                                            })()}
                                        </div>
                                    </TableCell>
                                    <TableCell>
                                        <Badge variant={toneBadgeVariant(truckStatusTone(String(truck.status)))}>{getStatusLabel(String(truck.status))}</Badge>
                                    </TableCell>
                                    <TableCell className="text-right">
                                        <DropdownMenu>
                                            <DropdownMenuTrigger asChild onClick={(e) => e.stopPropagation()}>
                                                <Button variant="ghost" className="h-8 w-8 p-0">
                                                    <span className="sr-only">{t("common.actionsForRow")}</span>
                                                    <MoreHorizontal className="h-4 w-4" />
                                                </Button>
                                            </DropdownMenuTrigger>
                                            <DropdownMenuContent align="end">
                                                <DropdownMenuLabel>{t('common.actions')}</DropdownMenuLabel>
                                                <DropdownMenuItem asChild>
                                                    <Link href={`/app/trucks/view?id=${truck.id}`} prefetch={false} className="flex items-center cursor-pointer" onClick={(e) => e.stopPropagation()}>
                                                        <Eye className="mr-2 h-4 w-4" />
                                                        {t('trucks.action.view')}
                                                    </Link>
                                                </DropdownMenuItem>
                                                <DropdownMenuItem asChild>
                                                    <Link href={`/app/trucks/edit?id=${truck.id}`} prefetch={false} className="flex items-center cursor-pointer" onClick={(e) => e.stopPropagation()}>
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

                {/* Pagination over the loaded rows; the next keyset page loads on demand (T25). */}
                {!(error && !hasData) && <>
                <div className="px-4 pt-4 border-t border-border/50 bg-muted/20">
                    <LoadMore loaded={filteredTrucks.length} hasNextPage={hasNextPage} isFetchingNextPage={isFetchingNextPage} onLoadMore={fetchNextPage} />
                </div>
                <div className="flex flex-wrap items-center justify-between gap-4 px-4 py-4 bg-muted/20">
                    <div className="text-sm text-muted-foreground">
                        {t('masterData.list.showing', { from: paginatedTrucks.length > 0 ? (currentPage - 1) * itemsPerPage + 1 : 0, to: Math.min(currentPage * itemsPerPage, filteredTrucks.length), total: filteredTrucks.length })}
                    </div>
                    <div className="flex items-center gap-2">
                        <Button aria-label={t("common.previousPage")}
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
                                        className="h-8 w-8 p-0"
                                        onClick={() => setCurrentPage(pageNum)}
                                    >
                                        {pageNum}
                                    </Button>
                                );
                            })}
                        </div>
                        <Button aria-label={t("common.nextPage")}
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
                </>}
            </div>
        </div>
    );
}
