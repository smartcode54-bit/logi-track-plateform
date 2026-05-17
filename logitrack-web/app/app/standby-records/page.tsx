"use client"

import { useState, useEffect, useMemo } from "react"
import { collection, onSnapshot, query, orderBy, limit } from "firebase/firestore"
import { db } from "@/firebase/client"
import { COLLECTIONS } from "@/lib/collections"
import { useLanguage } from "@/context/language"
import { format } from "date-fns"
import { PagePermissionGuard } from "@/components/page-permission-guard"
import { CAPABILITIES } from "@/lib/capabilities"
import {
    Search,
    RefreshCw,
    Loader2,
    CalendarIcon,
    PauseCircle,
    Clock,
    MapPin,
} from "lucide-react"

import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from "@/components/ui/table"
import { Input } from "@/components/ui/input"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Card, CardContent } from "@/components/ui/card"
import { cn } from "@/lib/utils"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Calendar } from "@/components/ui/calendar"
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "@/components/ui/dialog"
import { ImagePreviewGallery } from "@/components/accounting/ImagePreviewGallery"
import { StandbyBackfillDialog } from "./standby-backfill-dialog"
import { usePermission } from "@/hooks/usePermission"
import { Plus } from "lucide-react"

// --- Types ---
interface StandbyPhoto {
    url: string
    type: "customer_worksheet" | "site_photo"
}

interface StandbyRecord {
    id: string
    driverId: string | null
    taskId: string | null
    startLocation: string | null
    endLocation: string | null
    startedAt: Date | null
    endedAt: Date | null
    durationMinutes: number | null
    photos: StandbyPhoto[]
    note: string | null
    status: string
    lat?: number | null
    lng?: number | null
    createdAt: Date | null
}

interface Driver {
    id: string
    authId?: string
    firstName?: string
    lastName?: string
}

function toDate(val: unknown): Date | null {
    if (!val) return null
    if (typeof val === "object" && val !== null && "toDate" in val && typeof (val as { toDate: unknown }).toDate === "function") {
        return (val as { toDate: () => Date }).toDate()
    }
    if (val instanceof Date) return val
    if (typeof val === "number" || typeof val === "string") return new Date(val)
    return null
}

export default function StandbyRecordsPage() {
    const { t } = useLanguage()

    const [records, setRecords] = useState<StandbyRecord[]>([])
    const [drivers, setDrivers] = useState<Record<string, Driver>>({})
    const [loading, setLoading] = useState(true)

    const [dateFrom, setDateFrom] = useState<Date | undefined>(undefined)
    const [dateTo, setDateTo] = useState<Date | undefined>(undefined)
    const [searchQuery, setSearchQuery] = useState("")

    const [currentPage, setCurrentPage] = useState(1)
    const itemsPerPage = 15

    const [detailRecord, setDetailRecord] = useState<StandbyRecord | null>(null)
    const [backfillOpen, setBackfillOpen] = useState(false)
    const { hasPermission: canCreateStandby } = usePermission(CAPABILITIES.operations_create_standby)

    const fetchRecords = () => {
        setLoading(true)
        const q = query(
            collection(db, COLLECTIONS.STANDBY_RECORDS),
            orderBy("createdAt", "desc"),
            limit(300)
        )
        const unsub = onSnapshot(q, (snap) => {
            const list: StandbyRecord[] = snap.docs.map(d => {
                const data = d.data()
                return {
                    id: d.id,
                    driverId: data.driverId ?? null,
                    taskId: data.taskId ?? null,
                    startLocation: data.startLocation ?? null,
                    endLocation: data.endLocation ?? null,
                    startedAt: toDate(data.startedAt),
                    endedAt: toDate(data.endedAt),
                    durationMinutes: data.durationMinutes ?? null,
                    photos: Array.isArray(data.photos) ? data.photos : [],
                    note: data.note ?? null,
                    status: data.status ?? "completed",
                    lat: data.lat ?? null,
                    lng: data.lng ?? null,
                    createdAt: toDate(data.createdAt),
                } as StandbyRecord
            })
            setRecords(list)
            setLoading(false)
        }, err => {
            console.error("Error fetching standby records:", err)
            setLoading(false)
        })
        return unsub
    }

    useEffect(() => {
        const unsub = fetchRecords()
        return () => unsub()
    }, [])

    useEffect(() => {
        const q = query(collection(db, COLLECTIONS.DRIVERS), limit(300))
        const unsub = onSnapshot(q, (snap) => {
            const map: Record<string, Driver> = {}
            snap.docs.forEach((d) => {
                const data = d.data()
                map[d.id] = { id: d.id, ...data } as Driver
            })
            setDrivers(map)
        })
        return () => unsub()
    }, [])

    const driversByAuthId = useMemo(() => {
        const byAuth: Record<string, Driver> = {}
        Object.values(drivers).forEach((d) => {
            if (d.authId) byAuth[d.authId] = d
        })
        return byAuth
    }, [drivers])

    const getDriverName = (driverId?: string | null) => {
        if (!driverId) return "-"
        const driver = driversByAuthId[driverId] ?? drivers[driverId] ?? null
        if (!driver) return driverId.slice(0, 8) + "..."
        return `${driver.firstName ?? ""} ${driver.lastName ?? ""}`.trim() || "-"
    }

    const filteredRecords = useMemo(() => {
        const fromMs = dateFrom ? new Date(dateFrom.getFullYear(), dateFrom.getMonth(), dateFrom.getDate(), 0, 0, 0, 0).getTime() : null
        const toMs = dateTo ? new Date(dateTo.getFullYear(), dateTo.getMonth(), dateTo.getDate(), 23, 59, 59, 999).getTime() : null
        return records.filter(rec => {
            if (fromMs != null || toMs != null) {
                const recDate = rec.createdAt
                if (!recDate) return false
                const t = recDate.getTime()
                if (fromMs != null && t < fromMs) return false
                if (toMs != null && t > toMs) return false
            }
            if (searchQuery.trim()) {
                const q = searchQuery.toLowerCase()
                const driverName = getDriverName(rec.driverId).toLowerCase()
                const origin = (rec.startLocation ?? "").toLowerCase()
                const dest = (rec.endLocation ?? "").toLowerCase()
                const note = (rec.note ?? "").toLowerCase()
                if (!driverName.includes(q) && !origin.includes(q) && !dest.includes(q) && !note.includes(q)) {
                    return false
                }
            }
            return true
        })
    }, [records, dateFrom, dateTo, searchQuery, driversByAuthId, drivers])

    const paginatedRecords = filteredRecords.slice(
        (currentPage - 1) * itemsPerPage,
        currentPage * itemsPerPage
    )
    const totalPages = Math.ceil(filteredRecords.length / itemsPerPage)

    useEffect(() => { setCurrentPage(1) }, [dateFrom, dateTo, searchQuery])

    const fmt = (val: unknown) => {
        const d = toDate(val)
        return d ? format(d, "dd/MM/yy HH:mm") : "-"
    }

    return (
        <PagePermissionGuard capability={CAPABILITIES.operations_view_driver_monitor}>
            <div className="container mx-auto p-6 space-y-6 max-w-[1600px]">
                {/* Header */}
                <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4">
                    <div>
                        <h1 className="text-3xl font-bold tracking-tight">
                            {t("standbyRecords.title", "Standby Records")}
                        </h1>
                        <p className="text-muted-foreground mt-1">
                            {t("standbyRecords.subtitle", "Driver check-in records with no delivery jobs")}
                        </p>
                    </div>
                    <div className="flex items-center gap-2">
                        {canCreateStandby && (
                            <Button onClick={() => setBackfillOpen(true)} className="gap-2">
                                <Plus className="h-4 w-4" />
                                {t("standbyRecords.create.button", "Backfill Standby")}
                            </Button>
                        )}
                        <Button variant="outline" size="icon" onClick={() => fetchRecords()}>
                            <RefreshCw className="h-4 w-4" />
                        </Button>
                    </div>
                </div>

                {/* Stats */}
                <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
                    <Card className="bg-card">
                        <CardContent className="p-6 flex items-center justify-between">
                            <div>
                                <p className="text-sm font-medium text-muted-foreground">
                                    {t("standbyRecords.stats.total", "Total Records")}
                                </p>
                                <h2 className="text-3xl font-bold mt-1">{records.length}</h2>
                            </div>
                            <div className="h-12 w-12 rounded-full bg-orange-100 dark:bg-orange-900/30 flex items-center justify-center text-orange-500">
                                <PauseCircle className="h-6 w-6" />
                            </div>
                        </CardContent>
                    </Card>
                </div>

                {/* Filters */}
                <div className="flex flex-col sm:flex-row flex-wrap gap-4 bg-card/50 p-4 rounded-lg border border-border/50">
                    <Popover>
                        <PopoverTrigger asChild>
                            <Button
                                variant="outline"
                                size="sm"
                                className={cn(
                                    "w-[180px] justify-start text-left font-normal",
                                    !dateFrom && "text-muted-foreground"
                                )}
                            >
                                <CalendarIcon className="mr-2 h-4 w-4" />
                                {dateFrom
                                    ? format(dateFrom, "dd/MM/yyyy")
                                    : t("standbyRecords.filter.from", "From date")}
                            </Button>
                        </PopoverTrigger>
                        <PopoverContent className="w-auto p-0" align="start">
                            <Calendar
                                mode="single"
                                selected={dateFrom}
                                onSelect={setDateFrom}
                                initialFocus
                            />
                        </PopoverContent>
                    </Popover>

                    <Popover>
                        <PopoverTrigger asChild>
                            <Button
                                variant="outline"
                                size="sm"
                                className={cn(
                                    "w-[180px] justify-start text-left font-normal",
                                    !dateTo && "text-muted-foreground"
                                )}
                            >
                                <CalendarIcon className="mr-2 h-4 w-4" />
                                {dateTo
                                    ? format(dateTo, "dd/MM/yyyy")
                                    : t("standbyRecords.filter.to", "To date")}
                            </Button>
                        </PopoverTrigger>
                        <PopoverContent className="w-auto p-0" align="start">
                            <Calendar
                                mode="single"
                                selected={dateTo}
                                onSelect={setDateTo}
                                initialFocus
                            />
                        </PopoverContent>
                    </Popover>

                    <div className="relative flex-1 max-w-sm">
                        <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
                        <Input
                            placeholder={t("standbyRecords.filter.search", "Search driver, location...")}
                            className="pl-10 bg-background/50 border-border/50 h-9"
                            value={searchQuery}
                            onChange={(e) => setSearchQuery(e.target.value)}
                        />
                    </div>

                    {(dateFrom || dateTo) && (
                        <Button
                            variant="ghost"
                            size="sm"
                            onClick={() => {
                                setDateFrom(undefined)
                                setDateTo(undefined)
                            }}
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
                                    <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">
                                        {t("standbyRecords.table.date", "Date")}
                                    </TableHead>
                                    <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">
                                        {t("standbyRecords.table.driver", "Driver")}
                                    </TableHead>
                                    <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">
                                        {t("standbyRecords.table.route", "Origin → Destination")}
                                    </TableHead>
                                    <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">
                                        {t("standbyRecords.table.startedAt", "Check-in")}
                                    </TableHead>
                                    <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">
                                        {t("standbyRecords.table.endedAt", "Ended")}
                                    </TableHead>
                                    <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">
                                        {t("standbyRecords.table.duration", "Duration (min)")}
                                    </TableHead>
                                    <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">
                                        {t("standbyRecords.table.status", "Status")}
                                    </TableHead>
                                </TableRow>
                            </TableHeader>
                            <TableBody>
                                {loading ? (
                                    <TableRow>
                                        <TableCell colSpan={7} className="h-32 text-center">
                                            <div className="flex flex-col items-center justify-center gap-2">
                                                <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
                                                <p className="text-sm text-muted-foreground">
                                                    {t("standbyRecords.table.loading", "Loading...")}
                                                </p>
                                            </div>
                                        </TableCell>
                                    </TableRow>
                                ) : paginatedRecords.length === 0 ? (
                                    <TableRow>
                                        <TableCell colSpan={7} className="h-32 text-center">
                                            <p className="text-sm text-muted-foreground">
                                                {t("standbyRecords.table.noRecords", "No standby records found")}
                                            </p>
                                        </TableCell>
                                    </TableRow>
                                ) : (
                                    paginatedRecords.map((rec) => (
                                        <TableRow
                                            key={rec.id}
                                            className="cursor-pointer hover:bg-orange-50/50 dark:hover:bg-orange-900/10 transition-colors border-b border-border/50"
                                            onClick={() => setDetailRecord(rec)}
                                        >
                                            <TableCell className="text-sm">
                                                {fmt(rec.createdAt)}
                                            </TableCell>
                                            <TableCell>
                                                <span className="font-medium text-sm">
                                                    {getDriverName(rec.driverId)}
                                                </span>
                                            </TableCell>
                                            <TableCell>
                                                <span className="text-sm text-muted-foreground">
                                                    {rec.startLocation ?? "-"}
                                                    {rec.startLocation && rec.endLocation ? "  →  " : ""}
                                                    {rec.endLocation ?? ""}
                                                </span>
                                            </TableCell>
                                            <TableCell className="text-sm">
                                                {fmt(rec.startedAt)}
                                            </TableCell>
                                            <TableCell className="text-sm">
                                                {fmt(rec.endedAt)}
                                            </TableCell>
                                            <TableCell className="text-sm font-mono">
                                                {rec.durationMinutes != null ? rec.durationMinutes : "-"}
                                            </TableCell>
                                            <TableCell>
                                                <Badge variant="outline" className="font-medium bg-orange-50 text-orange-700 border-orange-200 dark:bg-orange-950/30 dark:text-orange-400 dark:border-orange-900">
                                                    {rec.status.toUpperCase()}
                                                </Badge>
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
                            Showing {paginatedRecords.length > 0 ? (currentPage - 1) * itemsPerPage + 1 : 0} to {Math.min(currentPage * itemsPerPage, filteredRecords.length)} of {filteredRecords.length} entries
                        </div>
                        <div className="flex items-center gap-2">
                            <Button
                                variant="outline"
                                size="sm"
                                onClick={() => setCurrentPage((p) => Math.max(1, p - 1))}
                                disabled={currentPage === 1}
                            >
                                Previous
                            </Button>
                            <Button
                                variant="outline"
                                size="sm"
                                onClick={() => setCurrentPage((p) => Math.min(totalPages, p + 1))}
                                disabled={currentPage === totalPages || totalPages === 0}
                            >
                                Next
                            </Button>
                        </div>
                    </div>
                </div>

                {/* Backfill Dialog (admin-only) */}
                {canCreateStandby && (
                    <StandbyBackfillDialog
                        open={backfillOpen}
                        onOpenChange={setBackfillOpen}
                        onSaved={() => fetchRecords()}
                    />
                )}

                {/* Detail Dialog */}
                <Dialog open={!!detailRecord} onOpenChange={(open) => !open && setDetailRecord(null)}>
                    <DialogContent className="max-w-xl">
                        <DialogHeader>
                            <DialogTitle className="flex items-center gap-2">
                                <PauseCircle className="w-5 h-5 text-orange-500" />
                                {t("standbyRecords.detail.title", "Standby Record Detail")}
                            </DialogTitle>
                            <DialogDescription>
                                ID: {detailRecord?.id}
                            </DialogDescription>
                        </DialogHeader>

                        <ScrollArea className="max-h-[80vh] px-1">
                            {detailRecord && (
                                <div className="grid gap-4 py-2 pr-4">
                                    {/* Summary card */}
                                    <div className="bg-orange-50 dark:bg-orange-950/20 border border-orange-200 dark:border-orange-900 rounded-lg p-4 space-y-2">
                                        <div className="flex items-center gap-2 text-sm font-medium text-orange-900 dark:text-orange-100">
                                            <MapPin className="w-4 h-4" />
                                            {detailRecord.startLocation ?? "-"}  →  {detailRecord.endLocation ?? "-"}
                                        </div>
                                        <div className="flex items-center gap-2 text-xs text-orange-700 dark:text-orange-400">
                                            <Clock className="w-3.5 h-3.5" />
                                            {fmt(detailRecord.startedAt)}  →  {fmt(detailRecord.endedAt)}
                                            {detailRecord.durationMinutes != null && (
                                                <span className="ml-2 font-mono">({detailRecord.durationMinutes} {t("standbyRecords.detail.minutes", "min")})</span>
                                            )}
                                        </div>
                                    </div>

                                    {/* Info grid */}
                                    <div className="grid grid-cols-2 gap-4 text-sm bg-muted/30 p-4 rounded-lg">
                                        <div>
                                            <p className="text-muted-foreground text-xs uppercase tracking-wider mb-1">
                                                {t("standbyRecords.table.driver", "Driver")}
                                            </p>
                                            <p className="font-medium">{getDriverName(detailRecord.driverId)}</p>
                                        </div>
                                        <div>
                                            <p className="text-muted-foreground text-xs uppercase tracking-wider mb-1">
                                                {t("standbyRecords.detail.taskId", "Task ID")}
                                            </p>
                                            <p className="font-mono text-xs">{detailRecord.taskId ? detailRecord.taskId.slice(0, 12) + "..." : "-"}</p>
                                        </div>
                                        {detailRecord.note && (
                                            <div className="col-span-2">
                                                <p className="text-muted-foreground text-xs uppercase tracking-wider mb-1">
                                                    {t("standbyRecords.detail.note", "Note")}
                                                </p>
                                                <p className="text-sm">{detailRecord.note}</p>
                                            </div>
                                        )}
                                        {detailRecord.lat != null && detailRecord.lng != null && (
                                            <div className="col-span-2">
                                                <p className="text-muted-foreground text-xs uppercase tracking-wider mb-1">
                                                    {t("standbyRecords.detail.location", "Location")}
                                                </p>
                                                <a
                                                    href={`https://www.google.com/maps?q=${detailRecord.lat},${detailRecord.lng}`}
                                                    target="_blank"
                                                    rel="noreferrer"
                                                    className="text-blue-500 hover:underline text-xs font-mono"
                                                >
                                                    {detailRecord.lat.toFixed(5)}, {detailRecord.lng.toFixed(5)}
                                                </a>
                                            </div>
                                        )}
                                    </div>

                                    {/* Photos */}
                                    {detailRecord.photos.length > 0 && (
                                        <div className="mt-2">
                                            <ImagePreviewGallery
                                                compact
                                                items={detailRecord.photos.map(p => ({
                                                    url: p.url,
                                                    label: p.type === "customer_worksheet"
                                                        ? t("standbyRecords.detail.worksheetPhoto", "Customer Worksheet")
                                                        : t("standbyRecords.detail.sitePhoto", "Site Photo"),
                                                }))}
                                            />
                                        </div>
                                    )}
                                </div>
                            )}
                        </ScrollArea>
                    </DialogContent>
                </Dialog>
            </div>
        </PagePermissionGuard>
    )
}
