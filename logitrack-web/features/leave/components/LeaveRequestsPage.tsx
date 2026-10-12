"use client";

/**
 * Leave requests over Go (T46, issue #66; Appendix E §E.2.2, §E.5 row 10), rendered while the `hr` web flag
 * is on. The list is `['leave', {status, driverId}]`: keyset pages of 50 with the filters on the server,
 * polled every 60 s while visible, instead of a listener on the whole collection; the summary cards read the
 * first page's `meta.statusCounts`. Decisions are `POST /v1/leave-requests/{id}/approve|reject|cancel`
 * (`hr:manage_leave`): approve and reject a pending request, cancel an approved one, a rejection with its
 * reason. While the installed 3.x apps still write leave requests in Firestore a decision may answer
 * `202` (written back, not mirrored yet): the page says so and the poll shows the new status once mirrored.
 */
import { useState } from "react";
import { Ban, CheckCircle2, Clock, FileText, Loader2, MoreHorizontal, Paperclip, RefreshCw, User, XCircle } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { useCan } from "@/features/auth/api/useMe";
import { SearchPicker, type PickValue } from "@/features/accounting/components/go/shared";
import { HrErrorAlert, LoadMoreFooter, StatusBadge, dayLabel } from "@/features/driver-compensation/components/go/hrShared";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuLabel,
    DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";

import {
    LEAVE_STATUSES,
    evidenceHref,
    isWriteBackPending,
    leaveActions,
    leaveRows,
    leaveStatusCounts,
    useDecideLeave,
    useLeaveRequests,
    type LeaveAction,
    type LeaveRequestDTO,
    type LeaveStatus,
} from "../api/leave.go";

const ALL = "all";

export function LeaveRequestsPage() {
    const { t, language } = useLanguage();
    const canManage = useCan("hr:manage_leave").allowed;
    const [status, setStatus] = useState<LeaveStatus | typeof ALL>(ALL);
    const [driver, setDriver] = useState<PickValue | null>(null);
    const list = useLeaveRequests({ status: status === ALL ? undefined : status, driverId: driver?.id });
    const rows = leaveRows(list.data?.pages);
    const counts = leaveStatusCounts(list.data?.pages);
    const allCount = counts ? Object.values(counts).reduce((a, b) => a + b, 0) : undefined;
    const total = counts ? (status === ALL ? allCount : counts[status]) : undefined;

    const [selected, setSelected] = useState<{ request: LeaveRequestDTO; action?: LeaveAction } | null>(null);

    return (
        <div className="container mx-auto max-w-[1400px] space-y-6 p-6">
            <div className="flex flex-col items-start justify-between gap-4 md:flex-row md:items-center">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">{t("nav.leaveRequests")}</h1>
                    <p className="mt-1 text-muted-foreground">{t("leaveRequests.subtitle")}</p>
                </div>
                <Button variant="outline" size="sm" onClick={() => void list.refetch()} disabled={list.isFetching}>
                    <RefreshCw className={`mr-2 h-4 w-4 ${list.isFetching ? "animate-spin" : ""}`} />
                    {t("hr.refresh")}
                </Button>
            </div>

            <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
                <Card>
                    <CardContent className="flex items-center justify-between p-6">
                        <div>
                            <p className="text-sm font-medium text-muted-foreground">{t("leaveRequests.stats.totalRequests")}</p>
                            <h2 className="text-3xl font-bold" data-testid="leave-total">
                                {allCount !== undefined ? allCount.toLocaleString() : "—"}
                            </h2>
                        </div>
                        <FileText className="h-8 w-8 text-blue-500" />
                    </CardContent>
                </Card>
                <Card className="border-yellow-500/20 bg-yellow-50/30 dark:bg-yellow-950/10">
                    <CardContent className="flex items-center justify-between p-6">
                        <div>
                            <p className="text-sm font-medium text-yellow-600">{t("leaveRequests.stats.pendingReview")}</p>
                            <h2 className="text-3xl font-bold text-yellow-600" data-testid="leave-pending">
                                {counts ? counts.pending.toLocaleString() : "—"}
                            </h2>
                        </div>
                        <Clock className="h-8 w-8 text-yellow-500" />
                    </CardContent>
                </Card>
                <Card className="border-green-500/20 bg-green-50/30 dark:bg-green-950/10">
                    <CardContent className="flex items-center justify-between p-6">
                        <div>
                            <p className="text-sm font-medium text-green-600">{t("hr.leave.approvedTotal")}</p>
                            <h2 className="text-3xl font-bold text-green-600" data-testid="leave-approved">
                                {counts ? counts.approved.toLocaleString() : "—"}
                            </h2>
                        </div>
                        <CheckCircle2 className="h-8 w-8 text-green-500" />
                    </CardContent>
                </Card>
            </div>

            <div className="flex flex-col gap-4 rounded-lg border border-border/50 bg-card/50 p-4 sm:flex-row sm:items-end">
                <div className="flex flex-col gap-1 text-sm font-medium">
                    {t("leaveRequests.table.driver")}
                    <SearchPicker kind="driver" value={driver} onChange={setDriver} placeholder={t("hr.allDrivers")} clearLabel={t("hr.allDrivers")} />
                </div>
                <div className="flex gap-2 overflow-x-auto">
                    {[ALL, ...LEAVE_STATUSES].map((s) => (
                        <Button key={s} variant={status === s ? "secondary" : "ghost"} size="sm" onClick={() => setStatus(s as LeaveStatus | typeof ALL)}>
                            {s === ALL ? t("leaveRequests.filter.all") : t(`leaveRequests.status.${s}`)}
                            {s !== ALL && counts ? <span className="ml-1 text-xs text-muted-foreground">({counts[s as LeaveStatus]})</span> : null}
                        </Button>
                    ))}
                </div>
            </div>

            <HrErrorAlert error={list.error} title={t("leaveRequests.toast.failedLoad")} />

            <div className="overflow-hidden rounded-lg border bg-card">
                <Table>
                    <TableHeader className="bg-muted/50">
                        <TableRow>
                            <TableHead>{t("leaveRequests.table.driver")}</TableHead>
                            <TableHead>{t("leaveRequests.table.type")}</TableHead>
                            <TableHead>{t("leaveRequests.table.duration")}</TableHead>
                            <TableHead>{t("leaveRequests.table.reason")}</TableHead>
                            <TableHead>{t("leaveRequests.table.createdAt")}</TableHead>
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
                                        <p className="text-sm text-muted-foreground">{t("leaveRequests.loading")}</p>
                                    </div>
                                </TableCell>
                            </TableRow>
                        ) : rows.length === 0 ? (
                            <TableRow>
                                <TableCell colSpan={7} className="h-32 text-center text-muted-foreground">
                                    {t("leaveRequests.noRequests")}
                                </TableCell>
                            </TableRow>
                        ) : (
                            rows.map((req) => {
                                const actions = canManage ? leaveActions(req.status) : [];
                                return (
                                    <TableRow key={req.id} className="cursor-pointer transition-colors hover:bg-muted/50" onClick={() => setSelected({ request: req })}>
                                        <TableCell>
                                            <div className="flex items-center gap-2">
                                                <div className="flex h-8 w-8 items-center justify-center rounded-full bg-muted">
                                                    <User className="h-4 w-4 text-muted-foreground" />
                                                </div>
                                                <p className="text-sm font-medium">{req.driverName || t("leaveRequests.unknownDriver")}</p>
                                            </div>
                                        </TableCell>
                                        <TableCell>
                                            <Badge variant="outline" className="text-[10px] font-bold">
                                                {t(`hr.leave.type.${req.type}`, req.type)}
                                            </Badge>
                                        </TableCell>
                                        <TableCell>
                                            <div className="text-xs">
                                                <p className="font-medium">
                                                    {dayLabel(req.startDate, language)} – {dayLabel(req.endDate, language)}
                                                </p>
                                                <p className="text-muted-foreground">{t("hr.leave.days", { days: req.days })}</p>
                                            </div>
                                        </TableCell>
                                        <TableCell className="max-w-[200px] truncate text-sm">{req.reason}</TableCell>
                                        <TableCell className="whitespace-nowrap text-xs text-muted-foreground">{dayLabel(req.createdAt, language, "PPp")}</TableCell>
                                        <TableCell>
                                            <StatusBadge status={req.status} labelKey={`leaveRequests.status.${req.status}`} />
                                        </TableCell>
                                        <TableCell className="text-right">
                                            <DropdownMenu>
                                                <DropdownMenuTrigger asChild>
                                                    <Button
                                                        variant="ghost"
                                                        size="icon"
                                                        aria-label={`${t("leaveRequests.manageRequest")} ${req.driverName ?? ""}`.trim()}
                                                        onClick={(e) => e.stopPropagation()}
                                                    >
                                                        <MoreHorizontal className="h-4 w-4" />
                                                    </Button>
                                                </DropdownMenuTrigger>
                                                <DropdownMenuContent align="end" onClick={(e) => e.stopPropagation()}>
                                                    <DropdownMenuLabel>{t("leaveRequests.manageRequest")}</DropdownMenuLabel>
                                                    {actions.map((a) => (
                                                        <DropdownMenuItem
                                                            key={a}
                                                            className={a === "approve" ? "text-green-600" : a === "reject" ? "text-red-600" : "text-amber-600"}
                                                            onClick={() => setSelected({ request: req, action: a })}
                                                        >
                                                            {a === "approve" ? <CheckCircle2 className="mr-2 h-4 w-4" /> : <XCircle className="mr-2 h-4 w-4" />}
                                                            {t(`hr.leave.action.${a}`)}
                                                        </DropdownMenuItem>
                                                    ))}
                                                    <DropdownMenuItem onClick={() => setSelected({ request: req })}>
                                                        <FileText className="mr-2 h-4 w-4" /> {t("leaveRequests.viewDetails")}
                                                    </DropdownMenuItem>
                                                </DropdownMenuContent>
                                            </DropdownMenu>
                                        </TableCell>
                                    </TableRow>
                                );
                            })
                        )}
                    </TableBody>
                </Table>
                <LoadMoreFooter
                    shown={rows.length}
                    total={total}
                    hasNextPage={Boolean(list.hasNextPage)}
                    fetchingNext={list.isFetchingNextPage}
                    onLoadMore={() => void list.fetchNextPage()}
                />
            </div>

            <LeaveDecisionDialog
                request={selected?.request ?? null}
                initialAction={selected?.action}
                canManage={canManage}
                onClose={() => setSelected(null)}
            />
        </div>
    );
}

function LeaveDecisionDialog({
    request,
    initialAction,
    canManage,
    onClose,
}: {
    request: LeaveRequestDTO | null;
    initialAction?: LeaveAction;
    canManage: boolean;
    onClose: () => void;
}) {
    // Remounted per request and action, so the form starts empty each time.
    if (!request) return null;
    return <DecisionDialogBody key={`${request.id}:${initialAction ?? ""}`} request={request} initialAction={initialAction} canManage={canManage} onClose={onClose} />;
}

function DecisionDialogBody({
    request,
    initialAction,
    canManage,
    onClose,
}: {
    request: LeaveRequestDTO;
    initialAction?: LeaveAction;
    canManage: boolean;
    onClose: () => void;
}) {
    const { t, language } = useLanguage();
    const decide = useDecideLeave();
    const [rejecting, setRejecting] = useState(initialAction === "reject");
    const [reason, setReason] = useState("");
    const [error, setError] = useState<unknown>(null);
    const actions = canManage ? leaveActions(request.status) : [];

    const run = async (action: LeaveAction) => {
        if (action === "reject" && !rejecting) {
            setRejecting(true);
            return;
        }
        setError(null);
        try {
            const out = await decide.mutateAsync({ id: request.id, action, reason: action === "reject" ? reason.trim() : undefined });
            toast.success(isWriteBackPending(out) ? t("hr.leave.decisionPending") : t(`hr.leave.done.${action}`));
            onClose();
        } catch (e) {
            setError(e);
        }
    };

    return (
        <Dialog open onOpenChange={(open) => !open && !decide.isPending && onClose()}>
            <DialogContent className="sm:max-w-[520px]">
                <DialogHeader>
                    <DialogTitle className="flex items-center gap-2">
                        <FileText className="h-5 w-5 text-blue-600" />
                        {t("leaveRequests.dialog.title")}
                    </DialogTitle>
                    <DialogDescription>
                        {t("leaveRequests.dialog.submittedOn")} {dayLabel(request.createdAt, language, "PPP")}
                    </DialogDescription>
                </DialogHeader>

                <div className="space-y-5 py-2">
                    <div className="grid grid-cols-2 gap-4">
                        <div className="space-y-1">
                            <Label className="text-xs uppercase text-muted-foreground">{t("leaveRequests.dialog.driver")}</Label>
                            <div className="flex items-center gap-2">
                                <User className="h-4 w-4 text-muted-foreground" />
                                <span className="font-medium">{request.driverName ?? t("leaveRequests.unknownDriver")}</span>
                            </div>
                        </div>
                        <div className="space-y-1">
                            <Label className="text-xs uppercase text-muted-foreground">{t("leaveRequests.dialog.type")}</Label>
                            <div className="flex items-center gap-2">
                                <Badge variant="secondary">{t(`hr.leave.type.${request.type}`, request.type)}</Badge>
                                <StatusBadge status={request.status} labelKey={`leaveRequests.status.${request.status}`} />
                            </div>
                        </div>
                    </div>
                    <div className="space-y-1 rounded-lg border bg-muted/50 p-3">
                        <Label className="text-xs uppercase text-muted-foreground">{t("leaveRequests.dialog.duration")}</Label>
                        <p className="text-sm font-bold">
                            {dayLabel(request.startDate, language)} – {dayLabel(request.endDate, language)} ({t("hr.leave.days", { days: request.days })})
                        </p>
                    </div>
                    <div className="space-y-1">
                        <Label className="text-xs uppercase text-muted-foreground">{t("leaveRequests.dialog.reasonForLeave")}</Label>
                        <p className="min-h-[60px] rounded-md border bg-background p-3 text-sm">{request.reason}</p>
                    </div>
                    {request.rejectionReason ? (
                        <div className="space-y-1">
                            <Label className="text-xs uppercase text-red-600">{t("leaveRequests.dialog.reasonForRejection")}</Label>
                            <p className="rounded-md border border-red-200 p-3 text-sm">{request.rejectionReason}</p>
                        </div>
                    ) : null}
                    {request.attachments.length > 0 ? (
                        <div className="space-y-2">
                            <Label className="text-xs uppercase text-muted-foreground">{t("leaveRequests.dialog.attachments")}</Label>
                            <div className="flex flex-wrap gap-2">
                                {request.attachments.map((a, i) =>
                                    evidenceHref(a.url) ? (
                                        <Button key={i} variant="outline" size="sm" className="h-8 gap-2" asChild>
                                            <a href={evidenceHref(a.url) ?? undefined} target="_blank" rel="noopener noreferrer">
                                                <Paperclip className="h-3 w-3" />
                                                {t("leaveRequests.dialog.document")} {i + 1}
                                            </a>
                                        </Button>
                                    ) : (
                                        <span key={i} className="inline-flex items-center gap-1 rounded border border-dashed px-2 py-1 text-xs text-muted-foreground">
                                            <Paperclip className="h-3 w-3" />
                                            {t("leaveRequests.dialog.document")} {i + 1}: {t("hr.leave.attachmentUnavailable")}
                                        </span>
                                    )
                                )}
                            </div>
                        </div>
                    ) : null}
                    {rejecting ? (
                        <div className="space-y-2">
                            <Label htmlFor="leave-rejection-reason" className="text-xs font-bold uppercase text-red-600">
                                {t("leaveRequests.dialog.reasonForRejection")}
                            </Label>
                            <Textarea
                                id="leave-rejection-reason"
                                placeholder={t("leaveRequests.dialog.rejectionPlaceholder")}
                                value={reason}
                                maxLength={2000}
                                onChange={(e) => setReason(e.target.value)}
                            />
                        </div>
                    ) : null}
                    <HrErrorAlert error={error} />
                </div>

                <DialogFooter className="gap-2 sm:gap-0">
                    {rejecting ? (
                        <>
                            <Button variant="ghost" onClick={() => setRejecting(false)} disabled={decide.isPending}>
                                {t("leaveRequests.dialog.cancelButton")}
                            </Button>
                            <Button variant="destructive" onClick={() => void run("reject")} disabled={decide.isPending || !reason.trim()}>
                                {decide.isPending ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                                {t("leaveRequests.dialog.confirmRejection")}
                            </Button>
                        </>
                    ) : (
                        <>
                            <Button variant="ghost" onClick={onClose} disabled={decide.isPending}>
                                {t("payroll.dialog.close")}
                            </Button>
                            {actions.includes("cancel") ? (
                                <Button variant="outline" className="text-amber-600" onClick={() => void run("cancel")} disabled={decide.isPending}>
                                    <Ban className="mr-2 h-4 w-4" /> {t("leaveRequests.cancelRequest")}
                                </Button>
                            ) : null}
                            {actions.includes("reject") ? (
                                <Button variant="outline" className="text-red-600" onClick={() => void run("reject")} disabled={decide.isPending}>
                                    <XCircle className="mr-2 h-4 w-4" /> {t("leaveRequests.reject")}
                                </Button>
                            ) : null}
                            {actions.includes("approve") ? (
                                <Button className="bg-green-600 text-white hover:bg-green-700" onClick={() => void run("approve")} disabled={decide.isPending}>
                                    {decide.isPending ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <CheckCircle2 className="mr-2 h-4 w-4" />}
                                    {t("leaveRequests.dialog.approveRequest")}
                                </Button>
                            ) : null}
                        </>
                    )}
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
