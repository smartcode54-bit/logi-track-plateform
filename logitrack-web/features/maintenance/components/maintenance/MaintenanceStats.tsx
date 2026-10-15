"use client";

import { useLanguage } from "@/context/language";
import { Card, CardContent } from "@/components/ui/card";
import { History, AlertTriangle } from "lucide-react";

interface MaintenanceStatsProps {
    totalPMCost: number;
    totalCMCost: number;
    loadedCount?: number;
}

export function MaintenanceStats({ totalPMCost, totalCMCost, loadedCount }: MaintenanceStatsProps) {
    const { t } = useLanguage();

    const scope = loadedCount == null ? t("maintenance.go.ofLoaded") : t("maintenance.go.loadedCosts", { count: loadedCount });
    return (
        <div className="space-y-2">
            <p className="text-xs text-muted-foreground">{scope}</p>
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                <Card>
                    <CardContent className="p-4 pt-4">
                        <div className="flex justify-between items-center">
                            <h3 className="text-sm font-medium text-muted-foreground mb-1">{t("maintenance.form.preventiveCost")}</h3>
                            <History className="h-4 w-4 text-muted-foreground" />
                        </div>
                        <div className="text-2xl font-semibold tabular-nums">฿{totalPMCost.toLocaleString()}</div>
                    </CardContent>
                </Card>
                <Card>
                    <CardContent className="p-4 pt-4">
                        <div className="flex justify-between items-center">
                            <h3 className="text-sm font-medium text-muted-foreground mb-1">{t("maintenance.form.correctiveCost")}</h3>
                            <AlertTriangle className="h-4 w-4 text-muted-foreground" />
                        </div>
                        <div className="text-2xl font-semibold tabular-nums">฿{totalCMCost.toLocaleString()}</div>
                    </CardContent>
                </Card>
            </div>
        </div>
    );
}
