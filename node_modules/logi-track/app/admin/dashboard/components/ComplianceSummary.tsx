"use client";

import { useLanguage } from "@/context/language";
import { useEffect, useState } from "react";
import { collection, query, where, getDocs } from "firebase/firestore";
import { db } from "@/firebase/client";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { AlertTriangle, CalendarDays, CheckCircle2, ShieldAlert } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import Link from "next/link";

interface ComplianceItem {
    id: string;
    licensePlate: string;
    type: "tax" | "insurance" | "service";
    expiryDate: Date;
    daysRemaining?: number;
    kmsRemaining?: number;
}

export function ComplianceSummary() {
    const { t } = useLanguage();
    const [items, setItems] = useState<ComplianceItem[]>([]);
    const [loading, setLoading] = useState(true);

    useEffect(() => {
        async function fetchComplianceData() {
            setLoading(true);
            try {
                // Fetch all owned trucks
                const trucksRef = collection(db, "trucks");
                const q = query(trucksRef, where("ownershipType", "==", "own"), where("truckStatus", "==", "active"));
                const snapshot = await getDocs(q);

                const complianceList: ComplianceItem[] = [];
                const now = new Date();
                const warningThresholdDays = 30; // Days
                const warningThresholdKm = 1000; // Kilometers

                snapshot.forEach((doc) => {
                    const data = doc.data();
                    const truckId = doc.id;
                    const plate = data.licensePlate || "Unknown";

                    // Check Tax Expiry
                    if (data.taxExpiryDate) {
                        const taxDate = new Date(data.taxExpiryDate);
                        const days = Math.ceil((taxDate.getTime() - now.getTime()) / (1000 * 60 * 60 * 24));
                        if (days <= warningThresholdDays) {
                            complianceList.push({
                                id: truckId,
                                licensePlate: plate,
                                type: "tax",
                                expiryDate: taxDate,
                                daysRemaining: days,
                            });
                        }
                    }

                    // Check Insurance Expiry
                    if (data.insuranceExpiryDate) {
                        const insDate = new Date(data.insuranceExpiryDate);
                        const days = Math.ceil((insDate.getTime() - now.getTime()) / (1000 * 60 * 60 * 24));
                        if (days <= warningThresholdDays) {
                            complianceList.push({
                                id: truckId,
                                licensePlate: plate,
                                type: "insurance",
                                expiryDate: insDate,
                                daysRemaining: days,
                            });
                        }
                    }

                    // Check Service Expiry (Date)
                    if (data.nextServiceDate) {
                        const serviceDate = new Date(data.nextServiceDate);
                        const days = Math.ceil((serviceDate.getTime() - now.getTime()) / (1000 * 60 * 60 * 24));
                        if (days <= warningThresholdDays) {
                            complianceList.push({
                                id: truckId,
                                licensePlate: plate,
                                type: "service",
                                expiryDate: serviceDate,
                                daysRemaining: days,
                            });
                        }
                    }

                    // Check Service Expiry (Mileage)
                    if (data.nextServiceMileage && data.currentMileage !== undefined) {
                        const kmsLeft = data.nextServiceMileage - data.currentMileage;
                        if (kmsLeft <= warningThresholdKm) {
                            // Use a mock date for sorting purposes if date is missing, or use nextServiceDate if available
                            const sortDate = data.nextServiceDate ? new Date(data.nextServiceDate) : new Date();

                            // Check if we already have a service alert for this truck (prefer date if both exist & close, or show both?)
                            // For simplicity, let's treat it as a separate alert if it's distinct enough or just add another item.
                            // Better UX: consolidated alert. But let's add a separate item with type 'service' but distinct display.
                            complianceList.push({
                                id: truckId,
                                licensePlate: plate,
                                type: "service",
                                expiryDate: sortDate, // Display purposes
                                kmsRemaining: kmsLeft,
                            });
                        }
                    }
                });

                // Sort by urgency (days or kms) - simplifying to generic urgency
                // Prioritize items with negative values (overdue)
                complianceList.sort((a, b) => {
                    const aVal = a.daysRemaining ?? (a.kmsRemaining !== undefined ? a.kmsRemaining / 100 : 999);
                    const bVal = b.daysRemaining ?? (b.kmsRemaining !== undefined ? b.kmsRemaining / 100 : 999);
                    return aVal - bVal;
                });

                setItems(complianceList);
            } catch (err) {
                console.error("Failed to fetch compliance data", err);
            } finally {
                setLoading(false);
            }
        }

        fetchComplianceData();
    }, []);

    if (loading) {
        return (
            <Card>
                <CardHeader>
                    <CardTitle className="text-sm font-medium">Compliance Alerts</CardTitle>
                </CardHeader>
                <CardContent>
                    <div className="text-sm text-muted-foreground">Loading...</div>
                </CardContent>
            </Card>
        );
    }

    if (items.length === 0) {
        return (
            <Card className="bg-green-50/50 border-green-100">
                <CardHeader className="pb-2">
                    <CardTitle className="text-lg font-medium flex items-center gap-2 text-green-700">
                        <CheckCircle2 className="h-5 w-5" />
                        Compliance Status
                    </CardTitle>
                </CardHeader>
                <CardContent>
                    <p className="text-sm text-green-600">All vehicles are compliant. No immediate actions required.</p>
                </CardContent>
            </Card>
        );
    }

    return (
        <Card className="border-red-100 shadow-sm">
            <CardHeader className="pb-3 border-b border-red-50 bg-red-50/30">
                <CardTitle className="text-lg font-medium flex items-center gap-2 text-red-700">
                    <ShieldAlert className="h-5 w-5" />
                    Action Required
                    <Badge variant="destructive" className="ml-auto">
                        {items.length} Alerts
                    </Badge>
                </CardTitle>
            </CardHeader>
            <CardContent className="p-0">
                <div className="divide-y divide-red-50 max-h-[400px] overflow-y-auto">
                    {items.map((item, idx) => (
                        <Link href={`/admin/trucks/${item.id}/edit`} key={`${item.id}-${item.type}-${idx}`} className="flex items-center justify-between p-4 hover:bg-muted/50 transition-colors">
                            <div className="flex items-start gap-3">
                                <div className={`mt-0.5 p-1.5 rounded-full ${getItemColor(item.type)}`}>
                                    {getItemIcon(item.type)}
                                </div>
                                <div className="space-y-1">
                                    <p className="text-sm font-medium leading-none">
                                        {item.licensePlate}
                                    </p>
                                    <div className="text-xs text-muted-foreground flex items-center gap-1">
                                        <span className="capitalize font-semibold">{item.type}</span>
                                        <span>
                                            {item.kmsRemaining !== undefined
                                                ? `due in ${item.kmsRemaining} km`
                                                : `expiring on ${item.expiryDate.toLocaleDateString('en-GB')}`
                                            }
                                        </span>
                                    </div>
                                </div>
                            </div>
                            <div className="text-right">
                                <StatusBadge item={item} />
                            </div>
                        </Link>
                    ))}
                </div>
            </CardContent>
        </Card>
    );
}

function StatusBadge({ item }: { item: ComplianceItem }) {
    if (item.kmsRemaining !== undefined) {
        const isOverdue = item.kmsRemaining < 0;
        return (
            <Badge variant={item.kmsRemaining < 500 ? "destructive" : "secondary"} className={isOverdue ? "bg-red-600 hover:bg-red-700" : ""}>
                {isOverdue
                    ? `${Math.abs(item.kmsRemaining).toLocaleString()} km overdue`
                    : `${item.kmsRemaining.toLocaleString()} km left`}
            </Badge>
        );
    }

    if (item.daysRemaining !== undefined) {
        const isOverdue = item.daysRemaining < 0;
        return (
            <Badge variant={item.daysRemaining < 7 ? "destructive" : "secondary"} className={isOverdue ? "bg-red-600 hover:bg-red-700" : ""}>
                {isOverdue
                    ? `${Math.abs(item.daysRemaining)} days overdue`
                    : `${item.daysRemaining} days left`}
            </Badge>
        );
    }
    return null;
}

function getItemIcon(type: string) {
    switch (type) {
        case "tax": return <AlertTriangle className="h-4 w-4 text-orange-600" />;
        case "insurance": return <ShieldAlert className="h-4 w-4 text-blue-600" />;
        case "service": return <CalendarDays className="h-4 w-4 text-purple-600" />;
        default: return <AlertTriangle className="h-4 w-4" />;
    }
}

function getItemColor(type: string) {
    switch (type) {
        case "tax": return "bg-orange-100";
        case "insurance": return "bg-blue-100";
        case "service": return "bg-purple-100";
        default: return "bg-gray-100";
    }
}
