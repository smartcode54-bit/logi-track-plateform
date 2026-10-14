"use client";

/**
 * Billing impact report over Go (T42, P3; ADR 0009, R14) — READ-ONLY: `POST /v1/jobs/billing.impact-report`
 * (`{fromDateStr, toDateStr}` -> `202 {jobId}`), followed through `['jobs',id]`; the report is the finished
 * job's result (the `billingImpactReport` callable held a request open for up to 540 s). It names the
 * customers, rounds and trips the two corrected pricing defects affected, and writes nothing.
 */
import { useState } from "react";
import { enUS, th as thDateLocale } from "date-fns/locale";
import { AlertCircle, FileSearch, Loader2 } from "lucide-react";

import { useLanguage } from "@/context/language";
import { PageHeader } from "@/components/page-header";
import { apiErrorText } from "@/lib/apiError";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { DateOnlyRangePicker } from "@/components/ui/date-range-picker";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

import { useBillingJob } from "../../api/useBillingJob";
import { useCustomerLookup } from "../../api/useCustomerLookup";

interface BandImpactRow {
    adjustmentId: string;
    customerId: string;
    effectiveFromDateStr: string;
    referenceFuelPriceThb: number;
    storedAddThbPerTrip: number;
    correctedAddThbPerTrip: number;
    deltaThbPerTrip: number;
}

interface BoundaryImpactRow {
    tripId: string;
    customerId: string;
    deliveredAtIso: string;
    pricedUnderDateStr: string;
    shouldBeDateStr: string;
}

interface ImpactReportResponse {
    ok: true;
    scannedAnnouncements: number;
    scannedTrips: number;
    bandImpacts: BandImpactRow[];
    boundaryImpacts: BoundaryImpactRow[];
    periodsAffected: string[];
}

export function BillingImpactPage() {
    const { t, language } = useLanguage();
    const dateLocale = language === "th" ? thDateLocale : enUS;
    const [fromDate, setFromDate] = useState(() => {
        const d = new Date();
        d.setMonth(d.getMonth() - 3);
        return d.toISOString().slice(0, 10);
    });
    const [toDate, setToDate] = useState(() => new Date().toISOString().slice(0, 10));
    const job = useBillingJob<ImpactReportResponse>();
    const result = job.result ?? null;
    // The report's customers only, once it has rows (no customer is read before a report runs).
    const customers = useCustomerLookup([...(result?.bandImpacts ?? []), ...(result?.boundaryImpacts ?? [])].map((r) => r.customerId));
    const customerName = (id: string) => customers.label(id);

    const run = () => job.start("billing.impact-report", { fromDateStr: fromDate, toDateStr: toDate });

    return (
        <div className="flex flex-col gap-section">
            <PageHeader title={t("nav.billingImpact")} description={t("accounting.impact.subtitle")} />

            <Alert>
                <FileSearch className="h-4 w-4" />
                <AlertTitle>{t("accounting.impact.readOnlyTitle")}</AlertTitle>
                <AlertDescription>{t("accounting.impact.readOnlyBody")}</AlertDescription>
            </Alert>

            <Card>
                <CardHeader>
                    <CardTitle>{t("accounting.impact.runTitle")}</CardTitle>
                    <CardDescription>{t("accounting.impact.runDescription")}</CardDescription>
                </CardHeader>
                <CardContent className="space-y-4">
                    <div className="space-y-1.5">
                        <Label>{t("accounting.impact.dateRange")}</Label>
                        <DateOnlyRangePicker
                            from={fromDate}
                            to={toDate}
                            onChange={(from, to) => {
                                setFromDate(from);
                                setToDate(to);
                            }}
                            locale={dateLocale}
                            className="w-[230px]"
                        />
                    </div>
                    <Button onClick={() => void run()} disabled={job.running || !fromDate || !toDate}>
                        {job.running ? (
                            <Loader2 className="h-4 w-4 mr-2 animate-spin" />
                        ) : (
                            <FileSearch className="h-4 w-4 mr-2" />
                        )}
                        {t("accounting.impact.run")}
                    </Button>

                    {job.running ? <p className="text-xs text-muted-foreground">{t("accounting.go.backfill.jobHint")}</p> : null}
                    {job.error ? (
                        <Alert variant="destructive">
                            <AlertCircle className="h-4 w-4" />
                            <AlertTitle>{t("accounting.impact.error")}</AlertTitle>
                            <AlertDescription className="font-mono text-xs">{apiErrorText(job.error, t)}</AlertDescription>
                        </Alert>
                    ) : null}
                </CardContent>
            </Card>

            {result && (
                <>
                    <div className="grid gap-4 sm:grid-cols-4">
                        <Card>
                            <CardHeader className="pb-2">
                                <CardDescription>{t("accounting.impact.stats.announcements")}</CardDescription>
                                <CardTitle className="text-2xl">{result.scannedAnnouncements}</CardTitle>
                            </CardHeader>
                        </Card>
                        <Card>
                            <CardHeader className="pb-2">
                                <CardDescription>{t("accounting.impact.stats.trips")}</CardDescription>
                                <CardTitle className="text-2xl">{result.scannedTrips}</CardTitle>
                            </CardHeader>
                        </Card>
                        <Card>
                            <CardHeader className="pb-2">
                                <CardDescription>{t("accounting.impact.stats.bandImpacts")}</CardDescription>
                                <CardTitle className="text-2xl">{result.bandImpacts.length}</CardTitle>
                            </CardHeader>
                        </Card>
                        <Card>
                            <CardHeader className="pb-2">
                                <CardDescription>{t("accounting.impact.stats.boundaryImpacts")}</CardDescription>
                                <CardTitle className="text-2xl">{result.boundaryImpacts.length}</CardTitle>
                            </CardHeader>
                        </Card>
                    </div>

                    {result.periodsAffected.length > 0 && (
                        <Card>
                            <CardHeader>
                                <CardTitle>{t("accounting.impact.periods")}</CardTitle>
                            </CardHeader>
                            <CardContent className="flex flex-wrap gap-2">
                                {result.periodsAffected.map((p) => (
                                    <Badge key={p} variant="secondary" className="font-mono">
                                        {p}
                                    </Badge>
                                ))}
                            </CardContent>
                        </Card>
                    )}

                    <Card>
                        <CardHeader>
                            <CardTitle>{t("accounting.impact.bandTitle")}</CardTitle>
                            <CardDescription>{t("accounting.impact.bandDescription")}</CardDescription>
                        </CardHeader>
                        <CardContent>
                            {result.bandImpacts.length === 0 ? (
                                <p className="text-sm text-muted-foreground">{t("accounting.impact.none")}</p>
                            ) : (
                                <div className="overflow-x-auto">
                                    <Table>
                                        <TableHeader>
                                            <TableRow>
                                                <TableHead>{t("accounting.impact.col.customer")}</TableHead>
                                                <TableHead>{t("accounting.impact.col.effectiveFrom")}</TableHead>
                                                <TableHead className="text-right">
                                                    {t("accounting.impact.col.fuelPrice")}
                                                </TableHead>
                                                <TableHead className="text-right">
                                                    {t("accounting.impact.col.stored")}
                                                </TableHead>
                                                <TableHead className="text-right">
                                                    {t("accounting.impact.col.corrected")}
                                                </TableHead>
                                                <TableHead className="text-right">
                                                    {t("accounting.impact.col.delta")}
                                                </TableHead>
                                            </TableRow>
                                        </TableHeader>
                                        <TableBody>
                                            {result.bandImpacts.map((r) => (
                                                <TableRow key={r.adjustmentId}>
                                                    <TableCell className="text-xs">{customerName(r.customerId)}</TableCell>
                                                    <TableCell className="font-mono text-xs">
                                                        {r.effectiveFromDateStr}
                                                    </TableCell>
                                                    <TableCell className="text-right font-mono text-xs">
                                                        {r.referenceFuelPriceThb.toFixed(2)}
                                                    </TableCell>
                                                    <TableCell className="text-right font-mono text-xs">
                                                        {r.storedAddThbPerTrip.toFixed(2)}
                                                    </TableCell>
                                                    <TableCell className="text-right font-mono text-xs">
                                                        {r.correctedAddThbPerTrip.toFixed(2)}
                                                    </TableCell>
                                                    <TableCell className="text-right font-mono text-xs font-semibold text-destructive">
                                                        {r.deltaThbPerTrip.toFixed(2)}
                                                    </TableCell>
                                                </TableRow>
                                            ))}
                                        </TableBody>
                                    </Table>
                                </div>
                            )}
                        </CardContent>
                    </Card>

                    <Card>
                        <CardHeader>
                            <CardTitle>{t("accounting.impact.boundaryTitle")}</CardTitle>
                            <CardDescription>{t("accounting.impact.boundaryDescription")}</CardDescription>
                        </CardHeader>
                        <CardContent>
                            {result.boundaryImpacts.length === 0 ? (
                                <p className="text-sm text-muted-foreground">{t("accounting.impact.none")}</p>
                            ) : (
                                <div className="overflow-x-auto">
                                    <Table>
                                        <TableHeader>
                                            <TableRow>
                                                <TableHead>{t("accounting.impact.col.trip")}</TableHead>
                                                <TableHead>{t("accounting.impact.col.customer")}</TableHead>
                                                <TableHead>{t("accounting.impact.col.delivered")}</TableHead>
                                                <TableHead>{t("accounting.impact.col.pricedUnder")}</TableHead>
                                                <TableHead>{t("accounting.impact.col.shouldBe")}</TableHead>
                                            </TableRow>
                                        </TableHeader>
                                        <TableBody>
                                            {result.boundaryImpacts.map((r) => (
                                                <TableRow key={r.tripId}>
                                                    <TableCell className="font-mono text-xs">{r.tripId}</TableCell>
                                                    <TableCell className="text-xs">{customerName(r.customerId)}</TableCell>
                                                    <TableCell className="font-mono text-xs">
                                                        {r.deliveredAtIso}
                                                    </TableCell>
                                                    <TableCell className="font-mono text-xs">
                                                        {r.pricedUnderDateStr}
                                                    </TableCell>
                                                    <TableCell className="font-mono text-xs">
                                                        {r.shouldBeDateStr}
                                                    </TableCell>
                                                </TableRow>
                                            ))}
                                        </TableBody>
                                    </Table>
                                </div>
                            )}
                        </CardContent>
                    </Card>
                </>
            )}
        </div>
    );
}
