"use client";

import { useLanguage } from "@/context/language";
import { Card, CardContent } from "@/components/ui/card";
import { History, AlertTriangle } from "lucide-react";

interface MaintenanceStatsProps {
    totalPMCost: number;
    totalCMCost: number;
}

export function MaintenanceStats({ totalPMCost, totalCMCost }: MaintenanceStatsProps) {
    const { t } = useLanguage();

    return (
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <Card className="border-l-4 border-l-blue-500 bg-blue-50/10">
                <CardContent className="p-4 pt-4">
                    <div className="flex justify-between items-center">
                        <h3 className="text-sm font-medium text-blue-600 mb-1">{t("maintenance.form.preventiveCost")}</h3>
                        <History className="h-4 w-4 text-blue-500" />
                    </div>
                    <div className="text-2xl font-bold text-blue-700">฿{totalPMCost.toLocaleString()}</div>
                    <p className="text-xs text-muted-foreground mt-1">{t("maintenance.pmCosts")}</p>
                </CardContent>
            </Card>
            <Card className="border-l-4 border-l-red-500 bg-red-50/10">
                <CardContent className="p-4 pt-4">
                    <div className="flex justify-between items-center">
                        <h3 className="text-sm font-medium text-red-600 mb-1">{t("maintenance.form.correctiveCost")}</h3>
                        <AlertTriangle className="h-4 w-4 text-red-500" />
                    </div>
                    <div className="text-2xl font-bold text-red-700">฿{totalCMCost.toLocaleString()}</div>
                    <p className="text-xs text-muted-foreground mt-1">{t("maintenance.cmCosts")}</p>
                </CardContent>
            </Card>
        </div>
    );
}
