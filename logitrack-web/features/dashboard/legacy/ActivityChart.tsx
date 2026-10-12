"use client";

import { useState, useEffect, useMemo } from "react";
import { collection, query, orderBy, getDocs, limit } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { useLanguage } from "@/context/language";

const DAY_LABELS_WEEK = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];
const CHART_WIDTH = 700;
const CHART_HEIGHT = 200;
const PADDING = 20;

function getDateKey(d: Date): string {
  return d.toISOString().slice(0, 10);
}

function getStartOfDay(d: Date): Date {
  const out = new Date(d);
  out.setHours(0, 0, 0, 0);
  return out;
}

export function ActivityChart() {
  const { t } = useLanguage();
  const [period, setPeriod] = useState<"Week" | "Month">("Week");
  const [loading, setLoading] = useState(true);
  const [dailyCounts, setDailyCounts] = useState<{ date: string; count: number }[]>([]);
  const [previousTotal, setPreviousTotal] = useState<number>(0);

  const daysBack = period === "Week" ? 7 : 30;

  useEffect(() => {
    let cancelled = false;
    async function fetchDeliveredTrips() {
      setLoading(true);
      try {
        const end = getStartOfDay(new Date());
        end.setDate(end.getDate() + 1);
        const startCurrent = getStartOfDay(new Date());
        startCurrent.setDate(startCurrent.getDate() - daysBack);
        const startPrevious = getStartOfDay(new Date());
        startPrevious.setDate(startPrevious.getDate() - daysBack * 2);

        const q = query(
          collection(db, COLLECTIONS.TRIP_RECORDS),
          orderBy("createdAt", "desc"),
          limit(1000)
        );
        const snapshot = await getDocs(q);
        const byDay: Record<string, number> = {};
        let currentTotal = 0;
        let prevTotal = 0;

        snapshot.forEach((doc) => {
          if (cancelled) return;
          const data = doc.data();
          if (data.status !== "delivered") return;
          const ts =
            data.deliveredTimestamp?.toDate?.() ??
            data.updatedAt?.toDate?.() ??
            data.createdAt?.toDate?.();
          if (!ts) return;
          const date = new Date(ts);
          if (date >= startPrevious && date < end) {
            const key = getDateKey(date);
            byDay[key] = (byDay[key] ?? 0) + 1;
            if (date >= startCurrent) currentTotal += 1;
            else prevTotal += 1;
          }
        });

        if (!cancelled) {
          const sorted = Object.entries(byDay)
            .filter(([date]) => {
              const d = new Date(date);
              return d >= startCurrent && d < end;
            })
            .map(([date, count]) => ({ date, count }))
            .sort((a, b) => a.date.localeCompare(b.date));
          setDailyCounts(sorted);
          setPreviousTotal(prevTotal);
        }
      } catch (err) {
        console.error("[ActivityChart] Failed to fetch trip_records:", err);
        if (!cancelled) setDailyCounts([]);
      } finally {
        if (!cancelled) setLoading(false);
      }
    }
    fetchDeliveredTrips();
    return () => {
      cancelled = true;
    };
  }, [daysBack]);

  const { pathData, lineData, total, trendPercent, xLabels } = useMemo(() => {
    if (dailyCounts.length === 0) {
      const zeroPath = `M0 ${CHART_HEIGHT - PADDING} L${CHART_WIDTH} ${CHART_HEIGHT - PADDING} L${CHART_WIDTH} ${CHART_HEIGHT} L0 ${CHART_HEIGHT} Z`;
      const zeroLine = `M0 ${CHART_HEIGHT - PADDING} L${CHART_WIDTH} ${CHART_HEIGHT - PADDING}`;
      return {
        pathData: zeroPath,
        lineData: zeroLine,
        total: 0,
        trendPercent: previousTotal === 0 ? null : 100,
        xLabels: period === "Week" ? DAY_LABELS_WEEK : Array.from({ length: Math.min(7, 30) }, (_, i) => `${i + 1}`),
      };
    }

    const total = dailyCounts.reduce((s, d) => s + d.count, 0);
    const maxCount = Math.max(1, ...dailyCounts.map((d) => d.count));
    const chartTop = PADDING;
    const chartBottom = CHART_HEIGHT - PADDING;
    const rangeY = chartBottom - chartTop;
    const stepX = (CHART_WIDTH - PADDING * 2) / Math.max(1, dailyCounts.length - 1);
    const points: { x: number; y: number }[] = dailyCounts.map((d, i) => ({
      x: PADDING + i * stepX,
      y: chartBottom - (d.count / maxCount) * rangeY,
    }));

    const linePath = points.map((p, i) => `${i === 0 ? "M" : "L"} ${p.x} ${p.y}`).join(" ");
    const areaPath =
      linePath +
      ` L ${points[points.length - 1].x} ${chartBottom} L ${points[0].x} ${chartBottom} Z`;
    const labels =
      period === "Week"
        ? dailyCounts.map((d) => DAY_LABELS_WEEK[new Date(d.date).getDay()] ?? d.date.slice(5))
        : dailyCounts.map((d) => d.date.slice(8));

    let trendPercent: number | null = null;
    if (previousTotal > 0 && total !== previousTotal) {
      trendPercent = Math.round(((total - previousTotal) / previousTotal) * 100);
    } else if (previousTotal === 0 && total > 0) {
      trendPercent = 100;
    }

    return {
      pathData: areaPath,
      lineData: linePath,
      total,
      trendPercent,
      xLabels: labels,
    };
  }, [dailyCounts, previousTotal, period]);

  if (loading) {
    return (
      <div className="bg-card border border-border rounded-xl p-6 shadow-sm col-span-1 lg:col-span-2">
        <div className="flex justify-between items-start mb-8">
          <div>
            <h3 className="text-lg font-semibold text-foreground">{t("dashboard.activity.title")}</h3>
            <p className="text-sm text-muted-foreground">
              {period === "Week" ? t("dashboard.activity.weekly") : t("dashboard.activity.monthly")}
            </p>
          </div>
        </div>
        <div className="h-[250px] flex items-center justify-center text-muted-foreground text-sm">
          {t("dashboard.activity.loading")}
        </div>
      </div>
    );
  }

  return (
    <div className="bg-card border border-border rounded-xl p-6 shadow-sm col-span-1 lg:col-span-2">
      <div className="flex justify-between items-start mb-8">
        <div>
          <h3 className="text-lg font-semibold text-foreground">{t("dashboard.activity.title")}</h3>
          <p className="text-sm text-muted-foreground">
            {period === "Week" ? t("dashboard.activity.weekly") : t("dashboard.activity.monthly")}
          </p>
        </div>
        <div className="flex bg-muted/50 rounded-lg p-1">
          <button
            onClick={() => setPeriod("Week")}
            className={`px-3 py-1 text-xs font-medium rounded-md transition-all ${
              period === "Week" ? "bg-background shadow-sm text-foreground" : "text-muted-foreground hover:text-foreground"
            }`}
          >
            {t("dashboard.activity.week")}
          </button>
          <button
            onClick={() => setPeriod("Month")}
            className={`px-3 py-1 text-xs font-medium rounded-md transition-all ${
              period === "Month"
                ? "bg-background shadow-sm text-foreground"
                : "text-muted-foreground hover:text-foreground"
            }`}
          >
            {t("dashboard.activity.month")}
          </button>
        </div>
      </div>

      <div className="mb-6">
        <h2 className="text-4xl font-bold text-foreground">{total.toLocaleString()}</h2>
        <div className="flex items-center gap-2 mt-1">
          {trendPercent !== null ? (
            <>
              <span
                className={`text-sm font-medium ${
                  trendPercent >= 0 ? "text-green-500" : "text-red-500"
                }`}
              >
                {trendPercent >= 0 ? "↑" : "↓"} {Math.abs(trendPercent)}%
              </span>
              <span className="text-muted-foreground text-sm">
                {t("dashboard.activity.vsPrevious", { period: period === "Week" ? t("dashboard.activity.week") : t("dashboard.activity.month") })}
              </span>
            </>
          ) : (
            <span className="text-muted-foreground text-sm">
              {t("dashboard.activity.vsPrevious", { period: period === "Week" ? t("dashboard.activity.week") : t("dashboard.activity.month") })}
            </span>
          )}
        </div>
      </div>

      <div className="h-[250px] w-full relative overflow-hidden">
        <svg
          viewBox={`0 0 ${CHART_WIDTH} ${CHART_HEIGHT}`}
          className="w-full h-full"
          preserveAspectRatio="none"
        >
          <defs>
            <linearGradient id="chartGradient" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="#3b82f6" stopOpacity={0.5} />
              <stop offset="100%" stopColor="#3b82f6" stopOpacity={0} />
            </linearGradient>
          </defs>
          <path d={pathData} fill="url(#chartGradient)" />
          <path
            d={lineData}
            fill="none"
            stroke="#3b82f6"
            strokeWidth="4"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
        <div className="flex justify-between text-xs text-muted-foreground mt-4 px-2">
          {xLabels.map((label, i) => (
            <span key={i}>{label}</span>
          ))}
        </div>
      </div>
    </div>
  );
}
