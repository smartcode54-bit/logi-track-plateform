"use client";

import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useLanguage } from "@/context/language";
import type { PnlResult } from "@/lib/pnlCompute";
import { fixedCategoryLabel, fmtNum, fmtPct, fmtThb } from "../utils/format";

/** `text` (already translated) wins over `t(key, params)` — used for owner-named fixed-cost categories. */
type Line = { key: string; text?: string; amount: number; kind?: "subtotal" | "total" | "cost" | "info"; params?: Record<string, string | number> };

/** Month P&L (R16), break-even (R17), off-plan km and maintenance variance per truck (R18). */
export function PnlMonthStatement({ result }: { result: PnlResult }) {
    const { t } = useLanguage();
    const s = result.statement;
    const be = result.breakEven;

    const lines: Line[] = [
        { key: "pnl.line.revenue", amount: s.revenueThb, params: { trips: s.countedTrips, standbys: s.countedStandbys } },
        { key: "pnl.line.tripFuel", amount: -s.tripFuelThb, kind: "cost" },
        { key: "pnl.line.driverPay", amount: -s.driverPayThb, kind: "cost" },
        { key: "pnl.line.helperShare", amount: -s.helperShareThb, kind: "cost" },
        { key: "pnl.line.maintAccrual", amount: -s.maintAccrualThb, kind: "cost" },
        { key: "pnl.line.contribution", amount: s.contributionThb, kind: "subtotal" },
        { key: "pnl.line.offPlanFuel", amount: -s.offPlanFuelThb, kind: "cost" },
        { key: "pnl.line.fuelReconciliation", amount: -s.fuelReconciliationThb, kind: "cost", params: { cash: fmtThb(s.fuelCashThb) } },
        { key: "pnl.line.helperNoTrip", amount: -s.helperNoTripThb, kind: "cost" },
        { key: "pnl.line.toll", amount: -s.tollThb, kind: "cost" },
        { key: "pnl.line.otherVehicleExpenses", amount: -s.otherVehicleExpenseThb, kind: "cost" },
        { key: "pnl.line.maintenanceVariance", amount: -s.maintVarianceThb, kind: "cost", params: { actual: fmtThb(s.maintActualThb) } },
        { key: "pnl.line.afterMonthLines", amount: s.afterMonthLinesThb, kind: "subtotal" },
        { key: "pnl.line.insurance", amount: -s.insuranceThb, kind: "cost" },
        { key: "pnl.line.vehicleTax", amount: -s.vehicleTaxThb, kind: "cost" },
        ...s.fixedItemLines.map((l) => ({
            key: l.key,
            text: fixedCategoryLabel(l.category, l.label, t),
            amount: -l.amountThb,
            kind: "cost" as const,
        })),
        { key: "pnl.line.operatingProfit", amount: s.operatingProfitThb, kind: "total" },
    ];
    if (s.insuranceClaimInfoThb > 0) {
        lines.push({ key: "pnl.line.insuranceClaimInfo", amount: s.insuranceClaimInfoThb, kind: "info" });
    }

    return (
        <div className="grid gap-4 lg:grid-cols-3">
            <Card className="lg:col-span-2">
                <CardHeader>
                    <CardTitle className="text-base">{t("pnl.month.statementTitle")}</CardTitle>
                </CardHeader>
                <CardContent>
                    <Table>
                        <TableBody>
                            {lines.map((l) => (
                                <TableRow key={l.key} className={l.kind === "subtotal" || l.kind === "total" ? "bg-muted/50 font-semibold" : ""}>
                                    <TableCell className={l.kind === "cost" ? "pl-8" : l.kind === "info" ? "pl-8 text-muted-foreground italic" : ""}>
                                        {l.text ?? t(l.key, l.params)}
                                    </TableCell>
                                    <TableCell className={`text-right tabular-nums ${l.amount < 0 ? "text-red-600 dark:text-red-400" : ""} ${l.kind === "total" ? "text-lg" : ""}`}>
                                        {fmtThb(l.amount)}
                                    </TableCell>
                                </TableRow>
                            ))}
                        </TableBody>
                    </Table>
                </CardContent>
            </Card>

            <div className="space-y-4">
                <Card>
                    <CardHeader>
                        <CardTitle className="text-base">{t("pnl.breakEven.title")}</CardTitle>
                    </CardHeader>
                    <CardContent className="space-y-2 text-sm">
                        {be.primaryTrips === null ? (
                            <p className="text-destructive font-medium">{t("pnl.breakEven.unreachable")}</p>
                        ) : (
                            <>
                                <p className="text-3xl font-bold tabular-nums">{fmtNum(be.primaryTrips, 0)}</p>
                                <p className="text-muted-foreground">{t("pnl.breakEven.primaryHint", { fixed: fmtThb(s.fixedCostsThb), avg: fmtThb(be.avgContributionThb) })}</p>
                                <p>
                                    {t("pnl.breakEven.withMonthLines")}: <span className="font-semibold tabular-nums">{fmtNum(be.withMonthLinesTrips, 0)}</span>
                                </p>
                            </>
                        )}
                        <p>
                            {t("pnl.breakEven.actual")}: <span className="font-semibold tabular-nums">{be.actualTrips}</span>
                        </p>
                    </CardContent>
                </Card>
                <Card>
                    <CardHeader>
                        <CardTitle className="text-base">{t("pnl.month.subcontractorTitle")}</CardTitle>
                    </CardHeader>
                    <CardContent className="text-sm space-y-1">
                        <p>{t("pnl.month.subcontractorTrips", { count: result.subcontractor.trips })}</p>
                        <p>{t("pnl.month.subcontractorRevenue", { amount: fmtThb(result.subcontractor.revenueThb) })}</p>
                        <p className="text-xs text-muted-foreground">{t("pnl.month.subcontractorNote")}</p>
                    </CardContent>
                </Card>
            </div>

            <Card className="lg:col-span-3">
                <CardHeader>
                    <CardTitle className="text-base">{t("pnl.month.perTruckTitle")}</CardTitle>
                </CardHeader>
                <CardContent className="overflow-x-auto">
                    <Table>
                        <TableHeader>
                            <TableRow>
                                <TableHead>{t("pnl.col.truck")}</TableHead>
                                <TableHead className="text-right">{t("pnl.col.odoKm")}</TableHead>
                                <TableHead className="text-right">{t("pnl.col.jobKm")}</TableHead>
                                <TableHead className="text-right">{t("pnl.col.offPlanKm")}</TableHead>
                                <TableHead className="text-right">{t("pnl.col.fuelRate")}</TableHead>
                                <TableHead className="text-right">{t("pnl.line.offPlanFuel")}</TableHead>
                                <TableHead className="text-right">{t("pnl.col.maintAccrual")}</TableHead>
                                <TableHead className="text-right">{t("pnl.col.maintPm")}</TableHead>
                                <TableHead className="text-right">{t("pnl.col.maintCm")}</TableHead>
                                <TableHead className="text-right">{t("pnl.col.maintVariance")}</TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {result.trucks.map((tr) => (
                                <TableRow key={tr.truckId}>
                                    <TableCell className="font-medium">{tr.plate}</TableCell>
                                    <TableCell className="text-right tabular-nums">{fmtNum(tr.odoKm)}</TableCell>
                                    <TableCell className="text-right tabular-nums">{fmtNum(tr.jobKm)}</TableCell>
                                    <TableCell className="text-right tabular-nums">
                                        {fmtNum(tr.offPlanKm)} <span className="text-xs text-muted-foreground">({fmtPct(tr.offPlanPct)})</span>
                                    </TableCell>
                                    <TableCell className="text-right tabular-nums">
                                        {fmtThb(tr.fuelRateThbPerKm)}
                                        {tr.fuelRateIsFleetAvg && <Badge variant="outline" className="ml-1">{t("pnl.badge.fleetAvgFuelRate")}</Badge>}
                                    </TableCell>
                                    <TableCell className="text-right tabular-nums">{fmtThb(tr.offPlanFuelThb)}</TableCell>
                                    <TableCell className="text-right tabular-nums">{fmtThb(tr.maintAccrualThb)}</TableCell>
                                    <TableCell className="text-right tabular-nums">{fmtThb(tr.maintActualPmThb)}</TableCell>
                                    <TableCell className="text-right tabular-nums">{fmtThb(tr.maintActualCmThb)}</TableCell>
                                    <TableCell className="text-right tabular-nums">
                                        {fmtThb(tr.maintActualPmThb + tr.maintActualCmThb - tr.maintAccrualThb)}
                                    </TableCell>
                                </TableRow>
                            ))}
                        </TableBody>
                    </Table>
                </CardContent>
            </Card>
        </div>
    );
}
