"use client"

import { useState, useEffect, useMemo } from "react"
import { collection, onSnapshot, query, orderBy, limit, getDoc, doc as firestoreDoc } from "firebase/firestore"
import { db } from "@/firebase/client"
import { COLLECTIONS } from "@/lib/collections"
import { useLanguage } from "@/context/language"
import { PagePermissionGuard } from "@/components/page-permission-guard"
import { CAPABILITIES } from "@/lib/capabilities"
import { format } from "date-fns"
import {
    Search,
    RefreshCw,
    Loader2,
    CalendarIcon,
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
import { Card, CardContent } from "@/components/ui/card"
import { cn } from "@/lib/utils"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Calendar } from "@/components/ui/calendar"
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select"

interface CustomStopRow {
    tripRecordId: string
    taskId: string | null
    driverId: string | null
    destination: string
    stopIndex: number
    status: "pending" | "delivered" | "failed"
    addedAt: Date | null
    deliveredAt: Date | null
}

interface Driver {
    id: string
    authId?: string
    firstName?: string
    lastName?: string
}

function toDate(val: unknown): Date | null {
    if (!val) return null
    if (typeof val.toDate === "function") return val.toDate()
    if (val instanceof Date) return val
    if (typeof val === "number" || typeof val === "string") return new Date(val)
    return null
}

export default function CustomStopsReviewPage() {
    const { t } = useLanguage()

    const [rows, setRows] = useState<CustomStopRow[]>([])
    const [drivers, setDrivers] = useState<Record<string, Driver>>({})
    const [loading, setLoading] = useState(true)

    const [date, setDate] = useState<Date | undefined>(undefined)
    const [searchQuery, setSearchQuery] = useState("")
    const [statusFilter, setStatusFilter] = useState<string>("all")

    const [currentPage, setCurrentPage] = useState(1)
    const itemsPerPage = 15

    const fetchData = () => {
        setLoading(true)
        // Query trip_records where isMultiDelivery=true
        const q = query(
            collection(db, COLLECTIONS.TRIP_RECORDS),
            orderBy("createdAt", "desc"),
            limit(300)
        )
        const unsub = onSnapshot(q, async (snap) => {
            const allRows: CustomStopRow[] = []

            // For each trip_record, we need the task to know which stops are isCustom
            const taskFetches: Promise<void>[] = []

            snap.docs.forEach((doc) => {
                const tripData = doc.data()
                if (!tripData.isMultiDelivery) return

                const taskId: string | null = tripData.taskId ?? null
                const driverId: string | null = tripData.driverId ?? null
                const progress: unknown[] = tripData.deliveryStopsProgress ?? []

                const fetchTask = async () => {
                    const customStopIndices = new Set<number>()
                    const addedAtByIndex: Record<number, Date | null> = {}

                    if (taskId) {
                        try {
                            const taskDoc = await getDoc(firestoreDoc(db, COLLECTIONS.TASKS, taskId))
                            if (taskDoc.exists()) {
                                const taskData = taskDoc.data() as Record<string, unknown>
                                const stops: unknown[] = (taskData.deliveryStops as unknown[] | undefined) ?? []
                                stops.forEach((s: unknown) => {
                                    const stop = s as Record<string, unknown>
                                    if (stop.isCustom) {
                                        customStopIndices.add(stop.index as number)
                                        addedAtByIndex[stop.index as number] = toDate(stop.addedAt)
                                    }
                                })
                            }
                        } catch {
                            // skip
                        }
                    }

                    progress.forEach((p: unknown) => {
                        const prog = p as Record<string, unknown>
                        if (!customStopIndices.has(prog.index as number)) return
                        allRows.push({
                            tripRecordId: doc.id,
                            taskId,
                            driverId,
                            destination: String(prog.destination ?? "-"),
                            stopIndex: prog.index as number,
                            status: String(prog.status ?? "pending"),
                            addedAt: addedAtByIndex[prog.index as number] ?? null,
                            deliveredAt: toDate(prog.deliveredAt),
                        })
                    })
                }

                taskFetches.push(fetchTask())
            })

            await Promise.all(taskFetches)
            setRows(allRows)
            setLoading(false)
        }, (err) => {
            console.error("[CustomStops] error:", err)
            setLoading(false)
        })
        return unsub
    }

    useEffect(() => {
        const unsub = fetchData()
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

    const filteredRows = useMemo(() => {
        return rows.filter((row) => {
            if (date) {
                const addedDate = row.addedAt
                if (addedDate) {
                    if (format(date, "dd/MM/yyyy") !== format(addedDate, "dd/MM/yyyy")) return false
                } else {
                    return false
                }
            }
            if (statusFilter !== "all" && row.status !== statusFilter) return false
            if (searchQuery.trim()) {
                const q = searchQuery.toLowerCase()
                const driver = getDriverName(row.driverId).toLowerCase()
                const dest = row.destination.toLowerCase()
                const trip = row.tripRecordId.toLowerCase()
                if (!driver.includes(q) && !dest.includes(q) && !trip.includes(q)) return false
            }
            return true
        })
    }, [rows, date, statusFilter, searchQuery, driversByAuthId, drivers])

    const paginated = filteredRows.slice(
        (currentPage - 1) * itemsPerPage,
        currentPage * itemsPerPage
    )
    const totalPages = Math.ceil(filteredRows.length / itemsPerPage)

    useEffect(() => { setCurrentPage(1) }, [date, statusFilter, searchQuery])

    const formatTs = (val: any) => {
        const d = toDate(val)
        return d ? format(d, "dd/MM/yy HH:mm") : "-"
    }

    const statusBadge = (status: string) => {
        if (status === "delivered") return <Badge className="bg-green-100 text-green-700 border-green-200 dark:bg-green-950/30 dark:text-green-400 dark:border-green-900">{t("customStops.status.delivered")}</Badge>
        if (status === "failed") return <Badge className="bg-red-100 text-red-700 border-red-200 dark:bg-red-950/30 dark:text-red-400 dark:border-red-900">{t("customStops.status.failed")}</Badge>
        return <Badge variant="outline" className="text-yellow-700 border-yellow-300 dark:text-yellow-400 dark:border-yellow-800">{t("customStops.status.pending")}</Badge>
    }

    return (
        <PagePermissionGuard capability={CAPABILITIES.operations_view_custom_stops}>
            <div className="container mx-auto p-6 space-y-6 max-w-[1600px]">
                {/* Header */}
                <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4">
                    <div>
                        <h1 className="text-3xl font-bold tracking-tight">
                            {t("customStops.title")}
                        </h1>
                        <p className="text-muted-foreground mt-1">
                            {t("customStops.subtitle")}
                        </p>
                    </div>
                    <Button variant="outline" size="icon" onClick={() => { setRows([]); fetchData() }}>
                        <RefreshCw className="h-4 w-4" />
                    </Button>
                </div>

                {/* Stats */}
                <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
                    <Card className="bg-card">
                        <CardContent className="p-6 flex items-center justify-between">
                            <div>
                                <p className="text-sm font-medium text-muted-foreground">
                                    {t("customStops.stats.total")}
                                </p>
                                <h2 className="text-3xl font-bold mt-1">{rows.length}</h2>
                            </div>
                            <div className="h-12 w-12 rounded-full bg-blue-100 dark:bg-blue-900/30 flex items-center justify-center text-blue-500">
                                <MapPin className="h-6 w-6" />
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
                                {date ? format(date, "dd/MM/yyyy") : t("customStops.filter.pickDate")}
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

                    <Select value={statusFilter} onValueChange={setStatusFilter}>
                        <SelectTrigger className="w-[160px] h-9">
                            <SelectValue placeholder={t("customStops.filter.status")} />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value="all">{t("customStops.filter.allStatuses")}</SelectItem>
                            <SelectItem value="pending">{t("customStops.status.pending")}</SelectItem>
                            <SelectItem value="delivered">{t("customStops.status.delivered")}</SelectItem>
                            <SelectItem value="failed">{t("customStops.status.failed")}</SelectItem>
                        </SelectContent>
                    </Select>

                    <div className="relative flex-1 max-w-sm">
                        <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
                        <Input
                            placeholder={t("customStops.filter.search")}
                            className="pl-10 bg-background/50 border-border/50 h-9"
                            value={searchQuery}
                            onChange={(e) => setSearchQuery(e.target.value)}
                        />
                    </div>

                    {(date || statusFilter !== "all") && (
                        <Button
                            variant="ghost"
                            size="sm"
                            onClick={() => { setDate(undefined); setStatusFilter("all") }}
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
                                        {t("customStops.table.tripId")}
                                    </TableHead>
                                    <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">
                                        {t("customStops.table.addedAt")}
                                    </TableHead>
                                    <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">
                                        {t("customStops.table.driver")}
                                    </TableHead>
                                    <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">
                                        {t("customStops.table.destination")}
                                    </TableHead>
                                    <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">
                                        {t("customStops.table.status")}
                                    </TableHead>
                                    <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">
                                        {t("customStops.table.deliveredAt")}
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
                                                    {t("customStops.table.loading")}
                                                </p>
                                            </div>
                                        </TableCell>
                                    </TableRow>
                                ) : paginated.length === 0 ? (
                                    <TableRow>
                                        <TableCell colSpan={6} className="h-32 text-center">
                                            <p className="text-sm text-muted-foreground">
                                                {t("customStops.table.noStops")}
                                            </p>
                                        </TableCell>
                                    </TableRow>
                                ) : (
                                    paginated.map((row, i) => (
                                        <TableRow
                                            key={`${row.tripRecordId}-${row.stopIndex}-${i}`}
                                            className="border-b border-border/50 hover:bg-muted/30 transition-colors"
                                        >
                                            <TableCell>
                                                <span className="font-mono text-xs text-muted-foreground">
                                                    {row.tripRecordId.slice(0, 10)}
                                                </span>
                                            </TableCell>
                                            <TableCell className="text-sm">
                                                {formatTs(row.addedAt)}
                                            </TableCell>
                                            <TableCell>
                                                <span className="font-medium text-sm">
                                                    {getDriverName(row.driverId)}
                                                </span>
                                            </TableCell>
                                            <TableCell>
                                                <div className="flex items-center gap-1.5">
                                                    <MapPin className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
                                                    <span className="text-sm font-medium">{row.destination}</span>
                                                </div>
                                            </TableCell>
                                            <TableCell>
                                                {statusBadge(row.status)}
                                            </TableCell>
                                            <TableCell className="text-sm text-muted-foreground">
                                                {formatTs(row.deliveredAt)}
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
                            Showing {paginated.length > 0 ? (currentPage - 1) * itemsPerPage + 1 : 0} to {Math.min(currentPage * itemsPerPage, filteredRows.length)} of {filteredRows.length} entries
                        </div>
                        <div className="flex items-center gap-2">
                            <Button
                                variant="outline"
                                size="sm"
                                onClick={() => setCurrentPage((p) => Math.max(1, p - 1))}
                                disabled={currentPage === 1}
                            >
                                {t("incidentReports.pagination.previous")}
                            </Button>
                            <Button
                                variant="outline"
                                size="sm"
                                onClick={() => setCurrentPage((p) => Math.min(totalPages, p + 1))}
                                disabled={currentPage === totalPages || totalPages === 0}
                            >
                                {t("incidentReports.pagination.next")}
                            </Button>
                        </div>
                    </div>
                </div>
            </div>
        </PagePermissionGuard>
    )
}
