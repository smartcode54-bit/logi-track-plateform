"use client";

import { useMemo } from "react";
import { format } from "date-fns";
import {
    Calendar as CalendarIcon,
    Truck,
    PackageCheck,
    Loader2,
    ArrowRight,
    Search,
    Package,
    Navigation,
    MapPin,
    Clock,
    Camera,
    ExternalLink,
    ClipboardCheck,
    Pencil,
    RefreshCw,
} from "lucide-react";

import { useLanguage } from "@/context/language";
import { useAuth } from "@/context/auth";
import { canEditTripDetails } from "@/lib/permissions";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Calendar } from "@/components/ui/calendar";
import {
    Popover,
    PopoverContent,
    PopoverTrigger,
} from "@/components/ui/popover";
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select";
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from "@/components/ui/dialog";
import { cn } from "@/lib/utils";

import { EditTripDetailsDialog } from "./EditTripDetailsDialog";
import { useDriverMonitor } from "../hooks/useDriverMonitor";
import { TRIP_STATUS_ENUM } from "@/validate/tripRecordSchema";

// Constants/Helpers
const STATUS_COLOR: Record<string, string> = {
    loading: "bg-slate-500/15 text-slate-600 border-slate-500/25 dark:text-slate-400",
    departure: "bg-blue-500/15 text-blue-600 border-blue-500/25 dark:text-blue-400",
    in_transit: "bg-amber-500/15 text-amber-600 border-amber-500/25 dark:text-amber-400",
    incident: "bg-red-500/15 text-red-600 border-red-500/25 dark:text-red-400",
    delivered: "bg-emerald-500/15 text-emerald-600 border-emerald-500/25 dark:text-emerald-400",
};

const JOB_TYPE_COLOR: Record<string, string> = {
    first_mile: "bg-indigo-500/15 text-indigo-600 border-indigo-500/25 dark:text-indigo-400",
    line_haul: "bg-orange-500/15 text-orange-600 border-orange-500/25 dark:text-orange-400",
};

const JOB_TYPE_LABEL: Record<string, string> = {
    first_mile: "First Mile",
    line_haul: "Line Haul",
};

function toDateLocal(val: any): Date | null {
    if (!val) return null;
    if (val instanceof Date) return val;
    if (typeof val?.toDate === "function") return val.toDate();
    if (typeof val === "string") return new Date(val);
    return null;
}

export default function DriverMonitorDashboard() {
    const { t } = useLanguage();
    const auth = useAuth();
    const canEdit = canEditTripDetails(auth?.customClaims ?? null);

    const {
        paginatedTrips,
        totalPages,
        currentPage,
        setCurrentPage,
        stats,
        filteredTrips,
        loading,
        date,
        setDate,
        statusFilter,
        setStatusFilter,
        jobTypeFilter,
        setJobTypeFilter,
        searchQuery,
        setSearchQuery,
        detailTrip,
        setDetailTrip,
        previewPhoto,
        setPreviewPhoto,
        editTripDialogOpen,
        setEditTripDialogOpen,
        incidentReportsByTripId,
        checkInAtByTaskId,
        getDriver,
        getSourceDisplayName,
        fetchHubs,
        itemsPerPage
    } = useDriverMonitor();

    const getDriverName = (driverId?: string) => {
        if (!driverId) return t("driverMonitor.table.unknown");
        const driver = getDriver(driverId);
        if (!driver) return driverId.slice(0, 8) + "...";
        return `${driver.firstName} ${driver.lastName}`.trim() || "-";
    };

    const getLicensePlate = (driverId?: string) => {
        if (!driverId) return "-";
        const driver = getDriver(driverId);
        return driver?.currentAssignment?.truckPlate ?? "-";
    };

    const getDriverDisplayFull = (driverId?: string) => {
        if (!driverId) return t("driverMonitor.table.unknown");
        const driver = getDriver(driverId);
        if (!driver) return driverId.slice(0, 8) + "...";
        const plate = driver.currentAssignment?.truckPlate ?? "-";
        return `${driver.firstName} ${driver.lastName} - ${plate}`;
    };

    const formatTimestamp = (val: any) => {
        const d = toDateLocal(val);
        return d ? format(d, "dd/MM/yy HH:mm") : "-";
    };

    return (
        <div className="container mx-auto p-6 space-y-6 max-w-[1600px]">
            {/* Header */}
            <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">
                        {t("driverMonitor.title")}
                    </h1>
                    <p className="text-muted-foreground mt-1">
                        {t("driverMonitor.subtitle")}
                    </p>
                </div>
                <Button variant="outline" size="icon" onClick={() => fetchHubs()} aria-label={t("driverMonitor.refresh")}>
                    <RefreshCw className="h-4 w-4" />
                </Button>
            </div>

            {/* Stats Cards */}
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-5 gap-4">
                <Card className="bg-card">
                    <CardContent className="p-6 flex items-center justify-between">
                        <div>
                            <p className="text-sm font-medium text-muted-foreground">
                                {t("driverMonitor.stats.totalTrips")}
                            </p>
                            <h2 className="text-3xl font-bold mt-1">{stats.total}</h2>
                        </div>
                        <div className="h-12 w-12 rounded-full bg-blue-100 dark:bg-blue-900/30 flex items-center justify-center text-blue-500">
                            <Package className="h-6 w-6" />
                        </div>
                    </CardContent>
                </Card>

                <Card className="bg-card">
                    <CardContent className="p-6 flex items-center justify-between">
                        <div>
                            <p className="text-sm font-medium text-muted-foreground">
                                {t("driverMonitor.stats.loading")}
                            </p>
                            <h2 className="text-3xl font-bold mt-1">{stats.loading}</h2>
                        </div>
                        <div className="h-12 w-12 rounded-full bg-slate-100 dark:bg-slate-800 flex items-center justify-center text-slate-500">
                            <Loader2 className="h-5 w-5" />
                        </div>
                    </CardContent>
                </Card>

                <Card className="bg-card">
                    <CardContent className="p-6 flex items-center justify-between">
                        <div>
                            <p className="text-sm font-medium text-muted-foreground">
                                {t("driverMonitor.stats.checkIn")}
                            </p>
                            <h2 className="text-3xl font-bold mt-1">
                                <span className="text-emerald-600 dark:text-emerald-400">{stats.checkInActual}</span>
                                <span className="text-muted-foreground text-base font-normal ml-1">/ {stats.checkInTotal}</span>
                            </h2>
                        </div>
                        <div className="h-12 w-12 rounded-full bg-teal-100 dark:bg-teal-900/30 flex items-center justify-center text-teal-500">
                            <ClipboardCheck className="h-5 w-5" />
                        </div>
                    </CardContent>
                </Card>

                <Card className="bg-card">
                    <CardContent className="p-6 flex items-center justify-between">
                        <div>
                            <p className="text-sm font-medium text-muted-foreground">
                                {t("driverMonitor.stats.inTransit")}
                            </p>
                            <h2 className="text-3xl font-bold mt-1">{stats.inTransit}</h2>
                        </div>
                        <div className="h-12 w-12 rounded-full bg-amber-100 dark:bg-amber-900/30 flex items-center justify-center text-amber-500">
                            <Truck className="h-5 w-5" />
                        </div>
                    </CardContent>
                </Card>

                <Card className="bg-card">
                    <CardContent className="p-6 flex items-center justify-between">
                        <div>
                            <p className="text-sm font-medium text-muted-foreground">
                                {t("driverMonitor.stats.delivered")}
                            </p>
                            <h2 className="text-3xl font-bold mt-1">{stats.delivered}</h2>
                        </div>
                        <div className="h-12 w-12 rounded-full bg-emerald-100 dark:bg-emerald-900/30 flex items-center justify-center text-emerald-500">
                            <PackageCheck className="h-5 w-5" />
                        </div>
                    </CardContent>
                </Card>
            </div>

            {/* Filters */}
            <div className="space-y-4">
                <div className="flex flex-col sm:flex-row flex-wrap gap-4 bg-card/50 p-4 rounded-lg border border-border/50">
                    <div className="flex gap-2 border-b sm:border-b-0 sm:border-r border-border/50 pb-4 sm:pb-0 sm:pr-4">
                        <Button
                            variant={jobTypeFilter === "all" ? "secondary" : "ghost"}
                            onClick={() => setJobTypeFilter("all")}
                            className="whitespace-nowrap"
                            size="sm"
                        >
                            {t("driverMonitor.jobType.all")}
                        </Button>
                        <Button
                            variant={jobTypeFilter === "first_mile" ? "secondary" : "ghost"}
                            onClick={() => setJobTypeFilter("first_mile")}
                            className="whitespace-nowrap"
                            size="sm"
                        >
                            <span className="w-2 h-2 rounded-full bg-indigo-500 mr-1.5" />
                            {t("driverMonitor.jobType.firstMile")}
                        </Button>
                        <Button
                            variant={jobTypeFilter === "line_haul" ? "secondary" : "ghost"}
                            onClick={() => setJobTypeFilter("line_haul")}
                            className="whitespace-nowrap"
                            size="sm"
                        >
                            <span className="w-2 h-2 rounded-full bg-orange-500 mr-1.5" />
                            {t("driverMonitor.jobType.lineHaul")}
                        </Button>
                    </div>

                    <Popover>
                        <PopoverTrigger asChild>
                            <Button
                                variant="outline"
                                size="sm"
                                className={cn(
                                    "w-[180px] justify-start text-left font-normal",
                                    !date && "text-muted-foreground"
                                )}
                            >
                                <CalendarIcon className="mr-2 h-4 w-4" />
                                {date ? format(date, "dd/MM/yyyy") : t("driverMonitor.filter.pickDate")}
                            </Button>
                        </PopoverTrigger>
                        <PopoverContent className="w-auto p-0" align="start">
                            <Calendar
                                mode="single"
                                selected={date}
                                onSelect={setDate as any}
                                initialFocus
                            />
                        </PopoverContent>
                    </Popover>

                    <Select value={statusFilter} onValueChange={setStatusFilter}>
                        <SelectTrigger className="w-[180px] h-9">
                            <SelectValue placeholder={t("driverMonitor.filter.allStatuses")} />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value="all">{t("driverMonitor.filter.allStatuses")}</SelectItem>
                            {TRIP_STATUS_ENUM.map((s) => (
                                <SelectItem key={s} value={s}>
                                    {t(`driverMonitor.status.${s}` as any)}
                                </SelectItem>
                            ))}
                        </SelectContent>
                    </Select>

                    <div className="relative flex-1">
                        <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
                        <Input
                            placeholder={t("driverMonitor.filter.searchDriver")}
                            className="pl-10 bg-background/50 border-border/50 h-9"
                            value={searchQuery}
                            onChange={(e) => setSearchQuery(e.target.value)}
                        />
                    </div>

                    {date && (
                        <Button
                            variant="ghost"
                            size="sm"
                            onClick={() => setDate(undefined)}
                            className="text-muted-foreground"
                        >
                            ✕
                        </Button>
                    )}
                </div>

                {/* Table */}
                <div className="border rounded-lg bg-card overflow-hidden">
                    <div className="overflow-x-auto">
                        <Table className="min-w-[900px]">
                            <TableHeader className="bg-muted/50">
                                <TableRow className="border-b border-border/50">
                                    <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">{t("driverMonitor.table.tripId")}</TableHead>
                                    <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">{t("driverMonitor.table.createdAt")}</TableHead>
                                    <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">{t("driverMonitor.table.driver")}</TableHead>
                                    <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">{t("driverMonitor.table.licensePlate")}</TableHead>
                                    <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">{t("driverMonitor.table.jobType")}</TableHead>
                                    <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">{t("driverMonitor.table.origin")}</TableHead>
                                    <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">{t("driverMonitor.table.destination")}</TableHead>
                                    <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">{t("driverMonitor.table.sealCode")}</TableHead>
                                    <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">{t("driverMonitor.table.status")}</TableHead>
                                    <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">{t("driverMonitor.table.deliveredTime")}</TableHead>
                                </TableRow>
                            </TableHeader>
                            <TableBody>
                                {loading ? (
                                    <TableRow>
                                        <TableCell colSpan={10} className="h-32 text-center">
                                            <div className="flex flex-col items-center justify-center gap-2">
                                                <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
                                                <p className="text-sm text-muted-foreground">{t("driverMonitor.table.loadingData")}</p>
                                            </div>
                                        </TableCell>
                                    </TableRow>
                                ) : paginatedTrips.length === 0 ? (
                                    <TableRow>
                                        <TableCell colSpan={10} className="h-32 text-center">
                                            <p className="text-sm text-muted-foreground">{t("driverMonitor.table.noTrips")}</p>
                                        </TableCell>
                                    </TableRow>
                                ) : (
                                    paginatedTrips.map((trip) => (
                                        <TableRow
                                            key={trip.id}
                                            className="cursor-pointer hover:bg-muted/50 transition-colors border-b border-border/50"
                                            onClick={() => setDetailTrip(trip)}
                                        >
                                            <TableCell><span className="font-mono text-xs">{trip.spxTripId || trip.id?.slice(0, 10) || "-"}</span></TableCell>
                                            <TableCell className="text-sm text-muted-foreground">
                                                {formatTimestamp((trip.id && checkInAtByTaskId[trip.id]) || trip.createdAt)}
                                            </TableCell>
                                            <TableCell><span className="font-medium text-sm">{getDriverName(trip.driverId)}</span></TableCell>
                                            <TableCell><span className="font-mono text-sm text-muted-foreground">{getLicensePlate(trip.driverId)}</span></TableCell>
                                            <TableCell>
                                                <Badge variant="secondary" className={cn("font-medium border", JOB_TYPE_COLOR[trip.jobType] || "bg-gray-500/10 text-gray-500")}>
                                                    {JOB_TYPE_LABEL[trip.jobType] || trip.jobType}
                                                </Badge>
                                            </TableCell>
                                            <TableCell className="text-sm"><span className="font-medium">{getSourceDisplayName(trip.origin)}</span></TableCell>
                                            <TableCell className="text-sm"><span className="font-medium">{getSourceDisplayName(trip.destination)}</span></TableCell>
                                            <TableCell><span className="font-mono text-xs">{trip.sealCode || "-"}</span></TableCell>
                                            <TableCell>
                                                <div className="flex items-center gap-2">
                                                    <Badge variant="secondary" className={cn("font-medium border", STATUS_COLOR[trip.status] || "bg-gray-500/10 text-gray-500")}>
                                                        <span className="w-1.5 h-1.5 rounded-full bg-current mr-1.5" />
                                                        {t(`driverMonitor.status.${trip.status}` as any)}
                                                    </Badge>
                                                    {trip.id && incidentReportsByTripId[trip.id] && (
                                                        <img src="/exclamation_8848378.png" alt="incident" className="w-4 h-4 object-contain" title="Incident Reported" />
                                                    )}
                                                </div>
                                            </TableCell>
                                            <TableCell className="text-sm text-muted-foreground">
                                                {formatTimestamp(trip.status === "delivered" && trip.deliveredTimestamp ? trip.deliveredTimestamp : trip.updatedAt)}
                                            </TableCell>
                                        </TableRow>
                                    ))
                                )}
                            </TableBody>
                        </Table>
                    </div>

                    {/* Pagination */}
                    <div className="flex items-center justify-between px-4 py-4 border-t border-border/50 bg-muted/20">
                        <div className="text-sm text-muted-foreground">
                            {t("driverMonitor.pagination.showing")} {paginatedTrips.length > 0 ? (currentPage - 1) * itemsPerPage + 1 : 0} {t("driverMonitor.pagination.to")} {Math.min(currentPage * itemsPerPage, filteredTrips.length)} {t("driverMonitor.pagination.of")} {filteredTrips.length} {t("driverMonitor.pagination.entries")}
                        </div>
                        <div className="flex items-center gap-2">
                            <Button variant="outline" size="sm" onClick={() => setCurrentPage((p) => Math.max(1, p - 1))} disabled={currentPage === 1}>{t("driverMonitor.pagination.previous")}</Button>
                            <Button variant="outline" size="sm" onClick={() => setCurrentPage((p) => Math.min(totalPages, p + 1))} disabled={currentPage === totalPages || totalPages === 0}>{t("driverMonitor.pagination.next")}</Button>
                        </div>
                    </div>
                </div>
            </div>

            {/* Detail Dialog */}
            <Dialog open={!!detailTrip} onOpenChange={(open) => !open && setDetailTrip(null)}>
                <DialogContent className="max-w-2xl max-h-[90vh] overflow-y-auto">
                    <DialogHeader>
                        <DialogTitle className="flex items-center gap-2">
                            {t("driverMonitor.detail.title")}
                            {detailTrip && (
                                <Badge variant="secondary" className={cn("font-medium border ml-2", JOB_TYPE_COLOR[detailTrip.jobType] || "")}>
                                    {JOB_TYPE_LABEL[detailTrip.jobType] || detailTrip.jobType}
                                </Badge>
                            )}
                        </DialogTitle>
                        <DialogDescription>{detailTrip?.spxTripId || detailTrip?.id}</DialogDescription>
                    </DialogHeader>

                    {detailTrip && (
                        <div className="grid gap-6 py-2">
                             {detailTrip.id && incidentReportsByTripId[detailTrip.id] && (
                                <div className="space-y-3">
                                    <h4 className="text-sm font-semibold text-red-800 dark:text-red-400 flex items-center gap-2">
                                        <img src="/exclamation_8848378.png" alt="incident" className="w-4 h-4 object-contain" />
                                        {t("driverMonitor.detail.incidentReport", "Incident Report")}
                                    </h4>
                                    <div className="bg-red-50 dark:bg-red-950/20 border border-red-200 dark:border-red-900 rounded-lg p-4">
                                        <p className="text-sm text-red-700 dark:text-red-300">
                                            {incidentReportsByTripId[detailTrip.id].description}
                                        </p>
                                        {incidentReportsByTripId[detailTrip.id].delayCause && (
                                            <p className="text-xs font-medium text-red-800 dark:text-red-400 mt-2">
                                                Cause: {incidentReportsByTripId[detailTrip.id].delayCause!.replace("incident_cause_", "").toUpperCase()}
                                            </p>
                                        )}
                                        <p className="text-[10px] text-red-600/70 dark:text-red-400/70 mt-3 flex items-center gap-1.5">
                                            <Clock className="w-3 h-3" />
                                            {formatTimestamp(incidentReportsByTripId[detailTrip.id].createdAt)}
                                        </p>
                                    </div>
                                </div>
                            )}

                            <div className="space-y-3">
                                <h4 className="text-sm font-semibold text-muted-foreground flex items-center gap-2">
                                    <Navigation className="h-4 w-4" />
                                    {t("driverMonitor.detail.tripInfo")}
                                </h4>
                                <div className="grid grid-cols-2 gap-x-6 gap-y-2 text-sm bg-muted/30 rounded-lg p-4">
                                    <span className="text-muted-foreground">{t("driverMonitor.table.status")}</span>
                                    <span>
                                        <Badge variant="secondary" className={cn("font-medium border", STATUS_COLOR[detailTrip.status] || "")}>
                                            <span className="w-1.5 h-1.5 rounded-full bg-current mr-1.5" />
                                            {t(`driverMonitor.status.${detailTrip.status}` as any)}
                                        </Badge>
                                    </span>
                                    <span className="text-muted-foreground">{t("driverMonitor.detail.spxTripId")}</span>
                                    <span className="font-mono text-xs">{detailTrip.spxTripId || "-"}</span>
                                    <span className="text-muted-foreground">{t("driverMonitor.detail.sealCode")}</span>
                                    <span className="font-mono text-xs">{detailTrip.sealCode || "-"}</span>
                                </div>
                            </div>

                            <div className="space-y-3">
                                <h4 className="text-sm font-semibold text-muted-foreground flex items-center gap-2">
                                    <MapPin className="h-4 w-4" />
                                    {t("driverMonitor.detail.routeInfo")}
                                </h4>
                                <div className="flex items-center gap-4 bg-muted/30 rounded-lg p-4">
                                    <div className="flex-1 text-center">
                                        <p className="text-xs text-muted-foreground mb-1">{t("driverMonitor.detail.origin")}</p>
                                        <p className="font-semibold">{getSourceDisplayName(detailTrip.origin)}</p>
                                    </div>
                                    <ArrowRight className="h-5 w-5 text-muted-foreground shrink-0" />
                                    <div className="flex-1 text-center">
                                        <p className="text-xs text-muted-foreground mb-1">{t("driverMonitor.detail.destination")}</p>
                                        <p className="font-semibold">{getSourceDisplayName(detailTrip.destination)}</p>
                                    </div>
                                </div>
                            </div>

                            <div className="space-y-3">
                                <h4 className="text-sm font-semibold text-muted-foreground flex items-center gap-2">
                                    <Truck className="h-4 w-4" />
                                    {t("driverMonitor.detail.driverInfo")}
                                </h4>
                                <div className="grid grid-cols-2 gap-x-6 gap-y-2 text-sm bg-muted/30 rounded-lg p-4">
                                    <span className="text-muted-foreground">{t("driverMonitor.table.driver")}</span>
                                    <span className="font-medium">{getDriverDisplayFull(detailTrip.driverId)}</span>
                                </div>
                            </div>

                            {detailTrip.photos && detailTrip.photos.length > 0 && (
                                <div className="space-y-3">
                                    <h4 className="text-sm font-semibold text-muted-foreground flex items-center gap-2">
                                        <Camera className="h-4 w-4" />
                                        {t("driverMonitor.detail.photos")} ({detailTrip.photos.length})
                                    </h4>
                                    <div className="grid grid-cols-2 sm:grid-cols-3 gap-3">
                                        {detailTrip.photos.map((photo, idx) => (
                                            <button
                                                key={idx}
                                                type="button"
                                                onClick={() => setPreviewPhoto({ url: photo.url, type: photo.type, address: photo.geocoding?.address })}
                                                className="group relative block rounded-lg overflow-hidden border border-border/50 aspect-square bg-muted/50 hover:border-primary/50 transition-colors text-left w-full"
                                            >
                                                <img src={photo.url} alt={photo.type} className="object-cover w-full h-full" />
                                                <div className="absolute inset-x-0 bottom-0 bg-gradient-to-t from-black/70 to-transparent p-2">
                                                    <p className="text-white text-xs font-medium">{photo.type.replace(/_/g, " ")}</p>
                                                </div>
                                            </button>
                                        ))}
                                    </div>
                                </div>
                            )}
                        </div>
                    )}
                    <DialogFooter>
                        {canEdit && (
                            <Button variant="outline" onClick={() => setEditTripDialogOpen(true)} className="mr-auto">
                                <Pencil className="mr-2 h-4 w-4" />
                                {t("driverMonitor.detail.edit", "Edit Task")}
                            </Button>
                        )}
                        <Button variant="outline" onClick={() => setDetailTrip(null)}>
                            {t("driverMonitor.detail.close")}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>

            {detailTrip && (
                <EditTripDetailsDialog
                    open={editTripDialogOpen}
                    onOpenChange={setEditTripDialogOpen}
                    trip={detailTrip}
                    getSourceDisplayName={getSourceDisplayName}
                    onSuccess={() => setEditTripDialogOpen(false)}
                />
            )}
        </div>
    );
}
