"use client";

import { useMemo, useState } from "react";
import { AlertTriangle, ChevronDown, ChevronRight, Loader2, ServerCrash } from "lucide-react";
import { format } from "date-fns";
import { useLanguage } from "@/context/language";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { toast } from "sonner";
import {
    assignStandbyCustomerAndPrice,
    type StandbyBillingDiagnostics,
    type StandbyBillingIssue,
    type StandbyIssueReason,
} from "../api/billing";

export interface UnpricedStandbyPanelProps {
    diagnostics: StandbyBillingDiagnostics | null;
    /** Customer options for the repair picker: id + display name. */
    customers: Array<{ id: string; name: string }>;
    /** Called after a record is successfully priced, so the page can reload its rows. */
    onFixed: () => void;
    /** Admins only — the picker is hidden for everyone else. */
    canRepair: boolean;
}

const REASON_VARIANT: Record<StandbyIssueReason, "destructive" | "secondary" | "outline"> = {
    no_customer: "destructive",
    no_rate: "destructive",
    not_computed: "secondary",
    no_ended_at: "outline",
};

function fmtDate(d?: Date): string {
    return d ? format(d, "dd/MM/yyyy HH:mm") : "-";
}

/**
 * Surfaces standby that produced no billable row (ADR 0008 §6). Before this, such records were
 * discarded inside `fetchBillingTripRows`, so unbilled work looked exactly like no work.
 *
 * These rows are deliberately NOT part of the invoice set — they carry no price, and mixing them into
 * the billed rows would put ฿0 lines on an invoice (ADR 0005). Repair is one record at a time on
 * purpose: guessing a customer in bulk produces a confidently wrong invoice (ADR 0008 §7).
 */
export function UnpricedStandbyPanel({
    diagnostics,
    customers,
    onFixed,
    canRepair,
}: UnpricedStandbyPanelProps) {
    const { t } = useLanguage();
    const [expanded, setExpanded] = useState(false);
    const [pickedCustomer, setPickedCustomer] = useState<Record<string, string>>({});
    const [savingId, setSavingId] = useState<string | null>(null);

    const rows = useMemo<StandbyBillingIssue[]>(() => {
        if (!diagnostics) return [];
        return [...diagnostics.unpriced, ...diagnostics.missingEndedAt];
    }, [diagnostics]);

    if (!diagnostics) return null;

    // ADR 0008 §8 — a failed standby query must never look like "there were no standby events".
    if (diagnostics.queryFailed) {
        return (
            <Alert variant="destructive">
                <ServerCrash className="h-4 w-4" />
                <AlertTitle>{t("accounting.billingDocument.standbyIssues.queryFailedTitle")}</AlertTitle>
                <AlertDescription className="space-y-1">
                    <p>{t("accounting.billingDocument.standbyIssues.queryFailedBody")}</p>
                    {diagnostics.queryError && (
                        <p className="text-xs font-mono opacity-80">{diagnostics.queryError}</p>
                    )}
                </AlertDescription>
            </Alert>
        );
    }

    if (rows.length === 0) return null;

    async function handleFix(row: StandbyBillingIssue) {
        const customerId = pickedCustomer[row.id] ?? row.customerId ?? "";
        if (!customerId) return;
        setSavingId(row.id);
        try {
            const res = await assignStandbyCustomerAndPrice(row.id, customerId);
            if (res.blocked) {
                toast.error(
                    t("accounting.billingDocument.standbyIssues.blocked", {
                        invoiceNumber: res.invoiceNumber ?? "-",
                    })
                );
                return;
            }
            if (!res.ok || !res.billingEstimateThb) {
                toast.error(res.error ?? t("accounting.billingDocument.standbyIssues.fixFailed"));
                return;
            }
            toast.success(
                t("accounting.billingDocument.standbyIssues.fixed", {
                    amount: String(res.billingEstimateThb),
                })
            );
            onFixed();
        } catch (e) {
            console.error("[standbyIssues] repair failed:", e);
            toast.error(e instanceof Error ? e.message : String(e));
        } finally {
            setSavingId(null);
        }
    }

    return (
        <Alert className="border-amber-500/50 bg-amber-500/5">
            <AlertTriangle className="h-4 w-4 text-amber-600" />
            <AlertTitle className="flex items-center gap-2">
                {t("accounting.billingDocument.standbyIssues.title", { count: String(rows.length) })}
                <Button
                    variant="ghost"
                    size="sm"
                    className="h-6 px-2"
                    onClick={() => setExpanded((v) => !v)}
                >
                    {expanded ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
                    {expanded
                        ? t("accounting.billingDocument.standbyIssues.hide")
                        : t("accounting.billingDocument.standbyIssues.show")}
                </Button>
            </AlertTitle>
            <AlertDescription className="space-y-3">
                <p className="text-sm">
                    {t("accounting.billingDocument.standbyIssues.body", {
                        unpriced: String(diagnostics.unpriced.length),
                        missingEndedAt: String(diagnostics.missingEndedAt.length),
                    })}
                </p>

                {expanded && (
                    <div className="overflow-x-auto rounded border">
                        <Table>
                            <TableHeader>
                                <TableRow>
                                    <TableHead>{t("accounting.billingDocument.standbyIssues.col.reason")}</TableHead>
                                    <TableHead>{t("accounting.billingDocument.standbyIssues.col.driver")}</TableHead>
                                    <TableHead>{t("accounting.billingDocument.standbyIssues.col.route")}</TableHead>
                                    <TableHead>{t("accounting.billingDocument.standbyIssues.col.endedAt")}</TableHead>
                                    {canRepair && (
                                        <TableHead>{t("accounting.billingDocument.standbyIssues.col.fix")}</TableHead>
                                    )}
                                </TableRow>
                            </TableHeader>
                            <TableBody>
                                {rows.map((row) => {
                                    const selected = pickedCustomer[row.id] ?? row.customerId ?? "";
                                    // A record with no endedAt belongs to no month, so pricing it here would
                                    // still leave it invisible — it needs the date repaired first.
                                    const repairable = canRepair && row.reason !== "no_ended_at";
                                    return (
                                        <TableRow key={row.id}>
                                            <TableCell>
                                                <Badge variant={REASON_VARIANT[row.reason]} className="text-xs">
                                                    {t(`accounting.billingDocument.standbyIssues.reason.${row.reason}`)}
                                                </Badge>
                                            </TableCell>
                                            <TableCell className="text-sm">{row.driverName ?? row.driverId ?? "-"}</TableCell>
                                            <TableCell className="text-sm">
                                                {(row.startLocation ?? "-") + " → " + (row.endLocation ?? "-")}
                                            </TableCell>
                                            <TableCell className="text-sm whitespace-nowrap">
                                                {row.reason === "no_ended_at" ? (
                                                    <span className="text-muted-foreground">
                                                        {t("accounting.billingDocument.standbyIssues.noEndedAtCell", {
                                                            createdAt: fmtDate(row.createdAt),
                                                        })}
                                                    </span>
                                                ) : (
                                                    fmtDate(row.endedAt)
                                                )}
                                            </TableCell>
                                            {canRepair && (
                                                <TableCell>
                                                    {repairable ? (
                                                        <div className="flex items-center gap-2">
                                                            <Select
                                                                value={selected}
                                                                onValueChange={(v) =>
                                                                    setPickedCustomer((prev) => ({ ...prev, [row.id]: v }))
                                                                }
                                                            >
                                                                <SelectTrigger className="w-44 h-8">
                                                                    <SelectValue
                                                                        placeholder={t(
                                                                            "accounting.billingDocument.standbyIssues.pickCustomer"
                                                                        )}
                                                                    />
                                                                </SelectTrigger>
                                                                <SelectContent>
                                                                    {customers.map((c) => (
                                                                        <SelectItem key={c.id} value={c.id}>
                                                                            {c.name}
                                                                        </SelectItem>
                                                                    ))}
                                                                </SelectContent>
                                                            </Select>
                                                            <Button
                                                                size="sm"
                                                                disabled={!selected || savingId === row.id}
                                                                onClick={() => handleFix(row)}
                                                            >
                                                                {savingId === row.id && (
                                                                    <Loader2 className="h-3 w-3 mr-1 animate-spin" />
                                                                )}
                                                                {t("accounting.billingDocument.standbyIssues.priceNow")}
                                                            </Button>
                                                        </div>
                                                    ) : (
                                                        <span className="text-xs text-muted-foreground">
                                                            {t("accounting.billingDocument.standbyIssues.notRepairableHere")}
                                                        </span>
                                                    )}
                                                </TableCell>
                                            )}
                                        </TableRow>
                                    );
                                })}
                            </TableBody>
                        </Table>
                    </div>
                )}
            </AlertDescription>
        </Alert>
    );
}
