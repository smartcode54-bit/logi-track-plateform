"use client";

import { useLanguage } from "@/context/language";
import { useMemo, useState } from "react";
import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { truckStatusTone, toneBadgeVariant, type StatusTone } from "@/lib/statusPresentation";
import { useFleetStats } from "../api/useTrucks";
import type { TruckDTO } from "../api/trucks";

interface FleetStatus {
    total: number;
    active: number;
    maintenance: number;
    inactive: number;
}

interface MaintenanceStatus {
    total: number; // Total trucks with PM tracked
    overdue: number; // Overdue PM
    dueSoon: number; // Due soon PM
    normal: number; // Normal status
}

interface TruckComplianceCardsProps {
    onFilterChange: (filter: { type: string | null; status: string | null }) => void;
}

const WARNING_DAYS = 30;
const WARNING_KM = 2000;

/** The counters of the cards over every truck (own fleet, carriers, PM schedule of the own fleet). */
export function fleetCounters(trucks: readonly TruckDTO[], now: Date = new Date()) {
    const stats = {
        fleet: { total: 0, active: 0, maintenance: 0, inactive: 0 } as FleetStatus,
        sub: { total: 0, active: 0, maintenance: 0, inactive: 0 } as FleetStatus,
        maintenance: { total: 0, overdue: 0, dueSoon: 0, normal: 0 } as MaintenanceStatus,
    };
    for (const t of trucks) {
        const group = t.ownershipType === "own" ? stats.fleet : stats.sub;
        group.total++;
        if (t.status === "active") group.active++;
        else if (t.status === "maintenance") group.maintenance++;
        else if (t.status === "inactive") group.inactive++;
        // PM schedule: own fleet only.
        if (t.ownershipType !== "own") continue;
        let tracked = false;
        let overdue = false;
        let dueSoon = false;
        if (t.nextServiceDate) {
            tracked = true;
            const days = Math.ceil((new Date(t.nextServiceDate).getTime() - now.getTime()) / 86_400_000);
            if (days < 0) overdue = true;
            else if (days <= WARNING_DAYS) dueSoon = true;
        }
        if (t.nextServiceMileage) {
            tracked = true;
            const kms = t.nextServiceMileage - (t.currentMileage ?? 0);
            if (kms < 0) overdue = true;
            else if (kms <= WARNING_KM) dueSoon = true;
        }
        if (tracked) {
            stats.maintenance.total++;
            if (overdue) stats.maintenance.overdue++;
            else if (dueSoon) stats.maintenance.dueSoon++;
            else stats.maintenance.normal++;
        }
    }
    return stats;
}

export function TruckComplianceCards({ onFilterChange }: TruckComplianceCardsProps) {
    const { t } = useLanguage();
    // `['trucks','fleetStats']` (T25): every truck through the active source; the list's refresh
    // invalidates `['trucks']`, which reaches it.
    const fleet = useFleetStats();
    const stats = useMemo(() => fleetCounters(fleet.trucks), [fleet.trucks]);
    const loading = fleet.isPending;
    const [activeFilter, setActiveFilter] = useState<{ type: string; status: string } | null>(null);

    const handleCardClick = (type: string, status: string) => {
        if (activeFilter?.type === type && activeFilter?.status === status) {
            setActiveFilter(null);
            onFilterChange({ type: null, status: null });
        } else {
            setActiveFilter({ type, status });
            onFilterChange({ type, status });
        }
    };

    if (loading) {
        return (
            <div className="grid grid-cols-1 md:grid-cols-2 gap-6 mb-6">
                {[1, 2].map((i) => (
                    <Card key={i} className="border-l-muted shadow-sm animate-pulse">
                        <CardHeader className="pb-2">
                            <div className="h-5 w-32 bg-muted rounded" />
                        </CardHeader>
                        <CardContent>
                            <div className="grid grid-cols-3 gap-2 mt-2 text-center">
                                <div className="h-10 bg-muted rounded col-span-3" />
                            </div>
                        </CardContent>
                    </Card>
                ))}
            </div>
        );
    }

    return (
        <div className="grid grid-cols-1 md:grid-cols-3 gap-6 mb-6">
            {/* Card 1: Own Fleet Status */}
            <Card className="border shadow-sm">
                <CardHeader className="pb-4">
                    <div className="flex justify-between items-start">
                        <span className="text-lg font-semibold text-foreground">{t('trucks.stats.own')}</span>
                        <div className="flex flex-col items-end">
                            <span className="text-xs text-muted-foreground font-medium">{t('trucks.stats.all')}</span>
                            <span className="text-3xl font-bold text-foreground leading-none mt-1">{stats.fleet.total}</span>
                        </div>
                    </div>
                </CardHeader>
                <CardContent>
                    <div className="grid grid-cols-3 gap-4 mt-2 text-center divide-x divide-border/40">
                        <StatusItem
                            label={t('trucks.stats.active')}
                            count={stats.fleet.active}
                            tone={truckStatusTone("active")}
                            active={activeFilter?.type === 'own' && activeFilter?.status === 'active'}
                            onClick={() => handleCardClick('own', 'active')}
                        />
                        <StatusItem
                            label={t('trucks.stats.available')}
                            count={stats.fleet.inactive}
                            tone={truckStatusTone("inactive")}
                            active={activeFilter?.type === 'own' && activeFilter?.status === 'available'}
                            onClick={() => handleCardClick('own', 'available')}
                        />
                        <StatusItem
                            label={t('trucks.stats.corrective')}
                            count={stats.fleet.maintenance}
                            tone={truckStatusTone("maintenance")}
                            active={activeFilter?.type === 'own' && activeFilter?.status === 'corrective'}
                            lastItem
                            onClick={() => handleCardClick('own', 'corrective')}
                        />
                    </div>
                </CardContent>
            </Card>

            {/* Card 2: Subcontractor Fleet Status */}
            <Card className="border shadow-sm">
                <CardHeader className="pb-4">
                    <div className="flex justify-between items-start">
                        <span className="text-lg font-semibold text-foreground">{t('trucks.stats.sub')}</span>
                        <div className="flex flex-col items-end">
                            <span className="text-xs text-muted-foreground font-medium">{t('trucks.stats.all')}</span>
                            <span className="text-3xl font-bold text-foreground leading-none mt-1">{stats.sub.total}</span>
                        </div>
                    </div>
                </CardHeader>
                <CardContent>
                    <div className="grid grid-cols-3 gap-4 mt-2 text-center divide-x divide-border/40">
                        <StatusItem
                            label={t('trucks.stats.active')}
                            count={stats.sub.active}
                            tone={truckStatusTone("active")}
                            active={activeFilter?.type === 'sub' && activeFilter?.status === 'active'}
                            onClick={() => handleCardClick('sub', 'active')}
                        />
                        <StatusItem
                            label={t('trucks.stats.available')}
                            count={stats.sub.inactive}
                            tone={truckStatusTone("inactive")}
                            active={activeFilter?.type === 'sub' && activeFilter?.status === 'available'}
                            onClick={() => handleCardClick('sub', 'available')}
                        />
                        <StatusItem
                            label={t('trucks.stats.corrective')}
                            count={stats.sub.maintenance}
                            tone={truckStatusTone("maintenance")}
                            active={activeFilter?.type === 'sub' && activeFilter?.status === 'corrective'}
                            lastItem
                            onClick={() => handleCardClick('sub', 'corrective')}
                        />
                    </div>
                </CardContent>
            </Card>

            {/* Card 3: Maintenance Schedule */}
            <Card className="border shadow-sm">
                <CardHeader className="pb-4">
                    <div className="flex flex-col items-start gap-1">
                        <span className="text-base font-semibold text-foreground">{t('masterData.trucks.maintenanceCorrective')}</span>
                        <span className="text-xs text-muted-foreground font-medium">{t('trucks.stats.ownFleetOnly')}</span>
                    </div>
                </CardHeader>
                <CardContent>
                    <div className="grid grid-cols-3 gap-4 mt-2 text-center divide-x divide-border/40">
                        <StatusItem
                            label={t('trucks.stats.pmSoon')}
                            count={stats.maintenance.dueSoon}
                            tone="warning"
                            active={activeFilter?.type === 'maintenance' && activeFilter?.status === 'due'}
                            onClick={() => handleCardClick('maintenance', 'due')}
                        />
                        <StatusItem
                            label={t('trucks.stats.pmOverdue')}
                            count={stats.maintenance.overdue}
                            tone="destructive"
                            active={activeFilter?.type === 'maintenance' && activeFilter?.status === 'overdue'}
                            onClick={() => handleCardClick('maintenance', 'overdue')}
                        />
                        <StatusItem
                            label={t('trucks.stats.corrective')}
                            count={stats.fleet.maintenance}
                            tone={truckStatusTone("maintenance")}
                            active={activeFilter?.type === 'own' && activeFilter?.status === 'corrective'}
                            lastItem
                            onClick={() => handleCardClick('own', 'corrective')}
                        />
                    </div>
                </CardContent>
            </Card>
        </div>
    );
}

function StatusItem({ label, count, tone = "neutral", active, lastItem, onClick }: { label: string, count: number, tone?: StatusTone, active?: boolean, lastItem?: boolean, onClick: () => void }) {
    return (
        <button
            type="button"
            onClick={onClick}
            aria-pressed={Boolean(active)}
            className={cn(
                "flex min-w-0 flex-col items-center justify-center gap-2 rounded-md p-1 text-foreground hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                !lastItem ? "md:pr-4" : "",
                active ? "bg-muted ring-1 ring-ring" : ""
            )}
        >
            <span className="text-xs font-medium text-muted-foreground">{label}</span>
            <Badge variant={toneBadgeVariant(tone)} className="text-lg tabular-nums">{count}</Badge>
        </button>
    );
}
