"use client"

import { useState, useEffect, useMemo } from "react"
import { collection, onSnapshot, query, orderBy, limit, getDocs, where } from "firebase/firestore"
import { db } from "@/firebase/client"
import { COLLECTIONS } from "@/lib/collections"
import { useLanguage } from "@/context/language"
import { useAuth } from "@/context/auth"
import { useCustomerScope } from "@/hooks/useCustomerScope"
import { format } from "date-fns"
import { PagePermissionGuard } from "@/components/page-permission-guard"
import { CAPABILITIES } from "@/lib/capabilities"
import {
    AlertTriangle,
    Search,
    RefreshCw,
    Loader2,
    CalendarIcon,
    Navigation,
    Clock
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
import { IncidentLocationMap } from "@/features/incident-reports"
import { ImagePreviewGallery } from "@/components/accounting/ImagePreviewGallery"

// --- Types ---
interface IncidentReport {
    id: string
    description: string
    delayCause: string | null
    tripId: string | null
    driverId: string | null
    truckId: string | null
    truckPlate: string | null
    reportedBy: string | null
    reportedByUid: string | null
    createdAt: Date | null
    updatedAt: Date | null
    lat?: number | null
    lng?: number | null
    mapPhotoUrl?: string | null
    situation1PhotoUrl?: string | null
    situation2PhotoUrl?: string | null
}

interface Driver {
    id: string
    authId?: string
    firstName?: string
    lastName?: string
}

function toDate(val: any): Date | null {
    if (!val) return null
    if (typeof val.toDate === "function") return val.toDate()
    if (val instanceof Date) return val
    if (typeof val === "number" || typeof val === "string") return new Date(val)
    return null
}

export default function IncidentReportsPage() {
    const { t } = useLanguage()
    const auth = useAuth()
    const { customerScopeId, isCustomer } = useCustomerScope()

    const [reports, setReports] = useState<IncidentReport[]>([])
    const [drivers, setDrivers] = useState<Record<string, Driver>>({})
    const [loading, setLoading] = useState(true)
    const [customerTripIds, setCustomerTripIds] = useState<Set<string>>(new Set())

    // Filters
    const [date, setDate] = useState<Date | undefined>(undefined)
    const [searchQuery, setSearchQuery] = useState("")

    // Pagination
    const [currentPage, setCurrentPage] = useState(1)
    const itemsPerPage = 15

    // Detail Dialog
    const [detailReport, setDetailReport] = useState<IncidentReport | null>(null)

    const fetchIncidentReports = () => {
        setLoading(true)
        const q = query(
            collection(db, COLLECTIONS.INCIDENT_REPORTS),
            orderBy("createdAt", "desc"),
            limit(200)
        )
        const unsub = onSnapshot(q, (snap) => {
            const list: IncidentReport[] = snap.docs.map(d => {
                const data = d.data()
                return {
                    id: d.id,
                    ...data,
                    createdAt: toDate(data.createdAt),
                    updatedAt: toDate(data.updatedAt)
                } as IncidentReport
            })
            setReports(list)
            setLoading(false)
        }, err => {
            console.error("Error fetching incident reports:", err)
            setLoading(false)
        })
        return unsub
    }

    // Fetch customer's trip IDs for filtering incidents
    useEffect(() => {
        if (!isCustomer || !customerScopeId) {
            setCustomerTripIds(new Set())
            return
        }

        const q = query(
            collection(db, COLLECTIONS.TRIP_RECORDS),
            where("billingCustomerId", "==", customerScopeId),
            limit(500)
        )
        const unsub = getDocs(q).then((snap) => {
            const tripIds = new Set(snap.docs.map((d) => d.id))
            setCustomerTripIds(tripIds)
        }).catch((err) => {
            console.error("Error fetching customer trips:", err)
            setCustomerTripIds(new Set())
        })

        return () => { }
    }, [isCustomer, customerScopeId])

    useEffect(() => {
        const unsub = fetchIncidentReports()
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

    // Lookup driver by authId or doc ID
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
        return `${driver.firstName} ${driver.lastName}`.trim() || "-"
    }

    const filteredReports = useMemo(() => {
        return reports.filter(report => {
            // Customer scope: only show incidents from their trips
            if (isCustomer && customerTripIds.size > 0) {
                if (!report.tripId || !customerTripIds.has(report.tripId)) {
                    return false
                }
            }

            if (date) {
                const reportDate = report.createdAt
                if (reportDate) {
                    const filterStr = format(date, "dd/MM/yyyy")
                    const reportStr = format(reportDate, "dd/MM/yyyy")
                    if (filterStr !== reportStr) return false
                } else {
                    return false
                }
            }

            if (searchQuery.trim()) {
                const q = searchQuery.toLowerCase()
                const desc = (report.description || "").toLowerCase()
                const driverName = getDriverName(report.driverId).toLowerCase()
                const tripId = (report.tripId || "").toLowerCase()
                const truckPlate = (report.truckPlate || "").toLowerCase()

                if (!desc.includes(q) && !driverName.includes(q) && !tripId.includes(q) && !truckPlate.includes(q)) {
                    return false
                }
            }
            return true
        })
    }, [reports, date, searchQuery, drivers, driversByAuthId, isCustomer, customerTripIds])

    const paginatedReports = filteredReports.slice(
        (currentPage - 1) * itemsPerPage,
        currentPage * itemsPerPage
    )
    const totalPages = Math.ceil(filteredReports.length / itemsPerPage)

    useEffect(() => {
        setCurrentPage(1)
    }, [date, searchQuery])

    const formatTimestamp = (val: any) => {
        const d = toDate(val)
        return d ? format(d, "dd/MM/yy HH:mm") : "-"
    }

    return (
        <PagePermissionGuard capability={CAPABILITIES.operations_view_incidents}>
            <div className="container mx-auto p-6 space-y-6 max-w-[1600px]">
            {/* Header */}
            <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">
                        {t("incidentReports.title")}
                    </h1>
                    <p className="text-muted-foreground mt-1">
                        {t("incidentReports.subtitle")}
                    </p>
                </div>
                <Button variant="outline" size="icon" onClick={() => fetchIncidentReports()}>
                    <RefreshCw className="h-4 w-4" />
                </Button>
            </div>

            {/* Stats */}
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
                <Card className="bg-card">
                    <CardContent className="p-6 flex items-center justify-between">
                        <div>
                            <p className="text-sm font-medium text-muted-foreground">
                                {t("incidentReports.stats.total")}
                            </p>
                            <h2 className="text-3xl font-bold mt-1">{reports.length}</h2>
                        </div>
                        <div className="h-12 w-12 rounded-full bg-red-100 dark:bg-red-900/30 flex items-center justify-center text-red-500">
                            <AlertTriangle className="h-6 w-6" />
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
                                !date && "text-muted-foreground"
                            )}
                        >
                            <CalendarIcon className="mr-2 h-4 w-4" />
                            {date ? format(date, "dd/MM/yyyy") : t("incidentReports.filter.pickDate")}
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

                <div className="relative flex-1 max-w-sm">
                    <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
                    <Input
                        placeholder={t("incidentReports.filter.search")}
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
                                <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">
                                    {t("incidentReports.table.tripId", "Trip ID")}
                                </TableHead>
                                <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">
                                    {t("incidentReports.table.createdAt")}
                                </TableHead>
                                <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">
                                    {t("incidentReports.table.driver", "Driver")}
                                </TableHead>
                                <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">
                                    {t("incidentReports.table.plate")}
                                </TableHead>
                                <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider max-w-[300px]">
                                    {t("incidentReports.table.description", "Description")}
                                </TableHead>
                                <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">
                                    {t("incidentReports.table.cause")}
                                </TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {loading ? (
                                <TableRow>
                                    <TableCell colSpan={6} className="h-32 text-center">
                                        <div className="flex flex-col items-center justify-center gap-2">
                                            <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
                                            <p className="text-sm text-muted-foreground">
                                                {t("incidentReports.table.loading")}
                                            </p>
                                        </div>
                                    </TableCell>
                                </TableRow>
                            ) : paginatedReports.length === 0 ? (
                                <TableRow>
                                    <TableCell colSpan={6} className="h-32 text-center">
                                        <p className="text-sm text-muted-foreground">
                                            {t("incidentReports.table.noReports")}
                                        </p>
                                    </TableCell>
                                </TableRow>
                            ) : (
                                paginatedReports.map((report) => (
                                    <TableRow
                                        key={report.id}
                                        className="cursor-pointer hover:bg-red-50/50 dark:hover:bg-red-900/10 transition-colors border-b border-border/50"
                                        onClick={() => setDetailReport(report)}
                                    >
                                        <TableCell>
                                            <span className="font-mono text-xs text-muted-foreground">
                                                {report.tripId?.slice(0, 10) || "-"}
                                            </span>
                                        </TableCell>
                                        <TableCell className="text-sm">
                                            {formatTimestamp(report.createdAt)}
                                        </TableCell>
                                        <TableCell>
                                            <span className="font-medium text-sm">
                                                {getDriverName(report.driverId)}
                                            </span>
                                        </TableCell>
                                        <TableCell>
                                            <span className="font-mono text-sm text-muted-foreground">
                                                {report.truckPlate || "-"}
                                            </span>
                                        </TableCell>
                                        <TableCell className="max-w-[300px] truncate text-sm">
                                            {report.description}
                                        </TableCell>
                                        <TableCell>
                                            <Badge variant="outline" className="font-medium bg-red-50 text-red-700 border-red-200 dark:bg-red-950/30 dark:text-red-400 dark:border-red-900">
                                                {report.delayCause?.replace("incident_cause_", "").toUpperCase() || "-"}
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
                        Showing {paginatedReports.length > 0 ? (currentPage - 1) * itemsPerPage + 1 : 0} to {Math.min(currentPage * itemsPerPage, filteredReports.length)} of {filteredReports.length} entries
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

            {/* Detail Dialog */}
            <Dialog open={!!detailReport} onOpenChange={(open) => !open && setDetailReport(null)}>
                <DialogContent className="max-w-xl">
                    <DialogHeader>
                        <DialogTitle className="flex items-center gap-2">
                            <img src="/exclamation_8848378.png" alt="incident" className="w-5 h-5 object-contain" />
                            {t("incidentReports.detail.title")}
                        </DialogTitle>
                        <DialogDescription>
                            ID: {detailReport?.id}
                        </DialogDescription>
                    </DialogHeader>

                    <ScrollArea className="max-h-[80vh] px-1">
                        {detailReport && (
                            <div className="grid gap-4 py-2 pr-4">
                                <div className="space-y-3">
                                    <div className="bg-red-50 dark:bg-red-950/20 border border-red-200 dark:border-red-900 rounded-lg p-4">
                                        <p className="text-sm font-medium text-red-900 dark:text-red-100">
                                            {detailReport.description}
                                        </p>
                                        {detailReport.delayCause && (
                                            <p className="text-xs font-semibold text-red-700 dark:text-red-400 mt-2">
                                                {t("incidentReports.detail.cause")}: {detailReport.delayCause.replace("incident_cause_", "").toUpperCase()}
                                            </p>
                                        )}
                                        <p className="text-[11px] text-red-600/70 dark:text-red-400/70 mt-3 flex items-center gap-1.5">
                                            <Clock className="w-3.5 h-3.5" />
                                            {t("incidentReports.detail.reported")}: {formatTimestamp(detailReport.createdAt)}
                                        </p>
                                    </div>
                                </div>

                                <div className="grid grid-cols-2 gap-4 text-sm mt-4 bg-muted/30 p-4 rounded-lg">
                                    <div>
                                        <p className="text-muted-foreground text-xs uppercase tracking-wider mb-1">{t("incidentReports.table.driver")}</p>
                                        <p className="font-medium">{getDriverName(detailReport.driverId)}</p>
                                    </div>
                                    <div>
                                        <p className="text-muted-foreground text-xs uppercase tracking-wider mb-1">{t("incidentReports.detail.licensePlate")}</p>
                                        <p className="font-mono">{detailReport.truckPlate || "-"}</p>
                                    </div>
                                    <div>
                                        <p className="text-muted-foreground text-xs uppercase tracking-wider mb-1">{t("incidentReports.table.tripId")}</p>
                                        <p className="font-mono">{detailReport.tripId || "-"}</p>
                                    </div>
                                    {(detailReport.lat != null && detailReport.lng != null) && (
                                        <div className="col-span-2 mt-2">
                                            <p className="text-muted-foreground text-xs uppercase tracking-wider mb-2">{t("incidentReports.detail.locationMap")}</p>
                                            <div className="h-[200px] w-full rounded-md border border-border/50 overflow-hidden relative z-0">
                                                <IncidentLocationMap lat={detailReport.lat} lng={detailReport.lng} />
                                            </div>
                                            <div className="mt-2 text-right">
                                                <a href={`https://www.google.com/maps?q=${detailReport.lat},${detailReport.lng}`} target="_blank" rel="noreferrer" className="text-blue-500 hover:underline text-xs font-mono">
                                                    {detailReport.lat.toFixed(5)}, {detailReport.lng.toFixed(5)} ({t("incidentReports.detail.openInMaps")})
                                                </a>
                                            </div>
                                        </div>
                                    )}

                                    {(detailReport.mapPhotoUrl || detailReport.situation1PhotoUrl || detailReport.situation2PhotoUrl) && (
                                        <div className="col-span-2 mt-4">
                                            <ImagePreviewGallery
                                                compact
                                                items={[
                                                    detailReport.mapPhotoUrl ? { url: detailReport.mapPhotoUrl, label: t("incidentReports.detail.locationSnapshot") } : null,
                                                    detailReport.situation1PhotoUrl ? { url: detailReport.situation1PhotoUrl, label: t("incidentReports.detail.situation1") } : null,
                                                    detailReport.situation2PhotoUrl ? { url: detailReport.situation2PhotoUrl, label: t("incidentReports.detail.situation2") } : null,
                                                ].filter((item): item is { url: string; label: string } => item !== null)}
                                            />
                                        </div>
                                    )}
                                </div>
                            </div>
                        )}
                    </ScrollArea>
                </DialogContent>
            </Dialog>
            </div>
        </PagePermissionGuard>
    )
}
