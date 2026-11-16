"use client";

import { useMemo, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useLanguage } from "@/context/language";
import type { PnlResult, PnlTripRow } from "@/lib/pnlCompute";
import { fmtNum, fmtPct, fmtThb, routeLabel } from "../utils/format";

const PAGE_SIZE = 50;

/** Per-trip view (R18): every row of the month, counted or not, with its badges. */
export function PnlTripTable({ result, hubLabels }: { result: PnlResult; hubLabels: Record<string, string> }) {
    const { t } = useLanguage();
    const [truck, setTruck] = useState("all");
    const [customer, setCustomer] = useState("all");
    const [lane, setLane] = useState("all");
    const [status, setStatus] = useState<"all" | "counted" | "excluded">("all");
    const [page, setPage] = useState(0);

    const trucks = useMemo(() => uniq(result.trips.map((r) => r.truckPlate ?? "")), [result.trips]);
    const customers = useMemo(
        () => [...new Map(result.trips.filter((r) => r.customerId).map((r) => [r.customerId!, r.customerName ?? r.customerId!])).entries()],
        [result.trips],
    );
    const lanes = useMemo(() => uniq(result.trips.map((r) => r.laneKey)), [result.trips]);

    const filtered = useMemo(
        () =>
            result.trips.filter(
                (r) =>
                    (truck === "all" || (r.truckPlate ?? "") === truck) &&
                    (customer === "all" || r.customerId === customer) &&
                    (lane === "all" || r.laneKey === lane) &&
                    (status === "all" || (status === "counted") === r.counted),
            ),
        [result.trips, truck, customer, lane, status],
    );
    const pages = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE));
    const current = Math.min(page, pages - 1);
    const rows = filtered.slice(current * PAGE_SIZE, (current + 1) * PAGE_SIZE);
    const reset = <T,>(set: (v: T) => void) => (v: T) => {
        set(v);
        setPage(0);
    };
    const laneLabel = (key: string) => {
        const r = result.trips.find((x) => x.laneKey === key);
        if (!r) return key;
        return r.kind === "standby" ? `${t("pnl.kind.standby")} · ${r.customerName ?? ""}` : routeLabel(r.originCode, r.destinationCodes, hubLabels);
    };

    return (
        <div className="space-y-3">
            <div className="flex flex-wrap gap-2">
                <Filter value={truck} onChange={reset(setTruck)} all={t("pnl.filter.allTrucks")} options={trucks.map((p) => [p, p || "—"])} />
                <Filter value={customer} onChange={reset(setCustomer)} all={t("pnl.filter.allCustomers")} options={customers} />
                <Filter value={lane} onChange={reset(setLane)} all={t("pnl.filter.allLanes")} options={lanes.map((k) => [k, laneLabel(k)])} wide />
                <Filter
                    value={status}
                    onChange={reset((v: string) => setStatus(v as typeof status))}
                    all={t("pnl.filter.allStatuses")}
                    options={[["counted", t("pnl.filter.counted")], ["excluded", t("pnl.filter.excluded")]]}
                />
            </div>
            <div className="overflow-x-auto rounded-md border">
                <Table>
                    <TableHeader>
                        <TableRow>
                            <TableHead>{t("pnl.col.date")}</TableHead>
                            <TableHead>{t("pnl.col.truck")}</TableHead>
                            <TableHead>{t("pnl.col.driver")}</TableHead>
                            <TableHead>{t("pnl.col.customer")}</TableHead>
                            <TableHead>{t("pnl.col.route")}</TableHead>
                            <TableHead>{t("pnl.col.jobCategory")}</TableHead>
                            <TableHead className="text-right">{t("pnl.col.jobKm")}</TableHead>
                            <TableHead className="text-right">{t("pnl.col.revenue")}</TableHead>
                            <TableHead className="text-right">{t("pnl.trip.fuel")}</TableHead>
                            <TableHead className="text-right">{t("pnl.trip.driverPay")}</TableHead>
                            <TableHead className="text-right">{t("pnl.trip.helperShare")}</TableHead>
                            <TableHead className="text-right">{t("pnl.trip.maintAccrual")}</TableHead>
                            <TableHead className="text-right">{t("pnl.trip.contribution")}</TableHead>
                            <TableHead className="text-right">{t("pnl.col.margin")}</TableHead>
                            <TableHead>{t("pnl.col.badges")}</TableHead>
                        </TableRow>
                    </TableHeader>
                    <TableBody>
                        {rows.length === 0 ? (
                            <TableRow>
                                <TableCell colSpan={15} className="text-center text-muted-foreground py-8">{t("pnl.empty")}</TableCell>
                            </TableRow>
                        ) : (
                            rows.map((r) => <TripRow key={`${r.kind}:${r.id}`} r={r} hubLabels={hubLabels} />)
                        )}
                    </TableBody>
                </Table>
            </div>
            <div className="flex items-center justify-between text-sm text-muted-foreground">
                <span>{t("pnl.pagination.summary", { count: filtered.length, page: current + 1, pages })}</span>
                <div className="flex gap-2">
                    <Button variant="outline" size="sm" disabled={current === 0} onClick={() => setPage(current - 1)}>{t("pnl.pagination.prev")}</Button>
                    <Button variant="outline" size="sm" disabled={current >= pages - 1} onClick={() => setPage(current + 1)}>{t("pnl.pagination.next")}</Button>
                </div>
            </div>
        </div>
    );
}

function TripRow({ r, hubLabels }: { r: PnlTripRow; hubLabels: Record<string, string> }) {
    const { t } = useLanguage();
    return (
        <TableRow className={r.counted ? "" : "bg-amber-50/60 dark:bg-amber-950/20"}>
            <TableCell className="whitespace-nowrap">{r.dateKey}</TableCell>
            <TableCell className="whitespace-nowrap">{r.truckPlate ?? "—"}</TableCell>
            <TableCell className="whitespace-nowrap">{r.driverName ?? "—"}</TableCell>
            <TableCell className="whitespace-nowrap">{r.customerName ?? "—"}</TableCell>
            <TableCell className="min-w-[200px]">
                {r.kind === "standby" && <Badge variant="secondary" className="mr-1">{t("pnl.kind.standby")}</Badge>}
                {r.isMultiDrop && <Badge variant="outline" className="mr-1">{t("pnl.kind.multiDrop")}</Badge>}
                {routeLabel(r.originCode, r.destinationCodes, hubLabels)}
            </TableCell>
            <TableCell>{r.jobCategory ? t(`pnl.jobCategory.${r.jobCategory}`) : "—"}</TableCell>
            <TableCell className="text-right tabular-nums">{fmtNum(r.jobKm)}</TableCell>
            <TableCell className="text-right tabular-nums">{fmtThb(r.revenueThb)}</TableCell>
            <TableCell className="text-right tabular-nums">{fmtThb(r.fuelThb)}</TableCell>
            <TableCell className="text-right tabular-nums">{fmtThb(r.driverPayThb)}</TableCell>
            <TableCell className="text-right tabular-nums">{fmtThb(r.helperShareThb)}</TableCell>
            <TableCell className="text-right tabular-nums">{fmtThb(r.maintAccrualThb)}</TableCell>
            <TableCell className={`text-right tabular-nums font-medium ${(r.contributionThb ?? 0) < 0 ? "text-red-600 dark:text-red-400" : ""}`}>
                {fmtThb(r.contributionThb)}
            </TableCell>
            <TableCell className="text-right tabular-nums">{fmtPct(r.marginPct)}</TableCell>
            <TableCell className="min-w-[160px]">
                <div className="flex flex-wrap gap-1">
                    {r.reasons.map((c) => (
                        <Badge key={c} variant="destructive">
                            {t(`pnl.badge.${c}`)}
                            {c === "noJobDistance" && r.missingLeg ? ` (${r.missingLeg.join("→")})` : ""}
                        </Badge>
                    ))}
                    {r.notes.map((c) => (
                        <Badge key={c} variant="outline">{t(`pnl.badge.${c}`)}</Badge>
                    ))}
                </div>
            </TableCell>
        </TableRow>
    );
}

function Filter({
    value, onChange, all, options, wide,
}: {
    value: string;
    onChange: (v: string) => void;
    all: string;
    options: Array<[string, string]>;
    wide?: boolean;
}) {
    return (
        <Select value={value} onValueChange={onChange}>
            <SelectTrigger className={wide ? "w-[280px]" : "w-[180px]"}>
                <SelectValue />
            </SelectTrigger>
            <SelectContent>
                <SelectItem value="all">{all}</SelectItem>
                {options.filter(([v]) => v !== "").map(([v, label]) => (
                    <SelectItem key={v} value={v}>{label}</SelectItem>
                ))}
            </SelectContent>
        </Select>
    );
}

function uniq(xs: string[]): string[] {
    return [...new Set(xs)].sort();
}
