"use client";

import { useEffect, useMemo, useState } from "react";
import { useMap } from "@vis.gl/react-google-maps";
import { useLanguage } from "@/context/language";
import { fetchDistanceRows } from "@/features/hubs/api/hubs";
import { socIdMatchesKey } from "@/validate/taskSchema";
import { GoogleMap, MarkerWithPopup, PanTo, THAILAND_CENTER, fitPoints, type LatLng } from "./GoogleMap";

export interface SourcePoint {
    id?: string;
    source_id: string;
    source_name_en: string;
    source_name_th?: string;
    latitude?: number;
    longitude?: number;
    station_type?: string;
}

type PlacedSource = SourcePoint & { latitude: number; longitude: number };

/** Shows every point while nothing is selected (a selection flies to its own point). */
function FitBounds({ points, skip }: { points: LatLng[]; skip: boolean }) {
    const map = useMap();
    useEffect(() => {
        if (map && !skip) fitPoints(map, points, { padding: 24, maxZoom: 14, singleZoom: 14 });
    }, [map, points, skip]);
    return null;
}

/** Distance row: for HUB we use socId (SOC key); for SOC we use destId (Hub source_id). */
type DistanceRow = { socId: string; destId: string; distanceKm: number; durationMinutes: number };

/** รายการ SOC จากผล query ระยะ (ไม่จำกัดเฉพาะ SOCE/SOCN/SOCW) */
function sortedSocIdsFromDistanceRows(distances: DistanceRow[]): string[] {
    return [
        ...new Set(distances.map((r) => String(r.socId ?? "").trim()).filter(Boolean)),
    ].sort((a, b) => a.localeCompare(b, undefined, { sensitivity: "base" }));
}

/** Popup of the point selected in the table: its name and the Hub→SOC or SOC→Hub distances (LM trip_records prep). */
function SelectedSourceDetails({ selected, mode }: { selected: PlacedSource; mode: "HUB_SOC" | "SOC_HUB" }) {
    const { t } = useLanguage();
    const [distances, setDistances] = useState<DistanceRow[]>([]);
    const isSocStation = String(selected.station_type ?? "").toUpperCase() === "SOC";

    useEffect(() => {
        // `['distances', {code, isSoc, mode}]` (T25): Go `GET /v1/distances`, cached, so selecting a point again costs no request.
        fetchDistanceRows({ code: selected.source_id, isSoc: isSocStation, mode })
            .then(setDistances)
            .catch(() => setDistances([]));
    }, [selected.source_id, isSocStation, mode]);

    const rows: [string, DistanceRow | undefined][] = isSocStation
        ? [...new Set(distances.map((r) => String(r.destId ?? "").trim()).filter(Boolean))]
              .sort((a, b) => a.localeCompare(b, undefined, { sensitivity: "base" }))
              .map((hid) => [hid, distances.find((r) => r.destId === hid)])
        : sortedSocIdsFromDistanceRows(distances).map((id) => [id, distances.find((r) => r.socId === id || socIdMatchesKey(r.socId, id))]);

    const name =
        selected.source_name_th && selected.source_name_th !== selected.source_name_en
            ? `${selected.source_name_en} (${selected.source_name_th})`
            : selected.source_name_en;
    return (
        <div className="px-1.5 py-0.5 text-sm min-w-[140px] max-h-[200px] overflow-y-auto">
            <p className="text-muted-foreground text-xs leading-tight">{name}</p>
            {rows.length > 0 ? (
                <div className="mt-1 pt-1 border-t border-border">
                    <p className="text-xs font-medium text-muted-foreground mb-0.5 leading-tight">
                        {t(mode === "HUB_SOC" ? "firstMile.sources.distanceToSoc" : "firstMile.sources.distanceFromSoc")}
                    </p>
                    {rows.map(([label, d]) => (
                        <p key={label} className="text-xs mt-0">
                            <span className="font-medium">{label}</span> :{" "}
                            {d
                                ? t("firstMile.sources.kmMin").replace("{{km}}", d.distanceKm.toFixed(2)).replace("{{min}}", d.durationMinutes.toFixed(1))
                                : t("firstMile.sources.noDistanceData")}
                        </p>
                    ))}
                </div>
            ) : null}
        </div>
    );
}

interface SourcesMapProps {
    sources: SourcePoint[];
    selectedSourceId?: string | null;
    onSourceSelect?: (sourceId: string) => void;
    onClearSelection?: () => void;
    distanceViewMode?: "HUB_SOC" | "SOC_HUB";
    className?: string;
}

export default function SourcesMap({ sources, selectedSourceId = null, onSourceSelect, onClearSelection, distanceViewMode, className = "" }: SourcesMapProps) {
    const { t } = useLanguage();
    // A marker's own popup; a new selection from the table replaces it (Leaflet's openOn closed it).
    const [clicked, setClicked] = useState<string | null>(null);
    const [shownSelection, setShownSelection] = useState(selectedSourceId);
    if (shownSelection !== selectedSourceId) {
        setShownSelection(selectedSourceId);
        setClicked(null);
    }

    const pointsWithCoords = useMemo(
        () =>
            sources.filter(
                (s): s is PlacedSource =>
                    s.latitude != null && s.longitude != null && Number.isFinite(s.latitude) && Number.isFinite(s.longitude)
            ),
        [sources]
    );

    const center = useMemo(() => {
        if (pointsWithCoords.length === 0) return THAILAND_CENTER;
        const lat = pointsWithCoords.reduce((a, p) => a + p.latitude, 0) / pointsWithCoords.length;
        const lng = pointsWithCoords.reduce((a, p) => a + p.longitude, 0) / pointsWithCoords.length;
        return { lat, lng };
    }, [pointsWithCoords]);

    const boundsPoints = useMemo(() => pointsWithCoords.map((p) => ({ lat: p.latitude, lng: p.longitude })), [pointsWithCoords]);

    const selected = selectedSourceId ? pointsWithCoords.find((s) => s.id === selectedSourceId || s.source_id === selectedSourceId) : undefined;
    const getStationTypeLabel = (type: string) => t(`firstMile.hub.stationType.${type}`) || type;
    const mode = distanceViewMode ?? "HUB_SOC";

    return (
        <div className={`relative z-0 isolate h-full min-h-[320px] w-full rounded-md overflow-hidden border bg-muted/30 ${className}`}>
            <GoogleMap defaultCenter={center} defaultZoom={pointsWithCoords.length <= 1 ? 12 : 10}>
                <FitBounds points={boundsPoints} skip={!!selectedSourceId} />
                <PanTo position={selected ? { lat: selected.latitude, lng: selected.longitude } : null} zoom={16} />
                {pointsWithCoords.map((src, idx) => {
                    const sourceKey = src.id ?? src.source_id;
                    const popup =
                        clicked === sourceKey ? (
                            <div className="text-sm">
                                <p className="font-semibold">{src.source_id}</p>
                                <p className="text-muted-foreground">{src.source_name_en}</p>
                                {src.source_name_th && src.source_name_th !== src.source_name_en && (
                                    <p className="text-muted-foreground text-xs">{src.source_name_th}</p>
                                )}
                                {src.station_type && <p className="text-xs mt-1">{getStationTypeLabel(src.station_type)}</p>}
                            </div>
                        ) : clicked == null && src === selected ? (
                            <SelectedSourceDetails key={`${src.source_id}:${mode}`} selected={src} mode={mode} />
                        ) : null;
                    return (
                        <MarkerWithPopup
                            key={src.id ?? `${src.source_id}-${src.latitude}-${src.longitude}-${idx}`}
                            position={{ lat: src.latitude, lng: src.longitude }}
                            title={src.source_id}
                            onClick={() => {
                                setClicked(sourceKey);
                                onSourceSelect?.(sourceKey);
                            }}
                            popup={popup}
                            onPopupClose={() => (clicked === sourceKey ? setClicked(null) : onClearSelection?.())}
                        />
                    );
                })}
            </GoogleMap>
        </div>
    );
}
