"use client";

import { useEffect, useState } from "react";
import { collection, query, where, getCountFromServer, getDocs, orderBy, limit } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { Users, MapPin, Package, Zap } from "lucide-react";
import { useLanguage } from "@/context/language";

interface StatCardProps {
  title: string;
  value: string;
  trend: string;
  isPositive: boolean | null;
  icon: React.ElementType;
  iconBgColor: string;
  iconColor: string;
  highlightSecondary?: boolean;
}

function StatCard({
  title,
  value,
  trend,
  isPositive,
  icon: Icon,
  iconBgColor,
  iconColor,
  highlightSecondary,
}: StatCardProps) {
  let valueNode: React.ReactNode = value;
  if (highlightSecondary && value.includes("/")) {
    const [first, second] = value.split("/");
    valueNode = (
      <span>
        {first.trim()}{" "}
        <span className="text-yellow-400 font-semibold">/ {second.trim()}</span>
      </span>
    );
  }

  return (
    <div className="bg-card border border-border rounded-xl p-6 shadow-sm hover:shadow-md transition-shadow">
      <div className="flex justify-between items-start mb-4">
        <div className={`p-3 rounded-lg ${iconBgColor} ${iconColor}`}>
          <Icon className="w-6 h-6" />
        </div>
        {trend !== "—" && (
          <div
            className={`flex items-center gap-1 text-sm font-medium ${
              isPositive === true ? "text-green-500" : isPositive === false ? "text-red-500" : "text-muted-foreground"
            }`}
          >
            <span>{isPositive !== false ? (isPositive === true ? "+" : "") : ""}{trend}</span>
            {isPositive !== null && (
              <svg
                xmlns="http://www.w3.org/2000/svg"
                width="16"
                height="16"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
                strokeLinejoin="round"
                className={isPositive ? "" : "rotate-180"}
              >
                <polyline points="23 6 13.5 15.5 8.5 10.5 1 18"></polyline>
                <polyline points="17 6 23 6 23 12"></polyline>
              </svg>
            )}
          </div>
        )}
      </div>
      <div>
        <p className="text-muted-foreground text-xs font-semibold uppercase tracking-wider mb-1">{title}</p>
        <h3 className="text-3xl font-bold text-foreground">{valueNode}</h3>
      </div>
    </div>
  );
}

export function DashboardStats() {
  const { t } = useLanguage();
  const [loading, setLoading] = useState(true);
  const [totalUsers, setTotalUsers] = useState(0);
  const [activeDrivers, setActiveDrivers] = useState(0);
  const [deliveredTrips, setDeliveredTrips] = useState(0);
  const [totalPackages, setTotalPackages] = useState(0);
  const [fleetEfficiency, setFleetEfficiency] = useState<number | null>(null);
  const [fleetDelayRate, setFleetDelayRate] = useState<number | null>(null);

  useEffect(() => {
    async function fetchStats() {
      setLoading(true);
      try {
        // Web users only (exclude role "driver" — drivers are counted in the Driver card from drivers collection)
        const [
          adminSnap,
          managerSnap,
          opStaffSnap,
          operatorSnap,
          partnerSnap,
          driversActiveSnap,
          driversOnDutySnap,
          driversAllSnap,
          deliveredSnap,
          deliveredDocsSnap,
          recentTripsSnap,
          incidentsSnap,
        ] = await Promise.all([
          getCountFromServer(query(collection(db, COLLECTIONS.USERS), where("role", "==", "admin"))),
          getCountFromServer(query(collection(db, COLLECTIONS.USERS), where("role", "==", "manager"))),
          getCountFromServer(query(collection(db, COLLECTIONS.USERS), where("role", "==", "operation_staff"))),
          getCountFromServer(query(collection(db, COLLECTIONS.USERS), where("role", "==", "operator"))),
          getCountFromServer(query(collection(db, COLLECTIONS.USERS), where("role", "==", "partner"))),
          // Drivers currently working or available: Active (พร้อมทำงาน) + On-Duty (กำลังปฏิบัติงาน)
          getCountFromServer(
            query(collection(db, COLLECTIONS.DRIVERS), where("status", "==", "Active"))
          ).catch(() => null),
          getCountFromServer(
            query(collection(db, COLLECTIONS.DRIVERS), where("status", "==", "On-Duty"))
          ).catch(() => null),
          getCountFromServer(collection(db, COLLECTIONS.DRIVERS)),
          getCountFromServer(
            query(collection(db, COLLECTIONS.TRIP_RECORDS), where("status", "==", "delivered"))
          ),
          getDocs(
            query(
              collection(db, COLLECTIONS.TRIP_RECORDS),
              where("status", "==", "delivered"),
              limit(5000)
            )
          ).catch(() => null),
          getDocs(
            query(
              collection(db, COLLECTIONS.TRIP_RECORDS),
              orderBy("createdAt", "desc"),
              limit(500)
            )
          ).catch(() => null),
          getDocs(
            query(
              collection(db, COLLECTIONS.INCIDENT_REPORTS),
              orderBy("createdAt", "desc"),
              limit(2000)
            )
          ).catch(() => null),
        ]);

        const webUserCount =
          adminSnap.data().count +
          managerSnap.data().count +
          opStaffSnap.data().count +
          operatorSnap.data().count +
          partnerSnap.data().count;
        setTotalUsers(webUserCount);
        const activeCount =
          (driversActiveSnap?.data().count ?? 0) + (driversOnDutySnap?.data().count ?? 0) ||
          driversAllSnap.data().count;
        setActiveDrivers(activeCount);
        setDeliveredTrips(deliveredSnap.data().count);

        let allDeliveredPackages = 0;

        // เที่ยวส่งสำเร็จทั้งหมด เทียบกับ จำนวนเที่ยวที่แจ้งรายงานปัญหา (On-time / Delay)
        const incidentTripIds = new Set<string>();
        if (incidentsSnap && !incidentsSnap.empty) {
          incidentsSnap.forEach((doc) => {
            const d = doc.data() as { tripId?: string | null };
            if (d.tripId) incidentTripIds.add(String(d.tripId));
          });
        }

        const deliveredIds = new Set<string>();
        if (deliveredDocsSnap && !deliveredDocsSnap.empty) {
          deliveredDocsSnap.forEach((doc) => deliveredIds.add(doc.id));
        }

        let delayedCount = 0;
        deliveredIds.forEach((id) => {
          if (incidentTripIds.has(id)) delayedCount += 1;
        });
        const totalDeliveredForFleet = deliveredIds.size;
        if (totalDeliveredForFleet > 0) {
          const onTimePct = Math.round(((totalDeliveredForFleet - delayedCount) / totalDeliveredForFleet) * 1000) / 10;
          const delayPct = Math.round((delayedCount / totalDeliveredForFleet) * 1000) / 10;
          setFleetEfficiency(onTimePct);
          setFleetDelayRate(delayPct);
        } else {
          setFleetEfficiency(null);
          setFleetDelayRate(null);
        }

        // Package count จากเที่ยวส่งสำเร็จ (recent trips)
        if (recentTripsSnap && !recentTripsSnap.empty) {
          recentTripsSnap.forEach((doc) => {
            const d = doc.data();
            if (d.status === "delivered") {
              let pkgCount = 0;
              const rawPkg =
                d.packageCount ?? d.totalPackages ?? d.packages ?? d.parcelCount ?? d.totalParcels;
              if (typeof rawPkg === "number") {
                pkgCount = rawPkg;
              } else if (typeof rawPkg === "string") {
                const parsed = parseInt(rawPkg, 10);
                if (!Number.isNaN(parsed)) pkgCount = parsed;
              }
              allDeliveredPackages += pkgCount;
            }
          });
          setTotalPackages(allDeliveredPackages);
        } else {
          setTotalPackages(0);
        }
      } catch (err) {
        console.error("[DashboardStats] Failed to fetch stats:", err);
      } finally {
        setLoading(false);
      }
    }
    fetchStats();
  }, []);

  if (loading) {
    return (
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-6 mb-6">
        {[1, 2, 3, 4].map((i) => (
          <div
            key={i}
            className="bg-card border border-border rounded-xl p-6 shadow-sm animate-pulse flex items-center gap-4"
          >
            <div className="h-12 w-12 rounded-lg bg-muted" />
            <div className="flex-1">
              <div className="h-3 w-16 bg-muted rounded mb-2" />
              <div className="h-8 w-20 bg-muted rounded" />
            </div>
          </div>
        ))}
      </div>
    );
  }

  return (
    <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-6 mb-6">
      <StatCard
        title={t("dashboard.stats.totalUsers")}
        value={totalUsers.toLocaleString()}
        trend="—"
        isPositive={null}
        icon={Users}
        iconBgColor="bg-blue-500/10"
        iconColor="text-blue-500"
      />
      <StatCard
        title={t("dashboard.stats.activeDrivers")}
        value={activeDrivers.toLocaleString()}
        trend="—"
        isPositive={null}
        icon={MapPin}
        iconBgColor="bg-sky-500/10"
        iconColor="text-sky-500"
      />
      <StatCard
        title={t("dashboard.stats.tripsAndPackages")}
        value={`${deliveredTrips.toLocaleString()} / ${totalPackages.toLocaleString()}`}
        trend="—"
        isPositive={null}
        icon={Package}
        iconBgColor="bg-indigo-500/10"
        iconColor="text-indigo-500"
      />
      <StatCard
        title={t("dashboard.stats.fleetEfficiency")}
        value={
          fleetEfficiency != null && fleetDelayRate != null
            ? `${fleetEfficiency}% / ${fleetDelayRate}%`
            : "—"
        }
        trend="—"
        isPositive={null}
        icon={Zap}
        iconBgColor="bg-yellow-500/10"
        iconColor="text-yellow-500"
        highlightSecondary={fleetEfficiency != null && fleetDelayRate != null}
      />
    </div>
  );
}
