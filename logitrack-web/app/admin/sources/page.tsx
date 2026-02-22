"use client";

import { useEffect, useState, useMemo } from "react";
import dynamic from "next/dynamic";
import { Plus, MapPin, Search, Pencil, ChevronLeft, ChevronRight, RefreshCw, Download, Route, MoreHorizontal, Link } from "lucide-react";
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
import { HubDialog } from "../first-mile/hub-dialog";
import { PickupLocationImportDialog } from "./pickup-import-dialog";
import { collection, getDocs } from "firebase/firestore";
import { httpsCallable } from "firebase/functions";
import { db, functions } from "@/firebase/client";
import { useLanguage } from "@/context/language";
import { COLLECTIONS } from "@/lib/collections";
import type { Hub, StationType } from "@/validate/hubSchema";
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
}

/** Normalize to HUB or SOC; map legacy FM_HUB/LH_HUB → HUB, RETURN_CENTER → SOC */
function normalizeStationType(value: unknown): StationType {
    const v = String(value ?? "").toUpperCase();
    if (v === "SOC" || v === "RETURN_CENTER") return "SOC";
    return "HUB"; // HUB, FM_HUB, LH_HUB, or missing
}

function mapDocToSourceRow(doc: { id: string; data: Record<string, unknown> }): SourceRow {
    const data = doc.data;
    return {
        id: doc.id,
        source_id: (data.source_id ?? data.hubId ?? data.hubCode ?? "") as string,
        source_name_en: (data.source_name_en ?? data.hubName ?? "") as string,
        latitude: (data.latitude ?? data.lat ?? undefined) as number | undefined,
        longitude: (data.longitude ?? data.lng ?? undefined) as number | undefined,
        station_type: normalizeStationType(data.station_type),
    };
}

export default function SourcesPage() {
    const { t } = useLanguage();
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

    /** จำนวนแถวต่อหน้า fix ที่ 10 */
    const itemsPerPage = 10;

    const fetchHubs = async () => {
        setLoading(true);
        try {
            const querySnapshot = await getDocs(collection(db, COLLECTIONS.HUBS));
            const list: SourceRow[] = querySnapshot.docs.map((doc) =>
                mapDocToSourceRow({ id: doc.id, data: doc.data() as Record<string, unknown> })
            );
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

    const filteredSources = useMemo(
        () =>
            sources.filter(
                (row) =>
                    (row.source_name_en && row.source_name_en.toLowerCase().includes(search.toLowerCase())) ||
                    (row.source_id && row.source_id.toLowerCase().includes(search.toLowerCase()))
            ),
        [sources, search]
    );

    const totalPages = Math.max(1, Math.ceil(filteredSources.length / itemsPerPage));
    const paginatedSources = useMemo(
        () =>
            filteredSources.slice(
                (currentPage - 1) * itemsPerPage,
                currentPage * itemsPerPage
            ),
        [filteredSources, currentPage, itemsPerPage]
    );

    useEffect(() => {
        setCurrentPage(1);
        setSelectedSourceId(null);
    }, [search]);

    useEffect(() => {
        if (currentPage > totalPages) setCurrentPage(1);
    }, [currentPage, totalPages]);

    const handleDownloadSources = () => {
        const headers = [
            t("firstMile.sources.table.sourceId"),
            t("firstMile.sources.table.nameSPX"),
            "Latitude",
            "Longitude",
            t("firstMile.sources.table.stationType"),
        ];
        const rows = filteredSources.map((row) => [
            row.source_id ?? "",
            row.source_name_en ?? "",
            row.latitude ?? "",
            row.longitude ?? "",
            row.station_type ?? "",
        ]);
        const ws = XLSX.utils.aoa_to_sheet([headers, ...rows]);
        const wb = XLSX.utils.book_new();
        XLSX.utils.book_append_sheet(wb, ws, "Sources");
        XLSX.writeFile(wb, `Sources_${new Date().toISOString().slice(0, 10)}.xlsx`);
    };

    const handleCalculateDistances = async () => {
        setCalculatingDistances(true);
        setDistancesMessage(null);
        try {
            const computeHubSocDistances = httpsCallable<
                void,
                { ok: boolean; written: number; hubsCount: number; socsCount: number; error?: string }
            >(functions, "computeHubSocDistances");
            const { data } = await computeHubSocDistances();
            if (!data.ok && data.error) {
                setDistancesMessage({ type: "error", text: data.error || t("firstMile.sources.distancesError") });
                return;
            }
            const msg = t("firstMile.sources.distancesSuccess")
                .replace("{{written}}", String(data.written ?? 0))
                .replace("{{hubsCount}}", String(data.hubsCount ?? 0))
                .replace("{{socsCount}}", String(data.socsCount ?? 0));
            setDistancesMessage({ type: "success", text: msg });
        } catch {
            setDistancesMessage({ type: "error", text: t("firstMile.sources.distancesError") });
        } finally {
            setCalculatingDistances(false);
        }
    };

    return (
        <div className="flex-1 space-y-4 p-8 pt-6">
            <div className="flex items-center justify-between space-y-2">
                <h2 className="text-3xl font-bold tracking-tight">{t("firstMile.sources.title")}</h2>
                <div className="flex flex-col items-end gap-2">
                    {distancesMessage && (
                        <p className={distancesMessage.type === "success" ? "text-sm text-green-600" : "text-sm text-destructive"}>
                            {distancesMessage.text}
                        </p>
                    )}
                    <div className="flex items-center gap-2">
                    <Button
                        variant="outline"
                        onClick={handleCalculateDistances}
                        disabled={calculatingDistances}
                        className="gap-2"
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

            <div className="grid grid-cols-1 lg:grid-cols-2 gap-6 items-stretch min-h-0">
                {/* Left: จุดรับ-ส่ง (Table) — ความสูงเท่ากับแผนที่ */}
                <Card className="flex flex-col min-h-[520px] lg:min-h-0">
                    <CardHeader className="shrink-0">
                        <div className="flex items-center justify-between gap-2">
                            <CardTitle className="text-lg">{t("firstMile.sources.dbSources")}</CardTitle>
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
                                    disabled={loading || filteredSources.length === 0}
                                    aria-label={t("firstMile.sources.download")}
                                >
                                    <Download className="h-4 w-4" />
                                    {t("firstMile.sources.download")}
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
                                ) : filteredSources.length === 0 ? (
                                    <TableRow>
                                        <TableCell colSpan={5} className="text-center h-24">
                                            {t("firstMile.sources.noData")}
                                        </TableCell>
                                    </TableRow>
                                ) : (
                                    paginatedSources.map((row, index) => {
                                        const rowKey = row.id ?? row.source_id;
                                        const isSelected = selectedSourceId !== null && (row.id === selectedSourceId || row.source_id === selectedSourceId);
                                        const hasCoords = row.latitude != null && row.longitude != null;
                                        return (
                                            <TableRow
                                                key={row.id || index}
                                                className={`cursor-pointer ${isSelected ? "bg-primary/10 border-primary/30" : "hover:bg-muted/50"}`}
                                                onClick={() => {
                                                    if (hasCoords) setSelectedSourceId(rowKey);
                                                }}
                                            >
                                                <TableCell className="font-medium">{row.source_id}</TableCell>
                                                <TableCell>{row.source_name_en}</TableCell>
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
                        {filteredSources.length > 0 && (
                            <div className="flex items-center justify-between px-4 py-3 border-t bg-muted/20">
                                <p className="text-sm text-muted-foreground">
                                    {t("firstMile.sources.pagination.showing")}{" "}
                                    {(currentPage - 1) * itemsPerPage + 1}{" "}
                                    {t("firstMile.sources.pagination.to")}{" "}
                                    {Math.min(currentPage * itemsPerPage, filteredSources.length)}{" "}
                                    {t("firstMile.sources.pagination.of")} {filteredSources.length}{" "}
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
                                {filteredSources.filter((s) => s.latitude != null && s.longitude != null).length}{" "}
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
                            sources={filteredSources}
                            selectedSourceId={selectedSourceId}
                            onClearSelection={() => setSelectedSourceId(null)}
                            className="flex-1 min-h-[320px]"
                        />
                    </CardContent>
                </Card>
            </div>
        </div>
    );
}
