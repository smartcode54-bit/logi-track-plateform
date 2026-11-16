"use client";

import { useMemo, useState } from "react";
import { Loader2 } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useLanguage } from "@/context/language";
import type { PnlResult } from "@/lib/pnlCompute";
import { yearTotal } from "@/lib/pnl/year";
import { PnlSourceError } from "../api/pnlSources";
import { usePnlYear } from "../hooks/usePnlYear";
import { fixedCategoryLabel, fmtNum, fmtThb } from "../utils/format";

type Kind = "line" | "cost" | "sub" | "total" | "count";
interface YearRow {
    key: string;
    label: string;
    kind: Kind;
    value: (r: PnlResult) => number | null;
    /** Year column: sum (default) or nothing (break-even). */
    noTotal?: boolean;
}

/** Year view (owner 2026-11-16): P&L lines as rows, January → latest month as columns + year total. */
export function PnlYearView() {
    const { t, language } = useLanguage();
    const now = new Date();
    const thisYear = now.getFullYear();
    const [year, setYear] = useState(thisYear);
    const lastMonth = year === thisYear ? now.getMonth() + 1 : 12;
    const { result, loading, error } = usePnlYear(year, lastMonth);

    const monthNames = useMemo(() => {
        const fmt = new Intl.DateTimeFormat(language === "th" ? "th-TH" : "en-GB", { month: "short" });
        return Array.from({ length: 12 }, (_, i) => fmt.format(new Date(2026, i, 15)));
    }, [language]);

    const rows: YearRow[] = useMemo(() => {
        if (!result) return [];
        const cost = (key: string, pick: (r: PnlResult) => number): YearRow => ({ key, label: t(key), kind: "cost", value: (r) => -pick(r) });
        // Fixed-cost lines differ by month (items start/stop) — union them by category key.
        const fixedKeys = new Map<string, string>();
        for (const m of result.months) {
            for (const l of m.statement.fixedItemLines) {
                if (!fixedKeys.has(l.key)) fixedKeys.set(l.key, fixedCategoryLabel(l.category, l.label, t));
            }
        }
        return [
            { key: "revenue", label: t("pnl.year.revenue"), kind: "line", value: (r) => r.statement.revenueThb },
            cost("pnl.line.tripFuel", (r) => r.statement.tripFuelThb),
            cost("pnl.line.driverPay", (r) => r.statement.driverPayThb),
            cost("pnl.line.helperShare", (r) => r.statement.helperShareThb),
            cost("pnl.line.maintAccrual", (r) => r.statement.maintAccrualThb),
            { key: "contribution", label: t("pnl.line.contribution"), kind: "sub", value: (r) => r.statement.contributionThb },
            cost("pnl.line.offPlanFuel", (r) => r.statement.offPlanFuelThb),
            { key: "fuelRecon", label: t("pnl.year.fuelReconciliation"), kind: "cost", value: (r) => -r.statement.fuelReconciliationThb },
            cost("pnl.line.helperNoTrip", (r) => r.statement.helperNoTripThb),
            cost("pnl.line.toll", (r) => r.statement.tollThb),
            cost("pnl.line.otherVehicleExpenses", (r) => r.statement.otherVehicleExpenseThb),
            { key: "maintVariance", label: t("pnl.col.maintVariance"), kind: "cost", value: (r) => -r.statement.maintVarianceThb },
            { key: "afterMonthLines", label: t("pnl.line.afterMonthLines"), kind: "sub", value: (r) => r.statement.afterMonthLinesThb },
            cost("pnl.line.insurance", (r) => r.statement.insuranceThb),
            cost("pnl.line.vehicleTax", (r) => r.statement.vehicleTaxThb),
            ...[...fixedKeys.entries()].map(([key, label]): YearRow => ({
                key: `fixed:${key}`,
                label,
                kind: "cost",
                value: (r) => -(r.statement.fixedItemLines.find((l) => l.key === key)?.amountThb ?? 0),
            })),
            { key: "operatingProfit", label: t("pnl.line.operatingProfit"), kind: "total", value: (r) => r.statement.operatingProfitThb },
            { key: "trips", label: t("pnl.year.countedTrips"), kind: "count", value: (r) => r.statement.countedTrips + r.statement.countedStandbys },
            { key: "breakEven", label: t("pnl.breakEven.title"), kind: "count", value: (r) => r.breakEven.primaryTrips, noTotal: true },
            { key: "excluded", label: t("pnl.year.excluded"), kind: "count", value: (r) => r.statement.excludedCount },
        ];
    }, [result, t]);

    const yearOptions = [thisYear, thisYear - 1, thisYear - 2];
    const cell = (kind: Kind, v: number | null) =>
        v === null ? "—" : kind === "count" ? fmtNum(v, 0) : fmtThb(v);

    return (
        <Card>
            <CardHeader className="flex flex-row items-center justify-between space-y-0 gap-3 flex-wrap">
                <CardTitle className="text-base">{t("pnl.year.title", { year, from: monthNames[0], to: monthNames[lastMonth - 1] })}</CardTitle>
                <Select value={String(year)} onValueChange={(v) => setYear(Number(v))}>
                    <SelectTrigger className="w-[110px]"><SelectValue /></SelectTrigger>
                    <SelectContent>
                        {yearOptions.map((y) => <SelectItem key={y} value={String(y)}>{y}</SelectItem>)}
                    </SelectContent>
                </Select>
            </CardHeader>
            <CardContent>
                {loading ? (
                    <div className="flex justify-center py-12"><Loader2 className="w-8 h-8 animate-spin text-muted-foreground" /></div>
                ) : error ? (
                    <p className="text-sm text-destructive">
                        {error instanceof PnlSourceError
                            ? t(error.settingsRulesMissing ? "pnl.error.settingsRules" : "pnl.error.source", { source: error.source })
                            : t("pnl.error.generic")}
                    </p>
                ) : result ? (
                    <>
                        <p className="text-xs text-muted-foreground mb-3">{t("pnl.year.hint")}</p>
                        <div className="overflow-x-auto rounded-md border">
                            <Table>
                                <TableHeader>
                                    <TableRow>
                                        <TableHead className="sticky left-0 bg-background min-w-[220px]">{t("pnl.html.line")}</TableHead>
                                        {result.months.map((m, i) => (
                                            <TableHead key={m.monthKey} className="text-right whitespace-nowrap">{monthNames[i]}</TableHead>
                                        ))}
                                        <TableHead className="text-right whitespace-nowrap font-semibold">{t("pnl.year.total")}</TableHead>
                                    </TableRow>
                                </TableHeader>
                                <TableBody>
                                    {rows.map((row) => {
                                        const values = result.months.map((m) => row.value(m));
                                        const total = row.noTotal ? null : yearTotal(result.months, (m) => row.value(m) ?? 0);
                                        const strong = row.kind === "sub" || row.kind === "total";
                                        return (
                                            <TableRow key={row.key} className={strong ? "bg-muted/50 font-semibold" : row.kind === "count" ? "text-muted-foreground" : ""}>
                                                <TableCell className={`sticky left-0 bg-background whitespace-nowrap ${row.kind === "cost" ? "pl-8" : ""}`}>{row.label}</TableCell>
                                                {values.map((v, i) => (
                                                    <TableCell key={i} className={`text-right tabular-nums whitespace-nowrap ${v !== null && v < -0.004 ? "text-red-600 dark:text-red-400" : ""}`}>
                                                        {cell(row.kind, v)}
                                                    </TableCell>
                                                ))}
                                                <TableCell className={`text-right tabular-nums whitespace-nowrap font-semibold ${total !== null && total < -0.004 ? "text-red-600 dark:text-red-400" : ""}`}>
                                                    {cell(row.kind, total)}
                                                </TableCell>
                                            </TableRow>
                                        );
                                    })}
                                </TableBody>
                            </Table>
                        </div>
                    </>
                ) : null}
            </CardContent>
        </Card>
    );
}
