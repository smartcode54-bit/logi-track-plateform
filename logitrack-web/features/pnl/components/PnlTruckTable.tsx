"use client";

import { Badge } from "@/components/ui/badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useLanguage } from "@/context/language";
import type { PnlResult } from "@/lib/pnlCompute";
import { fmtNum, fmtPct, fmtThb } from "../utils/format";

/** Per-truck view (R18): contribution, then the truck's own fixed costs (never spread into trips). */
export function PnlTruckTable({ result }: { result: PnlResult }) {
    const { t } = useLanguage();
    return (
        <div className="overflow-x-auto rounded-md border">
            <Table>
                <TableHeader>
                    <TableRow>
                        <TableHead>{t("pnl.col.truck")}</TableHead>
                        <TableHead className="text-right">{t("pnl.col.trips")}</TableHead>
                        <TableHead className="text-right">{t("pnl.col.revenue")}</TableHead>
                        <TableHead className="text-right">{t("pnl.col.jobKm")}</TableHead>
                        <TableHead className="text-right">{t("pnl.col.odoKm")}</TableHead>
                        <TableHead className="text-right">{t("pnl.col.offPlanKm")}</TableHead>
                        <TableHead className="text-right">{t("pnl.col.fuelRate")}</TableHead>
                        <TableHead className="text-right">{t("pnl.col.tripFuel")}</TableHead>
                        <TableHead className="text-right">{t("pnl.line.offPlanFuel")}</TableHead>
                        <TableHead className="text-right">{t("pnl.col.maintAccrual")}</TableHead>
                        <TableHead className="text-right">{t("pnl.col.maintActual")}</TableHead>
                        <TableHead className="text-right">{t("pnl.trip.contribution")}</TableHead>
                        <TableHead className="text-right">{t("pnl.col.truckFixed")}</TableHead>
                        <TableHead className="text-right">{t("pnl.col.afterFixed")}</TableHead>
                    </TableRow>
                </TableHeader>
                <TableBody>
                    {result.trucks.length === 0 ? (
                        <TableRow>
                            <TableCell colSpan={14} className="text-center text-muted-foreground py-8">{t("pnl.empty")}</TableCell>
                        </TableRow>
                    ) : (
                        result.trucks.map((tr) => (
                            <TableRow key={tr.truckId}>
                                <TableCell className="font-medium whitespace-nowrap">{tr.plate}</TableCell>
                                <TableCell className="text-right tabular-nums">
                                    {tr.trips}
                                    {tr.standbys > 0 && <span className="block text-xs text-muted-foreground">+{tr.standbys} {t("pnl.kind.standby")}</span>}
                                </TableCell>
                                <TableCell className="text-right tabular-nums">{fmtThb(tr.revenueThb)}</TableCell>
                                <TableCell className="text-right tabular-nums">{fmtNum(tr.jobKm)}</TableCell>
                                <TableCell className="text-right tabular-nums">{fmtNum(tr.odoKm)}</TableCell>
                                <TableCell className="text-right tabular-nums">
                                    {fmtNum(tr.offPlanKm)}
                                    <span className="block text-xs text-muted-foreground">{fmtPct(tr.offPlanPct)}</span>
                                </TableCell>
                                <TableCell className="text-right tabular-nums">
                                    {fmtThb(tr.fuelRateThbPerKm)}
                                    {tr.fuelRateIsFleetAvg && <Badge variant="outline" className="ml-1">{t("pnl.badge.fleetAvgFuelRate")}</Badge>}
                                </TableCell>
                                <TableCell className="text-right tabular-nums">{fmtThb(tr.tripFuelThb)}</TableCell>
                                <TableCell className="text-right tabular-nums">{fmtThb(tr.offPlanFuelThb)}</TableCell>
                                <TableCell className="text-right tabular-nums">{fmtThb(tr.maintAccrualThb)}</TableCell>
                                <TableCell className="text-right tabular-nums">
                                    {fmtThb(tr.maintActualPmThb + tr.maintActualCmThb)}
                                    <span className="block text-xs text-muted-foreground">
                                        PM {fmtThb(tr.maintActualPmThb)} · CM {fmtThb(tr.maintActualCmThb)}
                                    </span>
                                </TableCell>
                                <TableCell className="text-right tabular-nums font-medium">{fmtThb(tr.contributionThb)}</TableCell>
                                <TableCell className="text-right tabular-nums">
                                    {fmtThb(tr.insuranceThb + tr.vehicleTaxThb + tr.fixedItemsThb)}
                                    <span className="block text-xs text-muted-foreground">
                                        {t("pnl.truck.fixedBreakdown", {
                                            insurance: fmtThb(tr.insuranceThb),
                                            tax: fmtThb(tr.vehicleTaxThb),
                                            items: fmtThb(tr.fixedItemsThb),
                                        })}
                                    </span>
                                </TableCell>
                                <TableCell className={`text-right tabular-nums font-semibold ${tr.afterFixedThb < 0 ? "text-red-600 dark:text-red-400" : ""}`}>
                                    {fmtThb(tr.afterFixedThb)}
                                </TableCell>
                            </TableRow>
                        ))
                    )}
                </TableBody>
            </Table>
        </div>
    );
}
