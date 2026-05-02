"use client";

import { useEffect, useState, useCallback } from "react";
import dynamic from "next/dynamic";
import { useRouter } from "next/navigation";
import { MaintenanceDashboardData, getMaintenanceOverview, updateMaintenanceRecord } from "@/features/maintenance/api/maintenance";
import { useLanguage } from "@/context/language";
import { useAuth } from "@/context/auth";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { MoreHorizontal, Wrench, Search, Loader2, DollarSign, Activity, CalendarClock, FileText, RotateCw, MapPin } from "lucide-react";
import {
    Dialog,
    DialogContent,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from "@/components/ui/dialog";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuLabel,
    DropdownMenuTrigger
} from "@/components/ui/dropdown-menu";
import Link from "next/link";
import { format } from "date-fns";
import { toast } from "sonner";
import { Select, SelectTrigger, SelectValue, SelectContent, SelectItem } from "@/components/ui/select";
import { MaintenanceFormWrapper } from "./maintenance/MaintenanceFormWrapper";
import { maintenanceDisplayCost } from "@/features/maintenance/utils/maintenanceDisplayCost";

const LocationPicker = dynamic(() => import("@/components/map/LocationPicker"), { ssr: false });

type BookingDraft = {
    date: string;
    time: string;
    location: string;
    providerLat?: number;
    providerLng?: number;
};

export default function MaintenanceOverview() {
    const [records, setRecords] = useState<MaintenanceDashboardData[]>([]);
    const [loading, setLoading] = useState(true);
    const [view, setView] = useState<"list" | "form">("list");
    const [searchTerm, setSearchTerm] = useState("");
    const [typeFilter, setTypeFilter] = useState<"all" | "PM" | "CM">("all");
    const [bookingDrafts, setBookingDrafts] = useState<Record<string, BookingDraft>>({});
    const [savingBookingId, setSavingBookingId] = useState<string | null>(null);
    const [mapDialogRecordId, setMapDialogRecordId] = useState<string | null>(null);
    const { t } = useLanguage();
    const router = useRouter();
    const auth = useAuth();

    const loadData = async () => {
        setLoading(true);
        const data = await getMaintenanceOverview();
        setRecords(data);
        setLoading(false);
    };

    useEffect(() => {
        loadData();
    }, []);

    useEffect(() => {
        setBookingDrafts((prev) => {
            const next = { ...prev };
            for (const r of records.filter((x) => x.status === "PM Booking")) {
                if (!next[r.id]) {
                    const at = (r as MaintenanceDashboardData & { appointmentTime?: string }).appointmentTime;
                    const ext = r as MaintenanceDashboardData & {
                        appointmentTime?: string;
                        providerLat?: number;
                        providerLng?: number;
                    };
                    const draft: BookingDraft = {
                        date: r.startDate?.slice(0, 10) || new Date().toISOString().slice(0, 10),
                        time: at || "09:00",
                        location: r.provider || "",
                    };
                    if (typeof ext.providerLat === "number" && typeof ext.providerLng === "number") {
                        draft.providerLat = ext.providerLat;
                        draft.providerLng = ext.providerLng;
                    }
                    next[r.id] = draft;
                }
            }
            return next;
        });
    }, [records]);

    const patchBookingDraft = useCallback((id: string, patch: Partial<BookingDraft>) => {
        setBookingDrafts((p) => {
            const cur = p[id] ?? { date: "", time: "09:00", location: "" };
            return { ...p, [id]: { ...cur, ...patch } };
        });
    }, []);

    const saveBooking = useCallback(
        async (record: MaintenanceDashboardData) => {
            const uid = auth?.currentUser?.uid;
            if (!uid) {
                toast.error(t("maintenance.signInRequired"));
                return;
            }
            const d = bookingDrafts[record.id];
            if (!d?.date || !String(d.time || "").trim() || !String(d.location || "").trim()) {
                toast.error(t("maintenance.bookingValidation"));
                return;
            }
            if (
                d.providerLat == null ||
                d.providerLng == null ||
                Number.isNaN(d.providerLat) ||
                Number.isNaN(d.providerLng)
            ) {
                toast.error(t("maintenance.bookingNeedMapPin"));
                return;
            }
            setSavingBookingId(record.id);
            try {
                await updateMaintenanceRecord(
                    record.id,
                    {
                        truckId: record.truckId,
                        status: "Scheduled",
                        startDate: d.date,
                        appointmentTime: d.time.trim(),
                        provider: d.location.trim(),
                        providerLat: d.providerLat,
                        providerLng: d.providerLng,
                    },
                    uid
                );
                toast.success(t("maintenance.bookingSaved"));
                await loadData();
            } catch (e) {
                console.error(e);
                toast.error(e instanceof Error ? e.message : "Save failed");
            } finally {
                setSavingBookingId(null);
            }
        },
        [auth?.currentUser?.uid, bookingDrafts, t]
    );

    const totalRecords = records.length;
    const totalCost = records.reduce((sum, r) => sum + maintenanceDisplayCost(r), 0);
    const pmCost = records.filter(r => r.type === "PM").reduce((sum, r) => sum + maintenanceDisplayCost(r), 0);
    const cmCost = records.filter(r => r.type === "CM").reduce((sum, r) => sum + maintenanceDisplayCost(r), 0);
    const pctPmOfTotal = totalCost > 0 ? ((pmCost / totalCost) * 100).toFixed(1) : "0.0";
    const pctCmOfTotal = totalCost > 0 ? ((cmCost / totalCost) * 100).toFixed(1) : "0.0";
    const activeJobs = records.filter(r => r.status === "in_progress").length;
    const pmNeedBooking = records.filter((r) => r.status === "PM Booking").length;
    const scheduledAwaiting = records.filter((r) => r.status === "Scheduled").length;
    const pendingQueue = records.filter((r) => r.status === "PM Booking" || r.status === "Scheduled");
    const pendingQueueSorted = [...pendingQueue].sort((a, b) => {
        const o = (r: MaintenanceDashboardData) => (r.status === "PM Booking" ? 0 : 1);
        if (o(a) !== o(b)) return o(a) - o(b);
        const ca = (a.createdAt as { toDate?: () => Date } | undefined)?.toDate?.()?.getTime() ?? 0;
        const cb = (b.createdAt as { toDate?: () => Date } | undefined)?.toDate?.()?.getTime() ?? 0;
        return cb - ca;
    });

    const historyRecords = records.filter((r) => r.status !== "PM Booking" && r.status !== "Scheduled");

    const filteredRecords = historyRecords.filter((record) => {
        const matchesSearch =
            record.truckLicensePlate.toLowerCase().includes(searchTerm.toLowerCase()) ||
            record.truckBrand.toLowerCase().includes(searchTerm.toLowerCase()) ||
            record.serviceType.toLowerCase().includes(searchTerm.toLowerCase());

        const matchesType = typeFilter === "all" || record.type === typeFilter;

        return matchesSearch && matchesType;
    });

    const sortedTableRecords = [...filteredRecords].sort((a, b) => {
        const da = a.startDate ? new Date(a.startDate).getTime() : 0;
        const db = b.startDate ? new Date(b.startDate).getTime() : 0;
        return db - da;
    });

    const rowDisplayDate = (record: MaintenanceDashboardData): Date => {
        if (record.startDate) {
            const d = new Date(record.startDate);
            if (!Number.isNaN(d.getTime())) return d;
        }
        const ca = record.createdAt as { toDate?: () => Date } | undefined;
        if (ca && typeof ca.toDate === "function") return ca.toDate();
        return new Date();
    };

    if (loading) {
        return <div className="flex h-screen items-center justify-center"><Loader2 className="h-8 w-8 animate-spin text-primary" /></div>;
    }

    if (view === "form") {
        return (
            <div className="container mx-auto p-6 space-y-8 max-w-[1400px]">
                <MaintenanceFormWrapper onSuccess={() => { setView("list"); loadData(); }} onCancel={() => setView("list")} />
            </div>
        );
    }

    return (
        <div className="container mx-auto p-6 space-y-8 max-w-[1400px]">
            <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">{t("maintenance.title")}</h1>
                    <p className="text-muted-foreground mt-1">{t("maintenance.subtitle")}</p>
                </div>
                <div className="flex gap-2">
                    <Button type="button" variant="outline" onClick={() => loadData()} className="shadow-sm">
                        <RotateCw className="w-4 h-4 mr-1" /> {t("maintenance.refresh")}
                    </Button>
                    <Button onClick={() => setView("form")} className="shadow-md bg-blue-600 hover:bg-blue-700 text-white">
                        <Wrench className="w-4 h-4 mr-1" /> {t("maintenance.form.addRecord")}
                    </Button>
                </div>
            </div>

            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
                <Card>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium">{t("maintenance.totalExpenses")}</CardTitle>
                        <DollarSign className="h-4 w-4 text-muted-foreground" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold">฿{totalCost.toLocaleString()}</div>
                        <p className="text-xs text-muted-foreground">{t("maintenance.lifetimeSpend")}</p>
                        <div className="mt-2 pt-2 border-t text-xs flex justify-between items-center text-muted-foreground">
                            <span>{t("maintenance.avgPerRecord")}:</span>
                            <span className="font-semibold text-foreground">฿{totalRecords > 0 ? Math.round(totalCost / totalRecords).toLocaleString() : 0}</span>
                        </div>
                    </CardContent>
                </Card>

                <Card className="border-l-4 border-l-blue-500">
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium text-blue-700">{t("maintenance.pmCosts")}</CardTitle>
                        <FileText className="h-4 w-4 text-blue-500" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold text-blue-700">฿{pmCost.toLocaleString()}</div>
                        <p className="text-xs text-blue-600 font-medium">{t("maintenance.preventiveMaintenance")}</p>
                        <p className="text-xs text-muted-foreground mt-1">
                            {pctPmOfTotal}% {t("maintenance.ofTotal")}
                        </p>
                    </CardContent>
                </Card>

                <Card className="border-l-4 border-l-orange-500">
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium text-orange-700">{t("maintenance.cmCosts")}</CardTitle>
                        <Wrench className="h-4 w-4 text-orange-500" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold text-orange-700">฿{cmCost.toLocaleString()}</div>
                        <p className="text-xs text-orange-600 font-medium">{t("maintenance.correctiveRepairs")}</p>
                        <p className="text-xs text-muted-foreground mt-1">
                            {pctCmOfTotal}% {t("maintenance.ofTotal")}
                        </p>
                    </CardContent>
                </Card>

                <Card className={activeJobs > 0 || pmNeedBooking > 0 || scheduledAwaiting > 0 ? "animate-pulse border-yellow-400 border" : ""}>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium">{t("maintenance.activeJobs")}</CardTitle>
                        <Activity className="h-4 w-4 text-green-500" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold">{activeJobs}</div>
                        <p className="text-xs text-muted-foreground">{t("maintenance.vehiclesInShop")}</p>
                        {(pmNeedBooking > 0 || scheduledAwaiting > 0) && (
                            <div className="mt-3 pt-3 border-t space-y-2 text-xs">
                                {pmNeedBooking > 0 && (
                                    <div>
                                        <span className="font-bold text-amber-700 text-lg">{pmNeedBooking}</span>
                                        <span className="text-amber-800 ml-2">{t("maintenance.pmBookingPending")}</span>
                                    </div>
                                )}
                                {scheduledAwaiting > 0 && (
                                    <div>
                                        <span className="font-bold text-blue-700 text-lg">{scheduledAwaiting}</span>
                                        <span className="text-blue-800 ml-2">{t("maintenance.scheduledBadge")}</span>
                                    </div>
                                )}
                                <p className="text-muted-foreground">{t("maintenance.pmBookingPendingHint")}</p>
                            </div>
                        )}
                        {activeJobs > 0 && (
                            <div className="mt-2 text-xs text-orange-600 font-medium">
                                {t("maintenance.requiresAttention")}
                            </div>
                        )}
                    </CardContent>
                </Card>
            </div>

            {pendingQueueSorted.length > 0 && (
                <Card className="border-t-4 border-t-amber-500 shadow-md overflow-hidden">
                    <CardHeader className="bg-amber-500/10 space-y-1">
                        <CardTitle className="flex items-center gap-2 text-amber-900">
                            <CalendarClock className="h-5 w-5" /> {t("maintenance.pendingSectionTitle")}
                        </CardTitle>
                        <CardDescription>{t("maintenance.pendingSectionSubtitle")}</CardDescription>
                    </CardHeader>
                    <CardContent className="p-0 overflow-x-auto">
                        <Table>
                            <TableHeader>
                                <TableRow className="hover:bg-transparent">
                                    <TableHead className="min-w-[100px]">{t("maintenance.col.triggerDate")}</TableHead>
                                    <TableHead className="min-w-[120px]">{t("maintenance.table.vehicle")}</TableHead>
                                    <TableHead className="text-right min-w-[90px]">{t("maintenance.form.odometer")}</TableHead>
                                    <TableHead>{t("maintenance.table.status")}</TableHead>
                                    <TableHead className="min-w-[130px]">{t("maintenance.col.appointmentDate")}</TableHead>
                                    <TableHead className="min-w-[100px]">{t("maintenance.col.time")}</TableHead>
                                    <TableHead className="min-w-[220px]">{t("maintenance.col.place")}</TableHead>
                                    <TableHead className="text-right min-w-[140px]">{t("maintenance.table.actions")}</TableHead>
                                </TableRow>
                            </TableHeader>
                            <TableBody>
                                {pendingQueueSorted.map((record) => {
                                    const draft = bookingDrafts[record.id];
                                    const created = rowDisplayDate(record);
                                    const ext = record as MaintenanceDashboardData & { appointmentTime?: string };
                                    return (
                                        <TableRow
                                            key={record.id}
                                            className={record.status === "PM Booking" ? "bg-amber-50/60" : "bg-sky-50/50"}
                                        >
                                            <TableCell className="font-mono text-xs whitespace-nowrap">{format(created, "dd MMM yyyy")}</TableCell>
                                            <TableCell>
                                                <div className="font-semibold">{record.truckLicensePlate}</div>
                                                <div className="text-xs text-muted-foreground">{record.truckBrand}</div>
                                            </TableCell>
                                            <TableCell className="text-right font-bold">{record.currentMileage?.toLocaleString() ?? "—"}</TableCell>
                                            <TableCell>
                                                {record.status === "PM Booking" ? (
                                                    <Badge variant="outline" className="text-amber-800 border-amber-300 bg-amber-100">
                                                        {t("maintenance.pmBookingBadge")}
                                                    </Badge>
                                                ) : (
                                                    <Badge variant="outline" className="text-blue-800 border-blue-300 bg-blue-100">
                                                        {t("maintenance.scheduledBadge")}
                                                    </Badge>
                                                )}
                                            </TableCell>
                                            <TableCell>
                                                {record.status === "PM Booking" ? (
                                                    <Input
                                                        type="date"
                                                        className="h-9"
                                                        value={draft?.date ?? ""}
                                                        onChange={(e) =>
                                                            patchBookingDraft(record.id, { date: e.target.value })
                                                        }
                                                    />
                                                ) : (
                                                    <span className="text-sm font-mono">{record.startDate?.slice(0, 10) ?? "—"}</span>
                                                )}
                                            </TableCell>
                                            <TableCell>
                                                {record.status === "PM Booking" ? (
                                                    <Input
                                                        type="time"
                                                        className="h-9"
                                                        value={draft?.time ?? "09:00"}
                                                        onChange={(e) =>
                                                            patchBookingDraft(record.id, { time: e.target.value })
                                                        }
                                                    />
                                                ) : (
                                                    <span className="text-sm font-mono">{ext.appointmentTime ?? "—"}</span>
                                                )}
                                            </TableCell>
                                            <TableCell className="min-w-[200px]">
                                                {record.status === "PM Booking" ? (
                                                    <div className="flex flex-col gap-2">
                                                        <Input
                                                            className="h-9"
                                                            placeholder={t("maintenance.form.enterGarage")}
                                                            value={draft?.location ?? ""}
                                                            onChange={(e) =>
                                                                patchBookingDraft(record.id, {
                                                                    location: e.target.value,
                                                                })
                                                            }
                                                        />
                                                        <Button
                                                            type="button"
                                                            variant="outline"
                                                            size="sm"
                                                            className="h-8 shrink-0 justify-start"
                                                            onClick={() => setMapDialogRecordId(record.id)}
                                                        >
                                                            <MapPin className="h-3.5 w-3.5 mr-1 shrink-0" />
                                                            {t("maintenance.bookingPickOnMap")}
                                                        </Button>
                                                        {draft?.providerLat != null &&
                                                        draft?.providerLng != null ? (
                                                            <span className="text-xs text-green-700 font-medium">
                                                                {t("maintenance.bookingCoordsOk")}
                                                            </span>
                                                        ) : (
                                                            <span className="text-xs text-muted-foreground">
                                                                {t("maintenance.bookingNoCoordsYet")}
                                                            </span>
                                                        )}
                                                    </div>
                                                ) : (
                                                    <div className="space-y-1">
                                                        <span className="text-sm block">{record.provider ?? "—"}</span>
                                                        {typeof record.providerLat === "number" &&
                                                        typeof record.providerLng === "number" ? (
                                                            <span className="text-xs font-mono text-muted-foreground">
                                                                {record.providerLat.toFixed(5)},{" "}
                                                                {record.providerLng.toFixed(5)}
                                                            </span>
                                                        ) : null}
                                                    </div>
                                                )}
                                            </TableCell>
                                            <TableCell className="text-right">
                                                <div className="flex flex-col sm:flex-row gap-2 justify-end">
                                                    {record.status === "PM Booking" && (
                                                        <Button
                                                            size="sm"
                                                            className="bg-amber-700 hover:bg-amber-800 text-white"
                                                            disabled={savingBookingId === record.id}
                                                            onClick={(e) => {
                                                                e.stopPropagation();
                                                                void saveBooking(record);
                                                            }}
                                                        >
                                                            {savingBookingId === record.id ? (
                                                                <Loader2 className="h-4 w-4 animate-spin" />
                                                            ) : (
                                                                t("maintenance.saveBooking")
                                                            )}
                                                        </Button>
                                                    )}
                                                    <Button
                                                        size="sm"
                                                        variant="outline"
                                                        onClick={(e) => {
                                                            e.stopPropagation();
                                                            router.push(`/admin/trucks/maintenance?id=${record.truckId}`);
                                                        }}
                                                    >
                                                        <Wrench className="w-4 h-4 mr-1" /> {t("maintenance.manageUpdate")}
                                                    </Button>
                                                </div>
                                            </TableCell>
                                        </TableRow>
                                    );
                                })}
                            </TableBody>
                        </Table>
                    </CardContent>
                </Card>
            )}

            <Card>
                <CardHeader>
                    <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4">
                        <CardTitle>{t("maintenance.serviceHistory")}</CardTitle>
                        <div className="flex gap-2 w-full md:w-auto">
                            <Select value={typeFilter} onValueChange={(v: any) => setTypeFilter(v)}>
                                <SelectTrigger className="w-[150px]">
                                    <SelectValue placeholder={t("maintenance.filterType")} />
                                </SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="all">{t("maintenance.allTypes")}</SelectItem>
                                    <SelectItem value="PM">{t("maintenance.form.pm")}</SelectItem>
                                    <SelectItem value="CM">{t("maintenance.form.cm")}</SelectItem>
                                </SelectContent>
                            </Select>
                            <div className="relative flex-1 md:w-[300px]">
                                <Search className="absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
                                <Input
                                    placeholder={t("maintenance.searchPlaceholder")}
                                    className="pl-9"
                                    value={searchTerm}
                                    onChange={(e) => setSearchTerm(e.target.value)}
                                />
                            </div>
                        </div>
                    </div>
                </CardHeader>
                <CardContent>
                    <Table>
                        <TableHeader>
                            <TableRow>
                                <TableHead>{t("maintenance.table.date")}</TableHead>
                                <TableHead>{t("maintenance.table.vehicle")}</TableHead>
                                <TableHead>{t("maintenance.table.type")}</TableHead>
                                <TableHead>{t("maintenance.table.service")}</TableHead>
                                <TableHead className="text-right">{t("maintenance.table.labor")}</TableHead>
                                <TableHead className="text-right">{t("maintenance.table.parts")}</TableHead>
                                <TableHead className="text-right">{t("maintenance.table.total")}</TableHead>
                                <TableHead>{t("maintenance.table.status")}</TableHead>
                                <TableHead>{t("maintenance.table.truckStatus")}</TableHead>
                                <TableHead className="text-right">{t("maintenance.table.actions")}</TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {sortedTableRecords.map((record) => {
                                const rowTotal = maintenanceDisplayCost(record);
                                return (
                                <TableRow
                                    key={record.id}
                                    className="cursor-pointer hover:bg-muted/50"
                                    onClick={() => router.push(`/admin/trucks/maintenance?id=${record.truckId}`)}
                                >
                                    <TableCell className="whitespace-nowrap font-mono text-xs">
                                        {format(rowDisplayDate(record), "dd MMM yyyy")}
                                    </TableCell>
                                    <TableCell>
                                        <div className="font-semibold">{record.truckLicensePlate}</div>
                                        <div className="text-xs text-muted-foreground">{record.truckBrand}</div>
                                    </TableCell>
                                    <TableCell>
                                        <Badge variant={record.type === 'PM' ? "default" : "destructive"} className={record.type === 'PM' ? "bg-blue-600" : ""}>
                                            {record.type}
                                        </Badge>
                                    </TableCell>
                                    <TableCell className="max-w-[200px] truncate" title={record.serviceType}>
                                        {t(`maintenance.service.${record.serviceType}`) !== `maintenance.service.${record.serviceType}` ? t(`maintenance.service.${record.serviceType}`) : record.serviceType}
                                    </TableCell>
                                    <TableCell className="text-right text-muted-foreground">
                                        {record.costLabor ? `฿${record.costLabor.toLocaleString()}` : "-"}
                                    </TableCell>
                                    <TableCell className="text-right text-muted-foreground">
                                        {record.costParts ? `฿${record.costParts.toLocaleString()}` : "-"}
                                    </TableCell>
                                    <TableCell className="text-right font-bold">
                                        {rowTotal > 0 ? `฿${rowTotal.toLocaleString()}` : "—"}
                                    </TableCell>
                                    <TableCell>
                                        {record.status === "cancelled" ? (
                                            <Badge variant="outline" className="text-slate-600 border-slate-200">{t("maintenance.form.cancelled")}</Badge>
                                        ) : record.status === "completed" ? (
                                            <Badge variant="outline" className="text-green-600 border-green-200 bg-green-50">{t("maintenance.form.completed")}</Badge>
                                        ) : (
                                            <Badge variant="secondary" className="bg-yellow-100 text-yellow-800 hover:bg-yellow-200">{t("maintenance.form.inProgress")}</Badge>
                                        )}
                                    </TableCell>
                                    <TableCell>
                                        <Badge
                                            variant={record.truckStatus === 'maintenance' ? "destructive" : "outline"}
                                            className={record.truckStatus === 'active' ? "text-emerald-600 border-emerald-200 bg-emerald-50" : ""}
                                        >
                                            {t(record.truckStatus || "active")}
                                        </Badge>
                                    </TableCell>
                                    <TableCell className="text-right">
                                        <DropdownMenu>
                                            <DropdownMenuTrigger asChild>
                                                <Button variant="ghost" className="h-8 w-8 p-0">
                                                    <span className="sr-only">{t("maintenance.history.openMenu")}</span>
                                                    <MoreHorizontal className="h-4 w-4" />
                                                </Button>
                                            </DropdownMenuTrigger>
                                            <DropdownMenuContent align="end">
                                                <DropdownMenuLabel>{t("maintenance.table.actions")}</DropdownMenuLabel>
                                                <DropdownMenuItem asChild>
                                                    <Link href={`/admin/trucks/maintenance?id=${record.truckId}`} prefetch={false} className="flex items-center cursor-pointer">
                                                        <Wrench className="mr-2 h-4 w-4" />
                                                        {t("maintenance.manageUpdate")}
                                                    </Link>
                                                </DropdownMenuItem>
                                            </DropdownMenuContent>
                                        </DropdownMenu>
                                    </TableCell>
                                </TableRow>
                                );
                            })}
                            {sortedTableRecords.length === 0 && (
                                <TableRow>
                                    <TableCell colSpan={9} className="h-24 text-center">
                                        {t("maintenance.noRecords")}
                                    </TableCell>
                                </TableRow>
                            )}
                        </TableBody>
                    </Table>
                </CardContent>
            </Card>

            <Dialog
                open={mapDialogRecordId != null}
                onOpenChange={(open) => {
                    if (!open) setMapDialogRecordId(null);
                }}
            >
                <DialogContent className="sm:max-w-lg max-h-[90vh] overflow-y-auto">
                    <DialogHeader>
                        <DialogTitle>{t("maintenance.bookingMapDialogTitle")}</DialogTitle>
                    </DialogHeader>
                    {mapDialogRecordId ? (
                        <LocationPicker
                            value={
                                bookingDrafts[mapDialogRecordId]?.providerLat != null &&
                                bookingDrafts[mapDialogRecordId]?.providerLng != null
                                    ? {
                                          lat: bookingDrafts[mapDialogRecordId]!.providerLat!,
                                          lng: bookingDrafts[mapDialogRecordId]!.providerLng!,
                                      }
                                    : undefined
                            }
                            onChange={(pos) =>
                                patchBookingDraft(mapDialogRecordId, {
                                    providerLat: pos.lat,
                                    providerLng: pos.lng,
                                })
                            }
                        />
                    ) : null}
                    <DialogFooter>
                        <Button type="button" onClick={() => setMapDialogRecordId(null)}>
                            {t("maintenance.bookingMapDone")}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </div>
    );
}
