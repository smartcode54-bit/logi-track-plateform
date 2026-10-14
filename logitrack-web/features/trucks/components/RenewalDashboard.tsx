"use client";

import { useState } from "react";
import { useSearchParams } from "next/navigation";
import { useLanguage } from "@/context/language";
import { useTruck } from "../api/useTrucks";
import { Button } from "@/components/ui/button";
import { Loader2, RefreshCw } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { RecordState } from "@/components/record-state";
import { formatLicensePlate } from "@/lib/utils";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { RenewalForm } from "./renew/RenewalForm";

export default function RenewalDashboard() {
    const { t } = useLanguage();
    const searchParams = useSearchParams();
    const id = searchParams.get("id") ?? "";
    // `['trucks','detail',id]` (T25); a saved renewal refetches it.
    const query = useTruck(id);
    const truck = query.data ?? null;
    const loading = query.isPending && Boolean(id);
    const [activeTab, setActiveTab] = useState<string>(searchParams.get("type") === "insurance" ? "insurance" : "tax");
    const loadData = () => void query.refetch();

    if (loading) return <div className="flex min-h-[50vh] justify-center items-center" role="status" aria-busy="true"><Loader2 className="animate-spin h-8 w-8 text-primary" aria-hidden /><span className="sr-only">{t("common.loading")}</span></div>;
    if (!truck) return <RecordState error={query.error} onRetry={loadData} retrying={query.isFetching} />;

    return (
        <div className="flex max-w-4xl flex-col gap-section">
            <PageHeader
                title={t("nav.trucks")}
                recordName={formatLicensePlate(truck.licensePlate)}
                back={{ href: `/app/trucks/view?id=${truck.id}`, label: t("trucks.edit.backToDetails") }}
                description={`${t("nav.truckRenewals")} · ${[truck.brand, truck.model].filter(Boolean).join(" ")}`}
                actions={
                    <Button variant="outline" onClick={loadData} disabled={query.isFetching}>
                        <RefreshCw className="h-4 w-4" aria-hidden />
                        {t("common.refresh")}
                    </Button>
                }
            />

            {/* Content Tabs */}
            <Tabs value={activeTab} onValueChange={setActiveTab} className="space-y-6">
                <TabsList className="grid w-full grid-cols-2 max-w-md bg-muted/50 p-1">
                    <TabsTrigger value="tax" className="data-[state=active]:bg-background data-[state=active]:shadow-sm">
                        {t("renewals.form.tab.tax")}
                    </TabsTrigger>
                    <TabsTrigger value="insurance" className="data-[state=active]:bg-background data-[state=active]:shadow-sm">
                        {t("renewals.form.tab.insurance")}
                    </TabsTrigger>
                </TabsList>

                <TabsContent value="tax" className="mt-0">
                    <RenewalForm
                        type="tax"
                        truck={truck}
                        onSuccess={loadData}
                    />
                </TabsContent>

                <TabsContent value="insurance" className="mt-0">
                    <RenewalForm
                        type="insurance"
                        truck={truck}
                        onSuccess={loadData}
                    />
                </TabsContent>
            </Tabs>
        </div>
    );
}
