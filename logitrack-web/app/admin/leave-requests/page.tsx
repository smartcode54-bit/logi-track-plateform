"use client";

import { useState, useEffect, useMemo } from "react";
import { collection, query, orderBy, onSnapshot, doc, updateDoc, Timestamp, where } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { LeaveRequest, LEAVE_STATUS_ENUM, LEAVE_TYPE_ENUM } from "@/validate/leaveRequestSchema";
import {
    CheckCircle2,
    XCircle,
    Clock,
    Search,
    Loader2,
    MoreHorizontal,
    FileText,
    User,
    Calendar,
    Filter
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
import { LeaveRequestReviewDialog } from "./LeaveRequestReviewDialog";

export default function LeaveRequestsPage() {
    const { t } = useLanguage();
    const auth = useAuth();
    const user = auth?.currentUser ?? null;
    const [requests, setRequests] = useState<LeaveRequest[]>([]);
    const [loading, setLoading] = useState(true);
    const [statusFilter, setStatusFilter] = useState<string>("all");
    const [searchQuery, setSearchQuery] = useState("");
    
    // Dialog state
    const [selectedRequest, setSelectedRequest] = useState<LeaveRequest | null>(null);
    const [isReviewOpen, setIsReviewOpen] = useState(false);

    // Fetch leave requests
    useEffect(() => {
        setLoading(true);
        const ref = collection(db, COLLECTIONS.LEAVE_REQUESTS);
        const q = query(ref, orderBy("createdAt", "desc"));

        const unsubscribe = onSnapshot(q, (snapshot) => {
            const fetched: LeaveRequest[] = [];
            snapshot.forEach((doc) => {
                const data = doc.data();
                fetched.push({
                    id: doc.id,
                    ...data,
                    startDate: data.startDate instanceof Timestamp ? data.startDate.toDate() : new Date(data.startDate),
                    endDate: data.endDate instanceof Timestamp ? data.endDate.toDate() : new Date(data.endDate),
                    createdAt: data.createdAt instanceof Timestamp ? data.createdAt.toDate() : new Date(data.createdAt),
                } as LeaveRequest);
            });
            setRequests(fetched);
            setLoading(false);
        }, (err) => {
            console.error("Error fetching leave requests:", err);
            toast.error("Failed to load leave requests");
            setLoading(false);
        });

        return () => unsubscribe();
    }, []);

    const filteredRequests = useMemo(() => {
        return requests.filter(r => {
            const matchesStatus = statusFilter === "all" || r.status === statusFilter;
            const matchesSearch = 
                (r.driverName?.toLowerCase() || "").includes(searchQuery.toLowerCase()) ||
                (r.reason?.toLowerCase() || "").includes(searchQuery.toLowerCase());
            return matchesStatus && matchesSearch;
        });
    }, [requests, statusFilter, searchQuery]);

    const stats = useMemo(() => {
        return {
            total: requests.length,
            pending: requests.filter(r => r.status === "PENDING").length,
            approved: requests.filter(r => r.status === "APPROVED").length,
        };
    }, [requests]);

    const getStatusColor = (status: string) => {
        switch (status) {
            case "PENDING": return "bg-yellow-500/10 text-yellow-600 border-yellow-500/20";
            case "APPROVED": return "bg-green-500/10 text-green-600 border-green-500/20";
            case "REJECTED": return "bg-red-500/10 text-red-600 border-red-500/20";
            default: return "bg-gray-500/10 text-gray-500 border-gray-500/20";
        }
    };

    const handleUpdateStatus = async (id: string, status: "APPROVED" | "REJECTED", rejectionReason?: string) => {
        try {
            const ref = doc(db, COLLECTIONS.LEAVE_REQUESTS, id);
            const updates: any = {
                status,
                approverId: user?.uid,
                approvedAt: Timestamp.now(),
                updatedAt: Timestamp.now(),
            };
            
            if (rejectionReason) {
                updates.rejectionReason = rejectionReason;
            }

            await updateDoc(ref, updates);
            toast.success(`Request ${status.toLowerCase()}`);
        } catch (error) {
            console.error("Error updating status:", error);
            toast.error("Failed to update status");
        }
    };

    return (
        <div className="container mx-auto p-6 space-y-6 max-w-[1400px]">
            {/* Header */}
            <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">{t("nav.leaveRequests")}</h1>
                    <p className="text-muted-foreground mt-1">
                        Review and manage driver leave applications.
                    </p>
                </div>
            </div>

            {/* Stats */}
            <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                <Card>
                    <CardContent className="p-6 flex items-center justify-between">
                        <div>
                            <p className="text-sm font-medium text-muted-foreground">Total Requests</p>
                            <h2 className="text-3xl font-bold">{stats.total}</h2>
                        </div>
                        <FileText className="h-8 w-8 text-blue-500" />
                    </CardContent>
                </Card>
                <Card className="border-yellow-500/20 bg-yellow-50/30 dark:bg-yellow-950/10">
                    <CardContent className="p-6 flex items-center justify-between">
                        <div>
                            <p className="text-sm font-medium text-yellow-600">Pending Review</p>
                            <h2 className="text-3xl font-bold text-yellow-600">{stats.pending}</h2>
                        </div>
                        <Clock className="h-8 w-8 text-yellow-500" />
                    </CardContent>
                </Card>
                <Card className="border-green-500/20 bg-green-50/30 dark:bg-green-950/10">
                    <CardContent className="p-6 flex items-center justify-between">
                        <div>
                            <p className="text-sm font-medium text-green-600">Approved This Month</p>
                            <h2 className="text-3xl font-bold text-green-600">{stats.approved}</h2>
                        </div>
                        <CheckCircle2 className="h-8 w-8 text-green-500" />
                    </CardContent>
                </Card>
            </div>

            <LeaveRequestReviewDialog 
                request={selectedRequest}
                open={isReviewOpen}
                onOpenChange={setIsReviewOpen}
                onUpdateStatus={handleUpdateStatus}
            />

            {/* Filters */}
            <div className="flex flex-col sm:flex-row gap-4 bg-card/50 p-4 rounded-lg border border-border/50">
                <div className="relative flex-1 max-w-sm">
                    <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
                    <Input
                        placeholder="Search driver name or reason..."
                        className="pl-10"
                        value={searchQuery}
                        onChange={(e) => setSearchQuery(e.target.value)}
                    />
                </div>
                <div className="flex gap-2">
                    {["all", ...LEAVE_STATUS_ENUM].map((status) => (
                        <Button
                            key={status}
                            variant={statusFilter === status ? "secondary" : "ghost"}
                            size="sm"
                            onClick={() => setStatusFilter(status)}
                            className="capitalize"
                        >
                            {status.toLowerCase()}
                        </Button>
                    ))}
                </div>
            </div>

            {/* Table */}
            <div className="border rounded-lg bg-card overflow-hidden">
                <Table>
                    <TableHeader className="bg-muted/50">
                        <TableRow>
                            <TableHead>Driver</TableHead>
                            <TableHead>Type</TableHead>
                            <TableHead>Duration</TableHead>
                            <TableHead>Reason</TableHead>
                            <TableHead>Status</TableHead>
                            <TableHead className="text-right">Actions</TableHead>
                        </TableRow>
                    </TableHeader>
                    <TableBody>
                        {loading ? (
                            <TableRow>
                                <TableCell colSpan={6} className="h-32 text-center">
                                    <div className="flex flex-col items-center justify-center gap-2">
                                        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
                                        <p className="text-sm text-muted-foreground">Loading requests...</p>
                                    </div>
                                </TableCell>
                            </TableRow>
                        ) : filteredRequests.length === 0 ? (
                            <TableRow>
                                <TableCell colSpan={6} className="h-32 text-center text-muted-foreground">
                                    No leave requests found.
                                </TableCell>
                            </TableRow>
                        ) : (
                            filteredRequests.map((req) => (
                                <TableRow 
                                    key={req.id} 
                                    className="cursor-pointer hover:bg-muted/50 transition-colors"
                                    onClick={() => {
                                        setSelectedRequest(req);
                                        setIsReviewOpen(true);
                                    }}
                                >
                                    <TableCell>
                                        <div className="flex items-center gap-2">
                                            <div className="h-8 w-8 rounded-full bg-muted flex items-center justify-center">
                                                <User className="h-4 w-4 text-muted-foreground" />
                                            </div>
                                            <div>
                                                <p className="font-medium text-sm">{req.driverName || "Unknown Driver"}</p>
                                                <p className="text-xs text-muted-foreground">ID: {req.driverId.slice(-6)}</p>
                                            </div>
                                        </div>
                                    </TableCell>
                                    <TableCell>
                                        <Badge variant="outline" className="text-[10px] font-bold">
                                            {req.type}
                                        </Badge>
                                    </TableCell>
                                    <TableCell>
                                        <div className="text-xs">
                                            <p className="font-medium">{format(req.startDate, "dd MMM")}</p>
                                            <p className="text-muted-foreground">to {format(req.endDate, "dd MMM yyyy")}</p>
                                        </div>
                                    </TableCell>
                                    <TableCell className="max-w-[200px] truncate text-sm">
                                        {req.reason}
                                    </TableCell>
                                    <TableCell>
                                        <Badge variant="outline" className={getStatusColor(req.status)}>
                                            {req.status}
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
                                                <DropdownMenuLabel>Manage Request</DropdownMenuLabel>
                                                {req.status === "PENDING" && (
                                                    <>
                                                        <DropdownMenuItem 
                                                            className="text-green-600"
                                                            onClick={() => req.id && handleUpdateStatus(req.id, "APPROVED")}
                                                        >
                                                            <CheckCircle2 className="mr-2 h-4 w-4" /> Approve
                                                        </DropdownMenuItem>
                                                        <DropdownMenuItem 
                                                            className="text-red-600"
                                                            onClick={() => {
                                                                setSelectedRequest(req);
                                                                setIsReviewOpen(true);
                                                            }}
                                                        >
                                                            <XCircle className="mr-2 h-4 w-4" /> Reject
                                                        </DropdownMenuItem>
                                                    </>
                                                )}
                                                <DropdownMenuItem onClick={() => {
                                                    setSelectedRequest(req);
                                                    setIsReviewOpen(true);
                                                }}>
                                                    <FileText className="mr-2 h-4 w-4" /> View Details
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
