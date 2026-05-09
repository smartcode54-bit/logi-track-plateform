"use client";

import { useState, useEffect, useMemo } from "react";
import { collection, query, orderBy, onSnapshot, doc, updateDoc, Timestamp, limit } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { Payroll, PAYROLL_STATUS_ENUM } from "@/validate/payrollSchema";
import {
    Banknote,
    CheckCircle2,
    Clock,
    Search,
    Loader2,
    MoreHorizontal,
    FileText,
    User,
    ArrowUpCircle,
    ArrowDownCircle,
    Download,
    Eye
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from "@/components/ui/table";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuLabel,
    DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { useLanguage } from "@/context/language";
import { format } from "date-fns";
import { toast } from "sonner";
import { useAuth } from "@/context/auth";
import { PayrollReviewDialog } from "./PayrollReviewDialog";

export default function PayrollPage() {
    const { t } = useLanguage();
    const auth = useAuth();
    const user = auth?.currentUser ?? null;
    const [payrollRecords, setPayrollRecords] = useState<Payroll[]>([]);
    const [loading, setLoading] = useState(true);
    const [statusFilter, setStatusFilter] = useState<string>("all");
    const [searchQuery, setSearchQuery] = useState("");
    
    // Dialog state
    const [selectedPayroll, setSelectedPayroll] = useState<Payroll | null>(null);
    const [isReviewOpen, setIsReviewOpen] = useState(false);

    // Fetch payroll records
    useEffect(() => {
        setLoading(true);
        const ref = collection(db, COLLECTIONS.PAYROLL);
        const q = query(ref, orderBy("periodEnd", "desc"), limit(100));

        const unsubscribe = onSnapshot(q, (snapshot) => {
            const fetched: Payroll[] = [];
            snapshot.forEach((doc) => {
                const data = doc.data();
                fetched.push({
                    id: doc.id,
                    ...data,
                    periodStart: data.periodStart instanceof Timestamp ? data.periodStart.toDate() : new Date(data.periodStart),
                    periodEnd: data.periodEnd instanceof Timestamp ? data.periodEnd.toDate() : new Date(data.periodEnd),
                    createdAt: data.createdAt instanceof Timestamp ? data.createdAt.toDate() : new Date(data.createdAt),
                    paymentDate: data.paymentDate instanceof Timestamp ? data.paymentDate.toDate() : data.paymentDate ? new Date(data.paymentDate) : undefined,
                } as Payroll);
            });
            setPayrollRecords(fetched);
            setLoading(false);
        }, (err) => {
            console.error("Error fetching payroll:", err);
            toast.error(t("payroll.toast.failedLoad"));
            setLoading(false);
        });

        return () => unsubscribe();
    }, []);

    const filteredRecords = useMemo(() => {
        return payrollRecords.filter(p => {
            const matchesStatus = statusFilter === "all" || p.status === statusFilter;
            const matchesSearch = 
                (p.driverName?.toLowerCase() || "").includes(searchQuery.toLowerCase());
            return matchesStatus && matchesSearch;
        });
    }, [payrollRecords, statusFilter, searchQuery]);

    const stats = useMemo(() => {
        const totalNetPay = payrollRecords.reduce((acc, curr) => acc + (curr.netPay || 0), 0);
        const pendingApproval = payrollRecords.filter(p => p.status === "PENDING_APPROVAL").length;
        const totalPaid = payrollRecords.filter(p => p.status === "PAID").length;
        return { totalNetPay, pendingApproval, totalPaid };
    }, [payrollRecords]);

    const getStatusColor = (status: string) => {
        switch (status) {
            case "DRAFT": return "bg-gray-500/10 text-gray-500 border-gray-500/20";
            case "PENDING_APPROVAL": return "bg-yellow-500/10 text-yellow-600 border-yellow-500/20";
            case "APPROVED": return "bg-blue-500/10 text-blue-600 border-blue-500/20";
            case "PAID": return "bg-green-500/10 text-green-600 border-green-500/20";
            case "CANCELLED": return "bg-red-500/10 text-red-600 border-red-500/20";
            default: return "bg-gray-500/10 text-gray-500 border-gray-500/20";
        }
    };

    const handleUpdateStatus = async (id: string, status: Payroll["status"]) => {
        try {
            const ref = doc(db, COLLECTIONS.PAYROLL, id);
            const updates: any = { 
                status,
                updatedAt: Timestamp.now()
            };
            
            if (status === "APPROVED") {
                updates.approvedBy = user?.uid;
                updates.approvedAt = Timestamp.now();
            }
            
            if (status === "PAID") {
                updates.paymentDate = Timestamp.now();
            }

            await updateDoc(ref, updates);
            toast.success(`Payroll marked as ${status.toLowerCase().replace("_", " ")}`);
        } catch (error) {
            console.error("Error updating payroll:", error);
            toast.error(t("payroll.toast.failedUpdate"));
        }
    };

    return (
        <div className="container mx-auto p-6 space-y-6 max-w-[1400px]">
            {/* Header */}
            <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">{t("nav.payroll")}</h1>
                    <p className="text-muted-foreground mt-1">
                        {t("payroll.subtitle")}
                    </p>
                </div>
                <div className="flex gap-3">
                    <Button variant="outline" onClick={() => toast.info("Export CSV coming soon")}>
                        <Download className="mr-2 h-4 w-4" />
                        {t("payroll.export")}
                    </Button>
                    <Button onClick={() => toast.info("Batch Generation coming soon")}>
                        {t("payroll.generatePayroll")}
                    </Button>
                </div>
            </div>

            {/* Stats */}
            <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                <Card>
                    <CardContent className="p-6 flex items-center justify-between">
                        <div>
                            <p className="text-sm font-medium text-muted-foreground">{t("payroll.stats.totalNetPay")}</p>
                            <h2 className="text-3xl font-bold">{stats.totalNetPay.toLocaleString()} THB</h2>
                        </div>
                        <Banknote className="h-8 w-8 text-green-500" />
                    </CardContent>
                </Card>
                <Card>
                    <CardContent className="p-6 flex items-center justify-between">
                        <div>
                            <p className="text-sm font-medium text-yellow-600">Pending Approval</p>
                            <h2 className="text-3xl font-bold text-yellow-600">{stats.pendingApproval}</h2>
                        </div>
                        <Clock className="h-8 w-8 text-yellow-500" />
                    </CardContent>
                </Card>
                <Card>
                    <CardContent className="p-6 flex items-center justify-between">
                        <div>
                            <p className="text-sm font-medium text-blue-600">{t("payroll.stats.successfullyPaid")}</p>
                            <h2 className="text-3xl font-bold text-blue-600">{stats.totalPaid}</h2>
                        </div>
                        <CheckCircle2 className="h-8 w-8 text-blue-500" />
                    </CardContent>
                </Card>
            </div>

            <PayrollReviewDialog 
                payroll={selectedPayroll}
                open={isReviewOpen}
                onOpenChange={setIsReviewOpen}
                onUpdateStatus={handleUpdateStatus}
            />

            {/* Filters */}
            <div className="flex flex-col sm:flex-row gap-4 bg-card/50 p-4 rounded-lg border border-border/50">
                <div className="relative flex-1 max-w-sm">
                    <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
                    <Input
                        placeholder={t("payroll.searchPlaceholder")}
                        className="pl-10"
                        value={searchQuery}
                        onChange={(e) => setSearchQuery(e.target.value)}
                    />
                </div>
                <div className="flex gap-2 overflow-x-auto pb-2 sm:pb-0">
                    {["all", ...PAYROLL_STATUS_ENUM].map((status) => (
                        <Button
                            key={status}
                            variant={statusFilter === status ? "secondary" : "ghost"}
                            size="sm"
                            onClick={() => setStatusFilter(status)}
                            className="whitespace-nowrap"
                        >
                            {status === "all" ? t("payroll.filter.all") : t(`payroll.status.${status.toLowerCase()}`)}
                        </Button>
                    ))}
                </div>
            </div>

            {/* Table */}
            <div className="border rounded-lg bg-card overflow-hidden">
                <Table>
                    <TableHeader className="bg-muted/50">
                        <TableRow>
                            <TableHead>{t("payroll.table.driver")}</TableHead>
                            <TableHead>{t("payroll.table.period")}</TableHead>
                            <TableHead>{t("payroll.table.earnings")}</TableHead>
                            <TableHead>{t("payroll.table.deductions")}</TableHead>
                            <TableHead>{t("payroll.table.netPay")}</TableHead>
                            <TableHead>{t("leaveRequests.table.status")}</TableHead>
                            <TableHead className="text-right">{t("common.actions")}</TableHead>
                        </TableRow>
                    </TableHeader>
                    <TableBody>
                        {loading ? (
                            <TableRow>
                                <TableCell colSpan={7} className="h-32 text-center">
                                    <div className="flex flex-col items-center justify-center gap-2">
                                        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
                                        <p className="text-sm text-muted-foreground">{t("payroll.loading")}</p>
                                    </div>
                                </TableCell>
                            </TableRow>
                        ) : filteredRecords.length === 0 ? (
                            <TableRow>
                                <TableCell colSpan={7} className="h-32 text-center text-muted-foreground">
                                    {t("payroll.noRecords")}
                                </TableCell>
                            </TableRow>
                        ) : (
                            filteredRecords.map((payroll) => (
                                <TableRow 
                                    key={payroll.id}
                                    className="cursor-pointer hover:bg-muted/50 transition-colors"
                                    onClick={() => {
                                        setSelectedPayroll(payroll);
                                        setIsReviewOpen(true);
                                    }}
                                >
                                    <TableCell>
                                        <div className="flex items-center gap-2">
                                            <div className="h-8 w-8 rounded-full bg-muted flex items-center justify-center">
                                                <User className="h-4 w-4 text-muted-foreground" />
                                            </div>
                                            <div>
                                                <p className="font-medium text-sm">{payroll.driverName || t("payroll.unknownDriver")}</p>
                                                <p className="text-[10px] text-muted-foreground font-mono">ID: {payroll.driverId.slice(-6)}</p>
                                            </div>
                                        </div>
                                    </TableCell>
                                    <TableCell>
                                        <div className="text-xs">
                                            <p className="font-medium">{format(payroll.periodStart, "dd MMM")}</p>
                                            <p className="text-muted-foreground">to {format(payroll.periodEnd, "dd MMM yyyy")}</p>
                                        </div>
                                    </TableCell>
                                    <TableCell>
                                        <div className="flex items-center gap-1 text-green-600 font-medium text-sm">
                                            <ArrowUpCircle className="h-3 w-3" />
                                            {payroll.totalEarnings?.toLocaleString()}
                                        </div>
                                    </TableCell>
                                    <TableCell>
                                        <div className="flex items-center gap-1 text-red-600 font-medium text-sm">
                                            <ArrowDownCircle className="h-3 w-3" />
                                            {payroll.totalDeductions?.toLocaleString()}
                                        </div>
                                    </TableCell>
                                    <TableCell>
                                        <span className="font-bold text-sm">
                                            {payroll.netPay?.toLocaleString()} {payroll.currency}
                                        </span>
                                    </TableCell>
                                    <TableCell>
                                        <Badge variant="outline" className={getStatusColor(payroll.status)}>
                                            {t(`payroll.status.${payroll.status.toLowerCase()}`)}
                                        </Badge>
                                    </TableCell>
                                    <TableCell className="text-right">
                                        <DropdownMenu>
                                            <DropdownMenuTrigger asChild>
                                                <Button variant="ghost" size="icon" onClick={(e) => e.stopPropagation()}>
                                                    <MoreHorizontal className="h-4 w-4" />
                                                </Button>
                                            </DropdownMenuTrigger>
                                            <DropdownMenuContent align="end" onClick={(e) => e.stopPropagation()}>
                                                <DropdownMenuLabel>{t("payroll.managePayroll")}</DropdownMenuLabel>
                                                <DropdownMenuItem onClick={() => {
                                                    setSelectedPayroll(payroll);
                                                    setIsReviewOpen(true);
                                                }}>
                                                    <Eye className="mr-2 h-4 w-4" /> {t("payroll.viewDetails")}
                                                </DropdownMenuItem>
                                                
                                                {payroll.status === "PENDING_APPROVAL" && (
                                                    <DropdownMenuItem 
                                                        className="text-blue-600"
                                                        onClick={() => payroll.id && handleUpdateStatus(payroll.id, "APPROVED")}
                                                    >
                                                        <CheckCircle2 className="mr-2 h-4 w-4" /> {t("payroll.approve")}
                                                    </DropdownMenuItem>
                                                )}

                                                {payroll.status === "APPROVED" && (
                                                    <DropdownMenuItem 
                                                        className="text-green-600"
                                                        onClick={() => payroll.id && handleUpdateStatus(payroll.id, "PAID")}
                                                    >
                                                        <Banknote className="mr-2 h-4 w-4" /> {t("payroll.markAsPaid")}
                                                    </DropdownMenuItem>
                                                )}

                                                <DropdownMenuItem className="text-red-600" onClick={() => toast.info("Cancellation coming soon")}>
                                                    <XCircle className="mr-2 h-4 w-4" /> {t("payroll.cancel")}
                                                </DropdownMenuItem>
                                            </DropdownMenuContent>
                                        </DropdownMenu>
                                    </TableCell>
                                </TableRow>
                            ))
                        )}
                    </TableBody>
                </Table>
            </div>
        </div>
    );
}

function XCircle(props: any) {
    return (
        <svg
            {...props}
            xmlns="http://www.w3.org/2000/svg"
            width="24"
            height="24"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
        >
            <circle cx="12" cy="12" r="10" />
            <path d="m15 9-6 6" />
            <path d="m9 9 6 6" />
        </svg>
    )
}
