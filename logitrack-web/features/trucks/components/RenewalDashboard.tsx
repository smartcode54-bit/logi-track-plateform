"use client";

import { useState } from "react";
import { useSearchParams, useRouter } from "next/navigation";
import { useLanguage } from "@/context/language";
import { useTruck } from "../api/useTrucks";
import { Button } from "@/components/ui/button";
import { ArrowLeft, Loader2, RefreshCw } from "lucide-react";
import { formatLicensePlate } from "@/lib/utils";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { RenewalForm } from "./renew/RenewalForm";

export default function RenewalDashboard() {
    const { t } = useLanguage();
    const searchParams = useSearchParams();
    const router = useRouter();
    const id = searchParams.get("id") ?? "";
    // `['trucks','detail',id]` (T25); a saved renewal refetches it.
    const query = useTruck(id);
    const truck = query.data ?? null;
    const loading = query.isPending && Boolean(id);
    const [activeTab, setActiveTab] = useState<string>(searchParams.get("type") === "insurance" ? "insurance" : "tax");
    const loadData = () => void query.refetch();

    if (loading) return <div className="flex h-screen justify-center items-center"><Loader2 className="animate-spin h-8 w-8 text-primary" /></div>;
    if (!truck) return <div className="text-center py-12 text-muted-foreground">{t("renewals.form.notFound")}</div>;

    return (
        <div className="container mx-auto max-w-4xl p-6 space-y-6">
            {/* Header */}
            <div className="flex items-center gap-4">
                <Button variant="ghost" size="icon" onClick={() => router.back()}>
                    <ArrowLeft className="h-5 w-5" />
                </Button>
                <div>
                    <h1 className="text-2xl font-bold tracking-tight">{t("renewals.title")}</h1>
                    <p className="text-muted-foreground">
                        {truck.brand} {truck.model} - <span className="font-mono font-medium text-foreground">{formatLicensePlate(truck.licensePlate)}</span>
                    </p>
                </div>
                <div className="ml-auto">
                    <Button variant="outline" size="icon" onClick={loadData}>
                        <RefreshCw className="h-4 w-4" />
                    </Button>
                </div>
            </div>

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
