"use client";

/**
 * The rate entries tab (T42): `['rateCard','entries',cust]`, server pages of 100 (`GET
 * /v1/billing/rate-entries?customerId&includeVoided`), enabled only while the tab is shown. Source,
 * destination, class and search narrow the loaded pages (the server pages by customer only). A manual row
 * (`POST`), an import (`POST .../import`) and a void (`POST .../{id}/void`) refetch only this customer's
 * key and the `all` view (`rateCardChangedKeys`); the rate card's other tables stay cached.
 */
import { useMemo, useState } from "react";
import dynamic from "next/dynamic";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { createColumnHelper } from "@tanstack/react-table";
import { Ban, Download, Loader2, Plus, Upload } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { apiErrorText } from "@/lib/apiError";
import { LazyDialogLoading } from "@/components/lazy-dialog";
import { LoadMore } from "@/components/load-more";
import { VirtualTable } from "@/components/virtual-table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { SOC_DESTINATIONS, TASK_TRUCK_TYPE_ENUM } from "@/validate/taskSchema";

import { customerLabel, hubLabel, hubPicksQuery, type CustomerPick } from "../../../api/pickers.go";
import {
    ALL_CUSTOMERS,
    createRateEntry,
    filterRateEntries,
    rateEntriesQuery,
    rateEntryRows,
    voidRateEntry,
    type JobCategory,
    type RateEntryDTO,
} from "../../../api/rateCard.go";
import { ApiErrorAlert, CustomerSelect, formatDay, formatThb, todayKey } from "../shared";
import { VoidDialog } from "./VoidDialog";

// xlsx loads with the dialog, on its first open (developer-spec.md §10.11).
const RateImportDialog = dynamic(() => import("./RateImportDialog").then((m) => m.RateImportDialog), { ssr: false, loading: LazyDialogLoading });

const col = createColumnHelper<RateEntryDTO>();
const ALL = "all";

function AddRateDialog({ customers, hubs, initialCustomerId, onClose }: { customers: CustomerPick[]; hubs: { code: string; label: string }[]; initialCustomerId: string; onClose: () => void }) {
    const { t } = useLanguage();
    const [customerId, setCustomerId] = useState(initialCustomerId === ALL_CUSTOMERS ? "" : initialCustomerId);
    const [hubId, setHubId] = useState("");
    const [destination, setDestination] = useState("");
    const [vehicleClass, setVehicleClass] = useState<string>("4WJ");
    const [rate, setRate] = useState("");
    const [distance, setDistance] = useState("");
    const [effectiveFrom, setEffectiveFrom] = useState(todayKey());
    const [category, setCategory] = useState<JobCategory>("PRIMARY");
    const [saving, setSaving] = useState(false);
    const valid = customerId && hubId && destination.trim() && rate.trim() !== "" && Number(rate) >= 0 && effectiveFrom;
    const save = async () => {
        if (!valid) return;
        setSaving(true);
        try {
            const hub = hubs.find((h) => h.code === hubId);
            await createRateEntry({
                customerId,
                effectiveFromDateStr: effectiveFrom,
                hubId,
                rawHubName: hub ? `${hub.code} - ${hub.label}` : hubId,
                destinationCode: destination.trim().toUpperCase(),
                vehicleClass,
                rateThb: Number(rate),
                distanceKm: distance.trim() === "" ? null : Number(distance),
                jobCategory: category,
            });
            onClose();
        } catch (e) {
            toast.error(`${t("accounting.rateCard.manualAdd.error")} ${apiErrorText(e, t)}`);
        } finally {
            setSaving(false);
        }
    };
    const destinations = [...hubs.map((h) => ({ code: h.code, label: `${h.code} - ${h.label}` })), ...Object.entries(SOC_DESTINATIONS).map(([code, label]) => ({ code, label: `${code} - ${label}` }))];
    return (
        <Dialog open onOpenChange={(o) => !o && !saving && onClose()}>
            <DialogContent className="sm:max-w-xl">
                <DialogHeader>
                    <DialogTitle>{t("accounting.rateCard.manualAdd.title")}</DialogTitle>
                </DialogHeader>
                <div className="grid grid-cols-2 gap-3">
                    <div className="col-span-2 space-y-1">
                        <Label>{t("accounting.rateCard.table.customer")}</Label>
                        <CustomerSelect value={customerId} onChange={setCustomerId} customers={customers} className="w-full" />
                    </div>
                    <div className="space-y-1">
                        <Label>{t("accounting.rateCard.table.hubId")}</Label>
                        <Select value={hubId} onValueChange={setHubId}>
                            <SelectTrigger>
                                <SelectValue placeholder={t("accounting.rateCard.filterSourceSearchPlaceholder")} />
                            </SelectTrigger>
                            <SelectContent>
                                {hubs.map((h) => (
                                    <SelectItem key={h.code} value={h.code}>
                                        {h.code} - {h.label}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>
                    <div className="space-y-1">
                        <Label>{t("accounting.rateCard.table.destination")}</Label>
                        <Select value={destination} onValueChange={setDestination}>
                            <SelectTrigger>
                                <SelectValue placeholder={t("accounting.rateCard.filterDestinationSearchPlaceholder")} />
                            </SelectTrigger>
                            <SelectContent>
                                {destinations.map((d) => (
                                    <SelectItem key={d.code} value={d.code}>
                                        {d.label}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>
                    <div className="space-y-1">
                        <Label>{t("accounting.rateCard.table.vehicleClass")}</Label>
                        <Select value={vehicleClass} onValueChange={setVehicleClass}>
                            <SelectTrigger>
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                {TASK_TRUCK_TYPE_ENUM.map((v) => (
                                    <SelectItem key={v} value={v}>
                                        {v}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>
                    <div className="space-y-1">
                        <Label>{t("accounting.rateCard.jobCategory.label")}</Label>
                        <Select value={category} onValueChange={(v) => setCategory(v === "SUPPLEMENTARY" ? "SUPPLEMENTARY" : "PRIMARY")}>
                            <SelectTrigger>
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="PRIMARY">{t("accounting.rateCard.jobCategory.primary")}</SelectItem>
                                <SelectItem value="SUPPLEMENTARY">{t("accounting.rateCard.jobCategory.supplementary")}</SelectItem>
                            </SelectContent>
                        </Select>
                    </div>
                    <div className="space-y-1">
                        <Label>{t("accounting.rateCard.table.rateThb")}</Label>
                        <Input type="number" step="any" min="0" value={rate} onChange={(e) => setRate(e.target.value)} />
                    </div>
                    <div className="space-y-1">
                        <Label>{t("accounting.rateCard.table.distanceKm")}</Label>
                        <Input type="number" step="any" min="0" value={distance} onChange={(e) => setDistance(e.target.value)} />
                    </div>
                    <div className="space-y-1">
                        <Label>{t("accounting.rateCard.table.effectiveFrom")}</Label>
                        <Input type="date" value={effectiveFrom} onChange={(e) => setEffectiveFrom(e.target.value)} />
                    </div>
                </div>
                {!valid ? <p className="text-xs text-muted-foreground">{t("accounting.rateCard.manualAdd.invalid")}</p> : null}
                <DialogFooter>
                    <Button variant="outline" onClick={onClose} disabled={saving}>
                        {t("common.cancel")}
                    </Button>
                    <Button onClick={() => void save()} disabled={!valid || saving}>
                        {saving ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                        {t("accounting.rateCard.manualAdd.button")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}

export function EntriesTab({ customerId, customers, canEdit }: { customerId: string; customers: CustomerPick[]; canEdit: boolean }) {
    const { t } = useLanguage();
    const entries = useInfiniteQuery(rateEntriesQuery(customerId));
    const hubsQuery = useQuery(hubPicksQuery);
    const [hub, setHub] = useState(ALL);
    const [dest, setDest] = useState(ALL);
    const [vehicleClass, setVehicleClass] = useState(ALL);
    const [q, setQ] = useState("");
    const [showVoided, setShowVoided] = useState(false);
    const [adding, setAdding] = useState(false);
    const [importOpen, setImportOpen] = useState(false);
    const [importMounted, setImportMounted] = useState(false);
    const [voiding, setVoiding] = useState<RateEntryDTO | null>(null);
    const [exporting, setExporting] = useState(false);

    const all = rateEntryRows(entries.data?.pages);
    const rows = useMemo(() => filterRateEntries(all, { hubId: hub, destinationCode: dest, vehicleClass, q, showVoided }), [all, hub, dest, vehicleClass, q, showVoided]);
    const byId = useMemo(() => new Map(customers.map((c) => [c.id, c])), [customers]);
    const hubs = useMemo(
        () =>
            (hubsQuery.data ?? [])
                .filter((h) => (h.stationType ?? "").toUpperCase() !== "SOC")
                .map((h) => ({ code: h.sourceId.toUpperCase(), label: hubLabel(h) }))
                .sort((a, b) => a.code.localeCompare(b.code)),
        [hubsQuery.data]
    );
    const hubName = useMemo(() => new Map(hubs.map((h) => [h.code, h.label])), [hubs]);
    const sourceOf = (r: RateEntryDTO) => r.rawHubName?.trim() || (hubName.get(r.hubId) ? `${r.hubId} - ${hubName.get(r.hubId)}` : r.hubId);
    const destinationOf = (code: string) => (SOC_DESTINATIONS as Record<string, string>)[code] ?? hubName.get(code) ?? code;
    const options = (pick: (r: RateEntryDTO) => string) => [...new Set(all.map(pick))].filter(Boolean).sort();

    const exportRows = async () => {
        setExporting(true);
        try {
            const XLSX = await import("xlsx");
            const sheet = XLSX.utils.json_to_sheet(
                rows.map((r) => ({
                    [t("accounting.rateCard.export.customerCode")]: byId.get(r.customerId)?.code ?? r.customerId,
                    [t("accounting.rateCard.export.hubName")]: sourceOf(r),
                    [t("accounting.rateCard.table.destination")]: r.destinationCode,
                    [t("accounting.rateCard.export.destinationName")]: destinationOf(r.destinationCode),
                    [t("accounting.rateCard.table.vehicleClass")]: r.vehicleClass,
                    [t("accounting.rateCard.jobCategory.label")]: r.jobCategory,
                    [t("accounting.rateCard.table.rateThb")]: r.rateThb,
                    [t("accounting.rateCard.table.distanceKm")]: r.distanceKm ?? "",
                    [t("accounting.rateCard.table.effectiveFrom")]: r.effectiveFromDateStr,
                    [t("accounting.rateCard.table.importId")]: r.importId,
                }))
            );
            const wb = XLSX.utils.book_new();
            XLSX.utils.book_append_sheet(wb, sheet, "rate_card");
            XLSX.writeFile(wb, `rate_card_${customerId === ALL_CUSTOMERS ? "all" : (byId.get(customerId)?.code ?? customerId)}.xlsx`);
        } catch (e) {
            toast.error(apiErrorText(e, t));
        } finally {
            setExporting(false);
        }
    };

    const columns = useMemo(
        () => [
            ...(customerId === ALL_CUSTOMERS
                ? [col.display({ id: "customer", header: () => t("accounting.rateCard.table.customer"), cell: ({ row: { original: r } }) => customerLabel(byId.get(r.customerId)) || r.customerId, meta: { cellClassName: "text-xs" } })]
                : []),
            col.display({ id: "source", header: () => t("accounting.rateCard.table.hubId"), cell: ({ row: { original: r } }) => sourceOf(r), meta: { cellClassName: "text-xs" } }),
            col.display({
                id: "destination",
                header: () => t("accounting.rateCard.table.destination"),
                cell: ({ row: { original: r } }) => (
                    <span>
                        <span className="font-mono">{r.destinationCode}</span>
                        <span className="block text-muted-foreground">{destinationOf(r.destinationCode)}</span>
                    </span>
                ),
                meta: { cellClassName: "text-xs" },
            }),
            col.accessor("vehicleClass", { header: () => t("accounting.rateCard.table.vehicleClass"), cell: (c) => <Badge variant="outline">{c.getValue()}</Badge> }),
            col.accessor("jobCategory", {
                header: () => t("accounting.rateCard.jobCategory.label"),
                cell: (c) => (c.getValue() === "SUPPLEMENTARY" ? t("accounting.rateCard.jobCategory.supplementary") : t("accounting.rateCard.jobCategory.primary")),
                meta: { cellClassName: "text-xs" },
            }),
            col.accessor("rateThb", { header: () => t("accounting.rateCard.table.rateThb"), cell: (c) => formatThb(c.getValue()), meta: { headerClassName: "text-right", cellClassName: "text-right font-mono" } }),
            col.accessor("distanceKm", { header: () => t("accounting.rateCard.table.distanceKm"), cell: (c) => c.getValue() ?? "—", meta: { headerClassName: "text-right", cellClassName: "text-right text-xs" } }),
            col.accessor("effectiveFromDateStr", { header: () => t("accounting.rateCard.table.effectiveFrom"), cell: (c) => formatDay(c.getValue()), meta: { cellClassName: "whitespace-nowrap text-xs" } }),
            col.accessor("importId", { header: () => t("accounting.rateCard.table.importId"), meta: { cellClassName: "max-w-[140px] truncate font-mono text-[10px]" } }),
            col.display({
                id: "status",
                header: () => "",
                cell: ({ row: { original: r } }) =>
                    r.voided ? (
                        <Badge variant="destructive" title={r.voidedReason ?? undefined}>
                            {t("accounting.rateCard.void.badge")}
                        </Badge>
                    ) : canEdit ? (
                        <Button size="sm" variant="ghost" className="h-7 px-2 text-xs" onClick={() => setVoiding(r)}>
                            <Ban className="mr-1 h-3 w-3" />
                            {t("accounting.rateCard.void.action")}
                        </Button>
                    ) : null,
            }),
        ],
        // sourceOf / destinationOf read hubName.
        // eslint-disable-next-line react-hooks/exhaustive-deps
        [t, customerId, byId, hubName, canEdit]
    );

    return (
        <div className="space-y-4">
            <div className="flex flex-wrap items-end gap-3">
                <div className="space-y-1">
                    <Label className="text-xs">{t("accounting.rateCard.filterSource")}</Label>
                    <Select value={hub} onValueChange={setHub}>
                        <SelectTrigger className="w-48">
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value={ALL}>{t("accounting.filter.all")}</SelectItem>
                            {options((r) => r.hubId).map((h) => (
                                <SelectItem key={h} value={h}>
                                    {h} {hubName.get(h) ? `- ${hubName.get(h)}` : ""}
                                </SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                </div>
                <div className="space-y-1">
                    <Label className="text-xs">{t("accounting.rateCard.filterDestination")}</Label>
                    <Select value={dest} onValueChange={setDest}>
                        <SelectTrigger className="w-48">
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value={ALL}>{t("accounting.filter.all")}</SelectItem>
                            {options((r) => r.destinationCode).map((d) => (
                                <SelectItem key={d} value={d}>
                                    {d} - {destinationOf(d)}
                                </SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                </div>
                <div className="space-y-1">
                    <Label className="text-xs">{t("accounting.rateCard.filterVehicleClass")}</Label>
                    <Select value={vehicleClass} onValueChange={setVehicleClass}>
                        <SelectTrigger className="w-32">
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value={ALL}>{t("accounting.filter.all")}</SelectItem>
                            {options((r) => r.vehicleClass).map((v) => (
                                <SelectItem key={v} value={v}>
                                    {v}
                                </SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                </div>
                <div className="space-y-1">
                    <Label className="text-xs">{t("accounting.rateCard.filterSearch")}</Label>
                    <Input className="w-64" value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("accounting.rateCard.filterSearchPlaceholder")} />
                </div>
                <label className="flex cursor-pointer items-center gap-2 text-sm">
                    <Checkbox checked={showVoided} onCheckedChange={(v) => setShowVoided(v === true)} />
                    {t("accounting.go.rateCard.showVoided")}
                </label>
                <div className="ml-auto flex flex-wrap gap-2">
                    <Button variant="outline" size="sm" onClick={() => void exportRows()} disabled={exporting || rows.length === 0}>
                        {exporting ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Download className="mr-2 h-4 w-4" />}
                        {t("accounting.rateCard.exportData")}
                    </Button>
                    {canEdit ? (
                        <>
                            <Button
                                variant="outline"
                                size="sm"
                                onClick={() => {
                                    setImportMounted(true);
                                    setImportOpen(true);
                                }}
                            >
                                <Upload className="mr-2 h-4 w-4" />
                                {t("accounting.rateCard.importButton")}
                            </Button>
                            <Button size="sm" onClick={() => setAdding(true)}>
                                <Plus className="mr-2 h-4 w-4" />
                                {t("accounting.rateCard.manualAdd.button")}
                            </Button>
                        </>
                    ) : null}
                </div>
            </div>
            <p className="text-xs text-muted-foreground">{t("accounting.go.rateCard.loadedHint", { shown: rows.length, loaded: all.length })}</p>
            <ApiErrorAlert error={entries.error} />
            <VirtualTable
                data={rows}
                columns={columns}
                getRowId={(r) => r.id}
                aria-label={t("accounting.rateCard.title")}
                infinite={entries}
                loading={entries.isPending}
                error={entries.isError}
                emptyState={<p className="py-8 text-center text-sm text-muted-foreground">{t("accounting.rateCard.noEntries")}</p>}
                resetScrollKey={customerId}
            />
            <LoadMore
                loaded={all.length}
                hasNextPage={entries.hasNextPage}
                isFetchingNextPage={entries.isFetchingNextPage}
                error={entries.isFetchNextPageError}
                onLoadMore={() => void entries.fetchNextPage()}
            />
            {adding ? <AddRateDialog customers={customers} hubs={hubs} initialCustomerId={customerId} onClose={() => setAdding(false)} /> : null}
            {importMounted ? (
                <RateImportDialog
                    open={importOpen}
                    onOpenChange={setImportOpen}
                    customers={customers}
                    knownHubIds={hubs.map((h) => h.code)}
                    initialCustomerId={customerId === ALL_CUSTOMERS ? undefined : customerId}
                />
            ) : null}
            {voiding ? (
                <VoidDialog
                    label={`${sourceOf(voiding)} → ${voiding.destinationCode} · ${voiding.vehicleClass} · ${formatThb(voiding.rateThb)} · ${voiding.effectiveFromDateStr}`}
                    onVoid={(reason) => voidRateEntry(voiding, reason)}
                    onClose={() => setVoiding(null)}
                />
            ) : null}
        </div>
    );
}
