"use client";

/**
 * The fuel adjustments tab (T42): `['rateCard','fuelAdj',cust]` over `GET /v1/billing/fuel-adjustments`.
 * An announcement is a percent of the base rate, a fixed amount per trip, or a band derived from the
 * reference diesel price (`POST`, Go computes the stored multiplier and add, main spec §6.6); the
 * reference price defaults to the Bangchak snapshot of the effective date (`['fuel','daily',dayKey]`,
 * ADR 0009 §5). Rounds are void-only (`POST .../{id}/void`); "announce like this" starts a new round from
 * an old one. Writes refetch only this customer's key and the `all` view.
 */
import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Ban, Copy, Loader2, Plus } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { apiErrorText } from "@/lib/apiError";
import { pickFleetBangchakDieselReference } from "@/lib/bangchakFleetReferenceFuel";
import { CustomerSearchSelect } from "@/components/entity-pickers";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

import { fuelDayQuery } from "../../../api/fuel.go";
import {
    ALL_CUSTOMERS,
    createFuelAdjustment,
    fuelAdjustmentsQuery,
    multiplierPercentText,
    voidFuelAdjustment,
    type FuelAdjustmentDTO,
    type FuelAdjustmentInput,
} from "../../../api/rateCard.go";
import { useCustomerLookup } from "../../../api/useCustomerLookup";
import { ApiErrorAlert, CenteredSpinner, formatDateTime, formatDay, formatThb, todayKey } from "../shared";
import { VoidDialog } from "./VoidDialog";

type Mode = FuelAdjustmentInput["mode"];

interface Draft {
    customerId: string;
    effectiveFrom: string;
    mode: Mode;
    pct: string;
    add: string;
    reference: string;
    baseline: string;
    perBaht: string;
    note: string;
}

function draftFrom(row: FuelAdjustmentDTO | null, customerId: string): Draft {
    if (!row) {
        return { customerId: customerId === ALL_CUSTOMERS ? "" : customerId, effectiveFrom: todayKey(), mode: "percent", pct: "0", add: "0", reference: "", baseline: "41", perBaht: "10", note: "" };
    }
    const mode: Mode = row.fuelBandEnabled ? "band" : row.rateMultiplier !== 1 ? "percent" : "fixed_thb";
    return {
        customerId: row.customerId,
        effectiveFrom: todayKey(),
        mode,
        pct: String(Math.round((row.rateMultiplier - 1) * 1_000_000) / 10_000),
        add: String(row.addThbPerTrip),
        reference: row.referenceFuelPriceThbPerLitre != null ? String(row.referenceFuelPriceThbPerLitre) : "",
        baseline: String(row.fuelBandBaselineFuelFloor ?? 41),
        perBaht: String(row.fuelBandThbPerBaht ?? 10),
        note: row.announcementNote,
    };
}

/** The request of a draft (`null` when it is incomplete). */
export function fuelAdjustmentInput(d: Draft): FuelAdjustmentInput | null {
    if (!d.customerId || !d.effectiveFrom) return null;
    const n = (v: string) => (v.trim() === "" ? null : Number(v));
    const reference = n(d.reference);
    const base: FuelAdjustmentInput = {
        customerId: d.customerId,
        effectiveFromDateStr: d.effectiveFrom,
        mode: d.mode,
        referenceFuelPriceThbPerLitre: reference,
        announcementNote: d.note.trim(),
    };
    if (d.mode === "percent") {
        const pct = n(d.pct);
        return pct == null || !Number.isFinite(pct) ? null : { ...base, pct };
    }
    if (d.mode === "fixed_thb") {
        const add = n(d.add);
        return add == null || !Number.isFinite(add) ? null : { ...base, addThbPerTrip: add };
    }
    if (reference == null || !Number.isFinite(reference)) return null;
    return { ...base, fuelBandBaselineFuelFloor: n(d.baseline), fuelBandThbPerBaht: n(d.perBaht) };
}

function AdjustmentDialog({ initial, onClose }: { initial: Draft; onClose: () => void }) {
    const { t } = useLanguage();
    const [d, setD] = useState<Draft>(initial);
    const [saving, setSaving] = useState(false);
    const day = useQuery(fuelDayQuery(d.effectiveFrom));
    const diesel = day.data ? pickFleetBangchakDieselReference(day.data.items) : undefined;
    // The reference price is the effective date's snapshot until the user types one.
    const [touched, setTouched] = useState(initial.reference !== "");
    const effective: Draft = !touched && diesel ? { ...d, reference: String(diesel.price) } : d;
    const input = fuelAdjustmentInput(effective);
    const set = (k: keyof Draft) => (e: { target: { value: string } }) => setD((x) => ({ ...x, [k]: e.target.value }));
    const save = async () => {
        if (!input) return;
        setSaving(true);
        try {
            await createFuelAdjustment(input);
            onClose();
        } catch (e) {
            toast.error(`${t("accounting.rateCard.fuelAdjustments.form.error")} ${apiErrorText(e, t)}`);
        } finally {
            setSaving(false);
        }
    };
    return (
        <Dialog open onOpenChange={(o) => !o && !saving && onClose()}>
            <DialogContent className="sm:max-w-xl">
                <DialogHeader>
                    <DialogTitle>{t("accounting.rateCard.fuelAdjustments.dialog.createTitle")}</DialogTitle>
                    <DialogDescription>{t("accounting.rateCard.fuelAdjustments.dialog.description")}</DialogDescription>
                </DialogHeader>
                <div className="grid grid-cols-2 gap-3">
                    <div className="col-span-2 space-y-1">
                        <Label>{t("accounting.rateCard.fuelAdjustments.form.customer")}</Label>
                        <CustomerSearchSelect
                            aria-label={t("accounting.rateCard.fuelAdjustments.form.customer")}
                            value={d.customerId}
                            onValueChange={(v) => setD((x) => ({ ...x, customerId: v }))}
                            className="w-full"
                        />
                    </div>
                    <div className="space-y-1">
                        <Label>{t("accounting.rateCard.fuelAdjustments.form.effectiveFrom")}</Label>
                        <Input type="date" value={d.effectiveFrom} onChange={set("effectiveFrom")} />
                    </div>
                    <div className="space-y-1">
                        <Label>{t("accounting.rateCard.fuelAdjustments.form.adjustmentTypeLabel")}</Label>
                        <div className="flex flex-wrap gap-1">
                            {(["percent", "fixed_thb", "band"] as const).map((m) => (
                                <Button key={m} type="button" size="sm" variant={d.mode === m ? "default" : "outline"} onClick={() => setD((x) => ({ ...x, mode: m }))}>
                                    {m === "percent"
                                        ? t("accounting.rateCard.fuelAdjustments.form.adjustmentTypePercent")
                                        : m === "fixed_thb"
                                          ? t("accounting.rateCard.fuelAdjustments.form.adjustmentTypeFixed")
                                          : t("accounting.rateCard.fuelAdjustments.form.fuelBandToggle")}
                                </Button>
                            ))}
                        </div>
                    </div>
                    {d.mode === "percent" ? (
                        <div className="space-y-1">
                            <Label>{t("accounting.rateCard.fuelAdjustments.form.multiplier")}</Label>
                            <Input type="number" step="any" value={d.pct} onChange={set("pct")} placeholder={t("accounting.rateCard.fuelAdjustments.form.multiplierPlaceholder")} />
                        </div>
                    ) : null}
                    {d.mode === "fixed_thb" ? (
                        <div className="space-y-1">
                            <Label>{t("accounting.rateCard.fuelAdjustments.form.addPerTrip")}</Label>
                            <Input type="number" step="any" value={d.add} onChange={set("add")} />
                        </div>
                    ) : null}
                    {d.mode === "band" ? (
                        <>
                            <div className="space-y-1">
                                <Label>{t("accounting.rateCard.fuelAdjustments.form.fuelBandBaseline")}</Label>
                                <Input type="number" step="1" value={d.baseline} onChange={set("baseline")} />
                            </div>
                            <div className="space-y-1">
                                <Label>{t("accounting.rateCard.fuelAdjustments.form.fuelBandThbPerBaht")}</Label>
                                <Input type="number" step="any" value={d.perBaht} onChange={set("perBaht")} />
                            </div>
                        </>
                    ) : null}
                    <div className="space-y-1">
                        <Label>{t("accounting.rateCard.fuelAdjustments.form.referenceFuelPrice")}</Label>
                        <Input
                            type="number"
                            step="any"
                            value={effective.reference}
                            onChange={(e) => {
                                setTouched(true);
                                set("reference")(e);
                            }}
                        />
                        <p className="text-xs text-muted-foreground">
                            {day.isFetching
                                ? t("accounting.rateCard.fuelAdjustments.form.dailyPriceLoading")
                                : diesel
                                  ? t("accounting.rateCard.fuelAdjustments.form.dailyPriceFound", { date: formatDay(d.effectiveFrom), price: diesel.price })
                                  : t("accounting.rateCard.fuelAdjustments.form.dailyPriceMissing", { date: formatDay(d.effectiveFrom) })}
                        </p>
                    </div>
                    <div className="col-span-2 space-y-1">
                        <Label>{t("accounting.rateCard.fuelAdjustments.form.note")}</Label>
                        <Input value={d.note} onChange={set("note")} placeholder={t("accounting.rateCard.fuelAdjustments.form.notePlaceholder")} />
                    </div>
                </div>
                {!input ? <p className="text-xs text-muted-foreground">{t("accounting.rateCard.fuelAdjustments.form.invalid")}</p> : null}
                <DialogFooter>
                    <Button variant="outline" onClick={onClose} disabled={saving}>
                        {t("accounting.rateCard.fuelAdjustments.dialog.cancel")}
                    </Button>
                    <Button onClick={() => void save()} disabled={!input || saving}>
                        {saving ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                        {saving ? t("accounting.rateCard.fuelAdjustments.form.saving") : t("accounting.rateCard.fuelAdjustments.form.save")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}

export function FuelAdjustmentsTab({ customerId, canEdit }: { customerId: string; canEdit: boolean }) {
    const { t } = useLanguage();
    const list = useQuery(fuelAdjustmentsQuery(customerId));
    const [draft, setDraft] = useState<Draft | null>(null);
    const [voiding, setVoiding] = useState<FuelAdjustmentDTO | null>(null);
    const rows = useMemo(() => [...(list.data ?? [])].sort((a, b) => b.effectiveFromDateStr.localeCompare(a.effectiveFromDateStr)), [list.data]);
    const customers = useCustomerLookup(rows.map((r) => r.customerId));

    return (
        <div className="space-y-4">
            <div className="flex items-center justify-between gap-2">
                <p className="text-sm text-muted-foreground">{t("accounting.rateCard.fuelAdjustments.subtitle")}</p>
                {canEdit ? (
                    <Button size="sm" onClick={() => setDraft(draftFrom(null, customerId))}>
                        <Plus className="mr-2 h-4 w-4" />
                        {t("accounting.rateCard.fuelAdjustments.addRule")}
                    </Button>
                ) : null}
            </div>
            <ApiErrorAlert error={list.error} />
            {list.isPending ? (
                <CenteredSpinner />
            ) : rows.length === 0 ? (
                <p className="py-8 text-center text-sm text-muted-foreground">{t("accounting.rateCard.fuelAdjustments.noRows")}</p>
            ) : (
                <div className="overflow-x-auto rounded-md border">
                    <Table>
                        <TableHeader>
                            <TableRow>
                                <TableHead>{t("accounting.rateCard.fuelAdjustments.table.customer")}</TableHead>
                                <TableHead>{t("accounting.rateCard.fuelAdjustments.table.effectiveFrom")}</TableHead>
                                <TableHead className="text-right">{t("accounting.rateCard.fuelAdjustments.table.multiplier")}</TableHead>
                                <TableHead className="text-right">{t("accounting.rateCard.fuelAdjustments.table.addPerTrip")}</TableHead>
                                <TableHead className="text-right">{t("accounting.rateCard.fuelAdjustments.table.referencePrice")}</TableHead>
                                <TableHead>{t("accounting.rateCard.fuelAdjustments.form.note")}</TableHead>
                                <TableHead>{t("accounting.rateCard.fuelAdjustments.table.createdAt")}</TableHead>
                                <TableHead>{t("accounting.rateCard.fuelAdjustments.table.actions")}</TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {rows.map((r) => (
                                <TableRow key={r.id} className={r.voided ? "opacity-60" : undefined}>
                                    <TableCell className="text-xs">{customers.label(r.customerId)}</TableCell>
                                    <TableCell className="whitespace-nowrap text-xs">{formatDay(r.effectiveFromDateStr)}</TableCell>
                                    <TableCell className="text-right font-mono text-xs">{r.rateMultiplier !== 1 ? multiplierPercentText(r.rateMultiplier) : "—"}</TableCell>
                                    <TableCell className="text-right font-mono text-xs">
                                        {r.addThbPerTrip !== 0 ? formatThb(r.addThbPerTrip) : "—"}
                                        {r.fuelBandEnabled ? (
                                            <Badge variant="outline" className="ml-1 px-1 py-0 text-[10px]">
                                                {t("accounting.rateCard.fuelAdjustments.table.bandBadge")}
                                            </Badge>
                                        ) : null}
                                    </TableCell>
                                    <TableCell className="text-right font-mono text-xs">
                                        {r.referenceFuelPriceThbPerLitre != null ? r.referenceFuelPriceThbPerLitre.toFixed(2) : "—"}
                                        {r.fuelBand ? <span className="block text-[10px] text-muted-foreground">{`${r.fuelBand.lowerThb.toFixed(2)}–${r.fuelBand.upperThb.toFixed(2)}`}</span> : null}
                                    </TableCell>
                                    <TableCell className="max-w-[200px] truncate text-xs">{r.announcementNote || "—"}</TableCell>
                                    <TableCell className="whitespace-nowrap text-xs">{formatDateTime(r.createdAt)}</TableCell>
                                    <TableCell>
                                        {r.voided ? (
                                            <Badge variant="destructive" title={r.voidedReason ?? undefined}>
                                                {t("accounting.rateCard.void.badge")}
                                            </Badge>
                                        ) : canEdit ? (
                                            <div className="flex gap-1">
                                                <Button size="sm" variant="ghost" className="h-7 px-2 text-xs" onClick={() => setDraft(draftFrom(r, customerId))} title={t("accounting.rateCard.entry.announceCorrection")}>
                                                    <Copy className="h-3 w-3" />
                                                </Button>
                                                <Button size="sm" variant="ghost" className="h-7 px-2 text-xs" onClick={() => setVoiding(r)}>
                                                    <Ban className="mr-1 h-3 w-3" />
                                                    {t("accounting.rateCard.void.action")}
                                                </Button>
                                            </div>
                                        ) : null}
                                    </TableCell>
                                </TableRow>
                            ))}
                        </TableBody>
                    </Table>
                </div>
            )}
            {draft ? <AdjustmentDialog initial={draft} onClose={() => setDraft(null)} /> : null}
            {voiding ? (
                <VoidDialog
                    label={`${customers.label(voiding.customerId)} · ${voiding.effectiveFromDateStr}`}
                    onVoid={(reason) => voidFuelAdjustment(voiding, reason)}
                    onClose={() => setVoiding(null)}
                />
            ) : null}
        </div>
    );
}
