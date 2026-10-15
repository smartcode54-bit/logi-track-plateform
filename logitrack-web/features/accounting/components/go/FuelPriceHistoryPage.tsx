"use client";

/**
 * Fuel price history over Go (T42, P3; Appendix E §E.2.3 row "fuel-price-history"): the Bangchak daily
 * snapshots since a date, `['fuel','daily',{from}]` over `GET /v1/fuel/daily?from` (paged; the
 * Firestore page stopped at 400 days, Appendix E §E.10). The fuel-type filter stays in the browser over
 * each day's items (one snapshot per day). Text through i18n (the Firestore page was Thai only).
 */
import { useMemo, useState } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { format, subDays } from "date-fns";
import { Loader2, Minus, RefreshCw, TrendingDown, TrendingUp } from "lucide-react";

import { useLanguage } from "@/context/language";
import { PageHeader } from "@/components/page-header";
import { LoadMore } from "@/components/load-more";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

import { fuelDailyRangeQuery, fuelHistoryTable, fuelSnapshotRows, isDieselName, type FuelTypeFilter } from "../../api/fuel.go";
import { ApiErrorAlert, CenteredSpinner, formatDay } from "./shared";

const RANGES = ["30", "60", "90", "180", "365"] as const;

function PriceDiff({ diff }: { diff: number | null }) {
    if (diff === null) return <span className="text-xs text-muted-foreground">—</span>;
    const Icon = diff > 0 ? TrendingUp : diff < 0 ? TrendingDown : Minus;
    const color = diff > 0 ? "text-destructive" : diff < 0 ? "text-success" : "text-muted-foreground";
    return (
        <span className={`flex items-center justify-end gap-0.5 font-mono text-xs ${color}`}>
            <Icon className="h-3 w-3" />
            {diff > 0 ? "+" : ""}
            {diff.toFixed(2)}
        </span>
    );
}

export function FuelPriceHistoryPage() {
    const { t } = useLanguage();
    const [range, setRange] = useState<(typeof RANGES)[number]>("90");
    const [fuelType, setFuelType] = useState<FuelTypeFilter>("diesel");
    const from = useMemo(() => format(subDays(new Date(), Number(range)), "yyyy-MM-dd"), [range]);
    const days = useInfiniteQuery(fuelDailyRangeQuery(from));
    const rows = fuelSnapshotRows(days.data?.pages);
    const table = useMemo(() => fuelHistoryTable(rows, fuelType), [rows, fuelType]);
    const latest = rows[0];
    const previous = rows[1];
    const dieselNow = latest?.items.filter((i) => isDieselName(i.nameTh)) ?? [];

    return (
        <div className="flex flex-col gap-section">
            <PageHeader
                title={t("nav.fuelPriceHistory")}
                description={t("accounting.fuelHistory.subtitle")}
                actions={
                    <Button variant="outline" size="sm" onClick={() => void days.refetch()} disabled={days.isFetching}>
                        {days.isFetching ? <Loader2 aria-hidden className="h-4 w-4 animate-spin" /> : <RefreshCw aria-hidden className="h-4 w-4" />}
                        <span className="ml-1">{t("common.refresh")}</span>
                    </Button>
                }
            />

            {dieselNow.length > 0 ? (
                <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
                    {dieselNow.map((item) => {
                        const prev = previous?.items.find((i) => i.nameTh === item.nameTh);
                        const diff = prev != null ? Math.round((item.price - prev.price) * 100) / 100 : null;
                        return (
                            <Card key={item.nameTh}>
                                <CardContent className="pb-3 pt-3">
                                    <p className="truncate text-xs text-muted-foreground">{item.nameTh}</p>
                                    <p className="font-mono text-xl font-semibold tabular-nums">{item.price.toFixed(2)}</p>
                                    <div className="mt-0.5 flex items-center justify-between">
                                        <span className="text-xs text-muted-foreground">{item.unit}</span>
                                        <PriceDiff diff={diff} />
                                    </div>
                                </CardContent>
                            </Card>
                        );
                    })}
                </div>
            ) : null}

            <div className="flex flex-wrap gap-3">
                <Select value={range} onValueChange={(v) => setRange(v as (typeof RANGES)[number])}>
                    <SelectTrigger className="w-[180px]" aria-label={t("accounting.fuelHistory.range")}>
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                        {RANGES.map((r) => (
                            <SelectItem key={r} value={r}>
                                {t("accounting.fuelHistory.lastDays", { days: r })}
                            </SelectItem>
                        ))}
                    </SelectContent>
                </Select>
                <Select value={fuelType} onValueChange={(v) => setFuelType(v as FuelTypeFilter)}>
                    <SelectTrigger className="w-[220px]" aria-label={t("accounting.fuelHistory.fuelType")}>
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="all">{t("accounting.fuelHistory.type.all")}</SelectItem>
                        <SelectItem value="diesel">{t("accounting.fuelHistory.type.diesel")}</SelectItem>
                        <SelectItem value="gasohol">{t("accounting.fuelHistory.type.gasohol")}</SelectItem>
                    </SelectContent>
                </Select>
                <span className="self-center text-sm text-muted-foreground">{t("accounting.fuelHistory.dayCount", { count: rows.length })}</span>
            </div>

            <ApiErrorAlert error={days.error} />

            <Card>
                <CardHeader className="pb-2">
                    <CardTitle className="text-base">{t("accounting.fuelHistory.tableTitle")}</CardTitle>
                </CardHeader>
                <CardContent className="overflow-x-auto p-0">
                    {days.isPending ? (
                        <CenteredSpinner />
                    ) : rows.length === 0 ? (
                        <div className="py-12 text-center text-sm text-muted-foreground">{t("accounting.fuelHistory.empty")}</div>
                    ) : (
                        <Table>
                            <TableHeader>
                                <TableRow>
                                    <TableHead className="sticky left-0 z-10 min-w-[110px] bg-background">{t("accounting.fuelHistory.date")}</TableHead>
                                    {table.names.map((name) => (
                                        <TableHead key={name} className="min-w-[110px] text-right text-xs">
                                            {name}
                                        </TableHead>
                                    ))}
                                </TableRow>
                            </TableHeader>
                            <TableBody>
                                {table.rows.map(({ day, prices, diff }) => (
                                    <TableRow key={day.dayKey ?? day.capturedAt}>
                                        <TableCell className="sticky left-0 z-10 bg-background font-mono text-xs">
                                            <div>{formatDay(day.dayKey ?? null)}</div>
                                            <div className="text-xs text-muted-foreground">{formatDay(day.capturedAt, "HH:mm")}</div>
                                            {day.status === "error" ? <div className="text-xs text-destructive">{day.errorMessage ?? "error"}</div> : null}
                                        </TableCell>
                                        {table.names.map((name) => {
                                            const price = prices.get(name);
                                            return (
                                                <TableCell key={name} className="text-right">
                                                    {price != null ? (
                                                        <div>
                                                            <span className="font-mono text-sm">{price.toFixed(2)}</span>
                                                            <PriceDiff diff={diff(name)} />
                                                        </div>
                                                    ) : (
                                                        <span className="text-muted-foreground">—</span>
                                                    )}
                                                </TableCell>
                                            );
                                        })}
                                    </TableRow>
                                ))}
                            </TableBody>
                        </Table>
                    )}
                </CardContent>
            </Card>
            <LoadMore
                loaded={rows.length}
                hasNextPage={days.hasNextPage}
                isFetchingNextPage={days.isFetchingNextPage}
                error={days.isFetchNextPageError}
                onLoadMore={() => void days.fetchNextPage()}
            />
        </div>
    );
}
