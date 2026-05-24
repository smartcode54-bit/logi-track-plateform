"use client";

import { PagePermissionGuard } from "@/components/page-permission-guard";
import { CAPABILITIES } from "@/lib/capabilities";
import { useEffect, useMemo, useState } from "react";
import {
    getBillingStatements,
    updateBillingStatementStatus,
    type BillingStatement,
    type BillingStatementStatus,
} from "@/lib/billingStatement";
import { getCustomers } from "@/features/customers/api/customers";
import type { Customer } from "@/validate/customerSchema";
import { downloadBillingZip, type BillingCustomer } from "@/lib/billingDocument";
import { useLanguage } from "@/context/language";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from "@/components/ui/table";
import { Label } from "@/components/ui/label";
import { Loader2, MoreHorizontal, RefreshCw, TrendingUp, FileText, Clock, AlertTriangle } from "lucide-react";
import { format, differenceInDays } from "date-fns";
import { toast } from "sonner";
import { WITHHOLDING_TAX_RATE } from "@/lib/billingConfig";

// ─── Helpers ─────────────────────────────────────────────────────────────────

function formatThb(n: number) {
    return new Intl.NumberFormat("th-TH", {
        minimumFractionDigits: 2,
        maximumFractionDigits: 2,
    }).format(n);
}

function toDate(val: unknown): Date | undefined {
    if (!val) return undefined;
    if (val instanceof Date) return val;
    if (typeof (val as { toDate?: () => Date }).toDate === "function") {
        return (val as { toDate: () => Date }).toDate();
    }
    return undefined;
}

function effectiveStatus(stmt: BillingStatement): BillingStatementStatus | "overdue" {
    if (stmt.status !== "draft" && stmt.status !== "sent") return stmt.status;
    const due = toDate(stmt.dueDate);
    if (due && differenceInDays(new Date(), due) > 0) {
        return "overdue";
    }
    return stmt.status;
}

const STATUS_COLORS: Record<string, string> = {
    draft: "bg-gray-100 text-gray-700",
    sent: "bg-blue-100 text-blue-700",
    paid: "bg-green-100 text-green-700",
    cancelled: "bg-red-100 text-red-700",
    overdue: "bg-amber-100 text-amber-700",
};

const MONTHS = [
    { value: 1, label: "มกราคม / January" },
    { value: 2, label: "กุมภาพันธ์ / February" },
    { value: 3, label: "มีนาคม / March" },
    { value: 4, label: "เมษายน / April" },
    { value: 5, label: "พฤษภาคม / May" },
    { value: 6, label: "มิถุนายน / June" },
    { value: 7, label: "กรกฎาคม / July" },
    { value: 8, label: "สิงหาคม / August" },
    { value: 9, label: "กันยายน / September" },
    { value: 10, label: "ตุลาคม / October" },
    { value: 11, label: "พฤศจิกายน / November" },
    { value: 12, label: "ธันวาคม / December" },
];

const currentYear = new Date().getFullYear();
const YEARS = Array.from({ length: 3 }, (_, i) => currentYear - i);

// ─── Page ─────────────────────────────────────────────────────────────────────

export default function BillingResultPage() {
    const { t } = useLanguage();

    const [statements, setStatements] = useState<BillingStatement[]>([]);
    const [customers, setCustomers] = useState<Customer[]>([]);
    const [loading, setLoading] = useState(false);
    const [updatingId, setUpdatingId] = useState<string | null>(null);
    const [redownloadingId, setRedownloadingId] = useState<string | null>(null);

    // Filters
    const [filterCustomerId, setFilterCustomerId] = useState<string>("all");
    const [filterStatus, setFilterStatus] = useState<string>("all");
    const [filterMonth, setFilterMonth] = useState<string>("all");
    const [filterYear, setFilterYear] = useState<string>("all");

    useEffect(() => {
        getCustomers().then(setCustomers).catch(console.error);
    }, []);

    async function loadStatements() {
        setLoading(true);
        try {
            const data = await getBillingStatements({
                customerId: filterCustomerId !== "all" ? filterCustomerId : undefined,
                status: filterStatus !== "all" ? (filterStatus as BillingStatementStatus) : "all",
            });
            setStatements(data);
        } catch (e) {
            console.error("[billing-result] load error:", e);
            toast.error("Failed to load billing statements.");
        } finally {
            setLoading(false);
        }
    }

    useEffect(() => {
        loadStatements();
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []);

    // Client-side period filter
    const filtered = useMemo(() => {
        return statements.filter((s) => {
            if (filterMonth !== "all" && s.period.month !== Number(filterMonth)) return false;
            if (filterYear !== "all" && s.period.year !== Number(filterYear)) return false;
            return true;
        });
    }, [statements, filterMonth, filterYear]);

    // ── Stats ──
    const stats = useMemo(() => {
        let totalAmount = 0;
        let draftCount = 0;
        let sentCount = 0;
        let paidCount = 0;
        let overdueCount = 0;

        for (const s of filtered) {
            totalAmount += s.netAmount;
            const eff = effectiveStatus(s);
            if (eff === "draft") draftCount++;
            else if (eff === "sent") sentCount++;
            else if (eff === "paid") paidCount++;
            else if (eff === "overdue") overdueCount++;
        }

        return { totalAmount, draftCount, sentCount, paidCount, overdueCount };
    }, [filtered]);

    // ── Actions ──
    async function handleStatusUpdate(stmt: BillingStatement, newStatus: BillingStatementStatus) {
        if (!stmt.id) return;
        if (newStatus === "cancelled" && !window.confirm(t("accounting.billingResult.confirmCancel"))) return;

        setUpdatingId(stmt.id);
        try {
            await updateBillingStatementStatus(stmt.id, newStatus);
            setStatements((prev) =>
                prev.map((s) =>
                    s.id === stmt.id
                        ? {
                              ...s,
                              status: newStatus,
                              sentAt: newStatus === "sent" ? new Date() : s.sentAt,
                              paidAt: newStatus === "paid" ? new Date() : s.paidAt,
                          }
                        : s
                )
            );
        } catch (e) {
            console.error(e);
            toast.error(t("accounting.billingResult.action.updateError"));
        } finally {
            setUpdatingId(null);
        }
    }

    async function handleRedownload(stmt: BillingStatement) {
        if (!stmt.id) return;
        const customer = customers.find((c) => c.id === stmt.customerId);
        if (!customer) return;

        setRedownloadingId(stmt.id);
        try {
            const billingCustomer: BillingCustomer = {
                id: customer.id!,
                name: customer.name,
                address: customer.address,
                taxId: customer.taxId,
                branchType: customer.branchType,
                branchNumber: customer.branchNumber,
                contactName: customer.contactName,
                contactPhone: customer.contactPhone,
                paymentTermsDays: customer.paymentTermsDays,
                invoiceNote: customer.invoiceNote,
            };
            // Re-download with empty trips — the invoice number comes from the saved statement
            // For a proper re-download we'd need saved trip rows; for now generate with original number
            await downloadBillingZip([], billingCustomer, stmt.period, stmt.invoiceNumber);
        } finally {
            setRedownloadingId(null);
        }
    }

    return (
        <PagePermissionGuard capability={CAPABILITIES.accounting_billing_result}>
            <div className="p-6 space-y-6">
                {/* ── Header ── */}
                <div>
                    <h1 className="text-2xl font-bold">{t("nav.billingResult")}</h1>
                    <p className="text-muted-foreground text-sm mt-1">
                        {t("accounting.billingResult.subtitle")}
                    </p>
                </div>

                {/* ── Filters ── */}
                <Card>
                    <CardContent className="pt-4">
                        <div className="flex flex-wrap gap-4 items-end">
                            {/* Customer */}
                            <div className="space-y-1">
                                <Label className="text-xs">{t("accounting.billingResult.filters.customer")}</Label>
                                <Select value={filterCustomerId} onValueChange={setFilterCustomerId}>
                                    <SelectTrigger className="w-48">
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent>
                                        <SelectItem value="all">{t("accounting.billingResult.filters.allCustomers")}</SelectItem>
                                        {customers.map((c) => (
                                            <SelectItem key={c.id} value={c.id!}>
                                                {c.name}
                                            </SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                            </div>

                            {/* Status */}
                            <div className="space-y-1">
                                <Label className="text-xs">{t("accounting.billingResult.filters.status")}</Label>
                                <Select value={filterStatus} onValueChange={setFilterStatus}>
                                    <SelectTrigger className="w-36">
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent>
                                        <SelectItem value="all">{t("accounting.billingResult.filters.allStatuses")}</SelectItem>
                                        <SelectItem value="draft">{t("accounting.billingResult.status.draft")}</SelectItem>
                                        <SelectItem value="sent">{t("accounting.billingResult.status.sent")}</SelectItem>
                                        <SelectItem value="paid">{t("accounting.billingResult.status.paid")}</SelectItem>
                                        <SelectItem value="cancelled">{t("accounting.billingResult.status.cancelled")}</SelectItem>
                                    </SelectContent>
                                </Select>
                            </div>

                            {/* Month */}
                            <div className="space-y-1">
                                <Label className="text-xs">{t("accounting.billingResult.filters.month")}</Label>
                                <Select value={filterMonth} onValueChange={setFilterMonth}>
                                    <SelectTrigger className="w-52">
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent>
                                        <SelectItem value="all">{t("accounting.billingResult.filters.allPeriods")}</SelectItem>
                                        {MONTHS.map((m) => (
                                            <SelectItem key={m.value} value={String(m.value)}>
                                                {m.label}
                                            </SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                            </div>

                            {/* Year */}
                            <div className="space-y-1">
                                <Label className="text-xs">{t("accounting.billingResult.filters.year")}</Label>
                                <Select value={filterYear} onValueChange={setFilterYear}>
                                    <SelectTrigger className="w-28">
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent>
                                        <SelectItem value="all">{t("accounting.billingResult.filters.allPeriods")}</SelectItem>
                                        {YEARS.map((y) => (
                                            <SelectItem key={y} value={String(y)}>
                                                {y}
                                            </SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                            </div>

                            <Button variant="outline" size="sm" onClick={loadStatements} disabled={loading}>
                                {loading ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />}
                            </Button>
                        </div>
                    </CardContent>
                </Card>

                {/* ── Summary Cards ── */}
                <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
                    <Card>
                        <CardContent className="pt-4">
                            <div className="flex items-center gap-2 text-muted-foreground text-xs mb-1">
                                <TrendingUp className="h-3.5 w-3.5" />
                                {t("accounting.billingResult.stats.total")}
                            </div>
                            <p className="text-xl font-bold">฿{formatThb(stats.totalAmount)}</p>
                            <p className="text-xs text-muted-foreground">{filtered.length} invoices</p>
                        </CardContent>
                    </Card>
                    <Card>
                        <CardContent className="pt-4">
                            <div className="flex items-center gap-2 text-muted-foreground text-xs mb-1">
                                <FileText className="h-3.5 w-3.5" />
                                {t("accounting.billingResult.stats.draft")} / {t("accounting.billingResult.stats.sent")}
                            </div>
                            <p className="text-xl font-bold">{stats.draftCount + stats.sentCount}</p>
                            <p className="text-xs text-muted-foreground">
                                {stats.draftCount} draft · {stats.sentCount} sent
                            </p>
                        </CardContent>
                    </Card>
                    <Card>
                        <CardContent className="pt-4">
                            <div className="flex items-center gap-2 text-muted-foreground text-xs mb-1">
                                <Clock className="h-3.5 w-3.5" />
                                {t("accounting.billingResult.stats.paid")}
                            </div>
                            <p className="text-xl font-bold text-green-600">{stats.paidCount}</p>
                        </CardContent>
                    </Card>
                    <Card>
                        <CardContent className="pt-4">
                            <div className="flex items-center gap-2 text-muted-foreground text-xs mb-1">
                                <AlertTriangle className="h-3.5 w-3.5" />
                                {t("accounting.billingResult.stats.overdue")}
                            </div>
                            <p className={`text-xl font-bold ${stats.overdueCount > 0 ? "text-amber-600" : ""}`}>
                                {stats.overdueCount}
                            </p>
                        </CardContent>
                    </Card>
                </div>

                {/* ── Table ── */}
                <Card>
                    <CardContent className="pt-4">
                        {loading ? (
                            <div className="flex justify-center py-12">
                                <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
                            </div>
                        ) : filtered.length === 0 ? (
                            <p className="text-center text-sm text-muted-foreground py-12">
                                {t("accounting.billingResult.noData")}
                            </p>
                        ) : (
                            <div className="overflow-x-auto">
                                <Table>
                                    <TableHeader>
                                        <TableRow>
                                            <TableHead>{t("accounting.billingResult.table.invoiceNumber")}</TableHead>
                                            <TableHead>{t("accounting.billingResult.table.customer")}</TableHead>
                                            <TableHead>{t("accounting.billingResult.table.period")}</TableHead>
                                            <TableHead className="text-right">{t("accounting.billingResult.table.tripCount")}</TableHead>
                                            <TableHead className="text-right">{t("accounting.billingResult.table.totalAmount")}</TableHead>
                                            <TableHead className="text-right">{t("accounting.billingResult.table.withholdingTax")}</TableHead>
                                            <TableHead className="text-right">{t("accounting.billingResult.table.netAmount")}</TableHead>
                                            <TableHead>{t("accounting.billingResult.table.status")}</TableHead>
                                            <TableHead>{t("accounting.billingResult.table.dueDate")}</TableHead>
                                            <TableHead>{t("accounting.billingResult.table.generatedAt")}</TableHead>
                                            <TableHead>{t("accounting.billingResult.table.actions")}</TableHead>
                                        </TableRow>
                                    </TableHeader>
                                    <TableBody>
                                        {filtered.map((stmt) => {
                                            const eff = effectiveStatus(stmt);
                                            const genDate = toDate(stmt.generatedAt);
                                            const dueDate = toDate(stmt.dueDate);
                                            const isUpdating = updatingId === stmt.id;
                                            const isRedownloading = redownloadingId === stmt.id;
                                            const mm = String(stmt.period.month).padStart(2, "0");
                                            const periodLabel = `${mm}/${stmt.period.year}`;

                                            return (
                                                <TableRow key={stmt.id}>
                                                    <TableCell className="font-mono text-sm font-medium">
                                                        {stmt.invoiceNumber}
                                                    </TableCell>
                                                    <TableCell>
                                                        <div className="font-medium">{stmt.customerName}</div>
                                                        <div className="text-xs text-muted-foreground font-mono">{stmt.customerCode}</div>
                                                    </TableCell>
                                                    <TableCell className="font-mono text-sm">{periodLabel}</TableCell>
                                                    <TableCell className="text-right">{stmt.tripCount}</TableCell>
                                                    <TableCell className="text-right font-mono">
                                                        {formatThb(stmt.totalAmount)}
                                                    </TableCell>
                                                    <TableCell className="text-right font-mono text-muted-foreground">
                                                        {formatThb(stmt.withholdingTax)}
                                                    </TableCell>
                                                    <TableCell className="text-right font-mono font-medium">
                                                        {formatThb(stmt.netAmount)}
                                                    </TableCell>
                                                    <TableCell>
                                                        <Badge className={`text-xs ${STATUS_COLORS[eff] ?? ""}`} variant="outline">
                                                            {t(`accounting.billingResult.status.${eff === "overdue" ? "overdue" : eff}`)}
                                                        </Badge>
                                                    </TableCell>
                                                    <TableCell className="text-sm">
                                                        {dueDate ? (
                                                            <span className={eff === "overdue" ? "text-amber-600 font-medium" : ""}>
                                                                {format(dueDate, "dd/MM/yyyy")}
                                                            </span>
                                                        ) : (
                                                            <span className="text-muted-foreground">—</span>
                                                        )}
                                                    </TableCell>
                                                    <TableCell className="text-sm text-muted-foreground">
                                                        {genDate ? format(genDate, "dd/MM/yyyy") : "—"}
                                                    </TableCell>
                                                    <TableCell>
                                                        {isUpdating ? (
                                                            <Loader2 className="h-4 w-4 animate-spin" />
                                                        ) : (
                                                            <DropdownMenu>
                                                                <DropdownMenuTrigger asChild>
                                                                    <Button variant="ghost" size="icon" className="h-8 w-8">
                                                                        <MoreHorizontal className="h-4 w-4" />
                                                                    </Button>
                                                                </DropdownMenuTrigger>
                                                                <DropdownMenuContent align="end">
                                                                    {stmt.status === "draft" && (
                                                                        <DropdownMenuItem onClick={() => handleStatusUpdate(stmt, "sent")}>
                                                                            {t("accounting.billingResult.action.markSent")}
                                                                        </DropdownMenuItem>
                                                                    )}
                                                                    {(stmt.status === "draft" || stmt.status === "sent") && (
                                                                        <DropdownMenuItem onClick={() => handleStatusUpdate(stmt, "paid")}>
                                                                            {t("accounting.billingResult.action.markPaid")}
                                                                        </DropdownMenuItem>
                                                                    )}
                                                                    {stmt.status !== "cancelled" && stmt.status !== "paid" && (
                                                                        <DropdownMenuItem
                                                                            className="text-destructive"
                                                                            onClick={() => handleStatusUpdate(stmt, "cancelled")}
                                                                        >
                                                                            {t("accounting.billingResult.action.cancel")}
                                                                        </DropdownMenuItem>
                                                                    )}
                                                                    <DropdownMenuItem
                                                                        disabled={isRedownloading}
                                                                        onClick={() => handleRedownload(stmt)}
                                                                    >
                                                                        {isRedownloading
                                                                            ? t("accounting.billingResult.action.redownloading")
                                                                            : t("accounting.billingResult.action.redownload")}
                                                                    </DropdownMenuItem>
                                                                </DropdownMenuContent>
                                                            </DropdownMenu>
                                                        )}
                                                    </TableCell>
                                                </TableRow>
                                            );
                                        })}
                                    </TableBody>
                                </Table>
                            </div>
                        )}
                    </CardContent>
                </Card>
            </div>
        </PagePermissionGuard>
    );
}
