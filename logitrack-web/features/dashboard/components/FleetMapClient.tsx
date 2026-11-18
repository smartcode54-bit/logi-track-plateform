"use client";

/**
 * The Google map of the dashboard over Go (T54R), loaded only through `FleetMap` (`next/dynamic`, so
 * the maps library stays out of the route's initial JS, developer-spec.md §10.11). It draws the positions of
 * `['vehicleLocations']` and marks in red the trucks named by the loaded incident page; it fetches
 * nothing itself.
 */
import { useCallback, useEffect, useRef, useState } from "react";
import { useMap } from "@vis.gl/react-google-maps";

import { GoogleMap, MarkerWithPopup, fitPoints } from "@/components/map/GoogleMap";
import { Button } from "@/components/ui/button";
import { useLanguage } from "@/context/language";
import { hasIncident } from "../api/incidentMarkers";
import type { VehicleLocationDTO } from "../api/vehicleLocationEvents";

const DEFAULT_CENTER = { lat: 13.7563, lng: 100.5018 };
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

export interface FleetMapProps {
    vehicles: readonly VehicleLocationDTO[];
    /** Trucks named by the loaded incident page, by id and by plate. */
    incidentTruckIds: ReadonlySet<string>;
    incidentPlates: ReadonlySet<string>;
}

export function FleetMapClient({ vehicles, incidentTruckIds, incidentPlates }: FleetMapProps) {
    const { t } = useLanguage();
    const [open, setOpen] = useState<string | null>(null);
    return (
        <div className="relative h-[500px] min-h-[400px] w-full bg-muted/30">
            <GoogleMap defaultCenter={DEFAULT_CENTER} defaultZoom={DEFAULT_ZOOM}>
                <CenterAll vehicles={vehicles} label={t("dashboard.vehicleMap.centerAll")} />
                {/* The markers follow the cache: a new Cartrack batch (vehicle_locations.updated) moves them in place. */}
                {vehicles.map((v) => {
                    const plate = v.licensePlate?.trim() || "—";
                    const incident = hasIncident(v, incidentTruckIds, incidentPlates);
                    return (
                        <MarkerWithPopup
                            key={v.truckId}
                            position={{ lat: v.lat, lng: v.lng }}
                            title={plate}
                            onClick={() => setOpen(v.truckId)}
                            popup={open === v.truckId ? <div dangerouslySetInnerHTML={{ __html: fleetPopupHtml(v, incident, t) }} /> : null}
                            onPopupClose={() => setOpen(null)}
                        >
                            {/* fleetMarkerHtml escapes the plate. */}
                            <div className="dashboard-vehicle-marker" dangerouslySetInnerHTML={{ __html: fleetMarkerHtml(plate, v.engineOn === true, incident) }} />
                        </MarkerWithPopup>
                    );
                })}
            </GoogleMap>
        </div>
    );
}

/** "Center all": fits every truck; also runs once when the first positions arrive (later batches keep the view). */
function CenterAll({ vehicles, label }: { vehicles: readonly VehicleLocationDTO[]; label: string }) {
    const map = useMap();
    const fit = useCallback(() => {
        if (!map) return;
        if (vehicles.length === 0) {
            map.setCenter(DEFAULT_CENTER);
            map.setZoom(DEFAULT_ZOOM);
            return;
        }
        fitPoints(map, vehicles, { padding: 40, maxZoom: 15, singleZoom: 14 });
    }, [map, vehicles]);
    const fitted = useRef(false);
    useEffect(() => {
        if (fitted.current || !map || vehicles.length === 0) return;
        fitted.current = true;
        fit();
    }, [map, vehicles, fit]);
    return (
        <Button variant="outline" size="sm" type="button" onClick={fit} className="absolute right-2 top-2 z-raised">
            {label}
        </Button>
    );
}
