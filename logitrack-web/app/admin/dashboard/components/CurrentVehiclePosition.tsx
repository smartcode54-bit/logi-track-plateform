"use client";

import { useState, useEffect } from "react";
import Link from "next/link";
import { collection, query, where, getDocs, limit } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { MapPin, ExternalLink, Loader2, Truck, RefreshCw } from "lucide-react";
import { useLanguage } from "@/context/language";

interface TripWithPosition {
  id: string;
  driverId?: string;
  status: string;
  lat: number;
  lng: number;
  updatedAt?: unknown;
  origin?: string;
  destination?: string;
}

interface DriverInfo {
  id: string;
  firstName?: string;
  lastName?: string;
  currentAssignment?: { truckPlate?: string };
}

export function CurrentVehiclePosition() {
  const { t } = useLanguage();
  const [trips, setTrips] = useState<TripWithPosition[]>([]);
  const [drivers, setDrivers] = useState<Record<string, DriverInfo>>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const fetchData = async () => {
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

      const driversMap: Record<string, DriverInfo> = {};
      driversSnap.docs.forEach((d) => {
        const data = d.data();
        driversMap[d.id] = {
          id: d.id,
          firstName: data.firstName,
          lastName: data.lastName,
          currentAssignment: data.currentAssignment,
        };
        if (data.authId) driversMap[data.authId] = driversMap[d.id];
      });
      setDrivers(driversMap);

      const list: TripWithPosition[] = [];
      tripsSnap.docs.forEach((doc) => {
        const d = doc.data();
        const lat = d.lat ?? d.latitude;
        const lng = d.lng ?? d.longitude;
        if (typeof lat === "number" && typeof lng === "number" && Number.isFinite(lat) && Number.isFinite(lng)) {
          list.push({
            id: doc.id,
            driverId: d.driverId,
            status: d.status ?? "",
            lat,
            lng,
            updatedAt: d.updatedAt,
            origin: d.origin,
            destination: d.destination,
          });
        }
      });
      setTrips(list);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to fetch");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchData();
  }, []);

  const getDriverLabel = (driverId?: string) => {
    if (!driverId) return "—";
    const driver = drivers[driverId];
    if (!driver) return driverId.slice(0, 8);
    const name = [driver.firstName, driver.lastName].filter(Boolean).join(" ").trim() || "—";
    const plate = driver.currentAssignment?.truckPlate;
    return plate ? `${name} (${plate})` : name;
  };

  const statusLabel: Record<string, string> = {
    departure: t("dashboard.vehiclePosition.statusDeparture", "Departure"),
    in_transit: t("dashboard.vehiclePosition.statusInTransit", "In transit"),
  };

  return (
    <Card className="bg-card border border-border">
      <CardHeader className="pb-2">
        <div className="flex items-center justify-between">
          <h3 className="text-sm font-semibold text-foreground flex items-center gap-2">
            <Truck className="h-4 w-4 text-muted-foreground" />
            {t("dashboard.vehiclePosition.title", "Current vehicle position")}
          </h3>
          <Link
            href="/admin/driver-monitor"
            className="text-xs font-medium text-primary hover:underline"
          >
            {t("dashboard.vehiclePosition.viewAll", "View all")}
          </Link>
        </div>
      </CardHeader>
      <CardContent className="space-y-2">
        {loading ? (
          <div className="flex justify-center py-6">
            <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
          </div>
        ) : error ? (
          <div className="py-4 px-2 text-center space-y-2">
            <p className="text-sm text-muted-foreground">
              {t("dashboard.vehiclePosition.loadError", "Unable to load vehicle positions.")}
            </p>
            <button
              type="button"
              onClick={() => fetchData()}
              className="inline-flex items-center gap-1.5 text-xs font-medium text-primary hover:underline"
            >
              <RefreshCw className="h-3.5 w-3.5" />
              {t("dashboard.vehiclePosition.retry", "Retry")}
            </button>
          </div>
        ) : trips.length === 0 ? (
          <p className="text-sm text-muted-foreground py-4 text-center">
            {t("dashboard.vehiclePosition.noVehicles", "No vehicles with position data.")}
          </p>
        ) : (
          <ul className="space-y-2 max-h-[280px] overflow-y-auto">
            {trips.map((trip) => (
              <li
                key={trip.id}
                className="flex items-center justify-between gap-2 p-2 rounded-lg bg-muted/40 hover:bg-muted/60 transition-colors"
              >
                <div className="min-w-0 flex-1">
                  <p className="text-sm font-medium text-foreground truncate">
                    {getDriverLabel(trip.driverId)}
                  </p>
                  <p className="text-xs text-muted-foreground">
                    {statusLabel[trip.status] ?? trip.status}
                  </p>
                </div>
                <a
                  href={`https://www.google.com/maps?q=${trip.lat},${trip.lng}`}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="shrink-0 inline-flex items-center gap-1 text-xs text-primary hover:underline"
                  title={`${trip.lat.toFixed(5)}, ${trip.lng.toFixed(5)}`}
                >
                  <MapPin className="h-3.5 w-3.5" />
                  {t("dashboard.vehiclePosition.openMap", "Map")}
                  <ExternalLink className="h-3 w-3" />
                </a>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}
