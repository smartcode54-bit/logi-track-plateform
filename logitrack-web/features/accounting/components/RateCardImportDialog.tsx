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
import { stepBandRange, type FuelBandStepParams } from "@/lib/billingCompute";
import { isRateMatrixSheet, parseRateMatrixSheet, type ParsedRateMatrix } from "../utils/cjsfRateMatrix";

export interface RateCardCustomerOption {
    id: string;
    code: string;
    name: string;
    /**
     * ADR 0029 — set for a customer priced by base price + fuel band steps (CJSF). Its rate card is
     * imported as BASE prices, and a band-matrix sheet is read through `parseRateMatrixSheet`.
     */
    fuelPricing?: FuelBandStepParams;
}

/** Origin offered by default for a matrix sheet — its own "SPK / 999001" is not a code in our system. */
const DEFAULT_MATRIX_ORIGIN_HUB = "SPK-GW";

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
    const [jobCategory, setJobCategory] = useState<"PRIMARY" | "SUPPLEMENTARY">("PRIMARY");
    const [rows, setRows] = useState<CustomerRateEntryInput[]>([]);
    const [warnings, setWarnings] = useState<string[]>([]);
    // ADR 0029 — a band-matrix sheet (CJSF) and the single origin all of its rows run from.
    const [matrix, setMatrix] = useState<ParsedRateMatrix | null>(null);
    const [originHubId, setOriginHubId] = useState<string>("");
    const [loading, setLoading] = useState(false);
    const [error, setError] = useState<string | null>(null);

    const knownHubSet = useMemo(() => new Set(knownHubIds.map((h) => h.toUpperCase())), [knownHubIds]);
    const selectedCustomer = useMemo(
        () => customers.find((c) => c.id === customerId) ?? null,
        [customers, customerId]
    );
    const fuelPricing = selectedCustomer?.fuelPricing;
    const anchorBand = fuelPricing
        ? stepBandRange(0, fuelPricing.anchorBandLowerThb, fuelPricing.bandWidthThb)
        : null;
    const matrixBlocked = matrix != null && (matrix.mismatches.length > 0 || matrix.conflicts.length > 0);
    const readyCount = matrix ? matrix.rows.length : rows.length;
    const canImport =
        !!customerId && readyCount > 0 && !loading && !matrixBlocked && (!matrix || !!originHubId);

    const clearParsed = () => {
        setRows([]);
        setWarnings([]);
        setMatrix(null);
        setError(null);
    };

    const resetState = () => {
        clearParsed();
        setEffectiveFrom(undefined);
        setJobCategory("PRIMARY");
        setOriginHubId("");
    };

    const parseMatrix = (rawRows: unknown[][], displayRows: unknown[][], params: FuelBandStepParams) => {
        // Prices as the customer's sheet shows them (922.5 formatted `#,##0` → 923) — ADR 0029 §9.
        const parsed = parseRateMatrixSheet(rawRows, params, displayRows);
        if (!parsed) {
            const band = stepBandRange(0, params.anchorBandLowerThb, params.bandWidthThb);
            setError(
                t("accounting.rateCard.import.matrixNoAnchor", {
                    lower: band?.lowerThb.toFixed(2) ?? "",
                    upper: band?.upperThb.toFixed(2) ?? "",
                })
            );
            return;
        }
        setMatrix(parsed);
        setWarnings(
            parsed.warnings.map((w) =>
                w.kind === "missingDestination"
                    ? t("accounting.rateCard.import.matrixWarnMissingDestination", { row: w.sheetRow })
                    : w.kind === "invalidDestination"
                      ? t("accounting.rateCard.import.matrixWarnInvalidDestination", { row: w.sheetRow, value: w.value })
                      : t("accounting.rateCard.import.matrixWarnMissingBase", { row: w.sheetRow })
            )
        );
        if (parsed.rows.length === 0) setError(t("accounting.rateCard.import.noValidRows"));
        setOriginHubId((prev) =>
            prev || (knownHubSet.has(DEFAULT_MATRIX_ORIGIN_HUB) ? DEFAULT_MATRIX_ORIGIN_HUB : "")
        );
    };

    const parseFile = async (file: File) => {
        clearParsed();
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
            if (fuelPricing && isRateMatrixSheet(rawRows)) {
                const displayRows = XLSX.utils.sheet_to_json(sheet, { header: 1, defval: "", raw: false }) as unknown[][];
                parseMatrix(rawRows, displayRows, fuelPricing);
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
                    // ADR 0029 — for a band-step customer the template's price column IS the base price.
                    priceBasis: fuelPricing ? "base" : "final",
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
        if (!canImport) return;
        setLoading(true);
        setError(null);
        try {
            const date = effectiveFrom ?? new Date();
            const sourceRows: CustomerRateEntryInput[] = matrix
                ? matrix.rows.map((r) => ({
                      hubId: originHubId,
                      rawHubName: originHubId,
                      destinationCode: r.destinationCode,
                      vehicleClass: r.vehicleClass,
                      rateThb: r.baseRateThb,
                      priceBasis: "base",
                  }))
                : rows;
            // Tag the whole sheet with the selected หลัก/เสริม category (ADR-0005).
            const taggedRows = sourceRows.map((r) => ({ ...r, jobCategory }));
            await batchCreateCustomerRateEntries(customerId, taggedRows, date);
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
                        <Select
                            value={customerId}
                            onValueChange={(v) => {
                                // A parsed sheet was read under the previous customer's pricing model.
                                setCustomerId(v);
                                clearParsed();
                            }}
                        >
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
                        <Label>{t("accounting.rateCard.import.jobCategory", "ประเภทงาน (หลัก/เสริม)")}</Label>
                        <Select
                            value={jobCategory}
                            onValueChange={(v) => setJobCategory(v === "SUPPLEMENTARY" ? "SUPPLEMENTARY" : "PRIMARY")}
                            disabled={loading}
                        >
                            <SelectTrigger>
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent className="z-[1005]" position="popper">
                                <SelectItem value="PRIMARY">{t("accounting.rateCard.jobCategory.primary", "หลัก")}</SelectItem>
                                <SelectItem value="SUPPLEMENTARY">{t("accounting.rateCard.jobCategory.supplementary", "เสริม")}</SelectItem>
                            </SelectContent>
                        </Select>
                        <p className="text-xs text-muted-foreground">
                            {t("accounting.rateCard.import.jobCategoryHint", "แท็กทั้งชีตเป็นเรตหลักหรือเรตเสริม — เรตเสริมจะถูก freeze ตอนคิดเงิน")}
                        </p>
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
                            {fuelPricing && anchorBand && (
                                <p className="mt-1 text-xs text-blue-700">
                                    {t("accounting.rateCard.import.basePriceNote", {
                                        lower: anchorBand.lowerThb.toFixed(2),
                                        upper: anchorBand.upperThb.toFixed(2),
                                    })}
                                </p>
                            )}
                        </div>
                    </div>

                    {matrix && (
                        <div className="space-y-3 rounded-md border border-blue-200 bg-blue-50/50 p-3 text-sm">
                            <p className="font-medium">
                                {t("accounting.rateCard.import.matrixDetected", {
                                    anchor: anchorBand
                                        ? `${anchorBand.lowerThb.toFixed(2)}–${anchorBand.upperThb.toFixed(2)}`
                                        : "",
                                })}
                            </p>
                            <div className="grid gap-2">
                                <Label>{t("accounting.rateCard.import.matrixOrigin")}</Label>
                                <Select value={originHubId} onValueChange={setOriginHubId} disabled={loading}>
                                    <SelectTrigger>
                                        <SelectValue placeholder={t("accounting.rateCard.import.matrixOriginPlaceholder")} />
                                    </SelectTrigger>
                                    <SelectContent className="z-[1005]" position="popper">
                                        {knownHubIds.map((h) => (
                                            <SelectItem key={h} value={h}>
                                                {h}
                                            </SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                            </div>
                            <p className="text-xs text-muted-foreground">
                                {t("accounting.rateCard.import.matrixSummary", {
                                    rows: matrix.rows.length,
                                    merged: matrix.mergedDuplicates,
                                    bands: matrix.bandColumns.length,
                                })}
                            </p>
                            {!matrixBlocked && fuelPricing && (
                                <p className="text-xs text-emerald-700">
                                    {t("accounting.rateCard.import.matrixVerified", {
                                        pct: String(fuelPricing.stepBasisPoints / 100),
                                        roundTo: String(fuelPricing.roundToThb),
                                    })}
                                </p>
                            )}
                            {matrix.displayedBases.length > 0 && (
                                <p className="text-xs text-muted-foreground">
                                    {t("accounting.rateCard.import.matrixDisplayedBase", {
                                        count: matrix.displayedBases.length,
                                        examples: Array.from(
                                            new Set(
                                                matrix.displayedBases.map(
                                                    (d) => `${d.cellThb.toLocaleString()} → ${d.displayedThb.toLocaleString()}`
                                                )
                                            )
                                        ).join(", "),
                                    })}
                                </p>
                            )}
                            {matrix.rows.length > 0 && (
                                <div className="max-h-40 overflow-auto rounded border bg-background">
                                    <table className="w-full text-xs">
                                        <tbody>
                                            {matrix.rows.map((r) => (
                                                <tr key={`${r.destinationCode}-${r.vehicleClass}`} className="border-b last:border-0">
                                                    <td className="px-2 py-1 font-mono">{r.destinationCode}</td>
                                                    <td className="px-2 py-1">{r.destinationName}</td>
                                                    <td className="px-2 py-1">{r.vehicleClass}</td>
                                                    <td className="px-2 py-1 text-right">{r.baseRateThb.toLocaleString()}</td>
                                                </tr>
                                            ))}
                                        </tbody>
                                    </table>
                                </div>
                            )}
                            {matrix.mismatches.length > 0 && (
                                <div className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-destructive">
                                    <p className="font-medium">
                                        {t("accounting.rateCard.import.matrixMismatches", { count: matrix.mismatches.length })}
                                    </p>
                                    <ul className="mt-1 max-h-32 space-y-0.5 overflow-auto text-xs">
                                        {matrix.mismatches.slice(0, 30).map((m) => (
                                            <li key={`${m.sheetRow}-${m.header}`}>
                                                {t("accounting.rateCard.import.matrixMismatchRow", {
                                                    row: m.sheetRow,
                                                    dest: m.destinationCode,
                                                    band: m.header,
                                                    sheet: m.sheetThb.toLocaleString(),
                                                    computed: m.computedThb.toLocaleString(),
                                                })}
                                            </li>
                                        ))}
                                    </ul>
                                </div>
                            )}
                            {matrix.conflicts.length > 0 && (
                                <div className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-destructive">
                                    <p className="font-medium">
                                        {t("accounting.rateCard.import.matrixConflicts", { count: matrix.conflicts.length })}
                                    </p>
                                    <ul className="mt-1 max-h-32 space-y-0.5 overflow-auto text-xs">
                                        {matrix.conflicts.map((c) => (
                                            <li key={`${c.destinationCode}-${c.vehicleClass}`}>
                                                {t("accounting.rateCard.import.matrixConflictRow", {
                                                    dest: c.destinationCode,
                                                    vehicle: c.vehicleClass,
                                                    rows: c.sheetRows.join(", "),
                                                    prices: c.pricesThb.map((p) => p.toLocaleString()).join(" / "),
                                                })}
                                            </li>
                                        ))}
                                    </ul>
                                </div>
                            )}
                        </div>
                    )}

                    {error && (
                        <div className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
                            {error}
                        </div>
                    )}

                    {!matrix && !!rows.length && (
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
                    <Button onClick={handleImport} disabled={!canImport}>
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
