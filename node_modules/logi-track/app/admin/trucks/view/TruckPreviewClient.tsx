"use client";

import { useState, useEffect } from "react";
import Link from "next/link";
import { useSearchParams, useRouter } from "next/navigation";
import {
    ArrowLeft, Edit, Truck, Calendar, User, FileText, Info, Loader2, Camera,
    MapPin, Phone, Shield, MoreHorizontal, Download, Plus,
    Wrench
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Separator } from "@/components/ui/separator";
import Image from "next/image";
import { getTruckByIdClient, TruckData } from "../actions.client";
import { getTruckAssignmentHistory, AssignmentData } from "../../truck-assignment/actions.client";
import { FileViewer } from "@/components/ui/file-viewer";
import { getSubcontractors } from "../../subcontractors/actions.client";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { useBreadcrumb } from "@/context/breadcrumb";

export default function TruckPreviewClient() {
    const searchParams = useSearchParams();
    const router = useRouter();
    const truckId = searchParams.get('id');
    const { setCustomLastItem } = useBreadcrumb();

    const [truck, setTruck] = useState<TruckData | null>(null);
    const [assignmentHistory, setAssignmentHistory] = useState<AssignmentData[]>([]);
    const [isLoading, setIsLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);

    const [isViewerOpen, setIsViewerOpen] = useState(false);
    const [viewerIndex, setViewerIndex] = useState(0);
    const [subcontractors, setSubcontractors] = useState<any[]>([]);

    useEffect(() => {
        const fetchTruck = async () => {
            if (!truckId) {
                setError("No truck ID provided");
                setIsLoading(false);
                return;
            }
            try {
                setIsLoading(true);
                setError(null);
                const data = await getTruckByIdClient(truckId);
                if (!data) {
                    setError("Truck not found.");
                    return;
                }
                setTruck(data);
                setCustomLastItem(`Truck ${data.licensePlate}`);

                // Fetch history
                const history = await getTruckAssignmentHistory(truckId);
                setAssignmentHistory(history);
            } catch (err) {
                console.error("Error fetching truck:", err);
                setError(err instanceof Error ? err.message : "Failed to load truck data.");
            } finally {
                setIsLoading(false);
            }
        };

        fetchTruck();

        // Cleanup breadcrumb on unmount
        return () => {
            setCustomLastItem(null);
        };
    }, [truckId, setCustomLastItem]);

    // Fetch subcontractors to resolve names
    useEffect(() => {
        getSubcontractors().then(setSubcontractors);
    }, []);

    const formatDate = (date: Date | string | null | undefined) => {
        if (!date) return "-";
        return new Date(date).toLocaleDateString("th-TH", {
            year: "numeric",
            month: "long",
            day: "numeric",
        });
    };

    const StatusBadge = ({ status }: { status: string }) => {
        const styles = {
            active: "bg-green-100 text-green-800 hover:bg-green-100",
            inactive: "bg-gray-100 text-gray-800 hover:bg-gray-100",
            maintenance: "bg-yellow-100 text-yellow-800 hover:bg-yellow-100",
            "insurance-claim": "bg-red-100 text-red-800 hover:bg-red-100",
            sold: "bg-purple-100 text-purple-800 hover:bg-purple-100",
        };

        const labels = {
            active: "Available",
            inactive: "Inactive",
            maintenance: "Maintenance",
            "insurance-claim": "Insurance Claim",
            sold: "Sold",
            // Fallback
            [status]: status
        };

        const statusKey = status as keyof typeof styles;
        return (
            <Badge className={styles[statusKey] || styles.inactive} variant="outline">
                {labels[statusKey as any] || status}
            </Badge>
        );
    };

    if (isLoading) {
        return (
            <div className="flex items-center justify-center min-h-[400px]">
                <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
                <span className="ml-2 text-muted-foreground">Loading truck data...</span>
            </div>
        );
    }

    if (error || !truck) {
        return (
            <div className="container mx-auto px-4 py-8 max-w-5xl">
                <div className="text-center py-12">
                    <h2 className="text-2xl font-bold text-destructive mb-4">
                        {error || "Truck not found"}
                    </h2>
                    <Button asChild>
                        <Link href="/admin/trucks">Back to Trucks</Link>
                    </Button>
                </div>
            </div>
        );
    }

    // Construct viewable files list
    const viewableFiles = [
        ...(truck.imageFrontRight ? [{ url: truck.imageFrontRight, type: "image" as const, label: "Front-Right View" }] : []),
        ...(truck.imageFrontLeft ? [{ url: truck.imageFrontLeft, type: "image" as const, label: "Front-Left View" }] : []),
        ...(truck.imageBackRight ? [{ url: truck.imageBackRight, type: "image" as const, label: "Back-Right View" }] : []),
        ...(truck.imageBackLeft ? [{ url: truck.imageBackLeft, type: "image" as const, label: "Back-Left View" }] : []),
        ...(truck.documentTax ? [{ url: truck.documentTax, type: "pdf" as const, label: "Tax Document" }] : []),
        ...(truck.documentRegister ? [{ url: truck.documentRegister, type: "pdf" as const, label: "Registration Document" }] : []),
        // Include generic images if any exist (legacy support)
        ...(truck.images || []).map((img, i) => ({ url: img, type: "image" as const, label: `Legacy Image ${i + 1}` })),
        // Include insurance docs
        ...(truck.insuranceDocuments || []).map((doc, i) => ({ url: doc, type: "pdf" as const, label: `Insurance Document ${i + 1}` })),
    ];

    const handleFileClick = (url: string) => {
        const index = viewableFiles.findIndex(f => f.url === url);
        if (index !== -1) {
            setViewerIndex(index);
            setIsViewerOpen(true);
        }
    };

    return (
        <div className="container mx-auto p-6 space-y-6 max-w-[1600px]">
            {/* Header */}
            <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4">
                <div>
                    <div className="flex items-center gap-3 mb-1">
                        <h1 className="text-3xl font-bold tracking-tight">{truck.licensePlate}</h1>
                        <StatusBadge status={truck.truckStatus} />
                    </div>
                    <div className="flex items-center gap-2 text-sm text-muted-foreground">
                        <span className="flex items-center gap-1">
                            <Info className="h-3.5 w-3.5" />
                            Last updated: {truck.updatedAt ? new Date(truck.updatedAt).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : "2 hours ago"}
                        </span>
                        <span>•</span>
                        <span>System ID: {truck.id.substring(0, 8).toUpperCase()}</span>
                    </div>
                </div>
                <div className="flex items-center gap-3">

                    <Button asChild variant="outline" className="gap-2 bg-yellow-600 hover:bg-yellow-700 text-white">
                        <Link href={`/admin/trucks/maintenance?id=${truck.id}`}>
                            <Wrench className="h-4 w-4" />
                            Maintenance
                        </Link>
                    </Button>
                    <Button asChild className="gap-2 bg-blue-600 hover:bg-blue-700 text-white">
                        <Link href={`/admin/trucks/edit?id=${truck.id}`}>
                            <Edit className="h-4 w-4" />
                            Edit Details
                        </Link>
                    </Button>
                </div>
            </div>

            <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
                {/* Left Column - Main Info */}
                <div className="lg:col-span-2 space-y-6">
                    {/* Vehicle Specifications */}
                    <Card>
                        <CardHeader className="pb-3">
                            <CardTitle className="flex items-center gap-2 text-base font-semibold">
                                <Truck className="h-5 w-5 text-blue-600" />
                                Vehicle Specifications
                            </CardTitle>
                        </CardHeader>
                        <CardContent className="grid grid-cols-2 md:grid-cols-4 gap-y-6 gap-x-4">
                            <div>
                                <p className="text-xs font-semibold text-muted-foreground uppercase tracking-wider mb-1">Brand</p>
                                <p className="font-semibold">{truck.brand}</p>
                            </div>
                            <div>
                                <p className="text-xs font-semibold text-muted-foreground uppercase tracking-wider mb-1">Model</p>
                                <p className="font-semibold">{truck.model}</p>
                            </div>
                            <div>
                                <p className="text-xs font-semibold text-muted-foreground uppercase tracking-wider mb-1">Year</p>
                                <p className="font-semibold">{truck.year}</p>
                            </div>
                            <div>
                                <p className="text-xs font-semibold text-muted-foreground uppercase tracking-wider mb-1">Fuel Type</p>
                                <p className="font-semibold">{truck.fuelType || "Diesel"}</p>
                            </div>
                            <div>
                                <p className="text-xs font-semibold text-muted-foreground uppercase tracking-wider mb-1">Max Load</p>
                                <p className="font-semibold">{truck.maxLoadWeight ? `${truck.maxLoadWeight.toLocaleString()} kg` : "-"}</p>
                            </div>
                        </CardContent>
                    </Card>

                    {/* Engine & Registration */}
                    <Card>
                        <CardHeader className="pb-3">
                            <CardTitle className="flex items-center gap-2 text-base font-semibold">
                                <Info className="h-5 w-5 text-blue-600" />
                                Engine & Registration
                            </CardTitle>
                        </CardHeader>
                        <CardContent>
                            <div className="grid grid-cols-1 md:grid-cols-2 gap-y-4 gap-x-12">
                                <div className="flex justify-between py-2 border-b">
                                    <span className="text-sm text-muted-foreground">License Plate Province</span>
                                    <span className="text-sm font-medium">{truck.province}</span>
                                </div>
                                <div className="flex justify-between py-2 border-b">
                                    <span className="text-sm text-muted-foreground">Engine Number</span>
                                    <span className="text-sm font-medium font-mono">{truck.engineNumber}</span>
                                </div>
                                <div className="flex justify-between py-2 border-b">
                                    <span className="text-sm text-muted-foreground">Chassis Number</span>
                                    <span className="text-sm font-medium font-mono">{truck.vin}</span>
                                </div>
                                <div className="flex justify-between py-2 border-b">
                                    <span className="text-sm text-muted-foreground">Engine Capacity</span>
                                    <span className="text-sm font-medium">{truck.engineCapacity ? `${truck.engineCapacity.toLocaleString()} cc` : "-"}</span>
                                </div>
                                <div className="flex justify-between py-2 border-b">
                                    <span className="text-sm text-muted-foreground">Fuel Capacity</span>
                                    <span className="text-sm font-medium">{truck.fuelCapacity ? `${truck.fuelCapacity.toLocaleString()} L` : "-"}</span>
                                </div>
                                <div className="flex justify-between py-2 border-b">
                                    <span className="text-sm text-muted-foreground">Registration Date</span>
                                    <span className="text-sm font-medium">{formatDate(truck.registrationDate)}</span>
                                </div>
                                <div className="flex justify-between py-2 border-b">
                                    <span className="text-sm text-muted-foreground">Buying Date</span>
                                    <span className="text-sm font-medium">{formatDate(truck.buyingDate)}</span>
                                </div>
                                <div className="flex justify-between py-2 border-b items-center">
                                    <span className="text-sm text-muted-foreground">Tax (Act) Expiry</span>
                                    <span className="text-sm font-medium">{formatDate(truck.taxExpiryDate)}</span>
                                </div>
                                <div className="flex justify-between py-2 border-b items-center">
                                    <span className="text-sm text-muted-foreground">Tax Renewal Status</span>
                                    {(() => {
                                        const status = truck.taxRenewalStatus || 'pending';
                                        if (status === 'completed') {
                                            return <Badge className="bg-green-100 text-green-700 border-green-200 hover:bg-green-100">Completed</Badge>;
                                        }
                                        if (status === 'in_progress') {
                                            return <Badge className="bg-blue-100 text-blue-700 border-blue-200 hover:bg-blue-100">In Progress</Badge>;
                                        }
                                        return <Badge variant="outline" className="capitalize">Pending</Badge>;
                                    })()}
                                </div>
                            </div>
                        </CardContent>
                    </Card>

                    {/* Insurance & Renewal Details */}
                    <Card>
                        <CardHeader className="pb-3">
                            <CardTitle className="flex items-center gap-2 text-base font-semibold">
                                <Shield className="h-5 w-5 text-blue-600" />
                                Insurance & Renewal Details
                            </CardTitle>
                        </CardHeader>
                        <CardContent>
                            <div className="grid grid-cols-1 md:grid-cols-2 gap-y-4 gap-x-12">
                                <div className="flex justify-between py-2 border-b">
                                    <span className="text-sm text-muted-foreground">Insurance Provider</span>
                                    <span className="text-sm font-medium">{truck.insuranceCompany || "-"}</span>
                                </div>
                                <div className="flex justify-between py-2 border-b">
                                    <span className="text-sm text-muted-foreground">Policy Number</span>
                                    <span className="text-sm font-medium">{truck.insurancePolicyNumber || "-"}</span>
                                </div>
                                <div className="flex justify-between py-2 border-b">
                                    <span className="text-sm text-muted-foreground">Type</span>
                                    <span className="text-sm font-medium">{truck.insuranceType ? `Type ${truck.insuranceType}` : "-"}</span>
                                </div>
                                <div className="flex justify-between py-2 border-b">
                                    <span className="text-sm text-muted-foreground">Premium</span>
                                    <span className="text-sm font-medium">{truck.insurancePremium ? `${truck.insurancePremium.toLocaleString()} THB` : "-"}</span>
                                </div>
                                <div className="flex justify-between py-2 border-b">
                                    <span className="text-sm text-muted-foreground">Start Date</span>
                                    <span className="text-sm font-medium">{formatDate(truck.insuranceStartDate)}</span>
                                </div>
                                <div className="flex justify-between py-2 border-b items-center">
                                    <span className="text-sm text-muted-foreground">End Date</span>
                                    <span className="text-sm font-medium">{formatDate(truck.insuranceExpiryDate)}</span>
                                </div>
                                <div className="flex justify-between py-2 border-b items-center">
                                    <span className="text-sm text-muted-foreground">Renewal Status</span>
                                    {(() => {
                                        const status = truck.insuranceRenewalStatus || 'pending';
                                        if (status === 'completed') {
                                            return <Badge className="bg-green-100 text-green-700 border-green-200 hover:bg-green-100">Completed</Badge>;
                                        }
                                        if (status === 'in_progress') {
                                            return <Badge className="bg-purple-100 text-purple-700 border-purple-200 hover:bg-purple-100">In Progress</Badge>;
                                        }
                                        return <Badge variant="outline" className="capitalize">Pending</Badge>;
                                    })()}
                                </div>
                            </div>
                        </CardContent>
                    </Card>

                    {/* Ownership Info */}
                    <Card>
                        <CardHeader className="pb-3">
                            <CardTitle className="flex items-center gap-2 text-base font-semibold">
                                <Shield className="h-5 w-5 text-blue-600" />
                                Ownership Info
                            </CardTitle>
                        </CardHeader>
                        <CardContent>
                            <div className="bg-muted/30 rounded-lg p-4 border flex items-start gap-4">
                                <div className="h-10 w-10 rounded-full bg-blue-100 flex items-center justify-center flex-shrink-0">
                                    <Truck className="h-5 w-5 text-blue-600" />
                                </div>
                                <div>
                                    <h4 className="text-sm font-semibold flex items-center gap-2">
                                        {truck.ownershipType === "subcontractor" ? "Subcontractor Fleet" : "Own Fleet"}
                                        {truck.ownershipType === "subcontractor" && (
                                            <Badge variant="secondary" className="bg-blue-100 text-blue-700 text-[10px] h-5 px-1.5 rounded-sm">PARTNER</Badge>
                                        )}
                                    </h4>
                                    <p className="text-sm text-muted-foreground mt-1">
                                        {truck.ownershipType === "subcontractor" && truck.subcontractorId
                                            ? `Managed by ${subcontractors.find(s => s.id === truck.subcontractorId)?.name || truck.subcontractorId}`
                                            : "Managed by Internal Logistics Team"}
                                    </p>
                                    <Button variant="link" className="p-0 h-auto text-blue-600 mt-2 text-xs font-medium flex items-center gap-1">
                                        View Partner Profile <ArrowLeft className="h-3 w-3 rotate-180" />
                                    </Button>
                                </div>
                            </div>
                        </CardContent>
                    </Card>
                </div>

                {/* Right Column - Sidebar */}
                <div className="space-y-6">
                    {/* Current Assignment */}
                    <Card>
                        <CardHeader className="pb-3 border-b">
                            <CardTitle className="flex items-center gap-2 text-base font-semibold">
                                <MapPin className="h-5 w-5 text-blue-600" />
                                Current Assignment
                            </CardTitle>
                        </CardHeader>
                        <CardContent className="pt-6">
                            {(() => {
                                const uniqueAssignments = truck.currentAssignments
                                    ? Array.from(new Map(truck.currentAssignments.map(item => [item.driverId, item])).values())
                                    : [];

                                return uniqueAssignments.length > 0 ? (
                                    <div className="space-y-4">
                                        {uniqueAssignments.map((assignment, index) => (
                                            <div key={assignment.assignmentId} className="flex flex-col items-center text-center border-b last:border-0 pb-4 last:pb-0">
                                                {/* Show Avatar only for the first one or smaller for list? Let's keep big for now if few, or adjust.
                                                If multiple, maybe a more compact list view is better. 
                                                Let's do a compact list if > 1, else keep the big card view for single. 
                                            */}

                                                {// If only 1 driver, show the big detailed view
                                                    uniqueAssignments.length === 1 ? (
                                                        <>
                                                            <div className="relative mb-3">
                                                                <Avatar className="h-20 w-20 border-4 border-background shadow-sm">
                                                                    <AvatarFallback className="bg-blue-100 text-blue-600 font-bold text-xl">
                                                                        {(assignment.driverName || 'DR').substring(0, 2).toUpperCase()}
                                                                    </AvatarFallback>
                                                                </Avatar>
                                                                <span className="absolute bottom-1 right-1 h-4 w-4 rounded-full bg-green-500 border-2 border-background"></span>
                                                            </div>
                                                            <h3 className="text-lg font-bold">{assignment.driverName}</h3>
                                                            <p className="text-xs text-muted-foreground font-medium mb-4">
                                                                Assigned since {assignment.assignedAt ? new Date(assignment.assignedAt).toLocaleDateString() : '-'}
                                                            </p>

                                                            <div className="grid grid-cols-2 gap-4 w-full mb-6">
                                                                <div className="bg-muted/50 rounded p-2 text-center">
                                                                    <p className="text-[10px] uppercase text-muted-foreground font-bold tracking-wider mb-0.5">Status</p>
                                                                    <p className="text-xs font-semibold text-green-600">On Duty</p>
                                                                </div>
                                                                <div className="bg-muted/50 rounded p-2 text-center">
                                                                    <p className="text-[10px] uppercase text-muted-foreground font-bold tracking-wider mb-0.5">Action</p>
                                                                    <p className="text-xs font-semibold">Active</p>
                                                                </div>
                                                            </div>

                                                            <div className="grid grid-cols-2 gap-3 w-full">
                                                                <Button variant="outline" className="w-full text-xs h-9" asChild>
                                                                    <Link href={`/admin/drivers/view?id=${assignment.driverId}`}>
                                                                        View Profile
                                                                    </Link>
                                                                </Button>
                                                                <Button className="w-full text-xs h-9 bg-red-50 text-red-600 hover:bg-red-100 border-red-100 shadow-none" asChild>
                                                                    <Link href="/admin/truck-assignment">
                                                                        Manage
                                                                    </Link>
                                                                </Button>
                                                            </div>
                                                        </>
                                                    ) : (
                                                        // Multiple Drivers View - Compact
                                                        <div className="w-full flex items-center justify-between p-2 rounded-lg bg-muted/30">
                                                            <div className="flex items-center gap-3">
                                                                <Avatar className="h-10 w-10 border-2 border-background shadow-sm">
                                                                    <AvatarFallback className="bg-blue-100 text-blue-600 font-bold text-sm">
                                                                        {(assignment.driverName || 'DR').substring(0, 2).toUpperCase()}
                                                                    </AvatarFallback>
                                                                </Avatar>
                                                                <div className="text-left">
                                                                    <p className="font-semibold text-sm">{assignment.driverName}</p>
                                                                    <p className="text-[10px] text-muted-foreground">Original: {assignment.assignedAt ? new Date(assignment.assignedAt).toLocaleDateString() : '-'}</p>
                                                                </div>
                                                            </div>
                                                            <Button variant="ghost" size="sm" asChild className="h-8 w-8 p-0">
                                                                <Link href={`/admin/drivers/view?id=${assignment.driverId}`}>
                                                                    <User className="h-4 w-4 text-muted-foreground" />
                                                                </Link>
                                                            </Button>
                                                        </div>
                                                    )}
                                            </div>
                                        ))}

                                        {uniqueAssignments.length > 1 && (
                                            <div className="pt-2">
                                                <Button className="w-full text-xs h-9 bg-blue-600 hover:bg-blue-700 text-white" asChild>
                                                    <Link href="/admin/truck-assignment">
                                                        Manage Assignments
                                                    </Link>
                                                </Button>
                                            </div>
                                        )}
                                    </div>
                                ) : (
                                    <div className="flex flex-col items-center justify-center text-center">
                                        <div className="h-12 w-12 rounded-full bg-muted flex items-center justify-center mb-3">
                                            <User className="h-6 w-6 text-muted-foreground" />
                                        </div>
                                        <p className="text-sm font-medium mb-1">No Driver Assigned</p>
                                        <p className="text-xs text-muted-foreground mb-4">Assign a driver to this truck to track performance.</p>
                                        <Button className="w-full gap-2 bg-blue-600 hover:bg-blue-700 text-white" asChild>
                                            <Link href="/admin/truck-assignment">
                                                <Plus className="h-4 w-4" />
                                                Assign Driver
                                            </Link>
                                        </Button>
                                    </div>
                                );
                            })()}
                        </CardContent>
                    </Card>

                    {/* Vehicle Photos */}
                    <Card>
                        <CardHeader className="pb-3 border-b flex flex-row items-center justify-between">
                            <CardTitle className="flex items-center gap-2 text-base font-semibold">
                                <Camera className="h-5 w-5 text-blue-600" />
                                Vehicle Photos
                            </CardTitle>
                            <span className="text-xs font-medium text-muted-foreground">{viewableFiles.filter(f => f.type === 'image').length} PHOTOS</span>
                        </CardHeader>
                        <CardContent className="pt-4">
                            <div className="grid grid-cols-2 gap-2">
                                {viewableFiles.filter(f => f.type === 'image').map((file, i) => (
                                    <div
                                        key={i}
                                        className="relative aspect-video rounded-md overflow-hidden bg-muted border cursor-pointer"
                                        onClick={() => handleFileClick(file.url)}
                                    >
                                        <Image src={file.url} alt={file.label} fill className="object-cover hover:scale-105 transition-transform duration-300" />
                                    </div>
                                ))}
                            </div>
                        </CardContent>
                    </Card>

                    {/* TAX Document */}
                    <Card>
                        <CardHeader className="pb-3 border-b">
                            <CardTitle className="flex items-center gap-2 text-base font-semibold">
                                <FileText className="h-5 w-5 text-blue-600" />
                                TAX Document
                            </CardTitle>
                        </CardHeader>
                        <CardContent className="pt-4">
                            {truck.documentTax ? (
                                <div
                                    className="relative aspect-[4/3] rounded-md overflow-hidden bg-muted cursor-pointer"
                                    onClick={() => handleFileClick(truck.documentTax!)}
                                >
                                    {truck.documentTax.toLowerCase().endsWith('.pdf') ? (
                                        <div className="flex flex-col items-center justify-center h-full bg-muted">
                                            <FileText className="h-12 w-12 text-blue-600 mb-2" />
                                            <span className="text-sm font-medium text-blue-600">View Tax Document</span>
                                        </div>
                                    ) : (
                                        <Image
                                            src={truck.documentTax}
                                            alt="Tax Document"
                                            fill
                                            className="object-cover"
                                        />
                                    )}
                                </div>
                            ) : (
                                <div className="flex items-center justify-center aspect-[4/3] rounded-md bg-muted/50">
                                    <p className="text-sm text-red-500 font-medium">TAX image or file for view</p>
                                </div>
                            )}
                        </CardContent>
                    </Card>

                    {/* Insurance Document */}
                    <Card>
                        <CardHeader className="pb-3 border-b">
                            <CardTitle className="flex items-center gap-2 text-base font-semibold">
                                <Shield className="h-5 w-5 text-blue-600" />
                                Insurance Document
                            </CardTitle>
                        </CardHeader>
                        <CardContent className="pt-4">
                            {(truck.insuranceDocuments && truck.insuranceDocuments.length > 0) ? (
                                <div
                                    className="relative aspect-[4/3] rounded-md overflow-hidden bg-muted cursor-pointer"
                                    onClick={() => handleFileClick(truck.insuranceDocuments![0])}
                                >
                                    {truck.insuranceDocuments[0].toLowerCase().endsWith('.pdf') ? (
                                        <div className="flex flex-col items-center justify-center h-full bg-muted">
                                            <FileText className="h-12 w-12 text-blue-600 mb-2" />
                                            <span className="text-sm font-medium text-blue-600">View Insurance Document</span>
                                        </div>
                                    ) : (
                                        <Image
                                            src={truck.insuranceDocuments[0]}
                                            alt="Insurance Document"
                                            fill
                                            className="object-cover"
                                        />
                                    )}
                                </div>
                            ) : (
                                <div className="flex items-center justify-center aspect-[4/3] rounded-md bg-muted/50">
                                    <p className="text-sm text-red-500 font-medium">Insurance image or file for view</p>
                                </div>
                            )}
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
