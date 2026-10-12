"use client";

/**
 * Driver penalties over Go (T46, issue #66; Appendix E §E.2.2, §E.10 row 11), the only source since TW7.
 * Three independent reads in parallel, none waiting on another (the legacy page's waterfall was
 * every driver, then the config, then the newest 200 penalties): the driver picker searched on the server
 * (`['drivers',{fields:'minimal',q}]`, opened on demand), the active version's penalty types
 * (`['compConfig','active']`) and the list (`['penalties',{driverId,status}]`, keyset pages of 50, no cap).
 * A penalty is added with `POST /v1/penalties` (whole baht, deducted by R2 runs in installments) and an open
 * one cancelled with `POST /v1/penalties/{id}/cancel` behind a confirmation.
 */
import { useState } from "react";
import { format } from "date-fns";
import { Ban, Loader2, Plus } from "lucide-react";
import { toast } from "sonner";
import { useQuery } from "@tanstack/react-query";

import { useLanguage } from "@/context/language";
import { SearchPicker, formatThb, type PickValue } from "@/features/accounting/components/go/shared";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

import { activeCompConfigQuery } from "../../api/compConfig.go";
import {
    PENALTY_STATUSES,
    isOpenPenalty,
    penaltyRows,
    useCancelPenalty,
    useCreatePenalty,
    usePenalties,
    type PenaltyDTO,
    type PenaltyFilters,
} from "../../api/penalties.go";
import { HrErrorAlert, LoadMoreFooter, StatusBadge, dayLabel } from "./hrShared";

const ALL = "all";
type StatusFilter = NonNullable<PenaltyFilters["status"]> | typeof ALL;

export function PenaltiesPage() {
    const { t, language } = useLanguage();
    const [filterDriver, setFilterDriver] = useState<PickValue | null>(null);
    const [status, setStatus] = useState<StatusFilter>(ALL);
    const list = usePenalties({ driverId: filterDriver?.id, status: status === ALL ? undefined : status });
    const rows = penaltyRows(list.data?.pages);
    const [cancelling, setCancelling] = useState<PenaltyDTO | null>(null);

    return (
        <div className="max-w-6xl space-y-6 p-4 md:p-6">
            <div>
                <h1 className="text-2xl font-semibold">{t("driverComp.penalty.title")}</h1>
                <p className="text-sm text-muted-foreground">{t("driverComp.penalty.subtitle")}</p>
            </div>

            <AddPenaltyForm />

            <div className="flex flex-wrap items-end gap-3">
                <div className="flex flex-col gap-1 text-sm font-medium">
                    {t("driverComp.penalty.driver")}
                    <SearchPicker kind="driver" value={filterDriver} onChange={setFilterDriver} placeholder={t("hr.allDrivers")} clearLabel={t("hr.allDrivers")} />
                </div>
                <label className="flex flex-col gap-1 text-sm font-medium">
                    {t("driverComp.penalty.status")}
                    <Select value={status} onValueChange={(v) => setStatus(v as StatusFilter)}>
                        <SelectTrigger className="w-[200px]" aria-label={t("driverComp.penalty.status")}>
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent className="z-[1005]" position="popper">
                            <SelectItem value={ALL}>{t("payroll.filter.all")}</SelectItem>
                            <SelectItem value="open">{t("hr.penalty.status.open")}</SelectItem>
                            {PENALTY_STATUSES.map((s) => (
                                <SelectItem key={s} value={s}>
                                    {t(`hr.penalty.status.${s}`)}
                                </SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                </label>
            </div>

            <HrErrorAlert error={list.error} />

            <div className="overflow-hidden rounded-lg border bg-card">
                <Table>
                    <TableHeader>
                        <TableRow>
                            <TableHead>{t("driverComp.penalty.driver")}</TableHead>
                            <TableHead>{t("driverComp.penalty.type")}</TableHead>
                            <TableHead>{t("hr.penalty.incurredAt")}</TableHead>
                            <TableHead className="text-right">{t("driverComp.penalty.amount")}</TableHead>
                            <TableHead className="text-right">{t("driverComp.penalty.remaining")}</TableHead>
                            <TableHead className="text-right">{t("hr.penalty.installments")}</TableHead>
                            <TableHead className="text-right">{t("hr.penalty.nextDeduction")}</TableHead>
                            <TableHead>{t("driverComp.penalty.status")}</TableHead>
                            <TableHead />
                        </TableRow>
                    </TableHeader>
                    <TableBody>
                        {list.isPending ? (
                            <TableRow>
                                <TableCell colSpan={9} className="h-24 text-center">
                                    <Loader2 className="mx-auto h-5 w-5 animate-spin text-muted-foreground" />
                                </TableCell>
                            </TableRow>
                        ) : rows.length === 0 ? (
                            <TableRow>
                                <TableCell colSpan={9} className="text-center text-muted-foreground">
                                    {t("driverComp.penalty.none")}
                                </TableCell>
                            </TableRow>
                        ) : (
                            rows.map((p) => (
                                <TableRow key={p.id}>
                                    <TableCell>{p.driverName || t("payroll.unknownDriver")}</TableCell>
                                    <TableCell>
                                        <div className="text-sm">{p.typeName || p.typeCode}</div>
                                        {p.reason ? <div className="max-w-[240px] truncate text-xs text-muted-foreground">{p.reason}</div> : null}
                                    </TableCell>
                                    <TableCell className="text-xs">{dayLabel(p.incurredAt, language)}</TableCell>
                                    <TableCell className="text-right tabular-nums">{formatThb(p.totalThb)}</TableCell>
                                    <TableCell className="text-right tabular-nums">{formatThb(p.remainingThb)}</TableCell>
                                    <TableCell className="text-right tabular-nums">
                                        {p.installmentsPaid}/{p.installmentsTotal}
                                    </TableCell>
                                    <TableCell className="text-right tabular-nums">{isOpenPenalty(p) ? formatThb(p.installmentThb) : "—"}</TableCell>
                                    <TableCell>
                                        <StatusBadge status={p.status} labelKey={`hr.penalty.status.${p.status}`} />
                                    </TableCell>
                                    <TableCell>
                                        {isOpenPenalty(p) ? (
                                            <Button
                                                variant="ghost"
                                                size="icon"
                                                aria-label={`${t("hr.penalty.cancel")} ${p.driverName ?? ""}`.trim()}
                                                onClick={() => setCancelling(p)}
                                            >
                                                <Ban className="h-4 w-4" />
                                            </Button>
                                        ) : null}
                                    </TableCell>
                                </TableRow>
                            ))
                        )}
                    </TableBody>
                </Table>
                <LoadMoreFooter
                    shown={rows.length}
                    hasNextPage={Boolean(list.hasNextPage)}
                    fetchingNext={list.isFetchingNextPage}
                    onLoadMore={() => void list.fetchNextPage()}
                />
            </div>

            <CancelPenaltyDialog penalty={cancelling} onClose={() => setCancelling(null)} />
        </div>
    );
}

function AddPenaltyForm() {
    const { t, language } = useLanguage();
    const config = useQuery(activeCompConfigQuery);
    const types = config.data?.penaltyTypes ?? [];
    const create = useCreatePenalty();
    const [driver, setDriver] = useState<PickValue | null>(null);
    const [typeCode, setTypeCode] = useState("");
    const [amount, setAmount] = useState(0);
    const [installments, setInstallments] = useState(1);
    const [reason, setReason] = useState("");
    const [incurredAt, setIncurredAt] = useState(() => format(new Date(), "yyyy-MM-dd"));
    const [error, setError] = useState<unknown>(null);

    const onTypeChange = (code: string) => {
        setTypeCode(code);
        const type = types.find((x) => x.code === code);
        if (type && !amount) setAmount(type.defaultAmountThb);
    };

    const add = async () => {
        setError(null);
        if (!driver || !typeCode || !(amount > 0)) {
            toast.error(t("driverComp.penalty.invalid"));
            return;
        }
        try {
            // No typeName: Go keeps the type's English name of the version in effect at incurredAt, as the
            // legacy page did (it is the name of the run's deduction line).
            await create.mutateAsync({
                driverId: driver.id,
                typeCode,
                totalThb: Math.round(amount),
                installmentsTotal: Math.max(1, Math.round(installments)),
                reason: reason.trim() || undefined,
                incurredAt,
            });
            toast.success(t("driverComp.penalty.saved"));
            setAmount(0);
            setReason("");
            setInstallments(1);
        } catch (e) {
            setError(e);
            toast.error(t("driverComp.penalty.saveError"));
        }
    };

    return (
        <Card>
            <CardContent className="space-y-3 pt-6">
                <div className="grid grid-cols-1 gap-3 md:grid-cols-6">
                    <div className="md:col-span-2">
                        <SearchPicker kind="driver" value={driver} onChange={setDriver} placeholder={t("driverComp.penalty.driver")} className="w-full" />
                    </div>
                    <Select value={typeCode} onValueChange={onTypeChange}>
                        <SelectTrigger aria-label={t("driverComp.penalty.type")}>
                            <SelectValue placeholder={t("driverComp.penalty.type")} />
                        </SelectTrigger>
                        <SelectContent className="z-[1005]" position="popper">
                            {types.map((x) => (
                                <SelectItem key={x.code} value={x.code}>
                                    {language === "th" ? x.nameTh || x.nameEn : x.nameEn || x.nameTh}
                                </SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                    <Input
                        type="number"
                        min={1}
                        aria-label={t("driverComp.penalty.amount")}
                        placeholder={t("driverComp.penalty.amount")}
                        value={amount || ""}
                        onChange={(e) => setAmount(Number(e.target.value))}
                    />
                    <Input
                        type="number"
                        min={1}
                        aria-label={t("driverComp.penalty.installments")}
                        placeholder={t("driverComp.penalty.installments")}
                        value={installments}
                        onChange={(e) => setInstallments(Number(e.target.value))}
                    />
                    <Input type="date" aria-label={t("hr.penalty.incurredAt")} value={incurredAt} onChange={(e) => setIncurredAt(e.target.value)} />
                    <Input
                        className="md:col-span-5"
                        aria-label={t("driverComp.penalty.reason")}
                        placeholder={t("driverComp.penalty.reason")}
                        value={reason}
                        maxLength={2000}
                        onChange={(e) => setReason(e.target.value)}
                    />
                    <Button onClick={() => void add()} disabled={create.isPending}>
                        {create.isPending ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Plus className="mr-2 h-4 w-4" />}
                        {t("driverComp.penalty.add")}
                    </Button>
                </div>
                {config.isSuccess && types.length === 0 ? <p className="text-xs text-muted-foreground">{t("hr.penalty.noTypes")}</p> : null}
                <HrErrorAlert error={config.error ?? error} />
            </CardContent>
        </Card>
    );
}

function CancelPenaltyDialog({ penalty, onClose }: { penalty: PenaltyDTO | null; onClose: () => void }) {
    const { t } = useLanguage();
    const cancel = useCancelPenalty();
    const [error, setError] = useState<unknown>(null);
    const confirm = async () => {
        if (!penalty) return;
        setError(null);
        try {
            await cancel.mutateAsync({ id: penalty.id });
            toast.success(t("hr.penalty.cancelled"));
            onClose();
        } catch (e) {
            setError(e);
        }
    };
    return (
        <Dialog
            open={penalty !== null}
            onOpenChange={(open) => {
                if (!open && !cancel.isPending) {
                    setError(null);
                    onClose();
                }
            }}
        >
            <DialogContent>
                <DialogHeader>
                    <DialogTitle>{t("hr.penalty.cancelTitle")}</DialogTitle>
                    <DialogDescription>
                        {penalty
                            ? t("hr.penalty.cancelDesc", {
                                  driver: penalty.driverName ?? t("payroll.unknownDriver"),
                                  remaining: formatThb(penalty.remainingThb),
                              })
                            : ""}
                    </DialogDescription>
                </DialogHeader>
                <HrErrorAlert error={error} />
                <DialogFooter>
                    <Button variant="outline" onClick={onClose} disabled={cancel.isPending}>
                        {t("hr.cancel")}
                    </Button>
                    <Button variant="destructive" onClick={() => void confirm()} disabled={cancel.isPending}>
                        {cancel.isPending ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                        {t("hr.penalty.cancel")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
