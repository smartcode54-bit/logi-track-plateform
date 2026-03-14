"use client";

import { useState } from "react";
import { LeaveRequest } from "@/validate/leaveRequestSchema";
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
    Paperclip
} from "lucide-react";

interface LeaveRequestReviewDialogProps {
    request: LeaveRequest | null;
    open: boolean;
    onOpenChange: (open: boolean) => void;
    onUpdateStatus: (id: string, status: "APPROVED" | "REJECTED", reason?: string) => Promise<void>;
}

export function LeaveRequestReviewDialog({ 
    request, 
    open, 
    onOpenChange, 
    onUpdateStatus 
}: LeaveRequestReviewDialogProps) {
    const [isSubmitting, setIsSubmitting] = useState(false);
    const [rejectionReason, setRejectionReason] = useState("");
    const [showRejectionInput, setShowRejectionInput] = useState(false);

    if (!request) return null;

    const handleAction = async (status: "APPROVED" | "REJECTED") => {
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
                        Leave Request Details
                    </DialogTitle>
                    <DialogDescription>
                        Submitted on {format(request.createdAt || new Date(), "PPP")}
                    </DialogDescription>
                </DialogHeader>

                <div className="space-y-6 py-4">
                    {/* Driver & Info */}
                    <div className="grid grid-cols-2 gap-4">
                        <div className="space-y-1">
                            <Label className="text-xs text-muted-foreground uppercase">Driver</Label>
                            <div className="flex items-center gap-2">
                                <User className="h-4 w-4 text-muted-foreground" />
                                <span className="font-medium">{request.driverName}</span>
                            </div>
                        </div>
                        <div className="space-y-1">
                            <Label className="text-xs text-muted-foreground uppercase">Type</Label>
                            <div>
                                <Badge variant="secondary">{request.type}</Badge>
                            </div>
                        </div>
                    </div>

                    {/* Dates */}
                    <div className="space-y-2 rounded-lg bg-muted/50 p-3 border">
                        <Label className="text-xs text-muted-foreground uppercase">Duration</Label>
                        <div className="flex items-center justify-between">
                            <div className="flex flex-col">
                                <span className="text-sm font-bold">{format(request.startDate, "dd MMM yyyy")}</span>
                                <span className="text-[10px] text-muted-foreground">Start Date</span>
                            </div>
                            <Calendar className="h-4 w-4 text-muted-foreground" />
                            <div className="flex flex-col text-right">
                                <span className="text-sm font-bold">{format(request.endDate, "dd MMM yyyy")}</span>
                                <span className="text-[10px] text-muted-foreground">End Date</span>
                            </div>
                        </div>
                    </div>

                    {/* Reason */}
                    <div className="space-y-1">
                        <Label className="text-xs text-muted-foreground uppercase">Reason for Leave</Label>
                        <p className="text-sm bg-background border rounded-md p-3 min-h-[80px]">
                            {request.reason}
                        </p>
                    </div>

                    {/* Attachments */}
                    {request.attachments && request.attachments.length > 0 && (
                        <div className="space-y-2">
                            <Label className="text-xs text-muted-foreground uppercase">Attachments</Label>
                            <div className="flex flex-wrap gap-2">
                                {request.attachments.map((url, i) => (
                                    <Button key={i} variant="outline" size="sm" className="h-8 gap-2" asChild>
                                        <a href={url} target="_blank" rel="noopener noreferrer">
                                            <Paperclip className="h-3 w-3" />
                                            Document {i + 1}
                                        </a>
                                    </Button>
                                ))}
                            </div>
                        </div>
                    )}

                    {/* Rejection Input */}
                    {showRejectionInput && (
                        <div className="space-y-2 animate-in fade-in slide-in-from-top-1">
                            <Label htmlFor="rejectionReason" className="text-xs font-bold text-red-600 uppercase">Reason for Rejection *</Label>
                            <Textarea
                                id="rejectionReason"
                                placeholder="Explain why this request is being rejected..."
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
                            <Button 
                                variant="outline" 
                                className="text-red-600 hover:text-red-700 hover:bg-red-50"
                                onClick={() => handleAction("REJECTED")}
                                disabled={isSubmitting || request.status !== "PENDING"}
                            >
                                <XCircle className="mr-2 h-4 w-4" /> Reject
                            </Button>
                            <Button 
                                className="bg-green-600 hover:bg-green-700 text-white"
                                onClick={() => handleAction("APPROVED")}
                                disabled={isSubmitting || request.status !== "PENDING"}
                            >
                                {isSubmitting ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <CheckCircle2 className="mr-2 h-4 w-4" />}
                                Approve Request
                            </Button>
                        </>
                    ) : (
                        <>
                            <Button variant="ghost" onClick={() => setShowRejectionInput(false)}>Cancel</Button>
                            <Button 
                                variant="destructive" 
                                onClick={() => handleAction("REJECTED")}
                                disabled={isSubmitting || !rejectionReason.trim()}
                            >
                                {isSubmitting && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                                Confirm Rejection
                            </Button>
                        </>
                    )}
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
