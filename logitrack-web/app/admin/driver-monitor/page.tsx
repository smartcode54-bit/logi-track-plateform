"use client";

import { useState, useEffect, useMemo } from "react";
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
} from "lucide-react";
import { collection, query, orderBy, onSnapshot, limit } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import {
    TripRecord,
    TRIP_STATUS_ENUM,
    TRIP_JOB_TYPE_ENUM,
    type TripStatus,
    type TripJobType,
} from "@/validate/tripRecordSchema";
import { Driver } from "@/validate/driverSchema";
import { Task } from "@/validate/taskSchema";
import { useLanguage } from "@/context/language";

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
import { EditTripDetailsDialog } from "@/app/admin/driver-monitor/EditTripDetailsDialog";

// ─── Helpers ────────────────────────────────────────────────

function toDate(val: any): Date | null {
    if (!val) return null;
    if (val instanceof Date) return val;
    if (typeof val?.toDate === "function") return val.toDate();
    if (typeof val === "string") return new Date(val);
    return null;
}

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

// ─── Task Status Label (for stats) ──────────────────────────
const TASK_STATUS_LABEL: Record<string, string> = {
    "Pending": "Pending",
    "Assigned": "Assigned",
    "Checked in": "Checked in",
    "In-Transit": "In-Transit",
    "Completed": "Completed",
    "Cancelled": "Cancelled",
};

// ─── Component ──────────────────────────────────────────────

export default function DriverMonitorPage() {
    const { t } = useLanguage();

    // Data
    const [trips, setTrips] = useState<TripRecord[]>([]);
    const [drivers, setDrivers] = useState<Record<string, Driver>>({});
    const [tasks, setTasks] = useState<Task[]>([]);
    const [loading, setLoading] = useState(true);

    // Filters
    const [date, setDate] = useState<Date | undefined>(undefined);
    const [statusFilter, setStatusFilter] = useState("all");
    const [jobTypeFilter, setJobTypeFilter] = useState<string>("all");
    const [searchQuery, setSearchQuery] = useState("");

    // Detail Dialog
    const [detailTrip, setDetailTrip] = useState<TripRecord | null>(null);
    const [previewPhoto, setPreviewPhoto] = useState<{ url: string; type: string; address?: string } | null>(null);

    // Edit Trip Details Dialog (trip_record photos & metadata)
    const [editTripDialogOpen, setEditTripDialogOpen] = useState(false);

    // Pagination
    const [currentPage, setCurrentPage] = useState(1);
    const itemsPerPage = 15;

    // ─── Fetch trip_records ─────────────────────────────────
    useEffect(() => {
        setLoading(true);
        const q = query(
            collection(db, COLLECTIONS.TRIP_RECORDS),
            orderBy("createdAt", "desc"),
            limit(200)
        );
        const unsub = onSnapshot(
            q,
            (snap) => {
                const list: TripRecord[] = snap.docs.map((d) => {
                    const data = d.data();
                    return {
                        id: d.id,
                        ...data,
                        createdAt: toDate(data.createdAt),
                        updatedAt: toDate(data.updatedAt),
                        deliveredTimestamp: toDate(data.deliveredTimestamp),
                    } as TripRecord;
                });
                setTrips(list);
                setLoading(false);
            },
            (err) => {
                console.error("Error fetching trip_records:", err);
                setLoading(false);
            }
        );
        return () => unsub();
    }, []);

    // ─── Fetch drivers for name lookup ──────────────────────
    // trip_records.driverId is Firebase Auth UID; drivers are keyed by doc id and have authId
    useEffect(() => {
        const q = query(collection(db, COLLECTIONS.DRIVERS), limit(300));
        const unsub = onSnapshot(q, (snap) => {
            const map: Record<string, Driver> = {};
            snap.docs.forEach((d) => {
                const data = d.data();
                map[d.id] = { id: d.id, ...data } as Driver;
            });
            setDrivers(map);
        });
        return () => unsub();
    }, []);

    // Lookup driver by authId (trip.driverId) or by document id
    const driversByAuthId = useMemo(() => {
        const byAuth: Record<string, Driver> = {};
        Object.values(drivers).forEach((d) => {
            if (d.authId) byAuth[d.authId] = d;
        });
        return byAuth;
    }, [drivers]);

    const getDriver = (driverId?: string): Driver | null => {
        if (!driverId) return null;
        return driversByAuthId[driverId] ?? drivers[driverId] ?? null;
    };

    // ─── Fetch first_mile_tasks for check-in stats ──────────
    useEffect(() => {
        const q = query(
            collection(db, COLLECTIONS.TASKS),
            orderBy("createdAt", "desc"),
            limit(200)
        );
        const unsub = onSnapshot(q, (snap) => {
            const list = snap.docs.map((d) => ({
                id: d.id,
                ...d.data(),
            })) as Task[];
            setTasks(list);
        });
        return () => unsub();
    }, []);

    // ─── Stats ──────────────────────────────────────────────
    const stats = useMemo(() => {
        const total = trips.length;
        const inTransit = trips.filter((t) => t.status === "in_transit").length;
        const delivered = trips.filter((t) => t.status === "delivered").length;
        const loadingCount = trips.filter((t) => t.status === "loading").length;

        // Check in: "Checked in" from tasks (Admin monitors both types)
        const activeTasks = tasks.filter((t) => ["Assigned", "Checked in", "In-Transit", "Completed"].includes(t.status));
        const checkedInTasks = tasks.filter((t) => t.status === "Checked in");
        const checkInActual = checkedInTasks.length;
        const checkInTotal = activeTasks.length;

        return { total, inTransit, delivered, loading: loadingCount, checkInActual, checkInTotal };
    }, [trips, tasks]);

    // ─── Filtering ──────────────────────────────────────────
    const filteredTrips = useMemo(() => {
        return trips.filter((trip) => {
            // Date filter
            if (date) {
                const tripDate = toDate(trip.createdAt);
                if (tripDate) {
                    const filterStr = format(date, "dd/MM/yyyy");
                    const tripStr = format(tripDate, "dd/MM/yyyy");
                    if (filterStr !== tripStr) return false;
                } else {
                    return false;
                }
            }

            // Status
            if (statusFilter !== "all" && trip.status !== statusFilter) return false;

            // Job type
            if (jobTypeFilter !== "all" && trip.jobType !== jobTypeFilter) return false;

            // Search (driver name or trip id; trip.driverId is authId)
            if (searchQuery.trim()) {
                const q = searchQuery.toLowerCase();
                const driver = getDriver(trip.driverId);
                const driverName = driver
                    ? `${driver.firstName} ${driver.lastName}`.toLowerCase()
                    : "";
                const tripId = (trip.id || "").toLowerCase();
                const spxId = (trip.spxTripId || "").toLowerCase();
                if (
                    !driverName.includes(q) &&
                    !tripId.includes(q) &&
                    !spxId.includes(q)
                )
                    return false;
            }

            return true;
        });
    }, [trips, date, statusFilter, jobTypeFilter, searchQuery, drivers, driversByAuthId]);

    // ─── Pagination ─────────────────────────────────────────
    const paginatedTrips = filteredTrips.slice(
        (currentPage - 1) * itemsPerPage,
        currentPage * itemsPerPage
    );
    const totalPages = Math.ceil(filteredTrips.length / itemsPerPage);

    // Reset page on filter change
    useEffect(() => {
        setCurrentPage(1);
    }, [date, statusFilter, jobTypeFilter, searchQuery]);

    // ─── Helpers ────────────────────────────────────────────

    /** Driver Job Monitor table: "First - LicensePlate" (driverId = authId from trip_records) */
    const getDriverDisplayShort = (driverId?: string) => {
        if (!driverId) return t("driverMonitor.table.unknown");
        const driver = getDriver(driverId);
        if (!driver) return driverId.slice(0, 8) + "...";
        const plate = driver.currentAssignment?.truckPlate ?? "-";
        return `${driver.firstName} - ${plate}`;
    };

    /** Trip Details: "FirstName LastName - LicensePlate" */
    const getDriverDisplayFull = (driverId?: string) => {
        if (!driverId) return t("driverMonitor.table.unknown");
        const driver = getDriver(driverId);
        if (!driver) return driverId.slice(0, 8) + "...";
        const plate = driver.currentAssignment?.truckPlate ?? "-";
        return `${driver.firstName} ${driver.lastName} - ${plate}`;
    };

    const formatTimestamp = (val: any) => {
        const d = toDate(val);
        return d ? format(d, "dd/MM/yy HH:mm") : "-";
    };

    // ─── Render ─────────────────────────────────────────────

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
            </div>

            {/* Stats Cards — Order: Total, Loading|CheckIn (split), In Transit, Delivered */}
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
                {/* 1. Total Trips */}
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

                {/* 2. Loading | Check in (split half-half) */}
                <Card className="bg-card">
                    <CardContent className="p-0 flex h-full">
                        {/* Loading half */}
                        <div className="flex-1 p-4 flex items-center justify-between border-r border-border/30">
                            <div>
                                <p className="text-xs font-medium text-muted-foreground">
                                    {t("driverMonitor.stats.loading")}
                                </p>
                                <h2 className="text-2xl font-bold mt-1">{stats.loading}</h2>
                            </div>
                            <div className="h-9 w-9 rounded-full bg-slate-100 dark:bg-slate-800 flex items-center justify-center text-slate-500">
                                <Loader2 className="h-4 w-4" />
                            </div>
                        </div>
                        {/* Check in half */}
                        <div className="flex-1 p-4 flex items-center justify-between">
                            <div>
                                <p className="text-xs font-medium text-muted-foreground">
                                    {t("driverMonitor.stats.checkIn")}
                                </p>
                                <h2 className="text-2xl font-bold mt-1">
                                    <span className="text-emerald-500">{stats.checkInActual}</span>
                                    <span className="text-muted-foreground text-base font-normal"> / {stats.checkInTotal}</span>
                                </h2>
                            </div>
                            <div className="h-9 w-9 rounded-full bg-teal-100 dark:bg-teal-900/30 flex items-center justify-center text-teal-500">
                                <ClipboardCheck className="h-4 w-4" />
                            </div>
                        </div>
                    </CardContent>
                </Card>

                {/* 3. In Transit */}
                <Card className="bg-card">
                    <CardContent className="p-6 flex items-center justify-between">
                        <div>
                            <p className="text-sm font-medium text-muted-foreground">
                                {t("driverMonitor.stats.inTransit")}
                            </p>
                            <h2 className="text-3xl font-bold mt-1">{stats.inTransit}</h2>
                        </div>
                        <div className="h-12 w-12 rounded-full bg-amber-100 dark:bg-amber-900/30 flex items-center justify-center text-amber-500">
                            <Truck className="h-6 w-6" />
                        </div>
                    </CardContent>
                </Card>

                {/* 4. Delivered */}
                <Card className="bg-card">
                    <CardContent className="p-6 flex items-center justify-between">
                        <div>
                            <p className="text-sm font-medium text-muted-foreground">
                                {t("driverMonitor.stats.delivered")}
                            </p>
                            <h2 className="text-3xl font-bold mt-1">{stats.delivered}</h2>
                        </div>
                        <div className="h-12 w-12 rounded-full bg-emerald-100 dark:bg-emerald-900/30 flex items-center justify-center text-emerald-500">
                            <PackageCheck className="h-6 w-6" />
                        </div>
                    </CardContent>
                </Card>
            </div>

            {/* Job Type Tabs + Filters */}
            <div className="space-y-4">
                <div className="flex flex-col sm:flex-row gap-4 bg-card/50 p-4 rounded-lg border border-border/50">
                    {/* Job Type Tabs */}
                    <div className="flex gap-2 border-r border-border/50 pr-4">
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

                    {/* Date Picker */}
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
                                onSelect={setDate}
                                initialFocus
                            />
                        </PopoverContent>
                    </Popover>

                    {/* Status Filter */}
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

                    {/* Search */}
                    <div className="relative flex-1">
                        <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
                        <Input
                            placeholder={t("driverMonitor.filter.searchDriver")}
                            className="pl-10 bg-background/50 border-border/50 h-9"
                            value={searchQuery}
                            onChange={(e) => setSearchQuery(e.target.value)}
                        />
                    </div>

                    {/* Clear date */}
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

                {/* Data Table */}
                <div className="border rounded-lg bg-card overflow-hidden">
                    <Table>
                        <TableHeader className="bg-muted/50">
                            <TableRow className="border-b border-border/50">
                                <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">
                                    {t("driverMonitor.table.tripId")}
                                </TableHead>
                                <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">
                                    {t("driverMonitor.table.date")}
                                </TableHead>
                                <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">
                                    {t("driverMonitor.table.driver")}
                                </TableHead>
                                <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">
                                    {t("driverMonitor.table.jobType")}
                                </TableHead>
                                <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">
                                    {t("driverMonitor.table.route")}
                                </TableHead>
                                <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">
                                    {t("driverMonitor.table.sealCode")}
                                </TableHead>
                                <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">
                                    {t("driverMonitor.table.status")}
                                </TableHead>
                                <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">
                                    {t("driverMonitor.table.time")}
                                </TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {loading ? (
                                <TableRow>
                                    <TableCell colSpan={8} className="h-32 text-center">
                                        <div className="flex flex-col items-center justify-center gap-2">
                                            <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
                                            <p className="text-sm text-muted-foreground">
                                                {t("driverMonitor.table.loadingData")}
                                            </p>
                                        </div>
                                    </TableCell>
                                </TableRow>
                            ) : paginatedTrips.length === 0 ? (
                                <TableRow>
                                    <TableCell colSpan={8} className="h-32 text-center">
                                        <p className="text-sm text-muted-foreground">
                                            {t("driverMonitor.table.noTrips")}
                                        </p>
                                    </TableCell>
                                </TableRow>
                            ) : (
                                paginatedTrips.map((trip) => (
                                    <TableRow
                                        key={trip.id}
                                        className="cursor-pointer hover:bg-muted/50 transition-colors border-b border-border/50"
                                        onClick={() => setDetailTrip(trip)}
                                    >
                                        {/* Trip ID */}
                                        <TableCell>
                                            <span className="font-mono text-xs">
                                                {trip.spxTripId || trip.id?.slice(0, 10) || "-"}
                                            </span>
                                        </TableCell>

                                        {/* Date */}
                                        <TableCell className="text-sm text-muted-foreground">
                                            {formatTimestamp(trip.createdAt)}
                                        </TableCell>

                                        {/* Driver: First - LicensePlate */}
                                        <TableCell>
                                            <span className="font-medium text-sm">
                                                {getDriverDisplayShort(trip.driverId)}
                                            </span>
                                        </TableCell>

                                        {/* Job Type */}
                                        <TableCell>
                                            <Badge
                                                variant="secondary"
                                                className={cn(
                                                    "font-medium border",
                                                    JOB_TYPE_COLOR[trip.jobType] || "bg-gray-500/10 text-gray-500"
                                                )}
                                            >
                                                {JOB_TYPE_LABEL[trip.jobType] || trip.jobType}
                                            </Badge>
                                        </TableCell>

                                        {/* Route */}
                                        <TableCell>
                                            <div className="flex items-center gap-1.5 text-sm">
                                                <span className="font-medium">{trip.origin || "-"}</span>
                                                <ArrowRight className="h-3 w-3 text-muted-foreground shrink-0" />
                                                <span className="font-medium">{trip.destination || "-"}</span>
                                            </div>
                                        </TableCell>

                                        {/* Seal Code */}
                                        <TableCell>
                                            <span className="font-mono text-xs">
                                                {trip.sealCode || "-"}
                                            </span>
                                        </TableCell>

                                        {/* Status */}
                                        <TableCell>
                                            <Badge
                                                variant="secondary"
                                                className={cn(
                                                    "font-medium border",
                                                    STATUS_COLOR[trip.status] || "bg-gray-500/10 text-gray-500"
                                                )}
                                            >
                                                <span className="w-1.5 h-1.5 rounded-full bg-current mr-1.5" />
                                                {t(`driverMonitor.status.${trip.status}` as any)}
                                            </Badge>
                                        </TableCell>

                                        {/* Time */}
                                        <TableCell className="text-sm text-muted-foreground">
                                            {formatTimestamp(trip.updatedAt)}
                                        </TableCell>
                                    </TableRow>
                                ))
                            )}
                        </TableBody>
                    </Table>

                    {/* Pagination Footer */}
                    <div className="flex items-center justify-between px-4 py-4 border-t border-border/50 bg-muted/20">
                        <div className="text-sm text-muted-foreground">
                            {t("driverMonitor.pagination.showing")}{" "}
                            {paginatedTrips.length > 0 ? (currentPage - 1) * itemsPerPage + 1 : 0}{" "}
                            {t("driverMonitor.pagination.to")}{" "}
                            {Math.min(currentPage * itemsPerPage, filteredTrips.length)}{" "}
                            {t("driverMonitor.pagination.of")} {filteredTrips.length}{" "}
                            {t("driverMonitor.pagination.entries")}
                        </div>
                        <div className="flex items-center gap-2">
                            <Button
                                variant="outline"
                                size="sm"
                                onClick={() => setCurrentPage((p) => Math.max(1, p - 1))}
                                disabled={currentPage === 1}
                            >
                                {t("driverMonitor.pagination.previous")}
                            </Button>
                            <Button
                                variant="outline"
                                size="sm"
                                onClick={() => setCurrentPage((p) => Math.min(totalPages, p + 1))}
                                disabled={currentPage === totalPages || totalPages === 0}
                            >
                                {t("driverMonitor.pagination.next")}
                            </Button>
                        </div>
                    </div>
                </div>
            </div>

            {/* ─── Detail Dialog ────────────────────────────────── */}
            <Dialog open={!!detailTrip} onOpenChange={(open) => !open && setDetailTrip(null)}>
                <DialogContent className="max-w-2xl max-h-[90vh] overflow-y-auto">
                    <DialogHeader>
                        <DialogTitle className="flex items-center gap-2">
                            {t("driverMonitor.detail.title")}
                            {detailTrip && (
                                <Badge
                                    variant="secondary"
                                    className={cn(
                                        "font-medium border ml-2",
                                        JOB_TYPE_COLOR[detailTrip.jobType] || ""
                                    )}
                                >
                                    {JOB_TYPE_LABEL[detailTrip.jobType] || detailTrip.jobType}
                                </Badge>
                            )}
                        </DialogTitle>
                        <DialogDescription>
                            {detailTrip?.spxTripId || detailTrip?.id}
                        </DialogDescription>
                    </DialogHeader>

                    {detailTrip && (
                        <div className="grid gap-6 py-2">
                            {/* Trip Info */}
                            <div className="space-y-3">
                                <h4 className="text-sm font-semibold text-muted-foreground flex items-center gap-2">
                                    <Navigation className="h-4 w-4" />
                                    {t("driverMonitor.detail.tripInfo")}
                                </h4>
                                <div className="grid grid-cols-2 gap-x-6 gap-y-2 text-sm bg-muted/30 rounded-lg p-4">
                                    <span className="text-muted-foreground">{t("driverMonitor.table.status")}</span>
                                    <span>
                                        <Badge
                                            variant="secondary"
                                            className={cn(
                                                "font-medium border",
                                                STATUS_COLOR[detailTrip.status] || ""
                                            )}
                                        >
                                            <span className="w-1.5 h-1.5 rounded-full bg-current mr-1.5" />
                                            {t(`driverMonitor.status.${detailTrip.status}` as any)}
                                        </Badge>
                                    </span>
                                    <span className="text-muted-foreground">{t("driverMonitor.detail.spxTripId")}</span>
                                    <span className="font-mono text-xs">{detailTrip.spxTripId || "-"}</span>
                                    <span className="text-muted-foreground">{t("driverMonitor.detail.sealCode")}</span>
                                    <span className="font-mono text-xs">{detailTrip.sealCode || "-"}</span>
                                    {detailTrip.ocrData?.routeInfo && (
                                        <>
                                            <span className="text-muted-foreground">{t("driverMonitor.detail.ocrRoute")}</span>
                                            <span className="text-xs">{detailTrip.ocrData.routeInfo}</span>
                                        </>
                                    )}
                                    {detailTrip.distance && (
                                        <>
                                            <span className="text-muted-foreground">{t("driverMonitor.detail.distance")}</span>
                                            <span>{detailTrip.distance}</span>
                                        </>
                                    )}
                                    {detailTrip.parcelCount != null && (
                                        <>
                                            <span className="text-muted-foreground">{t("driverMonitor.detail.parcelCount")}</span>
                                            <span>{detailTrip.parcelCount}</span>
                                        </>
                                    )}
                                    {detailTrip.totalWeight && (
                                        <>
                                            <span className="text-muted-foreground">{t("driverMonitor.detail.weight")}</span>
                                            <span>{detailTrip.totalWeight}</span>
                                        </>
                                    )}
                                </div>
                            </div>

                            {/* Route Info */}
                            <div className="space-y-3">
                                <h4 className="text-sm font-semibold text-muted-foreground flex items-center gap-2">
                                    <MapPin className="h-4 w-4" />
                                    {t("driverMonitor.detail.routeInfo")}
                                </h4>
                                <div className="flex items-center gap-4 bg-muted/30 rounded-lg p-4">
                                    <div className="flex-1 text-center">
                                        <p className="text-xs text-muted-foreground mb-1">{t("driverMonitor.detail.origin")}</p>
                                        <p className="font-semibold">{detailTrip.origin || "-"}</p>
                                    </div>
                                    <ArrowRight className="h-5 w-5 text-muted-foreground shrink-0" />
                                    <div className="flex-1 text-center">
                                        <p className="text-xs text-muted-foreground mb-1">{t("driverMonitor.detail.destination")}</p>
                                        <p className="font-semibold">{detailTrip.destination || "-"}</p>
                                    </div>
                                </div>
                            </div>

                            {/* Driver Info */}
                            <div className="space-y-3">
                                <h4 className="text-sm font-semibold text-muted-foreground flex items-center gap-2">
                                    <Truck className="h-4 w-4" />
                                    {t("driverMonitor.detail.driverInfo")}
                                </h4>
                                <div className="grid grid-cols-2 gap-x-6 gap-y-2 text-sm bg-muted/30 rounded-lg p-4">
                                    <span className="text-muted-foreground">{t("driverMonitor.table.driver")}</span>
                                    <span className="font-medium">{getDriverDisplayFull(detailTrip.driverId)}</span>
                                    {detailTrip.driverId && getDriver(detailTrip.driverId)?.mobile && (
                                        <>
                                            <span className="text-muted-foreground">Mobile</span>
                                            <span>{getDriver(detailTrip.driverId)!.mobile}</span>
                                        </>
                                    )}
                                </div>
                            </div>

                            {/* Photos */}
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
                                                {/* eslint-disable-next-line @next/next/no-img-element */}
                                                <img
                                                    src={photo.url}
                                                    alt={photo.type}
                                                    className="object-cover w-full h-full"
                                                />
                                                <div className="absolute inset-x-0 bottom-0 bg-gradient-to-t from-black/70 to-transparent p-2">
                                                    <p className="text-white text-xs font-medium">{photo.type.replace(/_/g, " ")}</p>
                                                    {photo.geocoding?.address && (
                                                        <p className="text-white/70 text-[10px] truncate">{photo.geocoding.address}</p>
                                                    )}
                                                </div>
                                                <div className="absolute top-2 right-2 opacity-0 group-hover:opacity-100 transition-opacity">
                                                    <Camera className="h-4 w-4 text-white drop-shadow" />
                                                </div>
                                            </button>
                                        ))}
                                    </div>
                                </div>
                            )}

                            {/* Delivery Info */}
                            {detailTrip.status === "delivered" && (
                                <div className="space-y-3">
                                    <h4 className="text-sm font-semibold text-muted-foreground flex items-center gap-2">
                                        <PackageCheck className="h-4 w-4" />
                                        {t("driverMonitor.detail.delivery")}
                                    </h4>
                                    <div className="grid grid-cols-2 gap-x-6 gap-y-2 text-sm bg-emerald-500/5 rounded-lg p-4 border border-emerald-500/15">
                                        <span className="text-muted-foreground">{t("driverMonitor.detail.deliveredAt")}</span>
                                        <span>{formatTimestamp(detailTrip.deliveredTimestamp)}</span>
                                        {detailTrip.deliveredLat != null && detailTrip.deliveredLng != null && (
                                            <>
                                                <span className="text-muted-foreground">{t("driverMonitor.detail.deliveryLocation")}</span>
                                                <a
                                                    href={`https://www.google.com/maps?q=${detailTrip.deliveredLat},${detailTrip.deliveredLng}`}
                                                    target="_blank"
                                                    rel="noopener noreferrer"
                                                    className="text-primary underline text-xs"
                                                    onClick={(e) => e.stopPropagation()}
                                                >
                                                    {detailTrip.deliveredLat.toFixed(5)}, {detailTrip.deliveredLng.toFixed(5)}
                                                </a>
                                            </>
                                        )}
                                    </div>
                                </div>
                            )}

                            {/* Timestamps */}
                            <div className="grid grid-cols-2 gap-x-6 gap-y-2 text-sm border-t pt-4 text-muted-foreground">
                                <span className="flex items-center gap-1.5">
                                    <Clock className="h-3.5 w-3.5" />
                                    {t("driverMonitor.detail.createdAt")}
                                </span>
                                <span>{formatTimestamp(detailTrip.createdAt)}</span>
                                <span className="flex items-center gap-1.5">
                                    <Clock className="h-3.5 w-3.5" />
                                    {t("driverMonitor.detail.updatedAt")}
                                </span>
                                <span>{formatTimestamp(detailTrip.updatedAt)}</span>
                            </div>
                        </div>
                    )}
                    <DialogFooter>
                        <Button
                            variant="outline"
                            onClick={() => setEditTripDialogOpen(true)}
                            className="mr-auto"
                        >
                            <Pencil className="mr-2 h-4 w-4" />
                            {t("driverMonitor.detail.edit", "Edit Task")}
                        </Button>
                        <Button variant="outline" onClick={() => setDetailTrip(null)}>
                            {t("driverMonitor.detail.close")}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>

            {/* Edit Trip Details Dialog */}
            {detailTrip && (
                <EditTripDetailsDialog
                    open={editTripDialogOpen}
                    onOpenChange={setEditTripDialogOpen}
                    trip={detailTrip}
                    onSuccess={() => setEditTripDialogOpen(false)}
                />
            )}

            {/* Image preview dialog */}
            <Dialog open={!!previewPhoto} onOpenChange={(open) => !open && setPreviewPhoto(null)}>
                <DialogContent className="max-w-[95vw] max-h-[95vh] w-auto p-2">
                    <DialogTitle className="sr-only">{t("driverMonitor.detail.imagePreview")}</DialogTitle>
                    {previewPhoto && (
                        <div className="flex flex-col gap-2">
                            <div className="relative flex items-center justify-center bg-muted/30 rounded-lg overflow-hidden min-h-[200px]">
                                {/* eslint-disable-next-line @next/next/no-img-element */}
                                <img
                                    src={previewPhoto.url}
                                    alt={previewPhoto.type}
                                    className="max-w-full max-h-[85vh] w-auto h-auto object-contain"
                                />
                            </div>
                            <div className="flex items-center justify-between gap-4 px-1">
                                <div className="min-w-0">
                                    <p className="font-medium text-sm">{previewPhoto.type.replace(/_/g, " ")}</p>
                                    {previewPhoto.address && (
                                        <p className="text-muted-foreground text-xs truncate">{previewPhoto.address}</p>
                                    )}
                                </div>
                                <a
                                    href={previewPhoto.url}
                                    target="_blank"
                                    rel="noopener noreferrer"
                                    className="shrink-0 inline-flex items-center gap-1.5 text-xs text-primary hover:underline"
                                >
                                    <ExternalLink className="h-3.5 w-3.5" />
                                    {t("driverMonitor.detail.openInNewTab")}
                                </a>
                            </div>
                        </div>
                    )}
                </DialogContent>
            </Dialog>
        </div>
    );
}
