"use client";

/**
 * Payroll over Go (T46, issue #66; Appendix E §E.2.2, §E.5 row 12, §E.10 row 10), the only source since
 * TW7. The runs are `['payroll', {period, round, status, driverId}]` keyset pages of 50 with every
 * filter on the server (no 100-row window), polled every 60 s while visible; the summary cards read the
 * first page's `meta.statusCounts`. "Generate payroll" is the `payroll.run` job (`POST /v1/jobs/payroll.run`,
 * followed through `['jobs',id]`), approval and the status moves are Go routes: no callable and no browser
 * write. Writes need `hr:manage_payroll`; `hr:view_payroll` alone reads.
 */
import { useEffect, useEffectEvent, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { format } from "date-fns";
import { Banknote, CheckCircle2, Clock, Eye, Loader2, MoreHorizontal, Play, RefreshCw, User } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { useCan } from "@/features/auth/api/useMe";
import { SearchPicker, formatThb, type PickValue } from "@/features/accounting/components/go/shared";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuLabel,
    DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

import {
    PAYROLL_ROUNDS,
    PAYROLL_STATUSES,
    payrollActions,
    payrollRuns,
    payrollStatusCounts,
    usePayrollRunJob,
    usePayrollRuns,
    type PayrollRound,
    type PayrollRunDTO,
    type PayrollAction,
    type PayrollStatus,
} from "../../api/payroll.go";
import { HrErrorAlert, LoadMoreFooter, StatusBadge, periodLabel } from "./hrShared";
import { PayrollRunDialog, usePayrollActions } from "./PayrollRunDialog";

const ALL = "all";

export function PayrollPage() {
    const { t, language } = useLanguage();
    const canManage = useCan("hr:manage_payroll").allowed;

    const [period, setPeriod] = useState("");
    const [round, setRound] = useState<PayrollRound | typeof ALL>(ALL);
    const [status, setStatus] = useState<PayrollStatus | typeof ALL>(ALL);
    const [driver, setDriver] = useState<PickValue | null>(null);
    const filters = {
        period: period || undefined,
        round: round === ALL ? undefined : round,
        status: status === ALL ? undefined : status,
        driverId: driver?.id,
    };
    const list = usePayrollRuns(filters);
    const runs = payrollRuns(list.data?.pages);
    const counts = payrollStatusCounts(list.data?.pages);
    const total = counts ? (filters.status ? counts[filters.status] : Object.values(counts).reduce((a, b) => a + b, 0)) : undefined;
    const loadedNet = useMemo(() => runs.reduce((acc, r) => acc + (r.netPayThb || 0), 0), [runs]);

    const [openId, setOpenId] = useState<string | null>(null);
    const actions = usePayrollActions();

    return (
        <div className="flex flex-col gap-section">
            <PageHeader
                title={t("nav.payroll")}
                description={t("payroll.subtitle")}
                actions={
                    <>
                        {canManage ? (
                            <>
                                <Button variant="outline" asChild>
                                    <Link href="/app/payroll/config">{t("driverComp.config.title")}</Link>
                                </Button>
                                <Button variant="outline" asChild>
                                    <Link href="/app/payroll/penalties">{t("driverComp.penalty.title")}</Link>
                                </Button>
                            </>
                        ) : null}
                        <Button variant="outline" size="sm" onClick={() => void list.refetch()} disabled={list.isFetching}>
                            <RefreshCw className={`mr-2 h-4 w-4 ${list.isFetching ? "animate-spin" : ""}`} aria-hidden />
                            {t("hr.refresh")}
                        </Button>
                    </>
                }
            />

            {canManage ? <GeneratePanel /> : null}

            <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
                <Card>
                    <CardContent className="flex items-center justify-between p-6">
                        <div>
                            <p className="text-sm font-medium text-muted-foreground">{t("hr.payroll.loadedNetPay")}</p>
                            <h2 className="text-3xl font-bold" data-testid="payroll-loaded-net">
                                {formatThb(loadedNet)}
                            </h2>
                        </div>
                        <Banknote className="h-8 w-8 text-green-500" />
                    </CardContent>
                </Card>
                <Card>
                    <CardContent className="flex items-center justify-between p-6">
                        <div>
                            <p className="text-sm font-medium text-yellow-600">{t("payroll.stats.pendingApproval")}</p>
                            <h2 className="text-3xl font-bold text-yellow-600" data-testid="payroll-pending">
                                {counts ? counts.pending_approval.toLocaleString() : "—"}
                            </h2>
                        </div>
                        <Clock className="h-8 w-8 text-yellow-500" />
                    </CardContent>
                </Card>
                <Card>
                    <CardContent className="flex items-center justify-between p-6">
                        <div>
                            <p className="text-sm font-medium text-blue-600">{t("payroll.stats.successfullyPaid")}</p>
                            <h2 className="text-3xl font-bold text-blue-600" data-testid="payroll-paid">
                                {counts ? counts.paid.toLocaleString() : "—"}
                            </h2>
                        </div>
                        <CheckCircle2 className="h-8 w-8 text-blue-500" />
                    </CardContent>
                </Card>
            </div>

            <div className="flex flex-col gap-4 rounded-lg border border-border/50 bg-card/50 p-4 lg:flex-row lg:items-end">
                <div className="flex flex-wrap items-end gap-3">
                    <label className="flex flex-col gap-1 text-sm font-medium">
                        {t("payroll.table.period")}
                        <Input type="month" value={period} onChange={(e) => setPeriod(e.target.value)} className="w-[170px]" aria-label={t("payroll.table.period")} />
                    </label>
                    <label className="flex flex-col gap-1 text-sm font-medium">
                        {t("hr.payroll.round")}
                        <Select value={round} onValueChange={(v) => setRound(v as PayrollRound | typeof ALL)}>
                            <SelectTrigger className="w-[150px]" aria-label={t("hr.payroll.round")}>
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value={ALL}>{t("payroll.filter.all")}</SelectItem>
                                {PAYROLL_ROUNDS.map((r) => (
                                    <SelectItem key={r} value={r}>
                                        {t(`hr.payroll.round.${r}`)}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </label>
                    <div className="flex flex-col gap-1 text-sm font-medium">
                        {t("payroll.table.driver")}
                        <SearchPicker kind="driver" value={driver} onChange={setDriver} placeholder={t("hr.allDrivers")} clearLabel={t("hr.allDrivers")} />
                    </div>
                </div>
                <div className="flex gap-2 overflow-x-auto pb-1">
                    {[ALL, ...PAYROLL_STATUSES].map((s) => (
                        <Button
                            key={s}
                            variant={status === s ? "secondary" : "ghost"}
                            size="sm"
                            onClick={() => setStatus(s as PayrollStatus | typeof ALL)}
                            className="whitespace-nowrap"
                        >
                            {s === ALL ? t("payroll.filter.all") : t(`payroll.status.${s}`)}
                            {s !== ALL && counts ? <span className="ml-1 text-xs text-muted-foreground">({counts[s as PayrollStatus]})</span> : null}
                        </Button>
                    ))}
                </div>
            </div>

            <HrErrorAlert error={list.error} title={t("payroll.toast.failedLoad")} />

            <div className="overflow-hidden rounded-lg border bg-card">
                <Table>
                    <TableHeader className="bg-muted/50">
                        <TableRow>
                            <TableHead>{t("payroll.table.driver")}</TableHead>
                            <TableHead>{t("payroll.table.period")}</TableHead>
                            <TableHead className="text-right">{t("payroll.table.earnings")}</TableHead>
                            <TableHead className="text-right">{t("payroll.table.deductions")}</TableHead>
                            <TableHead className="text-right">{t("payroll.table.netPay")}</TableHead>
                            <TableHead>{t("leaveRequests.table.status")}</TableHead>
                            <TableHead className="text-right">{t("common.actions")}</TableHead>
                        </TableRow>
                    </TableHeader>
                    <TableBody>
                        {list.isPending ? (
                            <TableRow>
                                <TableCell colSpan={7} className="h-32 text-center">
                                    <div className="flex flex-col items-center justify-center gap-2" role="status" aria-busy="true">
                                        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
                                        <p className="text-sm text-muted-foreground">{t("payroll.loading")}</p>
                                    </div>
                                </TableCell>
                            </TableRow>
                        ) : runs.length === 0 ? (
                            <TableRow>
                                <TableCell colSpan={7} className="h-32 text-center text-muted-foreground">
                                    {t("payroll.noRecords")}
                                </TableCell>
                            </TableRow>
                        ) : (
                            runs.map((run) => (
                                <PayrollRow
                                    key={run.id}
                                    run={run}
                                    language={language}
                                    canManage={canManage}
                                    onOpen={() => setOpenId(run.id)}
                                    onAction={(a) => actions.request(run, a)}
                                />
                            ))
                        )}
                    </TableBody>
                </Table>
                <LoadMoreFooter
                    shown={runs.length}
                    total={total}
                    hasNextPage={Boolean(list.hasNextPage)}
                    fetchingNext={list.isFetchingNextPage}
                    onLoadMore={() => void list.fetchNextPage()}
                />
            </div>

            <PayrollRunDialog id={openId} onClose={() => setOpenId(null)} canManage={canManage} actions={actions} />
            {actions.dialogs}
        </div>
    );
}

function PayrollRow({
    run,
    language,
    canManage,
    onOpen,
    onAction,
}: {
    run: PayrollRunDTO;
    language: string;
    canManage: boolean;
    onOpen: () => void;
    onAction: (a: PayrollAction) => void;
}) {
    const { t } = useLanguage();
    const actionsFor = canManage ? payrollActions(run.status) : [];
    return (
        <TableRow className="cursor-pointer transition-colors hover:bg-muted/50" onClick={onOpen}>
            <TableCell>
                <div className="flex items-center gap-2">
                    <div className="flex h-8 w-8 items-center justify-center rounded-full bg-muted">
                        <User className="h-4 w-4 text-muted-foreground" />
                    </div>
                    <p className="text-sm font-medium">{run.driverName || t("payroll.unknownDriver")}</p>
                </div>
            </TableCell>
            <TableCell>
                <div className="text-xs">
                    <p className="font-medium">{periodLabel(run.payPeriod, language)}</p>
                    <p className="text-muted-foreground">{t(`hr.payroll.round.${run.round}`)}</p>
                </div>
            </TableCell>
            <TableCell className="text-right text-sm font-medium text-green-700 tabular-nums">{formatThb(run.totalEarningsThb)}</TableCell>
            <TableCell className="text-right text-sm font-medium text-red-700 tabular-nums">{formatThb(run.totalDeductionsThb)}</TableCell>
            <TableCell className="text-right text-sm font-bold tabular-nums">{formatThb(run.netPayThb)}</TableCell>
            <TableCell>
                <StatusBadge status={run.status} labelKey={`payroll.status.${run.status}`} />
            </TableCell>
            <TableCell className="text-right">
                <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                        <Button
                            variant="ghost"
                            size="icon"
                            aria-label={`${t("payroll.managePayroll")} ${run.driverName ?? ""}`.trim()}
                            onClick={(e) => e.stopPropagation()}
                        >
                            <MoreHorizontal className="h-4 w-4" />
                        </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end" onClick={(e) => e.stopPropagation()}>
                        <DropdownMenuLabel>{t("payroll.managePayroll")}</DropdownMenuLabel>
                        <DropdownMenuItem onClick={onOpen}>
                            <Eye className="mr-2 h-4 w-4" /> {t("payroll.viewDetails")}
                        </DropdownMenuItem>
                        {actionsFor.map((a) => (
                            <DropdownMenuItem key={a} onClick={() => onAction(a)} className={a === "cancel" ? "text-red-600" : undefined}>
                                {t(`hr.payroll.action.${a}`)}
                            </DropdownMenuItem>
                        ))}
                    </DropdownMenuContent>
                </DropdownMenu>
            </TableCell>
        </TableRow>
    );
}

/** Month and round of a generation, then the `payroll.run` job's progress and result. */
function GeneratePanel() {
    const { t } = useLanguage();
    const [genPeriod, setGenPeriod] = useState(() => format(new Date(), "yyyy-MM"));
    const [genRound, setGenRound] = useState<PayrollRound>("R1");
    const job = usePayrollRunJob();
    const announced = useRef<string | null>(null);
    const { succeeded, jobId, result } = job;
    // Reads the active language when the job finishes; a language toggle never repeats the toast.
    const announce = useEffectEvent((r: typeof result) =>
        toast.success(t("payroll.generated", { written: r?.written ?? 0, skipped: r?.skippedApproved ?? 0 }))
    );
    useEffect(() => {
        if (!succeeded || !jobId || announced.current === jobId) return;
        announced.current = jobId;
        announce(result);
    }, [succeeded, jobId, result]);

    const start = async () => {
        if (!/^\d{4}-\d{2}$/.test(genPeriod)) {
            toast.error(t("hr.payroll.periodRequired"));
            return;
        }
        await job.start({ period: genPeriod, round: genRound });
    };

    return (
        <Card>
            <CardContent className="flex flex-col gap-3 p-4">
                <div className="flex flex-wrap items-end gap-3">
                    <label className="flex flex-col gap-1 text-sm font-medium">
                        {t("hr.payroll.generatePeriod")}
                        <Input
                            type="month"
                            value={genPeriod}
                            onChange={(e) => setGenPeriod(e.target.value)}
                            className="w-[170px]"
                            aria-label={t("hr.payroll.generatePeriod")}
                        />
                    </label>
                    <label className="flex flex-col gap-1 text-sm font-medium">
                        {t("hr.payroll.round")}
                        <Select value={genRound} onValueChange={(v) => setGenRound(v as PayrollRound)}>
                            <SelectTrigger className="w-[150px]" aria-label={t("hr.payroll.generateRound")}>
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                {PAYROLL_ROUNDS.map((r) => (
                                    <SelectItem key={r} value={r}>
                                        {t(`hr.payroll.round.${r}`)}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </label>
                    <Button onClick={() => void start()} disabled={job.running}>
                        {job.running ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Play className="mr-2 h-4 w-4" />}
                        {t("payroll.generatePayroll")}
                    </Button>
                </div>
                <p className="text-xs text-muted-foreground">{t("hr.payroll.generateHint")}</p>
                {job.running ? (
                    <p className="text-sm text-muted-foreground" role="status">
                        {job.joined ? t("hr.payroll.jobJoined") : t("hr.payroll.jobRunning")}
                    </p>
                ) : null}
                {job.error ? (
                    <div className="space-y-1">
                        <HrErrorAlert error={job.error} title={t("payroll.generateError")} />
                        {isNoConfig(job.error) ? (
                            <Link href="/app/payroll/config" className="text-sm text-primary underline">
                                {t("driverComp.config.title")}
                            </Link>
                        ) : null}
                    </div>
                ) : null}
                {job.succeeded && job.result ? (
                    <p className="text-sm text-green-700" role="status" data-testid="payroll-job-result">
                        {t("payroll.generated", { written: job.result.written, skipped: job.result.skippedApproved })}
                    </p>
                ) : null}
            </CardContent>
        </Card>
    );
}

function isNoConfig(error: unknown): boolean {
    return typeof error === "object" && error !== null && (error as { details?: { reason?: unknown } }).details?.reason === "no_compensation_config";
}
