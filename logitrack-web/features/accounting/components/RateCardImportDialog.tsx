"use client";

import { useMemo, useState } from "react";
import * as XLSX from "xlsx";
import { Loader2, Upload } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { DatePicker } from "@/components/ui/date-picker";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useLanguage } from "@/context/language";
import {
    batchCreateCustomerRateEntries,
    type CustomerRateEntryInput,
} from "../api/billing";

export interface RateCardCustomerOption {
    id: string;
    code: string;
    name: string;
}

interface RateCardImportDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    customers: RateCardCustomerOption[];
    knownHubIds: string[];
    initialCustomerId?: string;
    onImported?: () => void;
}

function parseNumeric(value: unknown): number | null {
    if (typeof value === "number" && Number.isFinite(value)) return value;
    if (typeof value !== "string") return null;
    const normalized = value.replace(/,/g, "").trim();
    if (!normalized) return null;
    const n = Number(normalized);
    return Number.isFinite(n) ? n : null;
}

function asText(value: unknown): string {
    return String(value ?? "").trim();
}

export function RateCardImportDialog({
    open,
    onOpenChange,
    customers,
    knownHubIds,
    initialCustomerId,
    onImported,
}: RateCardImportDialogProps) {
    const { t } = useLanguage();
    const [customerId, setCustomerId] = useState<string>(initialCustomerId ?? "");
    const [effectiveFrom, setEffectiveFrom] = useState<Date | undefined>(undefined);
    const [rows, setRows] = useState<CustomerRateEntryInput[]>([]);
    const [warnings, setWarnings] = useState<string[]>([]);
    const [loading, setLoading] = useState(false);
    const [error, setError] = useState<string | null>(null);

    const knownHubSet = useMemo(() => new Set(knownHubIds.map((h) => h.toUpperCase())), [knownHubIds]);
    const selectedCustomer = useMemo(
        () => customers.find((c) => c.id === customerId) ?? null,
        [customers, customerId]
    );

    const resetState = () => {
        setRows([]);
        setWarnings([]);
        setError(null);
        setEffectiveFrom(undefined);
    };

    const parseFile = async (file: File) => {
        setError(null);
        setWarnings([]);
        setRows([]);
        try {
            const buffer = await file.arrayBuffer();
            const wb = XLSX.read(buffer, { type: "array" });
            const sheetName = wb.SheetNames[0];
            if (!sheetName) {
                setError(t("accounting.rateCard.import.emptySheet"));
                return;
            }
            const sheet = wb.Sheets[sheetName];
            const rawRows = XLSX.utils.sheet_to_json(sheet, { header: 1, defval: "" }) as unknown[][];
            if (!rawRows.length || rawRows.length < 2) {
                setError(t("accounting.rateCard.import.emptySheet"));
                return;
            }

            const nextRows: CustomerRateEntryInput[] = [];
            const nextWarnings: string[] = [];
            for (let i = 1; i < rawRows.length; i += 1) {
                const row = rawRows[i] ?? [];
                const rawHubName = asText(row[0]);
                const destinationCode = asText(row[2]).toUpperCase();
                const rate = parseNumeric(row[3]);
                const distance = parseNumeric(row[4]);

                if (!rawHubName && !destinationCode && rate == null) continue;
                const hubId = rawHubName.split(" - ")[0].trim().toUpperCase();
                if (!hubId) {
                    nextWarnings.push(
                        t("accounting.rateCard.import.warningMissingHub", { row: i + 1 })
                    );
                    continue;
                }
                if (!knownHubSet.has(hubId)) {
                    nextWarnings.push(
                        t("accounting.rateCard.import.warningUnknownHub", { row: i + 1, hubId })
                    );
                    continue;
                }
                if (!destinationCode) {
                    nextWarnings.push(
                        t("accounting.rateCard.import.warningMissingDestination", { row: i + 1 })
                    );
                    continue;
                }
                if (rate == null) {
                    nextWarnings.push(
                        t("accounting.rateCard.import.warningInvalidRate", { row: i + 1 })
                    );
                    continue;
                }
                nextRows.push({
                    hubId,
                    rawHubName,
                    destinationCode,
                    vehicleClass: "4WJ",
                    rateThb: rate,
                    distanceKm: distance ?? undefined,
                });
            }

            if (nextRows.length === 0) {
                setError(t("accounting.rateCard.import.noValidRows"));
            }
            setRows(nextRows);
            setWarnings(nextWarnings);
        } catch (err) {
            console.error(err);
            setError(t("accounting.rateCard.import.parseError"));
        }
    };

    const handleImport = async () => {
        if (!customerId || rows.length === 0) return;
        setLoading(true);
        setError(null);
        try {
            const date = effectiveFrom ?? new Date();
            await batchCreateCustomerRateEntries(customerId, rows, date);
            onImported?.();
            onOpenChange(false);
            resetState();
        } catch (err) {
            console.error(err);
            setError(t("accounting.rateCard.import.pushError"));
        } finally {
            setLoading(false);
        }
    };

    return (
        <Dialog
            open={open}
            onOpenChange={(nextOpen) => {
                onOpenChange(nextOpen);
                if (!nextOpen) resetState();
            }}
        >
            <DialogContent className="sm:max-w-2xl">
                <DialogHeader>
                    <DialogTitle>{t("accounting.rateCard.import.title")}</DialogTitle>
                    <DialogDescription>
                        {selectedCustomer
                            ? `${selectedCustomer.code} - ${selectedCustomer.name}`
                            : t("accounting.rateCard.import.selectCustomerPlaceholder")}
                    </DialogDescription>
                </DialogHeader>

                <div className="space-y-4">
                    <div className="grid gap-2">
                        <Label>{t("accounting.rateCard.import.selectCustomer")}</Label>
                        <Select value={customerId} onValueChange={setCustomerId}>
                            <SelectTrigger>
                                <SelectValue placeholder={t("accounting.rateCard.import.selectCustomerPlaceholder")} />
                            </SelectTrigger>
                            <SelectContent className="z-[1005]" position="popper">
                                {customers.map((c) => (
                                    <SelectItem key={c.id} value={c.id}>
                                        {c.code} - {c.name}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>

                    <div className="grid gap-2">
                        <Label>{t("accounting.rateCard.table.effectiveFrom")}</Label>
                        <DatePicker
                            value={effectiveFrom}
                            onChange={setEffectiveFrom}
                            placeholder={t("accounting.rateCard.import.effectiveDatePlaceholder")}
                            disabled={loading}
                        />
                    </div>

                    <div className="grid gap-2">
                        <Label>{t("accounting.rateCard.import.clickUpload")}</Label>
                        <div className="rounded-lg border border-dashed p-4">
                            <Input
                                type="file"
                                accept=".xlsx,.xls"
                                disabled={!customerId || loading}
                                onChange={(e) => {
                                    const file = e.target.files?.[0];
                                    if (!file) return;
                                    void parseFile(file);
                                }}
                            />
                            <p className="mt-2 text-xs text-muted-foreground">
                                {t("accounting.rateCard.import.formatsSupported")}
                            </p>
                        </div>
                    </div>

                    {error && (
                        <div className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
                            {error}
                        </div>
                    )}

                    {!!rows.length && (
                        <div className="rounded-md border px-3 py-2 text-sm">
                            {t("accounting.rateCard.import.rowsReady", { count: rows.length })}
                            <p className="text-xs text-muted-foreground mt-1">
                                {t("accounting.rateCard.import.validationHint")}
                            </p>
                        </div>
                    )}

                    {!!warnings.length && (
                        <div className="rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-900">
                            <p className="font-medium">
                                {t("accounting.rateCard.import.warnings", { count: warnings.length })}
                            </p>
                            <ul className="mt-1 max-h-32 overflow-auto text-xs space-y-0.5">
                                {warnings.slice(0, 20).map((w) => (
                                    <li key={w}>{w}</li>
                                ))}
                            </ul>
                        </div>
                    )}
                </div>

                <DialogFooter>
                    <Button variant="outline" onClick={() => onOpenChange(false)} disabled={loading}>
                        {t("accounting.rateCard.import.cancel")}
                    </Button>
                    <Button onClick={handleImport} disabled={!customerId || rows.length === 0 || loading}>
                        {loading ? (
                            <Loader2 className="h-4 w-4 mr-2 animate-spin" />
                        ) : (
                            <Upload className="h-4 w-4 mr-2" />
                        )}
                        {loading
                            ? t("accounting.rateCard.import.uploading")
                            : t("accounting.rateCard.import.upload")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
