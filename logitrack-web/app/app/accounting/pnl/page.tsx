"use client";

import { useState } from "react";
import Link from "next/link";
import { Download, Loader2, RefreshCw, Settings, TrendingUp } from "lucide-react";
import { toast } from "sonner";
import { PagePermissionGuard } from "@/components/page-permission-guard";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useLanguage } from "@/context/language";
import { usePermission } from "@/hooks/usePermission";
import { CAPABILITIES } from "@/lib/capabilities";
import { PnlSourceError } from "@/features/pnl/api/pnlSources";
import { PnlLaneTable } from "@/features/pnl/components/PnlLaneTable";
import { PnlMonthStatement } from "@/features/pnl/components/PnlMonthStatement";
import { PnlTripTable } from "@/features/pnl/components/PnlTripTable";
import { PnlTruckTable } from "@/features/pnl/components/PnlTruckTable";
import { PnlWarningsPanel } from "@/features/pnl/components/PnlWarningsPanel";
import { PnlYearView } from "@/features/pnl/components/PnlYearView";
import { usePnlMonth } from "@/features/pnl/hooks/usePnlMonth";
import { fmtThb, monthOptions } from "@/features/pnl/utils/format";
import { downloadPnlHtml } from "@/features/pnl/utils/pnlHtml";

const MONTHS = monthOptions();

export default function PnlPage() {
    return (
        <PagePermissionGuard capability={CAPABILITIES.accounting_view_pnl}>
            <PnlContent />
        </PagePermissionGuard>
    );
}

function PnlContent() {
    const { t, language } = useLanguage();
    const { hasPermission: canManage } = usePermission(CAPABILITIES.accounting_manage_pnl_settings);
    const [monthValue, setMonthValue] = useState(MONTHS[0].value);
    const [tab, setTab] = useState("month");
    const selected = MONTHS.find((m) => m.value === monthValue) ?? MONTHS[0];
    const { result, sources, loading, error, reload } = usePnlMonth(selected.year, selected.month);
    const hasRates = (sources?.inputs.rates ?? []).some((r) => !r.voided);

    const onExport = () => {
        if (!result || !sources) return;
        try {
            downloadPnlHtml({
                result,
                hubLabels: sources.hubLabels,
                rates: sources.inputs.rates,
                fixedCosts: sources.inputs.fixedCosts,
                t,
                language,
            });
        } catch (e) {
            console.error("P&L export failed", e);
            toast.error(t("pnl.exportFailed"));
        }
    };

    return (
        <div className="container py-8 space-y-6">
            <div className="flex items-center justify-between flex-wrap gap-3">
                <div>
                    <h1 className="text-2xl font-bold flex items-center gap-2">
                        <TrendingUp className="w-6 h-6 text-emerald-600" />
                        {t("pnl.title")}
                    </h1>
                    <p className="text-muted-foreground text-sm mt-1">{t("pnl.subtitle")}</p>
                </div>
                <div className="flex items-center gap-2 flex-wrap">
                    <Select value={monthValue} onValueChange={setMonthValue}>
                        <SelectTrigger className="w-[140px]">
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                            {MONTHS.map((m) => (
                                <SelectItem key={m.value} value={m.value}>{m.value}</SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                    <Button variant="outline" size="sm" onClick={() => void reload()} disabled={loading}>
                        {loading ? <Loader2 className="w-4 h-4 animate-spin" /> : <RefreshCw className="w-4 h-4" />}
                        <span className="ml-1">{t("pnl.reload")}</span>
                    </Button>
                    <Button variant="outline" size="sm" onClick={onExport} disabled={!result || loading}>
                        <Download className="w-4 h-4" />
                        <span className="ml-1">{t("pnl.export")}</span>
                    </Button>
                    {canManage && (
                        <Button asChild variant="outline" size="sm">
                            <Link href="/app/accounting/pnl/settings">
                                <Settings className="w-4 h-4" />
                                <span className="ml-1">{t("pnl.settings.title")}</span>
                            </Link>
                        </Button>
                    )}
                </div>
            </div>

            {loading && !result ? (
                <div className="flex justify-center py-16">
                    <Loader2 className="w-8 h-8 animate-spin text-muted-foreground" />
                </div>
            ) : error ? (
                <Card className="border-destructive">
                    <CardContent className="pt-6 text-sm text-destructive">
                        {error instanceof PnlSourceError
                            ? t(error.settingsRulesMissing ? "pnl.error.settingsRules" : "pnl.error.source", { source: error.source })
                            : t("pnl.error.generic")}
                        <p className="mt-1 text-xs text-muted-foreground">{error.message}</p>
                    </CardContent>
                </Card>
            ) : result && sources ? (
                <>
                    {!hasRates && (
                        <Card className="border-destructive">
                            <CardContent className="pt-6 text-sm flex items-center justify-between gap-3 flex-wrap">
                                <span className="text-destructive font-medium">{t("pnl.banner.noSettings")}</span>
                                {canManage && (
                                    <Button asChild size="sm">
                                        <Link href="/app/accounting/pnl/settings">{t("pnl.settings.title")}</Link>
                                    </Button>
                                )}
                            </CardContent>
                        </Card>
                    )}
                    <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
                        <Kpi label={t("pnl.kpi.revenue")} value={fmtThb(result.statement.revenueThb)} />
                        <Kpi label={t("pnl.kpi.contribution")} value={fmtThb(result.statement.contributionThb)} />
                        <Kpi label={t("pnl.kpi.operatingProfit")} value={fmtThb(result.statement.operatingProfitThb)} negative={result.statement.operatingProfitThb < 0} />
                        <Kpi
                            label={t("pnl.kpi.breakEven")}
                            value={result.breakEven.primaryTrips === null ? "—" : `${result.breakEven.actualTrips} / ${result.breakEven.primaryTrips}`}
                        />
                    </div>
                    <PnlWarningsPanel result={result} />
                    <Tabs value={tab} onValueChange={setTab}>
                        <TabsList>
                            <TabsTrigger value="month">{t("pnl.tabs.month")}</TabsTrigger>
                            <TabsTrigger value="year">{t("pnl.tabs.year")}</TabsTrigger>
                            <TabsTrigger value="trips">{t("pnl.tabs.trips")}</TabsTrigger>
                            <TabsTrigger value="lanes">{t("pnl.tabs.lanes")}</TabsTrigger>
                            <TabsTrigger value="trucks">{t("pnl.tabs.trucks")}</TabsTrigger>
                        </TabsList>
                        <TabsContent value="month" className="mt-4">
                            <PnlMonthStatement result={result} />
                        </TabsContent>
                        <TabsContent value="year" className="mt-4">
                            {/* Loaded only when opened — a year is ~12× the reads of a month. */}
                            {tab === "year" && <PnlYearView />}
                        </TabsContent>
                        <TabsContent value="trips" className="mt-4">
                            <PnlTripTable result={result} hubLabels={sources.hubLabels} />
                        </TabsContent>
                        <TabsContent value="lanes" className="mt-4">
                            <PnlLaneTable result={result} hubLabels={sources.hubLabels} />
                        </TabsContent>
                        <TabsContent value="trucks" className="mt-4">
                            <PnlTruckTable result={result} />
                        </TabsContent>
                    </Tabs>
                </>
            ) : null}
        </div>
    );
}

function Kpi({ label, value, negative }: { label: string; value: string; negative?: boolean }) {
    return (
        <Card>
            <CardContent className="pt-4">
                <p className="text-xs text-muted-foreground">{label}</p>
                <p className={`text-xl font-bold tabular-nums ${negative ? "text-red-600 dark:text-red-400" : ""}`}>{value}</p>
            </CardContent>
        </Card>
    );
}
