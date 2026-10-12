"use client";

import { useState } from "react";
import { LeaveRequest } from "@/validate/leaveRequestSchema";
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
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Textarea } from "@/components/ui/textarea";
import { format } from "date-fns";
import { 
    Calendar, 
    User, 
    FileText, 
    CheckCircle2, 
    XCircle, 
    Loader2,
    Paperclip,
    Ban
} from "lucide-react";

interface LeaveRequestReviewDialogProps {
    request: LeaveRequest | null;
    open: boolean;
    onOpenChange: (open: boolean) => void;
    onUpdateStatus: (id: string, status: "APPROVED" | "REJECTED" | "CANCELLED", reason?: string) => Promise<void>;
}

export function LeaveRequestReviewDialog({ 
    request, 
    open, 
    onOpenChange, 
    onUpdateStatus 
}: LeaveRequestReviewDialogProps) {
    const { t } = useLanguage();
    const [isSubmitting, setIsSubmitting] = useState(false);
    const [rejectionReason, setRejectionReason] = useState("");
    const [showRejectionInput, setShowRejectionInput] = useState(false);

    if (!request) return null;

    const handleAction = async (status: "APPROVED" | "REJECTED" | "CANCELLED") => {
        if (status === "REJECTED" && !showRejectionInput) {
            setShowRejectionInput(true);
            return;
        }

        setIsSubmitting(true);
        try {
            await onUpdateStatus(request.id!, status, status === "REJECTED" ? rejectionReason : undefined);
            onOpenChange(false);
            setShowRejectionInput(false);
            setRejectionReason("");
        } finally {
            setIsSubmitting(false);
        }
    };

    return (
        <Dialog open={open} onOpenChange={(val) => {
            onOpenChange(val);
            if (!val) {
                setShowRejectionInput(false);
                setRejectionReason("");
            }
        }}>
            <DialogContent className="sm:max-w-[500px]">
                <DialogHeader>
                    <DialogTitle className="flex items-center gap-2">
                        <FileText className="h-5 w-5 text-blue-600" />
                        {t("leaveRequests.dialog.title")}
                    </DialogTitle>
                    <DialogDescription>
                        {t("leaveRequests.dialog.submittedOn")} {format(request.createdAt || new Date(), "PPP")}
                    </DialogDescription>
                </DialogHeader>

                <div className="space-y-6 py-4">
                    {/* Driver & Info */}
                    <div className="grid grid-cols-2 gap-4">
                        <div className="space-y-1">
                            <Label className="text-xs text-muted-foreground uppercase">{t("leaveRequests.dialog.driver")}</Label>
                            <div className="flex items-center gap-2">
                                <User className="h-4 w-4 text-muted-foreground" />
                                <span className="font-medium">{request.driverName}</span>
                            </div>
                        </div>
                        <div className="space-y-1">
                            <Label className="text-xs text-muted-foreground uppercase">{t("leaveRequests.dialog.type")}</Label>
                            <div>
                                <Badge variant="secondary">{request.type}</Badge>
                            </div>
                        </div>
                    </div>

                    {/* Dates */}
                    <div className="space-y-2 rounded-lg bg-muted/50 p-3 border">
                        <Label className="text-xs text-muted-foreground uppercase">{t("leaveRequests.dialog.duration")}</Label>
                        <div className="flex items-center justify-between">
                            <div className="flex flex-col">
                                <span className="text-sm font-bold">{format(request.startDate, "dd MMM yyyy")}</span>
                                <span className="text-[10px] text-muted-foreground">{t("leaveRequests.dialog.startDate")}</span>
                            </div>
                            <Calendar className="h-4 w-4 text-muted-foreground" />
                            <div className="flex flex-col text-right">
                                <span className="text-sm font-bold">{format(request.endDate, "dd MMM yyyy")}</span>
                                <span className="text-[10px] text-muted-foreground">{t("leaveRequests.dialog.endDate")}</span>
                            </div>
                        </div>
                    </div>

                    {/* Reason */}
                    <div className="space-y-1">
                        <Label className="text-xs text-muted-foreground uppercase">{t("leaveRequests.dialog.reasonForLeave")}</Label>
                        <p className="text-sm bg-background border rounded-md p-3 min-h-[80px]">
                            {request.reason}
                        </p>
                    </div>

                    {/* Attachments */}
                    {request.attachments && request.attachments.length > 0 && (
                        <div className="space-y-2">
                            <Label className="text-xs text-muted-foreground uppercase">{t("leaveRequests.dialog.attachments")}</Label>
                            <div className="flex flex-wrap gap-2">
                                {request.attachments.map((url, i) => (
                                    <Button key={i} variant="outline" size="sm" className="h-8 gap-2" asChild>
                                        <a href={url} target="_blank" rel="noopener noreferrer">
                                            <Paperclip className="h-3 w-3" />
                                            {t("leaveRequests.dialog.document")} {i + 1}
                                        </a>
                                    </Button>
                                ))}
                            </div>
                        </div>
                    )}

                    {/* Rejection Input */}
                    {showRejectionInput && (
                        <div className="space-y-2 animate-in fade-in slide-in-from-top-1">
                            <Label htmlFor="rejectionReason" className="text-xs font-bold text-red-600 uppercase">{t("leaveRequests.dialog.reasonForRejection")}</Label>
                            <Textarea
                                id="rejectionReason"
                                placeholder={t("leaveRequests.dialog.rejectionPlaceholder")}
                                value={rejectionReason}
                                onChange={(e) => setRejectionReason(e.target.value)}
                                className="border-red-200 focus-visible:ring-red-500"
                            />
                        </div>
                    )}
                </div>

                <DialogFooter className="gap-2 sm:gap-0">
                    {!showRejectionInput ? (
                        <>
                            {request.status === "APPROVED" && (
                                <Button 
                                    variant="outline" 
                                    className="text-amber-600 hover:text-amber-700 hover:bg-amber-50 dark:hover:bg-amber-950/30"
                                    onClick={() => handleAction("CANCELLED")}
                                    disabled={isSubmitting}
                                >
                                    <Ban className="mr-2 h-4 w-4" /> {t("leaveRequests.cancelRequest")}
                                </Button>
                            )}
                            <Button 
                                variant="outline" 
                                className="text-red-600 hover:text-red-700 hover:bg-red-50"
                                onClick={() => handleAction("REJECTED")}
                                disabled={isSubmitting || request.status !== "PENDING"}
                            >
                                <XCircle className="mr-2 h-4 w-4" /> {t("leaveRequests.reject")}
                            </Button>
                            <Button 
                                className="bg-green-600 hover:bg-green-700 text-white"
                                onClick={() => handleAction("APPROVED")}
                                disabled={isSubmitting || request.status !== "PENDING"}
                            >
                                {isSubmitting ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <CheckCircle2 className="mr-2 h-4 w-4" />}
                                {t("leaveRequests.dialog.approveRequest")}
                            </Button>
                        </>
                    ) : (
                        <>
                            <Button variant="ghost" onClick={() => setShowRejectionInput(false)}>{t("leaveRequests.dialog.cancelButton")}</Button>
                            <Button 
                                variant="destructive" 
                                onClick={() => handleAction("REJECTED")}
                                disabled={isSubmitting || !rejectionReason.trim()}
                            >
                                {isSubmitting && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                                {t("leaveRequests.dialog.confirmRejection")}
                            </Button>
                        </>
                    )}
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
