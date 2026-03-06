"use client";

import { useEffect, useRef, useState, useCallback } from "react";
import "leaflet/dist/leaflet.css";
import L from "leaflet";
import { collection, getDocs, limit, query } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { useLanguage } from "@/context/language";

interface VehiclePoint {
  id: string;
  truckId: string;
  licensePlate: string;
  lat: number;
  lng: number;
  speed: number;
  engineOn: boolean;
  updatedAt: any;
}

const DEFAULT_CENTER: L.LatLngExpression = [13.7563, 100.5018];
const DEFAULT_ZOOM = 6;

function escapeHtml(text: string): string {
  return text
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

function createTruckIcon(licensePlate: string, engineOn: boolean) {
  const plate = licensePlate || "—";
  const borderColor = engineOn ? "#22c55e" : "#94a3b8";
  const dotBg = engineOn ? "#22c55e" : "#94a3b8";
  return L.divIcon({
    className: "dashboard-vehicle-marker",
    html: `
      <div style="display:flex;flex-direction:column;align-items:center;">
        <div style="background:#0f172a;color:#f8fafc;border-radius:6px;padding:4px;box-shadow:0 1px 3px rgba(0,0,0,0.2);border:2px solid ${borderColor};">
          <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M14 18V6a2 2 0 0 0-2-2H4a2 2 0 0 0-2 2v11a1 1 0 0 0 1 1h2"/>
            <path d="M15 18h2"/>
            <path d="M19 18h2v-3.65a1 1 0 0 0-.22-.624l-3.48-4.35A1 1 0 0 0 17.52 9H14"/>
          </svg>
        </div>
        <span style="margin-top:2px;padding:2px 6px;border-radius:4px;font-size:10px;font-weight:600;background:#f8fafc;color:#0f172a;border:1px solid #e2e8f0;box-shadow:0 1px 2px rgba(0,0,0,0.05);white-space:nowrap;">
          <span style="display:inline-block;width:6px;height:6px;border-radius:50%;background:${dotBg};margin-right:3px;"></span>${escapeHtml(plate)}
        </span>
      </div>
    `,
    iconSize: [48, 40],
    iconAnchor: [24, 40],
  });
}

export function DashboardVehicleMapClient() {
  const { t } = useLanguage();
  const [vehicles, setVehicles] = useState<VehiclePoint[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const mapContainerRef = useRef<HTMLDivElement>(null);
  const mapInstanceRef = useRef<L.Map | null>(null);
  const markersRef = useRef<L.Marker[]>([]);

  // Fetch data
  useEffect(() => {
    let cancelled = false;
    async function fetchData() {
      setLoading(true);
      setError(null);
      try {
        const locSnap = await getDocs(
          query(collection(db, COLLECTIONS.VEHICLE_LOCATIONS), limit(100))
        );
        if (cancelled) return;

        const list: VehiclePoint[] = [];
        locSnap.docs.forEach((doc) => {
          const d = doc.data();
          const lat = d.lat;
          const lng = d.lng;
          if (typeof lat === "number" && typeof lng === "number" && Number.isFinite(lat) && Number.isFinite(lng)) {
            list.push({
              id: doc.id,
              truckId: d.truckId ?? "",
              licensePlate: d.licensePlate ?? "—",
              lat,
              lng,
              speed: d.speed ?? 0,
              engineOn: d.engineOn ?? false,
              updatedAt: d.updatedAt,
            });
          }
        });
        setVehicles(list);
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : "Failed to fetch");
      } finally {
        if (!cancelled) setLoading(false);
      }
    }
    fetchData();
    return () => { cancelled = true; };
  }, []);

  // Initialize map (once) and clean up on unmount
  useEffect(() => {
    if (!mapContainerRef.current || mapInstanceRef.current) return;

    const map = L.map(mapContainerRef.current, {
      center: DEFAULT_CENTER,
      zoom: DEFAULT_ZOOM,
      scrollWheelZoom: true,
      dragging: true,
    });

    L.tileLayer("https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png", {
      attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a>',
    }).addTo(map);

    mapInstanceRef.current = map;

    // Ensure proper sizing after mount
    setTimeout(() => map.invalidateSize(), 200);

    return () => {
      map.remove();
      mapInstanceRef.current = null;
    };
  }, [loading]); // Re-run when loading changes (map div is conditionally shown)

  // Update markers when vehicles change
  useEffect(() => {
    const map = mapInstanceRef.current;
    if (!map || vehicles.length === 0) return;

    // Clear old markers
    markersRef.current.forEach((m) => m.remove());
    markersRef.current = [];

    const engineOnLabel = t("dashboard.vehicleMap.engineOn", "Engine ON");
    const engineOffLabel = t("dashboard.vehicleMap.engineOff", "Engine OFF");
    const speedLabel = t("dashboard.vehicleMap.speed", "Speed");

    // Add new markers
    vehicles.forEach((v) => {
      const marker = L.marker([v.lat, v.lng], {
        icon: createTruckIcon(v.licensePlate, v.engineOn),
      }).addTo(map);

      marker.bindPopup(`
        <div style="font-size:13px;min-width:140px;">
          <p style="font-weight:600;margin:0 0 4px;">${escapeHtml(v.licensePlate)}</p>
          <p style="margin:0 0 2px;color:#6b7280;">
            ${v.engineOn ? `🟢 ${engineOnLabel}` : `⚪ ${engineOffLabel}`}
          </p>
          <p style="margin:0;font-size:11px;">${speedLabel}: ${v.speed} km/h</p>
        </div>
      `);

      markersRef.current.push(marker);
    });

    // Fit bounds
    map.invalidateSize();
    if (vehicles.length === 1) {
      map.setView([vehicles[0].lat, vehicles[0].lng], 14);
    } else {
      const bounds = L.latLngBounds(vehicles.map((v) => [v.lat, v.lng] as [number, number]));
      map.fitBounds(bounds, { padding: [24, 24], maxZoom: 14 });
    }
  }, [vehicles, t]);

  // Center map to fit all vehicles
  const fitAllVehicles = useCallback(() => {
    const map = mapInstanceRef.current;
    if (!map || vehicles.length === 0) return;
    map.invalidateSize();
    if (vehicles.length === 1) {
      map.setView([vehicles[0].lat, vehicles[0].lng], 14);
    } else {
      const bounds = L.latLngBounds(vehicles.map((v) => [v.lat, v.lng] as [number, number]));
      map.fitBounds(bounds, { padding: [24, 24], maxZoom: 14 });
    }
  }, [vehicles]);

  return (
    <div className="rounded-lg border border-border bg-card overflow-hidden">
      <div className="px-3 py-2 border-b border-border flex items-center justify-between">
        <h3 className="text-sm font-semibold text-foreground">
          {t("dashboard.vehicleMap.title", "ตำแหน่งรถบนแผนที่")}
        </h3>
        {!loading && !error && vehicles.length > 0 && (
          <button
            type="button"
            onClick={fitAllVehicles}
            className="inline-flex items-center gap-1 text-xs font-medium text-primary hover:text-primary/80 transition-colors px-2 py-1 rounded hover:bg-muted"
          >
            <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="12" cy="12" r="10" />
              <circle cx="12" cy="12" r="3" />
            </svg>
            {t("dashboard.vehicleMap.centerAll", "Center")}
          </button>
        )}
      </div>
      <div className="relative h-[320px] min-h-[280px] w-full bg-muted/30">
        {loading && (
          <div className="absolute inset-0 z-[1000] flex items-center justify-center bg-background/60">
            <p className="text-sm text-muted-foreground">{t("dashboard.vehicleMap.loading", "Loading...")}</p>
          </div>
        )}
        {error && !loading && (
          <div className="absolute inset-0 z-[1000] flex items-center justify-center bg-background/80">
            <p className="text-sm text-muted-foreground">{t("dashboard.vehiclePosition.loadError", "Unable to load vehicle positions.")}</p>
          </div>
        )}
        {!error && !loading && (
          <div
            ref={mapContainerRef}
            style={{ height: "100%", width: "100%" }}
          />
        )}
      </div>
    </div>
  );
}
