"use client";

import { useEffect, useState } from "react";
import { collection, getDocs, query, where } from "firebase/firestore";
import { db } from "@/firebase/client";
import { useLanguage } from "@/context/language";
import { COLLECTIONS } from "@/lib/collections";
import { Label } from "@/components/ui/label";

export interface SourceRowLike {
    source_id: string;
    station_type: string;
}

interface HubDistancePanelProps {
    selectedRow: SourceRowLike | null;
}

export function HubDistancePanel({ selectedRow }: HubDistancePanelProps) {
    const { t } = useLanguage();
    const [distances, setDistances] = useState<{ socId: string; distanceKm: number; durationMinutes: number }[]>([]);
    const [loading, setLoading] = useState(false);

    useEffect(() => {
        if (!selectedRow || selectedRow.station_type !== "HUB") {
            setDistances([]);
            return;
        }
        setLoading(true);
        getDocs(query(collection(db, COLLECTIONS.HUB_SOC_DISTANCES), where("hubId", "==", selectedRow.source_id)))
            .then((snap) => {
                const list = snap.docs.map((d) => {
                    const data = d.data();
                    return {
                        socId: (data.socId ?? "") as string,
                        distanceKm: Number(data.distanceKm ?? 0),
                        durationMinutes: Number(data.durationMinutes ?? 0),
                    };
                });
                setDistances(list);
            })
            .catch(() => setDistances([]))
            .finally(() => setLoading(false));
    }, [selectedRow?.source_id, selectedRow?.station_type]);

    if (!selectedRow || selectedRow.station_type !== "HUB") return null;

    const sorted = [...distances].sort((a, b) =>
        a.socId.localeCompare(b.socId, undefined, { sensitivity: "base" })
    );

    return (
        <div className="rounded-md border bg-muted/30 p-3 space-y-2">
            <p className="text-sm font-medium text-muted-foreground">{t("firstMile.sources.distanceToSoc")}</p>
            {loading ? (
                <p className="text-sm text-muted-foreground">{t("firstMile.sources.loading")}</p>
            ) : sorted.length === 0 ? (
                <p className="text-sm text-muted-foreground">{t("firstMile.sources.noDistanceData")}</p>
            ) : (
                <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-2 max-h-[220px] overflow-y-auto">
                    {sorted.map((d) => (
                        <div key={d.socId} className="flex flex-col gap-1">
                            <Label className="text-xs font-medium text-muted-foreground">{d.socId} :</Label>
                            <span className="text-sm">
                                {t("firstMile.sources.kmMin")
                                    .replace("{{km}}", String(d.distanceKm.toFixed(2)))
                                    .replace("{{min}}", String(d.durationMinutes.toFixed(1)))}
                            </span>
                        </div>
                    ))}
                </div>
            )}
        </div>
    );
}
