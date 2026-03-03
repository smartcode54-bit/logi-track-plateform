"use client";

import { useEffect, useMemo, useState } from "react";
import { MapContainer, TileLayer, Marker, Popup, useMap } from "react-leaflet";
import "leaflet/dist/leaflet.css";
import L from "leaflet";
import { collection, query, where, getDocs, limit } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { useLanguage } from "@/context/language";

interface VehiclePoint {
  id: string;
  driverId?: string;
  driverName: string;
  licensePlate: string;
  status: string;
  lat: number;
  lng: number;
}

const DEFAULT_CENTER: [number, number] = [13.7563, 100.5018];
const DEFAULT_ZOOM = 6;

function escapeHtml(text: string): string {
  return text
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

/** สร้างไอคอนรูปบรรทุก + Label ทะเบียน */
function createTruckIcon(licensePlate: string) {
  const plate = licensePlate || "—";
  return L.divIcon({
    className: "dashboard-vehicle-marker",
    html: `
      <div style="display:flex;flex-direction:column;align-items:center;">
        <div style="background:#0f172a;color:#f8fafc;border-radius:6px;padding:4px;box-shadow:0 1px 3px rgba(0,0,0,0.2);border:1px solid #334155;">
          <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M14 18V6a2 2 0 0 0-2-2H4a2 2 0 0 0-2 2v11a1 1 0 0 0 1 1h2"/>
            <path d="M15 18h2"/>
            <path d="M19 18h2v-3.65a1 1 0 0 0-.22-.624l-3.48-4.35A1 1 0 0 0 17.52 9H14"/>
          </svg>
        </div>
        <span style="margin-top:2px;padding:2px 6px;border-radius:4px;font-size:10px;font-weight:600;background:#f8fafc;color:#0f172a;border:1px solid #e2e8f0;box-shadow:0 1px 2px rgba(0,0,0,0.05);white-space:nowrap;">${escapeHtml(plate)}</span>
      </div>
    `,
    iconSize: [48, 40],
    iconAnchor: [24, 40],
  });
}

function FitBounds({ points }: { points: { lat: number; lng: number }[] }) {
  const map = useMap();
  useEffect(() => {
    if (points.length === 0) return;
    const run = () => {
      map.invalidateSize();
      if (points.length === 1) {
        map.setView([points[0].lat, points[0].lng], 14);
        return;
      }
      const bounds = L.latLngBounds(points.map((p) => [p.lat, p.lng] as [number, number]));
      map.fitBounds(bounds, { padding: [24, 24], maxZoom: 14 });
    };
    map.whenReady(run);
    const t = setTimeout(run, 300);
    return () => clearTimeout(t);
  }, [map, points]);
  return null;
}

export function DashboardVehicleMapClient() {
  const { t } = useLanguage();
  const [vehicles, setVehicles] = useState<VehiclePoint[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    async function fetchData() {
      setLoading(true);
      setError(null);
      try {
        const [tripsSnap, driversSnap] = await Promise.all([
          getDocs(
            query(
              collection(db, COLLECTIONS.TRIP_RECORDS),
              where("status", "in", ["departure", "in_transit"]),
              limit(50)
            )
          ),
          getDocs(query(collection(db, COLLECTIONS.DRIVERS), limit(300))),
        ]);

        if (cancelled) return;

        const driversMap: Record<string, { firstName?: string; lastName?: string; currentAssignment?: { truckPlate?: string } }> = {};
        driversSnap.docs.forEach((d) => {
          const data = d.data();
          const info = {
            firstName: data.firstName,
            lastName: data.lastName,
            currentAssignment: data.currentAssignment,
          };
          driversMap[d.id] = info;
          if (data.authId) driversMap[data.authId] = info;
        });

        const list: VehiclePoint[] = [];
        tripsSnap.docs.forEach((doc) => {
          const d = doc.data();
          const lat = d.lat ?? d.latitude;
          const lng = d.lng ?? d.longitude;
          if (typeof lat === "number" && typeof lng === "number" && Number.isFinite(lat) && Number.isFinite(lng)) {
            const driver = driversMap[d.driverId] ?? driversMap[doc.id];
            const name = [driver?.firstName, driver?.lastName].filter(Boolean).join(" ").trim() || "—";
            const plate = driver?.currentAssignment?.truckPlate ?? "—";
            list.push({
              id: doc.id,
              driverId: d.driverId,
              driverName: name,
              licensePlate: plate,
              status: d.status ?? "",
              lat,
              lng,
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

  const boundsPoints = useMemo(() => vehicles.map((v) => ({ lat: v.lat, lng: v.lng })), [vehicles]);
  const center = useMemo(() => {
    if (vehicles.length === 0) return DEFAULT_CENTER;
    const lat = vehicles.reduce((a, v) => a + v.lat, 0) / vehicles.length;
    const lng = vehicles.reduce((a, v) => a + v.lng, 0) / vehicles.length;
    return [lat, lng] as [number, number];
  }, [vehicles]);

  const statusLabel: Record<string, string> = {
    departure: t("dashboard.vehiclePosition.statusDeparture", "Departure"),
    in_transit: t("dashboard.vehiclePosition.statusInTransit", "In transit"),
  };

  return (
    <div className="rounded-lg border border-border bg-card overflow-hidden">
      <div className="px-3 py-2 border-b border-border">
        <h3 className="text-sm font-semibold text-foreground">
          {t("dashboard.vehicleMap.title", "ตำแหน่งรถบนแผนที่")}
        </h3>
      </div>
      <div className="relative h-[320px] min-h-[280px] w-full bg-muted/30">
        {loading && (
          <div className="absolute inset-0 z-1000 flex items-center justify-center bg-background/60">
            <p className="text-sm text-muted-foreground">{t("dashboard.vehicleMap.loading", "Loading...")}</p>
          </div>
        )}
        {error && !loading && (
          <div className="absolute inset-0 z-1000 flex items-center justify-center bg-background/80">
            <p className="text-sm text-muted-foreground">{t("dashboard.vehiclePosition.loadError", "Unable to load vehicle positions.")}</p>
          </div>
        )}
        {!error && (
          <MapContainer
            center={center}
            zoom={vehicles.length <= 1 ? 12 : DEFAULT_ZOOM}
            style={{ height: "100%", width: "100%" }}
            scrollWheelZoom
            dragging
          >
            <TileLayer
              attribution='&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a>'
              url="https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png"
            />
            {boundsPoints.length > 0 && <FitBounds points={boundsPoints} />}
            {vehicles.map((v) => (
              <Marker key={v.id} position={[v.lat, v.lng]} icon={createTruckIcon(v.licensePlate)}>
                <Popup>
                  <div className="text-sm min-w-[140px]">
                    <p className="font-semibold text-foreground">{v.licensePlate}</p>
                    <p className="text-muted-foreground">{v.driverName}</p>
                    <p className="text-xs mt-1">{statusLabel[v.status] ?? v.status}</p>
                  </div>
                </Popup>
              </Marker>
            ))}
          </MapContainer>
        )}
      </div>
    </div>
  );
}
