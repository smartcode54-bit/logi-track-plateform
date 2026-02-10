"use client";

import { useState, useEffect } from "react";
import Link from "next/link";
import { useSearchParams, useRouter } from "next/navigation";
import {
    ArrowLeft, Edit, Truck, Calendar, User, FileText, Info, Loader2, Camera,
    MapPin, Phone, Shield, MoreHorizontal, Download, Plus,
    Wrench, Mail, CreditCard, Briefcase
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Separator } from "@/components/ui/separator";
import Image from "next/image";
import { getDriverByIdClient } from "../actions.client";
import { getDriverAssignmentHistory, AssignmentData } from "../../truck-assignment/actions.client";
import { Driver } from "@/validate/driverSchema";
import { FileViewer } from "@/components/ui/file-viewer";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from "@/components/ui/table";
import { useBreadcrumb } from "@/context/breadcrumb";
import { format } from "date-fns";
import { getSubcontractors } from "../../subcontractors/actions.client";

export default function DriverPreviewClient() {
    const searchParams = useSearchParams();
    const router = useRouter();
    const driverId = searchParams.get('id');
    const { setCustomLastItem } = useBreadcrumb();

    const [driver, setDriver] = useState<Driver | null>(null);
    const [isLoading, setIsLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);

    const [isViewerOpen, setIsViewerOpen] = useState(false);
    const [viewerIndex, setViewerIndex] = useState(0);
    const [subcontractors, setSubcontractors] = useState<any[]>([]);
    const [assignmentHistory, setAssignmentHistory] = useState<AssignmentData[]>([]);

    useEffect(() => {
        const fetchDriver = async () => {
            if (!driverId) {
                setError("No driver ID provided");
                setIsLoading(false);
                return;
            }
            try {
                setIsLoading(true);
                setError(null);
                const data = await getDriverByIdClient(driverId);
                if (!data) {
                    setError("Driver not found.");
                    return;
                }
                setDriver(data);
                setCustomLastItem(`${data.firstName} ${data.lastName}`);

                // Fetch Assignment History
                getDriverAssignmentHistory(driverId).then(setAssignmentHistory);
            } catch (err) {
                console.error("Error fetching driver:", err);
                setError(err instanceof Error ? err.message : "Failed to load driver data.");
            } finally {
                setIsLoading(false);
            }
        };

        fetchDriver();

        // Cleanup breadcrumb on unmount
        return () => {
            setCustomLastItem(null);
        };
    }, [driverId, setCustomLastItem]);

    // Fetch subcontractors to resolve names
    useEffect(() => {
        getSubcontractors().then(setSubcontractors);
    }, []);

    const formatDate = (date: Date | string | null | undefined) => {
        if (!date) return "-";
        try {
            return format(new Date(date), "dd MMMM yyyy");
        } catch (e) {
            return "-";
        }
    };

    const StatusBadge = ({ status }: { status: string }) => {
        const styles = {
            Active: "bg-green-100 text-green-800 hover:bg-green-100",
            Inactive: "bg-gray-100 text-gray-800 hover:bg-gray-100",
            "On-Duty": "bg-blue-100 text-blue-800 hover:bg-blue-100",
        };

        const statusKey = status as keyof typeof styles;
        return (
            <Badge className={styles[statusKey] || styles.Inactive} variant="outline">
                {status}
            </Badge>
        );
    };

    if (isLoading) {
        return (
            <div className="flex items-center justify-center min-h-[400px]">
                <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
                <span className="ml-2 text-muted-foreground">Loading driver data...</span>
            </div>
        );
    }

    if (error || !driver) {
        return (
            <div className="container mx-auto px-4 py-8 max-w-5xl">
                <div className="text-center py-12">
                    <h2 className="text-2xl font-bold text-destructive mb-4">
                        {error || "Driver not found"}
                    </h2>
                    <Button asChild>
                        <Link href="/admin/drivers">Back to Drivers</Link>
                    </Button>
                </div>
            </div>
        );
    }

    // Construct viewable files list
    const viewableFiles = [
        ...(driver.profileImage ? [{ url: driver.profileImage, type: "image" as const, label: "Profile Photo" }] : []),
        ...(driver.idCardImage ? [{ url: driver.idCardImage, type: "pdf" as const, label: "ID Card" }] : []),
        ...(driver.truckLicenseImage ? [{ url: driver.truckLicenseImage, type: "pdf" as const, label: "Driver License" }] : []),
    ];

    const handleFileClick = (url: string) => {
        const index = viewableFiles.findIndex(f => f.url === url);
        if (index !== -1) {
            setViewerIndex(index);
            setIsViewerOpen(true);
        }
    };

    const getSubcontractorName = (id?: string) => {
        if (!id) return null;
        const sub = subcontractors.find(s => s.id === id);
        return sub ? sub.name : id;
    };

    return (
        <div className="container mx-auto p-6 space-y-6 max-w-[1600px]">
            {/* Header */}
            <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4">
                <div className="flex items-center gap-4">
                    <Button variant="ghost" size="icon" asChild className="-ml-2">
                        <Link href="/admin/drivers">
                            <ArrowLeft className="h-5 w-5" />
                        </Link>
                    </Button>
                    <div>
                        <div className="flex items-center gap-3 mb-1">
                            <h1 className="text-3xl font-bold tracking-tight">{driver.firstName} {driver.lastName}</h1>
                            <StatusBadge status={driver.status} />
                        </div>
                        <div className="flex items-center gap-2 text-sm text-muted-foreground">
                            <span className="flex items-center gap-1">
                                <Info className="h-3.5 w-3.5" />
                                Registered: {formatDate(driver.createdAt)}
                            </span>
                            <span>•</span>
                            <span>ID: {driver.id?.substring(0, 8).toUpperCase() ?? "-"}</span>
                        </div>
                    </div>
                </div>
                <div className="flex items-center gap-3">
                    <Button asChild className="gap-2 bg-blue-600 hover:bg-blue-700 text-white">
                        <Link href={`/admin/drivers/edit?id=${driver?.id}`}>
                            <Edit className="h-4 w-4" />
                            Edit Profile
                        </Link>
                    </Button>
                </div>
            </div>

            <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
                {/* Left Column - Main Info */}
                <div className="lg:col-span-2 space-y-6">
                    {/* Personal Information */}
                    <Card>
                        <CardHeader className="pb-3">
                            <CardTitle className="flex items-center gap-2 text-base font-semibold">
                                <User className="h-5 w-5 text-blue-600" />
                                Personal Information
                            </CardTitle>
                        </CardHeader>
                        <CardContent className="grid grid-cols-1 md:grid-cols-2 gap-y-4 gap-x-12">
                            <div className="flex justify-between py-2 border-b">
                                <span className="text-sm text-muted-foreground flex items-center gap-2">
                                    <Phone className="h-4 w-4" /> Mobile
                                </span>
                                <span className="text-sm font-medium">{driver.mobile}</span>
                            </div>
                            <div className="flex justify-between py-2 border-b">
                                <span className="text-sm text-muted-foreground flex items-center gap-2">
                                    <Mail className="h-4 w-4" /> Email
                                </span>
                                <span className="text-sm font-medium">{driver.email || "-"}</span>
                            </div>
                            <div className="flex justify-between py-2 border-b">
                                <span className="text-sm text-muted-foreground flex items-center gap-2">
                                    <Calendar className="h-4 w-4" /> Birth Date
                                </span>
                                <span className="text-sm font-medium">{formatDate(driver.birthDate)}</span>
                            </div>
                            <div className="flex justify-between py-2 border-b">
                                <span className="text-sm text-muted-foreground flex items-center gap-2">
                                    <CreditCard className="h-4 w-4" /> ID Card No.
                                </span>
                                <span className="text-sm font-medium font-mono">{driver.idCard}</span>
                            </div>
                        </CardContent>
                    </Card>

                    {/* Employment Details */}
                    <Card>
                        <CardHeader className="pb-3">
                            <CardTitle className="flex items-center gap-2 text-base font-semibold">
                                <Briefcase className="h-5 w-5 text-blue-600" />
                                Employment Details
                            </CardTitle>
                        </CardHeader>
                        <CardContent>
                            <div className="grid grid-cols-1 md:grid-cols-2 gap-y-4 gap-x-12">
                                <div className="flex justify-between py-2 border-b">
                                    <span className="text-sm text-muted-foreground">Employment Type</span>
                                    <span className="text-sm font-medium">
                                        {driver.employmentType === 'FULL_TIME' ? 'Full Time' :
                                            driver.employmentType === 'PART_TIME' ? 'Part Time' : 'Subcontractor'}
                                    </span>
                                </div>
                                {driver.employmentType === 'SUBCONTRACTOR' && (
                                    <div className="flex justify-between py-2 border-b">
                                        <span className="text-sm text-muted-foreground">Subcontractor</span>
                                        <span className="text-sm font-medium text-blue-600">
                                            {getSubcontractorName(driver.subcontractorId)}
                                        </span>
                                    </div>
                                )}
                                <div className="flex justify-between py-2 border-b">
                                    <span className="text-sm text-muted-foreground">Contract Duration</span>
                                    <span className="text-sm font-medium">{driver.contractYears ? `${driver.contractYears} Years` : "-"}</span>
                                </div>
                                <div className="flex justify-between py-2 border-b">
                                    <span className="text-sm text-muted-foreground">Truck License ID</span>
                                    <span className="text-sm font-medium font-mono">{driver.truckLicenseId}</span>
                                </div>
                                <div className="flex justify-between py-2 border-b">
                                    <span className="text-sm text-muted-foreground">License Type</span>
                                    <span className="text-sm font-medium">{driver.licenseType || "-"}</span>
                                </div>
                            </div>
                        </CardContent>
                    </Card>

                    {/* Documents */}
                    <Card>
                        <CardHeader className="pb-3 border-b">
                            <CardTitle className="flex items-center gap-2 text-base font-semibold">
                                <FileText className="h-5 w-5 text-blue-600" />
                                Documents
                            </CardTitle>
                        </CardHeader>
                        <CardContent className="pt-4 grid grid-cols-1 sm:grid-cols-2 gap-4">
                            {/* ID Card */}
                            <div className="space-y-2">
                                <p className="text-xs font-semibold text-muted-foreground uppercase">ID Card</p>
                                {driver.idCardImage ? (
                                    <div
                                        className="relative aspect-[4/3] rounded-md overflow-hidden bg-muted cursor-pointer border hover:border-blue-500 transition-all"
                                        onClick={() => handleFileClick(driver.idCardImage!)}
                                    >
                                        {driver.idCardImage.toLowerCase().includes('.pdf') ? (
                                            <div className="flex flex-col items-center justify-center h-full bg-muted/50">
                                                <FileText className="h-10 w-10 text-red-500 mb-2" />
                                                <span className="text-xs font-medium text-muted-foreground">View PDF</span>
                                            </div>
                                        ) : (
                                            <Image
                                                src={driver.idCardImage}
                                                alt="ID Card"
                                                fill
                                                className="object-contain"
                                            />
                                        )}
                                    </div>
                                ) : (
                                    <div className="flex items-center justify-center aspect-[4/3] rounded-md bg-muted/30 border border-dashed">
                                        <p className="text-xs text-muted-foreground">No document</p>
                                    </div>
                                )}
                            </div>

                            {/* License */}
                            <div className="space-y-2">
                                <p className="text-xs font-semibold text-muted-foreground uppercase">Driving License</p>
                                {driver.truckLicenseImage ? (
                                    <div
                                        className="relative aspect-[4/3] rounded-md overflow-hidden bg-muted cursor-pointer border hover:border-blue-500 transition-all"
                                        onClick={() => handleFileClick(driver.truckLicenseImage!)}
                                    >
                                        {driver.truckLicenseImage.toLowerCase().includes('.pdf') ? (
                                            <div className="flex flex-col items-center justify-center h-full bg-muted/50">
                                                <FileText className="h-10 w-10 text-red-500 mb-2" />
                                                <span className="text-xs font-medium text-muted-foreground">View PDF</span>
                                            </div>
                                        ) : (
                                            <Image
                                                src={driver.truckLicenseImage}
                                                alt="Driving License"
                                                fill
                                                className="object-contain"
                                            />
                                        )}
                                    </div>
                                ) : (
                                    <div className="flex items-center justify-center aspect-[4/3] rounded-md bg-muted/30 border border-dashed">
                                        <p className="text-xs text-muted-foreground">No document</p>
                                    </div>
                                )}
                            </div>
                        </CardContent>
                    </Card>
                </div>

                {/* Right Column - Sidebar */}
                <div className="space-y-6">
                    {/* Profile Image */}
                    <Card>
                        <CardContent className="pt-6 flex flex-col items-center">
                            <div
                                className="relative h-40 w-40 rounded-full overflow-hidden border-4 border-muted cursor-pointer mb-4 hover:opacity-90 transition-opacity"
                                onClick={() => driver.profileImage && handleFileClick(driver.profileImage)}
                            >
                                {driver.profileImage ? (
                                    <Image src={driver.profileImage} alt={driver.firstName} fill className="object-cover" />
                                ) : (
                                    <div className="h-full w-full bg-slate-100 flex items-center justify-center text-slate-300">
                                        <User className="h-20 w-20" />
                                    </div>
                                )}
                            </div>
                            <h3 className="text-xl font-bold">{driver.firstName} {driver.lastName}</h3>
                            <p className="text-sm text-muted-foreground">{driver.email}</p>
                            <div className="flex gap-2 mt-4 w-full">
                                <Button className="flex-1" variant="outline">
                                    <Phone className="h-4 w-4 mr-2" />
                                    Call
                                </Button>
                                <Button className="flex-1" variant="outline">
                                    <Mail className="h-4 w-4 mr-2" />
                                    Email
                                </Button>
                            </div>
                        </CardContent>
                    </Card>

                    {/* Current Assignment */}
                    <Card>
                        <CardHeader className="flex flex-row items-center justify-between py-6">
                            <CardTitle className="text-lg font-bold flex items-center gap-2">
                                <Truck className="h-5 w-5" />
                                Truck Assignment
                            </CardTitle>
                            {!driver.currentAssignment && (
                                <Button size="sm" className="h-8 gap-2" variant="outline" asChild>
                                    <Link href="/admin/truck-assignment">
                                        <Plus className="h-3.5 w-3.5" />
                                        Assign
                                    </Link>
                                </Button>
                            )}
                        </CardHeader>
                        <CardContent className="p-0">
                            <Table>
                                <TableHeader>
                                    <TableRow>
                                        <TableHead className="w-[120px] text-xs">Truck</TableHead>
                                        <TableHead className="text-xs">Start</TableHead>
                                        <TableHead className="text-xs">End</TableHead>
                                        <TableHead className="text-right text-xs">Status</TableHead>
                                    </TableRow>
                                </TableHeader>
                                <TableBody>
                                    {assignmentHistory.length > 0 ? (
                                        assignmentHistory.map((history) => (
                                            <TableRow key={history.id}>
                                                <TableCell>
                                                    <div className="font-medium text-xs">{history.truckPlate}</div>
                                                    <div className="text-[10px] text-muted-foreground">{history.truckModel}</div>
                                                </TableCell>
                                                <TableCell className="text-xs text-muted-foreground">
                                                    {format(history.createdAt, "dd MMM yy")}
                                                </TableCell>
                                                <TableCell className="text-xs text-muted-foreground">
                                                    {history.revokedAt ? format(history.revokedAt, "dd MMM yy") : "-"}
                                                </TableCell>
                                                <TableCell className="text-right">
                                                    <Badge
                                                        variant="secondary"
                                                        className={`text-[10px] px-1.5 h-5 ${!history.revokedAt ? 'bg-green-100 text-green-600 hover:bg-green-100' : 'bg-gray-100 text-gray-500 hover:bg-gray-100'}`}
                                                    >
                                                        {!history.revokedAt ? 'Current' : 'Revoked'}
                                                    </Badge>
                                                </TableCell>
                                            </TableRow>
                                        ))
                                    ) : (
                                        <TableRow>
                                            <TableCell colSpan={4} className="h-24 text-center text-xs text-muted-foreground">
                                                No assignment history found.
                                            </TableCell>
                                        </TableRow>
                                    )}
                                </TableBody>
                            </Table>
                        </CardContent>
                    </Card>
                </div>
            </div>

            <FileViewer
                isOpen={isViewerOpen}
                onClose={() => setIsViewerOpen(false)}
                files={viewableFiles}
                initialIndex={viewerIndex}
            />
        </div>
    );
}
