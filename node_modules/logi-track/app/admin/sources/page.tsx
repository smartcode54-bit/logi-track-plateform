"use client";

import { useEffect, useState } from "react";
import { Plus, MapPin, Search, Pencil } from "lucide-react";
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
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { HubDialog } from "../first-mile/hub-dialog";
import { collection, getDocs } from "firebase/firestore";
import { db } from "@/firebase/client";
import { useLanguage } from "@/context/language";
import type { Hub, StationType } from "@/validate/hubSchema";

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

    const fetchHubs = async () => {
        setLoading(true);
        try {
            const querySnapshot = await getDocs(collection(db, "hubs"));
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

    const filteredSources = sources.filter(
        (row) =>
            (row.source_name_en && row.source_name_en.toLowerCase().includes(search.toLowerCase())) ||
            (row.source_id && row.source_id.toLowerCase().includes(search.toLowerCase()))
    );

    return (
        <div className="flex-1 space-y-4 p-8 pt-6">
            <div className="flex items-center justify-between space-y-2">
                <h2 className="text-3xl font-bold tracking-tight">{t("firstMile.sources.title")}</h2>
                <div className="flex items-center space-x-2">
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

            <Card>
                <CardHeader>
                    <div className="flex items-center justify-between">
                        <CardTitle>{t("firstMile.sources.dbSources")}</CardTitle>
                        <div className="flex w-full max-w-sm items-center space-x-2">
                            <Search className="h-4 w-4 text-muted-foreground mr-2" />
                            <Input
                                placeholder={t("firstMile.sources.search")}
                                value={search}
                                onChange={(e) => setSearch(e.target.value)}
                                className="h-8 w-[250px]"
                            />
                        </div>
                    </div>
                </CardHeader>
                <CardContent>
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
                                filteredSources.map((row, index) => (
                                    <TableRow key={row.id || index}>
                                        <TableCell className="font-medium">{row.source_id}</TableCell>
                                        <TableCell>{row.source_name_en}</TableCell>
                                        <TableCell>
                                            {row.latitude != null && row.longitude != null ? (
                                                <a
                                                    href={`https://www.google.com/maps/search/?api=1&query=${row.latitude},${row.longitude}`}
                                                    target="_blank"
                                                    rel="noopener noreferrer"
                                                    className="flex items-center text-blue-600 hover:text-blue-800"
                                                >
                                                    <MapPin className="mr-1 h-3 w-3" />
                                                    {row.latitude.toFixed(4)}, {row.longitude.toFixed(4)}
                                                </a>
                                            ) : (
                                                <span className="text-muted-foreground text-xs">{t("firstMile.sources.noCoords")}</span>
                                            )}
                                        </TableCell>
                                        <TableCell>{t(`firstMile.hub.stationType.${row.station_type}`)}</TableCell>
                                        <TableCell>
                                            <Button
                                                variant="ghost"
                                                size="icon"
                                                className="h-8 w-8"
                                                onClick={() => {
                                                    setEditSource(row);
                                                    setEditOpen(true);
                                                }}
                                                aria-label={t("firstMile.sources.edit")}
                                            >
                                                <Pencil className="h-4 w-4" />
                                            </Button>
                                        </TableCell>
                                    </TableRow>
                                ))
                            )}
                        </TableBody>
                    </Table>
                </CardContent>
            </Card>
        </div>
    );
}
