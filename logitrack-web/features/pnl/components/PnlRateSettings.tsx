"use client";

import { useMemo, useState } from "react";
import { Ban, Plus } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useLanguage } from "@/context/language";
import { bangkokDateKey } from "@/lib/compensationCompute";
import { selectPnlRate } from "@/lib/pnlCompute";
import { PNL_RATE_KINDS, type PnlRateInput, type PnlRateKind, type PnlRateRow } from "@/validate/pnlSettingsSchema";
import { fmtThb } from "../utils/format";
import { PnlRateDialog } from "./PnlRateDialog";
import { PnlVoidDialog } from "./PnlVoidDialog";

export interface PayrollReminder {
    weekdayRateThb: number;
    holidayRateThb: number;
    helperDayRateThb: number;
}

/** The four per-unit rates (R1, R4): current value, full history including voided rows. */
export function PnlRateSettings({
    rates,
    payroll,
    onAdd,
    onVoid,
}: {
    rates: PnlRateRow[];
    payroll: PayrollReminder | null;
    onAdd: (input: PnlRateInput) => Promise<void>;
    onVoid: (id: string, reason: string) => Promise<void>;
}) {
    const { t } = useLanguage();
    const [addKind, setAddKind] = useState<PnlRateKind | null>(null);
    const [voiding, setVoiding] = useState<PnlRateRow | null>(null);
    const today = bangkokDateKey(new Date());
    const current = useMemo(
        () => Object.fromEntries(PNL_RATE_KINDS.map((k) => [k, selectPnlRate(rates, k, today)])) as Record<PnlRateKind, PnlRateRow | null>,
        [rates, today],
    );
    const history = useMemo(
        () => [...rates].sort((a, b) => a.kind.localeCompare(b.kind) || b.effectiveFrom.localeCompare(a.effectiveFrom)),
        [rates],
    );
    const unit = (k: PnlRateKind) => t(k === "driver_trip" ? "pnl.settings.unit.trip" : k === "helper_day" ? "pnl.settings.unit.helperDay" : "pnl.settings.unit.km");

    return (
        <Card>
            <CardHeader>
                <CardTitle className="text-base">{t("pnl.settings.rate.title")}</CardTitle>
            </CardHeader>
            <CardContent className="space-y-4">
                <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
                    {PNL_RATE_KINDS.map((k) => (
                        <div key={k} className="rounded-md border p-3 space-y-1">
                            <p className="text-sm text-muted-foreground">{t(`pnl.settings.rate.${k}`)}</p>
                            {current[k] ? (
                                <>
                                    <p className="text-xl font-bold tabular-nums">
                                        {fmtThb(current[k]!.amountThb)} <span className="text-xs font-normal text-muted-foreground">{unit(k)}</span>
                                    </p>
                                    <p className="text-xs text-muted-foreground">{t("pnl.settings.effectiveSince", { date: current[k]!.effectiveFrom })}</p>
                                </>
                            ) : (
                                <p className="text-sm text-destructive">{t("pnl.settings.rate.notSet")}</p>
                            )}
                            <Button size="sm" variant="outline" className="mt-1" onClick={() => setAddKind(k)}>
                                <Plus className="w-4 h-4 mr-1" />
                                {t("pnl.settings.rate.add")}
                            </Button>
                        </div>
                    ))}
                </div>

                {payroll && (
                    <div className="rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-900 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-200">
                        {t("pnl.settings.payrollReminder", {
                            weekday: fmtThb(payroll.weekdayRateThb),
                            holiday: fmtThb(payroll.holidayRateThb),
                            helper: fmtThb(payroll.helperDayRateThb),
                            pnl: fmtThb(current.driver_trip?.amountThb),
                            pnlHelper: fmtThb(current.helper_day?.amountThb),
                        })}
                    </div>
                )}

                <div className="overflow-x-auto rounded-md border">
                    <Table>
                        <TableHeader>
                            <TableRow>
                                <TableHead>{t("pnl.settings.rate.kind")}</TableHead>
                                <TableHead>{t("pnl.settings.effectiveFrom")}</TableHead>
                                <TableHead className="text-right">{t("pnl.settings.rate.amount")}</TableHead>
                                <TableHead>{t("pnl.settings.note")}</TableHead>
                                <TableHead>{t("pnl.settings.createdBy")}</TableHead>
                                <TableHead />
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {history.length === 0 ? (
                                <TableRow>
                                    <TableCell colSpan={6} className="text-center text-muted-foreground py-6">{t("pnl.settings.empty")}</TableCell>
                                </TableRow>
                            ) : (
                                history.map((r) => (
                                    <TableRow key={r.id} className={r.voided ? "opacity-60" : ""}>
                                        <TableCell>{t(`pnl.settings.rate.${r.kind}`)}</TableCell>
                                        <TableCell className={r.voided ? "line-through" : ""}>{r.effectiveFrom}</TableCell>
                                        <TableCell className={`text-right tabular-nums ${r.voided ? "line-through" : ""}`}>{fmtThb(r.amountThb)}</TableCell>
                                        <TableCell className="text-sm">
                                            {r.note ?? ""}
                                            {r.voided && (
                                                <span className="block text-xs text-destructive">
                                                    {t("pnl.settings.void.voidedNote", { reason: r.voidedReason ?? "" })}
                                                </span>
                                            )}
                                        </TableCell>
                                        <TableCell className="text-xs text-muted-foreground">
                                            {r.createdByName ?? r.createdBy ?? ""}
                                            {r.createdAt && <span className="block">{r.createdAt.toLocaleString()}</span>}
                                        </TableCell>
                                        <TableCell className="text-right">
                                            {r.voided ? (
                                                <Badge variant="outline">{t("pnl.settings.void.voided")}</Badge>
                                            ) : (
                                                <Button size="sm" variant="ghost" onClick={() => setVoiding(r)}>
                                                    <Ban className="w-4 h-4 mr-1" />
                                                    {t("pnl.settings.void.action")}
                                                </Button>
                                            )}
                                        </TableCell>
                                    </TableRow>
                                ))
                            )}
                        </TableBody>
                    </Table>
                </div>
            </CardContent>

            <PnlRateDialog
                open={addKind !== null}
                onOpenChange={(o) => !o && setAddKind(null)}
                kind={addKind ?? "driver_trip"}
                defaultAmount={addKind ? current[addKind]?.amountThb : undefined}
                onSave={onAdd}
            />
            <PnlVoidDialog
                open={voiding !== null}
                onOpenChange={(o) => !o && setVoiding(null)}
                description={voiding ? t("pnl.settings.void.rateDescription", { kind: t(`pnl.settings.rate.${voiding.kind}`), date: voiding.effectiveFrom, amount: fmtThb(voiding.amountThb) }) : ""}
                onConfirm={(reason) => (voiding ? onVoid(voiding.id, reason) : Promise.resolve())}
            />
        </Card>
    );
}
