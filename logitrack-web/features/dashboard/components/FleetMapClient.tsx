"use client";

/**
 * The Leaflet map of the dashboard over Go (T54R), loaded only through `FleetMap` (`next/dynamic`, so
 * leaflet stays out of the route's initial JS, developer-spec.md §10.11). It draws the positions of
 * `['vehicleLocations']` and marks in red the trucks named by the loaded incident page; it fetches
 * nothing itself.
 */
import { useCallback, useEffect, useLayoutEffect, useRef } from "react";
import "leaflet/dist/leaflet.css";
import L from "leaflet";

import { Button } from "@/components/ui/button";
import { useLanguage } from "@/context/language";
import { hasIncident } from "../api/incidentMarkers";
import type { VehicleLocationDTO } from "../api/vehicleLocationEvents";

const DEFAULT_CENTER: L.LatLngExpression = [13.7563, 100.5018];
const DEFAULT_ZOOM = 6;

function escapeHtml(text: string): string {
    return text.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&#39;");
}

export function fleetMarkerHtml(plate: string, engineOn: boolean, incident: boolean): string {
    const borderColor = incident ? "var(--destructive)" : engineOn ? "var(--success)" : "var(--muted-foreground)";
    const dotBg = engineOn ? "var(--success)" : "var(--muted-foreground)";
    const iconSrc = incident ? "/exclamation_8848378.png" : "/truck_2555001.png";
    return `
      <div style="display:flex;flex-direction:column;align-items:center;">
        <div style="background:var(--card);border-radius:50%;width:32px;height:32px;display:flex;align-items:center;justify-content:center;border:2px solid ${borderColor};overflow:hidden;">
          <img src="${escapeHtml(iconSrc)}" alt="" style="width:20px;height:20px;object-fit:contain;" />
        </div>
        <span style="margin-top:2px;padding:2px 6px;border-radius:4px;font-size:12px;font-weight:600;background:var(--card);color:var(--foreground);border:1px solid var(--border);white-space:nowrap;">
          <span aria-hidden="true" style="display:inline-block;width:6px;height:6px;border-radius:50%;background:${dotBg};margin-right:3px;"></span>${escapeHtml(plate)}
        </span>
      </div>`;
}

export function fleetPopupHtml(v: VehicleLocationDTO, incident: boolean, t: (key: string) => string): string {
    const plate = v.licensePlate?.trim() || "—";
    const lines = [`<p style="font-weight:600;margin:0 0 4px;">${escapeHtml(plate)}</p>`];
    if (v.engineOn !== undefined && v.engineOn !== null) {
        lines.push(`<p style="margin:0 0 2px;color:var(--muted-foreground);">${escapeHtml(t(v.engineOn ? "dashboard.vehicleMap.engineOn" : "dashboard.vehicleMap.engineOff"))}</p>`);
    }
    if (v.speedKmh !== null) lines.push(`<p style="margin:0;">${escapeHtml(t("dashboard.vehicleMap.speed"))}: ${escapeHtml(String(Math.round(v.speedKmh)))} km/h</p>`);
    if (typeof v.odometerKm === "number") {
        lines.push(`<p style="margin:2px 0 0;">${escapeHtml(t("dashboard.vehicleMap.odometer"))}: ${escapeHtml(v.odometerKm.toLocaleString(undefined, { maximumFractionDigits: 1 }))} km</p>`);
    }
    if (typeof v.fuelLevel === "number") {
        const pct = typeof v.fuelPercentage === "number" ? ` (${v.fuelPercentage}%)` : "";
        lines.push(`<p style="margin:2px 0 0;">${escapeHtml(t("dashboard.vehicleMap.fuel"))}: ${escapeHtml(String(v.fuelLevel))} L${escapeHtml(pct)}</p>`);
    }
    if (incident) lines.push(`<p style="margin:4px 0 0;color:var(--destructive);font-weight:600;">${escapeHtml(t("dashboard.vehicleMap.incidentReported"))}</p>`);
    return `<div style="font-size:12px;min-width:140px;background:var(--card);color:var(--foreground);">${lines.join("")}</div>`;
}

function truckIcon(plate: string, engineOn: boolean, incident: boolean) {
    return L.divIcon({
        className: "dashboard-vehicle-marker",
        html: fleetMarkerHtml(plate, engineOn, incident),
        iconSize: [48, 52],
        iconAnchor: [24, 52],
    });
}

export interface FleetMapProps {
    vehicles: readonly VehicleLocationDTO[];
    /** Trucks named by the loaded incident page, by id and by plate. */
    incidentTruckIds: ReadonlySet<string>;
    incidentPlates: ReadonlySet<string>;
}

export function FleetMapClient({ vehicles, incidentTruckIds, incidentPlates }: FleetMapProps) {
    const { t } = useLanguage();
    const containerRef = useRef<HTMLDivElement>(null);
    const mapRef = useRef<L.Map | null>(null);
    const markersRef = useRef<L.Marker[]>([]);

    useLayoutEffect(() => {
        if (!containerRef.current || mapRef.current) return;
        const map = L.map(containerRef.current, { center: DEFAULT_CENTER, zoom: DEFAULT_ZOOM, scrollWheelZoom: true, dragging: true });
        L.tileLayer("https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png", {
            attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a>',
        }).addTo(map);
        mapRef.current = map;
        const t0 = window.setTimeout(() => map.invalidateSize(), 0);
        const t1 = window.setTimeout(() => map.invalidateSize(), 200);
        return () => {
            window.clearTimeout(t0);
            window.clearTimeout(t1);
            map.remove();
            mapRef.current = null;
        };
    }, []);

    const fit = useCallback(() => {
        const map = mapRef.current;
        if (!map) return;
        map.invalidateSize();
        if (vehicles.length === 0) {
            map.setView(DEFAULT_CENTER, DEFAULT_ZOOM);
            return;
        }
        requestAnimationFrame(() =>
            requestAnimationFrame(() => {
                map.invalidateSize();
                if (vehicles.length === 1) map.setView([vehicles[0].lat, vehicles[0].lng], 14);
                else map.fitBounds(L.latLngBounds(vehicles.map((v) => [v.lat, v.lng] as [number, number])), { padding: [40, 40], maxZoom: 15 });
            })
        );
    }, [vehicles]);

    // The markers follow the cache: a new Cartrack batch (vehicle_locations.updated) moves them in place.
    const fittedRef = useRef(false);
    useEffect(() => {
        const map = mapRef.current;
        if (!map) return;
        for (const m of markersRef.current) m.remove();
        markersRef.current = [];
        for (const v of vehicles) {
            const plate = v.licensePlate?.trim() || "—";
            const incident = hasIncident(v, incidentTruckIds, incidentPlates);
            const marker = L.marker([v.lat, v.lng], { icon: truckIcon(plate, v.engineOn === true, incident) }).addTo(map);
            marker.bindPopup(fleetPopupHtml(v, incident, t));
            markersRef.current.push(marker);
        }
        // Fit once the first positions arrive; later batches only move the markers, not the view.
        if (!fittedRef.current && vehicles.length > 0) {
            fittedRef.current = true;
            fit();
        }
    }, [vehicles, incidentTruckIds, incidentPlates, t, fit]);

    return (
        <div className="relative h-[500px] min-h-[400px] w-full bg-muted/30 [&_.leaflet-popup-content-wrapper]:bg-card [&_.leaflet-popup-content-wrapper]:text-foreground [&_.leaflet-popup-tip]:bg-card">
            <Button
                variant="outline"
                size="sm"
                type="button"
                onClick={fit}
                className="absolute right-2 top-2 z-raised"
            >
                {t("dashboard.vehicleMap.centerAll")}
            </Button>
            <div ref={containerRef} className="relative h-full w-full overflow-hidden z-0" />
        </div>
    );
}
