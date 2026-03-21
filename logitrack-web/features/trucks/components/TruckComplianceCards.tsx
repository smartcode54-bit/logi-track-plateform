"use client";

import { useLanguage } from "@/context/language";
import { useEffect, useState } from "react";
import { collection, query, getDocs } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { cn } from "@/lib/utils";

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
    items: any[]; // Potentially store items for detailed view if needed later
}

interface TruckComplianceCardsProps {
    onFilterChange: (filter: { type: string | null; status: string | null }) => void;
    refreshKey?: number;
    onLoadingChange?: (loading: boolean) => void;
}

export function TruckComplianceCards({ onFilterChange, refreshKey, onLoadingChange }: TruckComplianceCardsProps) {
    const { t } = useLanguage();
    const [stats, setStats] = useState<{
        fleet: FleetStatus;
        sub: FleetStatus;
        maintenance: MaintenanceStatus;
    }>({
        fleet: { total: 0, active: 0, maintenance: 0, inactive: 0 },
        sub: { total: 0, active: 0, maintenance: 0, inactive: 0 },
        maintenance: { total: 0, overdue: 0, dueSoon: 0, normal: 0, items: [] },
    });
    const [loading, setLoading] = useState(true);
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

    useEffect(() => {
        async function fetchTruckData() {
            setLoading(true);
            onLoadingChange?.(true);
            try {
                const trucksRef = collection(db, COLLECTIONS.TRUCKS);
                const q = query(trucksRef);
                const snapshot = await getDocs(q);

                const newStats = {
                    fleet: { total: 0, active: 0, maintenance: 0, inactive: 0 },
                    sub: { total: 0, active: 0, maintenance: 0, inactive: 0 },
                    maintenance: { total: 0, overdue: 0, dueSoon: 0, normal: 0, items: [] },
                };

                const now = new Date();
                const warningThresholdDays = 30;
                const warningThresholdKm = 2000;

                const formatTimestamp = (timestamp: any): Date | null => {
                    if (!timestamp) return null;
                    if (timestamp.toDate) return timestamp.toDate();
                    if (timestamp.toMillis) return new Date(timestamp.toMillis());
                    if (timestamp.seconds) return new Date(timestamp.seconds * 1000);
                    return new Date(timestamp);
                };

                snapshot.forEach((doc) => {
                    const data = doc.data();

                    // Filter: OWN FLEET ONLY
                    if (data.ownershipType !== 'own') return;

                    const status = data.truckStatus || 'inactive';

                    // 1. Fleet Status Stats
                    newStats.fleet.total++;
                    if (status === 'active') newStats.fleet.active++;
                    else if (status === 'maintenance') newStats.fleet.maintenance++;
                    else if (status === 'inactive') newStats.fleet.inactive++;

                    // 2. Maintenance Schedule Stats
                    const serviceDate = formatTimestamp(data.nextServiceDate);
                    const currentKm = Number(data.currentMileage) || 0;
                    const nextServiceKm = Number(data.nextServiceMileage);

                    let isTracked = false;
                    let isOverdue = false;
                    let isDueSoon = false;

                    if (serviceDate) {
                        isTracked = true;
                        const days = Math.ceil((serviceDate.getTime() - now.getTime()) / (1000 * 60 * 60 * 24));
                        if (days < 0) isOverdue = true;
                        else if (days <= warningThresholdDays) isDueSoon = true;
                    }

                    if (nextServiceKm) {
                        isTracked = true;
                        const kms = nextServiceKm - currentKm;
                        if (kms < 0) isOverdue = true;
                        else if (kms <= warningThresholdKm) isDueSoon = true;
                    }

                    if (isTracked) {
                        newStats.maintenance.total++;
                        if (isOverdue) newStats.maintenance.overdue++;
                        else if (isDueSoon) newStats.maintenance.dueSoon++;
                        else newStats.maintenance.normal++;
                    }
                });

                setStats(newStats);
                setLoading(false);
                onLoadingChange?.(false);
            } catch (error) {
                console.error("Error fetching truck stats:", error);
                setLoading(false);
                onLoadingChange?.(false);
            }
        }

        fetchTruckData();
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [refreshKey]);

    if (loading) {
        return (
            <div className="grid grid-cols-1 md:grid-cols-2 gap-6 mb-6">
                {[1, 2].map((i) => (
                    <Card key={i} className="border-l-4 border-l-muted shadow-sm animate-pulse">
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
                            <span className="text-[10px] text-muted-foreground uppercase font-medium tracking-wider">{t('trucks.stats.all')}</span>
                            <span className="text-3xl font-bold text-foreground leading-none mt-1">{stats.fleet.total}</span>
                        </div>
                    </div>
                </CardHeader>
                <CardContent>
                    <div className="grid grid-cols-4 gap-4 mt-2 text-center divide-x divide-border/40">
                        <StatusItem
                            label={t('trucks.stats.active')}
                            count={stats.fleet.active}
                            color="text-green-500"
                            active={activeFilter?.type === 'own' && activeFilter?.status === 'active'}
                            onClick={() => handleCardClick('own', 'active')}
                        />
                        <StatusItem
                            label={t('trucks.stats.available')}
                            count={stats.fleet.inactive}
                            color="text-blue-500"
                            active={activeFilter?.type === 'own' && activeFilter?.status === 'available'}
                            onClick={() => handleCardClick('own', 'available')}
                        />
                        <StatusItem
                            label={t('trucks.stats.pm')}
                            count={0}
                            color="text-orange-500"
                            active={activeFilter?.type === 'own' && activeFilter?.status === 'pm'}
                            onClick={() => handleCardClick('own', 'pm')}
                        />
                        <StatusItem
                            label={t('trucks.stats.corrective')}
                            count={stats.fleet.maintenance}
                            color="text-red-500"
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
                            <span className="text-[10px] text-muted-foreground uppercase font-medium tracking-wider">{t('trucks.stats.all')}</span>
                            <span className="text-3xl font-bold text-foreground leading-none mt-1">{stats.sub.total}</span>
                        </div>
                    </div>
                </CardHeader>
                <CardContent>
                    <div className="grid grid-cols-4 gap-4 mt-2 text-center divide-x divide-border/40">
                        <StatusItem
                            label={t('trucks.stats.active')}
                            count={stats.sub.active}
                            color="text-green-500"
                            active={activeFilter?.type === 'sub' && activeFilter?.status === 'active'}
                            onClick={() => handleCardClick('sub', 'active')}
                        />
                        <StatusItem
                            label={t('trucks.stats.available')}
                            count={stats.sub.inactive}
                            color="text-blue-500"
                            active={activeFilter?.type === 'sub' && activeFilter?.status === 'available'}
                            onClick={() => handleCardClick('sub', 'available')}
                        />
                        <StatusItem
                            label={t('trucks.stats.pm')}
                            count={0}
                            color="text-orange-500"
                            active={activeFilter?.type === 'sub' && activeFilter?.status === 'pm'}
                            onClick={() => handleCardClick('sub', 'pm')}
                        />
                        <StatusItem
                            label={t('trucks.stats.corrective')}
                            count={stats.sub.maintenance}
                            color="text-red-500"
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
                        <span className="text-base font-semibold text-foreground">Maintenance / Corrective</span>
                        <span className="text-[10px] text-muted-foreground uppercase tracking-wider font-medium">{t('trucks.stats.ownFleetOnly')}</span>
                    </div>
                </CardHeader>
                <CardContent>
                    <div className="grid grid-cols-3 gap-4 mt-2 text-center divide-x divide-border/40">
                        <StatusItem
                            label={t('trucks.stats.pmSoon')}
                            count={stats.maintenance.dueSoon}
                            color="text-sky-500"
                            active={activeFilter?.type === 'maintenance' && activeFilter?.status === 'due'}
                            onClick={() => handleCardClick('maintenance', 'due')}
                        />
                        <StatusItem
                            label={t('trucks.stats.pmOverdue')}
                            count={stats.maintenance.overdue}
                            color="text-orange-500"
                            active={activeFilter?.type === 'maintenance' && activeFilter?.status === 'overdue'}
                            onClick={() => handleCardClick('maintenance', 'overdue')}
                        />
                        <StatusItem
                            label="Corrective"
                            count={stats.fleet.maintenance}
                            color="text-red-500"
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

function StatusItem({ label, count, color, active, lastItem, onClick }: { label: string, count: number, color?: string, active?: boolean, lastItem?: boolean, onClick: () => void }) {
    return (
        <div
            onClick={onClick}
            className={cn(
                "flex flex-col items-center justify-center cursor-pointer transition-all hover:opacity-80 relative",
                !lastItem ? "pr-4" : "",
                active ? "scale-105 transform" : ""
            )}
        >
            <span className={cn("text-[10px] uppercase tracking-wider mb-2 font-semibold", active ? "text-primary" : "text-muted-foreground")}>
                {label}
            </span>
            <span className={cn("text-2xl font-bold", color || "text-foreground")}>{count}</span>
        </div>
    );
}
