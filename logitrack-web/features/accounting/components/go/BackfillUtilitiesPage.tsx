"use client";

/** Billing backfill jobs and the read-only billing impact report. */
import { bangkokMonthRange } from "../../lib/billingUiDates";
import Link from "next/link";
import { Wand2 } from "lucide-react";

import { useLanguage } from "@/context/language";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

import { BackfillPanel } from "./BackfillPanel";

export function BackfillUtilitiesPage() {
    const { t } = useLanguage();
    const range = bangkokMonthRange();
    return (
        <div className="flex max-w-5xl flex-col gap-section">
            <PageHeader title={t("nav.utilities")} description={t("accounting.go.utilities.subtitle")} actions={<Button asChild className="max-md:min-h-11" variant="outline"><Link href="/app/utilities/billing-impact">{t("accounting.ui4.billingImpact")}</Link></Button>} />
            <Card>
                <CardHeader>
                    <CardTitle className="flex items-center gap-2">
                        <Wand2 className="h-5 w-5" />
                        {t("accounting.income.backfill.title")}
                    </CardTitle>
                    <CardDescription>{t("accounting.income.backfill.subtitle")}</CardDescription>
                </CardHeader>
                <CardContent>
                    <BackfillPanel defaultFrom={range.from} defaultTo={range.to} />
                </CardContent>
            </Card>

        </div>
    );
}
