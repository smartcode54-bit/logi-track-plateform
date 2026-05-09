"use client";

import { useState } from "react";
import { Payroll } from "@/validate/payrollSchema";
import { useLanguage } from "@/context/language";
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { format } from "date-fns";
import { 
    Banknote, 
    User, 
    CheckCircle2, 
    XCircle, 
    Loader2,
    Calendar,
    ArrowUpCircle,
    ArrowDownCircle,
    Download
} from "lucide-react";

interface PayrollReviewDialogProps {
    payroll: Payroll | null;
    open: boolean;
    onOpenChange: (open: boolean) => void;
    onUpdateStatus: (id: string, status: Payroll["status"]) => Promise<void>;
}

export function PayrollReviewDialog({ 
    payroll, 
    open, 
    onOpenChange, 
    onUpdateStatus 
}: PayrollReviewDialogProps) {
    const { t } = useLanguage();
    const [isSubmitting, setIsSubmitting] = useState(false);

    if (!payroll) return null;

    const handleAction = async (status: Payroll["status"]) => {
        setIsSubmitting(true);
        try {
            await onUpdateStatus(payroll.id!, status);
            onOpenChange(false);
        } finally {
            setIsSubmitting(false);
        }
    };

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

    // Separate line items
    const earnings = payroll.lineItems?.filter(item => item.type === "EARNING") || [];
    const deductions = payroll.lineItems?.filter(item => item.type === "DEDUCTION") || [];

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="sm:max-w-[600px] max-h-[90vh] overflow-y-auto">
                <DialogHeader className="border-b pb-4 mb-4">
                    <div className="flex justify-between items-start">
                        <div>
                            <DialogTitle className="flex items-center gap-2 text-xl">
                                <Banknote className="h-6 w-6 text-green-600" />
                                {t("payroll.dialog.title")}
                            </DialogTitle>
                            <DialogDescription className="mt-1">
                                {t("payroll.dialog.generatedOn")} {format(payroll.createdAt || new Date(), "PPP")}
                            </DialogDescription>
                        </div>
                        <Badge variant="outline" className={`${getStatusColor(payroll.status)} text-sm px-3 py-1`}>
                            {t(`payroll.status.${payroll.status.toLowerCase()}`)}
                        </Badge>
                    </div>
                </DialogHeader>

                <div className="space-y-6 py-2">
                    {/* Driver & Period Info */}
                    <div className="grid grid-cols-2 gap-4 bg-muted/30 p-4 rounded-lg border">
                        <div className="space-y-1">
                            <span className="text-xs text-muted-foreground uppercase font-semibold">{t("payroll.dialog.driver")}</span>
                            <div className="flex items-center gap-2">
                                <User className="h-4 w-4 text-muted-foreground" />
                                <span className="font-medium">{payroll.driverName}</span>
                            </div>
                            <span className="text-[10px] text-muted-foreground font-mono block mt-0.5">
                                ID: {payroll.driverId}
                            </span>
                        </div>
                        <div className="space-y-1">
                            <span className="text-xs text-muted-foreground uppercase font-semibold">{t("payroll.dialog.payPeriod")}</span>
                            <div className="flex items-center gap-2">
                                <Calendar className="h-4 w-4 text-muted-foreground" />
                                <span className="font-medium text-sm">
                                    {format(payroll.periodStart, "dd MMM")} - {format(payroll.periodEnd, "dd MMM yyyy")}
                                </span>
                            </div>
                        </div>
                    </div>

                    {/* Breakdown */}
                    <div className="space-y-4">
                        {/* Earnings */}
                        <div>
                            <h4 className="flex items-center gap-2 font-semibold text-green-700 mb-2 border-b pb-1">
                                <ArrowUpCircle className="h-4 w-4" /> {t("payroll.dialog.earnings")}
                            </h4>
                            {earnings.length > 0 ? (
                                <div className="space-y-2">
                                    {earnings.map((item, idx) => (
                                        <div key={idx} className="flex justify-between text-sm items-center">
                                            <div>
                                                <span className="font-medium">{item.name}</span>
                                                {item.description && <span className="text-xs text-muted-foreground ml-2 block">{item.description}</span>}
                                            </div>
                                            <span className="font-medium text-green-700">+{item.amount.toLocaleString()} {payroll.currency}</span>
                                        </div>
                                    ))}
                                </div>
                            ) : (
                                <p className="text-sm text-muted-foreground italic">{t("payroll.dialog.noEarnings")}</p>
                            )}
                        </div>

                        {/* Deductions */}
                        <div>
                            <h4 className="flex items-center gap-2 font-semibold text-red-700 mb-2 border-b pb-1 mt-6">
                                <ArrowDownCircle className="h-4 w-4" /> {t("payroll.dialog.deductions")}
                            </h4>
                            {deductions.length > 0 ? (
                                <div className="space-y-2">
                                    {deductions.map((item, idx) => (
                                        <div key={idx} className="flex justify-between text-sm items-center">
                                            <div>
                                                <span className="font-medium">{item.name}</span>
                                                {item.description && <span className="text-xs text-muted-foreground ml-2 block">{item.description}</span>}
                                            </div>
                                            <span className="font-medium text-red-700">-{item.amount.toLocaleString()} {payroll.currency}</span>
                                        </div>
                                    ))}
                                </div>
                            ) : (
                                <p className="text-sm text-muted-foreground italic">{t("payroll.dialog.noDeductions")}</p>
                            )}
                        </div>
                    </div>

                    {/* Totals */}
                    <div className="bg-muted p-4 rounded-lg border space-y-2 mt-4">
                        <div className="flex justify-between text-sm font-medium text-muted-foreground">
                            <span>{t("payroll.dialog.totalEarnings")}</span>
                            <span>{payroll.totalEarnings?.toLocaleString()} {payroll.currency}</span>
                        </div>
                        <div className="flex justify-between text-sm font-medium text-muted-foreground pb-2 border-b border-border/50">
                            <span>{t("payroll.dialog.totalDeductions")}</span>
                            <span>-{payroll.totalDeductions?.toLocaleString()} {payroll.currency}</span>
                        </div>
                        <div className="flex justify-between text-lg font-bold pt-1">
                            <span>{t("payroll.dialog.netPay")}</span>
                            <span className="text-primary">{payroll.netPay?.toLocaleString()} {payroll.currency}</span>
                        </div>
                    </div>
                    
                    {/* Payment Details */}
                    {(payroll.paymentDate || payroll.paymentMethod) && (
                        <div className="text-sm text-muted-foreground bg-green-50/50 dark:bg-green-950/20 p-3 rounded border border-green-100 dark:border-green-900">
                            {payroll.paymentDate && <p>{t("payroll.dialog.paidOn")} <span className="font-medium text-foreground">{format(payroll.paymentDate, "PPP")}</span></p>}
                            {payroll.paymentMethod && <p>{t("payroll.dialog.method")} <span className="font-medium text-foreground">{payroll.paymentMethod}</span></p>}
                        </div>
                    )}
                </div>

                <DialogFooter className="gap-2 sm:gap-0 mt-4 border-t pt-4">
                    <div className="flex w-full justify-between items-center">
                        <Button variant="outline" className="gap-2">
                            <Download className="h-4 w-4" /> {t("payroll.dialog.downloadPdf")}
                        </Button>
                        
                        <div className="flex gap-2">
                            <Button variant="ghost" onClick={() => onOpenChange(false)}>{t("payroll.dialog.close")}</Button>
                            
                            {payroll.status === "PENDING_APPROVAL" && (
                                <Button 
                                    className="bg-blue-600 hover:bg-blue-700 text-white gap-2"
                                    onClick={() => handleAction("APPROVED")}
                                    disabled={isSubmitting}
                                >
                                    {isSubmitting ? <Loader2 className="h-4 w-4 animate-spin" /> : <CheckCircle2 className="h-4 w-4" />}
                                    {t("payroll.dialog.approvePayroll")}
                                </Button>
                            )}

                            {payroll.status === "APPROVED" && (
                                <Button 
                                    className="bg-green-600 hover:bg-green-700 text-white gap-2"
                                    onClick={() => handleAction("PAID")}
                                    disabled={isSubmitting}
                                >
                                    {isSubmitting ? <Loader2 className="h-4 w-4 animate-spin" /> : <Banknote className="h-4 w-4" />}
                                    {t("payroll.markAsPaid")}
                                </Button>
                            )}
                        </div>
                    </div>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
