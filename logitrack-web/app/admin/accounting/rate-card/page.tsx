"use client";

import { useEffect, useMemo, useState } from "react";
import { format } from "date-fns";
import { Loader2, Plus, RefreshCw, Upload } from "lucide-react";
import { useLanguage } from "@/context/language";
import { usePermission } from "@/hooks/usePermission";
import { CAPABILITIES } from "@/lib/capabilities";
import { getCustomers } from "@/features/customers/api/customers";
import type { Customer } from "@/validate/customerSchema";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Combobox, type ComboboxOption } from "@/components/ui/combobox";
import {
    createCustomerFuelRateAdjustment,
    getCustomerFuelRateAdjustments,
    getCustomerRateEntries,
    type CustomerFuelRateAdjustmentRow,
    type CustomerRateEntryRow,
} from "../actions.client";
import { RateCardImportDialog, type RateCardCustomerOption } from "../rate-card-import-dialog";
import { db } from "@/firebase/client";
import { collection, getDocs } from "firebase/firestore";
import { COLLECTIONS } from "@/lib/collections";
import { SOC_DESTINATIONS } from "@/validate/taskSchema";

interface HubOption {
    id: string;
}

function parseDateInput(date: string): Date | null {
    if (!date) return null;
    const d = new Date(`${date}T00:00:00`);
    return Number.isNaN(d.getTime()) ? null : d;
}

export default function AccountingRateCardPage() {
    const { t } = useLanguage();
    const { hasPermission: canEdit } = usePermission(CAPABILITIES.accounting_edit_rate_card);
    const [loading, setLoading] = useState(true);
    const [importOpen, setImportOpen] = useState(false);
    const [customers, setCustomers] = useState<RateCardCustomerOption[]>([]);
    const [hubs, setHubs] = useState<HubOption[]>([]);
    const [entries, setEntries] = useState<CustomerRateEntryRow[]>([]);
    const [fuelAdjustments, setFuelAdjustments] = useState<CustomerFuelRateAdjustmentRow[]>([]);
    const [filterCustomerId, setFilterCustomerId] = useState("all");
    const [filterSourceHubId, setFilterSourceHubId] = useState("all");
    const [filterDestinationCode, setFilterDestinationCode] = useState("all");
    const [filterVehicleClass, setFilterVehicleClass] = useState("all");
    const [filterSearch, setFilterSearch] = useState("");
    const [entriesPage, setEntriesPage] = useState(1);
    const [entriesPerPage, setEntriesPerPage] = useState(15);
    const [adjustmentError, setAdjustmentError] = useState<string | null>(null);
    const [savingAdjustment, setSavingAdjustment] = useState(false);
    const [adjustmentForm, setAdjustmentForm] = useState({
        customerId: "",
        effectiveFrom: "",
        rateMultiplier: "1.00",
        addThbPerTrip: "0",
        referenceFuelPrice: "",
        announcementNote: "",
    });

    const customerNameById = useMemo(() => {
        const map = new Map<string, string>();
        customers.forEach((c) => map.set(c.id, `${c.code} - ${c.name}`));
        return map;
    }, [customers]);
    const sourceNameByHubId = useMemo(() => {
        const map = new Map<string, string>();
        entries.forEach((entry) => {
            if (!map.has(entry.hubId) && entry.rawHubName?.trim()) {
                map.set(entry.hubId, entry.rawHubName.trim());
            }
        });
        return map;
    }, [entries]);
    const formatSource = (hubId: string) => sourceNameByHubId.get(hubId) ?? hubId;
    const formatDestination = (destinationCode: string) =>
        (SOC_DESTINATIONS as Record<string, string>)[destinationCode] ?? destinationCode;

    const loadData = async () => {
        setLoading(true);
        try {
            const [customerRows, entryRows, fuelRows, hubRows] = await Promise.all([
                getCustomers(),
                getCustomerRateEntries(),
                getCustomerFuelRateAdjustments(),
                getDocs(collection(db, COLLECTIONS.HUBS)),
            ]);
            const mappedCustomers: RateCardCustomerOption[] = (customerRows as (Customer & { id: string })[]).map((c) => ({
                id: c.id,
                code: c.code,
                name: c.name,
            }));
            setCustomers(mappedCustomers);
            setEntries(entryRows);
            setFuelAdjustments(fuelRows);
            setHubs(
                hubRows.docs.map((d) => {
                    const data = d.data();
                    const id = String(data.hubId ?? data.source_id ?? "").trim().toUpperCase();
                    return { id };
                }).filter((x) => x.id)
            );
        } finally {
            setLoading(false);
        }
    };

    useEffect(() => {
        void loadData();
    }, []);

    const filteredEntries = useMemo(() => {
        let list = entries;
        if (filterCustomerId !== "all") {
            list = list.filter((e) => e.customerId === filterCustomerId);
        }
        if (filterSourceHubId !== "all") {
            list = list.filter((e) => e.hubId === filterSourceHubId);
        }
        if (filterDestinationCode !== "all") {
            list = list.filter((e) => e.destinationCode === filterDestinationCode);
        }
        if (filterVehicleClass !== "all") {
            list = list.filter((e) => e.vehicleClass === filterVehicleClass);
        }
        if (filterSearch.trim()) {
            const q = filterSearch.trim().toLowerCase();
            list = list.filter(
                (e) =>
                    e.hubId.toLowerCase().includes(q) ||
                    e.destinationCode.toLowerCase().includes(q) ||
                    e.rawHubName.toLowerCase().includes(q) ||
                    e.importId.toLowerCase().includes(q)
            );
        }
        return list;
    }, [entries, filterCustomerId, filterSourceHubId, filterDestinationCode, filterVehicleClass, filterSearch]);
    const sourceHubOptions = useMemo(
        () => Array.from(new Set(entries.map((e) => e.hubId))).sort((a, b) => a.localeCompare(b)),
        [entries]
    );
    const destinationOptions = useMemo(
        () => Array.from(new Set(entries.map((e) => e.destinationCode))).sort((a, b) => a.localeCompare(b)),
        [entries]
    );
    const vehicleOptions = useMemo(
        () => Array.from(new Set(entries.map((e) => e.vehicleClass))).sort((a, b) => a.localeCompare(b)),
        [entries]
    );
    const sourceComboboxOptions = useMemo<ComboboxOption[]>(
        () => [
            { value: "all", label: t("accounting.filter.all") },
            ...sourceHubOptions.map((hubId) => ({ value: hubId, label: formatSource(hubId) })),
        ],
        [sourceHubOptions, t]
    );
    const destinationComboboxOptions = useMemo<ComboboxOption[]>(
        () => [
            { value: "all", label: t("accounting.filter.all") },
            ...destinationOptions.map((destinationCode) => ({
                value: destinationCode,
                label: formatDestination(destinationCode),
            })),
        ],
        [destinationOptions, t]
    );
    const entriesTotalPages = Math.max(1, Math.ceil(filteredEntries.length / entriesPerPage));
    const paginatedEntries = useMemo(() => {
        const start = (entriesPage - 1) * entriesPerPage;
        return filteredEntries.slice(start, start + entriesPerPage);
    }, [filteredEntries, entriesPage, entriesPerPage]);
    const entriesRangeStart = filteredEntries.length === 0 ? 0 : (entriesPage - 1) * entriesPerPage + 1;
    const entriesRangeEnd = Math.min(entriesPage * entriesPerPage, filteredEntries.length);

    const filteredAdjustments = useMemo(() => {
        if (filterCustomerId === "all") return fuelAdjustments;
        return fuelAdjustments.filter((r) => r.customerId === filterCustomerId);
    }, [fuelAdjustments, filterCustomerId]);

    useEffect(() => {
        setEntriesPage(1);
    }, [filterCustomerId, filterSourceHubId, filterDestinationCode, filterVehicleClass, filterSearch, entriesPerPage]);

    useEffect(() => {
        if (entriesPage > entriesTotalPages) setEntriesPage(entriesTotalPages);
    }, [entriesPage, entriesTotalPages]);

    const handleCreateAdjustment = async () => {
        setAdjustmentError(null);
        const customerId = adjustmentForm.customerId.trim();
        const effectiveFrom = parseDateInput(adjustmentForm.effectiveFrom);
        const rateMultiplier = Number(adjustmentForm.rateMultiplier);
        const addThbPerTrip = Number(adjustmentForm.addThbPerTrip || "0");
        const referenceFuelPrice = adjustmentForm.referenceFuelPrice
            ? Number(adjustmentForm.referenceFuelPrice)
            : undefined;

        if (!customerId || !effectiveFrom || !Number.isFinite(rateMultiplier) || rateMultiplier <= 0) {
            setAdjustmentError(t("accounting.rateCard.fuelAdjustments.form.invalid"));
            return;
        }
        setSavingAdjustment(true);
        try {
            await createCustomerFuelRateAdjustment({
                customerId,
                effectiveFrom,
                rateMultiplier,
                addThbPerTrip: Number.isFinite(addThbPerTrip) ? addThbPerTrip : 0,
                referenceFuelPriceThbPerLitre: referenceFuelPrice,
                announcementNote: adjustmentForm.announcementNote,
            });
            setAdjustmentForm((prev) => ({
                ...prev,
                effectiveFrom: "",
                rateMultiplier: "1.00",
                addThbPerTrip: "0",
                referenceFuelPrice: "",
                announcementNote: "",
            }));
            await loadData();
        } catch (err) {
            console.error(err);
            setAdjustmentError(t("accounting.rateCard.fuelAdjustments.form.error"));
        } finally {
            setSavingAdjustment(false);
        }
    };

    if (loading) {
        return (
            <div className="flex h-64 items-center justify-center">
                <Loader2 className="h-8 w-8 animate-spin text-primary" />
            </div>
        );
    }

    return (
        <div className="container mx-auto p-6 space-y-6 max-w-[1600px]">
            <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">{t("accounting.rateCard.title")}</h1>
                    <p className="text-muted-foreground mt-1">{t("accounting.rateCard.subtitle")}</p>
                </div>
                <div className="flex items-center gap-2">
                    <Button variant="outline" onClick={() => void loadData()}>
                        <RefreshCw className="h-4 w-4 mr-2" />
                        {t("common.refresh")}
                    </Button>
                    {canEdit && (
                        <Button onClick={() => setImportOpen(true)}>
                            <Upload className="h-4 w-4 mr-2" />
                            {t("accounting.rateCard.importButton")}
                        </Button>
                    )}
                </div>
            </div>

            <Card>
                <CardHeader className="pb-3">
                    <CardTitle className="text-base">{t("accounting.filter.title")}</CardTitle>
                </CardHeader>
                <CardContent className="flex flex-wrap gap-4">
                    <div className="flex flex-col gap-1.5 min-w-[280px]">
                        <Label>{t("accounting.rateCard.filterCustomer")}</Label>
                        <Select value={filterCustomerId} onValueChange={setFilterCustomerId}>
                            <SelectTrigger>
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="all">{t("accounting.rateCard.filterCustomerAll")}</SelectItem>
                                {customers.map((c) => (
                                    <SelectItem key={c.id} value={c.id}>
                                        {c.code} - {c.name}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>
                    <div className="flex flex-col gap-1.5 min-w-[200px]">
                        <Label>{t("accounting.rateCard.filterSource")}</Label>
                        <Combobox
                            options={sourceComboboxOptions}
                            value={filterSourceHubId}
                            onSelect={(value) => setFilterSourceHubId(value || "all")}
                            placeholder={t("accounting.rateCard.filterSource")}
                            searchPlaceholder={t("accounting.rateCard.filterSourceSearchPlaceholder")}
                            emptyText={t("accounting.rateCard.filterNoResults")}
                            className="min-w-[200px]"
                        />
                    </div>
                    <div className="flex flex-col gap-1.5 min-w-[200px]">
                        <Label>{t("accounting.rateCard.filterDestination")}</Label>
                        <Combobox
                            options={destinationComboboxOptions}
                            value={filterDestinationCode}
                            onSelect={(value) => setFilterDestinationCode(value || "all")}
                            placeholder={t("accounting.rateCard.filterDestination")}
                            searchPlaceholder={t("accounting.rateCard.filterDestinationSearchPlaceholder")}
                            emptyText={t("accounting.rateCard.filterNoResults")}
                            className="min-w-[200px]"
                        />
                    </div>
                    <div className="flex flex-col gap-1.5 min-w-[180px]">
                        <Label>{t("accounting.rateCard.filterVehicleClass")}</Label>
                        <Select value={filterVehicleClass} onValueChange={setFilterVehicleClass}>
                            <SelectTrigger>
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="all">{t("accounting.filter.all")}</SelectItem>
                                {vehicleOptions.map((vehicleClass) => (
                                    <SelectItem key={vehicleClass} value={vehicleClass}>
                                        {vehicleClass}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>
                    <div className="flex flex-col gap-1.5 min-w-[260px]">
                        <Label>{t("accounting.rateCard.filterSearch")}</Label>
                        <Input
                            value={filterSearch}
                            onChange={(e) => setFilterSearch(e.target.value)}
                            placeholder={t("accounting.rateCard.filterSearchPlaceholder")}
                        />
                    </div>
                </CardContent>
            </Card>

            <Card>
                <CardHeader>
                    <CardTitle>{t("accounting.rateCard.title")}</CardTitle>
                </CardHeader>
                <CardContent>
                    <Table>
                        <TableHeader>
                            <TableRow>
                                <TableHead>{t("accounting.rateCard.table.customer")}</TableHead>
                                <TableHead>{t("accounting.rateCard.table.hubId")}</TableHead>
                                <TableHead>{t("accounting.rateCard.table.destination")}</TableHead>
                                <TableHead>{t("accounting.rateCard.table.vehicleClass")}</TableHead>
                                <TableHead className="text-right">{t("accounting.rateCard.table.rateThb")}</TableHead>
                                <TableHead className="text-right">{t("accounting.rateCard.table.distanceKm")}</TableHead>
                                <TableHead>{t("accounting.rateCard.table.effectiveFrom")}</TableHead>
                                <TableHead>{t("accounting.rateCard.table.importId")}</TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {paginatedEntries.map((row) => (
                                <TableRow key={row.id}>
                                    <TableCell>{customerNameById.get(row.customerId) ?? row.customerId}</TableCell>
                                    <TableCell>{formatSource(row.hubId)}</TableCell>
                                    <TableCell>{formatDestination(row.destinationCode)}</TableCell>
                                    <TableCell className="font-mono text-xs">{row.vehicleClass}</TableCell>
                                    <TableCell className="text-right">฿{row.rateThb.toLocaleString()}</TableCell>
                                    <TableCell className="text-right">
                                        {row.distanceKm != null ? row.distanceKm.toLocaleString() : "-"}
                                    </TableCell>
                                    <TableCell>{format(row.effectiveFrom, "dd/MM/yyyy")}</TableCell>
                                    <TableCell className="font-mono text-xs">{row.importId}</TableCell>
                                </TableRow>
                            ))}
                            {paginatedEntries.length === 0 && (
                                <TableRow>
                                    <TableCell colSpan={8} className="h-24 text-center text-muted-foreground">
                                        {t("accounting.rateCard.noEntries")}
                                    </TableCell>
                                </TableRow>
                            )}
                        </TableBody>
                    </Table>
                    <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 px-1 pt-4">
                        <div className="text-sm text-muted-foreground">
                            {t("accounting.rateCard.pagination.showing", {
                                from: entriesRangeStart,
                                to: entriesRangeEnd,
                                total: filteredEntries.length,
                            })}
                        </div>
                        <div className="flex flex-wrap items-center gap-2">
                            <div className="flex items-center gap-2">
                                <span className="text-sm text-muted-foreground">
                                    {t("accounting.rateCard.pagination.rowsPerPage")}
                                </span>
                                <Select
                                    value={String(entriesPerPage)}
                                    onValueChange={(v) => setEntriesPerPage(Number(v))}
                                >
                                    <SelectTrigger className="h-9 w-[84px]">
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent>
                                        {[10, 15, 25, 50, 100].map((n) => (
                                            <SelectItem key={n} value={String(n)}>
                                                {n}
                                            </SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                            </div>
                            <Button
                                variant="outline"
                                size="sm"
                                onClick={() => setEntriesPage((p) => Math.max(1, p - 1))}
                                disabled={entriesPage <= 1}
                            >
                                {t("accounting.rateCard.pagination.previous")}
                            </Button>
                            <Button
                                variant="outline"
                                size="sm"
                                onClick={() => setEntriesPage((p) => Math.min(entriesTotalPages, p + 1))}
                                disabled={entriesPage >= entriesTotalPages}
                            >
                                {t("accounting.rateCard.pagination.next")}
                            </Button>
                        </div>
                    </div>
                </CardContent>
            </Card>

            <Card>
                <CardHeader className="flex flex-row items-center justify-between">
                    <div>
                        <CardTitle>{t("accounting.rateCard.fuelAdjustments.title")}</CardTitle>
                        <p className="text-sm text-muted-foreground mt-1">
                            {t("accounting.rateCard.fuelAdjustments.subtitle")}
                        </p>
                    </div>
                    {canEdit && (
                        <Button variant="secondary" onClick={handleCreateAdjustment} disabled={savingAdjustment}>
                            {savingAdjustment ? (
                                <Loader2 className="h-4 w-4 mr-2 animate-spin" />
                            ) : (
                                <Plus className="h-4 w-4 mr-2" />
                            )}
                            {savingAdjustment
                                ? t("accounting.rateCard.fuelAdjustments.form.saving")
                                : t("accounting.rateCard.fuelAdjustments.form.save")}
                        </Button>
                    )}
                </CardHeader>
                <CardContent className="space-y-4">
                    {canEdit && (
                        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3 rounded-lg border p-4">
                            <div className="space-y-1.5">
                                <Label>{t("accounting.rateCard.fuelAdjustments.form.customer")}</Label>
                                <Select
                                    value={adjustmentForm.customerId}
                                    onValueChange={(v) => setAdjustmentForm((prev) => ({ ...prev, customerId: v }))}
                                >
                                    <SelectTrigger>
                                        <SelectValue placeholder={t("accounting.rateCard.import.selectCustomerPlaceholder")} />
                                    </SelectTrigger>
                                    <SelectContent>
                                        {customers.map((c) => (
                                            <SelectItem key={c.id} value={c.id}>
                                                {c.code} - {c.name}
                                            </SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                            </div>
                            <div className="space-y-1.5">
                                <Label>{t("accounting.rateCard.fuelAdjustments.form.effectiveFrom")}</Label>
                                <Input
                                    type="date"
                                    value={adjustmentForm.effectiveFrom}
                                    onChange={(e) => setAdjustmentForm((prev) => ({ ...prev, effectiveFrom: e.target.value }))}
                                />
                            </div>
                            <div className="space-y-1.5">
                                <Label>{t("accounting.rateCard.fuelAdjustments.form.multiplier")}</Label>
                                <Input
                                    type="number"
                                    step="0.01"
                                    value={adjustmentForm.rateMultiplier}
                                    onChange={(e) => setAdjustmentForm((prev) => ({ ...prev, rateMultiplier: e.target.value }))}
                                />
                            </div>
                            <div className="space-y-1.5">
                                <Label>{t("accounting.rateCard.fuelAdjustments.form.addPerTrip")}</Label>
                                <Input
                                    type="number"
                                    step="0.01"
                                    value={adjustmentForm.addThbPerTrip}
                                    onChange={(e) => setAdjustmentForm((prev) => ({ ...prev, addThbPerTrip: e.target.value }))}
                                />
                            </div>
                            <div className="space-y-1.5">
                                <Label>{t("accounting.rateCard.fuelAdjustments.form.referenceFuelPrice")}</Label>
                                <Input
                                    type="number"
                                    step="0.01"
                                    value={adjustmentForm.referenceFuelPrice}
                                    onChange={(e) => setAdjustmentForm((prev) => ({ ...prev, referenceFuelPrice: e.target.value }))}
                                />
                            </div>
                            <div className="space-y-1.5">
                                <Label>{t("accounting.rateCard.fuelAdjustments.form.note")}</Label>
                                <Input
                                    value={adjustmentForm.announcementNote}
                                    onChange={(e) => setAdjustmentForm((prev) => ({ ...prev, announcementNote: e.target.value }))}
                                    placeholder={t("accounting.rateCard.fuelAdjustments.form.notePlaceholder")}
                                />
                            </div>
                            {adjustmentError && (
                                <p className="md:col-span-2 lg:col-span-3 text-sm text-destructive">{adjustmentError}</p>
                            )}
                        </div>
                    )}

                    <Table>
                        <TableHeader>
                            <TableRow>
                                <TableHead>{t("accounting.rateCard.fuelAdjustments.table.customer")}</TableHead>
                                <TableHead>{t("accounting.rateCard.fuelAdjustments.table.effectiveFrom")}</TableHead>
                                <TableHead className="text-right">{t("accounting.rateCard.fuelAdjustments.table.multiplier")}</TableHead>
                                <TableHead className="text-right">{t("accounting.rateCard.fuelAdjustments.table.addPerTrip")}</TableHead>
                                <TableHead className="text-right">{t("accounting.rateCard.fuelAdjustments.table.referencePrice")}</TableHead>
                                <TableHead>{t("accounting.rateCard.fuelAdjustments.table.note")}</TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {filteredAdjustments.map((row) => (
                                <TableRow key={row.id}>
                                    <TableCell>{customerNameById.get(row.customerId) ?? row.customerId}</TableCell>
                                    <TableCell>{format(row.effectiveFrom, "dd/MM/yyyy")}</TableCell>
                                    <TableCell className="text-right">{row.rateMultiplier.toFixed(2)}</TableCell>
                                    <TableCell className="text-right">฿{(row.addThbPerTrip ?? 0).toLocaleString()}</TableCell>
                                    <TableCell className="text-right">
                                        {row.referenceFuelPriceThbPerLitre != null
                                            ? `฿${row.referenceFuelPriceThbPerLitre.toLocaleString()}`
                                            : "-"}
                                    </TableCell>
                                    <TableCell>{row.announcementNote || "-"}</TableCell>
                                </TableRow>
                            ))}
                            {filteredAdjustments.length === 0 && (
                                <TableRow>
                                    <TableCell colSpan={6} className="h-20 text-center text-muted-foreground">
                                        {t("accounting.rateCard.fuelAdjustments.noRows")}
                                    </TableCell>
                                </TableRow>
                            )}
                        </TableBody>
                    </Table>
                </CardContent>
            </Card>

            <RateCardImportDialog
                open={importOpen}
                onOpenChange={setImportOpen}
                customers={customers}
                initialCustomerId={filterCustomerId !== "all" ? filterCustomerId : undefined}
                knownHubIds={hubs.map((h) => h.id)}
                onImported={() => void loadData()}
            />
        </div>
    );
}
