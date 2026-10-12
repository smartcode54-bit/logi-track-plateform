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

import { useLanguage } from "@/context/language";
import { hasIncident } from "../api/incidentMarkers";
import type { VehicleLocationDTO } from "../api/vehicleLocationEvents";

const DEFAULT_CENTER: L.LatLngExpression = [13.7563, 100.5018];
const DEFAULT_ZOOM = 6;

function escapeHtml(text: string): string {
    return text.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&#39;");
}

function truckIcon(plate: string, engineOn: boolean, incident: boolean) {
    const borderColor = incident ? "#ef4444" : engineOn ? "#22c55e" : "#94a3b8";
    const dotBg = engineOn ? "#22c55e" : "#94a3b8";
    const iconSrc = incident ? "/exclamation_8848378.png" : "/truck_2555001.png";
    return L.divIcon({
        className: "dashboard-vehicle-marker",
        html: `
      <div style="display:flex;flex-direction:column;align-items:center;">
        <div style="background:#ffffff;border-radius:50%;width:32px;height:32px;display:flex;align-items:center;justify-content:center;box-shadow:0 2px 4px rgba(0,0,0,0.2);border:2px solid ${borderColor};overflow:hidden;">
          <img src="${iconSrc}" alt="" style="width:20px;height:20px;object-fit:contain;" />
        </div>
        <span style="margin-top:2px;padding:2px 6px;border-radius:4px;font-size:10px;font-weight:600;background:#f8fafc;color:#0f172a;border:1px solid #e2e8f0;box-shadow:0 1px 2px rgba(0,0,0,0.05);white-space:nowrap;">
          <span style="display:inline-block;width:6px;height:6px;border-radius:50%;background:${dotBg};margin-right:3px;"></span>${escapeHtml(plate)}
        </span>
      </div>`,
        iconSize: [48, 40],
        iconAnchor: [24, 40],
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
        const on = t("dashboard.vehicleMap.engineOn");
        const off = t("dashboard.vehicleMap.engineOff");
        for (const v of vehicles) {
            const plate = v.licensePlate?.trim() || "—";
            const incident = hasIncident(v, incidentTruckIds, incidentPlates);
            const marker = L.marker([v.lat, v.lng], { icon: truckIcon(plate, v.engineOn === true, incident) }).addTo(map);
            const lines = [`<p style="font-weight:600;margin:0 0 4px;">${escapeHtml(plate)}</p>`];
            if (v.engineOn !== undefined && v.engineOn !== null) lines.push(`<p style="margin:0 0 2px;color:#6b7280;">${v.engineOn ? `🟢 ${escapeHtml(on)}` : `⚪ ${escapeHtml(off)}`}</p>`);
            if (v.speedKmh !== null) lines.push(`<p style="margin:0;font-size:11px;">${escapeHtml(t("dashboard.vehicleMap.speed"))}: ${Math.round(v.speedKmh)} km/h</p>`);
            if (typeof v.odometerKm === "number") {
                lines.push(`<p style="margin:2px 0 0;font-size:11px;">${escapeHtml(t("dashboard.vehicleMap.odometer"))}: ${v.odometerKm.toLocaleString(undefined, { maximumFractionDigits: 1 })} km</p>`);
            }
            if (typeof v.fuelLevel === "number") {
                const pct = typeof v.fuelPercentage === "number" ? ` (${v.fuelPercentage}%)` : "";
                lines.push(`<p style="margin:2px 0 0;font-size:11px;">${escapeHtml(t("dashboard.vehicleMap.fuel"))}: ${v.fuelLevel} L${pct}</p>`);
            }
            if (incident) lines.push(`<p style="margin:4px 0 0;font-size:11px;color:#ef4444;font-weight:600;">${escapeHtml(t("dashboard.vehicleMap.incidentReported"))}</p>`);
            marker.bindPopup(`<div style="font-size:13px;min-width:140px;">${lines.join("")}</div>`);
            markersRef.current.push(marker);
        }
        // Fit once the first positions arrive; later batches only move the markers, not the view.
        if (!fittedRef.current && vehicles.length > 0) {
            fittedRef.current = true;
            fit();
        }
    }, [vehicles, incidentTruckIds, incidentPlates, t, fit]);

    return (
        <div className="relative h-[500px] min-h-[400px] w-full bg-muted/30">
            <button
                type="button"
                onClick={fit}
                className="absolute right-2 top-2 z-[1000] inline-flex items-center gap-1 rounded bg-background/90 px-2 py-1 text-xs font-medium text-primary shadow hover:bg-muted"
            >
                {t("dashboard.vehicleMap.centerAll")}
            </button>
            <div ref={containerRef} style={{ height: "100%", width: "100%" }} className="relative h-full w-full overflow-hidden z-0" />
        </div>
    );
}
