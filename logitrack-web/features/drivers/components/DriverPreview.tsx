"use client";

import { useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import {
    Edit, Truck, Calendar, User, FileText, Info,
    Phone, Mail, CreditCard, Briefcase, Building2, Plus
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { driverStatusTone, toneBadgeVariant } from "@/lib/statusPresentation";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import Image from "next/image";
import { useDriver } from "@/features/drivers/api/useDrivers";
import { useAssignmentsOf } from "@/features/trucks/api/useTrucks";
import { FileViewer } from "@/components/ui/file-viewer";
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from "@/components/ui/table";
import { PageLoading, QueryError, QueryLoading } from "@/components/page-states";
import { driverDisplayName } from "@/lib/driverName";
import { PageHeader } from "@/components/page-header";
import { RecordState, recordUnavailable } from "@/components/record-state";
import { format } from "date-fns";
import { th } from "date-fns/locale";
import { useLanguage } from "@/context/language";

function StatusBadge({ status }: { status: string }) {
    const { t } = useLanguage();
    return (
        <Badge variant={toneBadgeVariant(driverStatusTone(status))}>
            {t(`masterData.drivers.status.${status}`)}
        </Badge>
    );
}

export default function DriverPreview() {
    const searchParams = useSearchParams();
    const driverId = searchParams.get('id');
    const { t, language } = useLanguage();

    const [isViewerOpen, setIsViewerOpen] = useState(false);
    const [viewerIndex, setViewerIndex] = useState(0);

    // `['drivers','detail',id]` and `['truckAssignments',{driverId}]` in parallel (T25); the carrier
    // name comes inside the driver (`tenantNameTh`), so the subcontractor list is not loaded.
    const driverQuery = useDriver(driverId ?? "");
    const driver = driverQuery.data ?? null;
    const assignmentsQuery = useAssignmentsOf({ driverId: driverId ?? "" });
    const assignmentHistory = assignmentsQuery.assignments;
    const isLoading = driverQuery.isPending && Boolean(driverId);

    const formatDate = (date: Date | string | null | undefined) => {
        if (!date) return "-";
        try {
            return format(new Date(date), "dd MMMM yyyy", { locale: language === "th" ? th : undefined });
        } catch {
            return "-";
        }
    };

    if (isLoading) return <PageLoading />;

    // No id, no driver, or the read failed: not found, or a load error with a retry (UI3D).
    // A failed background refetch keeps the loaded record (recordUnavailable); only a missing one shows RecordState.
    if (!driverId || recordUnavailable(driverQuery) || !driver) {
        return <RecordState error={driverQuery.error} onRetry={() => void driverQuery.refetch()} retrying={driverQuery.isFetching} />;
    }

    // Presigned GETs on Go (5 min for the ID card and licence, `drivers:view_pii`), stored URLs on Firestore.
    const profileImage = driver.profileUrl ?? "";
    const idCardImage = driver.idCardUrl ?? "";
    const truckLicenseImage = driver.licenseUrl ?? "";

    const idCardIsPdf = (driver.idCardKey ?? idCardImage.split(/[?#]/)[0]).toLowerCase().endsWith(".pdf");
    const licenseIsPdf = (driver.licenseKey ?? truckLicenseImage.split(/[?#]/)[0]).toLowerCase().endsWith(".pdf");
    const viewableFiles = [
        ...(profileImage ? [{ url: profileImage, type: "image" as const, label: t("drivers.detail.profilePhoto") }] : []),
        ...(idCardImage ? [{ url: idCardImage, type: idCardIsPdf ? "pdf" as const : "image" as const, label: t("drivers.detail.idCard") }] : []),
        ...(truckLicenseImage ? [{ url: truckLicenseImage, type: licenseIsPdf ? "pdf" as const : "image" as const, label: t("drivers.detail.driverLicense") }] : []),
    ];

    const handleFileClick = (url: string) => {
        const index = viewableFiles.findIndex(f => f.url === url);
        if (index !== -1) {
            setViewerIndex(index);
            setIsViewerOpen(true);
        }
    };


    return (
        <div className="flex flex-col gap-section">
            <PageHeader
                title={t("nav.drivers")}
                recordName={driverDisplayName({ ...driver }, driver.id)}
                back={{ href: "/app/drivers" }}
                status={<StatusBadge status={String(driver.status)} />}
                description={
                    <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
                        <span className="flex items-center gap-1">
                            <Info className="h-3.5 w-3.5" aria-hidden />
                            {t("drivers.detail.registered")} {formatDate(driver.createdAt)}
                        </span>
                        <span aria-hidden>•</span>
                        <span>{t("trucks.table.id")}: <span className="font-mono">{driver.id?.substring(0, 8).toUpperCase() ?? "-"}</span></span>
                    </span>
                }
                actions={
                    <Button asChild>
                        <Link href={`/app/drivers/edit?id=${driver.id}`} prefetch={false}>
                            <Edit className="h-4 w-4" aria-hidden />
                            {t("drivers.detail.editProfile")}
                        </Link>
                    </Button>
                }
            />

            <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
                {/* Left Column */}
                <div className="lg:col-span-2 space-y-6">
                    <Card>
                        <CardHeader className="pb-3">
                            <CardTitle className="flex items-center gap-2 text-base font-semibold">
                                <User className="h-5 w-5 text-primary" />
                                {t("drivers.detail.personalInfo")}
                            </CardTitle>
                        </CardHeader>
                        <CardContent className="grid grid-cols-1 md:grid-cols-2 gap-y-4 gap-x-12">
                            <div className="flex justify-between py-2 border-b">
                                <span className="text-sm text-muted-foreground flex items-center gap-2">
                                    <Phone className="h-4 w-4" /> {t("drivers.detail.mobile")}
                                </span>
                                <span className="text-sm font-medium">{driver.mobile}</span>
                            </div>
                            <div className="flex justify-between py-2 border-b">
                                <span className="text-sm text-muted-foreground flex items-center gap-2">
                                    <Mail className="h-4 w-4" /> {t("drivers.detail.email")}
                                </span>
                                <span className="text-sm font-medium">{driver.email || "-"}</span>
                            </div>
                            <div className="flex justify-between py-2 border-b">
                                <span className="text-sm text-muted-foreground flex items-center gap-2">
                                    <Calendar className="h-4 w-4" /> {t("drivers.detail.birthDate")}
                                </span>
                                <span className="text-sm font-medium">{formatDate(driver.birthDate)}</span>
                            </div>
                            <div className="flex justify-between py-2 border-b">
                                <span className="text-sm text-muted-foreground flex items-center gap-2">
                                    <CreditCard className="h-4 w-4" /> {t("drivers.detail.idCardNo")}
                                </span>
                                <span className="text-sm font-medium font-mono">{driver.idCard ?? "-"}</span>
                            </div>
                        </CardContent>
                    </Card>

                    <Card>
                        <CardHeader className="pb-3">
                            <CardTitle className="flex items-center gap-2 text-base font-semibold">
                                <Briefcase className="h-5 w-5 text-primary" />
                                {t("drivers.detail.employmentDetails")}
                            </CardTitle>
                        </CardHeader>
                        <CardContent>
                            <div className="grid grid-cols-1 md:grid-cols-2 gap-y-4 gap-x-12">
                                <div className="flex justify-between py-2 border-b">
                                    <span className="text-sm text-muted-foreground">{t("drivers.detail.employmentType")}</span>
                                    <span className="text-sm font-medium">
                                        {driver.employmentType === 'full_time' ? t("drivers.detail.fullTime") :
                                            driver.employmentType === 'part_time' ? t("drivers.detail.partTime") : t("drivers.detail.subcontractor")}
                                    </span>
                                </div>
                                {driver.employmentType === 'subcontractor' && (
                                    <div className="flex justify-between py-2 border-b">
                                        <span className="text-sm text-muted-foreground">{t("drivers.detail.subcontractor")}</span>
                                        <span className="text-sm font-medium text-primary">
                                            {driver.tenantNameTh || driver.tenantNameEn || driver.tenantId}
                                        </span>
                                    </div>
                                )}
                                <div className="flex justify-between py-2 border-b">
                                    <span className="text-sm text-muted-foreground">{t("drivers.detail.contractDuration")}</span>
                                    <span className="text-sm font-medium">{driver.contractYears ? `${driver.contractYears} ${t("drivers.detail.years")}` : "-"}</span>
                                </div>
                                <div className="flex justify-between py-2 border-b">
                                    <span className="text-sm text-muted-foreground">{t("drivers.detail.truckLicenseId")}</span>
                                    <span className="text-sm font-medium font-mono">{driver.truckLicenseId}</span>
                                </div>
                                <div className="flex justify-between py-2 border-b">
                                    <span className="text-sm text-muted-foreground">{t("drivers.detail.licenseType")}</span>
                                    <span className="text-sm font-medium">{driver.licenseType || "-"}</span>
                                </div>
                            </div>
                        </CardContent>
                    </Card>

                    {(() => {
                        const entries = driver.customerDriverIds
                            .filter((c) => c.value && c.value.trim())
                            .map((c) => ({ customer: c.customerCode, idType: c.idTypeKey, value: c.value }));
                        if (entries.length === 0) return null;
                        return (
                            <Card>
                                <CardHeader className="pb-3">
                                    <CardTitle className="flex items-center gap-2 text-base font-semibold">
                                        <Building2 className="h-5 w-5 text-primary" />
                                        {t("drivers.detail.customerDriverIds")}
                                    </CardTitle>
                                    <p className="text-sm text-muted-foreground">{t("drivers.detail.customerDriverIdsDesc")}</p>
                                </CardHeader>
                                <CardContent>
                                    <div className="grid grid-cols-1 md:grid-cols-2 gap-y-4 gap-x-12">
                                        {entries.map(({ customer, idType, value }) => (
                                            <div key={`${customer}-${idType}`} className="flex justify-between py-2 border-b">
                                                <span className="text-sm text-muted-foreground capitalize">
                                                    {customer} – {idType}
                                                </span>
                                                <span className="text-sm font-medium font-mono">{value}</span>
                                            </div>
                                        ))}
                                    </div>
                                </CardContent>
                            </Card>
                        );
                    })()}

                    <Card>
                        <CardHeader className="pb-3 border-b">
                            <CardTitle className="flex items-center gap-2 text-base font-semibold">
                                <FileText className="h-5 w-5 text-primary" />
                                {t("drivers.detail.documents")}
                            </CardTitle>
                        </CardHeader>
                        <CardContent className="pt-4 grid grid-cols-1 sm:grid-cols-2 gap-4">
                            <div className="space-y-2">
                                <p className="text-xs font-semibold text-muted-foreground">{t("drivers.detail.idCard")}</p>
                                {idCardImage ? (
                                    <div
                                        className="relative aspect-[4/3] rounded-md overflow-hidden bg-muted cursor-pointer border hover:border-border transition-all"
                                        onClick={() => handleFileClick(idCardImage)}
                                    >
                                        {idCardIsPdf ? (
                                            <div className="flex flex-col items-center justify-center h-full bg-muted/50">
                                                <FileText className="h-10 w-10 text-destructive mb-2" />
                                                <span className="text-xs font-medium text-muted-foreground">{t("drivers.detail.viewPdf")}</span>
                                            </div>
                                        ) : (
                                            <Image
                                                src={idCardImage}
                                                alt={t("drivers.detail.idCard")}
                                                fill
                                                className="object-contain"
                                            />
                                        )}
                                    </div>
                                ) : (
                                    <div className="flex items-center justify-center aspect-[4/3] rounded-md bg-muted/30 border border-dashed">
                                        <p className="text-xs text-muted-foreground">{t("drivers.detail.noDocument")}</p>
                                    </div>
                                )}
                            </div>

                            <div className="space-y-2">
                                <p className="text-xs font-semibold text-muted-foreground">{t("drivers.detail.drivingLicense")}</p>
                                {truckLicenseImage ? (
                                    <div
                                        className="relative aspect-[4/3] rounded-md overflow-hidden bg-muted cursor-pointer border hover:border-border transition-all"
                                        onClick={() => handleFileClick(truckLicenseImage)}
                                    >
                                        {licenseIsPdf ? (
                                            <div className="flex flex-col items-center justify-center h-full bg-muted/50">
                                                <FileText className="h-10 w-10 text-destructive mb-2" />
                                                <span className="text-xs font-medium text-muted-foreground">{t("drivers.detail.viewPdf")}</span>
                                            </div>
                                        ) : (
                                            <Image
                                                src={truckLicenseImage}
                                                alt={t("drivers.detail.drivingLicense")}
                                                fill
                                                className="object-contain"
                                            />
                                        )}
                                    </div>
                                ) : (
                                    <div className="flex items-center justify-center aspect-[4/3] rounded-md bg-muted/30 border border-dashed">
                                        <p className="text-xs text-muted-foreground">{t("drivers.detail.noDocument")}</p>
                                    </div>
                                )}
                            </div>
                        </CardContent>
                    </Card>
                </div>

                {/* Right Column */}
                <div className="space-y-6">
                    <Card>
                        <CardContent className="pt-6 flex flex-col items-center">
                            <div
                                className="relative h-40 w-40 rounded-full overflow-hidden border-4 border-muted cursor-pointer mb-4 hover:opacity-90 transition-opacity"
                                onClick={() => profileImage && handleFileClick(profileImage)}
                            >
                                {profileImage ? (
                                    <Image src={profileImage} alt={driverDisplayName({ ...driver }, driver.id)} fill className="object-cover" />
                                ) : (
                                    <div className="h-full w-full bg-muted flex items-center justify-center text-muted-foreground">
                                        <User className="h-20 w-20" />
                                    </div>
                                )}
                            </div>
                            <h3 className="text-xl font-bold">{driverDisplayName({ ...driver }, driver.id)}</h3>
                            <p className="text-sm text-muted-foreground">{driver.email}</p>
                            <div className="flex gap-2 mt-4 w-full">
                                {driver.mobile?.trim() && (
                                    <Button asChild className="flex-1 max-md:min-h-11" variant="outline">
                                        <a href={`tel:${driver.mobile}`}>
                                            <Phone className="h-4 w-4 mr-2" aria-hidden />
                                            {t("drivers.detail.call")}
                                        </a>
                                    </Button>
                                )}
                                {driver.email?.trim() && (
                                    <Button asChild className="flex-1 max-md:min-h-11" variant="outline">
                                        <a href={`mailto:${driver.email}`}>
                                            <Mail className="h-4 w-4 mr-2" aria-hidden />
                                            {t("drivers.detail.emailBtn")}
                                        </a>
                                    </Button>
                                )}
                            </div>
                        </CardContent>
                    </Card>

                    <Card>
                        <CardHeader className="flex flex-row items-center justify-between py-6">
                            <CardTitle className="text-lg font-bold flex items-center gap-2">
                                <Truck className="h-5 w-5" />
                                {t("drivers.detail.truckAssignment")}
                            </CardTitle>
                            {!driver.currentAssignment && (
                                <Button size="sm" className="h-8 gap-2" variant="outline" asChild>
                                    <Link href="/app/truck-assignment" prefetch={false}>
                                        <Plus className="h-3.5 w-3.5" />
                                        {t("drivers.detail.assign")}
                                    </Link>
                                </Button>
                            )}
                        </CardHeader>
                        <CardContent className="p-0">
                            {assignmentsQuery.isError && assignmentsQuery.data ? <QueryError label={t("drivers.history.loadError")} onRetry={() => void assignmentsQuery.refetch()} retrying={assignmentsQuery.isFetching} /> : null}
                            <Table>
                                <TableHeader>
                                    <TableRow>
                                        <TableHead className="w-[120px]">{t("drivers.detail.truck")}</TableHead>
                                        <TableHead>{t("drivers.detail.start")}</TableHead>
                                        <TableHead>{t("drivers.detail.end")}</TableHead>
                                        <TableHead className="text-right">{t("drivers.table.status")}</TableHead>
                                    </TableRow>
                                </TableHeader>
                                <TableBody>
                                    {assignmentsQuery.isPending ? (
                                        <TableRow><TableCell colSpan={4}><QueryLoading label={t("assignments.table.loading")} /></TableCell></TableRow>
                                    ) : assignmentsQuery.isError && !assignmentsQuery.data ? (
                                        <TableRow><TableCell colSpan={4}><QueryError label={t("drivers.history.loadError")} onRetry={() => void assignmentsQuery.refetch()} retrying={assignmentsQuery.isFetching} /></TableCell></TableRow>
                                    ) : assignmentHistory.length > 0 ? (
                                        assignmentHistory.map((history) => (
                                            <TableRow key={history.id}>
                                                <TableCell>
                                                    <Link href={`/app/trucks/view?id=${history.truckId}`} prefetch={false} onClick={(e) => e.stopPropagation()} className="flex flex-col justify-center max-md:min-h-11 max-md:min-w-11 rounded-sm text-xs text-primary hover:underline focus-visible:outline-2 focus-visible:outline-ring">
                                                        <span className="font-medium">{history.truckPlate}</span>
                                                        <span>{history.truckModel}</span>
                                                    </Link>
                                                </TableCell>
                                                <TableCell className="text-xs text-muted-foreground">
                                                    {history.createdAt ? format(new Date(history.createdAt), "dd MMM yy", { locale: language === "th" ? th : undefined }) : "-"}
                                                </TableCell>
                                                <TableCell className="text-xs text-muted-foreground">
                                                    {history.revokedAt ? format(new Date(history.revokedAt), "dd MMM yy", { locale: language === "th" ? th : undefined }) : "-"}
                                                </TableCell>
                                                <TableCell className="text-right">
                                                    <Badge
                                                        variant={toneBadgeVariant(driverStatusTone(history.status))}
                                                    >
                                                        {history.status === "active" ? t("drivers.detail.current") : t("drivers.detail.revoked")}
                                                    </Badge>
                                                </TableCell>
                                            </TableRow>
                                        ))
                                    ) : (
                                        <TableRow>
                                            <TableCell colSpan={4} className="h-24 text-center text-xs text-muted-foreground">
                                                {t("drivers.detail.noHistory")}
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
