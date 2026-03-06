"use client";

import { useState, useEffect } from "react";
import Link from "next/link";
import { collection, getDocs, limit, query } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { MapPin, ExternalLink, Loader2, Truck, RefreshCw } from "lucide-react";
import { useLanguage } from "@/context/language";

interface VehicleLocation {
  id: string;
  licensePlate: string;
  positionDescription: string;
  lat: number;
  lng: number;
  engineOn: boolean;
  speed: number;
  updatedAt?: unknown;
}

export function CurrentVehiclePosition() {
  const { t } = useLanguage();
  const [vehicles, setVehicles] = useState<VehicleLocation[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const fetchData = async () => {
    setLoading(true);
    setError(null);
    try {
      const locSnap = await getDocs(
        query(collection(db, COLLECTIONS.VEHICLE_LOCATIONS), limit(50))
      );

      const list: VehicleLocation[] = [];
      locSnap.docs.forEach((doc) => {
        const d = doc.data();
        const lat = d.lat;
        const lng = d.lng;
        if (typeof lat === "number" && typeof lng === "number" && Number.isFinite(lat) && Number.isFinite(lng)) {
          list.push({
            id: doc.id,
            licensePlate: d.licensePlate ?? "—",
            positionDescription: d.positionDescription ?? "",
            lat,
            lng,
            engineOn: d.engineOn ?? false,
            speed: d.speed ?? 0,
            updatedAt: d.updatedAt,
          });
        }
      });
      setVehicles(list);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to fetch");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchData();
  }, []);

  return (
    <Card className="bg-card border border-border">
      <CardHeader className="pb-2">
        <div className="flex items-center justify-between">
          <h3 className="text-sm font-semibold text-foreground flex items-center gap-2">
            <Truck className="h-4 w-4 text-muted-foreground" />
            {t("dashboard.vehiclePosition.title", "ตำแหน่งรถปัจจุบัน")}
          </h3>
          <Link
            href="/admin/driver-monitor"
            className="text-xs font-medium text-primary hover:underline"
          >
            {t("dashboard.vehiclePosition.viewAll", "ดูทั้งหมด")}
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
        ) : vehicles.length === 0 ? (
          <p className="text-sm text-muted-foreground py-4 text-center">
            {t("dashboard.vehiclePosition.noVehicles", "ไม่มีรถที่มีข้อมูลตำแหน่ง")}
          </p>
        ) : (
          <ul className="space-y-2 max-h-[280px] overflow-y-auto">
            {vehicles.map((v) => (
              <li
                key={v.id}
                className="flex items-center justify-between gap-2 p-2 rounded-lg bg-muted/40 hover:bg-muted/60 transition-colors"
              >
                <div className="min-w-0 flex-1">
                  <p className="text-sm font-medium text-foreground truncate flex items-center gap-1.5">
                    {v.engineOn ? "🟢" : "⚪"}
                    {v.licensePlate}
                  </p>
                  {v.positionDescription && (
                    <p className="text-xs text-muted-foreground truncate mt-0.5">
                      {v.positionDescription}
                    </p>
                  )}
                </div>
                <a
                  href={`https://www.google.com/maps?q=${v.lat},${v.lng}`}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="shrink-0 inline-flex items-center gap-1 text-xs text-primary hover:underline"
                  title={`${v.lat.toFixed(5)}, ${v.lng.toFixed(5)}`}
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
