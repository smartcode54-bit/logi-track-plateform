import { useState } from "react";
import { z } from "zod";
import { truckSchema } from "@/validate/truckSchema";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { truckFileType } from "../../utils/uploads";
import { truckTypeLabel, truckFuelLabel } from "../../utils/labels";
import Image from "next/image";
import { Edit, Check, X, FileText, Camera } from "lucide-react";
import { useLanguage } from "@/context/language";
import { formatLicensePlate } from "@/lib/utils";
import { FileViewer } from "@/components/ui/file-viewer";

interface TruckPreviewProps {
    data: z.infer<typeof truckSchema>;
    subcontractorName?: string;
    onEdit: () => void;
    onConfirm: () => void;
    onCancel: () => void;
    isSubmitting?: boolean;
}

export function TruckPreview({
    data,
    subcontractorName,
    onEdit,
    onConfirm,
    onCancel,
    isSubmitting = false,
}: TruckPreviewProps) {
    const { t, language } = useLanguage();
    const [isViewerOpen, setIsViewerOpen] = useState(false);
    const [viewerIndex, setViewerIndex] = useState(0);

    const formatDate = (dateString: string | undefined) => {
        if (!dateString) return "-";
        try {
            const date = new Date(dateString);
            return date.toLocaleDateString(language === "th" ? "th-TH" : "en-US", {
                year: "numeric",
                month: "long",
                day: "numeric",
            });
        } catch {
            return dateString;
        }
    };

    const formatNumber = (value: number | undefined) => {
        if (value === undefined || value === null) return "-";
        return new Intl.NumberFormat(language === "th" ? "th-TH" : "en-US").format(value);
    };

    // Construct viewable files list
    const viewableFiles = [
        ...(data.imageFrontRight ? [{ url: data.imageFrontRight, type: "image" as const, label: t("trucks.photos.frontRight") }] : []),
        ...(data.imageFrontLeft ? [{ url: data.imageFrontLeft, type: "image" as const, label: t("trucks.photos.frontLeft") }] : []),
        ...(data.imageBackRight ? [{ url: data.imageBackRight, type: "image" as const, label: t("trucks.photos.backRight") }] : []),
        ...(data.imageBackLeft ? [{ url: data.imageBackLeft, type: "image" as const, label: t("trucks.photos.backLeft") }] : []),
        ...(data.documentTax ? [{ url: data.documentTax, type: truckFileType(data.documentTax), label: t("trucks.documents.tax") }] : []),
        ...(data.documentRegister ? [{ url: data.documentRegister, type: truckFileType(data.documentRegister), label: t("trucks.detail.view.registrationDoc") }] : []),
    ];

    const handleFileClick = (url: string) => {
        const index = viewableFiles.findIndex(f => f.url === url);
        if (index !== -1) {
            setViewerIndex(index);
            setIsViewerOpen(true);
        }
    };

    return (
        <div className="space-y-6">
            <div className="flex items-center justify-between border-b pb-4">
                <div>
                    <h2 className="text-2xl font-bold">{t("trucks.ui4.previewTitle")}</h2>
                    <p className="text-sm text-muted-foreground mt-1">
                        {t("trucks.ui4.previewHint")}
                    </p>
                </div>
            </div>

            {/* Ownership & Identification Section */}
            <Card>
                <CardHeader>
                    <CardTitle>{t("trucks.ui4.ownershipIdentification")}</CardTitle>
                </CardHeader>
                <CardContent className="grid grid-cols-1 md:grid-cols-2 gap-4">
                    <div>
                        <p className="text-sm font-medium text-muted-foreground">{t("trucks.ui4.ownershipType")}</p>
                        <p className="text-base font-semibold capitalize">{data.ownershipType === "subcontractor" ? t("trucks.detail.subcontractorFleet") : t("trucks.detail.ownFleet")}</p>
                    </div>
                    {data.ownershipType === "subcontractor" && subcontractorName && (
                        <div>
                            <p className="text-sm font-medium text-muted-foreground">{t("trucks.detail.subcontractorFleet")}</p>
                            <p className="text-base font-semibold">{subcontractorName}</p>
                        </div>
                    )}
                    <div>
                        <p className="text-sm font-medium text-muted-foreground">{t("trucks.section.licensePlate")}</p>
                        <p className="text-base font-semibold">{formatLicensePlate(data.licensePlate)}</p>
                    </div>
                    <div>
                        <p className="text-sm font-medium text-muted-foreground">{t("trucks.section.province")}</p>
                        <p className="text-base font-semibold">{data.province}</p>
                    </div>
                    {data.vin && (
                        <div>
                            <p className="text-sm font-medium text-muted-foreground">{t("trucks.section.vin")}</p>
                            <p className="text-base font-semibold">{data.vin}</p>
                        </div>
                    )}
                    {data.engineNumber && (
                        <div>
                            <p className="text-sm font-medium text-muted-foreground">{t("trucks.section.engineNumber")}</p>
                            <p className="text-base font-semibold">{data.engineNumber}</p>
                        </div>
                    )}
                    <div>
                        <p className="text-sm font-medium text-muted-foreground">{t("trucks.ui4.status")}</p>
                        <p className="text-base font-semibold capitalize">{data.truckStatus}</p>
                    </div>
                </CardContent>
            </Card>

            {/* Vehicle Details Section */}
            <Card>
                <CardHeader>
                    <CardTitle>{t("trucks.ui4.vehicleDetails")}</CardTitle>
                </CardHeader>
                <CardContent className="grid grid-cols-1 md:grid-cols-2 gap-4">
                    <div>
                        <p className="text-sm font-medium text-muted-foreground">{t("trucks.section.brand")}</p>
                        <p className="text-base font-semibold">{data.brand}</p>
                    </div>
                    <div>
                        <p className="text-sm font-medium text-muted-foreground">{t("trucks.section.model")}</p>
                        <p className="text-base font-semibold">{data.model}</p>
                    </div>
                    <div>
                        <p className="text-sm font-medium text-muted-foreground">{t("trucks.section.year")}</p>
                        <p className="text-base font-semibold">{data.year}</p>
                    </div>
                    <div>
                        <p className="text-sm font-medium text-muted-foreground">{t("trucks.section.color")}</p>
                        <p className="text-base font-semibold">{data.color}</p>
                    </div>
                    <div>
                        <p className="text-sm font-medium text-muted-foreground">{t("trucks.step4.type")}</p>
                        <p className="text-base font-semibold">{truckTypeLabel(data.type, t)}</p>
                    </div>
                    <div>
                        <p className="text-sm font-medium text-muted-foreground">{t("trucks.section.seats")}</p>
                        <p className="text-base font-semibold">{data.seats || "-"}</p>
                    </div>
                </CardContent>
            </Card>

            {/* Engine Information Section */}
            <Card>
                <CardHeader>
                    <CardTitle>{t("trucks.section.engineInfo")}</CardTitle>
                </CardHeader>
                <CardContent className="grid grid-cols-1 md:grid-cols-2 gap-4">
                    <div>
                        <p className="text-sm font-medium text-muted-foreground">{t("trucks.section.fuelType")}</p>
                        <p className="text-base font-semibold">{truckFuelLabel(data.fuelType, t)}</p>
                    </div>
                    <div>
                        <p className="text-sm font-medium text-muted-foreground">{t("trucks.ui4.engineCapacity")}</p>
                        <p className="text-base font-semibold">{formatNumber(data.engineCapacity)}</p>
                    </div>
                    <div>
                        <p className="text-sm font-medium text-muted-foreground">{t("trucks.ui4.fuelCapacity")}</p>
                        <p className="text-base font-semibold">{formatNumber(data.fuelCapacity)}</p>
                    </div>
                    <div>
                        <p className="text-sm font-medium text-muted-foreground">{t("trucks.ui4.maxLoadWeight")}</p>
                        <p className="text-base font-semibold">{formatNumber(data.maxLoadWeight)}</p>
                    </div>
                </CardContent>
            </Card>

            {/* Registration Section */}
            <Card>
                <CardHeader>
                    <CardTitle>{t("trucks.ui4.registrationAssignment")}</CardTitle>
                </CardHeader>
                <CardContent className="grid grid-cols-1 md:grid-cols-2 gap-4">
                    <div>
                        <p className="text-sm font-medium text-muted-foreground">{t("trucks.section.regDate")}</p>
                        <p className="text-base font-semibold">{formatDate(data.registrationDate)}</p>
                    </div>
                    <div>
                        <p className="text-sm font-medium text-muted-foreground">{t("trucks.section.buyingDate")}</p>
                        <p className="text-base font-semibold">{formatDate(data.buyingDate)}</p>
                    </div>
                    {data.notes && (
                        <div className="md:col-span-2">
                            <p className="text-sm font-medium text-muted-foreground">{t("trucks.section.notes")}</p>
                            <p className="text-base">{data.notes}</p>
                        </div>
                    )}
                </CardContent>
            </Card>
            {/* Action Buttons */}
            {/* Photos & Documents Section */}
            <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
                <Card>
                    <CardHeader>
                        <CardTitle className="flex items-center gap-2">
                            <Camera className="h-5 w-5 text-primary" />
                            {t("trucks.ui4.photos")}
                        </CardTitle>
                    </CardHeader>
                    <CardContent className="space-y-6">
                        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                            {[
                                { src: data.imageFrontRight, label: t("trucks.photos.frontRight") },
                                { src: data.imageFrontLeft, label: t("trucks.photos.frontLeft") },
                                { src: data.imageBackRight, label: t("trucks.photos.backRight") },
                                { src: data.imageBackLeft, label: t("trucks.photos.backLeft") },
                            ].map((photo, i) => (
                                <div key={i} className="space-y-2 cursor-pointer group" onClick={() => photo.src && handleFileClick(photo.src)}>
                                    <p className="text-sm font-medium text-muted-foreground">{photo.label}</p>
                                    <div className="relative aspect-video rounded-md overflow-hidden border bg-muted/30 group-hover:ring-2 group-hover:ring-primary transition-all">
                                        {photo.src ? (
                                            <Image src={photo.src} alt={photo.label} fill className="object-cover" />
                                        ) : (
                                            <div className="flex items-center justify-center h-full text-muted-foreground text-xs">{t("trucks.ui4.noImage")}</div>
                                        )}
                                        {photo.src && (
                                            <div className="absolute inset-0 bg-background/80 opacity-0 group-hover:opacity-100 transition-opacity flex items-center justify-center">
                                                <Camera className="h-6 w-6 text-foreground" />
                                            </div>
                                        )}
                                    </div>
                                </div>
                            ))}
                        </div>
                    </CardContent>
                </Card>

                <Card>
                    <CardHeader>
                        <CardTitle className="flex items-center gap-2">
                            <FileText className="h-5 w-5 text-primary" />
                            {t("trucks.ui4.documents")}
                        </CardTitle>
                    </CardHeader>
                    <CardContent className="space-y-4">
                        {[
                            { url: data.documentTax, label: t("trucks.documents.tax") },
                            { url: data.documentRegister, label: t("trucks.detail.view.registrationDoc") },
                        ].map((doc, i) => (
                            <div key={i} className="space-y-2">
                                <p className="text-sm font-medium text-muted-foreground">{doc.label}</p>
                                {doc.url ? (
                                    <div className="p-3 border rounded-md flex items-center gap-2 bg-muted/30">
                                        <FileText className="h-4 w-4 text-primary" />
                                        <Button variant="link" className="p-0 h-auto hover:underline" onClick={() => handleFileClick(doc.url!)}>
                                            {t("trucks.ui4.viewDocument")}
                                        </Button>
                                    </div>
                                ) : (
                                    <p className="text-sm text-destructive">{t("trucks.ui4.missing")}</p>
                                )}
                            </div>
                        ))}
                    </CardContent>
                </Card>
            </div>

            <FileViewer
                isOpen={isViewerOpen}
                onClose={() => setIsViewerOpen(false)}
                files={viewableFiles}
                initialIndex={viewerIndex}
            />

            {/* Action Buttons */}
            <div className="flex flex-col gap-3 border-t pt-4 md:flex-row md:justify-end">
                <Button
                    type="button"
                    variant="outline"
                    onClick={onEdit}
                    disabled={isSubmitting}
                    className="flex items-center gap-2"
                >
                    <Edit className="h-4 w-4" />
                    {t("trucks.action.edit")}
                </Button>
                <Button
                    type="button"
                    variant="outline"
                    onClick={onCancel}
                    disabled={isSubmitting}
                    className="flex items-center gap-2"
                >
                    <X className="h-4 w-4" />
                    {t("trucks.edit.cancel")}
                </Button>
                <Button
                    type="button"
                    onClick={onConfirm}
                    disabled={isSubmitting}
                    className="flex items-center gap-2"
                >
                    <Check className="h-4 w-4" />
                    {isSubmitting ? t("trucks.edit.saving") : t("trucks.ui4.confirmSave")}
                </Button>
            </div>
        </div>
    );
}
