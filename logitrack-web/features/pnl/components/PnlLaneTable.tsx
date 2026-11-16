"use client";

import { Badge } from "@/components/ui/badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useLanguage } from "@/context/language";
import type { PnlResult } from "@/lib/pnlCompute";
import { fmtNum, fmtPct, fmtThb, routeLabel } from "../utils/format";

/** Per-lane view (R18): counted rows only, sorted by average contribution per trip. */
export function PnlLaneTable({ result, hubLabels }: { result: PnlResult; hubLabels: Record<string, string> }) {
    const { t } = useLanguage();
    return (
        <div className="overflow-x-auto rounded-md border">
            <Table>
                <TableHeader>
                    <TableRow>
                        <TableHead>{t("pnl.col.customer")}</TableHead>
                        <TableHead>{t("pnl.col.route")}</TableHead>
                        <TableHead>{t("pnl.col.vehicleClass")}</TableHead>
                        <TableHead className="text-right">{t("pnl.col.trips")}</TableHead>
                        <TableHead className="text-right">{t("pnl.col.jobKmPerTrip")}</TableHead>
                        <TableHead className="text-right">{t("pnl.col.avgRevenue")}</TableHead>
                        <TableHead className="text-right">{t("pnl.col.avgContribution")}</TableHead>
                        <TableHead className="text-right">{t("pnl.col.totalContribution")}</TableHead>
                        <TableHead className="text-right">{t("pnl.col.margin")}</TableHead>
                    </TableRow>
                </TableHeader>
                <TableBody>
                    {result.lanes.length === 0 ? (
                        <TableRow>
                            <TableCell colSpan={9} className="text-center text-muted-foreground py-8">{t("pnl.empty")}</TableCell>
                        </TableRow>
                    ) : (
                        result.lanes.map((l) => (
                            <TableRow key={l.key}>
                                <TableCell className="whitespace-nowrap">{l.customerName ?? "—"}</TableCell>
                                <TableCell className="min-w-[220px]">
                                    {l.isStandby ? <Badge variant="secondary">{t("pnl.kind.standby")}</Badge> : routeLabel(l.originCode, l.destinationCodes, hubLabels)}
                                </TableCell>
                                <TableCell>{l.vehicleClass ?? "—"}</TableCell>
                                <TableCell className="text-right tabular-nums">
                                    {l.trips}
                                    <span className="block text-xs text-muted-foreground">
                                        {t("pnl.lane.primarySupp", { primary: l.primaryTrips, supp: l.supplementaryTrips })}
                                    </span>
                                </TableCell>
                                <TableCell className="text-right tabular-nums">{fmtNum(l.jobKmPerTrip)}</TableCell>
                                <TableCell className="text-right tabular-nums">{fmtThb(l.avgRevenueThb)}</TableCell>
                                <TableCell className={`text-right tabular-nums font-medium ${l.avgContributionThb < 0 ? "text-red-600 dark:text-red-400" : ""}`}>
                                    {fmtThb(l.avgContributionThb)}
                                </TableCell>
                                <TableCell className="text-right tabular-nums">{fmtThb(l.contributionThb)}</TableCell>
                                <TableCell className="text-right tabular-nums">{fmtPct(l.marginPct)}</TableCell>
                            </TableRow>
                        ))
                    )}
                </TableBody>
            </Table>
        </div>
    );
}
