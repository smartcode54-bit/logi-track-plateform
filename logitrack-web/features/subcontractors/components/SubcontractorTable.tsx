"use client";

import { useRef, useState } from "react";
import { useCan } from "@/features/auth/api/useMe";
import { useSuspendSubcontractor } from "../api/useSubcontractors";
import { carrierStatusTone, toneBadgeVariant } from "@/lib/statusPresentation";
import { apiErrorText } from "@/lib/apiError";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Dialog as AlertDialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from "@/components/ui/dialog";
import { useLanguage } from "@/context/language";
import type { SubcontractorSummaryDTO } from "../api/subcontractors";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Badge } from "@/components/ui/badge";
import { MoreHorizontal, ArrowUp, ArrowDown, Loader2 } from "lucide-react";
import { useRouter } from "next/navigation";

interface SubcontractorTableProps {
    subcontractors: SubcontractorSummaryDTO[];
    loading: boolean;
    currentPage: number;
    setCurrentPage: (p: number | ((prev: number) => number)) => void;
    itemsPerPage: number;
    filteredCount: number;
    sortConfig: { key: keyof SubcontractorSummaryDTO; direction: 'asc' | 'desc' } | null;
    handleSort: (key: keyof SubcontractorSummaryDTO) => void;
}

export function SubcontractorTable(props: SubcontractorTableProps) {
    const { t } = useLanguage();
    const router = useRouter();

    const { allowed: canSuspend } = useCan("platform:manage_tenants");
    const suspend = useSuspendSubcontractor();
    const [selected, setSelected] = useState<SubcontractorSummaryDTO | null>(null);
    const [succeeded, setSucceeded] = useState(false);
    const cancelRef = useRef<HTMLButtonElement>(null);
    const submitting = useRef(false);
    const actionButtons = useRef(new Map<string, HTMLButtonElement>());
    const invokingButton = useRef<HTMLButtonElement | null>(null);
    const focusFallback = useRef<HTMLDivElement>(null);

    const confirmSuspend = async () => {
        if (!selected || !canSuspend || selected.status === "suspended" || submitting.current) return;
        submitting.current = true;
        try {
            await suspend.mutateAsync(selected.id);
            setSelected(null);
            setSucceeded(true);
        } catch {
            // The shared alert keeps the API denial visible in the confirmation dialog.
        } finally {
            submitting.current = false;
        }
    };

    const getStatusBadge = (status: string) => (
        <Badge variant={toneBadgeVariant(carrierStatusTone(status))}>
            {["active", "pending", "suspended"].includes(status) ? t(`subcontractors.edit.status.${status}`) : status}
        </Badge>
    );

    return (
        <>
        {succeeded && <Alert variant="success" role="status"><AlertDescription>{t("subcontractors.ui4.suspended")}</AlertDescription></Alert>}
        <div ref={focusFallback} tabIndex={-1} className="rounded-xl border border-border/50 bg-card overflow-hidden">
            <Table>
                <TableHeader className="bg-muted/30">
                    <TableRow className="hover:bg-transparent border-border/50">
                        <TableHead className="pl-6" aria-sort={props.sortConfig?.key === 'nameTh' ? props.sortConfig.direction === "asc" ? "ascending" : "descending" : "none"}>
                            <Button variant="ghost" size="sm" className="-ml-3" onClick={() => props.handleSort('nameTh')}>
                                {t("subcontractors.table.companyName")} 
                                {props.sortConfig?.key === 'nameTh' && (props.sortConfig.direction === 'asc' ? <ArrowUp className="w-3 h-3" /> : <ArrowDown className="w-3 h-3" />)}
                            </Button>
                        </TableHead>
                        <TableHead aria-sort={props.sortConfig?.key === 'contactPerson' ? props.sortConfig.direction === "asc" ? "ascending" : "descending" : "none"}>
                            <Button variant="ghost" size="sm" className="-ml-3" onClick={() => props.handleSort('contactPerson')}>
                                {t("subcontractors.table.primaryContact")} 
                                {props.sortConfig?.key === 'contactPerson' && (props.sortConfig.direction === 'asc' ? <ArrowUp className="w-3 h-3" /> : <ArrowDown className="w-3 h-3" />)}
                            </Button>
                        </TableHead>
                        <TableHead aria-sort={props.sortConfig?.key === 'fleetSize' ? props.sortConfig.direction === "asc" ? "ascending" : "descending" : "none"}>
                            <Button variant="ghost" size="sm" className="-ml-3" onClick={() => props.handleSort('fleetSize')}>
                                {t("subcontractors.table.fleetSize")} 
                                {props.sortConfig?.key === 'fleetSize' && (props.sortConfig.direction === 'asc' ? <ArrowUp className="w-3 h-3" /> : <ArrowDown className="w-3 h-3" />)}
                            </Button>
                        </TableHead>
                        <TableHead>{t("subcontractors.table.serviceArea")}</TableHead>
                        <TableHead>{t("subcontractors.table.status")}</TableHead>
                        <TableHead className="text-right pr-6">{t("subcontractors.table.actions")}</TableHead>
                    </TableRow>
                </TableHeader>
                <TableBody>
                    {props.loading ? (
                        <TableRow>
                            <TableCell colSpan={6} className="h-32 text-center text-muted-foreground">
                                <Loader2 className="h-6 w-6 animate-spin mx-auto mb-2" />
                                {t("subcontractors.loading")}
                            </TableCell>
                        </TableRow>
                    ) : props.subcontractors.length === 0 ? (
                        <TableRow>
                            <TableCell colSpan={6} className="h-32 text-center text-muted-foreground">{t("subcontractors.noData")}</TableCell>
                        </TableRow>
                    ) : (
                        props.subcontractors.map((sub) => (
                            <TableRow key={sub.id} className="border-border/50 hover:bg-muted/30 transition-colors group">
                                <TableCell className="pl-6 py-4">
                                    <div className="flex gap-3 items-center">
                                        <div className="h-9 w-9 rounded-lg bg-muted text-muted-foreground flex items-center justify-center font-medium text-sm">
                                            {Array.from(sub.nameTh.trim())[0] ?? ""}
                                        </div>
                                        <div>
                                            <div className="font-semibold text-foreground text-sm">{sub.nameTh}</div>
                                            {sub.code && <div className="text-xs text-muted-foreground font-mono">{t("subcontractors.table.code")}: {sub.code}</div>}
                                        </div>
                                    </div>
                                </TableCell>
                                <TableCell>
                                    <div className="space-y-0.5">
                                        <div className="text-sm font-medium text-foreground">{sub.contactPerson}</div>
                                        <div className="text-xs text-muted-foreground">{sub.email || sub.phone}</div>
                                    </div>
                                </TableCell>
                                <TableCell>
                                    <div className="flex items-center gap-2">
                                        <span className="tabular-nums font-medium text-foreground">{sub.fleetSize || 0}</span>
                                        <span className="text-xs text-muted-foreground">{t("subcontractors.table.vehicles")}</span>
                                    </div>
                                </TableCell>
                                <TableCell>
                                    <span className="text-sm text-foreground/80">{sub.serviceRegions?.join(", ") || t("subcontractors.filter.notSpecified")}</span>
                                </TableCell>
                                <TableCell>
                                    {getStatusBadge(sub.status)}
                                </TableCell>
                                <TableCell className="text-right pr-6">
                                    <DropdownMenu>
                                        <DropdownMenuTrigger asChild>
                                            <Button ref={(element) => { if (element) actionButtons.current.set(sub.id, element); else actionButtons.current.delete(sub.id); }} aria-label={t("common.actionsFor", { name: sub.nameTh })} variant="ghost" size="icon" className="min-h-11 min-w-11 md:min-h-9 md:min-w-9">
                                                <MoreHorizontal className="h-4 w-4" />
                                            </Button>
                                        </DropdownMenuTrigger>
                                        <DropdownMenuContent align="end">
                                            <DropdownMenuItem className="min-h-11 min-w-11 md:min-h-8 md:min-w-0" onClick={() => router.push(`/app/subcontractors/${sub.id}`)}>
                                                {t("subcontractors.action.viewProfile")}
                                            </DropdownMenuItem>
                                            {canSuspend && sub.status !== "suspended" && (
                                                <DropdownMenuItem className="min-h-11 min-w-11 text-destructive focus:text-destructive md:min-h-8 md:min-w-0" onSelect={() => {
                                                    suspend.reset();
                                                    setSucceeded(false);
                                                    invokingButton.current = actionButtons.current.get(sub.id) ?? null;
                                                    setSelected(sub);
                                                }}>{t("subcontractors.action.suspendPartner")}</DropdownMenuItem>
                                            )}
                                        </DropdownMenuContent>
                                    </DropdownMenu>
                                </TableCell>
                            </TableRow>
                        ))
                    )}
                </TableBody>
            </Table>

            {/* Pagination */}
            <div className="flex flex-col gap-4 md:flex-row md:items-center md:justify-between px-6 py-4 border-t border-border/50 bg-muted/20">
                <div className="text-xs text-muted-foreground">
                    {t("subcontractors.pagination.showing")}{" "}
                    <span className="font-medium text-foreground">
                        {props.filteredCount === 0 ? 0 : (props.currentPage - 1) * props.itemsPerPage + 1}
                    </span>{" "}
                    {t("subcontractors.pagination.to")}{" "}
                    <span className="font-medium text-foreground">
                        {Math.min(props.currentPage * props.itemsPerPage, props.filteredCount)}
                    </span>{" "}
                    {t("subcontractors.pagination.of")}{" "}
                    <span className="font-medium text-foreground">{props.filteredCount}</span>{" "}
                    {t("subcontractors.pagination.results")}
                </div>
                <div className="flex items-center gap-2">
                    <Button
                        variant="outline"
                        size="sm"
                        onClick={() => props.setCurrentPage(p => Math.max(1, p - 1))}
                        disabled={props.currentPage === 1}
                    >
                        {t("subcontractors.pagination.previous")}
                    </Button>
                    <Button
                        variant="outline"
                        size="sm"
                        onClick={() => props.setCurrentPage(p => Math.min(Math.ceil(props.filteredCount / props.itemsPerPage), p + 1))}
                        disabled={props.currentPage >= Math.ceil(props.filteredCount / props.itemsPerPage)}
                    >
                        {t("subcontractors.pagination.next")}
                    </Button>
                </div>
            </div>
        </div>
        <AlertDialog open={selected !== null} onOpenChange={(open) => { if (!open && !suspend.isPending) setSelected(null); }}>
            <DialogContent role="alertdialog" showCloseButton={false}
                onOpenAutoFocus={(event) => { event.preventDefault(); cancelRef.current?.focus(); }}
                onCloseAutoFocus={(event) => {
                    event.preventDefault();
                    const original = invokingButton.current;
                    const fallback = Array.from(actionButtons.current.values()).find((button) => button.isConnected);
                    if (original?.isConnected) original.focus();
                    else if (fallback) fallback.focus();
                    else focusFallback.current?.focus();
                    invokingButton.current = null;
                }}
                onPointerDownOutside={(event) => event.preventDefault()}>
                <DialogHeader>
                    <DialogTitle>{t("subcontractors.ui4.suspendTitle")}</DialogTitle>
                    <DialogDescription>{t("subcontractors.ui4.suspendBody", { name: selected?.nameTh ?? "" })}</DialogDescription>
                </DialogHeader>
                {suspend.error && <Alert variant="destructive">
                    <AlertTitle>{t("subcontractors.ui4.suspendFailed")}</AlertTitle>
                    <AlertDescription>{apiErrorText(suspend.error, t)}</AlertDescription>
                </Alert>}
                <DialogFooter>
                    <Button ref={cancelRef} variant="outline" className="min-h-11 min-w-11 md:min-h-9 md:min-w-0" disabled={suspend.isPending} onClick={() => setSelected(null)}>{t("subcontractors.form.cancel")}</Button>
                    <Button variant="destructive" className="min-h-11 min-w-11 md:min-h-9 md:min-w-0" disabled={suspend.isPending} onClick={() => void confirmSuspend()}>
                        {suspend.isPending && <Loader2 className="h-4 w-4 animate-spin" aria-hidden />}
                        {suspend.isPending ? t("subcontractors.form.saving") : t("subcontractors.ui4.suspendConfirm")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </AlertDialog>
        </>
    );
}
