"use client";

import { useEffect, useState, useMemo } from "react";
import dynamic from "next/dynamic";
import { Plus, MapPin, Search, Pencil, ChevronLeft, ChevronRight, RefreshCw, Download, Route, MoreHorizontal, Link, AlertTriangle } from "lucide-react";
import * as XLSX from "xlsx";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from "@/components/ui/table";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuLabel,
    DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { HubDialog } from "../first-mile/hub-dialog";
import { PickupLocationImportDialog } from "./pickup-import-dialog";
import { collection, getDocs, doc, getDoc } from "firebase/firestore";
import { httpsCallable } from "firebase/functions";
import { db, functions } from "@/firebase/client";
import { useAuth } from "@/context/auth";
import { useLanguage } from "@/context/language";
import { COLLECTIONS } from "@/lib/collections";
import type { CustomerLinkKind, Hub, StationType } from "@/validate/hubSchema";
import { getCustomers, type CustomerData } from "@/features/customers/api/customers";
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select";
import { toast } from "sonner";

const SourcesMap = dynamic(() => import("@/components/map/SourcesMap"), {
    ssr: false,
    loading: () => (
        <div className="h-[360px] w-full rounded-md border bg-muted/30 flex items-center justify-center text-muted-foreground text-sm">
            Loading map...
        </div>
    ),
});

/** Display row: supports both new schema and legacy Firestore fields */
interface SourceRow extends Pick<Hub, "source_id" | "source_name_en" | "latitude" | "longitude" | "station_type"> {
    id?: string;
    /** Thai / legacy hubTHName — ค้นหาและแสดง (ฟอร์มแก้ไขยังใช้ source_name_en เป็นหลัก) */
    source_name_th?: string;
    linkedCustomerId?: string;
    customerLinkKind?: CustomerLinkKind;
    /** Denormalized สำหรับ export Excel — จาก customers lookup */
    linkedCustomerCode?: string;
    linkedCustomerName?: string;
    /** Driver เพิ่มจาก Mobile — Admin ควรกรอกพิกัดและข้อมูลให้ครบ */
    createdByDriver?: boolean;
}

/** Normalize to HUB or SOC; map legacy FM_HUB/LH_HUB → HUB, RETURN_CENTER → SOC */
function normalizeStationType(value: unknown): StationType {
    const v = String(value ?? "").toUpperCase();
    if (v === "SOC" || v === "RETURN_CENTER") return "SOC";
    return "HUB"; // HUB, FM_HUB, LH_HUB, or missing
}

function mapDocToSourceRow(doc: { id: string; data: Record<string, unknown> }): SourceRow {
    const data = doc.data;
    const source_name_en = (data.source_name_en ??
        data.hubName ??
        data.station_name_en ??
        "") as string;
    const thRaw = (data.source_name_th ??
        data.hubTHName ??
        data.hub_th_name ??
        data.station_name_th ??
        "") as string;
    const source_name_th = String(thRaw).trim();
    const lk = data.linkedCustomerId;
    const linkedCustomerId = typeof lk === "string" && lk.trim() !== "" ? lk.trim() : undefined;
    const ck = data.customerLinkKind;
    const customerLinkKind: CustomerLinkKind | undefined =
        ck === "partner" || ck === "customer" ? ck : undefined;

    return {
        id: doc.id,
        source_id: (data.source_id ?? data.hubId ?? data.hubCode ?? "") as string,
        source_name_en,
        source_name_th: source_name_th.length > 0 ? source_name_th : undefined,
        latitude: (data.latitude ?? data.lat ?? undefined) as number | undefined,
        longitude: (data.longitude ?? data.lng ?? undefined) as number | undefined,
        station_type: normalizeStationType(data.station_type),
        linkedCustomerId,
        customerLinkKind,
        createdByDriver: data.createdByDriver === true,
    };
}

export default function SourcesPage() {
    const { t } = useLanguage();
    const auth = useAuth();
    const isAdmin = auth?.customClaims?.admin === true;
    const [sources, setSources] = useState<SourceRow[]>([]);
    const [search, setSearch] = useState("");
    const [loading, setLoading] = useState(true);
    const [editOpen, setEditOpen] = useState(false);
    const [editSource, setEditSource] = useState<SourceRow | null>(null);
    /** คลิกแถวตามราง → แผนที่บินไปที่พิกัดและแสดง tooltip */
    const [selectedSourceId, setSelectedSourceId] = useState<string | null>(null);
    const [currentPage, setCurrentPage] = useState(1);
    const [calculatingDistances, setCalculatingDistances] = useState(false);
    const [distancesMessage, setDistancesMessage] = useState<{ type: "success" | "error"; text: string } | null>(null);
    const [lastCalculatedAt, setLastCalculatedAt] = useState<Date | null>(null);
    /** Hub→SOC: เลือก Hub เห็นระยะไป SOC | SOC→Hub: เลือก SOC เห็นระยะไป Hub */
    const [distanceViewMode, setDistanceViewMode] = useState<"HUB_SOC" | "SOC_HUB">("HUB_SOC");
    /** แท็บจุดรับส่ง: Hub หรือ SOC */
    const [stationTab, setStationTab] = useState<"HUB" | "SOC">("HUB");
    /** true = แสดงเฉพาะ Driver เพิ่มจาก Mobile ที่รอ Admin กรอกพิกัด */
    const [showOnlyNew, setShowOnlyNew] = useState(false);
    /** ลูกค้าใน Dropdown — "all" = ไม่กรอง | "__unlinked__" = ยังไม่ผูกลูกค้า */
    const [customerFilterId, setCustomerFilterId] = useState<string>("all");
    const [customerOptions, setCustomerOptions] = useState<CustomerData[]>([]);

    /** จำนวนแถวต่อหน้า fix ที่ 10 */
    const itemsPerPage = 10;

    const fetchHubs = async () => {
        setLoading(true);
        try {
            const [querySnapshot, customers] = await Promise.all([
                getDocs(collection(db, COLLECTIONS.HUBS)),
                getCustomers(),
            ]);
            setCustomerOptions(customers);
            const customerById = new Map(customers.map((c) => [c.id, c]));
            const list: SourceRow[] = querySnapshot.docs.map((d) => {
                const row = mapDocToSourceRow({ id: d.id, data: d.data() as Record<string, unknown> });
                const c = row.linkedCustomerId ? customerById.get(row.linkedCustomerId) : undefined;
                return {
                    ...row,
                    linkedCustomerCode: c?.code,
                    linkedCustomerName: c?.name,
                };
            });
            setSources(list);
        } catch (error) {
            console.error("Error fetching sources:", error);
        } finally {
            setLoading(false);
        }
    };

    useEffect(() => {
        fetchHubs();
    }, []);

    const fetchLastCalculated = async () => {
        try {
            const snap = await getDoc(doc(db, COLLECTIONS.METADATA, "distances_last_calculated"));
            const ts = snap.data()?.timestamp;
            if (ts?.toDate) setLastCalculatedAt(ts.toDate());
        } catch {
            // ignore
        }
    };
    useEffect(() => {
        fetchLastCalculated();
    }, []);

    const filteredSources = useMemo(() => {
        let list = sources;
        if (customerFilterId === "__unlinked__") {
            list = list.filter((row) => !row.linkedCustomerId);
        } else if (customerFilterId !== "all") {
            list = list.filter((row) => row.linkedCustomerId === customerFilterId);
        }

        const q = search.trim().toLowerCase();
        if (!q) return list;
        const hay = (s: string | undefined) => (s ?? "").toLowerCase().includes(q);
        return list.filter(
            (row) =>
                hay(row.source_name_en) ||
                hay(row.source_id) ||
                hay(row.source_name_th) ||
                hay(row.linkedCustomerCode) ||
                hay(row.linkedCustomerName)
        );
    }, [sources, search, customerFilterId]);

    /** รายการตามประเภทสถานีที่เลือกในแท็บ */
    const sourcesForStationTab = useMemo(
        () => filteredSources.filter((row) => row.station_type === stationTab),
        [filteredSources, stationTab]
    );

    /** Driver เพิ่มจาก Mobile ที่ยังไม่มีพิกัด — แจ้ง Admin กรอกข้อมูลให้ครบ */
    const driverAddedNeedsCompletion = useMemo(
        () =>
            filteredSources.filter(
                (row) => row.createdByDriver === true && (row.latitude == null || row.longitude == null)
            ),
        [filteredSources]
    );

    /** เมื่อ showOnlyNew = แสดงเฉพาะของใหม่ที่รอกรอก (ตามแท็บ HUB/SOC) | ไม่ใช่ = แสดงตามแท็บ */
    const tableSources = showOnlyNew
        ? driverAddedNeedsCompletion.filter((row) => row.station_type === stationTab)
        : sourcesForStationTab;

    const totalPages = Math.max(1, Math.ceil(tableSources.length / itemsPerPage));
    const paginatedSources = useMemo(
        () =>
            tableSources.slice(
                (currentPage - 1) * itemsPerPage,
                currentPage * itemsPerPage
            ),
        [tableSources, currentPage, itemsPerPage]
    );

    useEffect(() => {
        setCurrentPage(1);
        setSelectedSourceId(null);
    }, [search, showOnlyNew, stationTab, customerFilterId]);


    useEffect(() => {
        if (currentPage > totalPages) setCurrentPage(1);
    }, [currentPage, totalPages]);

    const handleDownloadSources = () => {
        const headers = [
            t("firstMile.sources.export.firestoreDocId"),
            t("firstMile.sources.table.sourceId"),
            t("firstMile.sources.table.nameSPX"),
            t("firstMile.sources.table.nameThai"),
            "Latitude",
            "Longitude",
            t("firstMile.sources.table.stationType"),
            t("firstMile.sources.export.customerCode"),
            t("firstMile.sources.export.customerName"),
        ];
        const rows = tableSources.map((row) => [
            row.id ?? "",
            row.source_id ?? "",
            row.source_name_en ?? "",
            row.source_name_th ?? "",
            row.latitude ?? "",
            row.longitude ?? "",
            row.station_type ?? "",
            row.linkedCustomerCode ?? "",
            row.linkedCustomerName ?? "",
        ]);
        const ws = XLSX.utils.aoa_to_sheet([headers, ...rows]);
        const wb = XLSX.utils.book_new();
        XLSX.utils.book_append_sheet(wb, ws, "Sources");
        XLSX.writeFile(wb, `Sources_${new Date().toISOString().slice(0, 10)}.xlsx`);
    };

    const handleCalculateDistances = async () => {
        if (!isAdmin) {
            setDistancesMessage({
                type: "error",
                text: "Admin only. Sign in with an admin account (or ensure your account has the admin claim).",
            });
            return;
        }
        setCalculatingDistances(true);
        setDistancesMessage(null);
        try {
            const computeHubSocDistances = httpsCallable<
                void,
                { ok: boolean; written: number; hubsCount: number; socsCount: number; calculatedAt?: string; error?: string }
            >(functions, "computeHubSocDistances");
            const { data } = await computeHubSocDistances();
            if (!data.ok && data.error) {
                setDistancesMessage({ type: "error", text: data.error || t("firstMile.sources.distancesError") });
                return;
            }
            if (data.calculatedAt) setLastCalculatedAt(new Date(data.calculatedAt));
            const msg = t("firstMile.sources.distancesSuccess")
                .replace("{{written}}", String(data.written ?? 0))
                .replace("{{hubsCount}}", String(data.hubsCount ?? 0))
                .replace("{{socsCount}}", String(data.socsCount ?? 0));
            setDistancesMessage({ type: "success", text: msg });
        } catch (err: unknown) {
            const code = err && typeof err === "object" && "code" in err ? (err as { code?: string }).code : undefined;
            const message = err && typeof err === "object" && "message" in err ? (err as { message?: string }).message : undefined;
            if (code === "functions/permission-denied" || (typeof message === "string" && message.toLowerCase().includes("admin"))) {
                setDistancesMessage({
                    type: "error",
                    text: "Admin only. Sign in with an account that has admin privileges.",
                });
            } else if (typeof message === "string" && message) {
                setDistancesMessage({ type: "error", text: message });
            } else {
                setDistancesMessage({ type: "error", text: t("firstMile.sources.distancesError") });
            }
            if (process.env.NODE_ENV === "development") {
                console.error("[computeHubSocDistances]", err);
            }
        } finally {
            setCalculatingDistances(false);
        }
    };

    return (
        <div className="flex-1 space-y-4 p-8 pt-6">
            <div className="flex items-center justify-between space-y-2">
                <h2 className="text-3xl font-bold tracking-tight">{t("firstMile.sources.title")}</h2>
                <div className="flex flex-col items-end gap-2">
                    <div className="flex flex-col items-end gap-1">
                        {lastCalculatedAt && (
                            <p className="text-sm text-muted-foreground">
                                {t("firstMile.sources.lastCalculated")}: {lastCalculatedAt.toLocaleString()}
                            </p>
                        )}
                        {distancesMessage && (
                            <p className={distancesMessage.type === "success" ? "text-sm text-green-600" : "text-sm text-destructive"}>
                                {distancesMessage.text}
                            </p>
                        )}
                    </div>
                    <div className="flex items-center gap-2">
                    <Button
                        variant="outline"
                        size="sm"
                        className="h-9 w-9 shrink-0 p-0"
                        onClick={() => fetchHubs()}
                        disabled={loading}
                        aria-label={t("firstMile.sources.refresh")}
                    >
                        <RefreshCw className={`h-4 w-4 ${loading ? "animate-spin" : ""}`} />
                    </Button>
                    <Button
                        variant="outline"
                        onClick={handleCalculateDistances}
                        disabled={calculatingDistances || !isAdmin}
                        className="gap-2"
                        title={!isAdmin ? "Admin only" : undefined}
                    >
                        {calculatingDistances ? (
                            <span className="animate-pulse">{t("firstMile.sources.calculatingDistances")}</span>
                        ) : (
                            <>
                                <Route className="h-4 w-4" />
                                {t("firstMile.sources.calculateDistances")}
                            </>
                        )}
                    </Button>
                    <Button
                        variant="outline"
                        onClick={handleDownloadSources}
                        disabled={loading || tableSources.length === 0}
                        className="gap-2"
                        title={loading || tableSources.length === 0 ? undefined : t("firstMile.sources.downloadHint")}
                    >
                        <Download className="h-4 w-4" />
                        {t("firstMile.sources.download")}
                    </Button>
                    <PickupLocationImportDialog onSuccess={fetchHubs} />
                    <HubDialog
                        trigger={
                            <Button>
                                <Plus className="mr-2 h-4 w-4" />
                                {t("firstMile.sources.newSource")}
                            </Button>
                        }
                        onSuccess={fetchHubs}
                    />
                    <HubDialog
                        open={editOpen}
                        onOpenChange={(open) => {
                            setEditOpen(open);
                            if (!open) setEditSource(null);
                        }}
                        defaultValues={editSource ? {
                            source_id: editSource.source_id,
                            source_name_en: editSource.source_name_en,
                            latitude: editSource.latitude,
                            longitude: editSource.longitude,
                            station_type: editSource.station_type,
                            linkedCustomerId: editSource.linkedCustomerId ?? "",
                            customerLinkKind: editSource.customerLinkKind ?? "customer",
                        } : undefined}
                        documentId={editSource?.id}
                        onSuccess={() => {
                            fetchHubs();
                            setEditOpen(false);
                            setEditSource(null);
                        }}
                    />
                    </div>
                </div>
            </div>

            {driverAddedNeedsCompletion.length > 0 && (
                <button
                    type="button"
                    onClick={() => setShowOnlyNew(true)}
                    className="flex w-full items-center gap-3 rounded-lg border border-amber-500/50 bg-amber-500/10 px-4 py-3 text-left text-amber-800 transition-colors hover:bg-amber-500/20 dark:text-amber-200 dark:hover:bg-amber-500/15"
                >
                    <AlertTriangle className="h-5 w-5 shrink-0" />
                    <p className="text-sm font-medium">
                        {t("firstMile.sources.driverAddedAlert")}
                        <span className="font-semibold ml-1">{driverAddedNeedsCompletion.length}</span>
                        {t("firstMile.sources.driverAddedAlertSuffix")}
                    </p>
                    <span className="ml-auto text-xs underline">{t("firstMile.sources.filterNewClick")}</span>
                </button>
            )}

            <div className="grid grid-cols-1 lg:grid-cols-2 gap-6 items-stretch min-h-0">
                {/* Left: จุดรับ-ส่ง (Table) — ความสูงเท่ากับแผนที่ */}
                <Card className="flex flex-col min-h-[520px] lg:min-h-0">
                    <CardHeader className="shrink-0 space-y-3">
                        <div className="flex items-center justify-between gap-2 flex-wrap">
                            <div className="flex items-center gap-3 flex-wrap">
                                <CardTitle className="text-lg">{t("firstMile.sources.dbSources")}</CardTitle>
                                <Tabs
                                    value={stationTab}
                                    onValueChange={(v) =>
                                        v === "HUB" || v === "SOC" ? setStationTab(v) : undefined
                                    }
                                >
                                    <TabsList className="h-8">
                                        <TabsTrigger value="HUB" className="text-xs px-3">
                                            {t("firstMile.sources.tabHub")}
                                        </TabsTrigger>
                                        <TabsTrigger value="SOC" className="text-xs px-3">
                                            {t("firstMile.sources.tabSoc")}
                                        </TabsTrigger>
                                    </TabsList>
                                </Tabs>
                                <Button
                                    variant={showOnlyNew ? "default" : "outline"}
                                    size="sm"
                                    className="h-8 gap-1"
                                    onClick={() => setShowOnlyNew(!showOnlyNew)}
                                >
                                    {t("firstMile.sources.filterNew")}
                                    {driverAddedNeedsCompletion.length > 0 && (
                                        <span className="ml-1 rounded-full bg-amber-500/30 px-1.5 py-0.5 text-xs font-medium">
                                            {driverAddedNeedsCompletion.length}
                                        </span>
                                    )}
                                </Button>
                                <Tabs value={distanceViewMode} onValueChange={(v) => v === "HUB_SOC" || v === "SOC_HUB" ? setDistanceViewMode(v) : undefined}>
                                    <TabsList className="h-8">
                                        <TabsTrigger value="HUB_SOC" className="text-xs px-3">{t("firstMile.sources.modeHubToSoc")}</TabsTrigger>
                                        <TabsTrigger value="SOC_HUB" className="text-xs px-3">{t("firstMile.sources.modeSocToHub")}</TabsTrigger>
                                    </TabsList>
                                </Tabs>
                            </div>
                            <div className="flex items-center space-x-2">
                                <Button
                                    variant="outline"
                                    size="sm"
                                    className="h-8 shrink-0"
                                    onClick={() => fetchHubs()}
                                    disabled={loading}
                                    aria-label={t("firstMile.sources.refresh")}
                                >
                                    <RefreshCw className={`h-4 w-4 ${loading ? "animate-spin" : ""}`} />
                                </Button>
                                <Button
                                    variant="outline"
                                    size="sm"
                                    className="h-8 shrink-0 gap-1"
                                    onClick={handleDownloadSources}
                                    disabled={loading || tableSources.length === 0}
                                    title={loading || tableSources.length === 0 ? undefined : t("firstMile.sources.downloadHint")}
                                    aria-label={t("firstMile.sources.download")}
                                >
                                    <Download className="h-4 w-4" />
                                    <span className="max-sm:hidden">{t("firstMile.sources.download")}</span>
                                </Button>
                                <Search className="h-4 w-4 text-muted-foreground shrink-0" />
                                <Input
                                    placeholder={t("firstMile.sources.search")}
                                    value={search}
                                    onChange={(e) => {
                                        setSearch(e.target.value);
                                    }}
                                    className="h-8 w-[200px]"
                                />
                                <Select value={customerFilterId} onValueChange={setCustomerFilterId}>
                                    <SelectTrigger className="h-8 w-[220px] shrink-0">
                                        <SelectValue placeholder={t("firstMile.sources.filterCustomer")} />
                                    </SelectTrigger>
                                    <SelectContent position="popper" className="max-h-[280px]">
                                        <SelectItem value="all">{t("firstMile.sources.filterCustomerAll")}</SelectItem>
                                        <SelectItem value="__unlinked__">
                                            {t("firstMile.sources.filterCustomerUnlinked")}
                                        </SelectItem>
                                        {customerOptions.map((c) => (
                                            <SelectItem key={c.id} value={c.id}>
                                                {(c.code ?? "").trim() || c.id} — {c.name ?? ""}
                                            </SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                            </div>
                        </div>
                    </CardHeader>
                    <CardContent className="flex-1 min-h-0 p-4 flex flex-col overflow-auto">
                       <Table>
                            <TableHeader>
                                <TableRow>
                                    <TableHead>{t("firstMile.sources.table.sourceId")}</TableHead>
                                    <TableHead>{t("firstMile.sources.table.nameSPX")}</TableHead>
                                    <TableHead>{t("firstMile.sources.table.coordinates")}</TableHead>
                                    <TableHead>{t("firstMile.sources.table.stationType")}</TableHead>
                                    <TableHead className="w-[80px]">{t("firstMile.sources.table.actions")}</TableHead>
                                </TableRow>
                            </TableHeader>
                            <TableBody>
                                {loading ? (
                                    <TableRow>
                                        <TableCell colSpan={5} className="text-center h-24">
                                            {t("firstMile.sources.loading")}
                                        </TableCell>
                                    </TableRow>
                                ) : tableSources.length === 0 ? (
                                    <TableRow>
                                        <TableCell colSpan={5} className="text-center h-24 text-muted-foreground">
                                            {showOnlyNew
                                                ? t("firstMile.sources.noNewItems")
                                                : stationTab === "HUB"
                                                  ? t("firstMile.sources.noHubsInFilter")
                                                  : t("firstMile.sources.noSocsInFilter")}
                                        </TableCell>
                                    </TableRow>
                                ) : (
                                    paginatedSources.map((row, index) => {
                                        const rowKey = row.id ?? row.source_id;
                                        const isSelected = selectedSourceId !== null && (row.id === selectedSourceId || row.source_id === selectedSourceId);
                                        const hasCoords = row.latitude != null && row.longitude != null;
                                        const needsAdminCompletion = row.createdByDriver === true && !hasCoords;
                                        return (
                                            <TableRow
                                                key={row.id || index}
                                                className={`cursor-pointer ${needsAdminCompletion ? "bg-amber-500/5" : ""} ${isSelected ? "bg-primary/10 border-primary/30" : "hover:bg-muted/50"}`}
                                                onClick={() => {
                                                    if (hasCoords) setSelectedSourceId(rowKey);
                                                }}
                                            >
                                                <TableCell className="font-medium">
                                                    <span className="flex items-center gap-2">
                                                        {row.source_id}
                                                        {needsAdminCompletion && (
                                                            <span className="inline-flex items-center rounded-full bg-amber-500/20 px-2 py-0.5 text-xs font-medium text-amber-800 dark:text-amber-200">
                                                                {t("firstMile.sources.needsCompletion")}
                                                            </span>
                                                        )}
                                                    </span>
                                                </TableCell>
                                                <TableCell>
                                                    <div>
                                                        <span>{row.source_name_en || "—"}</span>
                                                        {row.source_name_th &&
                                                            row.source_name_th !== row.source_name_en && (
                                                                <span className="block text-xs text-muted-foreground mt-0.5">
                                                                    {row.source_name_th}
                                                                </span>
                                                            )}
                                                    </div>
                                                </TableCell>
                                                <TableCell>
                                                    {hasCoords ? (
                                                        <a
                                                            href={`https://www.google.com/maps/search/?api=1&query=${row.latitude},${row.longitude}`}
                                                            target="_blank"
                                                            rel="noopener noreferrer"
                                                            className="flex items-center text-blue-600 hover:text-blue-800"
                                                            onClick={(e) => e.stopPropagation()}
                                                        >
                                                            <MapPin className="mr-1 h-3 w-3" />
                                                            {row.latitude!.toFixed(4)}, {row.longitude!.toFixed(4)}
                                                        </a>
                                                    ) : (
                                                        <span className="text-muted-foreground text-xs">{t("firstMile.sources.noCoords")}</span>
                                                    )}
                                                </TableCell>
                                                <TableCell>{t(`firstMile.hub.stationType.${row.station_type}`)}</TableCell>
                                                <TableCell onClick={(e) => e.stopPropagation()}>
                                                    <DropdownMenu>
                                                        <DropdownMenuTrigger asChild>
                                                            <Button variant="ghost" size="icon" className="h-8 w-8">
                                                                <span className="sr-only">{t("firstMile.sources.table.actions")}</span>
                                                                <MoreHorizontal className="h-4 w-4" />
                                                            </Button>
                                                        </DropdownMenuTrigger>
                                                        <DropdownMenuContent align="end">
                                                            <DropdownMenuLabel>{t("firstMile.sources.table.actions")}</DropdownMenuLabel>
                                                            <DropdownMenuItem
                                                                onClick={() => {
                                                                    setEditSource(row);
                                                                    setEditOpen(true);
                                                                }}
                                                            >
                                                                <Pencil className="mr-2 h-4 w-4" />
                                                                {t("firstMile.sources.edit")}
                                                            </DropdownMenuItem>
                                                            {hasCoords && (
                                                                <DropdownMenuItem
                                                                    onClick={async () => {
                                                                        const url = `https://www.google.com/maps/search/?api=1&query=${row.latitude},${row.longitude}`;
                                                                        await navigator.clipboard.writeText(url);
                                                                        toast.success(t("firstMile.sources.copyMapUrlSuccess"));
                                                                    }}
                                                                >
                                                                    <Link className="mr-2 h-4 w-4" />
                                                                    {t("firstMile.sources.copyMapUrl")}
                                                                </DropdownMenuItem>
                                                            )}
                                                        </DropdownMenuContent>
                                                    </DropdownMenu>
                                                </TableCell>
                                            </TableRow>
                                        );
                                    })
                                )}
                            </TableBody>
                        </Table>
                        {tableSources.length > 0 && (
                            <div className="flex items-center justify-between px-4 py-3 border-t bg-muted/20">
                                <p className="text-sm text-muted-foreground">
                                    {t("firstMile.sources.pagination.showing")}{" "}
                                    {(currentPage - 1) * itemsPerPage + 1}{" "}
                                    {t("firstMile.sources.pagination.to")}{" "}
                                    {Math.min(currentPage * itemsPerPage, tableSources.length)}{" "}
                                    {t("firstMile.sources.pagination.of")} {tableSources.length}{" "}
                                    {t("firstMile.sources.pagination.entries")}
                                </p>
                                <div className="flex gap-1">
                                    <Button
                                        variant="outline"
                                        size="sm"
                                        onClick={() => setCurrentPage((p) => Math.max(1, p - 1))}
                                        disabled={currentPage === 1}
                                    >
                                        <ChevronLeft className="h-4 w-4" />
                                    </Button>
                                    <Button
                                        variant="outline"
                                        size="sm"
                                        onClick={() => setCurrentPage((p) => Math.min(totalPages, p + 1))}
                                        disabled={currentPage === totalPages}
                                    >
                                        <ChevronRight className="h-4 w-4" />
                                    </Button>
                                </div>
                            </div>
                        )}
                    </CardContent>
                </Card>

                {/* Right: Map — ยืดสูงเท่ากับตาราง */}
                <Card className="flex flex-col min-h-[520px] lg:min-h-0">
                    <CardHeader className="pb-2 shrink-0 flex flex-row items-center justify-between">
                        <div>
                            <CardTitle className="text-base flex items-center gap-2">
                                <MapPin className="h-4 w-4" />
                                {t("firstMile.sources.mapTitle")}
                            </CardTitle>
                            <p className="text-xs text-muted-foreground">
                                {sourcesForStationTab.filter((s) => s.latitude != null && s.longitude != null).length}{" "}
                                {t("firstMile.sources.mapPoints")}
                            </p>
                        </div>
                        {selectedSourceId && (
                            <Button
                                variant="ghost"
                                size="sm"
                                className="text-muted-foreground"
                                onClick={() => setSelectedSourceId(null)}
                            >
                                {t("firstMile.sources.clearSelection")}
                            </Button>
                        )}
                    </CardHeader>
                    <CardContent className="flex-1 min-h-0 pt-0 overflow-hidden flex flex-col">
                        <SourcesMap
                            sources={sourcesForStationTab}
                            selectedSourceId={selectedSourceId}
                            onClearSelection={() => setSelectedSourceId(null)}
                            distanceViewMode={distanceViewMode}
                            className="flex-1 min-h-[320px]"
                        />
                    </CardContent>
                </Card>
            </div>
        </div>
    );
}
