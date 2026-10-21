"use client";

import { useLanguage } from "@/context/language";
import { Card, CardContent } from "@/components/ui/card";

interface SubcontractorStatsProps {
    stats: {
        totalPartners: number;
        activeTrucks: number;
        pendingContracts: number;
    };
    loading: boolean;
}

export function SubcontractorStats({ stats, loading }: SubcontractorStatsProps) {
    const { t } = useLanguage();
    const items = [
        { label: "subcontractors.stats.totalPartners", value: stats.totalPartners },
        { label: "subcontractors.stats.activeTrucks", value: stats.activeTrucks },
        { label: "subcontractors.stats.pendingContracts", value: stats.pendingContracts },
    ];

    return (
        <div className="grid grid-cols-3 gap-2 md:gap-4">
            {items.map(({ label, value }) => (
                <Card key={label}>
                    <CardContent className="space-y-2 p-6 max-md:p-2">
                        <p className="text-sm max-md:text-xs break-words font-medium text-muted-foreground">{t(label)}</p>
                        <p className="text-2xl font-semibold tabular-nums">{loading ? "-" : value}</p>
                        <p className="text-xs text-muted-foreground">{t(`${label}.desc`)}</p>
                    </CardContent>
                </Card>
            ))}
        </div>
    );
}
