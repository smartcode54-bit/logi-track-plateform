"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { MapContainer, TileLayer, Marker, Popup, useMap } from "react-leaflet";
import "leaflet/dist/leaflet.css";
import L from "leaflet";
import { collection, getDocs, query, where } from "firebase/firestore";
import { db } from "@/firebase/client";
import { useLanguage } from "@/context/language";
import { COLLECTIONS } from "@/lib/collections";
import { SOC_KEYS, socIdMatchesKey } from "@/validate/firstMileTaskSchema";

const defaultIcon = L.icon({
    iconUrl: "https://unpkg.com/leaflet@1.9.4/dist/images/marker-icon.png",
    shadowUrl: "https://unpkg.com/leaflet@1.9.4/dist/images/marker-shadow.png",
    iconSize: [25, 41],
    iconAnchor: [12, 41],
});

export interface SourcePoint {
    id?: string;
    source_id: string;
    source_name_en: string;
    latitude?: number;
    longitude?: number;
    station_type?: string;
}

interface FitBoundsProps {
    points: { lat: number; lng: number }[];
    skipWhenSelected: boolean;
    selectedSourceId: string | null;
}

/** เปิดการลากแผนที่เมื่อพร้อม และให้ map ได้รับ pointer events */
function MapDragEnabler() {
    const map = useMap();
    useEffect(() => {
        map.whenReady(() => {
            map.dragging.enable();
            const el = map.getContainer();
            if (el) {
                el.style.cursor = "grab";
                el.addEventListener("mousedown", () => { el.style.cursor = "grabbing"; });
                el.addEventListener("mouseup", () => { el.style.cursor = "grab"; });
                el.addEventListener("mouseleave", () => { el.style.cursor = "grab"; });
            }
        });
    }, [map]);
    return null;
}

function FitBounds({ points, skipWhenSelected, selectedSourceId }: FitBoundsProps) {
    const map = useMap();
    useEffect(() => {
        if (skipWhenSelected && selectedSourceId) return;
        if (points.length === 0) return;
        const run = () => {
            map.invalidateSize();
            if (points.length === 1) {
                map.setView(points[0], 14);
                return;
            }
            const bounds = L.latLngBounds(points);
            map.fitBounds(bounds, { padding: [24, 24], maxZoom: 14 });
        };
        map.whenReady(run);
        const t = setTimeout(run, 150);
        return () => clearTimeout(t);
    }, [map, points, skipWhenSelected, selectedSourceId]);
    return null;
}

/** เมื่อเลือกแถวตามราง: บินไปที่พิกัดและเปิด popup แสดงรายละเอียดจุดรับส่ง + ระยะทางไป SOC (ถ้าเป็น Hub) */
function SelectedSourcePopup({
    selectedSourceId,
    sources,
    getStationTypeLabel,
    t,
    onClearSelection,
}: {
    selectedSourceId: string | null;
    sources: (SourcePoint & { latitude: number; longitude: number })[];
    getStationTypeLabel: (type: string) => string;
    t: (key: string) => string;
    onClearSelection?: () => void;
}) {
    const map = useMap();
    const popupRef = useRef<L.Popup | null>(null);
    const selected = useMemo(
        () => sources.find((s) => s.id === selectedSourceId || s.source_id === selectedSourceId),
        [sources, selectedSourceId]
    );
    const [hubDistances, setHubDistances] = useState<{ socId: string; distanceKm: number; durationMinutes: number }[]>([]);

    useEffect(() => {
        if (!selected) {
            map.closePopup();
            popupRef.current = null;
            setHubDistances([]);
            return;
        }
        const latlng: L.LatLngExpression = [selected.latitude, selected.longitude];
        map.flyTo(latlng, 16, { duration: 0.5 });

        const buildContent = (distances: { socId: string; distanceKm: number; durationMinutes: number }[]) => {
            const getBySoc = (key: string) => distances.find((d) => socIdMatchesKey(d.socId, key));
            let distanceRows = "";
            if (selected!.station_type === "HUB") {
                const kmMinTpl = (() => {
                    const s = t("firstMile.sources.kmMin");
                    return s && !s.includes("kmMin") ? s : "{{km}} km / {{min}} min";
                })();
                const noDataLabel = (() => {
                    const s = t("firstMile.sources.noDistanceData");
                    return s && !s.includes("noDistanceData") ? s : "—";
                })();
                distanceRows = SOC_KEYS.map((key) => {
                    const d = getBySoc(key);
                    const value = d
                        ? kmMinTpl.replace("{{km}}", d.distanceKm.toFixed(2)).replace("{{min}}", d.durationMinutes.toFixed(1))
                        : noDataLabel;
                    return `<p class="text-xs mt-0"><span class="font-medium">${key}</span> : ${escapeHtml(value)}</p>`;
                }).join("");
            }
            const distanceToSocLabel = (() => {
                const s = t("firstMile.sources.distanceToSoc");
                return s && !s.includes("distanceToSoc") ? s : "Distance to SOC";
            })();
            return `
            <div class="px-1.5 py-0.5 text-sm min-w-[140px] max-h-[200px] overflow-y-auto">
                <p class="text-muted-foreground text-xs leading-tight">${escapeHtml(selected!.source_name_en)}</p>
                ${distanceRows ? `<div class="mt-1 pt-1 border-t border-border"><p class="text-xs font-medium text-muted-foreground mb-0.5 leading-tight">${escapeHtml(distanceToSocLabel)}</p>${distanceRows}</div>` : ""}
            </div>
        `;
        };

        const content = buildContent(hubDistances);
        const popup = L.popup({ className: "sources-map-selected-popup" })
            .setLatLng(latlng)
            .setContent(content)
            .openOn(map);
        popupRef.current = popup;

        if (selected.station_type === "HUB") {
            getDocs(query(collection(db, COLLECTIONS.HUB_SOC_DISTANCES), where("hubId", "==", selected.source_id)))
                .then((snap) => {
                    const list = snap.docs.map((d) => {
                        const data = d.data();
                        return {
                            socId: (data.socId ?? "") as string,
                            distanceKm: Number(data.distanceKm ?? 0),
                            durationMinutes: Number(data.durationMinutes ?? 0),
                        };
                    });
                    setHubDistances(list);
                })
                .catch(() => setHubDistances([]));
        } else {
            setHubDistances([]);
        }

        return () => {
            map.removeLayer(popup);
            popupRef.current = null;
        };
    }, [map, selected, getStationTypeLabel, selectedSourceId]);

    useEffect(() => {
        if (!selected || !popupRef.current) return;
        const getBySoc = (key: string) => hubDistances.find((d) => socIdMatchesKey(d.socId, key));
        let distanceRows = "";
        if (selected.station_type === "HUB") {
            const kmMinTpl = (() => {
                const s = t("firstMile.sources.kmMin");
                return s && !s.includes("kmMin") ? s : "{{km}} km / {{min}} min";
            })();
            const noDataLabel = (() => {
                const s = t("firstMile.sources.noDistanceData");
                return s && !s.includes("noDistanceData") ? s : "—";
            })();
            distanceRows = SOC_KEYS.map((key) => {
                const d = getBySoc(key);
                const value = d
                    ? kmMinTpl.replace("{{km}}", d.distanceKm.toFixed(2)).replace("{{min}}", d.durationMinutes.toFixed(1))
                    : noDataLabel;
                return `<p class="text-xs mt-0"><span class="font-medium">${key}</span> : ${escapeHtml(value)}</p>`;
            }).join("");
        }
        const distanceToSocLabel = (() => {
            const s = t("firstMile.sources.distanceToSoc");
            return s && !s.includes("distanceToSoc") ? s : "Distance to SOC";
        })();
        const content = `
            <div class="px-1.5 py-0.5 text-sm min-w-[140px] max-h-[200px] overflow-y-auto">
                <p class="text-muted-foreground text-xs leading-tight">${escapeHtml(selected.source_name_en)}</p>
                ${distanceRows ? `<div class="mt-1 pt-1 border-t border-border"><p class="text-xs font-medium text-muted-foreground mb-0.5 leading-tight">${escapeHtml(distanceToSocLabel)}</p>${distanceRows}</div>` : ""}
            </div>
        `;
        popupRef.current.setContent(content);
    }, [hubDistances, selected, getStationTypeLabel, t]);

    return null;
}

function escapeHtml(text: string): string {
    const div = document.createElement("div");
    div.textContent = text;
    return div.innerHTML;
}

interface SourcesMapProps {
    sources: SourcePoint[];
    selectedSourceId?: string | null;
    onSourceSelect?: (sourceId: string) => void;
    onClearSelection?: () => void;
    className?: string;
}

export default function SourcesMap({ sources, selectedSourceId = null, onSourceSelect, onClearSelection, className = "" }: SourcesMapProps) {
    const { t } = useLanguage();
    const [mapId] = useState(() => `sources-map-${Math.random().toString(36).slice(2, 9)}`);

    const pointsWithCoords = useMemo(
        () =>
            sources.filter(
                (s): s is SourcePoint & { latitude: number; longitude: number } =>
                    s.latitude != null &&
                    s.longitude != null &&
                    Number.isFinite(s.latitude) &&
                    Number.isFinite(s.longitude)
            ),
        [sources]
    );

    const center = useMemo(() => {
        if (pointsWithCoords.length === 0) return { lat: 13.7563, lng: 100.5018 };
        const lat =
            pointsWithCoords.reduce((a, p) => a + p.latitude!, 0) / pointsWithCoords.length;
        const lng =
            pointsWithCoords.reduce((a, p) => a + p.longitude!, 0) / pointsWithCoords.length;
        return { lat, lng };
    }, [pointsWithCoords]);

    const boundsPoints = useMemo(
        () => pointsWithCoords.map((p) => ({ lat: p.latitude!, lng: p.longitude! })),
        [pointsWithCoords]
    );

    useEffect(() => {
        return () => {
            const container = L.DomUtil.get(mapId);
            if (container) {
                // @ts-expect-error cleanup for react strict mode
                container._leaflet_id = null;
            }
        };
    }, [mapId]);

    const getStationTypeLabel = (type: string) => t(`firstMile.hub.stationType.${type}` as any) || type;

    return (
        <div className={`relative z-0 isolate h-full min-h-[320px] w-full rounded-md overflow-hidden border bg-muted/30 ${className}`}>
            <MapContainer
                id={mapId}
                key={mapId}
                center={center}
                zoom={pointsWithCoords.length <= 1 ? 12 : 10}
                style={{ height: "100%", width: "100%" }}
                scrollWheelZoom
                dragging
            >
                <TileLayer
                    attribution='&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a>'
                    url="https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png"
                />
                <MapDragEnabler />
                <FitBounds points={boundsPoints} skipWhenSelected={!!selectedSourceId} selectedSourceId={selectedSourceId} />
                <SelectedSourcePopup
                    selectedSourceId={selectedSourceId}
                    sources={pointsWithCoords}
                    getStationTypeLabel={getStationTypeLabel}
                    t={t}
                    onClearSelection={onClearSelection}
                />
                {pointsWithCoords.map((src, idx) => {
                    const sourceKey = src.id ?? src.source_id;
                    return (
                        <Marker
                            key={src.id ?? `${src.source_id}-${src.latitude}-${src.longitude}-${idx}`}
                            position={[src.latitude!, src.longitude!]}
                            icon={defaultIcon}
                            eventHandlers={{
                                click: () => {
                                    if (onSourceSelect) onSourceSelect(sourceKey);
                                },
                            }}
                        >
                            <Popup>
                                <div className="text-sm">
                                    <p className="font-semibold">{src.source_id}</p>
                                    <p className="text-muted-foreground">{src.source_name_en}</p>
                                    {src.station_type && (
                                        <p className="text-xs mt-1">{getStationTypeLabel(src.station_type)}</p>
                                    )}
                                </div>
                            </Popup>
                        </Marker>
                    );
                })}
            </MapContainer>
        </div>
    );
}
