"use client";

/**
 * Rate-card import over Go (T42; `POST /v1/billing/rate-entries/import`, Appendix B §B.2.14): the sheet
 * is read in the browser with `xlsx`, loaded on the first file pick (developer-spec.md §10.11), in the
 * layout of the template (column A the source `CODE - name`, C the destination code, D the rate, E the
 * distance); every row of one import shares the customer, the effective date and the job category, and
 * Go writes them as one round (`importId`). Rows with an unknown source or a bad number are skipped and
 * listed. Only `['rateCard','entries',customer]` is refetched afterwards.
 */
import { useMemo, useState } from "react";
import { Loader2, Upload } from "lucide-react";

import { useLanguage } from "@/context/language";
import { apiErrorText } from "@/lib/apiError";
import { CustomerSearchSelect } from "@/components/entity-pickers";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

import { importRateEntries, type JobCategory, type RateRowInput } from "../../../api/rateCard.go";
import { todayKey } from "../shared";

function num(value: unknown): number | null {
    if (typeof value === "number" && Number.isFinite(value)) return value;
    if (typeof value !== "string") return null;
    const s = value.replace(/,/g, "").trim();
    if (!s) return null;
    const n = Number(s);
    return Number.isFinite(n) ? n : null;
}

export interface ParsedRateSheet {
    rows: RateRowInput[];
    warnings: { key: string; params: Record<string, string | number> }[];
}

/** The rows of a template sheet (row 1 is the header), the legacy importer's rules. */
export function parseRateSheet(raw: unknown[][], knownHubs: Set<string>): ParsedRateSheet {
    const rows: RateRowInput[] = [];
    const warnings: ParsedRateSheet["warnings"] = [];
    for (let i = 1; i < raw.length; i++) {
        const r = raw[i] ?? [];
        const rawHubName = String(r[0] ?? "").trim();
        const destinationCode = String(r[2] ?? "").trim().toUpperCase();
        const rate = num(r[3]);
        const distance = num(r[4]);
        if (!rawHubName && !destinationCode && rate == null) continue;
        const hubId = rawHubName.split(" - ")[0].trim().toUpperCase();
        if (!hubId) warnings.push({ key: "accounting.rateCard.import.warningMissingHub", params: { row: i + 1 } });
        else if (!knownHubs.has(hubId)) warnings.push({ key: "accounting.rateCard.import.warningUnknownHub", params: { row: i + 1, hubId } });
        else if (!destinationCode) warnings.push({ key: "accounting.rateCard.import.warningMissingDestination", params: { row: i + 1 } });
        else if (rate == null) warnings.push({ key: "accounting.rateCard.import.warningInvalidRate", params: { row: i + 1 } });
        else rows.push({ hubId, rawHubName, destinationCode, vehicleClass: "4WJ", rateThb: rate, distanceKm: distance ?? undefined });
    }
    return { rows, warnings };
}

export function RateImportDialog({
    open,
    onOpenChange,
    knownHubIds,
    initialCustomerId,
}: {
    open: boolean;
    onOpenChange: (o: boolean) => void;
    knownHubIds: string[];
    initialCustomerId?: string;
}) {
    const { t } = useLanguage();
    const [customerId, setCustomerId] = useState(initialCustomerId ?? "");
    const [effectiveFrom, setEffectiveFrom] = useState(todayKey());
    const [category, setCategory] = useState<JobCategory>("PRIMARY");
    const [parsed, setParsed] = useState<ParsedRateSheet | null>(null);
    const [error, setError] = useState<string | null>(null);
    const [saving, setSaving] = useState(false);
    const known = useMemo(() => new Set(knownHubIds.map((h) => h.toUpperCase())), [knownHubIds]);

    const reset = () => {
        setParsed(null);
        setError(null);
    };
    const parse = async (file: File) => {
        reset();
        try {
            const XLSX = await import("xlsx");
            const wb = XLSX.read(await file.arrayBuffer(), { type: "array" });
            const sheet = wb.SheetNames[0] ? wb.Sheets[wb.SheetNames[0]] : undefined;
            const raw = sheet ? (XLSX.utils.sheet_to_json(sheet, { header: 1, defval: "" }) as unknown[][]) : [];
            if (raw.length < 2) return setError(t("accounting.rateCard.import.emptySheet"));
            const out = parseRateSheet(raw, known);
            if (out.rows.length === 0) setError(t("accounting.rateCard.import.noValidRows"));
            setParsed(out);
        } catch {
            setError(t("accounting.rateCard.import.parseError"));
        }
    };
    const submit = async () => {
        if (!customerId || !parsed || parsed.rows.length === 0) return;
        setSaving(true);
        setError(null);
        try {
            await importRateEntries({ customerId, effectiveFromDateStr: effectiveFrom, rows: parsed.rows.map((r) => ({ ...r, jobCategory: category })) });
            reset();
            onOpenChange(false);
        } catch (e) {
            setError(`${t("accounting.rateCard.import.pushError")} ${apiErrorText(e, t)}`);
        } finally {
            setSaving(false);
        }
    };

    return (
        <Dialog
            open={open}
            onOpenChange={(o) => {
                if (saving) return;
                onOpenChange(o);
                if (!o) reset();
            }}
        >
            <DialogContent className="sm:max-w-2xl">
                <DialogHeader>
                    <DialogTitle>{t("accounting.rateCard.import.title")}</DialogTitle>
                    <DialogDescription>{t("accounting.rateCard.import.validationHint")}</DialogDescription>
                </DialogHeader>
                <div className="grid gap-4 sm:grid-cols-3">
                    <div className="space-y-1 sm:col-span-3">
                        <Label>{t("accounting.rateCard.import.selectCustomer")}</Label>
                        <CustomerSearchSelect
                            aria-label={t("accounting.rateCard.import.selectCustomer")}
                            value={customerId}
                            onValueChange={setCustomerId}
                            placeholder={t("accounting.rateCard.import.selectCustomerPlaceholder")}
                            className="w-full"
                        />
                    </div>
                    <div className="space-y-1">
                        <Label>{t("accounting.rateCard.table.effectiveFrom")}</Label>
                        <Input type="date" value={effectiveFrom} onChange={(e) => setEffectiveFrom(e.target.value)} />
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
                        <Label>{t("accounting.rateCard.import.clickUpload")}</Label>
                        <Input
                            type="file"
                            accept=".xlsx,.xls"
                            disabled={!customerId || saving}
                            onChange={(e) => {
                                const f = e.target.files?.[0];
                                if (f) void parse(f);
                            }}
                        />
                    </div>
                </div>
                {error ? <p className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p> : null}
                {parsed && parsed.rows.length > 0 ? (
                    <p className="rounded-md border px-3 py-2 text-sm">{t("accounting.rateCard.import.rowsReady", { count: parsed.rows.length })}</p>
                ) : null}
                {parsed && parsed.warnings.length > 0 ? (
                    <Alert variant="warning"><AlertDescription>
                        <p className="font-medium">{t("accounting.rateCard.import.warnings", { count: parsed.warnings.length })}</p>
                        <ul className="mt-1 max-h-32 space-y-0.5 overflow-auto text-xs">
                            {parsed.warnings.slice(0, 20).map((w, i) => (
                                <li key={i}>{t(w.key, w.params)}</li>
                            ))}
                        </ul>
                    </AlertDescription></Alert>
                ) : null}
                <DialogFooter>
                    <Button variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
                        {t("accounting.rateCard.import.cancel")}
                    </Button>
                    <Button onClick={() => void submit()} disabled={!customerId || !parsed || parsed.rows.length === 0 || saving}>
                        {saving ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Upload className="mr-2 h-4 w-4" />}
                        {saving ? t("accounting.rateCard.import.uploading") : t("accounting.rateCard.import.upload")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
