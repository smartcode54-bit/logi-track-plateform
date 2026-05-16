"use client";

import { useEffect, useMemo, useState } from "react";
import { format, startOfMonth, endOfMonth } from "date-fns";
import { enUS, th as thLocale } from "date-fns/locale";
import * as XLSX from "xlsx";
import {
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
    ClipboardCheck,
    Pencil,
    RefreshCw,
    Download,
    Calendar as CalendarIcon,
    Clipboard,
    Copy,
} from "lucide-react";
import { toast } from "sonner";

import { getDoc, doc } from "firebase/firestore";

import { useLanguage } from "@/context/language";
import { useAuth } from "@/context/auth";
import { canEditTripDetails } from "@/lib/permissions";
import { getMultiDeliveryProgress, getMultiDeliveryProgressLabel } from "../utils/multiDeliveryProgress";
import { SOC_KEYS } from "@/validate/taskSchema";
import { COLLECTIONS } from "@/lib/collections";
import { db } from "@/firebase/client";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Calendar } from "@/components/ui/calendar";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Collapsible, CollapsibleTrigger, CollapsibleContent } from "@/components/ui/collapsible";
import { ChevronDown } from "lucide-react";
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
import {
    ImageUrlPreviewView,
    type ImageUrlPreviewLabels,
} from "@/components/image-preview/ImageUrlPreviewView";
import { IMAGE_PREVIEW_VIEWPORT_ACCOUNTING_DIALOG_CLASS } from "@/components/image-preview/image-preview-constants";
import { ImageUrlPreviewDownloadBar } from "@/components/image-preview/ImageUrlPreviewDownloadBar";
import { looksLikeImageUrl } from "@/features/maintenance/utils/looksLikeImageUrl";
import { buildTripPhotosZipEntries } from "@/lib/download-image-urls-zip";

import { EditTripDetailsDialog } from "./EditTripDetailsDialog";
import {
    useDriverMonitor,
    type ExportFilterCriteria,
    defaultDateRange,
    clampDateRange,
    DRIVER_MONITOR_DEFAULT_RANGE_DAYS,
    DRIVER_MONITOR_MAX_RANGE_DAYS,
    DRIVER_MONITOR_PARTNER_NONE,
    effectivePartnerCode,
    isExportRangeCoveredByLoaded,
} from "../hooks/useDriverMonitor";
import { buildTripDeliveryShareTextAsync } from "@/lib/tripLineShare";
import { ScrollArea } from "@/components/ui/scroll-area";
import { TRIP_STATUS_ENUM } from "@/validate/tripRecordSchema";
import type { TripRecord } from "@/validate/tripRecordSchema";

const ROWS_PER_PAGE_OPTIONS = [10, 15, 25, 50, 100] as const;

function escapeCsvCell(val: string): string {
    if (/[",\r\n]/.test(val)) return `"${val.replace(/"/g, '""')}"`;
    return val;
}

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
    if (val == null) return null;
    if (val instanceof Date) return val;
    if (typeof val?.toDate === "function") return val.toDate();
    if (typeof val === "string") return new Date(val);
    if (typeof val === "number" && Number.isFinite(val)) return new Date(val);
    return null;
}

export default function DriverMonitorDashboard() {
    const { t, language } = useLanguage();
    const auth = useAuth();
    const canEdit = canEditTripDetails(auth?.customClaims ?? null);
    const dateLocale = language === "th" ? thLocale : enUS;

    const [exportOpen, setExportOpen] = useState(false);
    const [exportLoading, setExportLoading] = useState(false);
    const [shareLinePreviewOpen, setShareLinePreviewOpen] = useState(false);
    const [shareLinePreviewText, setShareLinePreviewText] = useState("");
    const [shareLinePreviewLoading, setShareLinePreviewLoading] = useState(false);
    const initExportFilters = (): ExportFilterCriteria => {
        const dr = defaultDateRange();
        return {
            dateFrom: dr.from,
            dateTo: dr.to,
            driverFilter: "all",
            statusFilter: "all",
            jobTypeFilter: "all",
            partnerFilter: "all",
            searchQuery: "",
        };
    };
    const [exportFilters, setExportFilters] = useState<ExportFilterCriteria>(initExportFilters);

    const {
        paginatedTrips,
        totalPages,
        currentPage,
        setCurrentPage,
        stats,
        filteredTrips,
        loading,
        dateFrom,
        dateTo,
        setDateRange,
        driverFilter,
        setDriverFilter,
        driverOptions,
        statusFilter,
        setStatusFilter,
        jobTypeFilter,
        setJobTypeFilter,
        partnerFilter,
        setPartnerFilter,
        partnerOptions,
        searchQuery,
        setSearchQuery,
        detailTrip,
        setDetailTrip,
        photoPreviewStartIndex,
        setPhotoPreviewStartIndex,
        editTripDialogOpen,
        setEditTripDialogOpen,
        incidentReportsByTripId,
        checkInAtByTaskId,
        getDriver,
        getSourceDisplayName,
        fetchHubs,
        hubs,
        itemsPerPage,
        setItemsPerPage,
        getTripsForExport,
        getTripsForExportResolved,
        trips,
        getBillingForTrip,
    } = useDriverMonitor();

    useEffect(() => {
        if (!detailTrip) {
            setShareLinePreviewOpen(false);
            setShareLinePreviewText("");
        }
    }, [detailTrip]);

    const [tripPreviewCurrentUrl, setTripPreviewCurrentUrl] = useState("");
    const [tripPreviewCaption, setTripPreviewCaption] = useState("");
    const [tripPreviewSlideIndex, setTripPreviewSlideIndex] = useState(0);

    const tripDetailPhotos = detailTrip?.photos ?? [];
    const tripPreviewUrls = useMemo(() => tripDetailPhotos.map((p) => p.url), [tripDetailPhotos]);

    const tripPhotoZipEntries = useMemo(
        () => (detailTrip?.id ? buildTripPhotosZipEntries(detailTrip.id, tripDetailPhotos) : []),
        [detailTrip?.id, tripDetailPhotos]
    );

    const tripPreviewZipFilename = useMemo(() => {
        if (!detailTrip?.id) return "trip-photos.zip";
        return `trip-photos-${detailTrip.id.replace(/[/\\]/g, "_")}.zip`;
    }, [detailTrip?.id]);

    const accountingImagePreviewLabels: ImageUrlPreviewLabels = useMemo(
        () => ({
            zoomIn: t("accounting.preview.zoomIn"),
            zoomOut: t("accounting.preview.zoomOut"),
            resetZoom: t("accounting.preview.resetZoom"),
            prev: t("accounting.preview.previous"),
            next: t("accounting.preview.next"),
            notPreviewable: t("accounting.preview.notPreviewable"),
        }),
        [t]
    );

    const tripPreviewDownloadStem = useMemo(() => {
        if (!detailTrip?.id || !tripPreviewCurrentUrl.trim()) return "";
        const i = tripDetailPhotos.findIndex((p) => p.url === tripPreviewCurrentUrl);
        if (i < 0) return "";
        const safeId = detailTrip.id.replace(/[/\\]/g, "_");
        const safeType = String(tripDetailPhotos[i].type).replace(/[/\\]/g, "_");
        return `${safeId}_${safeType}_${i}`;
    }, [detailTrip?.id, tripDetailPhotos, tripPreviewCurrentUrl]);

    const tripPhotoPreviewActive =
        photoPreviewStartIndex !== null &&
        !!detailTrip?.photos?.length &&
        photoPreviewStartIndex >= 0 &&
        photoPreviewStartIndex < (detailTrip.photos?.length ?? 0);

    const partnerFilterSelectCodes = useMemo(() => {
        const set = new Set(partnerOptions);
        if (
            partnerFilter !== "all" &&
            partnerFilter !== DRIVER_MONITOR_PARTNER_NONE &&
            !set.has(partnerFilter)
        ) {
            return [...partnerOptions, partnerFilter].sort((a, b) => a.localeCompare(b));
        }
        return partnerOptions;
    }, [partnerOptions, partnerFilter]);

    const exportPartnerSelectCodes = useMemo(() => {
        const set = new Set(partnerOptions);
        const v = exportFilters.partnerFilter;
        if (v !== "all" && v !== DRIVER_MONITOR_PARTNER_NONE && !set.has(v)) {
            return [...partnerOptions, v].sort((a, b) => a.localeCompare(b));
        }
        return partnerOptions;
    }, [partnerOptions, exportFilters.partnerFilter]);

    useEffect(() => {
        if (!detailTrip) {
            setPhotoPreviewStartIndex(null);
            setTripPreviewCurrentUrl("");
            setTripPreviewCaption("");
            setTripPreviewSlideIndex(0);
        }
    }, [detailTrip, setPhotoPreviewStartIndex]);

    useEffect(() => {
        if (photoPreviewStartIndex === null || !detailTrip?.photos?.length) return;
        const p = detailTrip.photos![photoPreviewStartIndex];
        if (p) {
            setTripPreviewCurrentUrl(p.url);
            setTripPreviewCaption(p.type.replace(/_/g, " "));
            setTripPreviewSlideIndex(photoPreviewStartIndex);
        }
        // Intentionally only when opening preview or changing start index; slide changes use onCurrentUrlChange.
        // eslint-disable-next-line react-hooks/exhaustive-deps -- detailTrip.photos identity updates would reset the viewer
    }, [photoPreviewStartIndex]);

    const openExportDialog = () => {
        setExportFilters({
            dateFrom,
            dateTo,
            driverFilter,
            statusFilter,
            jobTypeFilter,
            partnerFilter,
            searchQuery: "",
        });
        setExportOpen(true);
    };

    const exportRowCount = useMemo(
        () => (exportOpen ? getTripsForExport(exportFilters).length : 0),
        [exportOpen, exportFilters, getTripsForExport]
    );

    const exportNeedsFetch = useMemo(() => {
        if (!exportOpen || !exportFilters.dateFrom || !exportFilters.dateTo) return false;
        return !isExportRangeCoveredByLoaded(exportFilters.dateFrom, exportFilters.dateTo, dateFrom, dateTo);
    }, [exportOpen, exportFilters.dateFrom, exportFilters.dateTo, dateFrom, dateTo]);

    const exportFileSuffix = useMemo(() => {
        const f = exportFilters.dateFrom ? format(exportFilters.dateFrom, "yyyy-MM-dd") : "from";
        const to = exportFilters.dateTo ? format(exportFilters.dateTo, "yyyy-MM-dd") : "to";
        return `${f}_${to}`;
    }, [exportFilters.dateFrom, exportFilters.dateTo]);

    const showLargeDatasetHint = trips.length >= 2000;

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

    const openShareLinePreview = async () => {
        if (!detailTrip) return;
        setShareLinePreviewOpen(true);
        setShareLinePreviewLoading(true);
        setShareLinePreviewText("");
        try {
            const driver = getDriver(detailTrip.driverId ?? "") ?? null;
            const incident = detailTrip.id ? incidentReportsByTripId[detailTrip.id] : undefined;
            const text = await buildTripDeliveryShareTextAsync({
                trip: detailTrip,
                driver,
                originLabel: getSourceDisplayName(detailTrip.origin),
                destLabel: getSourceDisplayName(detailTrip.destination),
                partnerLine: effectivePartnerCode(detailTrip) || "-",
                incidentNote: incident?.description ?? null,
            });
            setShareLinePreviewText(text);
        } catch {
            toast.error(t("driverMonitor.detail.shareLineFailed"));
            setShareLinePreviewOpen(false);
        } finally {
            setShareLinePreviewLoading(false);
        }
    };

    const copyShareLineTextToClipboard = async () => {
        const text = shareLinePreviewText.trim();
        if (!text) return;
        try {
            await navigator.clipboard.writeText(text);
            toast.success(t("driverMonitor.detail.shareLineCopySuccess"));
            setShareLinePreviewOpen(false);
        } catch {
            toast.error(t("driverMonitor.detail.shareLineCopyFailed"));
        }
    };

    const formatTimestamp = (val: any) => {
        const d = toDateLocal(val);
        return d ? format(d, "dd/MM/yy HH:mm") : "-";
    };
    const formatMoney = (amount?: number | null) =>
        typeof amount === "number" ? `฿${amount.toLocaleString()}` : "-";

    const buildExportTable = (list: TripRecord[]) => {
        const headers = [
            t("driverMonitor.table.tripId"),
            t("driverMonitor.table.createdAt"),
            t("driverMonitor.table.driver"),
            t("driverMonitor.table.licensePlate"),
            t("driverMonitor.table.jobType"),
            t("driverMonitor.table.origin"),
            t("driverMonitor.table.destination"),
            t("driverMonitor.table.sealCode"),
            t("driverMonitor.table.partnerCode"),
            t("driverMonitor.table.status"),
            t("nav.income"),
            t("driverMonitor.table.multiDrop"),
            t("driverMonitor.table.stopNumber"),
            t("driverMonitor.table.stopStatus"),
            t("driverMonitor.table.stopDeliveredTime"),
        ];

        const formatStopStatus = (status: string) => {
            if (status === "delivered") return t("driverMonitor.status.delivered");
            if (status === "failed") return t("customStops.status.failed");
            return t("customStops.status.pending");
        };

        const rows: (string | number | null | undefined)[][] = [];

        for (const trip of list) {
            const created = toDateLocal((trip.id && checkInAtByTaskId[trip.id]) || trip.createdAt);
            const jobLabel = JOB_TYPE_LABEL[trip.jobType] || trip.jobType;
            const statusLabel = t(`driverMonitor.status.${trip.status}` as any);
            const billing = getBillingForTrip(trip.id);

            const baseRow: (string | number | null | undefined)[] = [
                trip.spxTripId || trip.id?.slice(0, 10) || "",
                created ? format(created, "dd/MM/yyyy HH:mm") : "",
                getDriverName(trip.driverId),
                getLicensePlate(trip.driverId),
                jobLabel,
                getSourceDisplayName(trip.origin),
                "", // destination — filled per stop below
                trip.sealCode || "",
                effectivePartnerCode(trip),
                statusLabel,
                null, // revenue — filled per stop below
            ];

            const stops = trip.deliveryStopsProgress ?? [];

            if (trip.isMultiDelivery && stops.length > 0) {
                const totalBilling = billing ? billing.finalRateThb : trip.billingEstimateThb;
                const lastStopIndex = Math.max(...stops.map((s) => s.index));
                stops.forEach((stop) => {
                    let stopDeliveredStr = "";
                    if (stop.index === lastStopIndex) {
                        const emptyContainerPhoto = stop.photos?.find((p) => p.type === "empty_container");
                        const ts = emptyContainerPhoto?.geocoding?.timestamp;
                        const d = ts ? toDateLocal(ts) : null;
                        stopDeliveredStr = d ? format(d, "dd/MM/yyyy HH:mm") : "";
                    } else {
                        const d = toDateLocal(stop.deliveredAt);
                        stopDeliveredStr = d ? format(d, "dd/MM/yyyy HH:mm") : "";
                    }
                    const row: (string | number | null | undefined)[] = [...baseRow];
                    row[6] = getSourceDisplayName(stop.destination);
                    row[10] = stop.index === 1 ? (totalBilling ?? null) : null;
                    row.push(
                        t("driverMonitor.table.multiDropYes"),
                        stop.index,
                        formatStopStatus(stop.status),
                        stopDeliveredStr,
                    );
                    rows.push(row);
                });
            } else {
                const tripDelivered = toDateLocal(
                    trip.status === "delivered" && trip.deliveredTimestamp ? trip.deliveredTimestamp : trip.updatedAt
                );
                const row: (string | number | null | undefined)[] = [...baseRow];
                row[6] = getSourceDisplayName(trip.destination);
                row[10] = billing ? billing.finalRateThb : trip.billingEstimateThb;
                row.push(
                    t("driverMonitor.table.multiDropNo"),
                    1,
                    "",
                    tripDelivered ? format(tripDelivered, "dd/MM/yyyy HH:mm") : "",
                );
                rows.push(row);
            }
        }

        return { headers, rows };
    };

    const downloadExcel = async (criteria: ExportFilterCriteria) => {
        setExportLoading(true);
        try {
            const list = await getTripsForExportResolved(criteria);
            const { headers, rows } = buildExportTable(list);
            const ws = XLSX.utils.aoa_to_sheet([headers, ...rows]);
            const wb = XLSX.utils.book_new();
            XLSX.utils.book_append_sheet(wb, ws, "Trips");
            XLSX.writeFile(wb, `driver_monitor_${exportFileSuffix}.xlsx`);
        } finally {
            setExportLoading(false);
        }
    };

    const downloadCsv = async (criteria: ExportFilterCriteria) => {
        setExportLoading(true);
        try {
            const list = await getTripsForExportResolved(criteria);
            const { headers, rows } = buildExportTable(list);
            const lines = [headers, ...rows].map((line) => line.map((c) => escapeCsvCell(String(c))).join(","));
            const blob = new Blob(["\uFEFF" + lines.join("\r\n")], { type: "text/csv;charset=utf-8" });
            const url = URL.createObjectURL(blob);
            const a = document.createElement("a");
            a.href = url;
            a.download = `driver_monitor_${exportFileSuffix}.csv`;
            a.click();
            URL.revokeObjectURL(url);
        } finally {
            setExportLoading(false);
        }
    };

    const getDriverDisplayFull = (driverId?: string) => {
        if (!driverId) return t("driverMonitor.table.unknown");
        const driver = getDriver(driverId);
        if (!driver) return driverId.slice(0, 8) + "...";
        const plate = driver.currentAssignment?.truckPlate ?? "-";
        return `${driver.firstName} ${driver.lastName} - ${plate}`;
    };

    // Destination options for delivery stops editor
    const destinationOptions = useMemo(() => {
        const socOpts: Array<{
            value: string;
            label: string;
            type: "soc" | "hub";
            linkedCustomerId?: string;
            linkedCustomerName?: string;
            linkedCustomerKind?: string;
        }> = SOC_KEYS.map((k) => ({
            value: k,
            label: getSourceDisplayName(k),
            type: "soc",
        }));
        const hubOpts: Array<{
            value: string;
            label: string;
            type: "soc" | "hub";
            linkedCustomerId?: string;
            linkedCustomerName?: string;
            linkedCustomerKind?: string;
        }> = hubs
            .filter((h) => h.source_id)
            .map((h) => ({
                value: h.source_id,
                label: getSourceDisplayName(h.source_id),
                type: "hub",
                linkedCustomerId: h.linkedCustomerId,
                linkedCustomerName: h.linkedCustomerName,
                linkedCustomerKind: h.customerLinkKind ?? "customer",
            }));
        return [...socOpts, ...hubOpts];
    }, [hubs, getSourceDisplayName]);

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
                <div className="flex items-center gap-2">
                    <Button variant="outline" size="sm" onClick={openExportDialog}>
                        <Download className="h-4 w-4 mr-2" />
                        {t("driverMonitor.export.open")}
                    </Button>
                    <Button variant="outline" size="icon" onClick={() => fetchHubs()} aria-label={t("driverMonitor.refresh")}>
                        <RefreshCw className="h-4 w-4" />
                    </Button>
                </div>
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

                    <div className="flex flex-col gap-2">
                        <div className="flex flex-wrap items-center gap-2">
                            <span className="text-xs text-muted-foreground whitespace-nowrap">{t("driverMonitor.filter.dateFrom")}</span>
                            <Popover>
                                <PopoverTrigger asChild>
                                    <Button variant="outline" className="h-9 w-[150px] justify-start font-normal" type="button">
                                        <CalendarIcon className="mr-2 h-4 w-4 shrink-0" />
                                        {format(dateFrom, "dd/MM/yyyy")}
                                    </Button>
                                </PopoverTrigger>
                                <PopoverContent className="w-auto p-0" align="start">
                                    <Calendar
                                        mode="single"
                                        selected={dateFrom}
                                        onSelect={(d) => {
                                            if (d) setDateRange(d, dateTo);
                                        }}
                                        locale={dateLocale}
                                        initialFocus
                                    />
                                </PopoverContent>
                            </Popover>
                            <span className="text-xs text-muted-foreground whitespace-nowrap">{t("driverMonitor.filter.dateTo")}</span>
                            <Popover>
                                <PopoverTrigger asChild>
                                    <Button variant="outline" className="h-9 w-[150px] justify-start font-normal" type="button">
                                        <CalendarIcon className="mr-2 h-4 w-4 shrink-0" />
                                        {format(dateTo, "dd/MM/yyyy")}
                                    </Button>
                                </PopoverTrigger>
                                <PopoverContent className="w-auto p-0" align="start">
                                    <Calendar
                                        mode="single"
                                        selected={dateTo}
                                        onSelect={(d) => {
                                            if (d) setDateRange(dateFrom, d);
                                        }}
                                        locale={dateLocale}
                                        initialFocus
                                    />
                                </PopoverContent>
                            </Popover>
                            <Button
                                type="button"
                                variant="secondary"
                                size="sm"
                                onClick={() => {
                                    const dr = defaultDateRange();
                                    setDateRange(dr.from, dr.to);
                                }}
                            >
                                {t("driverMonitor.filter.presetLastDays", { days: DRIVER_MONITOR_DEFAULT_RANGE_DAYS })}
                            </Button>
                            <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                onClick={() => {
                                    const now = new Date();
                                    setDateRange(startOfMonth(now), endOfMonth(now));
                                }}
                            >
                                {t("driverMonitor.filter.presetThisMonth")}
                            </Button>
                        </div>
                        <p className="text-[11px] text-muted-foreground max-w-xl">
                            {t("driverMonitor.filter.maxRangeHint", { days: DRIVER_MONITOR_MAX_RANGE_DAYS })}
                        </p>
                    </div>

                    <Select value={driverFilter} onValueChange={setDriverFilter}>
                        <SelectTrigger className="w-[200px] h-9">
                            <SelectValue placeholder={t("driverMonitor.filter.driver")} />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value="all">{t("driverMonitor.filter.allDrivers")}</SelectItem>
                            {driverOptions.map((o) => (
                                <SelectItem key={o.value} value={o.value}>
                                    {o.label}
                                </SelectItem>
                            ))}
                        </SelectContent>
                    </Select>

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

                    <Select value={partnerFilter} onValueChange={setPartnerFilter}>
                        <SelectTrigger className="w-[200px] h-9">
                            <SelectValue placeholder={t("driverMonitor.filter.partnerChannel")} />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value="all">{t("driverMonitor.filter.allPartnerChannels")}</SelectItem>
                            <SelectItem value={DRIVER_MONITOR_PARTNER_NONE}>
                                {t("driverMonitor.filter.partnerNotSpecified")}
                            </SelectItem>
                            {partnerFilterSelectCodes.map((code) => (
                                <SelectItem key={code} value={code}>
                                    {code}
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

                </div>

                {showLargeDatasetHint && (
                    <p className="text-sm text-amber-700 dark:text-amber-400/90 bg-amber-50 dark:bg-amber-950/30 border border-amber-200/80 dark:border-amber-900/50 rounded-md px-3 py-2">
                        {t("driverMonitor.filter.largeDatasetHint")}
                    </p>
                )}

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
                                    <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">{t("driverMonitor.table.partnerCode")}</TableHead>
                                    <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">{t("driverMonitor.table.status")}</TableHead>
                                    <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">{t("driverMonitor.table.deliveredTime")}</TableHead>
                                    <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider text-right">{t("driverMonitor.table.estimatedRevenue")}</TableHead>
                                </TableRow>
                            </TableHeader>
                            <TableBody>
                                {loading ? (
                                    <TableRow>
                                        <TableCell colSpan={12} className="h-32 text-center">
                                            <div className="flex flex-col items-center justify-center gap-2">
                                                <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
                                                <p className="text-sm text-muted-foreground">{t("driverMonitor.table.loadingData")}</p>
                                            </div>
                                        </TableCell>
                                    </TableRow>
                                ) : paginatedTrips.length === 0 ? (
                                    <TableRow>
                                        <TableCell colSpan={12} className="h-32 text-center">
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
                                            <TableCell className="text-sm">
                                                {trip.isMultiDelivery && (trip.deliveryStopsProgress?.length ?? 0) > 0 ? (
                                                    <Collapsible>
                                                        <CollapsibleTrigger
                                                            onClick={(e) => e.stopPropagation()}
                                                            className="inline-flex items-center gap-1 text-xs px-1.5 py-0.5 rounded-full bg-blue-100 text-blue-700 hover:bg-blue-200 border border-blue-200 font-medium cursor-pointer transition-colors"
                                                        >
                                                            {trip.deliveryStopsProgress?.length ?? trip.totalDeliveryStops} stops
                                                            <ChevronDown className="h-3 w-3" />
                                                        </CollapsibleTrigger>
                                                        <CollapsibleContent className="mt-2 space-y-1 w-max">
                                                            {trip.deliveryStopsProgress?.map((stop) => (
                                                                <Badge
                                                                    key={stop.index}
                                                                    variant="outline"
                                                                    className={cn(
                                                                        "text-xs font-medium block",
                                                                        stop.status === "delivered"
                                                                            ? "bg-green-50 text-green-700 border-green-200"
                                                                            : "bg-gray-50 text-gray-600 border-gray-200"
                                                                    )}
                                                                >
                                                                    {stop.status === "delivered" ? "✓" : "◯"} {getSourceDisplayName(stop.destination)}
                                                                </Badge>
                                                            ))}
                                                        </CollapsibleContent>
                                                    </Collapsible>
                                                ) : (
                                                    <span className="font-medium">{getSourceDisplayName(trip.destination)}</span>
                                                )}
                                            </TableCell>
                                            <TableCell><span className="font-mono text-xs">{trip.sealCode || "-"}</span></TableCell>
                                            <TableCell>
                                                <span className="font-mono text-xs">
                                                    {effectivePartnerCode(trip) || "-"}
                                                </span>
                                            </TableCell>
                                            <TableCell>
                                                <div className="flex items-center gap-2 flex-wrap">
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
                                            <TableCell className="text-sm text-right font-medium">
                                                {formatMoney(getBillingForTrip(trip.id)?.finalRateThb ?? trip.billingEstimateThb)}
                                            </TableCell>
                                        </TableRow>
                                    ))
                                )}
                            </TableBody>
                        </Table>
                    </div>

                    {/* Pagination */}
                    <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 px-4 py-4 border-t border-border/50 bg-muted/20">
                        <div className="text-sm text-muted-foreground">
                            {t("driverMonitor.pagination.showing")} {paginatedTrips.length > 0 ? (currentPage - 1) * itemsPerPage + 1 : 0} {t("driverMonitor.pagination.to")} {Math.min(currentPage * itemsPerPage, filteredTrips.length)} {t("driverMonitor.pagination.of")} {filteredTrips.length} {t("driverMonitor.pagination.entries")}
                        </div>
                        <div className="flex flex-wrap items-center gap-2 justify-end">
                            <div className="flex items-center gap-2">
                                <span className="text-sm text-muted-foreground whitespace-nowrap">{t("driverMonitor.pagination.rowsPerPage")}</span>
                                <Select
                                    value={String(itemsPerPage)}
                                    onValueChange={(v) => setItemsPerPage(Number(v))}
                                >
                                    <SelectTrigger className="h-9 w-[72px]">
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent>
                                        {ROWS_PER_PAGE_OPTIONS.map((n) => (
                                            <SelectItem key={n} value={String(n)}>
                                                {n}
                                            </SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                            </div>
                            <Button variant="outline" size="sm" onClick={() => setCurrentPage((p) => Math.max(1, p - 1))} disabled={currentPage === 1}>{t("driverMonitor.pagination.previous")}</Button>
                            <Button variant="outline" size="sm" onClick={() => setCurrentPage((p) => Math.min(totalPages, p + 1))} disabled={currentPage === totalPages || totalPages === 0}>{t("driverMonitor.pagination.next")}</Button>
                        </div>
                    </div>
                </div>
            </div>

            {/* Export dialog — full filter set independent from table */}
            <Dialog open={exportOpen} onOpenChange={setExportOpen}>
                <DialogContent className="sm:max-w-xl max-h-[90vh] flex flex-col">
                    <DialogHeader>
                        <DialogTitle>{t("driverMonitor.export.title")}</DialogTitle>
                        <DialogDescription>{t("driverMonitor.export.description")}</DialogDescription>
                    </DialogHeader>
                    <div className="space-y-4 overflow-y-auto py-1 pr-1 max-h-[min(60vh,560px)]">
                        <div className="flex flex-wrap gap-2">
                            <Button
                                type="button"
                                variant={exportFilters.jobTypeFilter === "all" ? "secondary" : "ghost"}
                                onClick={() => setExportFilters((f) => ({ ...f, jobTypeFilter: "all" }))}
                                className="whitespace-nowrap"
                                size="sm"
                            >
                                {t("driverMonitor.jobType.all")}
                            </Button>
                            <Button
                                type="button"
                                variant={exportFilters.jobTypeFilter === "first_mile" ? "secondary" : "ghost"}
                                onClick={() => setExportFilters((f) => ({ ...f, jobTypeFilter: "first_mile" }))}
                                className="whitespace-nowrap"
                                size="sm"
                            >
                                <span className="w-2 h-2 rounded-full bg-indigo-500 mr-1.5" />
                                {t("driverMonitor.jobType.firstMile")}
                            </Button>
                            <Button
                                type="button"
                                variant={exportFilters.jobTypeFilter === "line_haul" ? "secondary" : "ghost"}
                                onClick={() => setExportFilters((f) => ({ ...f, jobTypeFilter: "line_haul" }))}
                                className="whitespace-nowrap"
                                size="sm"
                            >
                                <span className="w-2 h-2 rounded-full bg-orange-500 mr-1.5" />
                                {t("driverMonitor.jobType.lineHaul")}
                            </Button>
                        </div>

                        <div className="space-y-1.5">
                            <span className="text-xs font-medium text-muted-foreground">{t("driverMonitor.export.dateRange")}</span>
                            <div className="flex flex-wrap items-center gap-2">
                                <span className="text-xs text-muted-foreground">{t("driverMonitor.filter.dateFrom")}</span>
                                <Popover>
                                    <PopoverTrigger asChild>
                                        <Button variant="outline" className="h-9 w-[150px] justify-start font-normal" type="button">
                                            <CalendarIcon className="mr-2 h-4 w-4 shrink-0" />
                                            {exportFilters.dateFrom ? format(exportFilters.dateFrom, "dd/MM/yyyy") : "—"}
                                        </Button>
                                    </PopoverTrigger>
                                    <PopoverContent className="w-auto p-0" align="start">
                                        <Calendar
                                            mode="single"
                                            selected={exportFilters.dateFrom ?? undefined}
                                            onSelect={(d) => {
                                                if (!d) return;
                                                setExportFilters((f) => {
                                                    const to = f.dateTo ?? d;
                                                    const c = clampDateRange(d, to);
                                                    return { ...f, dateFrom: c.from, dateTo: c.to };
                                                });
                                            }}
                                            locale={dateLocale}
                                            initialFocus
                                        />
                                    </PopoverContent>
                                </Popover>
                                <span className="text-xs text-muted-foreground">{t("driverMonitor.filter.dateTo")}</span>
                                <Popover>
                                    <PopoverTrigger asChild>
                                        <Button variant="outline" className="h-9 w-[150px] justify-start font-normal" type="button">
                                            <CalendarIcon className="mr-2 h-4 w-4 shrink-0" />
                                            {exportFilters.dateTo ? format(exportFilters.dateTo, "dd/MM/yyyy") : "—"}
                                        </Button>
                                    </PopoverTrigger>
                                    <PopoverContent className="w-auto p-0" align="start">
                                        <Calendar
                                            mode="single"
                                            selected={exportFilters.dateTo ?? undefined}
                                            onSelect={(d) => {
                                                if (!d) return;
                                                setExportFilters((f) => {
                                                    const from = f.dateFrom ?? d;
                                                    const c = clampDateRange(from, d);
                                                    return { ...f, dateFrom: c.from, dateTo: c.to };
                                                });
                                            }}
                                            locale={dateLocale}
                                            initialFocus
                                        />
                                    </PopoverContent>
                                </Popover>
                                <Button
                                    type="button"
                                    variant="secondary"
                                    size="sm"
                                    onClick={() => {
                                        const dr = defaultDateRange();
                                        setExportFilters((f) => ({ ...f, dateFrom: dr.from, dateTo: dr.to }));
                                    }}
                                >
                                    {t("driverMonitor.filter.presetLastDays", { days: DRIVER_MONITOR_DEFAULT_RANGE_DAYS })}
                                </Button>
                                <Button
                                    type="button"
                                    variant="outline"
                                    size="sm"
                                    onClick={() => {
                                        const now = new Date();
                                        const c = clampDateRange(startOfMonth(now), endOfMonth(now));
                                        setExportFilters((f) => ({ ...f, dateFrom: c.from, dateTo: c.to }));
                                    }}
                                >
                                    {t("driverMonitor.filter.presetThisMonth")}
                                </Button>
                            </div>
                            <p className="text-[11px] text-muted-foreground">
                                {t("driverMonitor.filter.maxRangeHint", { days: DRIVER_MONITOR_MAX_RANGE_DAYS })}
                            </p>
                        </div>

                        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
                            <div className="space-y-1.5">
                                <span className="text-xs font-medium text-muted-foreground">{t("driverMonitor.filter.driver")}</span>
                                <Select
                                    value={exportFilters.driverFilter}
                                    onValueChange={(v) => setExportFilters((f) => ({ ...f, driverFilter: v }))}
                                >
                                    <SelectTrigger className="h-9 w-full">
                                        <SelectValue placeholder={t("driverMonitor.filter.driver")} />
                                    </SelectTrigger>
                                    <SelectContent>
                                        <SelectItem value="all">{t("driverMonitor.filter.allDrivers")}</SelectItem>
                                        {driverOptions.map((o) => (
                                            <SelectItem key={o.value} value={o.value}>
                                                {o.label}
                                            </SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                            </div>
                            <div className="space-y-1.5">
                                <span className="text-xs font-medium text-muted-foreground">{t("driverMonitor.filter.status")}</span>
                                <Select
                                    value={exportFilters.statusFilter}
                                    onValueChange={(v) => setExportFilters((f) => ({ ...f, statusFilter: v }))}
                                >
                                    <SelectTrigger className="h-9 w-full">
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
                            </div>
                            <div className="space-y-1.5 sm:col-span-2 lg:col-span-1">
                                <span className="text-xs font-medium text-muted-foreground">
                                    {t("driverMonitor.filter.partnerChannel")}
                                </span>
                                <Select
                                    value={exportFilters.partnerFilter}
                                    onValueChange={(v) => setExportFilters((f) => ({ ...f, partnerFilter: v }))}
                                >
                                    <SelectTrigger className="h-9 w-full">
                                        <SelectValue placeholder={t("driverMonitor.filter.partnerChannel")} />
                                    </SelectTrigger>
                                    <SelectContent>
                                        <SelectItem value="all">{t("driverMonitor.filter.allPartnerChannels")}</SelectItem>
                                        <SelectItem value={DRIVER_MONITOR_PARTNER_NONE}>
                                            {t("driverMonitor.filter.partnerNotSpecified")}
                                        </SelectItem>
                                        {exportPartnerSelectCodes.map((code) => (
                                            <SelectItem key={code} value={code}>
                                                {code}
                                            </SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                            </div>
                        </div>

                        <div className="space-y-1.5">
                            <span className="text-xs font-medium text-muted-foreground">{t("driverMonitor.filter.searchDriver")}</span>
                            <div className="relative">
                                <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
                                <Input
                                    className="pl-10 h-9"
                                    value={exportFilters.searchQuery}
                                    onChange={(e) => setExportFilters((f) => ({ ...f, searchQuery: e.target.value }))}
                                />
                            </div>
                        </div>

                        <div className="space-y-1 text-sm text-muted-foreground">
                            <p>{t("driverMonitor.export.rowCount", { count: exportRowCount })}</p>
                            {exportNeedsFetch && (
                                <p className="text-amber-700 dark:text-amber-400/90">
                                    {t("driverMonitor.export.fetchNote")}
                                </p>
                            )}
                        </div>
                    </div>
                    <DialogFooter className="gap-2 sm:gap-0 flex-col sm:flex-row sm:justify-between border-t pt-4">
                        <Button variant="outline" onClick={() => setExportOpen(false)} disabled={exportLoading}>
                            {t("driverMonitor.export.cancel")}
                        </Button>
                        <div className="flex flex-wrap gap-2 w-full sm:w-auto justify-end">
                            <Button
                                variant="secondary"
                                className="inline-flex items-center gap-2"
                                disabled={exportLoading}
                                onClick={async () => {
                                    await downloadCsv(exportFilters);
                                    setExportOpen(false);
                                }}
                            >
                                {exportLoading ? <Loader2 className="h-4 w-4 animate-spin shrink-0" /> : null}
                                {t("driverMonitor.export.csv")}
                            </Button>
                            <Button
                                className="inline-flex items-center gap-2"
                                disabled={exportLoading}
                                onClick={async () => {
                                    await downloadExcel(exportFilters);
                                    setExportOpen(false);
                                }}
                            >
                                {exportLoading ? <Loader2 className="h-4 w-4 animate-spin shrink-0" /> : null}
                                {t("driverMonitor.export.excel")}
                            </Button>
                        </div>
                    </DialogFooter>
                </DialogContent>
            </Dialog>

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
                                <div className="flex flex-wrap items-center justify-between gap-2">
                                    <h4 className="text-sm font-semibold text-muted-foreground flex items-center gap-2">
                                        <Navigation className="h-4 w-4" />
                                        {t("driverMonitor.detail.tripInfo")}
                                    </h4>
                                    {canEdit && (
                                        <Button
                                            type="button"
                                            variant="secondary"
                                            size="sm"
                                            className="shrink-0 gap-1.5"
                                            onClick={() => setEditTripDialogOpen(true)}
                                        >
                                            <Pencil className="h-3.5 w-3.5" />
                                            {t("driverMonitor.detail.editPartner")}
                                        </Button>
                                    )}
                                </div>
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
                                    <span className="text-muted-foreground">{t("driverMonitor.detail.partnerCode")}</span>
                                    <span className="font-mono text-xs">
                                        {effectivePartnerCode(detailTrip) || "-"}
                                    </span>
                                    <span className="text-muted-foreground">{t("driverMonitor.table.estimatedRevenue")}</span>
                                    <span className="font-semibold">
                                        {formatMoney(
                                            getBillingForTrip(detailTrip.id)?.finalRateThb ?? detailTrip.billingEstimateThb
                                        )}
                                    </span>
                                </div>
                            </div>

                            <div className="space-y-3">
                                <h4 className="text-sm font-semibold text-muted-foreground flex items-center gap-2">
                                    <MapPin className="h-4 w-4" />
                                    {t("driverMonitor.detail.routeInfo")}
                                </h4>
                                {detailTrip.isMultiDelivery && (detailTrip.deliveryStopsProgress?.length ?? 0) > 0 ? (
                                    <div className="bg-muted/30 rounded-lg p-4 space-y-3">
                                        <div className="text-center">
                                            <p className="text-xs text-muted-foreground mb-1">{t("driverMonitor.detail.origin")}</p>
                                            <p className="font-semibold">{getSourceDisplayName(detailTrip.origin)}</p>
                                        </div>
                                        <div className="border-t border-border/50 pt-3">
                                            <p className="text-xs text-muted-foreground mb-2">{t("driverMonitor.detail.deliveryStops")}</p>
                                            <div className="space-y-2">
                                                {(() => {
                                                    const allStops = detailTrip.deliveryStopsProgress ?? [];
                                                    const lastIdx = allStops.length > 0 ? Math.max(...allStops.map((s) => s.index)) : -1;
                                                    return allStops.map((stop) => {
                                                        let deliveredTimeStr = "";
                                                        if (stop.status === "delivered") {
                                                            if (stop.index === lastIdx) {
                                                                const emptyPhoto = stop.photos?.find((p) => p.type === "empty_container");
                                                                const ts = emptyPhoto?.geocoding?.timestamp;
                                                                const d = ts ? toDateLocal(ts) : null;
                                                                deliveredTimeStr = d ? format(d, "dd/MM/yy HH:mm") : "";
                                                            } else {
                                                                const d = toDateLocal(stop.deliveredAt);
                                                                deliveredTimeStr = d ? format(d, "dd/MM/yy HH:mm") : "";
                                                            }
                                                        }
                                                        return (
                                                            <div key={stop.index} className="flex items-center gap-2 p-2 rounded border border-border/50">
                                                                <span className={cn(
                                                                    "flex-shrink-0 w-5 h-5 rounded-full flex items-center justify-center text-xs font-bold",
                                                                    stop.status === "delivered" ? "bg-emerald-100 text-emerald-700" : "bg-gray-100 text-gray-500"
                                                                )}>
                                                                    {stop.index}
                                                                </span>
                                                                <span className="flex-1">{getSourceDisplayName(stop.destination)}</span>
                                                                {deliveredTimeStr && (
                                                                    <span className="text-emerald-600 text-xs">{deliveredTimeStr}</span>
                                                                )}
                                                                <span className={stop.status === "delivered" ? "text-emerald-600 text-xs" : "text-gray-400 text-xs"}>
                                                                    {stop.status === "delivered" ? "✓" : "◯"}
                                                                </span>
                                                            </div>
                                                        );
                                                    });
                                                })()}
                                            </div>
                                        </div>
                                        {detailTrip.billingIsMultiDelivery && detailTrip.billingMultiDeliveryBreakdown && detailTrip.billingMultiDeliveryBreakdown.length > 0 && (
                                            <div className="border-t border-border/50 pt-3 mt-3">
                                                <p className="text-xs text-muted-foreground mb-2">{t("driverMonitor.detail.billingPerStop") || "Billing per stop"}</p>
                                                <div className="space-y-1">
                                                    {detailTrip.billingMultiDeliveryBreakdown.map((stop) => (
                                                        <div key={stop.stopIndex} className="flex justify-between text-sm">
                                                            <span>Stop {stop.stopIndex}: {getSourceDisplayName(stop.destination)}</span>
                                                            <span className="font-medium">฿{stop.finalRateThb.toLocaleString()}</span>
                                                        </div>
                                                    ))}
                                                    <div className="flex justify-between text-sm font-semibold border-t pt-1">
                                                        <span>{t("driverMonitor.detail.total") || "Total"}</span>
                                                        <span>฿{(detailTrip.billingEstimateThb ?? 0).toLocaleString()}</span>
                                                    </div>
                                                </div>
                                            </div>
                                        )}
                                    </div>
                                ) : (
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
                                )}
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

                            {(detailTrip.photos && detailTrip.photos.length > 0) || (detailTrip.isMultiDelivery && detailTrip.deliveryStopsProgress?.some((stop) => stop.photos && stop.photos.length > 0)) ? (
                                <div className="space-y-3">
                                    <h4 className="text-sm font-semibold text-muted-foreground flex items-center gap-2">
                                        <Camera className="h-4 w-4" />
                                        {t("driverMonitor.detail.photos")}
                                    </h4>
                                    <div className="space-y-4">
                                        {/* Loading Phase Photos */}
                                        {detailTrip.photos && detailTrip.photos.length > 0 && (
                                            <div className="border border-border/50 rounded-lg p-4">
                                                <p className="text-sm font-semibold mb-3">Loading Phase</p>
                                                <div className="grid grid-cols-2 sm:grid-cols-3 gap-3">
                                                    {detailTrip.photos.map((photo, idx) => (
                                                        <button
                                                            key={`loading-${idx}`}
                                                            type="button"
                                                            onClick={() => setPhotoPreviewStartIndex(idx)}
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

                                        {/* Delivery Stop Photos (Multi-delivery only) */}
                                        {detailTrip.isMultiDelivery && (detailTrip.deliveryStopsProgress?.length ?? 0) > 0 && (
                                            <>
                                                {detailTrip.deliveryStopsProgress?.map((stop) => (
                                                    stop.photos && stop.photos.length > 0 && (
                                                        <div key={stop.index} className="border border-border/50 rounded-lg p-4">
                                                            <p className="text-sm font-semibold mb-3">
                                                                Stop {stop.index}: {getSourceDisplayName(stop.destination)}
                                                            </p>
                                                            <div className="grid grid-cols-2 sm:grid-cols-3 gap-3">
                                                                {stop.photos.map((photo, idx) => (
                                                                    <button
                                                                        key={`${stop.index}-${idx}`}
                                                                        type="button"
                                                                        onClick={() => setPhotoPreviewStartIndex(idx)}
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
                                                    )
                                                ))}
                                            </>
                                        )}
                                    </div>
                                </div>
                            ) : null}
                        </div>
                    )}
                    <DialogFooter className="gap-2 flex-col sm:flex-row sm:justify-end sm:gap-0">
                        <Button
                            type="button"
                            variant="secondary"
                            className="w-full sm:w-auto gap-2"
                            disabled={shareLinePreviewLoading || !detailTrip}
                            onClick={() => void openShareLinePreview()}
                        >
                            {shareLinePreviewLoading ? (
                                <Loader2 className="h-4 w-4 animate-spin shrink-0" />
                            ) : (
                                <Clipboard className="h-4 w-4 shrink-0" />
                            )}
                            {t("driverMonitor.detail.shareLine")}
                        </Button>
                        <Button variant="outline" onClick={() => setDetailTrip(null)} className="w-full sm:w-auto">
                            {t("driverMonitor.detail.close")}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>

            {/* ตัวอย่างข้อความก่อนแชร์ LINE */}
            <Dialog
                open={shareLinePreviewOpen}
                onOpenChange={(open) => {
                    if (!open) {
                        setShareLinePreviewOpen(false);
                        setShareLinePreviewText("");
                    }
                }}
            >
                <DialogContent className="max-w-lg max-h-[90vh] flex flex-col gap-4">
                    <DialogHeader>
                        <DialogTitle>{t("driverMonitor.detail.shareLinePreviewTitle")}</DialogTitle>
                        <DialogDescription>{t("driverMonitor.detail.shareLinePreviewDescription")}</DialogDescription>
                    </DialogHeader>
                    {shareLinePreviewLoading ? (
                        <div className="flex justify-center py-12">
                            <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
                        </div>
                    ) : (
                        <>
                            {detailTrip != null && (detailTrip.photos?.length ?? 0) > 0 ? (
                                <p className="text-sm text-muted-foreground">
                                    {t("driverMonitor.detail.shareLinePreviewPhotoHint")}
                                </p>
                            ) : null}
                            <ScrollArea className="h-[min(50vh,360px)] rounded-md border border-border">
                                <pre className="whitespace-pre-wrap break-words p-3 text-sm font-sans leading-relaxed">
                                    {shareLinePreviewText}
                                </pre>
                            </ScrollArea>
                        </>
                    )}
                    <DialogFooter className="gap-2 flex-col sm:flex-row sm:justify-end sm:gap-0">
                        <Button
                            type="button"
                            variant="outline"
                            onClick={() => setShareLinePreviewOpen(false)}
                            className="w-full sm:w-auto"
                        >
                            {t("driverMonitor.detail.shareLinePreviewCancel")}
                        </Button>
                        <Button
                            type="button"
                            className="w-full sm:w-auto gap-2"
                            onClick={() => void copyShareLineTextToClipboard()}
                            disabled={shareLinePreviewLoading || !shareLinePreviewText.trim()}
                        >
                            <Copy className="h-4 w-4 shrink-0" />
                            {t("driverMonitor.detail.shareLinePreviewCopy")}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>

            <Dialog
                open={tripPhotoPreviewActive}
                onOpenChange={(open) => {
                    if (!open) setPhotoPreviewStartIndex(null);
                }}
            >
                <DialogContent className="max-w-4xl max-h-[90vh] overflow-hidden flex flex-col p-0">
                    <DialogHeader className="px-6 pt-6 pb-2 shrink-0">
                        <DialogTitle>{t("driverMonitor.detail.imagePreview")}</DialogTitle>
                        <DialogDescription>
                            {detailTrip?.spxTripId || detailTrip?.id || ""}
                        </DialogDescription>
                    </DialogHeader>
                    {tripPhotoPreviewActive && detailTrip && tripPreviewUrls.length > 0 ? (
                        <div className="flex flex-1 min-h-0 flex-col px-4 pb-4">
                            <div className="flex flex-1 min-h-0 flex-col overflow-hidden rounded-lg border border-border bg-black/5">
                                <ImageUrlPreviewView
                                    urls={tripPreviewUrls}
                                    startIndex={photoPreviewStartIndex ?? 0}
                                    active={tripPhotoPreviewActive}
                                    labels={accountingImagePreviewLabels}
                                    isPreviewableImage={looksLikeImageUrl}
                                    onCurrentUrlChange={(url) => {
                                        setTripPreviewCurrentUrl(url);
                                        const i = tripPreviewUrls.indexOf(url);
                                        if (i >= 0) {
                                            setTripPreviewSlideIndex(i);
                                            const ph = detailTrip.photos?.[i];
                                            setTripPreviewCaption(ph ? ph.type.replace(/_/g, " ") : "");
                                        }
                                    }}
                                    viewportClassName={IMAGE_PREVIEW_VIEWPORT_ACCOUNTING_DIALOG_CLASS}
                                    toolbarClassName="shrink-0 border-border bg-muted/30"
                                />
                                {tripPreviewCaption ? (
                                    <p className="shrink-0 border-t border-border bg-muted/20 px-3 py-2 text-center text-sm text-muted-foreground">
                                        {tripPreviewCaption}
                                        {tripPreviewUrls.length > 1
                                            ? ` (${tripPreviewSlideIndex + 1} / ${tripPreviewUrls.length})`
                                            : ""}
                                    </p>
                                ) : null}
                                <ImageUrlPreviewDownloadBar
                                    currentUrl={tripPreviewCurrentUrl}
                                    currentFilenameStem={tripPreviewDownloadStem}
                                    zipEntries={tripPhotoZipEntries}
                                    zipFilename={tripPreviewZipFilename}
                                    labels={{
                                        downloadCurrent: t("driverMonitor.preview.downloadCurrent"),
                                        downloadAllZip: t("driverMonitor.preview.downloadAllZip"),
                                        downloadCurrentLoading: t("driverMonitor.preview.downloadCurrentLoading"),
                                    }}
                                />
                            </div>
                        </div>
                    ) : null}
                </DialogContent>
            </Dialog>

            {detailTrip && (
                <EditTripDetailsDialog
                    open={editTripDialogOpen}
                    onOpenChange={setEditTripDialogOpen}
                    trip={detailTrip}
                    getSourceDisplayName={getSourceDisplayName}
                    onSuccess={async () => {
                        setEditTripDialogOpen(false);
                        // Refetch trip to show updated delivery stops
                        if (detailTrip.id) {
                            try {
                                const docSnap = await getDoc(
                                    doc(db, COLLECTIONS.TRIP_RECORDS, detailTrip.id)
                                );
                                if (docSnap.exists()) {
                                    const updated = docSnap.data();
                                    setDetailTrip({
                                        ...detailTrip,
                                        deliveryStopsProgress: updated.deliveryStopsProgress ?? [],
                                        isMultiDelivery: updated.isMultiDelivery ?? false,
                                        totalDeliveryStops: updated.totalDeliveryStops,
                                        billingEstimateThb: updated.billingEstimateThb,
                                        billingIsMultiDelivery: updated.billingIsMultiDelivery,
                                        billingMultiDeliveryBreakdown: updated.billingMultiDeliveryBreakdown,
                                    } as TripRecord);
                                }
                            } catch (e) {
                                console.error("Failed to refetch trip:", e);
                            }
                        }
                    }}
                    destinationOptions={destinationOptions}
                />
            )}
        </div>
    );
}
