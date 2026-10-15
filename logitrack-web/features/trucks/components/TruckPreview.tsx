"use client";

import Link from "next/link";
import { useMe } from "@/features/auth/api/useMe";
import { routeAllowed } from "@/lib/routeCapabilities";
import { truckStatusTone, toneBadgeVariant } from "@/lib/statusPresentation";
import {
    ArrowLeft, Edit, Truck, User, FileText, Info, Loader2, Camera,
    MapPin, Shield, Plus,
    Wrench
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import Image from "next/image";
import { TruckAssignmentHistory } from "./TruckAssignmentHistory";
import { truckFuelLabel, truckTypeLabel } from "../utils/labels";
import { FileViewer } from "@/components/ui/file-viewer";
import { formatLicensePlate, isPdfUrl } from "@/lib/utils";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { useTruckPreview } from "../hooks/useTruckPreview";
import { PageHeader } from "@/components/page-header";
import { RecordState } from "@/components/record-state";
import { useLanguage } from "@/context/language";

function StatusBadge({ status }: { status: string }) {
    const { t } = useLanguage();
    const labels: Record<string, string> = {
        active: t("trucks.detail.status.available"),
        inactive: t("trucks.detail.status.inactive"),
        maintenance: t("trucks.detail.status.maintenance"),
        "insurance-claim": t("trucks.detail.status.insuranceClaim"),
        insurance_claim: t("trucks.detail.status.insuranceClaim"),
        sold: t("trucks.detail.status.sold"),
    };
    return (
        <Badge variant={toneBadgeVariant(truckStatusTone(status))}>
            {labels[status] || status}
        </Badge>
    );
}

export default function TruckPreview() {
    const { data: me } = useMe();
    const {
        truck,
        isLoading,
        missing,
        loadError,
        retry,
        retrying,
        isViewerOpen,
        setIsViewerOpen,
        viewerIndex,
        partnerName,
        documentTax,
        insuranceDocuments,
        viewableFiles,
        handleFileClick,
        formatDate,
        t,
    } = useTruckPreview();


    if (isLoading) {
        return (
            <div className="flex items-center justify-center min-h-[400px]">
                <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
                <span className="ml-2 text-muted-foreground">{t("trucks.detail.loading")}</span>
            </div>
        );
    }

    if (missing || !truck) {
        return <RecordState error={loadError} onRetry={retry} retrying={retrying} />;
    }

    return (
        <div className="flex flex-col gap-section">
            <PageHeader
                title={t("nav.trucks")}
                recordName={formatLicensePlate(truck.licensePlate)}
                back={{ href: "/app/trucks" }}
                status={<StatusBadge status={String(truck.status)} />}
                description={
                    <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
                        <span className="flex items-center gap-1">
                            <Info className="h-3.5 w-3.5" aria-hidden />
                            {t("trucks.detail.lastUpdated")}: {truck.updatedAt ? new Date(truck.updatedAt).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : "-"}
                        </span>
                        <span aria-hidden>•</span>
                        <span>{t("masterData.trucks.systemId")}: <span className="font-mono">{truck.id.substring(0, 8).toUpperCase()}</span></span>
                    </span>
                }
                actions={
                    <>
                        <Button asChild variant="outline">
                            <Link href={`/app/trucks/maintenance?id=${truck.id}`} prefetch={false}>
                                <Wrench className="h-4 w-4" aria-hidden />
                                {t("trucks.detail.maintenance")}
                            </Link>
                        </Button>
                        <Button asChild>
                            <Link href={`/app/trucks/edit?id=${truck.id}`} prefetch={false}>
                                <Edit className="h-4 w-4" aria-hidden />
                                {t("trucks.detail.editDetails")}
                            </Link>
                        </Button>
                    </>
                }
            />

            <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
                {/* Left Column - Main Info */}
                <div className="lg:col-span-2 space-y-6">
                    {/* Vehicle Specifications */}
                    <Card>
                        <CardHeader className="pb-3">
                            <CardTitle className="flex items-center gap-2 text-base font-semibold">
                                <Truck className="h-5 w-5 text-primary" />
                                {t("trucks.detail.vehicleSpecs")}
                            </CardTitle>
                        </CardHeader>
                        <CardContent className="grid grid-cols-2 md:grid-cols-4 gap-y-6 gap-x-4">
                            <div>
                                <p className="text-xs font-semibold text-muted-foreground mb-1">{t("trucks.detail.brand")}</p>
                                <p className="font-semibold">{truck.brand}</p>
                            </div>
                            <div>
                                <p className="text-xs font-semibold text-muted-foreground mb-1">{t("trucks.detail.model")}</p>
                                <p className="font-semibold">{truck.model}</p>
                            </div>
                            <div>
                                <p className="text-xs font-semibold text-muted-foreground mb-1">{t("trucks.detail.year")}</p>
                                <p className="font-semibold">{truck.year}</p>
                            </div>
                            <div>
                                <p className="text-xs font-semibold text-muted-foreground mb-1">{t("trucks.detail.fuelType")}</p>
                                <p className="font-semibold">{truckFuelLabel(truck.fuelType, t)}</p>
                            </div>
                            <div>
                                <p className="text-xs font-semibold text-muted-foreground mb-1">{t("trucks.detail.maxLoad")}</p>
                                <p className="font-semibold">{truck.maxLoadWeightKg ? t("trucks.ui4.kg", { value: truck.maxLoadWeightKg.toLocaleString() }) : "-"}</p>
                            </div>
                            <div>
                                <p className="text-xs font-semibold text-muted-foreground mb-1">{t("trucks.section.truckType")}</p>
                                <p className="font-semibold">
                                    {(() => {
                                        const typeMap: Record<string, string> = {
                                            "Pickup": "trucks.type.pickup",
                                            "4 Wheels": "trucks.type.4wheels",
                                            "4 Wheels Jumbo": "trucks.type.4wheels",
                                            "6 Wheels": "trucks.type.6wheels",
                                            "10 Wheels": "trucks.type.10wheels",
                                            "18 Wheels": "trucks.type.18wheels",
                                            "Van": "trucks.type.van",
                                        };
                                        return typeMap[truck.type] ? t(typeMap[truck.type]) : truck.type;
                                    })()}
                                </p>
                            </div>
                        </CardContent>
                    </Card>

                    {/* Engine & Registration */}
                    <Card>
                        <CardHeader className="pb-3">
                            <CardTitle className="flex items-center gap-2 text-base font-semibold">
                                <Info className="h-5 w-5 text-primary" />
                                {t("trucks.detail.engineReg")}
                            </CardTitle>
                        </CardHeader>
                        <CardContent>
                            <div className="grid grid-cols-1 md:grid-cols-2 gap-y-4 gap-x-12">
                                <div className="flex justify-between py-2 border-b">
                                    <span className="text-sm text-muted-foreground">{t("trucks.detail.province")}</span>
                                    <span className="text-sm font-medium">{truck.province}</span>
                                </div>
                                <div className="flex justify-between py-2 border-b">
                                    <span className="text-sm text-muted-foreground">{t("trucks.detail.engineNo")}</span>
                                    <span className="text-sm font-medium font-mono">{truck.engineNumber}</span>
                                </div>
                                <div className="flex justify-between py-2 border-b">
                                    <span className="text-sm text-muted-foreground">{t("trucks.detail.chassisNo")}</span>
                                    <span className="text-sm font-medium font-mono">{truck.vin}</span>
                                </div>
                                <div className="flex justify-between py-2 border-b">
                                    <span className="text-sm text-muted-foreground">{t("trucks.detail.engineCap")}</span>
                                    <span className="text-sm font-medium">{truck.engineCapacity ? t("trucks.ui4.cc", { value: truck.engineCapacity.toLocaleString() }) : "-"}</span>
                                </div>
                                <div className="flex justify-between py-2 border-b">
                                    <span className="text-sm text-muted-foreground">{t("trucks.detail.fuelCap")}</span>
                                    <span className="text-sm font-medium">{truck.fuelCapacity ? t("trucks.ui4.liters", { value: truck.fuelCapacity.toLocaleString() }) : "-"}</span>
                                </div>
                                <div className="flex justify-between py-2 border-b">
                                    <span className="text-sm text-muted-foreground">{t("trucks.detail.regDate")}</span>
                                    <span className="text-sm font-medium">{formatDate(truck.registrationDate)}</span>
                                </div>
                                <div className="flex justify-between py-2 border-b">
                                    <span className="text-sm text-muted-foreground">{t("trucks.detail.buyingDate")}</span>
                                    <span className="text-sm font-medium">{formatDate(truck.buyingDate)}</span>
                                </div>
                                <div className="flex justify-between py-2 border-b items-center">
                                    <span className="text-sm text-muted-foreground">{t("trucks.detail.taxExpiry")}</span>
                                    <span className="text-sm font-medium">{formatDate(truck.taxExpiryDate)}</span>
                                </div>
                                <div className="flex justify-between py-2 border-b items-center">
                                    <span className="text-sm text-muted-foreground">{t("trucks.detail.taxStatus")}</span>
                                    {(() => {
                                        const status = truck.taxRenewalStatus || 'pending';
                                        if (status === 'completed') {
                                            return <Badge variant="success">{t("trucks.detail.status.completed")}</Badge>;
                                        }
                                        if (status === 'in_progress') {
                                            return <Badge>{t("trucks.detail.status.inProgress")}</Badge>;
                                        }
                                        return <Badge variant="secondary">{t("trucks.detail.status.pending")}</Badge>;
                                    })()}
                                </div>
                            </div>
                        </CardContent>
                    </Card>

                    {/* Insurance & Renewal Details */}
                    <Card>
                        <CardHeader className="pb-3">
                            <CardTitle className="flex items-center gap-2 text-base font-semibold">
                                <Shield className="h-5 w-5 text-primary" />
                                {t("trucks.detail.insuranceDetails")}
                            </CardTitle>
                        </CardHeader>
                        <CardContent>
                            <div className="grid grid-cols-1 md:grid-cols-2 gap-y-4 gap-x-12">
                                <div className="flex justify-between py-2 border-b">
                                    <span className="text-sm text-muted-foreground">{t("trucks.detail.insuranceProvider")}</span>
                                    <span className="text-sm font-medium">{truck.insuranceCompany || "-"}</span>
                                </div>
                                <div className="flex justify-between py-2 border-b">
                                    <span className="text-sm text-muted-foreground">{t("trucks.detail.policyNo")}</span>
                                    <span className="text-sm font-medium">{truck.insurancePolicyNumber || "-"}</span>
                                </div>
                                <div className="flex justify-between py-2 border-b">
                                    <span className="text-sm text-muted-foreground">{t("trucks.detail.insuranceType")}</span>
                                    <span className="text-sm font-medium">{truck.insuranceType ? t(`trucks.insurance.type.${truck.insuranceType.replace("+", "_plus")}`) : "-"}</span>
                                </div>
                                <div className="flex justify-between py-2 border-b">
                                    <span className="text-sm text-muted-foreground">{t("trucks.detail.premium")}</span>
                                    <span className="text-sm font-medium">{truck.insurancePremiumThb ? t("trucks.ui4.thb", { value: truck.insurancePremiumThb.toLocaleString() }) : "-"}</span>
                                </div>
                                <div className="flex justify-between py-2 border-b">
                                    <span className="text-sm text-muted-foreground">{t("trucks.detail.startDate")}</span>
                                    <span className="text-sm font-medium">{formatDate(truck.insuranceStartDate)}</span>
                                </div>
                                <div className="flex justify-between py-2 border-b items-center">
                                    <span className="text-sm text-muted-foreground">{t("trucks.detail.endDate")}</span>
                                    <span className="text-sm font-medium">{formatDate(truck.insuranceExpiryDate)}</span>
                                </div>
                                <div className="flex justify-between py-2 border-b items-center">
                                    <span className="text-sm text-muted-foreground">{t("trucks.detail.renewalStatus")}</span>
                                    {(() => {
                                        const status = truck.insuranceRenewalStatus || 'pending';
                                        if (status === 'completed') {
                                            return <Badge variant="success">{t("trucks.detail.status.completed")}</Badge>;
                                        }
                                        if (status === 'in_progress') {
                                            return <Badge>{t("trucks.detail.status.inProgress")}</Badge>;
                                        }
                                        return <Badge variant="secondary">{t("trucks.detail.status.pending")}</Badge>;
                                    })()}
                                </div>
                            </div>
                        </CardContent>
                    </Card>

                    {/* Ownership Info */}
                    <Card>
                        <CardHeader className="pb-3">
                            <CardTitle className="flex items-center gap-2 text-base font-semibold">
                                <Shield className="h-5 w-5 text-primary" />
                                {t("trucks.detail.ownershipInfo")}
                            </CardTitle>
                        </CardHeader>
                        <CardContent>
                            <div className="bg-muted/30 rounded-lg p-4 border flex items-start gap-4">
                                <div className="h-10 w-10 rounded-full bg-primary/10 flex items-center justify-center flex-shrink-0">
                                    <Truck className="h-5 w-5 text-primary" />
                                </div>
                                <div>
                                    <h4 className="text-sm font-semibold flex items-center gap-2">
                                        {truck.ownershipType === "subcontractor" ? t("trucks.detail.subcontractorFleet") : t("trucks.detail.ownFleet")}
                                        {truck.ownershipType === "subcontractor" && (
                                            <Badge variant="secondary">{t("trucks.detail.partner")}</Badge>
                                        )}
                                    </h4>
                                    <p className="text-sm text-muted-foreground mt-1">
                                        {truck.ownershipType === "subcontractor" && partnerName
                                            ? `${t("trucks.detail.managedByPartner")} ${partnerName}`
                                            : t("trucks.detail.managedByInternal")}
                                    </p>
                                    {truck.ownershipType === "subcontractor" && truck.tenantId && routeAllowed(me?.capabilities ?? [], `/app/subcontractors/${truck.tenantId}`) && (
                                        <Button asChild variant="link" className="p-0 h-auto mt-2 text-xs max-md:min-h-11 max-md:min-w-11">
                                            <Link href={`/app/subcontractors/${truck.tenantId}`} prefetch={false}>
                                                {t("trucks.detail.viewPartner")} <ArrowLeft className="h-3 w-3 rotate-180" aria-hidden />
                                            </Link>
                                        </Button>
                                    )}
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
                                <MapPin className="h-5 w-5 text-primary" />
                                {t("trucks.detail.currentAssignment")}
                            </CardTitle>
                        </CardHeader>
                        <CardContent className="pt-6">
                            {(() => {
                                const uniqueAssignments = truck.currentAssignments
                                    ? Array.from(new Map(truck.currentAssignments.map((item: any) => [item.driverId, item])).values())
                                    : [];

                                return uniqueAssignments.length > 0 ? (
                                    <div className="space-y-4">
                                        {uniqueAssignments.map((assignment: any) => (
                                            <div key={assignment.id} className="flex flex-col items-center text-center border-b last:border-0 pb-4 last:pb-0">
                                                {uniqueAssignments.length === 1 ? (
                                                    <>
                                                        <div className="relative mb-3">
                                                            <Avatar className="h-20 w-20 border-4 border-background shadow-sm">
                                                                <AvatarFallback className="bg-primary/10 text-primary font-bold text-xl">
                                                                    {assignment.driverName?.trim().substring(0, 2).toUpperCase() || "-"}
                                                                </AvatarFallback>
                                                            </Avatar>
                                                            <span className="absolute bottom-1 right-1 h-4 w-4 rounded-full bg-muted border-2 border-background"></span>
                                                        </div>
                                                        <h3 className="text-lg font-bold">{assignment.driverName}</h3>
                                                        <p className="text-xs text-muted-foreground font-medium mb-4">
                                                            {t("trucks.detail.assignedSince")} {assignment.assignedAt ? new Date(assignment.assignedAt).toLocaleDateString() : '-'}
                                                        </p>

                                                        <div className="grid grid-cols-2 gap-4 w-full mb-6">
                                                            <div className="bg-muted/50 rounded p-2 text-center">
                                                                <p className="text-xs text-muted-foreground font-bold mb-0.5">{t("trucks.ui4.status")}</p>
                                                                <p className="text-xs font-semibold text-success">{t("trucks.detail.onDuty")}</p>
                                                            </div>
                                                            <div className="bg-muted/50 rounded p-2 text-center">
                                                                <p className="text-xs text-muted-foreground font-bold mb-0.5">{t("trucks.detail.action")}</p>
                                                                <p className="text-xs font-semibold">{t("trucks.detail.active")}</p>
                                                            </div>
                                                        </div>

                                                        <div className="grid grid-cols-2 gap-3 w-full">
                                                            <Button variant="outline" className="w-full text-xs h-9" asChild>
                                                                <Link href={`/app/drivers/view?id=${assignment.driverId}`} prefetch={false}>
                                                                    {t("trucks.detail.viewProfile")}
                                                                </Link>
                                                            </Button>
                                                            <Button className="w-full text-xs h-9" asChild>
                                                                <Link href="/app/truck-assignment" prefetch={false}>
                                                                    {t("trucks.detail.manage")}
                                                                </Link>
                                                            </Button>
                                                        </div>
                                                    </>
                                                ) : (
                                                    <div className="w-full flex items-center justify-between p-2 rounded-lg bg-muted/30">
                                                        <div className="flex items-center gap-3">
                                                            <Avatar className="h-10 w-10 border-2 border-background shadow-sm">
                                                                <AvatarFallback className="bg-primary/10 text-primary font-bold text-sm">
                                                                    {assignment.driverName?.trim().substring(0, 2).toUpperCase() || "-"}
                                                                </AvatarFallback>
                                                            </Avatar>
                                                            <div className="text-left">
                                                                <p className="font-semibold text-sm">{assignment.driverName}</p>
                                                                <p className="text-xs text-muted-foreground">{t("trucks.detail.assignedSince")}: {assignment.assignedAt ? new Date(assignment.assignedAt).toLocaleDateString() : '-'}</p>
                                                            </div>
                                                        </div>
                                                        <Button variant="ghost" size="sm" asChild className="h-8 w-8 p-0">
                                                            <Link href={`/app/drivers/view?id=${assignment.driverId}`} prefetch={false} aria-label={assignment.driverName || t("nav.drivers")}>
                                                                <User className="h-4 w-4 text-muted-foreground" aria-hidden />
                                                            </Link>
                                                        </Button>
                                                    </div>
                                                )}
                                            </div>
                                        ))}

                                        {uniqueAssignments.length > 1 && (
                                            <div className="pt-2">
                                                <Button className="w-full text-xs h-9" asChild>
                                                    <Link href="/app/truck-assignment" prefetch={false}>
                                                        {t("trucks.detail.manageAssignments")}
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
                                        <p className="text-sm font-medium mb-1">{t("trucks.detail.noDriver")}</p>
                                        <p className="text-xs text-muted-foreground mb-4">{t("trucks.detail.assignDriverDesc")}</p>
                                        <Button className="w-full gap-2" asChild>
                                            <Link href="/app/truck-assignment" prefetch={false}>
                                                <Plus className="h-4 w-4" />
                                                {t("trucks.detail.assignDriver")}
                                            </Link>
                                        </Button>
                                    </div>
                                );
                            })()}
                        </CardContent>
                    </Card>

                    <TruckAssignmentHistory truckId={truck.id} />
                    {/* Vehicle Photos */}
                    <Card>
                        <CardHeader className="pb-3 border-b flex flex-row items-center justify-between">
                            <CardTitle className="flex items-center gap-2 text-base font-semibold">
                                <Camera className="h-5 w-5 text-primary" />
                                {t("trucks.detail.photos")}
                            </CardTitle>
                            <span className="text-xs font-medium text-muted-foreground">{viewableFiles.filter((file) => file.isPhoto).length} {t("trucks.detail.photosCount")}</span>
                        </CardHeader>
                        <CardContent className="pt-4">
                            <div className="grid grid-cols-2 gap-2">
                                {viewableFiles.filter((file) => file.isPhoto).map((file: any, i: number) => (
                                    <div
                                        key={i}
                                        className="relative aspect-video rounded-md overflow-hidden bg-muted border cursor-pointer"
                                        onClick={() => handleFileClick(file.url)}
                                    >
                                        <Image src={file.url} alt={file.label} fill className="object-cover transition-transform duration-300" />
                                    </div>
                                ))}
                            </div>
                        </CardContent>
                    </Card>

                    {/* TAX Document */}
                    <Card>
                        <CardHeader className="pb-3 border-b">
                            <CardTitle className="flex items-center gap-2 text-base font-semibold">
                                <FileText className="h-5 w-5 text-primary" />
                                {t("trucks.detail.taxDoc")}
                            </CardTitle>
                        </CardHeader>
                        <CardContent className="pt-4">
                            {documentTax ? (
                                <div
                                    className="relative aspect-[4/3] rounded-md overflow-hidden bg-muted cursor-pointer"
                                    onClick={() => handleFileClick(documentTax)}
                                >
                                    {isPdfUrl(documentTax) ? (
                                        <div className="flex flex-col items-center justify-center h-full bg-muted">
                                            <FileText className="h-12 w-12 text-primary mb-2" />
                                            <span className="text-sm font-medium text-primary">{t("trucks.detail.viewTaxDoc")}</span>
                                        </div>
                                    ) : (
                                        <Image
                                            src={documentTax}
                                            alt={t("trucks.documents.tax")}
                                            fill
                                            className="object-cover"
                                        />
                                    )}
                                </div>
                            ) : (
                                <div className="flex items-center justify-center aspect-[4/3] rounded-md bg-muted/50">
                                    <p className="text-sm text-destructive font-medium">{t("masterData.trucks.noTaxDoc")}</p>
                                </div>
                            )}
                        </CardContent>
                    </Card>

                    {/* Insurance Document */}
                    <Card>
                        <CardHeader className="pb-3 border-b">
                            <CardTitle className="flex items-center gap-2 text-base font-semibold">
                                <Shield className="h-5 w-5 text-primary" />
                                {t("trucks.detail.insuranceDoc")}
                            </CardTitle>
                        </CardHeader>
                        <CardContent className="pt-4">
                            {insuranceDocuments.length > 0 ? (
                                <div
                                    className="relative aspect-[4/3] rounded-md overflow-hidden bg-muted cursor-pointer"
                                    onClick={() => handleFileClick(insuranceDocuments[0])}
                                >
                                    {isPdfUrl(insuranceDocuments[0]) ? (
                                        <div className="flex flex-col items-center justify-center h-full bg-muted">
                                            <FileText className="h-12 w-12 text-primary mb-2" />
                                            <span className="text-sm font-medium text-primary">{t("trucks.detail.viewInsuranceDoc")}</span>
                                        </div>
                                    ) : (
                                        <Image
                                            src={insuranceDocuments[0]}
                                            alt={t("trucks.detail.insuranceDoc")}
                                            fill
                                            className="object-cover"
                                        />
                                    )}
                                </div>
                            ) : (
                                <div className="flex items-center justify-center aspect-[4/3] rounded-md bg-muted/50">
                                    <p className="text-sm text-destructive font-medium">{t("masterData.trucks.noInsuranceDoc")}</p>
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
