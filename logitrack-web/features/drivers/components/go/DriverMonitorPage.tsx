"use client";

/**
 * The driver monitor over Go (T34, P2; developer-spec.md §10.9 W8, §10.13; Appendix E §E.2.1 row driver-monitor,
 * §E.5 rows 24-27; TW6 and TW8 M2).
 *
 * One aggregate request per page of 200 trips (`['trips','monitor',{from,to,filters}]` over `GET /v1/trips/monitor`):
 * the server joins the task, the driver, the incidents and the places, filters in SQL and counts the options of the
 * range (`meta.facets`), so the page opens no listener, reads no rate card and prices nothing (the stored price
 * shows in the trip detail and the export for `accounting:view_income`; the table has no revenue column, as the
 * owner's July layout). The rows are a `VirtualTable` (developer-spec.md §10.10 W9: "monitor trips, income | 200 |
 * Table + Virtual"): only the rows in view are in the DOM however many pages are loaded, and scrolling near the end
 * loads the next page. SSE `trip.*`, `standby.*`, `incident.created` and `task.*` refresh it through the 2 s window.
 * The export pages through the same route with its cursor and loads xlsx on the click.
 */
import { useId, useMemo, useState } from "react";
import dynamic from "next/dynamic";
import { keepPreviousData, useInfiniteQuery } from "@tanstack/react-query";
import { createColumnHelper } from "@tanstack/react-table";
import { differenceInCalendarDays, endOfMonth, format, parseISO, startOfMonth, subDays } from "date-fns";
import { enUS, th as thLocale } from "date-fns/locale";
import { AlertTriangle, ChevronDown, Download, Loader2, RefreshCw, Search, SlidersHorizontal, X } from "lucide-react";

import { useLanguage } from "@/context/language";
import { PageHeader } from "@/components/page-header";
import { PlaceFilterCombobox } from "@/components/place-filter-combobox";
import { PlateFilterCombobox } from "@/components/plate-filter-combobox";
import { ApiErrorAlert, formatDateTime, SearchPicker, useDebounced, type PickValue } from "@/features/accounting/components/go/shared";
import { cn } from "@/lib/utils";
import { VirtualTable } from "@/components/virtual-table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { DateRangePicker } from "@/components/ui/date-range-picker";
import { Label } from "@/components/ui/label";
import { useIsMobile } from "@/hooks/use-mobile";
import { useMe } from "@/features/auth/api/useMe";
import { navigationProfileOf, profileRouteLabelKey } from "@/features/auth/utils/navigationProfile";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

import { CapabilityGate, LoadMoreFooter } from "@/features/tasks/components/go/opsShared";
import { pageItems } from "@/features/tasks/api/tasks.go";
import {
    activeMonitorFilters,
    deliveredTimeOf,
    emptyMonitorFilter,
    FILTER_NONE,
    MONITOR_ALL,
    MONITOR_DEFAULT_DAYS,
    MONITOR_MAX_DAYS,
    monitorFacets,
    monitorQuery,
    placeLabel,
    TRIP_STATUSES,
    type MonitorFilter,
    type MonitorRowDTO,
} from "../../api/monitor.go";
import { MonitorTripSummary } from "./MonitorTripSummary";
import { JOB_TYPE_COLOR, STATUS_COLOR } from "./monitorStyles";

const TripDetailDialog = dynamic(() => import("./TripDetailDialog").then((m) => m.TripDetailDialog), { ssr: false });
const MonitorExportDialog = dynamic(() => import("./MonitorExportDialog").then((m) => m.MonitorExportDialog), { ssr: false });

const dayKey = (d: Date) => format(d, "yyyy-MM-dd");
const col = createColumnHelper<MonitorRowDTO>();
const HEAD = "text-sm font-medium text-foreground";

/** The monitor's maximum table height, px (the page scrolls the table, not the window). */
export const MONITOR_TABLE_MAX_HEIGHT = 640;

function defaultRange(): { from: string; to: string } {
    const now = new Date();
    return { from: dayKey(subDays(now, MONITOR_DEFAULT_DAYS - 1)), to: dayKey(now) };
}

export function DriverMonitorPage() {
    return (
        <CapabilityGate capability="operations:view_driver_monitor" anyOf={["dispatch:view_operations"]}>
            <Monitor />
        </CapabilityGate>
    );
}

function Monitor() {
    const { t, language } = useLanguage();
    const { data: me } = useMe();
    const profile = navigationProfileOf(me);
    const compact = useIsMobile();
    const title = t(profileRouteLabelKey("/app/driver-monitor", profile) ?? "nav.driverMonitor");
    const id = useId();
    const [summaryOpen, setSummaryOpen] = useState(false);
    const [filtersOpen, setFiltersOpen] = useState(false);
    const dateLocale = language === "th" ? thLocale : enUS;
    const [filter, setFilter] = useState<MonitorFilter>(() => {
        const r = defaultRange();
        return emptyMonitorFilter(r.from, r.to);
    });
    const [driver, setDriver] = useState<PickValue | null>(null);
    const [search, setSearch] = useState("");
    const q = useDebounced(search);
    const [rangeNote, setRangeNote] = useState(false);
    const [detail, setDetail] = useState<MonitorRowDTO | null>(null);
    const [exportOpen, setExportOpen] = useState(false);

    const effective: MonitorFilter = { ...filter, q, driverId: driver?.id ?? "" };
    const list = useInfiniteQuery({ ...monitorQuery(effective), placeholderData: keepPreviousData });
    const rows = useMemo(() => pageItems(list.data?.pages), [list.data]);
    const facets = monitorFacets(list.data?.pages);
    const set = <K extends keyof MonitorFilter>(k: K, v: MonitorFilter[K]) => setFilter((prev) => ({ ...prev, [k]: v }));
    const active = activeMonitorFilters(effective);

    const setRange = (from: Date | undefined, to: Date | undefined) => {
        if (!from || !to) return;
        const span = differenceInCalendarDays(to, from) + 1;
        const clamped = span > MONITOR_MAX_DAYS ? subDays(to, MONITOR_MAX_DAYS - 1) : from;
        setRangeNote(span > MONITOR_MAX_DAYS);
        setFilter((prev) => ({ ...prev, from: dayKey(clamped), to: dayKey(to) }));
    };
    const clearAll = () => {
        setFilter((prev) => emptyMonitorFilter(prev.from, prev.to));
        setDriver(null);
        setSearch("");
    };
    const jobTypeLabel = (j: string) => (j === "line_haul" ? t("driverMonitor.jobType.lineHaul") : t("driverMonitor.jobType.firstMile"));
    const count = (s: string) => facets?.statusCounts?.[s] ?? 0;
    // The export's up-front count: the facets count the range only (not the other filters), so it is the file's
    // size only with no filter on; with filters it is the loaded rows once the last page is in, else unknown (the
    // export then reports the streamed count).
    const exportTotal = active === 0 ? (facets?.total ?? null) : !list.hasNextPage && !list.isPending ? rows.length : null;

    const columns = useMemo(
        () => compact ? [
            col.display({
                id: "summary",
                header: () => title,
                cell: ({ row: { original } }) => <MonitorTripSummary row={original} onOpen={setDetail} />,
                meta: { headerClassName: HEAD, cellClassName: "whitespace-normal p-3 align-top" },
            }),
        ] : [
            col.accessor("tripNo", { header: () => t("driverMonitor.table.tripId"), cell: (c) => <span className="font-mono text-xs">{c.getValue()}</span>, meta: { headerClassName: HEAD } }),
            col.accessor("checkInAt", { header: () => t("driverMonitor.table.checkInAt"), cell: (c) => formatDateTime(c.getValue()), meta: { headerClassName: HEAD, cellClassName: "text-sm text-muted-foreground" } }),
            col.accessor("createdAt", { header: () => t("driverMonitor.table.depart"), cell: (c) => formatDateTime(c.getValue()), meta: { headerClassName: HEAD, cellClassName: "text-sm text-muted-foreground" } }),
            col.accessor("driverName", {
                header: () => t("driverMonitor.table.driver"),
                cell: (c) => <span className="text-sm font-medium">{c.getValue() ?? t("driverMonitor.table.unknown")}</span>,
                meta: { headerClassName: HEAD },
            }),
            col.display({
                id: "plate",
                header: () => t("driverMonitor.table.licensePlate"),
                cell: ({ row: { original: r } }) => <span className="font-mono text-sm text-muted-foreground">{r.truck?.plate ?? "—"}</span>,
                meta: { headerClassName: HEAD },
            }),
            col.accessor("jobType", {
                header: () => t("driverMonitor.table.jobType"),
                cell: (c) => (
                    <Badge variant="secondary" className={cn("border font-medium", JOB_TYPE_COLOR[c.getValue()] ?? "bg-muted text-foreground border-border")}>
                        {jobTypeLabel(c.getValue())}
                    </Badge>
                ),
                meta: { headerClassName: HEAD },
            }),
            col.display({
                id: "origin",
                header: () => t("driverMonitor.table.origin"),
                cell: ({ row: { original: r } }) => placeLabel(r.origin),
                meta: { headerClassName: HEAD, cellClassName: "text-sm font-medium" },
            }),
            col.display({
                id: "destination",
                header: () => t("driverMonitor.table.destination"),
                cell: ({ row: { original: r } }) =>
                    r.stops?.length > 0 ? (
                        <Collapsible>
                            <CollapsibleTrigger onClick={(e) => e.stopPropagation()}
                                className="inline-flex items-center gap-1 rounded-md border border-border bg-muted px-2 py-1 text-xs font-medium text-foreground hover:bg-accent">
                                {t("taskBoard.stops", { count: r.stops.length })}
                                <ChevronDown className="h-3 w-3" />
                            </CollapsibleTrigger>
                            <CollapsibleContent className="mt-2 w-max space-y-1">
                                {r.stops.map((s) => (
                                    <Badge key={s.index} variant="outline"
                                        className={cn("block text-xs font-medium", s.status === "delivered" ? "border-success/25 bg-success/10 text-success" : "border-border bg-muted text-muted-foreground")}>
                                        {placeLabel(s.place)}{s.status === "delivered" ? ` · ${t("driverMonitor.status.delivered")}` : ""}
                                    </Badge>
                                ))}
                            </CollapsibleContent>
                        </Collapsible>
                    ) : (
                        <span className="font-medium">{placeLabel(r.destination)}</span>
                    ),
                meta: { headerClassName: HEAD, cellClassName: "text-sm" },
            }),
            col.accessor("sealCode", { header: () => t("driverMonitor.table.sealCode"), cell: (c) => <span className="font-mono text-xs">{c.getValue() || "—"}</span>, meta: { headerClassName: HEAD } }),
            col.accessor("partnerCode", { header: () => t("driverMonitor.table.partnerCode"), cell: (c) => <span className="font-mono text-xs">{c.getValue() || "—"}</span>, meta: { headerClassName: HEAD } }),
            col.display({
                id: "status",
                header: () => t("driverMonitor.table.status"),
                cell: ({ row: { original: r } }) => (
                    <div className="flex items-center gap-1">
                        <Badge variant="outline" className={cn("font-medium", STATUS_COLOR[r.status])}>{t(`driverMonitor.status.${r.status}`)}</Badge>
                        {r.incidentCount > 0 && r.status !== "incident" ? <AlertTriangle className="h-4 w-4 text-destructive" aria-label={t("driverMonitor.status.incident")} /> : null}
                    </div>
                ),
                meta: { headerClassName: HEAD },
            }),
            col.display({
                id: "deliveredTime",
                header: () => t("driverMonitor.table.deliveredTime"),
                cell: ({ row: { original: r } }) => formatDateTime(deliveredTimeOf(r)),
                meta: { headerClassName: HEAD, cellClassName: "text-sm text-muted-foreground" },
            }),
        ],
        // eslint-disable-next-line react-hooks/exhaustive-deps -- the labels follow the language only
        [compact, t, title]
    );

    const dateLabelId = `${id}-range-label`;
    const dateButtonId = `${id}-range`;
    const dateValueId = `${dateButtonId}-value`;
    const rangeHint = (
        <p className={cn("max-w-xl text-xs", rangeNote ? "text-warning" : "text-muted-foreground")}>
            {t("driverMonitor.filter.maxRangeHint", { days: MONITOR_MAX_DAYS })}
        </p>
    );
    const dateControl = (
        <div className={cn("flex gap-2", compact ? "flex-col" : "flex-wrap items-center")}>
            <Label id={dateLabelId} htmlFor={dateButtonId}>{t("driverMonitor.filter.dateRange")}</Label>
            <DateRangePicker id={dateButtonId} aria-labelledby={`${dateLabelId} ${dateValueId}`} from={parseISO(filter.from)} to={parseISO(filter.to)} onChange={setRange} locale={dateLocale}
                className={compact ? "h-11 w-full" : "w-[230px]"} numberOfMonths={compact ? 1 : 2}
                calendarClassName={compact ? "p-2 [--cell-size:--spacing(11)]" : undefined}
                calendarClassNames={compact ? { month: "flex w-full flex-col gap-2", week: "mt-0 flex w-full" } : undefined} />
        </div>
    );
    const statusControl = (
        <Select value={filter.status} onValueChange={(v) => set("status", v)}>
            <SelectTrigger id={`${id}-status`} aria-label={t("driverMonitor.filter.status")} className={compact ? "h-11 w-full min-w-0" : "h-9 w-[180px]"}>
                <SelectValue placeholder={t("driverMonitor.filter.allStatuses")} />
            </SelectTrigger>
            <SelectContent className={compact ? "[&_[role=option]]:min-h-11" : undefined}>
                <SelectItem value={MONITOR_ALL}>{t("driverMonitor.filter.allStatuses")}</SelectItem>
                {TRIP_STATUSES.map((s) => (
                    <SelectItem key={s} value={s}>
                        {t(`driverMonitor.status.${s}`)}{facets ? ` (${s === "incident" ? facets.incident : count(s)})` : ""}
                    </SelectItem>
                ))}
            </SelectContent>
        </Select>
    );
    const actions = (
        <>
            <Button variant="outline" size={compact ? "icon" : "sm"} className={compact ? "size-11 shrink-0" : undefined}
                onClick={() => setExportOpen(true)} aria-label={t("driverMonitor.export.open")}>
                <Download className={cn("h-4 w-4", !compact && "mr-2")} aria-hidden />
                {!compact ? t("driverMonitor.export.open") : null}
            </Button>
            <Button variant="outline" size="icon" className={compact ? "size-11 shrink-0" : undefined}
                onClick={() => void list.refetch()} aria-label={t("driverMonitor.refresh")}>
                <RefreshCw className={cn("h-4 w-4", list.isFetching && "animate-spin")} aria-hidden />
            </Button>
        </>
    );

    return (
        <div className={cn("flex min-w-0 flex-col", compact ? "gap-3" : "gap-section")}>
            <PageHeader
                title={title}
                description={compact ? undefined : t(profile === "staff" ? "driverMonitor.subtitle" : "driverMonitor.scopedSubtitle")}
                actions={compact ? undefined : actions}
            />

            {compact ? (
                <div className="space-y-3">
                    {dateControl}
                    {rangeHint}
                    <div className="space-y-2">
                        <Label htmlFor={`${id}-status`}>{t("driverMonitor.filter.status")}</Label>
                        <div className="flex min-w-0 items-center gap-2">
                            <div className="min-w-0 flex-1">{statusControl}</div>
                            <Button variant="outline" size="icon" className="size-11 shrink-0" aria-label={t("driverMonitor.filtersActive", { count: active })}
                                aria-expanded={filtersOpen} aria-controls={`${id}-filters`} onClick={() => setFiltersOpen((open) => !open)}>
                                <SlidersHorizontal className="size-4" aria-hidden />
                            </Button>
                            {actions}
                        </div>
                    </div>
                </div>
            ) : null}

            <Collapsible open={!compact || summaryOpen} onOpenChange={setSummaryOpen}>
                {compact ? (
                    <CollapsibleTrigger className="flex min-h-11 w-full items-center justify-between gap-2 rounded-md border px-3 text-sm font-medium">
                        {t("driverMonitor.summary")}
                        <ChevronDown className={cn("size-4 shrink-0", summaryOpen && "rotate-180")} aria-hidden />
                    </CollapsibleTrigger>
                ) : null}
                <CollapsibleContent>
                    <div className={cn("grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4", compact && "mt-3 grid-cols-2 gap-3")}>
                        <Stat label={t("driverMonitor.stats.totalTrips")} value={facets?.total} />
                        <Stat label={t("driverMonitor.stats.inTransit")} value={facets ? count("in_transit") : undefined} />
                        <Stat label={t("driverMonitor.stats.delivered")} value={facets ? count("delivered") : undefined} />
                        <Stat label={t("driverMonitor.status.incident")} value={facets?.incident} />
                    </div>
                </CollapsibleContent>
            </Collapsible>

            <Collapsible open={!compact || filtersOpen} onOpenChange={setFiltersOpen}>
                {compact ? (
                    <CollapsibleTrigger aria-controls={`${id}-filters`} className="flex min-h-11 w-full items-center justify-between gap-2 rounded-md border px-3 text-sm font-medium">
                        <span>{t("driverMonitor.moreFilters")}</span>
                        <span className="flex items-center gap-2">
                            {active > 0 ? <Badge variant="secondary">{t("driverMonitor.filtersActive", { count: active })}</Badge> : null}
                            <ChevronDown className={cn("size-4 shrink-0", filtersOpen && "rotate-180")} aria-hidden />
                        </span>
                    </CollapsibleTrigger>
                ) : null}
                <CollapsibleContent id={`${id}-filters`}>
                    <div className={cn("flex flex-col flex-wrap gap-4 rounded-lg border bg-card p-4 sm:flex-row", compact && "mt-3 [&_button]:min-h-11 [&_button]:min-w-11 [&_input]:h-11 [&_button[role=combobox]]:w-full [&>div]:max-w-full")}>
                        <div className="flex gap-2 border-b border-border/50 pb-4 sm:border-b-0 sm:border-r sm:pb-0 sm:pr-4">
                            {[MONITOR_ALL, "first_mile", "line_haul"].map((j) => (
                                <Button key={j} variant={filter.jobType === j ? "secondary" : "ghost"} size="sm" className="whitespace-nowrap" onClick={() => set("jobType", j)}>
                                    {j === MONITOR_ALL ? t("driverMonitor.jobType.all") : jobTypeLabel(j)}
                                </Button>
                            ))}
                        </div>
                        <div className="flex flex-col gap-2">
                            <div className="flex flex-wrap items-center gap-2">
                                {!compact ? dateControl : null}
                                <Button type="button" variant="secondary" size="sm" onClick={() => setFilter((prev) => ({ ...prev, ...defaultRange() }))}>
                                    {t("driverMonitor.filter.presetLastDays", { days: MONITOR_DEFAULT_DAYS })}
                                </Button>
                                <Button type="button" variant="outline" size="sm"
                                    onClick={() => setFilter((prev) => ({ ...prev, from: dayKey(startOfMonth(new Date())), to: dayKey(endOfMonth(new Date())) }))}>
                                    {t("driverMonitor.filter.presetThisMonth")}
                                </Button>
                            </div>
                            {!compact ? rangeHint : null}
                        </div>
                        <SearchPicker kind="driver" value={driver} onChange={setDriver} placeholder={t("driverMonitor.filter.allDrivers")}
                            clearLabel={t("driverMonitor.filter.allDrivers")} className="h-9 w-[200px]" />
                        {!compact ? statusControl : null}
                        <Select value={filter.partner} onValueChange={(v) => set("partner", v)}>
                            <SelectTrigger aria-label={t("driverMonitor.filter.partnerChannel")} className="h-9 w-[200px]">
                                <SelectValue placeholder={t("driverMonitor.filter.partnerChannel")} />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value={MONITOR_ALL}>{t("driverMonitor.filter.allPartnerChannels")}</SelectItem>
                                {(facets?.partners ?? []).map((o) => (
                                    <SelectItem key={o.value} value={o.value}>
                                        {o.value === FILTER_NONE ? t("driverMonitor.filter.partnerNotSpecified") : o.label} ({o.count})
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                        <PlateFilterCombobox options={facets?.plates ?? []} value={filter.plate} onChange={(v) => set("plate", v)} keyPrefix="driverMonitor.filter" className="w-[200px]" />
                        <Select value={filter.vehicleClass} onValueChange={(v) => set("vehicleClass", v)}>
                            <SelectTrigger aria-label={t("driverMonitor.filter.vehicleClass")} className="h-9 w-[200px]">
                                <SelectValue placeholder={t("driverMonitor.filter.vehicleClass")} />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value={MONITOR_ALL}>{t("driverMonitor.filter.allVehicleClasses")}</SelectItem>
                                {(facets?.vehicleClasses ?? []).map((o) => (
                                    <SelectItem key={o.value} value={o.value}>
                                        {o.value === FILTER_NONE ? t("driverMonitor.filter.vehicleClassNotSpecified") : o.label} ({o.count})
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                        <PlaceFilterCombobox kind="origin" options={facets?.origins ?? []} value={filter.origin} onChange={(v) => set("origin", v)} keyPrefix="driverMonitor.filter" className="w-[200px]" />
                        <PlaceFilterCombobox kind="destination" options={facets?.destinations ?? []} value={filter.destination} onChange={(v) => set("destination", v)}
                            keyPrefix="driverMonitor.filter" className="w-[200px]" />
                        <div className="relative min-w-[200px] flex-1">
                            <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
                            <Input aria-label={t("driverMonitor.filter.searchLabel")} placeholder={t("driverMonitor.filter.searchDriver")} className="h-9 border-border/50 bg-background/50 pl-10" value={search}
                                onChange={(e) => setSearch(e.target.value)} />
                        </div>
                        {active > 0 ? (
                            <Button variant="ghost" size="sm" onClick={clearAll} className="h-9 whitespace-nowrap text-muted-foreground">
                                <X className="mr-1.5 h-4 w-4" />
                                {t("driverMonitor.filter.clearAll", { count: active })}
                            </Button>
                        ) : null}
                    </div>
                </CollapsibleContent>
            </Collapsible>

            <ApiErrorAlert error={list.error} />

            <div className={cn("overflow-hidden rounded-lg border bg-card", compact && "[&_button]:min-h-11 [&_button]:min-w-11")}>
                <VirtualTable
                    data={rows}
                    columns={columns}
                    getRowId={(r) => r.id}
                    aria-label={title}
                    infinite={list}
                    loading={list.isPending}
                    loadingState={
                        <span className="flex flex-col items-center justify-center gap-2 py-8">
                            <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
                            <span className="text-sm text-muted-foreground">{t("driverMonitor.table.loadingData")}</span>
                        </span>
                    }
                    error={Boolean(list.error)}
                    errorState={<Button variant="outline" className={compact ? "min-h-11" : undefined} onClick={() => void list.refetch()}>{t("shell.retry")}</Button>}
                    emptyState={<span className="block py-8 text-sm">{t("driverMonitor.table.noTrips")}</span>}
                    resetScrollKey={JSON.stringify(effective)}
                    onRowClick={compact ? undefined : setDetail}
                    estimateRowHeight={compact ? 260 : undefined}
                    className={compact ? "[&_table]:table-fixed" : undefined}
                    maxHeight={MONITOR_TABLE_MAX_HEIGHT}
                    headerRowClassName="border-b border-border/50 bg-muted/50"
                    rowClassName="border-b border-border/50 transition-colors hover:bg-muted/50"
                />
                <LoadMoreFooter loaded={rows.length} hasNextPage={Boolean(list.hasNextPage)} isFetchingNextPage={list.isFetchingNextPage}
                    onLoadMore={() => void list.fetchNextPage()} />
            </div>

            {detail ? <TripDetailDialog row={detail} onClose={() => setDetail(null)} /> : null}
            {exportOpen ? <MonitorExportDialog filter={effective} total={exportTotal} onClose={() => setExportOpen(false)} /> : null}
        </div>
    );
}

function Stat({ label, value }: { label: string; value: number | undefined }) {
    return (
        <div className="rounded-xl border bg-card p-4">
            <p className="text-sm font-medium text-muted-foreground">{label}</p>
            <p className="mt-1 text-3xl font-semibold tabular-nums">{value ?? "—"}</p>
        </div>
    );
}
