"use client";

/**
 * Rate card over Go (T42, P3; Appendix E §E.2.3 row "rate-card"; developer-spec.md §6.12, §10.7; R16,
 * R20, R51, ADR 0009).
 *
 * One key per table: `['rateCard','entries',cust]` (server pages of 100), `['rateCard','fuelAdj',cust]`,
 * `['rateCard','serviceFees']`, `['rateCard','standby']`, `['fuel','monthly']`, each read only while its
 * tab is shown. A write invalidates only the key it touched (and that table's `all` view): the Firestore
 * page reloaded all seven collections after each of its twelve writes (`rate-card/page.tsx:384-418`).
 * SSE `ratecard.changed {customerId, table}` does the same for other tabs (lib/realtime/eventMap.ts).
 * Rate entries, fuel adjustments and standby rates are void-only; a correction is a new round.
 * "Recompute billing" starts the billing backfill jobs (`BackfillPanel`).
 */
import { useState } from "react";
import { Calculator } from "lucide-react";

import { useLanguage } from "@/context/language";
import { useCan } from "@/features/auth/api/useMe";
import { format, startOfMonth } from "date-fns";
import { CustomerSearchSelect } from "@/components/entity-pickers";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";

import { ALL_CUSTOMERS } from "../../api/rateCard.go";
import { BackfillPanel } from "./BackfillPanel";
import { EntriesTab } from "./rateCard/EntriesTab";
import { FuelAdjustmentsTab } from "./rateCard/FuelAdjustmentsTab";
import { FuelMonthlyTab, ServiceFeesTab, StandbyRatesTab } from "./rateCard/OtherTabs";

type Tab = "entries" | "fuelAdj" | "serviceFees" | "standby" | "fuelMonthly";

export function RateCardPage() {
    const { t } = useLanguage();
    const canEdit = useCan("accounting:edit_rate_card").allowed;
    const canRecompute = useCan("accounting:recompute_force").allowed;
    // A customer searched on the server (`CustomerSearchSelect`); each tab names its rows' customers by id.
    const [customerId, setCustomerId] = useState<string>(ALL_CUSTOMERS);
    const [tab, setTab] = useState<Tab>("entries");
    const [recompute, setRecompute] = useState(false);
    const now = new Date();

    return (
        <div className="container mx-auto max-w-[1600px] space-y-6 p-6">
            <div className="flex flex-col justify-between gap-4 md:flex-row md:items-center">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">{t("accounting.rateCard.title")}</h1>
                    <p className="mt-1 text-muted-foreground">{t("accounting.rateCard.subtitle")}</p>
                </div>
                {canEdit || canRecompute ? (
                    <Button variant="outline" onClick={() => setRecompute(true)}>
                        <Calculator className="mr-2 h-4 w-4" />
                        {t("accounting.go.rateCard.recompute")}
                    </Button>
                ) : null}
            </div>

            <Card>
                <CardContent className="flex flex-wrap items-end gap-4 pt-4">
                    <div className="space-y-1">
                        <Label>{t("accounting.rateCard.filterCustomer")}</Label>
                        <CustomerSearchSelect
                            aria-label={t("accounting.rateCard.filterCustomer")}
                            value={customerId}
                            onValueChange={(id) => setCustomerId(id || ALL_CUSTOMERS)}
                            allValue={ALL_CUSTOMERS}
                            allLabel={t("accounting.rateCard.filterCustomerAll")}
                            className="w-72"
                        />
                    </div>
                    <p className="text-xs text-muted-foreground">{t("accounting.go.rateCard.customerHint")}</p>
                </CardContent>
            </Card>

            <Tabs value={tab} onValueChange={(v) => setTab(v as Tab)}>
                <TabsList className="flex-wrap">
                    <TabsTrigger value="entries">{t("accounting.rateCard.title")}</TabsTrigger>
                    <TabsTrigger value="fuelAdj">{t("accounting.rateCard.fuelAdjustments.title")}</TabsTrigger>
                    <TabsTrigger value="serviceFees">{t("accounting.serviceFees.title")}</TabsTrigger>
                    <TabsTrigger value="standby">{t("accounting.standbyRates.title")}</TabsTrigger>
                    <TabsTrigger value="fuelMonthly">{t("accounting.rateCard.fuelMonthly.title")}</TabsTrigger>
                </TabsList>
                <Card className="mt-4">
                    <CardContent className="pt-4">
                        {/* Each tab mounts (and so reads its key) only while it is shown. */}
                        <TabsContent value="entries" className="mt-0">
                            {tab === "entries" ? <EntriesTab customerId={customerId} canEdit={canEdit} /> : null}
                        </TabsContent>
                        <TabsContent value="fuelAdj" className="mt-0">
                            {tab === "fuelAdj" ? <FuelAdjustmentsTab customerId={customerId} canEdit={canEdit} /> : null}
                        </TabsContent>
                        <TabsContent value="serviceFees" className="mt-0">
                            {tab === "serviceFees" ? <ServiceFeesTab customerId={customerId} canEdit={canEdit} /> : null}
                        </TabsContent>
                        <TabsContent value="standby" className="mt-0">
                            {tab === "standby" ? <StandbyRatesTab customerId={customerId} canEdit={canEdit} /> : null}
                        </TabsContent>
                        <TabsContent value="fuelMonthly" className="mt-0">
                            {tab === "fuelMonthly" ? <FuelMonthlyTab /> : null}
                        </TabsContent>
                    </CardContent>
                </Card>
            </Tabs>

            <Dialog open={recompute} onOpenChange={setRecompute}>
                <DialogContent className="sm:max-w-2xl">
                    <DialogHeader>
                        <DialogTitle>{t("accounting.go.rateCard.recompute")}</DialogTitle>
                        <DialogDescription>{t("accounting.income.backfill.subtitle")}</DialogDescription>
                    </DialogHeader>
                    <BackfillPanel defaultFrom={format(startOfMonth(now), "yyyy-MM-dd")} defaultTo={format(now, "yyyy-MM-dd")} />
                </DialogContent>
            </Dialog>
        </div>
    );
}
