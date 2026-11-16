"use client";

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { collection, getDocs } from "firebase/firestore";
import { ArrowLeft, Loader2, Settings } from "lucide-react";
import { toast } from "sonner";
import { PagePermissionGuard } from "@/components/page-permission-guard";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { useAuth } from "@/context/auth";
import { useLanguage } from "@/context/language";
import { db } from "@/firebase/client";
import { CAPABILITIES } from "@/lib/capabilities";
import { COLLECTIONS } from "@/lib/collections";
import { getActiveCompensationConfig } from "@/features/driver-compensation/api/config";
import {
    addPnlFixedCostVersion,
    addPnlRate,
    listPnlFixedCosts,
    listPnlRates,
    voidPnlFixedCostVersion,
    voidPnlRate,
    type PnlActor,
} from "@/features/pnl/api/pnlSettings";
import { PnlFixedCostSettings } from "@/features/pnl/components/PnlFixedCostSettings";
import { PnlRateSettings, type PayrollReminder } from "@/features/pnl/components/PnlRateSettings";
import type { PnlFixedCostParsed, PnlFixedCostVersion, PnlRateInput, PnlRateRow } from "@/validate/pnlSettingsSchema";

export default function PnlSettingsPage() {
    return (
        <PagePermissionGuard capability={CAPABILITIES.accounting_manage_pnl_settings}>
            <PnlSettingsContent />
        </PagePermissionGuard>
    );
}

function PnlSettingsContent() {
    const { t } = useLanguage();
    const auth = useAuth();
    const user = auth?.currentUser ?? null;
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<{ code?: string; message: string } | null>(null);
    const [rates, setRates] = useState<PnlRateRow[]>([]);
    const [fixedCosts, setFixedCosts] = useState<PnlFixedCostVersion[]>([]);
    const [trucks, setTrucks] = useState<Array<{ id: string; plate: string }>>([]);
    const [payroll, setPayroll] = useState<PayrollReminder | null>(null);

    const load = useCallback(async () => {
        setLoading(true);
        setError(null);
        try {
            const [r, f, truckSnap, cfg] = await Promise.all([
                listPnlRates(),
                listPnlFixedCosts(),
                getDocs(collection(db, COLLECTIONS.TRUCKS)),
                getActiveCompensationConfig().catch((e) => {
                    console.error("Payroll config read failed (reminder only)", e);
                    return null;
                }),
            ]);
            setRates(r);
            setFixedCosts(f);
            setTrucks(
                truckSnap.docs
                    .map((s) => ({ id: s.id, data: s.data() }))
                    .filter(({ data }) => data.ownershipType !== "subcontractor" && !data.subcontractorId)
                    .map(({ id, data }) => ({ id, plate: String(data.licensePlate ?? id) }))
                    .sort((a, b) => a.plate.localeCompare(b.plate)),
            );
            setPayroll(cfg ? { weekdayRateThb: cfg.weekdayRateThb, holidayRateThb: cfg.holidayRateThb, helperDayRateThb: cfg.helperDayRateThb } : null);
        } catch (e) {
            console.error("P&L settings load failed", e);
            const code = (e as { code?: unknown } | null)?.code;
            setError({ code: typeof code === "string" ? code : undefined, message: e instanceof Error ? e.message : String(e) });
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        void load();
    }, [load]);

    const actor = (): PnlActor => {
        if (!user) throw new Error("Not signed in");
        return { uid: user.uid, name: user.displayName ?? user.email ?? undefined };
    };
    const run = async (fn: () => Promise<unknown>) => {
        try {
            await fn();
            toast.success(t("pnl.settings.saved"));
            await load();
        } catch (e) {
            console.error("P&L settings write failed", e);
            toast.error(t("pnl.settings.saveFailed"));
            throw e;
        }
    };

    return (
        <div className="container py-8 space-y-6">
            <div className="flex items-center justify-between flex-wrap gap-3">
                <div>
                    <h1 className="text-2xl font-bold flex items-center gap-2">
                        <Settings className="w-6 h-6 text-muted-foreground" />
                        {t("pnl.settings.title")}
                    </h1>
                    <p className="text-muted-foreground text-sm mt-1">{t("pnl.settings.subtitle")}</p>
                </div>
                <Button asChild variant="outline" size="sm">
                    <Link href="/app/accounting/pnl">
                        <ArrowLeft className="w-4 h-4" />
                        <span className="ml-1">{t("pnl.title")}</span>
                    </Link>
                </Button>
            </div>

            {loading ? (
                <div className="flex justify-center py-16">
                    <Loader2 className="w-8 h-8 animate-spin text-muted-foreground" />
                </div>
            ) : error ? (
                <Card className="border-destructive">
                    <CardContent className="pt-6 text-sm text-destructive">
                        {error.code === "permission-denied"
                            ? t("pnl.error.settingsRules", { source: "pnl_rates / pnl_fixed_costs" })
                            : t("pnl.error.generic")}
                        <p className="mt-1 text-xs text-muted-foreground">{error.message}</p>
                    </CardContent>
                </Card>
            ) : (
                <>
                    <PnlRateSettings
                        rates={rates}
                        payroll={payroll}
                        onAdd={(input: PnlRateInput) => run(() => addPnlRate(input, actor()))}
                        onVoid={(id, reason) => run(() => voidPnlRate(id, reason, actor()))}
                    />
                    <PnlFixedCostSettings
                        versions={fixedCosts}
                        trucks={trucks}
                        onSave={(values: PnlFixedCostParsed, itemId?: string) => run(() => addPnlFixedCostVersion(values, actor(), itemId))}
                        onVoid={(id, reason) => run(() => voidPnlFixedCostVersion(id, reason, actor()))}
                    />
                </>
            )}
        </div>
    );
}
