"use client";

/**
 * Billing utilities over Go (T42, P3; developer-spec.md §10.13 row `/app/utilities/{backfill,billing-impact}`,
 * gated by `accounting:recompute_force`). The billing backfills run as Go jobs
 * (`POST /v1/jobs/billing.backfill-trips|backfill-standby`, followed through `['jobs',id]`) instead of
 * 540 s callables. The one-off Firestore repairs of the Firestore page (task customer links, trip truck
 * data, truck-type and job-category migrations) are not offered here: on PostgreSQL the ETL load applies
 * them (`cmd/etl`, developer-spec.md §13), so there is nothing left for a button to fix.
 */
import { format, startOfMonth } from "date-fns";
import { Info, Wand2 } from "lucide-react";

import { useLanguage } from "@/context/language";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

import { BackfillPanel } from "./BackfillPanel";

export function BackfillUtilitiesPage() {
    const { t } = useLanguage();
    const now = new Date();
    return (
        <div className="container mx-auto max-w-5xl space-y-6 p-6">
            <div>
                <h1 className="text-3xl font-bold">{t("nav.utilities")}</h1>
                <p className="mt-2 text-muted-foreground">{t("accounting.go.utilities.subtitle")}</p>
            </div>
            <Card>
                <CardHeader>
                    <CardTitle className="flex items-center gap-2">
                        <Wand2 className="h-5 w-5" />
                        {t("accounting.income.backfill.title")}
                    </CardTitle>
                    <CardDescription>{t("accounting.income.backfill.subtitle")}</CardDescription>
                </CardHeader>
                <CardContent>
                    <BackfillPanel defaultFrom={format(startOfMonth(now), "yyyy-MM-dd")} defaultTo={format(now, "yyyy-MM-dd")} />
                </CardContent>
            </Card>
            <Alert>
                <Info className="h-4 w-4" />
                <AlertTitle>{t("accounting.go.utilities.etlTitle")}</AlertTitle>
                <AlertDescription>{t("accounting.go.utilities.etlBody")}</AlertDescription>
            </Alert>
        </div>
    );
}
