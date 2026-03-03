"use client";

import { useMemo } from "react";

function parseLatLng(refillLocation: string | null | undefined): { lat: number; lng: number } | null {
    if (!refillLocation || typeof refillLocation !== "string") return null;
    const trimmed = refillLocation.trim();
    const parts = trimmed.split(/[,،\s]+/).map((s) => s.trim()).filter(Boolean);
    if (parts.length < 2) return null;
    const lat = parseFloat(parts[0]);
    const lng = parseFloat(parts[1]);
    if (Number.isNaN(lat) || Number.isNaN(lng)) return null;
    if (lat < -90 || lat > 90 || lng < -180 || lng > 180) return null;
    return { lat, lng };
}

/** สร้าง OSM embed URL แสดงจุด lat,lng (ไม่ใช้ Leaflet เพื่อเลี่ยง error ใน Modal) */
function getEmbedUrl(lat: number, lng: number, zoom = 15): string {
    const delta = 0.01 * Math.pow(2, 15 - zoom);
    const bbox = [lng - delta, lat - delta, lng + delta, lat + delta].join(",");
    const marker = `${lat},${lng}`;
    const params = new URLSearchParams({
        bbox,
        layer: "mapnik",
        marker,
    });
    return `https://www.openstreetmap.org/export/embed.html?${params.toString()}`;
}

interface RefillLocationMapProps {
    refillLocation: string | null | undefined;
    className?: string;
    height?: string;
    noCoordsLabel?: string;
}

export function RefillLocationMap({
    refillLocation,
    className = "",
    height = "200px",
    noCoordsLabel = "ไม่มีพิกัด (lat,lng)",
}: RefillLocationMapProps) {
    const position = useMemo(() => parseLatLng(refillLocation), [refillLocation]);

    if (!position) {
        return (
            <div
                className={`rounded-md border border-dashed border-muted-foreground/30 bg-muted/20 flex items-center justify-center text-muted-foreground text-sm ${className}`}
                style={{ height }}
            >
                {noCoordsLabel}
            </div>
        );
    }

    const embedUrl = getEmbedUrl(position.lat, position.lng);

    return (
        <div
            className={`rounded-md overflow-hidden border border-border bg-muted/20 ${className}`}
            style={{ height, minHeight: "200px" }}
        >
            <iframe
                title={noCoordsLabel}
                src={embedUrl}
                className="w-full h-full border-0 block"
                loading="lazy"
                referrerPolicy="no-referrer-when-downgrade"
            />
        </div>
    );
}
