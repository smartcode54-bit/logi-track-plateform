"use client";

/**
 * Billing impact report (ADR 0009, spec R14) — READ-ONLY.
 *
 * Two pricing defects were fixed going forward: the fuel band charged one step too much when the
 * reference price ended in exactly `x.00`, and an announcement's boundary sat at 07:00 ICT instead
 * of Bangkok midnight. Neither is repaired retroactively — the owner's decision was fix-forward and
 * report. This page is that report: it names the customers, rounds, and trips affected so the
 * credit-note question can be answered with numbers, and it writes nothing.
 */

import { useState } from "react";
import { httpsCallable } from "firebase/functions";
import { AlertCircle, FileSearch, Loader2 } from "lucide-react";
import { functions } from "@/firebase/client";
import { useAuth } from "@/context/auth";
import { useLanguage } from "@/context/language";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

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

export default function BillingImpactPage() {
    const auth = useAuth();
    const { t } = useLanguage();
    const [fromDate, setFromDate] = useState(() => {
        const d = new Date();
        d.setMonth(d.getMonth() - 3);
        return d.toISOString().slice(0, 10);
    });
    const [toDate, setToDate] = useState(() => new Date().toISOString().slice(0, 10));
    const [loading, setLoading] = useState(false);
    const [result, setResult] = useState<ImpactReportResponse | null>(null);
    const [error, setError] = useState<string | null>(null);

    const run = async () => {
        if (!auth?.currentUser) return;
        setLoading(true);
        setError(null);
        setResult(null);
        try {
            const fn = httpsCallable(functions, "billingImpactReport");
            const res = await fn({ fromDateStr: fromDate, toDateStr: toDate });
            setResult(res.data as ImpactReportResponse);
        } catch (err) {
            console.error(err);
            setError(err instanceof Error ? err.message : String(err));
        } finally {
            setLoading(false);
        }
    };

    return (
        <div className="space-y-6 p-6">
            <div>
                <h1 className="text-2xl font-semibold">{t("accounting.impact.title")}</h1>
                <p className="text-sm text-muted-foreground mt-1">{t("accounting.impact.subtitle")}</p>
            </div>

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
                    <div className="grid gap-4 sm:grid-cols-2">
                        <div className="space-y-1.5">
                            <Label>{t("accounting.impact.fromDate")}</Label>
                            <Input type="date" value={fromDate} onChange={(e) => setFromDate(e.target.value)} />
                        </div>
                        <div className="space-y-1.5">
                            <Label>{t("accounting.impact.toDate")}</Label>
                            <Input type="date" value={toDate} onChange={(e) => setToDate(e.target.value)} />
                        </div>
                    </div>
                    <Button onClick={() => void run()} disabled={loading}>
                        {loading ? (
                            <Loader2 className="h-4 w-4 mr-2 animate-spin" />
                        ) : (
                            <FileSearch className="h-4 w-4 mr-2" />
                        )}
                        {t("accounting.impact.run")}
                    </Button>

                    {error && (
                        <Alert variant="destructive">
                            <AlertCircle className="h-4 w-4" />
                            <AlertTitle>{t("accounting.impact.error")}</AlertTitle>
                            <AlertDescription className="font-mono text-xs">{error}</AlertDescription>
                        </Alert>
                    )}
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
                                                    <TableCell className="font-mono text-xs">{r.customerId}</TableCell>
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
                                                    <TableCell className="font-mono text-xs">{r.customerId}</TableCell>
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
