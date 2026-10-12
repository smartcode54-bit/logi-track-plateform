"use client";

/**
 * The four cards of the dashboard over Go (T54R): every figure is computed by PostgreSQL. Users come
 * from `GET /v1/stats/users-by-role` (`security:view_overview`, the key the Security Center shares);
 * drivers, delivered trips with their parcels, and the on-time / delayed shares of the period from the
 * summary (`drivers`, `trips`). A card whose block the principal may not read is left out.
 */
import { MapPin, Package, Users, Zap } from "lucide-react";

import { useLanguage } from "@/context/language";
import { roleDistribution, type UsersByRoleDTO } from "@/features/security-center/api/overview";
import { activeDrivers, type DriversBlockDTO, type TripsBlockDTO } from "../api/summary";

interface StatCardProps {
    title: string;
    value: React.ReactNode;
    hint?: string;
    icon: React.ElementType;
    iconBgColor: string;
    iconColor: string;
    testId: string;
}

function StatCard({ title, value, hint, icon: Icon, iconBgColor, iconColor, testId }: StatCardProps) {
    return (
        <div className="bg-card border border-border rounded-xl p-6 shadow-sm" data-testid={testId}>
            <div className="flex justify-between items-start mb-4">
                <div className={`p-3 rounded-lg ${iconBgColor} ${iconColor}`}>
                    <Icon className="w-6 h-6" />
                </div>
            </div>
            <p className="text-muted-foreground text-xs font-semibold uppercase tracking-wider mb-1">{title}</p>
            <h3 className="text-3xl font-bold text-foreground tabular-nums">{value}</h3>
            {hint && <p className="mt-1 text-xs text-muted-foreground">{hint}</p>}
        </div>
    );
}

export function StatCardsSkeleton() {
    return (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-6 mb-6" aria-busy="true">
            {[1, 2, 3, 4].map((i) => (
                <div key={i} className="bg-card border border-border rounded-xl p-6 shadow-sm animate-pulse flex items-center gap-4">
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

const pctText = (v: number | null | undefined) => (v === null || v === undefined ? "—" : `${v}%`);

export function DashboardStatCards({ usersByRole, drivers, trips }: { usersByRole?: UsersByRoleDTO; drivers?: DriversBlockDTO; trips?: TripsBlockDTO }) {
    const { t } = useLanguage();
    if (!usersByRole && !drivers && !trips) return null;
    const people = usersByRole ? roleDistribution(usersByRole) : undefined;
    return (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-6 mb-6">
            {people && (
                <StatCard
                    testId="stat-users"
                    title={t("dashboard.stats.totalUsers")}
                    value={(people.operations + people.others).toLocaleString()}
                    hint={t("dashboard.stats.webUsers")}
                    icon={Users}
                    iconBgColor="bg-blue-500/10"
                    iconColor="text-blue-500"
                />
            )}
            {drivers && (
                <StatCard
                    testId="stat-drivers"
                    title={t("dashboard.stats.activeDrivers")}
                    value={activeDrivers(drivers).toLocaleString()}
                    hint={t("dashboard.stats.allDrivers", { total: drivers.total.toLocaleString() })}
                    icon={MapPin}
                    iconBgColor="bg-sky-500/10"
                    iconColor="text-sky-500"
                />
            )}
            {trips && (
                <StatCard
                    testId="stat-trips"
                    title={t("dashboard.stats.tripsAndPackages")}
                    value={
                        <span>
                            {trips.delivered.toLocaleString()} <span className="text-indigo-400 font-semibold">/ {trips.parcels.toLocaleString()}</span>
                        </span>
                    }
                    hint={t("dashboard.stats.inPeriod")}
                    icon={Package}
                    iconBgColor="bg-indigo-500/10"
                    iconColor="text-indigo-500"
                />
            )}
            {trips && (
                <StatCard
                    testId="stat-efficiency"
                    title={t("dashboard.stats.fleetEfficiency")}
                    value={
                        trips.onTimePct === null ? (
                            "—"
                        ) : (
                            <span>
                                {pctText(trips.onTimePct)} <span className="text-yellow-400 font-semibold">/ {pctText(trips.delayPct)}</span>
                            </span>
                        )
                    }
                    hint={t("dashboard.stats.inPeriod")}
                    icon={Zap}
                    iconBgColor="bg-yellow-500/10"
                    iconColor="text-yellow-500"
                />
            )}
        </div>
    );
}
