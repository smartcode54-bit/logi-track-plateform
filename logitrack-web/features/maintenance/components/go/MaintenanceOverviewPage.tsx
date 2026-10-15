"use client";

/**
 * The maintenance overview over Go (T42, P3; Appendix E §E.2.2 row `/app/maintenance`; developer-spec.md
 * §10.10; Appendix B §B.2.15 as built by T40).
 *
 * - The PM bookings and the scheduled appointments are `['maintenance',{status:'pm_booking'}]` and
 *   `['maintenance',{status:'scheduled'}]`, each paged with its own "load more" and "N of M" from
 *   `meta.statusCounts` (TW8: no queue stops silently at its first 100); booking one is a `PATCH` to
 *   `scheduled` with the date, time, garage and its map pin (Go validates the transition and asks the
 *   driver's app to remind them).
 * - The history is `['maintenance',{type,status}]` (`GET /v1/maintenance`, 100 per page, newest first)
 *   through TanStack Table + Virtual (W9): the Firestore page read every record and every truck and
 *   rendered every row (`maintenance.ts:72-104`, `MaintenanceOverview.tsx:515`).
 * - The counters of active jobs and bookings are the server's `meta.statusCounts`; the cost cards cover
 *   the loaded records (`displayCostThb`, the server's rule).
 * Writes and SSE `maintenance.*` (tenant:{tid}:fleet) make `['maintenance']` stale. Nothing here renders a
 * key of the split `accounting` namespace (this route is outside that group).
 */
import { useMemo, useState } from "react";
import dynamic from "next/dynamic";
import Link from "next/link";
import { useInfiniteQuery } from "@tanstack/react-query";
import { createColumnHelper } from "@tanstack/react-table";
import { Activity, BellRing, CalendarClock, DollarSign, FileText, Loader2, MapPin, RotateCw, Wrench } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { PageHeader } from "@/components/page-header";
import { apiErrorText } from "@/lib/apiError";
import { LoadMore } from "@/components/load-more";
import { VirtualTable } from "@/components/virtual-table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

import {
    MAINTENANCE_STATUSES,
    maintenanceCostTotals,
    maintenanceQuery,
    maintenanceRows,
    maintenanceStatusCounts,
    remindMaintenance,
    updateMaintenance,
    type MaintenanceRecordDTO,
} from "../../api/maintenance.go";
import { ApiErrorAlert, formatDay, formatThb, SearchPicker, type PickValue } from "@/features/accounting/components/go/shared";

const LocationPicker = dynamic(() => import("@/components/map/LocationPicker"), { ssr: false });

const col = createColumnHelper<MaintenanceRecordDTO>();

interface Draft {
    date: string;
    time: string;
    location: string;
    lat?: number;
    lng?: number;
}

const STATUS_KEY: Record<string, string> = {
    pm_booking: "maintenance.pmBookingBadge",
    scheduled: "maintenance.scheduledBadge",
    in_progress: "maintenance.form.inProgress",
    completed: "maintenance.form.completed",
    cancelled: "maintenance.form.cancelled",
};

function PendingQueue() {
    const { t } = useLanguage();
    const booking = useInfiniteQuery(maintenanceQuery({ status: "pm_booking" }));
    const scheduled = useInfiniteQuery(maintenanceQuery({ status: "scheduled" }));
    const rows = [...maintenanceRows(booking.data?.pages), ...maintenanceRows(scheduled.data?.pages)];
    const [drafts, setDrafts] = useState<Record<string, Draft>>({});
    const [mapFor, setMapFor] = useState<string | null>(null);
    const [busy, setBusy] = useState<string | null>(null);
    const draftOf = (id: string): Draft => drafts[id] ?? { date: "", time: "09:00", location: "" };
    const patch = (id: string, p: Partial<Draft>) => setDrafts((d) => ({ ...d, [id]: { ...draftOf(id), ...p } }));

    const book = async (r: MaintenanceRecordDTO) => {
        const d = draftOf(r.id);
        if (!d.date || !d.time.trim() || !d.location.trim()) return toast.error(t("maintenance.bookingValidation"));
        if (d.lat == null || d.lng == null) return toast.error(t("maintenance.bookingNeedMapPin"));
        setBusy(r.id);
        try {
            await updateMaintenance(r, {
                status: "scheduled",
                startDate: d.date,
                appointmentTime: d.time.trim(),
                provider: d.location.trim(),
                providerLat: d.lat,
                providerLng: d.lng,
            });
            toast.success(t("maintenance.bookingSaved"));
        } catch (e) {
            toast.error(apiErrorText(e, t));
        } finally {
            setBusy(null);
        }
    };
    const remind = async (r: MaintenanceRecordDTO) => {
        setBusy(r.id);
        try {
            await remindMaintenance(r.id);
            toast.success(t("maintenance.go.reminderSent"));
        } catch (e) {
            toast.error(apiErrorText(e, t));
        } finally {
            setBusy(null);
        }
    };

    if (rows.length === 0) return null;
    return (
        <Card className="overflow-hidden">
            <CardHeader className="space-y-1">
                <CardTitle className="flex items-center gap-2">
                    <CalendarClock className="h-5 w-5" /> {t("maintenance.pendingSectionTitle")}
                </CardTitle>
                <CardDescription>{t("maintenance.pendingSectionSubtitle")}</CardDescription>
            </CardHeader>
            <CardContent className="overflow-x-auto p-0">
                <Table>
                    <TableHeader>
                        <TableRow>
                            <TableHead>{t("maintenance.col.triggerDate")}</TableHead>
                            <TableHead>{t("maintenance.table.vehicle")}</TableHead>
                            <TableHead className="text-right">{t("maintenance.form.odometer")}</TableHead>
                            <TableHead>{t("maintenance.table.status")}</TableHead>
                            <TableHead>{t("maintenance.col.appointmentDate")}</TableHead>
                            <TableHead>{t("maintenance.col.time")}</TableHead>
                            <TableHead className="min-w-[220px]">{t("maintenance.col.place")}</TableHead>
                            <TableHead className="text-right">{t("maintenance.table.actions")}</TableHead>
                        </TableRow>
                    </TableHeader>
                    <TableBody>
                        {rows.map((r) => {
                            const d = draftOf(r.id);
                            const isBooking = r.status === "pm_booking";
                            return (
                                <TableRow key={r.id}>
                                    <TableCell className="whitespace-nowrap font-mono text-xs">{formatDay(r.createdAt, "dd MMM yyyy")}</TableCell>
                                    <TableCell>
                                        <div className="font-semibold">{r.licensePlate}</div>
                                        <div className="text-xs text-muted-foreground">{r.truckBrand}</div>
                                    </TableCell>
                                    <TableCell className="text-right font-semibold">{r.currentMileage?.toLocaleString() ?? "—"}</TableCell>
                                    <TableCell>
                                        <Badge variant="outline">{t(STATUS_KEY[r.status] ?? "", r.status)}</Badge>
                                    </TableCell>
                                    <TableCell>
                                        {isBooking ? <Input type="date" className="h-9" value={d.date} onChange={(e) => patch(r.id, { date: e.target.value })} /> : <span className="font-mono text-sm">{r.startDate}</span>}
                                    </TableCell>
                                    <TableCell>
                                        {isBooking ? <Input type="time" className="h-9" value={d.time} onChange={(e) => patch(r.id, { time: e.target.value })} /> : <span className="font-mono text-sm">{r.appointmentTime ?? "—"}</span>}
                                    </TableCell>
                                    <TableCell>
                                        {isBooking ? (
                                            <div className="flex flex-col gap-2">
                                                <Input className="h-9" placeholder={t("maintenance.form.enterGarage")} value={d.location} onChange={(e) => patch(r.id, { location: e.target.value })} />
                                                <Button type="button" variant="outline" size="sm" className="h-8 justify-start" onClick={() => setMapFor(r.id)}>
                                                    <MapPin className="mr-1 h-3.5 w-3.5" />
                                                    {t("maintenance.bookingPickOnMap")}
                                                </Button>
                                                <span className={`text-xs ${d.lat != null ? "font-medium text-success" : "text-muted-foreground"}`}>
                                                    {d.lat != null ? t("maintenance.bookingCoordsOk") : t("maintenance.bookingNoCoordsYet")}
                                                </span>
                                            </div>
                                        ) : (
                                            <span className="block text-sm">{r.provider ?? "—"}</span>
                                        )}
                                    </TableCell>
                                    <TableCell className="text-right">
                                        {isBooking ? (
                                            <Button size="sm" onClick={() => void book(r)} disabled={busy !== null}>
                                                {busy === r.id ? <Loader2 className="h-4 w-4 animate-spin" /> : t("maintenance.saveBooking")}
                                            </Button>
                                        ) : (
                                            <Button size="sm" variant="outline" onClick={() => void remind(r)} disabled={busy !== null}>
                                                {busy === r.id ? <Loader2 className="h-4 w-4 animate-spin" /> : <BellRing className="mr-1 h-4 w-4" />}
                                                {t("maintenance.go.remind")}
                                            </Button>
                                        )}
                                    </TableCell>
                                </TableRow>
                            );
                        })}
                    </TableBody>
                </Table>
                {[
                    { q: booking, status: "pm_booking" },
                    { q: scheduled, status: "scheduled" },
                ].map(({ q, status }) => {
                    const loaded = maintenanceRows(q.data?.pages).length;
                    const total = maintenanceStatusCounts(q.data?.pages)[status] ?? loaded;
                    return q.hasNextPage ? (
                        <div key={status} className="space-y-1 border-t p-3" data-testid={`pending-more-${status}`}>
                            <p className="text-xs font-medium text-muted-foreground">
                                {t("maintenance.go.queueLoaded", { status: t(STATUS_KEY[status] ?? "", status), loaded, total })}
                            </p>
                            <LoadMore
                                loaded={loaded}
                                hasNextPage={q.hasNextPage}
                                isFetchingNextPage={q.isFetchingNextPage}
                                error={q.isFetchNextPageError}
                                onLoadMore={() => void q.fetchNextPage()}
                            />
                        </div>
                    ) : null;
                })}
            </CardContent>
            <Dialog open={mapFor !== null} onOpenChange={(o) => !o && setMapFor(null)}>
                <DialogContent className="sm:max-w-2xl">
                    <DialogHeader>
                        <DialogTitle>{t("maintenance.bookingMapDialogTitle")}</DialogTitle>
                    </DialogHeader>
                    {mapFor ? (
                        <LocationPicker
                            value={draftOf(mapFor).lat != null && draftOf(mapFor).lng != null ? { lat: draftOf(mapFor).lat!, lng: draftOf(mapFor).lng! } : undefined}
                            onChange={(pos) => patch(mapFor, { lat: pos.lat, lng: pos.lng })}
                        />
                    ) : null}
                    <DialogFooter>
                        <Button type="button" onClick={() => setMapFor(null)}>
                            {t("maintenance.bookingMapDone")}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </Card>
    );
}

export function MaintenanceOverviewPage() {
    const { t } = useLanguage();
    const [type, setType] = useState<"all" | "PM" | "CM">("all");
    const [status, setStatus] = useState<string>("all");
    const [adding, setAdding] = useState(false);
    const [truck, setTruck] = useState<PickValue | null>(null);
    const filter = { type, status };
    const list = useInfiniteQuery(maintenanceQuery(filter));
    const all = useInfiniteQuery(maintenanceQuery({}));
    const rows = maintenanceRows(list.data?.pages);
    const counts = maintenanceStatusCounts(all.data?.pages);
    const totals = useMemo(() => maintenanceCostTotals(rows), [rows]);
    const pct = (part: number) => (totals.total > 0 ? ((part / totals.total) * 100).toFixed(1) : "0.0");

    const columns = useMemo(
        () => [
            col.accessor("startDate", { header: () => t("maintenance.table.date"), cell: (c) => formatDay(c.getValue()), meta: { cellClassName: "whitespace-nowrap text-xs" } }),
            col.display({
                id: "vehicle",
                header: () => t("maintenance.table.vehicle"),
                cell: ({ row: { original: r } }) => (
                    <div>
                        <div className="font-semibold">{r.licensePlate}</div>
                        <div className="text-xs text-muted-foreground">{r.truckBrand}</div>
                    </div>
                ),
            }),
            col.accessor("type", {
                header: () => t("maintenance.table.type"),
                cell: (c) => <Badge variant="outline">{c.getValue() === "PM" ? t("maintenance.form.pm") : t("maintenance.form.cm")}</Badge>,
            }),
            col.accessor("serviceType", { header: () => t("maintenance.table.service"), meta: { cellClassName: "max-w-[220px] truncate text-xs" } }),
            col.accessor("status", { header: () => t("maintenance.table.status"), cell: (c) => <Badge variant="secondary">{t(STATUS_KEY[c.getValue()] ?? "", c.getValue())}</Badge> }),
            col.accessor("costLaborThb", { header: () => t("maintenance.table.labor"), cell: (c) => formatThb(c.getValue()), meta: { headerClassName: "text-right", cellClassName: "text-right text-xs" } }),
            col.accessor("costPartsThb", { header: () => t("maintenance.table.parts"), cell: (c) => formatThb(c.getValue()), meta: { headerClassName: "text-right", cellClassName: "text-right text-xs" } }),
            col.accessor("displayCostThb", { header: () => t("maintenance.table.total"), cell: (c) => formatThb(c.getValue()), meta: { headerClassName: "text-right", cellClassName: "text-right font-semibold" } }),
            col.display({
                id: "actions",
                header: () => t("maintenance.table.actions"),
                cell: ({ row: { original: r } }) =>
                    r.truckId ? (
                        <Button asChild size="sm" variant="ghost">
                            <Link href={`/app/trucks/maintenance?id=${encodeURIComponent(r.truckId)}`} prefetch={false}>
                                {t("maintenance.manageUpdate")}
                            </Link>
                        </Button>
                    ) : null,
            }),
        ],
        [t]
    );

    return (
        <div className="flex flex-col gap-section">
            <PageHeader
                title={t("nav.maintenance")}
                description={t("maintenance.subtitle")}
                actions={
                    <>
                        <Button
                            variant="outline"
                            onClick={() => {
                                void list.refetch();
                                void all.refetch();
                            }}
                        >
                            <RotateCw className="mr-1 h-4 w-4" aria-hidden /> {t("maintenance.refresh")}
                        </Button>
                        <Button onClick={() => setAdding(true)}>
                            <Wrench className="mr-1 h-4 w-4" aria-hidden /> {t("maintenance.form.addRecord")}
                        </Button>
                    </>
                }
            />

            <div className="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-4">
                <Card>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium">{t("maintenance.totalExpenses")}</CardTitle>
                        <DollarSign className="h-4 w-4 text-muted-foreground" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-semibold tabular-nums">{formatThb(totals.total)}</div>
                        <p className="text-xs text-muted-foreground">{list.hasNextPage ? t("maintenance.go.ofLoaded") : t("maintenance.lifetimeSpend")}</p>
                    </CardContent>
                </Card>
                <Card>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium text-muted-foreground">{t("maintenance.pmCosts")}</CardTitle>
                        <FileText className="h-4 w-4 text-muted-foreground" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-semibold tabular-nums">{formatThb(totals.pm)}</div>
                        <p className="mt-1 text-xs text-muted-foreground">
                            {pct(totals.pm)}% {t("maintenance.ofTotal")}
                        </p>
                    </CardContent>
                </Card>
                <Card>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium text-muted-foreground">{t("maintenance.cmCosts")}</CardTitle>
                        <Wrench className="h-4 w-4 text-muted-foreground" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-semibold tabular-nums">{formatThb(totals.cm)}</div>
                        <p className="mt-1 text-xs text-muted-foreground">
                            {pct(totals.cm)}% {t("maintenance.ofTotal")}
                        </p>
                    </CardContent>
                </Card>
                <Card>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium">{t("maintenance.activeJobs")}</CardTitle>
                        <Activity className="h-4 w-4 text-muted-foreground" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-semibold tabular-nums">{counts.in_progress ?? 0}</div>
                        <p className="text-xs text-muted-foreground">{t("maintenance.vehiclesInShop")}</p>
                        {(counts.pm_booking ?? 0) > 0 ? (
                            <p className="mt-2 text-xs text-muted-foreground">
                                <span className="text-lg font-semibold tabular-nums text-foreground">{counts.pm_booking}</span> {t("maintenance.pmBookingPending")}
                            </p>
                        ) : null}
                        {(counts.scheduled ?? 0) > 0 ? (
                            <p className="text-xs text-muted-foreground">
                                <span className="text-lg font-semibold tabular-nums text-foreground">{counts.scheduled}</span> {t("maintenance.scheduledBadge")}
                            </p>
                        ) : null}
                    </CardContent>
                </Card>
            </div>

            <PendingQueue />

            <Card>
                <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3">
                    <CardTitle>{t("maintenance.serviceHistory")}</CardTitle>
                    <div className="flex flex-wrap gap-2">
                        <Select value={type} onValueChange={(v) => setType(v as "all" | "PM" | "CM")}>
                            <SelectTrigger className="w-40" aria-label={t("maintenance.filterType")}>
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="all">{t("maintenance.allTypes")}</SelectItem>
                                <SelectItem value="PM">{t("maintenance.form.pm")}</SelectItem>
                                <SelectItem value="CM">{t("maintenance.form.cm")}</SelectItem>
                            </SelectContent>
                        </Select>
                        <Select value={status} onValueChange={setStatus}>
                            <SelectTrigger className="w-44" aria-label={t("maintenance.table.status")}>
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="all">{t("maintenance.go.allStatuses")}</SelectItem>
                                {MAINTENANCE_STATUSES.map((s) => (
                                    <SelectItem key={s} value={s}>
                                        {t(STATUS_KEY[s] ?? "", s)}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>
                </CardHeader>
                <CardContent className="space-y-3">
                    <ApiErrorAlert error={list.error} />
                    <VirtualTable
                        data={rows}
                        columns={columns}
                        getRowId={(r) => r.id}
                        aria-label={t("maintenance.serviceHistory")}
                        infinite={list}
                        loading={list.isPending}
                        error={list.isError}
                        emptyState={<p className="py-8 text-center text-sm text-muted-foreground">{t("maintenance.noRecords")}</p>}
                        resetScrollKey={JSON.stringify(filter)}
                    />
                    <LoadMore
                        loaded={rows.length}
                        hasNextPage={list.hasNextPage}
                        isFetchingNextPage={list.isFetchingNextPage}
                        error={list.isFetchNextPageError}
                        onLoadMore={() => void list.fetchNextPage()}
                    />
                </CardContent>
            </Card>

            <Dialog open={adding} onOpenChange={setAdding}>
                <DialogContent className="sm:max-w-md">
                    <DialogHeader>
                        <DialogTitle>{t("maintenance.form.addRecord")}</DialogTitle>
                    </DialogHeader>
                    <p className="text-sm text-muted-foreground">{t("maintenance.go.pickTruck")}</p>
                    <SearchPicker kind="truck" value={truck} onChange={setTruck} placeholder={t("maintenance.table.vehicle")} className="w-full" />
                    <DialogFooter>
                        <Button asChild disabled={!truck}>
                            <Link href={truck ? `/app/trucks/maintenance?id=${encodeURIComponent(truck.id)}` : "#"} prefetch={false} aria-disabled={!truck}>
                                {t("maintenance.go.openTruck")}
                            </Link>
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </div>
    );
}
