"use client";

import { useState, useEffect } from "react";
import Link from "next/link";
import { collection, query, orderBy, getDocs, limit } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { Truck, Users, AlertTriangle, Wrench, Loader2, LucideIcon, CheckCircle } from "lucide-react";
import { useLanguage } from "@/context/language";

type UpdateType = "delivered" | "driver" | "incident" | "maintenance";

interface FeedItem {
  id: string;
  type: UpdateType;
  title: string;
  meta: string;
  timestamp: Date;
  icon: LucideIcon;
  iconBg: string;
  iconColor: string;
  link?: string;
}

function formatTimeAgo(d: Date, t: (key: string, params?: Record<string, string | number>) => string): string {
  const now = new Date();
  const sec = Math.floor((now.getTime() - d.getTime()) / 1000);
  if (sec < 60) return t("dashboard.updates.time.justNow");
  if (sec < 3600) return t("dashboard.updates.time.minutesAgo", { minutes: Math.floor(sec / 60) });
  if (sec < 86400) return t("dashboard.updates.time.hoursAgo", { hours: Math.floor(sec / 3600) });
  if (sec < 604800) return t("dashboard.updates.time.daysAgo", { days: Math.floor(sec / 86400) });
  return d.toLocaleDateString();
}

export function RecentUpdates() {
  const { t } = useLanguage();
  const [items, setItems] = useState<FeedItem[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    async function fetchFeed() {
      setLoading(true);
      try {
        const feed: FeedItem[] = [];

        const [
          tripsSnap,
          driversSnap,
          incidentsSnap,
          maintenanceSnap,
        ] = await Promise.all([
          getDocs(
            query(
              collection(db, COLLECTIONS.TRIP_RECORDS),
              orderBy("createdAt", "desc"),
              limit(50)
            )
          ),
          getDocs(
            query(
              collection(db, COLLECTIONS.DRIVERS),
              orderBy("updatedAt", "desc"),
              limit(10)
            )
          ),
          getDocs(
            query(
              collection(db, COLLECTIONS.INCIDENT_REPORTS),
              orderBy("createdAt", "desc"),
              limit(10)
            )
          ).catch(() => null),
          getDocs(
            query(
              collection(db, COLLECTIONS.MAINTENANCE),
              orderBy("createdAt", "desc"),
              limit(20)
            )
          ).catch(() => null),
        ]);

        tripsSnap.forEach((doc) => {
          const d = doc.data();
          if (d.status !== "delivered") return;
          const ts =
            d.deliveredTimestamp?.toDate?.() ??
            d.updatedAt?.toDate?.() ??
            d.createdAt?.toDate?.();
          if (!ts) return;
          const dest = d.destination || d.origin || "Hub";
          const plate = d.licensePlate || "";
          feed.push({
            id: `trip-${doc.id}`,
            type: "delivered",
            title: plate ? `${t("dashboard.updates.type.delivered")} · ${plate}` : t("dashboard.updates.type.delivered"),
            meta: `${formatTimeAgo(ts, t)} · ${dest}`,
            timestamp: ts,
            icon: Truck,
            iconBg: "bg-green-500/10",
            iconColor: "text-green-500",
            link: "/app/driver-monitor",
          });
        });

        driversSnap.forEach((doc) => {
          const d = doc.data();
          const ts = d.updatedAt?.toDate?.() ?? d.createdAt?.toDate?.();
          if (!ts) return;
          const name = [d.firstName, d.lastName].filter(Boolean).join(" ") || "Driver";
          feed.push({
            id: `driver-${doc.id}`,
            type: "driver",
            title: t("dashboard.updates.type.driver"),
            meta: `${formatTimeAgo(ts, t)} · ${name}`,
            timestamp: ts,
            icon: CheckCircle,
            iconBg: "bg-blue-500/10",
            iconColor: "text-blue-500",
            link: "/app/drivers",
          });
        });

        if (incidentsSnap) {
          incidentsSnap.forEach((doc) => {
            const d = doc.data();
            const ts = d.createdAt?.toDate?.() ?? d.updatedAt?.toDate?.();
            if (!ts) return;
            const cause = d.delayCause || d.description || "Route delay";
            feed.push({
              id: `incident-${doc.id}`,
              type: "incident",
              title: t("dashboard.updates.type.incident"),
              meta: `${formatTimeAgo(ts, t)} · ${typeof cause === "string" ? cause.slice(0, 40) : "Incident"}`,
              timestamp: ts,
              icon: AlertTriangle,
              iconBg: "bg-yellow-500/10",
              iconColor: "text-yellow-500",
              link: "/app/incident-reports",
            });
          });
        }

        if (maintenanceSnap) {
          maintenanceSnap.forEach((doc) => {
            const d = doc.data();
            if (d.status !== "completed") return;
            const ts =
              d.updatedAt?.toDate?.() ??
              (typeof d.endDate === "string" ? new Date(d.endDate) : null) ??
              d.createdAt?.toDate?.();
            if (!ts) return;
            const service = d.serviceType || "Maintenance";
            feed.push({
              id: `maint-${doc.id}`,
              type: "maintenance",
              title: t("dashboard.updates.type.maintenance"),
              meta: `${formatTimeAgo(ts, t)} · ${typeof service === "string" ? service.slice(0, 30) : "Service"}`,
              timestamp: ts,
              icon: Wrench,
              iconBg: "bg-gray-500/10",
              iconColor: "text-gray-500",
              link: "/app/maintenance",
            });
          });
        }

        feed.sort((a, b) => b.timestamp.getTime() - a.timestamp.getTime());
        setItems(feed.slice(0, 8));
      } catch (err) {
        console.error("[RecentUpdates] Failed to fetch feed:", err);
        setItems([]);
      } finally {
        setLoading(false);
      }
    }
    fetchFeed();
  }, []);

  return (
    <div className="bg-card border border-border rounded-xl p-6 shadow-sm col-span-1 h-full">
      <h3 className="text-lg font-semibold text-foreground mb-1">{t("dashboard.updates.title")}</h3>
      <p className="text-sm text-muted-foreground mb-6">{t("dashboard.updates.subtitle")}</p>

      {loading ? (
        <div className="flex justify-center py-8">
          <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
        </div>
      ) : items.length === 0 ? (
        <p className="text-sm text-muted-foreground py-4">{t("dashboard.updates.noActivity")}</p>
      ) : (
        <div className="space-y-6">
          {items.map((item) => {
            const content = (
              <div key={item.id} className="flex gap-4">
                <div
                  className={`p-3 rounded-full h-fit ${item.iconBg} ${item.iconColor}`}
                >
                  <item.icon className="w-5 h-5" />
                </div>
                <div>
                  <h4 className="font-medium text-sm text-foreground">{item.title}</h4>
                  <p className="text-xs text-muted-foreground mt-0.5">{item.meta}</p>
                </div>
              </div>
            );
            return item.link ? (
              <Link
                key={item.id}
                href={item.link}
                className="block hover:bg-muted/50 rounded-lg -m-2 p-2 transition-colors"
              >
                {content}
              </Link>
            ) : (
              <div key={item.id}>{content}</div>
            );
          })}
        </div>
      )}

      <div className="mt-8 pt-4 border-t border-border">
        <button className="w-full text-center text-sm font-medium text-blue-500 hover:text-blue-600 transition-colors">
          {t("dashboard.updates.viewAll")}
        </button>
      </div>
    </div>
  );
}
