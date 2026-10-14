"use client";

/**
 * The delivered-trips histogram of the dashboard over Go (T54R): the summary's `activity` block
 * (`reporting:view_analytics`), one value per Bangkok day of the period, zero days included, with the
 * total of the window before it and the trend Go computed. Replaces the 1000-trip sample counted in the
 * browser (`ActivityChart.tsx:45-81`), which was wrong once the period held more trips than the sample.
 */
import { useMemo } from "react";

import { useLanguage } from "@/context/language";
import type { ActivityBlockDTO, DashboardPeriodName } from "../api/summary";

const CHART_WIDTH = 700;
const CHART_HEIGHT = 200;
const PADDING = 20;

/** The SVG paths of the histogram (an area under a line), in a CHART_WIDTH x CHART_HEIGHT box. */
export function chartPaths(values: readonly number[]): { area: string; line: string } {
    const bottom = CHART_HEIGHT - PADDING;
    if (values.length === 0) {
        return {
            area: `M0 ${bottom} L${CHART_WIDTH} ${bottom} L${CHART_WIDTH} ${CHART_HEIGHT} L0 ${CHART_HEIGHT} Z`,
            line: `M0 ${bottom} L${CHART_WIDTH} ${bottom}`,
        };
    }
    const max = Math.max(1, ...values);
    const range = bottom - PADDING;
    const step = (CHART_WIDTH - PADDING * 2) / Math.max(1, values.length - 1);
    const points = values.map((v, i) => ({ x: PADDING + i * step, y: bottom - (v / max) * range }));
    const line = points.map((p, i) => `${i === 0 ? "M" : "L"} ${p.x} ${p.y}`).join(" ");
    return { line, area: `${line} L ${points[points.length - 1].x} ${bottom} L ${points[0].x} ${bottom} Z` };
}

export function DeliveryActivityChart({ activity, period }: { activity: ActivityBlockDTO; period: DashboardPeriodName }) {
    const { t, language } = useLanguage();
    const { area, line } = useMemo(() => chartPaths(activity.days.map((d) => d.delivered)), [activity.days]);
    const labels = useMemo(() => {
        const weekday = new Intl.DateTimeFormat(language === "th" ? "th-TH" : "en-US", { weekday: "short", timeZone: "UTC" });
        return activity.days.map((d, i) => {
            const day = new Date(`${d.date}T00:00:00Z`);
            if (period === "week") return weekday.format(day);
            // A month shows every fifth day and the last one, so the axis stays readable.
            return i % 5 === 0 || i === activity.days.length - 1 ? String(day.getUTCDate()) : "";
        });
    }, [activity.days, language, period]);
    const periodWord = period === "week" ? t("dashboard.activity.week") : t("dashboard.activity.month");

    return (
        <div className="bg-card border border-border rounded-xl p-6 shadow-sm" data-testid="activity-chart">
            <div className="mb-6">
                <h2 className="text-lg font-semibold text-foreground">{t("dashboard.activity.title")}</h2>
                <p className="text-sm text-muted-foreground">{period === "week" ? t("dashboard.activity.weekly") : t("dashboard.activity.monthly")}</p>
            </div>

            <div className="mb-6">
                <p className="text-4xl font-semibold text-foreground tabular-nums">{activity.total.toLocaleString()}</p>
                <div className="flex items-center gap-2 mt-1">
                    {activity.trendPct !== null && (
                        <span className={`text-sm font-medium ${activity.trendPct >= 0 ? "text-green-500" : "text-red-500"}`} data-testid="activity-trend">
                            {activity.trendPct >= 0 ? "↑" : "↓"} {Math.abs(activity.trendPct)}%
                        </span>
                    )}
                    <span className="text-muted-foreground text-sm">{t("dashboard.activity.vsPrevious", { period: periodWord })}</span>
                </div>
            </div>

            <div className="h-[250px] w-full relative overflow-hidden">
                <svg viewBox={`0 0 ${CHART_WIDTH} ${CHART_HEIGHT}`} className="w-full h-full" preserveAspectRatio="none" role="img" aria-label={t("dashboard.activity.title")}>
                    <defs>
                        <linearGradient id="deliveryActivityGradient" x1="0" y1="0" x2="0" y2="1">
                            <stop offset="0%" stopColor="#3b82f6" stopOpacity={0.5} />
                            <stop offset="100%" stopColor="#3b82f6" stopOpacity={0} />
                        </linearGradient>
                    </defs>
                    <path d={area} fill="url(#deliveryActivityGradient)" />
                    <path d={line} fill="none" stroke="#3b82f6" strokeWidth="4" strokeLinecap="round" strokeLinejoin="round" />
                </svg>
                <div className="flex justify-between text-xs text-muted-foreground mt-4 px-2">
                    {labels.map((label, i) => (
                        <span key={activity.days[i].date}>{label}</span>
                    ))}
                </div>
            </div>
        </div>
    );
}
