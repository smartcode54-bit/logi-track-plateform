"use client";

/**
 * One payroll run over Go (T46): `['payroll','detail',id]` (`GET /v1/payroll/{id}`) with its line items
 * (ADR 0013: quantity x unit rate where the engine gives them) and penalty applications, and the actions of
 * its status behind a confirmation (`usePayrollActions`): approve (`POST .../approve`, which also commits the
 * penalty installments and the ledger row) and the moves of `POST .../status` (submit for approval, mark as
 * paid with the payment day and method, cancel, re-open as draft).
 */
import { useState, type ReactNode } from "react";
import { format } from "date-fns";
import { ArrowDownCircle, ArrowUpCircle, Banknote, Calendar, Loader2, User } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { formatThb } from "@/features/accounting/components/go/shared";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";

import {
    payrollActions,
    useApprovePayrollRun,
    usePayrollRun,
    useSetPayrollStatus,
    type PayrollAction,
    type PayrollLineItemDTO,
    type PayrollRunDTO,
    type PayrollStatusMove,
} from "../../api/payroll.go";
import { HrErrorAlert, StatusBadge, dayLabel, periodLabel } from "./hrShared";

const STATUS_OF: Record<Exclude<PayrollAction, "approve">, PayrollStatusMove> = {
    submit: "pending_approval",
    pay: "paid",
    cancel: "cancelled",
    reopen: "draft",
};

/** The confirmation of a run action, and the mutation behind it. */
export function usePayrollActions() {
    const { t, language } = useLanguage();
    const approve = useApprovePayrollRun();
    const setStatus = useSetPayrollStatus();
    const [pending, setPending] = useState<{ run: PayrollRunDTO; action: PayrollAction } | null>(null);
    const [payDate, setPayDate] = useState("");
    const [method, setMethod] = useState("");
    const [error, setError] = useState<unknown>(null);
    const busy = approve.isPending || setStatus.isPending;

    const request = (run: PayrollRunDTO, action: PayrollAction) => {
        setPending({ run, action });
        setError(null);
        setPayDate(format(new Date(), "yyyy-MM-dd"));
        setMethod(run.paymentMethod ?? "transfer");
    };

    const confirm = async () => {
        if (!pending) return;
        setError(null);
        try {
            if (pending.action === "approve") {
                await approve.mutateAsync({ id: pending.run.id });
            } else {
                const move = STATUS_OF[pending.action];
                await setStatus.mutateAsync({
                    id: pending.run.id,
                    status: move,
                    ...(move === "paid" ? { paymentDate: payDate || undefined, paymentMethod: method.trim() || undefined } : {}),
                });
            }
            toast.success(t(`hr.payroll.done.${pending.action}`));
            setPending(null);
        } catch (e) {
            setError(e);
        }
    };

    const dialogs = (
        <Dialog open={pending !== null} onOpenChange={(open) => !open && !busy && setPending(null)}>
            <DialogContent className="sm:max-w-[460px]">
                <DialogHeader>
                    <DialogTitle>{pending ? t(`hr.payroll.action.${pending.action}`) : ""}</DialogTitle>
                    <DialogDescription>
                        {pending
                            ? t(`hr.payroll.confirm.${pending.action}`, {
                                  driver: pending.run.driverName ?? t("payroll.unknownDriver"),
                                  period: `${periodLabel(pending.run.payPeriod, language)} ${t(`hr.payroll.round.${pending.run.round}`)}`,
                                  net: formatThb(pending.run.netPayThb),
                              })
                            : ""}
                    </DialogDescription>
                </DialogHeader>
                {pending?.action === "pay" ? (
                    <div className="grid grid-cols-2 gap-3">
                        <label className="flex flex-col gap-1 text-sm font-medium">
                            {t("hr.payroll.paymentDate")}
                            <Input type="date" value={payDate} onChange={(e) => setPayDate(e.target.value)} />
                        </label>
                        <label className="flex flex-col gap-1 text-sm font-medium">
                            {t("hr.payroll.paymentMethod")}
                            <Input value={method} maxLength={100} onChange={(e) => setMethod(e.target.value)} />
                        </label>
                    </div>
                ) : null}
                <HrErrorAlert error={error} />
                <DialogFooter>
                    <Button variant="outline" onClick={() => setPending(null)} disabled={busy}>
                        {t("hr.cancel")}
                    </Button>
                    <Button
                        variant={pending?.action === "cancel" ? "destructive" : "default"}
                        onClick={() => void confirm()}
                        disabled={busy}
                    >
                        {busy ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                        {pending ? t(`hr.payroll.action.${pending.action}`) : ""}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );

    return { request, dialogs, busy };
}

function lineBreakdown(l: PayrollLineItemDTO): string | null {
    if (l.quantity != null && l.unitRateThb != null) return `${l.quantity.toLocaleString()} × ${formatThb(l.unitRateThb)}`;
    return l.description ?? null;
}

function Lines({ title, icon, lines, sign, tone, empty }: { title: string; icon: ReactNode; lines: PayrollLineItemDTO[]; sign: string; tone: string; empty: string }) {
    return (
        <div>
            <h4 className={`mb-2 flex items-center gap-2 border-b pb-1 font-semibold ${tone}`}>
                {icon} {title}
            </h4>
            {lines.length === 0 ? (
                <p className="text-sm italic text-muted-foreground">{empty}</p>
            ) : (
                <ul className="space-y-2">
                    {lines.map((l) => {
                        const detail = lineBreakdown(l);
                        return (
                            <li key={l.lineNo} className="flex items-center justify-between gap-3 text-sm">
                                <div>
                                    <span className="font-medium">{l.name}</span>
                                    {detail ? <span className="block text-xs text-muted-foreground">{detail}</span> : null}
                                </div>
                                <span className={`font-medium tabular-nums ${tone}`}>
                                    {sign}
                                    {formatThb(l.amountThb)}
                                </span>
                            </li>
                        );
                    })}
                </ul>
            )}
        </div>
    );
}

export function PayrollRunDialog({
    id,
    onClose,
    canManage,
    actions,
}: {
    id: string | null;
    onClose: () => void;
    canManage: boolean;
    actions: ReturnType<typeof usePayrollActions>;
}) {
    const { t, language } = useLanguage();
    const detail = usePayrollRun(id);
    const run = detail.data;
    const earnings = run?.lineItems.filter((l) => l.type === "earning" && l.amountThb !== 0) ?? [];
    const deductions = run?.lineItems.filter((l) => l.type === "deduction") ?? [];
    const available = run && canManage ? payrollActions(run.status) : [];

    return (
        <Dialog open={id !== null} onOpenChange={(open) => !open && onClose()}>
            <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-[600px]">
                <DialogHeader className="mb-2 border-b pb-4">
                    <div className="flex items-start justify-between gap-3">
                        <div>
                            <DialogTitle className="flex items-center gap-2 text-xl">
                                <Banknote className="h-6 w-6 text-green-600" />
                                {t("payroll.dialog.title")}
                            </DialogTitle>
                            <DialogDescription className="mt-1">
                                {run ? `${t("payroll.dialog.generatedOn")} ${dayLabel(run.createdAt, language, "PPP")}` : t("payroll.loading")}
                            </DialogDescription>
                        </div>
                        {run ? <StatusBadge status={run.status} labelKey={`payroll.status.${run.status}`} className="px-3 py-1 text-sm" /> : null}
                    </div>
                </DialogHeader>

                {detail.isPending ? (
                    <div className="flex justify-center py-10" role="status" aria-busy="true">
                        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
                    </div>
                ) : null}
                <HrErrorAlert error={detail.error} />

                {run ? (
                    <div className="space-y-6">
                        <div className="grid grid-cols-2 gap-4 rounded-lg border bg-muted/30 p-4">
                            <div className="space-y-1">
                                <span className="text-xs font-semibold uppercase text-muted-foreground">{t("payroll.dialog.driver")}</span>
                                <div className="flex items-center gap-2">
                                    <User className="h-4 w-4 text-muted-foreground" />
                                    <span className="font-medium">{run.driverName ?? t("payroll.unknownDriver")}</span>
                                </div>
                            </div>
                            <div className="space-y-1">
                                <span className="text-xs font-semibold uppercase text-muted-foreground">{t("payroll.dialog.payPeriod")}</span>
                                <div className="flex items-center gap-2">
                                    <Calendar className="h-4 w-4 text-muted-foreground" />
                                    <span className="text-sm font-medium">
                                        {periodLabel(run.payPeriod, language)} · {t(`hr.payroll.round.${run.round}`)}
                                    </span>
                                </div>
                            </div>
                        </div>

                        <Lines
                            title={t("payroll.dialog.earnings")}
                            icon={<ArrowUpCircle className="h-4 w-4" />}
                            lines={earnings}
                            sign="+"
                            tone="text-green-700"
                            empty={t("payroll.dialog.noEarnings")}
                        />
                        <Lines
                            title={t("payroll.dialog.deductions")}
                            icon={<ArrowDownCircle className="h-4 w-4" />}
                            lines={deductions}
                            sign="-"
                            tone="text-red-700"
                            empty={t("payroll.dialog.noDeductions")}
                        />

                        <div className="space-y-2 rounded-lg border bg-muted p-4">
                            <div className="flex justify-between text-sm font-medium text-muted-foreground">
                                <span>{t("payroll.dialog.totalEarnings")}</span>
                                <span className="tabular-nums">{formatThb(run.totalEarningsThb)}</span>
                            </div>
                            <div className="flex justify-between border-b border-border/50 pb-2 text-sm font-medium text-muted-foreground">
                                <span>{t("payroll.dialog.totalDeductions")}</span>
                                <span className="tabular-nums">-{formatThb(run.totalDeductionsThb)}</span>
                            </div>
                            <div className="flex justify-between pt-1 text-lg font-bold">
                                <span>{t("payroll.dialog.netPay")}</span>
                                <span className="text-primary tabular-nums" data-testid="payroll-detail-net">
                                    {formatThb(run.netPayThb)}
                                </span>
                            </div>
                        </div>

                        {run.paymentDate || run.paymentMethod ? (
                            <div className="rounded border border-green-100 bg-green-50/50 p-3 text-sm text-muted-foreground dark:border-green-900 dark:bg-green-950/20">
                                {run.paymentDate ? (
                                    <p>
                                        {t("payroll.dialog.paidOn")} <span className="font-medium text-foreground">{dayLabel(run.paymentDate, language, "PPP")}</span>
                                    </p>
                                ) : null}
                                {run.paymentMethod ? (
                                    <p>
                                        {t("payroll.dialog.method")} <span className="font-medium text-foreground">{run.paymentMethod}</span>
                                    </p>
                                ) : null}
                            </div>
                        ) : null}
                        {run.penaltyApplications.length > 0 && run.status !== "approved" && run.status !== "paid" ? (
                            <p className="text-xs text-muted-foreground">{t("hr.payroll.penaltiesOnApprove", { count: run.penaltyApplications.length })}</p>
                        ) : null}
                    </div>
                ) : null}

                <DialogFooter className="mt-4 gap-2 border-t pt-4">
                    <Button variant="ghost" onClick={onClose}>
                        {t("payroll.dialog.close")}
                    </Button>
                    {run
                        ? available.map((a) => (
                              <Button
                                  key={a}
                                  variant={a === "cancel" ? "destructive" : a === "approve" || a === "pay" ? "default" : "outline"}
                                  onClick={() => actions.request(run, a)}
                                  disabled={actions.busy}
                              >
                                  {t(`hr.payroll.action.${a}`)}
                              </Button>
                          ))
                        : null}
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
