"use client";

import { Fragment, useMemo, useState } from "react";
import { Ban, ChevronDown, ChevronRight, MoreHorizontal, Plus } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useLanguage } from "@/context/language";
import { bangkokDateKey } from "@/lib/compensationCompute";
import { activeFixedCostVersion, fixedCostMonthlyAmount } from "@/lib/pnlCompute";
import type { PnlFixedCostParsed, PnlFixedCostVersion } from "@/validate/pnlSettingsSchema";
import { fixedCategoryLabel, fmtThb } from "../utils/format";
import { PnlFixedCostDialog, type FixedCostDialogMode } from "./PnlFixedCostDialog";
import { PnlVoidDialog } from "./PnlVoidDialog";

interface Item {
    itemId: string;
    versions: PnlFixedCostVersion[];
    /** Latest non-voided version by effective month — the basis for "change" / "stop". */
    latest: PnlFixedCostVersion;
    monthlyNow: number;
    status: "active" | "stopped" | "ended" | "future";
}

/** Fixed-cost list (R2, R4): one row per item, its versions on expand; append / stop / void only. */
export function PnlFixedCostSettings({
    versions,
    trucks,
    onSave,
    onVoid,
}: {
    versions: PnlFixedCostVersion[];
    trucks: Array<{ id: string; plate: string }>;
    onSave: (values: PnlFixedCostParsed, itemId?: string) => Promise<void>;
    onVoid: (id: string, reason: string) => Promise<void>;
}) {
    const { t } = useLanguage();
    const [dialog, setDialog] = useState<{ mode: FixedCostDialogMode; item?: Item } | null>(null);
    const [voiding, setVoiding] = useState<PnlFixedCostVersion | null>(null);
    const [expanded, setExpanded] = useState<Set<string>>(new Set());
    const monthNow = bangkokDateKey(new Date()).slice(0, 7);

    const items: Item[] = useMemo(() => {
        const byItem = new Map<string, PnlFixedCostVersion[]>();
        versions.forEach((v) => byItem.set(v.itemId, [...(byItem.get(v.itemId) ?? []), v]));
        return [...byItem.entries()]
            .map(([itemId, vs]) => {
                const live = vs.filter((v) => !v.voided).sort((a, b) => b.effectiveFromMonth.localeCompare(a.effectiveFromMonth));
                const latest = live[0] ?? vs[0];
                const active = activeFixedCostVersion(vs, monthNow);
                const status: Item["status"] = !active
                    ? "future"
                    : active.isStop
                      ? "stopped"
                      : active.effectiveToMonth && active.effectiveToMonth < monthNow
                        ? "ended"
                        : "active";
                return { itemId, versions: vs, latest, monthlyNow: fixedCostMonthlyAmount(vs, monthNow), status };
            })
            .sort((a, b) => a.latest.name.localeCompare(b.latest.name));
    }, [versions, monthNow]);

    const customLabels = useMemo(
        () => [...new Set(versions.filter((v) => v.category === "custom" && v.categoryLabel).map((v) => v.categoryLabel as string))].sort(),
        [versions],
    );

    const toggle = (id: string) =>
        setExpanded((s) => {
            const n = new Set(s);
            if (n.has(id)) n.delete(id);
            else n.add(id);
            return n;
        });

    return (
        <Card>
            <CardHeader className="flex flex-row items-center justify-between space-y-0">
                <CardTitle className="text-base">{t("pnl.settings.fixed.title")}</CardTitle>
                <Button size="sm" onClick={() => setDialog({ mode: "new" })}>
                    <Plus className="w-4 h-4 mr-1" />
                    {t("pnl.settings.fixed.add")}
                </Button>
            </CardHeader>
            <CardContent>
                <p className="text-xs text-muted-foreground mb-3">{t("pnl.settings.fixed.renewalNote")}</p>
                <div className="overflow-x-auto rounded-md border">
                    <Table>
                        <TableHeader>
                            <TableRow>
                                <TableHead className="w-8" />
                                <TableHead>{t("pnl.settings.fixed.name")}</TableHead>
                                <TableHead>{t("pnl.settings.fixed.categoryLabel")}</TableHead>
                                <TableHead>{t("pnl.settings.fixed.scopeLabel")}</TableHead>
                                <TableHead className="text-right">{t("pnl.settings.fixed.amount")}</TableHead>
                                <TableHead className="text-right">{t("pnl.settings.fixed.monthlyNow")}</TableHead>
                                <TableHead>{t("pnl.settings.fixed.period")}</TableHead>
                                <TableHead>{t("pnl.settings.fixed.statusLabel")}</TableHead>
                                <TableHead />
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {items.length === 0 ? (
                                <TableRow>
                                    <TableCell colSpan={9} className="text-center text-muted-foreground py-6">{t("pnl.settings.empty")}</TableCell>
                                </TableRow>
                            ) : (
                                items.map((it) => (
                                    <Fragment key={it.itemId}>
                                        <TableRow>
                                            <TableCell>
                                                {/* History only when there is history: a one-version item is a single row. */}
                                                {it.versions.length > 1 && (
                                                    <Button size="icon" variant="ghost" className="h-6 w-6" onClick={() => toggle(it.itemId)} title={t("pnl.settings.fixed.history")}>
                                                        {expanded.has(it.itemId) ? <ChevronDown className="w-4 h-4" /> : <ChevronRight className="w-4 h-4" />}
                                                    </Button>
                                                )}
                                            </TableCell>
                                            <TableCell className="font-medium">
                                                {it.latest.name}
                                                {it.versions.length > 1 && (
                                                    <span className="block text-xs text-muted-foreground">{t("pnl.settings.fixed.versionCount", { count: it.versions.length })}</span>
                                                )}
                                            </TableCell>
                                            <TableCell>{fixedCategoryLabel(it.latest.category, it.latest.categoryLabel, t)}</TableCell>
                                            <TableCell>{it.latest.scope === "truck" ? it.latest.truckPlate ?? it.latest.truckId : t("pnl.settings.fixed.scope.company")}</TableCell>
                                            <TableCell className="text-right tabular-nums">
                                                {fmtThb(it.latest.amountThb)}
                                                <span className="block text-xs text-muted-foreground">{t(`pnl.settings.fixed.frequency.${it.latest.frequency}`)}</span>
                                            </TableCell>
                                            <TableCell className="text-right tabular-nums">{fmtThb(it.monthlyNow)}</TableCell>
                                            <TableCell className="whitespace-nowrap">{it.latest.effectiveFromMonth} → {it.latest.effectiveToMonth ?? "…"}</TableCell>
                                            <TableCell>
                                                <Badge variant={it.status === "active" ? "default" : "outline"}>{t(`pnl.settings.fixed.status.${it.status}`)}</Badge>
                                            </TableCell>
                                            <TableCell className="text-right">
                                                <DropdownMenu>
                                                    <DropdownMenuTrigger asChild>
                                                        <Button size="icon" variant="ghost" className="h-8 w-8"><MoreHorizontal className="w-4 h-4" /></Button>
                                                    </DropdownMenuTrigger>
                                                    <DropdownMenuContent align="end">
                                                        <DropdownMenuItem onClick={() => setDialog({ mode: "change", item: it })}>{t("pnl.settings.fixed.dialog.change")}</DropdownMenuItem>
                                                        <DropdownMenuItem onClick={() => setDialog({ mode: "stop", item: it })}>{t("pnl.settings.fixed.dialog.stop")}</DropdownMenuItem>
                                                        {!it.latest.voided && (
                                                            <DropdownMenuItem className="text-destructive" onClick={() => setVoiding(it.latest)}>
                                                                <Ban className="w-4 h-4 mr-1" />
                                                                {it.versions.length > 1 ? t("pnl.settings.fixed.voidLatest") : t("pnl.settings.void.action")}
                                                            </DropdownMenuItem>
                                                        )}
                                                    </DropdownMenuContent>
                                                </DropdownMenu>
                                            </TableCell>
                                        </TableRow>
                                        {expanded.has(it.itemId) && it.versions.length > 1 &&
                                            [...it.versions]
                                                .sort((a, b) => b.effectiveFromMonth.localeCompare(a.effectiveFromMonth))
                                                .map((v) => (
                                                    <TableRow key={v.id} className={`bg-muted/30 text-sm ${v.voided ? "opacity-60" : ""}`}>
                                                        <TableCell />
                                                        <TableCell colSpan={3} className={v.voided ? "line-through" : ""}>
                                                            {v.isStop ? t("pnl.settings.fixed.stopVersion") : `${v.name} · ${t(`pnl.settings.fixed.frequency.${v.frequency}`)}`}
                                                            {v.note && <span className="block text-xs text-muted-foreground">{v.note}</span>}
                                                            {v.voided && <span className="block text-xs text-destructive">{t("pnl.settings.void.voidedNote", { reason: v.voidedReason ?? "" })}</span>}
                                                        </TableCell>
                                                        <TableCell className={`text-right tabular-nums ${v.voided ? "line-through" : ""}`}>{v.isStop ? "—" : fmtThb(v.amountThb)}</TableCell>
                                                        <TableCell />
                                                        <TableCell className="whitespace-nowrap">{v.effectiveFromMonth} → {v.effectiveToMonth ?? "…"}</TableCell>
                                                        <TableCell className="text-xs text-muted-foreground">{v.createdByName ?? v.createdBy ?? ""}</TableCell>
                                                        <TableCell className="text-right">
                                                            {!v.voided && (
                                                                <Button size="sm" variant="ghost" onClick={() => setVoiding(v)}>
                                                                    <Ban className="w-4 h-4 mr-1" />
                                                                    {t("pnl.settings.void.action")}
                                                                </Button>
                                                            )}
                                                        </TableCell>
                                                    </TableRow>
                                                ))}
                                    </Fragment>
                                ))
                            )}
                        </TableBody>
                    </Table>
                </div>
            </CardContent>

            <PnlFixedCostDialog
                open={dialog !== null}
                onOpenChange={(o) => !o && setDialog(null)}
                mode={dialog?.mode ?? "new"}
                latest={dialog?.item?.latest}
                trucks={trucks}
                customLabels={customLabels}
                onSave={(values) => onSave(values, dialog?.item?.itemId)}
            />
            <PnlVoidDialog
                open={voiding !== null}
                onOpenChange={(o) => !o && setVoiding(null)}
                description={voiding ? t("pnl.settings.void.fixedDescription", { name: voiding.name, month: voiding.effectiveFromMonth }) : ""}
                onConfirm={(reason) => (voiding ? onVoid(voiding.id, reason) : Promise.resolve())}
            />
        </Card>
    );
}
