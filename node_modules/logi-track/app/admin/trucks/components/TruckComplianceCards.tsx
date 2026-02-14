"use client";

import { useLanguage } from "@/context/language";
import { useEffect, useState } from "react";
import { collection, query, where, getDocs } from "firebase/firestore";
import { db } from "@/firebase/client";
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
}

export function TruckComplianceCards({ onFilterChange }: TruckComplianceCardsProps) {
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

    useEffect(() => {
        async function fetchComplianceData() {
            setLoading(true);
            try {
                const trucksRef = collection(db, "trucks");
                const q = query(trucksRef, where("truckStatus", "==", "active"));
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
                    const truckId = doc.id;
                    const plate = data.licensePlate || "Unknown";

                    // Tax Logic
                    if (data.taxExpiryDate) {
                        const days = Math.ceil((new Date(data.taxExpiryDate).getTime() - now.getTime()) / (1000 * 60 * 60 * 24));
                        newStats.tax.total++;
                        if (days < 0) newStats.tax.overdue++;
                        else if (days <= warningThresholdDays) newStats.tax.expiringSoon++;
                        else if (days <= incomingThresholdDays) newStats.tax.incoming++;
                    }

                    // Insurance Logic
                    if (data.insuranceExpiryDate) {
                        const days = Math.ceil((new Date(data.insuranceExpiryDate).getTime() - now.getTime()) / (1000 * 60 * 60 * 24));
                        newStats.insurance.total++;
                        if (days < 0) newStats.insurance.overdue++;
                        else if (days <= warningThresholdDays) newStats.insurance.expiringSoon++;
                        else if (days <= incomingThresholdDays) newStats.insurance.incoming++;
                    }

                    // Service Logic
                    let sDays = 999;
                    let sKms = 99999;

                    if (data.nextServiceDate) {
                        sDays = Math.ceil((new Date(data.nextServiceDate).getTime() - now.getTime()) / (1000 * 60 * 60 * 24));
                    }
                    if (data.nextServiceMileage && data.currentMileage !== undefined) {
                        sKms = data.nextServiceMileage - data.currentMileage;
                    }

                    if (sDays < 900 || sKms < 50000) { // Only count if there's valid service data
                        newStats.service.total++;
                        if (sDays < 0 || sKms < 0) newStats.service.overdue++;
                        else if (sDays <= warningThresholdDays || sKms <= warningThresholdKm) newStats.service.expiringSoon++;
                        else if (sDays <= incomingThresholdDays || sKms <= incomingThresholdKm) newStats.service.incoming++;
                    }
                });

                setStats(newStats);
            } catch (err) {
                console.error("Failed to fetch compliance stats", err);
            } finally {
                setLoading(false);
            }
        }

        fetchComplianceData();
    }, []);

    if (loading) return null;

    return (
        <div className="grid grid-cols-1 md:grid-cols-3 gap-6 mb-6">
            <ComplianceCard
                title="Tax & Registration"
                icon={<FileText className="h-5 w-5 text-orange-600" />}
                stats={stats.tax}
                type="tax"
                activeFilter={activeFilter}
                onCardClick={handleCardClick}
            />
            <ComplianceCard
                title="Insurance Status"
                icon={<ShieldAlert className="h-5 w-5 text-blue-600" />}
                stats={stats.insurance}
                type="insurance"
                activeFilter={activeFilter}
                onCardClick={handleCardClick}
            />
            <ComplianceCard
                title="Maintenance Service"
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
                            {type === 'service' ? "PM Incoming" : "Incoming"}
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
                            {type === 'service' ? "PM Due Soon" : "Less 30 Days"}
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
                            {type === 'service' ? "PM Overdue" : "Overdue"}
                        </span>
                        <span className="text-2xl font-bold text-red-600">{stats.overdue}</span>
                    </div>
                </div>
            </CardContent>
            {/* Responsibility Footer */}
            <div className="bg-muted/30 px-4 py-2 border-t text-xs text-muted-foreground flex justify-between items-center">
                <span>Action Owner:</span>
                <span className="font-medium text-foreground">
                    {type === 'service' ? "Assigned Driver" : "Operation Admin"}
                </span>
            </div>
        </Card >
    );
}
