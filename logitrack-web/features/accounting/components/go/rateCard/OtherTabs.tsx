"use client";

/**
 * The rate card's small tables (T42): service fees (`['rateCard','serviceFees']`, edit and delete),
 * standby rates (`['rateCard','standby']`, edit and void) and the monthly Bangchak record
 * (`['fuel','monthly']`, plus the steward-only `bangchak.snapshot` job). Each list is read once for every
 * customer in reach and narrowed to the selected customer here; a write refetches only its own key.
 */
import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Ban, Loader2, Pencil, Plus, RefreshCw, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { isJobFinished, useJob } from "@/features/jobs/api/jobs";
import { apiErrorText } from "@/lib/apiError";
import { pickFleetBangchakDieselReference } from "@/lib/bangchakFleetReferenceFuel";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

import { fuelMonthlyQuery, startBangchakSnapshot } from "../../../api/fuel.go";
import { customerLabel, type CustomerPick } from "../../../api/pickers.go";
import {
    ALL_CUSTOMERS,
    createServiceFee,
    createStandbyRate,
    deleteServiceFee,
    patchServiceFee,
    patchStandbyRate,
    serviceFeesQuery,
    standbyRatesQuery,
    voidStandbyRate,
    type ServiceFeeDTO,
    type StandbyRateDTO,
} from "../../../api/rateCard.go";
import { ApiErrorAlert, CenteredSpinner, CustomerSelect, formatDateTime, formatDay, formatThb, todayKey } from "../shared";
import { VoidDialog } from "./VoidDialog";

const FEE_TYPES = ["extra_stop", "waiting_time", "special_handling", "service_charge", "standby", "custom"] as const;
const FEE_UNITS = ["per_trip", "per_stop"] as const;

function ServiceFeeDialog({ fee, customerId, customers, onClose }: { fee: ServiceFeeDTO | null; customerId: string; customers: CustomerPick[]; onClose: () => void }) {
    const { t } = useLanguage();
    const [cust, setCust] = useState(fee?.customerId ?? (customerId === ALL_CUSTOMERS ? "" : customerId));
    const [feeType, setFeeType] = useState<string>(fee?.feeType ?? "extra_stop");
    const [custom, setCustom] = useState(fee?.customTypeName ?? "");
    const [amount, setAmount] = useState(fee ? String(fee.amountThb) : "");
    const [unit, setUnit] = useState<string>(fee?.unit ?? "per_stop");
    const [note, setNote] = useState(fee?.note ?? "");
    const [saving, setSaving] = useState(false);
    const valid = cust && feeType && amount.trim() !== "" && Number(amount) >= 0 && (feeType !== "custom" || custom.trim());
    const save = async () => {
        if (!valid) return;
        setSaving(true);
        const body = { feeType, customTypeName: feeType === "custom" ? custom.trim() : null, amountThb: Number(amount), unit, note: note.trim() || null };
        try {
            if (fee) await patchServiceFee(fee.id, body);
            else await createServiceFee({ customerId: cust, ...body });
            onClose();
        } catch (e) {
            toast.error(`${t("accounting.serviceFees.form.error")} ${apiErrorText(e, t)}`);
        } finally {
            setSaving(false);
        }
    };
    return (
        <Dialog open onOpenChange={(o) => !o && !saving && onClose()}>
            <DialogContent className="sm:max-w-md">
                <DialogHeader>
                    <DialogTitle>{fee ? t("accounting.serviceFees.dialog.edit") : t("accounting.serviceFees.dialog.create")}</DialogTitle>
                </DialogHeader>
                <div className="space-y-3">
                    <div className="space-y-1">
                        <Label>{t("accounting.serviceFees.form.customer")}</Label>
                        <CustomerSelect value={cust} onChange={setCust} customers={customers} disabled={!!fee} className="w-full" />
                    </div>
                    <div className="grid grid-cols-2 gap-3">
                        <div className="space-y-1">
                            <Label>{t("accounting.serviceFees.form.feeType")}</Label>
                            <Select value={feeType} onValueChange={setFeeType}>
                                <SelectTrigger>
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    {FEE_TYPES.map((f) => (
                                        <SelectItem key={f} value={f}>
                                            {t(`accounting.serviceFees.type.${f}`)}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                        <div className="space-y-1">
                            <Label>{t("accounting.serviceFees.form.unit")}</Label>
                            <Select value={unit} onValueChange={setUnit}>
                                <SelectTrigger>
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    {FEE_UNITS.map((u) => (
                                        <SelectItem key={u} value={u}>
                                            {t(`accounting.serviceFees.unit.${u}`)}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                    </div>
                    {feeType === "custom" ? (
                        <div className="space-y-1">
                            <Label>{t("accounting.serviceFees.form.customTypeName")}</Label>
                            <Input value={custom} onChange={(e) => setCustom(e.target.value)} />
                        </div>
                    ) : null}
                    <div className="space-y-1">
                        <Label>{t("accounting.serviceFees.form.amount")}</Label>
                        <Input type="number" step="any" min="0" value={amount} onChange={(e) => setAmount(e.target.value)} />
                    </div>
                    <div className="space-y-1">
                        <Label>{t("accounting.serviceFees.form.note")}</Label>
                        <Input value={note} onChange={(e) => setNote(e.target.value)} />
                    </div>
                    {!valid ? <p className="text-xs text-muted-foreground">{t("accounting.serviceFees.form.invalid")}</p> : null}
                </div>
                <DialogFooter>
                    <Button variant="outline" onClick={onClose} disabled={saving}>
                        {t("accounting.serviceFees.form.cancel")}
                    </Button>
                    <Button onClick={() => void save()} disabled={!valid || saving}>
                        {saving ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                        {fee ? t("accounting.serviceFees.form.update") : t("accounting.serviceFees.form.save")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}

export function ServiceFeesTab({ customerId, customers, canEdit }: { customerId: string; customers: CustomerPick[]; canEdit: boolean }) {
    const { t } = useLanguage();
    const list = useQuery(serviceFeesQuery());
    const byId = useMemo(() => new Map(customers.map((c) => [c.id, c])), [customers]);
    const [editing, setEditing] = useState<ServiceFeeDTO | "new" | null>(null);
    const [deleting, setDeleting] = useState<string | null>(null);
    const rows = (list.data ?? []).filter((f) => customerId === ALL_CUSTOMERS || f.customerId === customerId);
    const remove = async (f: ServiceFeeDTO) => {
        if (!window.confirm(t("accounting.serviceFees.confirmDelete"))) return;
        setDeleting(f.id);
        try {
            await deleteServiceFee(f);
        } catch (e) {
            toast.error(`${t("accounting.serviceFees.deleteError")} ${apiErrorText(e, t)}`);
        } finally {
            setDeleting(null);
        }
    };
    return (
        <div className="space-y-4">
            <div className="flex items-center justify-between gap-2">
                <p className="text-sm text-muted-foreground">{t("accounting.serviceFees.subtitle")}</p>
                {canEdit ? (
                    <Button size="sm" onClick={() => setEditing("new")}>
                        <Plus className="mr-2 h-4 w-4" />
                        {t("accounting.serviceFees.addButton")}
                    </Button>
                ) : null}
            </div>
            <ApiErrorAlert error={list.error} />
            {list.isPending ? (
                <CenteredSpinner />
            ) : rows.length === 0 ? (
                <p className="py-8 text-center text-sm text-muted-foreground">{t("accounting.serviceFees.noRows")}</p>
            ) : (
                <div className="overflow-x-auto rounded-md border">
                    <Table>
                        <TableHeader>
                            <TableRow>
                                <TableHead>{t("accounting.serviceFees.table.customer")}</TableHead>
                                <TableHead>{t("accounting.serviceFees.table.feeType")}</TableHead>
                                <TableHead>{t("accounting.serviceFees.table.unit")}</TableHead>
                                <TableHead className="text-right">{t("accounting.serviceFees.table.amount")}</TableHead>
                                <TableHead>{t("accounting.serviceFees.table.note")}</TableHead>
                                <TableHead>{t("accounting.serviceFees.table.actions")}</TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {rows.map((f) => (
                                <TableRow key={f.id}>
                                    <TableCell className="text-xs">{customerLabel(byId.get(f.customerId)) || f.customerId}</TableCell>
                                    <TableCell className="text-xs">{f.feeType === "custom" ? f.customTypeName : t(`accounting.serviceFees.type.${f.feeType}`, f.feeType)}</TableCell>
                                    <TableCell className="text-xs">{t(`accounting.serviceFees.unit.${f.unit}`, f.unit)}</TableCell>
                                    <TableCell className="text-right font-mono">{formatThb(f.amountThb)}</TableCell>
                                    <TableCell className="max-w-[220px] truncate text-xs">{f.note ?? "—"}</TableCell>
                                    <TableCell>
                                        {canEdit ? (
                                            <div className="flex gap-1">
                                                <Button size="sm" variant="ghost" className="h-7 px-2" aria-label={t("accounting.serviceFees.table.edit")} onClick={() => setEditing(f)}>
                                                    <Pencil className="h-3 w-3" />
                                                </Button>
                                                <Button size="sm" variant="ghost" className="h-7 px-2 text-destructive" aria-label={t("accounting.serviceFees.table.delete")} disabled={deleting !== null} onClick={() => void remove(f)}>
                                                    {deleting === f.id ? <Loader2 className="h-3 w-3 animate-spin" /> : <Trash2 className="h-3 w-3" />}
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
            {editing ? <ServiceFeeDialog fee={editing === "new" ? null : editing} customerId={customerId} customers={customers} onClose={() => setEditing(null)} /> : null}
        </div>
    );
}

function StandbyRateDialog({ rate, customerId, customers, onClose }: { rate: StandbyRateDTO | null; customerId: string; customers: CustomerPick[]; onClose: () => void }) {
    const { t } = useLanguage();
    const [cust, setCust] = useState(rate?.customerId ?? (customerId === ALL_CUSTOMERS ? "" : customerId));
    const [amount, setAmount] = useState(rate ? String(rate.rateThb) : "");
    const [effectiveFrom, setEffectiveFrom] = useState(rate?.effectiveFromDateStr ?? todayKey());
    const [note, setNote] = useState(rate?.note ?? "");
    const [saving, setSaving] = useState(false);
    const valid = cust && amount.trim() !== "" && Number(amount) >= 0 && effectiveFrom;
    const save = async () => {
        if (!valid) return;
        setSaving(true);
        const body = { rateThb: Number(amount), effectiveFromDateStr: effectiveFrom, note: note.trim() || null };
        try {
            if (rate) await patchStandbyRate(rate.id, body);
            else await createStandbyRate({ customerId: cust, ...body });
            onClose();
        } catch (e) {
            toast.error(apiErrorText(e, t));
        } finally {
            setSaving(false);
        }
    };
    return (
        <Dialog open onOpenChange={(o) => !o && !saving && onClose()}>
            <DialogContent className="sm:max-w-md">
                <DialogHeader>
                    <DialogTitle>{rate ? t("accounting.standbyRates.dialog.editTitle") : t("accounting.standbyRates.dialog.addTitle")}</DialogTitle>
                    <DialogDescription>{t("accounting.standbyRates.dialog.description")}</DialogDescription>
                </DialogHeader>
                <div className="space-y-3">
                    <div className="space-y-1">
                        <Label>{t("accounting.standbyRates.form.customer")}</Label>
                        <CustomerSelect value={cust} onChange={setCust} customers={customers} disabled={!!rate} placeholder={t("accounting.standbyRates.form.customerPlaceholder")} className="w-full" />
                    </div>
                    <div className="grid grid-cols-2 gap-3">
                        <div className="space-y-1">
                            <Label>{t("accounting.standbyRates.form.rate")}</Label>
                            <Input type="number" step="any" min="0" value={amount} onChange={(e) => setAmount(e.target.value)} />
                        </div>
                        <div className="space-y-1">
                            <Label>{t("accounting.standbyRates.form.effectiveFrom")}</Label>
                            <Input type="date" value={effectiveFrom} onChange={(e) => setEffectiveFrom(e.target.value)} />
                        </div>
                    </div>
                    <div className="space-y-1">
                        <Label>{t("accounting.standbyRates.form.note")}</Label>
                        <Input value={note} onChange={(e) => setNote(e.target.value)} placeholder={t("accounting.standbyRates.form.notePlaceholder")} />
                    </div>
                </div>
                <DialogFooter>
                    <Button variant="outline" onClick={onClose} disabled={saving}>
                        {t("common.cancel")}
                    </Button>
                    <Button onClick={() => void save()} disabled={!valid || saving}>
                        {saving ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                        {t("common.save")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}

export function StandbyRatesTab({ customerId, customers, canEdit }: { customerId: string; customers: CustomerPick[]; canEdit: boolean }) {
    const { t } = useLanguage();
    const list = useQuery(standbyRatesQuery());
    const byId = useMemo(() => new Map(customers.map((c) => [c.id, c])), [customers]);
    const [editing, setEditing] = useState<StandbyRateDTO | "new" | null>(null);
    const [voiding, setVoiding] = useState<StandbyRateDTO | null>(null);
    const rows = (list.data ?? [])
        .filter((r) => customerId === ALL_CUSTOMERS || r.customerId === customerId)
        .sort((a, b) => b.effectiveFromDateStr.localeCompare(a.effectiveFromDateStr));
    return (
        <div className="space-y-4">
            <div className="flex items-center justify-between gap-2">
                <p className="text-sm text-muted-foreground">{t("accounting.standbyRates.subtitle")}</p>
                {canEdit ? (
                    <Button size="sm" onClick={() => setEditing("new")}>
                        <Plus className="mr-2 h-4 w-4" />
                        {t("accounting.standbyRates.addButton")}
                    </Button>
                ) : null}
            </div>
            <ApiErrorAlert error={list.error} />
            {list.isPending ? (
                <CenteredSpinner />
            ) : rows.length === 0 ? (
                <p className="py-8 text-center text-sm text-muted-foreground">{t("accounting.standbyRates.noRows")}</p>
            ) : (
                <div className="overflow-x-auto rounded-md border">
                    <Table>
                        <TableHeader>
                            <TableRow>
                                <TableHead>{t("accounting.standbyRates.table.customer")}</TableHead>
                                <TableHead className="text-right">{t("accounting.standbyRates.table.rate")}</TableHead>
                                <TableHead>{t("accounting.standbyRates.table.effectiveFrom")}</TableHead>
                                <TableHead>{t("accounting.standbyRates.table.note")}</TableHead>
                                <TableHead>{t("accounting.standbyRates.table.actions")}</TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {rows.map((r) => (
                                <TableRow key={r.id} className={r.voided ? "opacity-60" : undefined}>
                                    <TableCell className="text-xs">{customerLabel(byId.get(r.customerId)) || r.customerId}</TableCell>
                                    <TableCell className="text-right font-mono">{formatThb(r.rateThb)}</TableCell>
                                    <TableCell className="text-xs">{formatDay(r.effectiveFromDateStr)}</TableCell>
                                    <TableCell className="max-w-[220px] truncate text-xs">{r.note ?? "—"}</TableCell>
                                    <TableCell>
                                        {r.voided ? (
                                            <Badge variant="destructive" title={r.voidedReason ?? undefined}>
                                                {t("accounting.rateCard.void.badge")}
                                            </Badge>
                                        ) : canEdit ? (
                                            <div className="flex gap-1">
                                                <Button size="sm" variant="ghost" className="h-7 px-2" aria-label={t("accounting.standbyRates.table.edit")} onClick={() => setEditing(r)}>
                                                    <Pencil className="h-3 w-3" />
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
            {editing ? <StandbyRateDialog rate={editing === "new" ? null : editing} customerId={customerId} customers={customers} onClose={() => setEditing(null)} /> : null}
            {voiding ? (
                <VoidDialog
                    label={`${customerLabel(byId.get(voiding.customerId))} · ${formatThb(voiding.rateThb)} · ${voiding.effectiveFromDateStr}`}
                    onVoid={(reason) => voidStandbyRate(voiding, reason)}
                    onClose={() => setVoiding(null)}
                />
            ) : null}
        </div>
    );
}

export function FuelMonthlyTab() {
    const { t } = useLanguage();
    const list = useQuery(fuelMonthlyQuery);
    const [jobId, setJobId] = useState<string | null>(null);
    const [starting, setStarting] = useState(false);
    const job = useJob(jobId);
    const running = starting || (jobId !== null && !isJobFinished(job.data));
    const sync = async () => {
        setStarting(true);
        try {
            const out = await startBangchakSnapshot();
            setJobId(out.jobId);
        } catch (e) {
            toast.error(`${t("accounting.rateCard.fuelMonthly.syncFailed")} ${apiErrorText(e, t)}`);
        } finally {
            setStarting(false);
        }
    };
    return (
        <div className="space-y-4">
            <div className="flex items-center justify-between gap-2">
                <p className="text-sm text-muted-foreground">{t("accounting.rateCard.fuelMonthly.subtitle")}</p>
                <Button size="sm" variant="outline" onClick={() => void sync()} disabled={running}>
                    {running ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <RefreshCw className="mr-2 h-4 w-4" />}
                    {t("accounting.rateCard.fuelMonthly.syncButton")}
                </Button>
            </div>
            {job.data?.status === "failed" ? <p className="text-sm text-destructive">{`${t("accounting.rateCard.fuelMonthly.syncFailed")} ${job.data.error ?? ""}`}</p> : null}
            <ApiErrorAlert error={list.error} />
            {list.isPending ? (
                <CenteredSpinner />
            ) : (list.data ?? []).length === 0 ? (
                <p className="py-8 text-center text-sm text-muted-foreground">{t("accounting.rateCard.fuelMonthly.noRows")}</p>
            ) : (
                <div className="overflow-x-auto rounded-md border">
                    <Table>
                        <TableHeader>
                            <TableRow>
                                <TableHead>{t("accounting.rateCard.fuelMonthly.table.period")}</TableHead>
                                <TableHead>{t("accounting.rateCard.fuelMonthly.table.status")}</TableHead>
                                <TableHead>{t("accounting.rateCard.fuelMonthly.table.dieselRetail")}</TableHead>
                                <TableHead className="text-right">{t("accounting.rateCard.fuelMonthly.table.fuelTypesCount")}</TableHead>
                                <TableHead>{t("accounting.rateCard.fuelMonthly.table.savedAt")}</TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {(list.data ?? []).map((m) => {
                                const diesel = pickFleetBangchakDieselReference(m.items ?? []);
                                return (
                                    <TableRow key={m.monthKey}>
                                        <TableCell className="font-mono text-xs">{m.monthKey}</TableCell>
                                        <TableCell>
                                            {m.status === "ok" ? (
                                                <Badge variant="secondary">{t("accounting.rateCard.fuelMonthly.table.statusOk")}</Badge>
                                            ) : (
                                                <Badge variant="destructive" title={m.errorMessage ?? undefined}>
                                                    {t("accounting.rateCard.fuelMonthly.table.statusError")}
                                                </Badge>
                                            )}
                                        </TableCell>
                                        <TableCell className="text-xs">{diesel ? `${diesel.nameTh || diesel.nameEn} · ฿${diesel.price.toLocaleString()}` : "—"}</TableCell>
                                        <TableCell className="text-right text-xs">{(m.items ?? []).length}</TableCell>
                                        <TableCell className="whitespace-nowrap text-xs">{formatDateTime(m.updatedAt ?? m.capturedAt)}</TableCell>
                                    </TableRow>
                                );
                            })}
                        </TableBody>
                    </Table>
                </div>
            )}
        </div>
    );
}
