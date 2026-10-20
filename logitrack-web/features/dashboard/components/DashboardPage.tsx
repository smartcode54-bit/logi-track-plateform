"use client";

/**
 * The dashboard over Go (T54R, issue #74; developer-spec.md §10.13; Appendix E §E.2.1), the only source
 * since TW7. The page is a handful of requests instead of about 10.7k document
 * reads and two listeners:
 *
 * - `GET /v1/dashboard/summary?from&to` (`['dashboard','summary',{from,to}]`, 60 s poll): the trips,
 *   drivers, activity, recent-updates and compliance blocks, each present only for its capability;
 * - `GET /v1/stats/users-by-role` (`security:view_overview`, shared with the Security Center);
 * - `GET /v1/vehicle-locations` (`['vehicleLocations']`, the map and the position list; SSE batches);
 * - the first page of `GET /v1/incidents?from&to` (the map's incident markers, "latest N");
 * - `GET /v1/badges` (`['badges']`, the sidebar's key: pending expenses and chat counters).
 *
 * One period switch (the last 7 or 30 Bangkok days) drives every figure. Permission checks are
 * selectors over `['me']`; a principal without a block's capability gets no request and no card for it.
 */
import { useMemo, useState } from "react";
import { Loader2, RefreshCw } from "lucide-react";

import { PageHeader } from "@/components/page-header";
import { useLanguage } from "@/context/language";
import { useCan } from "@/features/auth/api/useMe";
import { useUsersByRole } from "@/features/security-center/api/overview";
import { useSecurityReach } from "@/features/security-center/api/reach";
import { apiErrorText } from "@/lib/apiError";
import { useBadges } from "../api/useBadges";
import { incidentTrucks, useRecentIncidents } from "../api/incidentMarkers";
import { periodRange, useDashboardSummary, type DashboardPeriodName } from "../api/summary";
import { useVehicleLocations } from "../api/vehicleLocations";
import { DashboardStatCards, StatCardsSkeleton } from "./DashboardStatCards";
import { ChatStatusCard, ComplianceCard, ExpenseAuditCard, RecentUpdatesFeed, VehiclePositionList } from "./DashboardWidgets";
import { DeliveryActivityChart } from "./DeliveryActivityChart";
import { FleetMap } from "./FleetMap";

const EMPTY_SET: ReadonlySet<string> = new Set();

function PeriodSwitch({ value, onChange }: { value: DashboardPeriodName; onChange: (p: DashboardPeriodName) => void }) {
    const { t } = useLanguage();
    const button = (p: DashboardPeriodName, label: string) => (
        <button
            type="button"
            aria-pressed={value === p}
            onClick={() => onChange(p)}
            className={`px-3 py-1 text-xs font-medium rounded-md transition-all ${value === p ? "bg-background shadow-sm text-foreground" : "text-muted-foreground hover:text-foreground"}`}
        >
            {label}
        </button>
    );
    return (
        <div className="flex bg-muted/50 rounded-lg p-1" role="group">
            {button("week", t("dashboard.activity.week"))}
            {button("month", t("dashboard.activity.month"))}
        </div>
    );
}

export function DashboardPage() {
    const { t, language } = useLanguage();
    const [period, setPeriod] = useState<DashboardPeriodName>("week");
    // Recomputed on every render: past midnight the key moves to the new day by itself.
    const range = periodRange(period);

    const canUsers = useCan("security:view_overview").allowed;
    const canMap = useCan("fleet:view_live_map").allowed;
    const canIncidents = useCan("operations:view_incidents").allowed;
    const dispatcher = useCan("dispatch:view_operations").allowed;
    const showMap = canMap || dispatcher;

    const summary = useDashboardSummary(range);
    const reach = useSecurityReach();
    const byRole = useUsersByRole(reach, canUsers);
    const locations = useVehicleLocations(showMap);
    const incidents = useRecentIncidents(range, showMap && (canIncidents || dispatcher));
    const { data: badges } = useBadges();

    const incidentRows = useMemo(() => incidents.data?.pages[0]?.data ?? [], [incidents.data]);
    const flagged = useMemo(() => incidentTrucks(incidentRows), [incidentRows]);
    const vehicles = locations.data ?? [];

    const s = summary.data;
    // The period is the page's subject: every figure below follows it (no "real-time" claim over a 60 s poll, no
    // language chip, no uppercase kicker; UI3D).
    const header = (
        <PageHeader
            title={t("nav.dashboard")}
            description={
                <span data-testid="dashboard-period">
                    {t(period === "week" ? "dashboard.period.week" : "dashboard.period.month")} · {t("dashboard.period.range", { from: range.from, to: range.to })}
                </span>
            }
            actions={
                <div className="flex items-center gap-3">
                    {summary.isFetching && <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" aria-hidden="true" />}
                    <PeriodSwitch value={period} onChange={setPeriod} />
                </div>
            }
        />
    );

    if (summary.isError && !s) {
        return (
            <div className="flex flex-col gap-section">
                {header}
                <div className="rounded-xl border border-border bg-card p-6 text-center space-y-3" role="alert">
                    <p className="text-sm text-foreground">{t("dashboard.loadError")}</p>
                    <p className="text-xs text-muted-foreground">{apiErrorText(summary.error, (k, f) => t(k, f))}</p>
                    <button type="button" onClick={() => summary.refetch()} className="inline-flex items-center gap-1.5 text-sm font-medium text-primary hover:underline">
                        <RefreshCw className="h-4 w-4" />
                        {t("dashboard.retry")}
                    </button>
                </div>
            </div>
        );
    }

    const pendingExpenses = badges?.pendingExpenses;
    const chats = badges?.unassignedChats !== undefined ? { unassigned: badges.unassignedChats, urgent: badges.urgentChats ?? 0 } : undefined;
    // The feed shows "no recent activity" to anyone who could see an entry of it.
    const showRecent = s !== undefined && (s.recent.length > 0 || Boolean(s.trips || s.drivers || s.incidents));
    const left = Boolean(s?.activity) || showMap;
    const right = showMap || pendingExpenses !== undefined || chats !== undefined || showRecent || Boolean(s?.compliance);
    const nothing = s !== undefined && !left && !right && !s.trips && !s.drivers && !canUsers;

    return (
        <div className="flex flex-col gap-section">
            {header}

            {s && summary.isError && <div role="alert" className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-warning bg-warning/10 p-4 text-sm">
                <p>{t("dashboard.stale", { time: new Date(summary.dataUpdatedAt).toLocaleString(language === "th" ? "th-TH" : "en-GB") })}</p>
                <button type="button" className="min-h-11 font-medium text-primary underline underline-offset-4" disabled={summary.isFetching} onClick={() => void summary.refetch()}>{t("dashboard.retry")}</button>
            </div>}

            {!s ? <StatCardsSkeleton /> : <DashboardStatCards usersByRole={canUsers ? byRole.data : undefined} drivers={s.drivers} trips={s.trips} />}

            {nothing && <p className="rounded-xl border border-border bg-card p-6 text-sm text-muted-foreground">{t("dashboard.empty")}</p>}

            <div className="grid grid-cols-1 lg:grid-cols-3 gap-6 h-full">
                <div className="lg:col-span-2 h-full space-y-6">
                    {s?.activity && <DeliveryActivityChart activity={s.activity} period={period} />}
                    {showMap && (
                        <FleetMap
                            vehicles={vehicles}
                            loading={locations.isPending}
                            error={locations.isError}
                            onRetry={() => locations.refetch()}
                            incidentTruckIds={incidents.data ? flagged.ids : EMPTY_SET}
                            incidentPlates={incidents.data ? flagged.plates : EMPTY_SET}
                            incidentCount={incidents.data ? incidentRows.length : undefined}
                        />
                    )}
                </div>

                <div className="lg:col-span-1 h-full space-y-6">
                    {showMap && <VehiclePositionList vehicles={vehicles} loading={locations.isPending} error={locations.isError} onRetry={() => locations.refetch()} />}
                    {pendingExpenses !== undefined && <ExpenseAuditCard pending={pendingExpenses} />}
                    {chats && <ChatStatusCard unassigned={chats.unassigned} urgent={chats.urgent} />}
                    {s && showRecent && <RecentUpdatesFeed items={s.recent} />}
                    {s?.compliance && <ComplianceCard compliance={s.compliance} />}
                </div>
            </div>
        </div>
    );
}
