"use client";

/**
 * Compensation config over Go (T46, issue #66; Appendix E §E.2.2), rendered while the `hr` web flag is on.
 * The form starts from the version in effect now (`['compConfig','active']`, `GET /v1/payroll/config?at=`),
 * or the schema defaults for a tenant without one; "Save new version" appends a version
 * (`POST /v1/payroll/config`, append-only: a run uses the version in effect on the month's last Bangkok
 * day). The history is `['compConfig','all']`. Both reads run in parallel; Go validates every field
 * (whole baht, unique penalty codes) and the page lists the fields it refused.
 */
import { useState, type ReactNode } from "react";
import { format } from "date-fns";
import { Loader2, Plus, Save, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { useQuery } from "@tanstack/react-query";

import { useLanguage } from "@/context/language";
import { formatThb } from "@/features/accounting/components/go/shared";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

import {
    activeCompConfigQuery,
    allCompConfigsQuery,
    editableConfig,
    useCreateCompConfig,
    type CompConfigDTO,
    type CompConfigInput,
    type PenaltyTypeDTO,
} from "../../api/compConfig.go";
import { HrErrorAlert, dayLabel } from "./hrShared";

type Editable = Omit<CompConfigInput, "effectiveFrom">;

export function PayrollConfigPage() {
    const { t } = useLanguage();
    const active = useQuery(activeCompConfigQuery);
    const all = useQuery(allCompConfigsQuery);

    if (active.isPending) {
        return (
            <div className="flex items-center justify-center p-12" role="status" aria-busy="true">
                <Loader2 className="h-6 w-6 animate-spin" />
            </div>
        );
    }
    return (
        <div className="max-w-4xl space-y-6 p-4 md:p-6">
            <div>
                <h1 className="text-2xl font-semibold">{t("driverComp.config.title")}</h1>
                <p className="text-sm text-muted-foreground">{t("driverComp.config.subtitle")}</p>
            </div>
            <HrErrorAlert error={active.error} />
            {active.isSuccess ? (
                // Keyed by the version it starts from: a new version in effect resets the form once.
                <ConfigForm key={active.data?.id ?? "defaults"} start={active.data} />
            ) : null}
            <History versions={all.data} error={all.error} loading={all.isPending} activeId={active.data?.id} />
        </div>
    );
}

function ConfigForm({ start }: { start: CompConfigDTO | null }) {
    const { t } = useLanguage();
    const create = useCreateCompConfig();
    const [effectiveFrom, setEffectiveFrom] = useState(() => format(new Date(), "yyyy-MM-dd"));
    const [cfg, setCfg] = useState<Editable>(() => editableConfig(start));
    const [error, setError] = useState<unknown>(null);

    const set = <K extends keyof Editable>(key: K, value: Editable[K]) => setCfg((c) => ({ ...c, [key]: value }));
    const setSSO = (key: keyof Editable["sso"], value: number) => setCfg((c) => ({ ...c, sso: { ...c.sso, [key]: value } }));

    const save = async () => {
        setError(null);
        try {
            const saved = await create.mutateAsync({ ...cfg, effectiveFrom });
            toast.success(t("hr.config.savedVersion", { date: saved.effectiveFromDate }));
        } catch (e) {
            setError(e);
            toast.error(t("driverComp.config.saveError"));
        }
    };

    return (
        <>
            {!start ? <p className="rounded-md border border-dashed p-3 text-sm text-muted-foreground">{t("hr.config.noVersion")}</p> : null}
            <Card>
                <CardContent className="space-y-4 pt-6">
                    <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
                        <Field label={t("driverComp.config.effectiveFrom")}>
                            <Input type="date" value={effectiveFrom} onChange={(e) => setEffectiveFrom(e.target.value)} aria-label={t("driverComp.config.effectiveFrom")} />
                        </Field>
                        <NumberField label={t("driverComp.config.weekdayRate")} value={cfg.weekdayRateThb} onChange={(v) => set("weekdayRateThb", v)} />
                        <NumberField label={t("driverComp.config.holidayRate")} value={cfg.holidayRateThb} onChange={(v) => set("holidayRateThb", v)} />
                        <NumberField label={t("driverComp.config.helperDayRate")} value={cfg.helperDayRateThb} onChange={(v) => set("helperDayRateThb", v)} />
                    </div>
                    <label className="flex items-center gap-2 text-sm">
                        <input type="checkbox" checked={cfg.payStandby} onChange={(e) => set("payStandby", e.target.checked)} />
                        {t("driverComp.config.payStandby")}
                    </label>
                    <p className="text-xs text-muted-foreground">{t("hr.config.effectiveHint")}</p>
                </CardContent>
            </Card>

            <Card>
                <CardContent className="space-y-3 pt-6">
                    <h2 className="text-sm font-semibold">{t("driverComp.config.fuelTitle")}</h2>
                    <NumberField label={t("driverComp.config.fuelMinRefuels")} value={cfg.fuelMinRefuelsPerMonth} onChange={(v) => set("fuelMinRefuelsPerMonth", v)} />
                    <TierEditor
                        rows={cfg.fuelIncentiveTiers.map((tier) => [tier.minKmPerLitre, tier.amountThb])}
                        colLabels={[t("driverComp.config.minKmPerLitre"), t("driverComp.config.amountThb")]}
                        onChange={(rows) => set("fuelIncentiveTiers", rows.map(([a, b]) => ({ minKmPerLitre: a, amountThb: b })))}
                        addLabel={t("driverComp.config.addTier")}
                        removeLabel={t("hr.config.removeRow")}
                    />
                </CardContent>
            </Card>

            <Card>
                <CardContent className="space-y-3 pt-6">
                    <h2 className="text-sm font-semibold">{t("driverComp.config.tripVolumeTitle")}</h2>
                    <TierEditor
                        rows={cfg.tripVolumeTiers.map((tier) => [tier.minTrips, tier.amountThb])}
                        colLabels={[t("driverComp.config.minTrips"), t("driverComp.config.amountThb")]}
                        onChange={(rows) => set("tripVolumeTiers", rows.map(([a, b]) => ({ minTrips: a, amountThb: b })))}
                        addLabel={t("driverComp.config.addTier")}
                        removeLabel={t("hr.config.removeRow")}
                    />
                </CardContent>
            </Card>

            <Card>
                <CardContent className="space-y-4 pt-6">
                    <h2 className="text-sm font-semibold">{t("driverComp.config.ssoTitle")}</h2>
                    <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
                        <NumberField label={t("driverComp.config.ssoRate")} value={cfg.sso.ratePercent} onChange={(v) => setSSO("ratePercent", v)} />
                        <NumberField label={t("driverComp.config.ssoBaseExisting")} value={cfg.sso.baseExistingThb} onChange={(v) => setSSO("baseExistingThb", v)} />
                        <NumberField label={t("driverComp.config.ssoBaseNew")} value={cfg.sso.baseNewThb} onChange={(v) => setSSO("baseNewThb", v)} />
                        <NumberField
                            label={t("driverComp.config.ssoExistingYear")}
                            value={cfg.sso.existingHiredBeforeYear}
                            onChange={(v) => setSSO("existingHiredBeforeYear", v)}
                        />
                        <NumberField label={t("driverComp.config.ssoMaxAge")} value={cfg.sso.maxAgeInclusive} onChange={(v) => setSSO("maxAgeInclusive", v)} />
                        <NumberField label={t("driverComp.config.ssoProbation")} value={cfg.sso.probationMonths} onChange={(v) => setSSO("probationMonths", v)} />
                    </div>
                </CardContent>
            </Card>

            <Card>
                <CardContent className="space-y-3 pt-6">
                    <h2 className="text-sm font-semibold">{t("hr.config.penaltyTypes")}</h2>
                    <PenaltyTypesEditor rows={cfg.penaltyTypes} onChange={(rows) => set("penaltyTypes", rows)} />
                </CardContent>
            </Card>

            <HrErrorAlert error={error} />
            <div className="flex items-center gap-3">
                <Button onClick={() => void save()} disabled={create.isPending}>
                    {create.isPending ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Save className="mr-2 h-4 w-4" />}
                    {t("driverComp.config.save")}
                </Button>
            </div>
        </>
    );
}

function History({ versions, error, loading, activeId }: { versions: CompConfigDTO[] | undefined; error: unknown; loading: boolean; activeId?: string }) {
    const { t, language } = useLanguage();
    return (
        <Card>
            <CardContent className="space-y-3 pt-6">
                <h2 className="text-sm font-semibold">
                    {t("driverComp.config.versions")} {versions ? `(${versions.length})` : ""}
                </h2>
                <HrErrorAlert error={error} />
                {loading ? <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" /> : null}
                {versions && versions.length > 0 ? (
                    <Table>
                        <TableHeader>
                            <TableRow>
                                <TableHead>{t("driverComp.config.effectiveFrom")}</TableHead>
                                <TableHead className="text-right">{t("driverComp.config.weekdayRate")}</TableHead>
                                <TableHead className="text-right">{t("driverComp.config.holidayRate")}</TableHead>
                                <TableHead className="text-right">{t("driverComp.config.helperDayRate")}</TableHead>
                                <TableHead>{t("hr.config.createdAt")}</TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {versions.map((v) => (
                                <TableRow key={v.id}>
                                    <TableCell className="font-medium">
                                        {dayLabel(v.effectiveFromDate, language)}
                                        {v.id === activeId ? <span className="ml-2 text-xs text-green-700">{t("hr.config.inEffect")}</span> : null}
                                    </TableCell>
                                    <TableCell className="text-right tabular-nums">{formatThb(v.weekdayRateThb)}</TableCell>
                                    <TableCell className="text-right tabular-nums">{formatThb(v.holidayRateThb)}</TableCell>
                                    <TableCell className="text-right tabular-nums">{formatThb(v.helperDayRateThb)}</TableCell>
                                    <TableCell className="text-xs text-muted-foreground">{dayLabel(v.createdAt, language, "PPp")}</TableCell>
                                </TableRow>
                            ))}
                        </TableBody>
                    </Table>
                ) : null}
            </CardContent>
        </Card>
    );
}

function Field({ label, children }: { label: string; children: ReactNode }) {
    return (
        <label className="flex flex-col gap-1">
            <span className="text-xs font-medium text-muted-foreground">{label}</span>
            {children}
        </label>
    );
}

function NumberField({ label, value, onChange }: { label: string; value: number; onChange: (v: number) => void }) {
    return (
        <Field label={label}>
            <Input type="number" value={Number.isFinite(value) ? value : ""} onChange={(e) => onChange(Number(e.target.value))} />
        </Field>
    );
}

function TierEditor({
    rows,
    colLabels,
    onChange,
    addLabel,
    removeLabel,
}: {
    rows: [number, number][];
    colLabels: [string, string];
    onChange: (rows: [number, number][]) => void;
    addLabel: string;
    removeLabel: string;
}) {
    const update = (i: number, j: 0 | 1, value: number) => {
        const next = rows.map((r) => [...r] as [number, number]);
        next[i][j] = value;
        onChange(next);
    };
    return (
        <div className="space-y-2">
            <div className="grid grid-cols-[1fr_1fr_auto] gap-2 text-xs font-medium text-muted-foreground">
                <span>{colLabels[0]}</span>
                <span>{colLabels[1]}</span>
                <span />
            </div>
            {rows.map((r, i) => (
                <div key={i} className="grid grid-cols-[1fr_1fr_auto] gap-2">
                    <Input type="number" aria-label={`${colLabels[0]} ${i + 1}`} value={r[0]} onChange={(e) => update(i, 0, Number(e.target.value))} />
                    <Input type="number" aria-label={`${colLabels[1]} ${i + 1}`} value={r[1]} onChange={(e) => update(i, 1, Number(e.target.value))} />
                    <Button variant="ghost" size="icon" aria-label={removeLabel} onClick={() => onChange(rows.filter((_, idx) => idx !== i))}>
                        <Trash2 className="h-4 w-4" />
                    </Button>
                </div>
            ))}
            <Button variant="outline" size="sm" onClick={() => onChange([...rows, [0, 0]])}>
                <Plus className="mr-1 h-4 w-4" /> {addLabel}
            </Button>
        </div>
    );
}

function PenaltyTypesEditor({ rows, onChange }: { rows: PenaltyTypeDTO[]; onChange: (rows: PenaltyTypeDTO[]) => void }) {
    const { t } = useLanguage();
    const update = (i: number, patch: Partial<PenaltyTypeDTO>) => onChange(rows.map((r, idx) => (idx === i ? { ...r, ...patch } : r)));
    return (
        <div className="space-y-2">
            <div className="grid grid-cols-[1fr_1.5fr_1.5fr_1fr_auto] gap-2 text-xs font-medium text-muted-foreground">
                <span>{t("hr.config.typeCode")}</span>
                <span>{t("hr.config.typeNameEn")}</span>
                <span>{t("hr.config.typeNameTh")}</span>
                <span>{t("hr.config.typeDefaultAmount")}</span>
                <span />
            </div>
            {rows.map((r, i) => (
                <div key={i} className="grid grid-cols-[1fr_1.5fr_1.5fr_1fr_auto] gap-2">
                    <Input value={r.code} aria-label={`${t("hr.config.typeCode")} ${i + 1}`} onChange={(e) => update(i, { code: e.target.value.toUpperCase() })} />
                    <Input value={r.nameEn} aria-label={`${t("hr.config.typeNameEn")} ${i + 1}`} onChange={(e) => update(i, { nameEn: e.target.value })} />
                    <Input value={r.nameTh} aria-label={`${t("hr.config.typeNameTh")} ${i + 1}`} onChange={(e) => update(i, { nameTh: e.target.value })} />
                    <Input
                        type="number"
                        value={r.defaultAmountThb}
                        aria-label={`${t("hr.config.typeDefaultAmount")} ${i + 1}`}
                        onChange={(e) => update(i, { defaultAmountThb: Number(e.target.value) })}
                    />
                    <Button variant="ghost" size="icon" aria-label={t("hr.config.removeRow")} onClick={() => onChange(rows.filter((_, idx) => idx !== i))}>
                        <Trash2 className="h-4 w-4" />
                    </Button>
                </div>
            ))}
            <Button variant="outline" size="sm" onClick={() => onChange([...rows, { code: "", nameEn: "", nameTh: "", defaultAmountThb: 0 }])}>
                <Plus className="mr-1 h-4 w-4" /> {t("hr.config.addType")}
            </Button>
        </div>
    );
}
