"use client";

import { useLanguage } from "@/context/language";
import { Card, CardContent } from "@/components/ui/card";
import { Users, Truck, FileText } from "lucide-react";

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

    return (
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
            <Card className="bg-card/50 border-border/50 shadow-sm relative overflow-hidden group">
                <CardContent className="p-6">
                    <div className="flex justify-between items-start">
                        <div className="space-y-2">
                            <p className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">{t("subcontractors.stats.totalPartners")}</p>
                            <div className="flex items-baseline gap-2">
                                <span className="text-3xl font-bold font-mono text-foreground">
                                    {loading ? "-" : stats.totalPartners}
                                </span>
                                <span className="text-xs font-medium text-green-500">↑5%</span>
                            </div>
                            <p className="text-xs text-muted-foreground">{t("subcontractors.stats.totalPartners.desc")}</p>
                        </div>
                        <div className="p-2 bg-blue-500/10 rounded-lg text-blue-500 group-hover:scale-110 transition-transform">
                            <Users className="h-5 w-5" />
                        </div>
                    </div>
                </CardContent>
            </Card>

            <Card className="bg-card/50 border-border/50 shadow-sm relative overflow-hidden group">
                <CardContent className="p-6">
                    <div className="flex justify-between items-start">
                        <div className="space-y-2">
                            <p className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">{t("subcontractors.stats.activeTrucks")}</p>
                            <div className="flex items-baseline gap-2">
                                <span className="text-3xl font-bold font-mono text-foreground">
                                    {loading ? "-" : stats.activeTrucks}
                                </span>
                                <span className="text-xs font-medium text-green-500">↑12%</span>
                            </div>
                            <p className="text-xs text-muted-foreground">{t("subcontractors.stats.activeTrucks.desc")}</p>
                        </div>
                        <div className="p-2 bg-blue-500/10 rounded-lg text-blue-500 group-hover:scale-110 transition-transform">
                            <Truck className="h-5 w-5" />
                        </div>
                    </div>
                </CardContent>
            </Card>

            <Card className="bg-card/50 border-border/50 shadow-sm relative overflow-hidden group">
                <CardContent className="p-6">
                    <div className="flex justify-between items-start">
                        <div className="space-y-2">
                            <p className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">{t("subcontractors.stats.pendingContracts")}</p>
                            <div className="flex items-baseline gap-2">
                                <span className="text-3xl font-bold font-mono text-foreground">
                                    {loading ? "-" : stats.pendingContracts}
                                </span>
                                <span className="text-xs font-medium text-orange-500">↓2%</span>
                            </div>
                            <p className="text-xs text-muted-foreground">{t("subcontractors.stats.pendingContracts.desc")}</p>
                        </div>
                        <div className="p-2 bg-blue-500/10 rounded-lg text-blue-500 group-hover:scale-110 transition-transform">
                            <FileText className="h-5 w-5" />
                        </div>
                    </div>
                </CardContent>
            </Card>
        </div>
    );
}
