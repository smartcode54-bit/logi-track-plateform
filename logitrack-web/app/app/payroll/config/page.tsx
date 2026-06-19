"use client";

import { useEffect, useState, type ReactNode } from "react";
import { format } from "date-fns";
import { Loader2, Plus, Trash2, Save } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardContent } from "@/components/ui/card";
import { useLanguage } from "@/context/language";
import { useAuth } from "@/context/auth";
import {
    DEFAULT_COMPENSATION_CONFIG,
    type CompensationConfig,
} from "@/validate/compensationConfigSchema";
import {
    getActiveCompensationConfig,
    listCompensationConfigs,
    saveCompensationConfig,
} from "@/features/driver-compensation/api/config";

type EditableConfig = Omit<CompensationConfig, "id" | "createdAt" | "updatedAt">;

function todayStr() {
    return format(new Date(), "yyyy-MM-dd");
}

export default function CompensationConfigPage() {
    const { t } = useLanguage();
    const auth = useAuth();
    const user = auth?.currentUser ?? null;

    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [effectiveFrom, setEffectiveFrom] = useState(todayStr());
    const [cfg, setCfg] = useState<EditableConfig>({
        ...DEFAULT_COMPENSATION_CONFIG,
        effectiveFrom: new Date(),
    });
    const [history, setHistory] = useState<CompensationConfig[]>([]);

    useEffect(() => {
        (async () => {
            try {
                const [active, all] = await Promise.all([
                    getActiveCompensationConfig(),
                    listCompensationConfigs(),
                ]);
                if (active) {
                    setCfg({ ...active });
                }
                setHistory(all);
            } catch (e) {
                console.error(e);
            } finally {
                setLoading(false);
            }
        })();
    }, []);

    function set<K extends keyof EditableConfig>(key: K, value: EditableConfig[K]) {
        setCfg((c) => ({ ...c, [key]: value }));
    }

    async function handleSave() {
        setSaving(true);
        try {
            await saveCompensationConfig(
                { ...cfg, effectiveFrom: new Date(effectiveFrom) },
                user?.uid,
            );
            toast.success(t("driverComp.config.saved"));
            const all = await listCompensationConfigs();
            setHistory(all);
        } catch (e) {
            console.error(e);
            toast.error(t("driverComp.config.saveError"));
        } finally {
            setSaving(false);
        }
    }

    if (loading) {
        return (
            <div className="flex items-center justify-center p-12">
                <Loader2 className="h-6 w-6 animate-spin" />
            </div>
        );
    }

    return (
        <div className="space-y-6 p-4 md:p-6 max-w-4xl">
            <div>
                <h1 className="text-2xl font-semibold">{t("driverComp.config.title")}</h1>
                <p className="text-sm text-muted-foreground">{t("driverComp.config.subtitle")}</p>
            </div>

            {/* Effective date + base rates */}
            <Card>
                <CardContent className="space-y-4 pt-6">
                    <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
                        <Field label={t("driverComp.config.effectiveFrom")}>
                            <Input type="date" value={effectiveFrom} onChange={(e) => setEffectiveFrom(e.target.value)} />
                        </Field>
                        <Field label={t("driverComp.config.weekdayRate")}>
                            <Input type="number" value={cfg.weekdayRateThb} onChange={(e) => set("weekdayRateThb", Number(e.target.value))} />
                        </Field>
                        <Field label={t("driverComp.config.holidayRate")}>
                            <Input type="number" value={cfg.holidayRateThb} onChange={(e) => set("holidayRateThb", Number(e.target.value))} />
                        </Field>
                    </div>
                    <label className="flex items-center gap-2 text-sm">
                        <input type="checkbox" checked={cfg.payStandby} onChange={(e) => set("payStandby", e.target.checked)} />
                        {t("driverComp.config.payStandby")}
                    </label>
                </CardContent>
            </Card>

            {/* Fuel incentive */}
            <Card>
                <CardContent className="space-y-3 pt-6">
                    <SectionHeader title={t("driverComp.config.fuelTitle")} />
                    <Field label={t("driverComp.config.fuelMinRefuels")}>
                        <Input type="number" value={cfg.fuelMinRefuelsPerMonth} onChange={(e) => set("fuelMinRefuelsPerMonth", Number(e.target.value))} />
                    </Field>
                    <TierEditor
                        rows={cfg.fuelIncentiveTiers.map((tier) => [tier.minKmPerLitre, tier.amountThb])}
                        colLabels={[t("driverComp.config.minKmPerLitre"), t("driverComp.config.amountThb")]}
                        onChange={(rows) => set("fuelIncentiveTiers", rows.map(([a, b]) => ({ minKmPerLitre: a, amountThb: b })))}
                        addLabel={t("driverComp.config.addTier")}
                    />
                </CardContent>
            </Card>

            {/* Trip-volume incentive */}
            <Card>
                <CardContent className="space-y-3 pt-6">
                    <SectionHeader title={t("driverComp.config.tripVolumeTitle")} />
                    <TierEditor
                        rows={cfg.tripVolumeTiers.map((tier) => [tier.minTrips, tier.amountThb])}
                        colLabels={[t("driverComp.config.minTrips"), t("driverComp.config.amountThb")]}
                        onChange={(rows) => set("tripVolumeTiers", rows.map(([a, b]) => ({ minTrips: a, amountThb: b })))}
                        addLabel={t("driverComp.config.addTier")}
                    />
                </CardContent>
            </Card>

            {/* Social security */}
            <Card>
                <CardContent className="space-y-4 pt-6">
                    <SectionHeader title={t("driverComp.config.ssoTitle")} />
                    <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
                        <Field label={t("driverComp.config.ssoRate")}>
                            <Input type="number" value={cfg.sso.ratePercent} onChange={(e) => set("sso", { ...cfg.sso, ratePercent: Number(e.target.value) })} />
                        </Field>
                        <Field label={t("driverComp.config.ssoBaseExisting")}>
                            <Input type="number" value={cfg.sso.baseExistingThb} onChange={(e) => set("sso", { ...cfg.sso, baseExistingThb: Number(e.target.value) })} />
                        </Field>
                        <Field label={t("driverComp.config.ssoBaseNew")}>
                            <Input type="number" value={cfg.sso.baseNewThb} onChange={(e) => set("sso", { ...cfg.sso, baseNewThb: Number(e.target.value) })} />
                        </Field>
                        <Field label={t("driverComp.config.ssoExistingYear")}>
                            <Input type="number" value={cfg.sso.existingHiredBeforeYear} onChange={(e) => set("sso", { ...cfg.sso, existingHiredBeforeYear: Number(e.target.value) })} />
                        </Field>
                        <Field label={t("driverComp.config.ssoMaxAge")}>
                            <Input type="number" value={cfg.sso.maxAgeInclusive} onChange={(e) => set("sso", { ...cfg.sso, maxAgeInclusive: Number(e.target.value) })} />
                        </Field>
                        <Field label={t("driverComp.config.ssoProbation")}>
                            <Input type="number" value={cfg.sso.probationMonths} onChange={(e) => set("sso", { ...cfg.sso, probationMonths: Number(e.target.value) })} />
                        </Field>
                    </div>
                </CardContent>
            </Card>

            <div className="flex items-center gap-3">
                <Button onClick={handleSave} disabled={saving}>
                    {saving ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Save className="mr-2 h-4 w-4" />}
                    {t("driverComp.config.save")}
                </Button>
                <span className="text-xs text-muted-foreground">
                    {t("driverComp.config.versions")}: {history.length}
                </span>
            </div>
        </div>
    );
}

function Field({ label, children }: { label: string; children: ReactNode }) {
    return (
        <div className="space-y-1">
            <label className="text-xs font-medium text-muted-foreground">{label}</label>
            {children}
        </div>
    );
}

function SectionHeader({ title }: { title: string }) {
    return <h2 className="text-sm font-semibold">{title}</h2>;
}

function TierEditor({
    rows,
    colLabels,
    onChange,
    addLabel,
}: {
    rows: [number, number][];
    colLabels: [string, string];
    onChange: (rows: [number, number][]) => void;
    addLabel: string;
}) {
    function update(i: number, j: 0 | 1, value: number) {
        const next = rows.map((r) => [...r] as [number, number]);
        next[i][j] = value;
        onChange(next);
    }
    return (
        <div className="space-y-2">
            <div className="grid grid-cols-[1fr_1fr_auto] gap-2 text-xs font-medium text-muted-foreground">
                <span>{colLabels[0]}</span>
                <span>{colLabels[1]}</span>
                <span />
            </div>
            {rows.map((r, i) => (
                <div key={i} className="grid grid-cols-[1fr_1fr_auto] gap-2">
                    <Input type="number" value={r[0]} onChange={(e) => update(i, 0, Number(e.target.value))} />
                    <Input type="number" value={r[1]} onChange={(e) => update(i, 1, Number(e.target.value))} />
                    <Button variant="ghost" size="icon" onClick={() => onChange(rows.filter((_, idx) => idx !== i))}>
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
