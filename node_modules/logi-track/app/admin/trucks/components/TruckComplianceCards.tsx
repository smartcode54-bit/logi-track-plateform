"use client";

import { useLanguage } from "@/context/language";
import { useEffect, useState } from "react";
import { collection, query, where, getDocs } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { AlertTriangle, CalendarDays, CheckCircle2, ShieldAlert, FileText, Wrench } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import Link from "next/link";
import { cn } from "@/lib/utils";

interface ComplianceStats {
    total: number;
    expiringSoon: number; // <= 30 days or <= 1000km
    overdue: number; // < 0 days or < 0km
    incoming: number; // 30-60 days
    items: ComplianceItem[];
}

interface ComplianceItem {
    id: string;
    licensePlate: string;
    type: "tax" | "insurance" | "service";
    expiryDate: Date;
    daysRemaining?: number;
    kmsRemaining?: number;
}

interface TruckComplianceCardsProps {
    onFilterChange: (filter: { type: string | null; status: string | null }) => void;
    refreshKey?: number;
    onLoadingChange?: (loading: boolean) => void;
}

export function TruckComplianceCards({ onFilterChange, refreshKey, onLoadingChange }: TruckComplianceCardsProps) {
    const { t } = useLanguage();
    const [stats, setStats] = useState<{
        tax: ComplianceStats;
        insurance: ComplianceStats;
        service: ComplianceStats;
    }>({
        tax: { total: 0, expiringSoon: 0, overdue: 0, incoming: 0, items: [] },
        insurance: { total: 0, expiringSoon: 0, overdue: 0, incoming: 0, items: [] },
        service: { total: 0, expiringSoon: 0, overdue: 0, incoming: 0, items: [] },
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

    const formatTimestamp = (timestamp: any): Date | null => {
        if (!timestamp) return null;
        if (timestamp.toDate) return timestamp.toDate();
        if (timestamp.toMillis) return new Date(timestamp.toMillis());
        if (timestamp.seconds) return new Date(timestamp.seconds * 1000);
        return new Date(timestamp); // Fallback for string/Date
    };

    useEffect(() => {
        async function fetchComplianceData() {
            setLoading(true);
            onLoadingChange?.(true);
            try {
                const trucksRef = collection(db, COLLECTIONS.TRUCKS);
                // Remove status filter for now to match table, or ensure table also filters by active
                // For compliance dashboard, checking all active/maintenance trucks might be better
                // But let's stick to 'not inactive' if possible, or just all trucks for now to debug
                const q = query(trucksRef);
                const snapshot = await getDocs(q);

                const now = new Date();
                const warningThresholdDays = 30;
                const incomingThresholdDays = 60; // 30-60 days
                const warningThresholdKm = 2000;
                const incomingThresholdKm = 5000;

                const newStats = {
                    tax: { total: 0, expiringSoon: 0, overdue: 0, incoming: 0, items: [] as ComplianceItem[] },
                    insurance: { total: 0, expiringSoon: 0, overdue: 0, incoming: 0, items: [] as ComplianceItem[] },
                    service: { total: 0, expiringSoon: 0, overdue: 0, incoming: 0, items: [] as ComplianceItem[] },
                };

                snapshot.forEach((doc) => {
                    const data = doc.data();

                    // Skip inactive trucks if that's the requirement, otherwise include all
                    if (data.truckStatus === 'inactive') return;

                    const truckId = doc.id;
                    const plate = data.licensePlate || "Unknown";

                    // Tax Logic
                    const taxDate = formatTimestamp(data.taxExpiryDate);
                    if (taxDate) {
                        const days = Math.ceil((taxDate.getTime() - now.getTime()) / (1000 * 60 * 60 * 24));
                        newStats.tax.total++;
                        // Logic: 
                        // Overdue: < 0
                        // Expiring (Warning): 0 <= days <= 30
                        // Incoming: 30 < days <= 60

                        if (days < 0) newStats.tax.overdue++;
                        else if (days <= warningThresholdDays) newStats.tax.expiringSoon++;
                        else if (days <= incomingThresholdDays) newStats.tax.incoming++;
                    }

                    // Insurance Logic
                    const insuranceDate = formatTimestamp(data.insuranceExpiryDate);
                    if (insuranceDate) {
                        const days = Math.ceil((insuranceDate.getTime() - now.getTime()) / (1000 * 60 * 60 * 24));
                        newStats.insurance.total++;
                        if (days < 0) newStats.insurance.overdue++;
                        else if (days <= warningThresholdDays) newStats.insurance.expiringSoon++;
                        else if (days <= incomingThresholdDays) newStats.insurance.incoming++;
                    }

                    // Maintenance (Service) Logic
                    const serviceDate = formatTimestamp(data.nextServiceDate);
                    const currentKm = Number(data.currentMileage) || 0;
                    const nextServiceKm = Number(data.nextServiceMileage);

                    let isServiceTracked = false;
                    let serviceDays = 999;
                    let serviceKms = 99999;

                    if (serviceDate) {
                        isServiceTracked = true;
                        serviceDays = Math.ceil((serviceDate.getTime() - now.getTime()) / (1000 * 60 * 60 * 24));
                    }

                    if (data.nextServiceMileage) {
                        isServiceTracked = true;
                        serviceKms = nextServiceKm - currentKm;
                    }

                    if (isServiceTracked) {
                        newStats.service.total++;
                        // Check Overdue first (logic OR)
                        if ((serviceDate && serviceDays < 0) || (data.nextServiceMileage && serviceKms < 0)) {
                            newStats.service.overdue++;
                        }
                        // Check Expiring Soon
                        else if ((serviceDate && serviceDays <= warningThresholdDays) || (data.nextServiceMileage && serviceKms <= warningThresholdKm)) {
                            newStats.service.expiringSoon++;
                        }
                        // Check Incoming
                        else if ((serviceDate && serviceDays <= incomingThresholdDays) || (data.nextServiceMileage && serviceKms <= incomingThresholdKm)) {
                            newStats.service.incoming++;
                        }
                    }
                });

                setStats(newStats);
                setLoading(false);
                onLoadingChange?.(false);
            } catch (error) {
                console.error("Error fetching compliance stats:", error);
                setLoading(false);
                onLoadingChange?.(false);
            }
        }

        fetchComplianceData();
    }, [refreshKey]);

    if (loading) {
        return (
            <div className="grid grid-cols-1 md:grid-cols-3 gap-6 mb-6">
                {[1, 2, 3].map((i) => (
                    <Card key={i} className="border-l-4 border-l-muted shadow-sm animate-pulse">
                        <CardHeader className="pb-2">
                            <div className="h-5 w-32 bg-muted rounded" />
                        </CardHeader>
                        <CardContent>
                            <div className="grid grid-cols-3 gap-2 mt-2 text-center">
                                {[1, 2, 3].map((j) => (
                                    <div key={j} className="flex flex-col p-2 space-y-2">
                                        <div className="h-3 w-16 bg-muted rounded mx-auto" />
                                        <div className="h-8 w-8 bg-muted rounded mx-auto" />
                                    </div>
                                ))}
                            </div>
                        </CardContent>
                    </Card>
                ))}
            </div>
        );
    }

    return (
        <div className="grid grid-cols-1 md:grid-cols-3 gap-6 mb-6">
            <ComplianceCard
                title={t('trucks.stats.tax')}
                icon={<FileText className="h-5 w-5 text-orange-600" />}
                stats={stats.tax}
                type="tax"
                activeFilter={activeFilter}
                onCardClick={handleCardClick}
            />
            <ComplianceCard
                title={t('trucks.stats.insurance')}
                icon={<ShieldAlert className="h-5 w-5 text-blue-600" />}
                stats={stats.insurance}
                type="insurance"
                activeFilter={activeFilter}
                onCardClick={handleCardClick}
            />
            <ComplianceCard
                title={t('trucks.stats.maintenance')}
                icon={<Wrench className="h-5 w-5 text-purple-600" />}
                stats={stats.service}
                type="service"
                activeFilter={activeFilter}
                onCardClick={handleCardClick}
            />
        </div>
    );
}

function ComplianceCard({ title, icon, stats, type, activeFilter, onCardClick }: any) {
    const { t } = useLanguage();
    const isActive = (status: string) => activeFilter?.type === type && activeFilter?.status === status;

    return (
        <Card className="border-l-4 border-l-blue-600 shadow-sm">
            <CardHeader className="pb-2">
                <CardTitle className="text-base font-medium text-muted-foreground flex items-center gap-2">
                    {icon}
                    {title}
                </CardTitle>
            </CardHeader>
            <CardContent>
                <div className="grid grid-cols-3 gap-2 mt-2 text-center">
                    <div
                        onClick={() => onCardClick(type, "incoming")}
                        className={cn(
                            "flex flex-col p-2 rounded cursor-pointer transition-colors hover:bg-muted",
                            isActive("incoming") ? "bg-muted ring-1 ring-primary" : ""
                        )}
                    >
                        <span className="text-xs text-muted-foreground uppercase tracking-wider font-semibold">
                            {type === 'service' ? t('trucks.stats.pmIncoming') : t('trucks.stats.incoming')}
                        </span>
                        <span className="text-2xl font-bold text-blue-600">{stats.incoming}</span>
                    </div>
                    <div
                        onClick={() => onCardClick(type, "expiring")}
                        className={cn(
                            "flex flex-col p-2 rounded cursor-pointer transition-colors hover:bg-muted",
                            isActive("expiring") ? "bg-muted ring-1 ring-primary" : ""
                        )}
                    >
                        <span className="text-xs text-muted-foreground uppercase tracking-wider font-semibold">
                            {type === 'service' ? t('trucks.stats.pmDueSoon') : t('trucks.stats.less30Days')}
                        </span>
                        <span className="text-2xl font-bold text-orange-600">{stats.expiringSoon}</span>
                    </div>
                    <div
                        onClick={() => onCardClick(type, "overdue")}
                        className={cn(
                            "flex flex-col p-2 rounded cursor-pointer transition-colors hover:bg-muted",
                            isActive("overdue") ? "bg-muted ring-1 ring-primary" : ""
                        )}
                    >
                        <span className="text-xs text-muted-foreground uppercase tracking-wider font-semibold">
                            {type === 'service' ? t('trucks.stats.pmOverdue') : t('trucks.stats.overdue')}
                        </span>
                        <span className="text-2xl font-bold text-red-600">{stats.overdue}</span>
                    </div>
                </div>
            </CardContent>
            {/* Responsibility Footer */}
            <div className="bg-muted/30 px-4 py-2 border-t text-xs text-muted-foreground flex justify-between items-center">
                <span>{t('trucks.stats.owner')}:</span>
                <span className="font-medium text-foreground">
                    {type === 'service' ? t('trucks.stats.assignedDriver') : t('trucks.stats.opAdmin')}
                </span>
            </div>
        </Card >
    );
}
