import { useState } from "react";
import { z } from "zod";
import { truckSchema } from "@/validate/truckSchema";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
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
    const { t } = useLanguage();
    const [isViewerOpen, setIsViewerOpen] = useState(false);
    const [viewerIndex, setViewerIndex] = useState(0);

    const formatDate = (dateString: string | undefined) => {
        if (!dateString) return "-";
        try {
            const date = new Date(dateString);
            return date.toLocaleDateString("th-TH", {
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
        return new Intl.NumberFormat("th-TH").format(value);
    };

    // Construct viewable files list
    const viewableFiles = [
        ...(data.imageFrontRight ? [{ url: data.imageFrontRight, type: "image" as const, label: "Front-Right View" }] : []),
        ...(data.imageFrontLeft ? [{ url: data.imageFrontLeft, type: "image" as const, label: "Front-Left View" }] : []),
        ...(data.imageBackRight ? [{ url: data.imageBackRight, type: "image" as const, label: "Back-Right View" }] : []),
        ...(data.imageBackLeft ? [{ url: data.imageBackLeft, type: "image" as const, label: "Back-Left View" }] : []),
        ...(data.documentTax ? [{ url: data.documentTax, type: "pdf" as const, label: "Tax Document" }] : []),
        ...(data.documentRegister ? [{ url: data.documentRegister, type: "pdf" as const, label: "Registration Document" }] : []),
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
                    <h2 className="text-2xl font-bold">Preview Truck Information</h2>
                    <p className="text-sm text-muted-foreground mt-1">
                        กรุณาตรวจสอบข้อมูลก่อนบันทึก
                    </p>
                </div>
            </div>

            {/* Ownership & Identification Section */}
            <Card>
                <CardHeader>
                    <CardTitle>Ownership & Identification</CardTitle>
                </CardHeader>
                <CardContent className="grid grid-cols-2 gap-4">
                    <div>
                        <p className="text-sm font-medium text-muted-foreground">Ownership Type</p>
                        <p className="text-base font-semibold capitalize">{data.ownershipType === "subcontractor" ? "Subcontractor" : "Own Fleet"}</p>
                    </div>
                    {data.ownershipType === "subcontractor" && subcontractorName && (
                        <div>
                            <p className="text-sm font-medium text-muted-foreground">Subcontractor</p>
                            <p className="text-base font-semibold">{subcontractorName}</p>
                        </div>
                    )}
                    <div>
                        <p className="text-sm font-medium text-muted-foreground">License Plate</p>
                        <p className="text-base font-semibold">{formatLicensePlate(data.licensePlate)}</p>
                    </div>
                    <div>
                        <p className="text-sm font-medium text-muted-foreground">Province</p>
                        <p className="text-base font-semibold">{data.province}</p>
                    </div>
                    {data.vin && (
                        <div>
                            <p className="text-sm font-medium text-muted-foreground">VIN</p>
                            <p className="text-base font-semibold">{data.vin}</p>
                        </div>
                    )}
                    {data.engineNumber && (
                        <div>
                            <p className="text-sm font-medium text-muted-foreground">Engine Number</p>
                            <p className="text-base font-semibold">{data.engineNumber}</p>
                        </div>
                    )}
                    <div>
                        <p className="text-sm font-medium text-muted-foreground">Status</p>
                        <p className="text-base font-semibold capitalize">{data.truckStatus}</p>
                    </div>
                </CardContent>
            </Card>

            {/* Vehicle Details Section */}
            <Card>
                <CardHeader>
                    <CardTitle>Vehicle Details</CardTitle>
                </CardHeader>
                <CardContent className="grid grid-cols-2 gap-4">
                    <div>
                        <p className="text-sm font-medium text-muted-foreground">Brand</p>
                        <p className="text-base font-semibold">{data.brand}</p>
                    </div>
                    <div>
                        <p className="text-sm font-medium text-muted-foreground">Model</p>
                        <p className="text-base font-semibold">{data.model}</p>
                    </div>
                    <div>
                        <p className="text-sm font-medium text-muted-foreground">Year</p>
                        <p className="text-base font-semibold">{data.year}</p>
                    </div>
                    <div>
                        <p className="text-sm font-medium text-muted-foreground">Color</p>
                        <p className="text-base font-semibold">{data.color}</p>
                    </div>
                    <div>
                        <p className="text-sm font-medium text-muted-foreground">Type</p>
                        <p className="text-base font-semibold">{data.type}</p>
                    </div>
                    <div>
                        <p className="text-sm font-medium text-muted-foreground">Seats</p>
                        <p className="text-base font-semibold">{data.seats || "-"}</p>
                    </div>
                </CardContent>
            </Card>

            {/* Engine Information Section */}
            <Card>
                <CardHeader>
                    <CardTitle>Engine Information</CardTitle>
                </CardHeader>
                <CardContent className="grid grid-cols-2 gap-4">
                    <div>
                        <p className="text-sm font-medium text-muted-foreground">Fuel Type</p>
                        <p className="text-base font-semibold">{data.fuelType}</p>
                    </div>
                    <div>
                        <p className="text-sm font-medium text-muted-foreground">Engine Capacity (cc)</p>
                        <p className="text-base font-semibold">{formatNumber(data.engineCapacity)}</p>
                    </div>
                    <div>
                        <p className="text-sm font-medium text-muted-foreground">Fuel Capacity (L)</p>
                        <p className="text-base font-semibold">{formatNumber(data.fuelCapacity)}</p>
                    </div>
                    <div>
                        <p className="text-sm font-medium text-muted-foreground">Max Load Weight (kg)</p>
                        <p className="text-base font-semibold">{formatNumber(data.maxLoadWeight)}</p>
                    </div>
                </CardContent>
            </Card>

            {/* Registration Section */}
            <Card>
                <CardHeader>
                    <CardTitle>Registration & Assignment</CardTitle>
                </CardHeader>
                <CardContent className="grid grid-cols-2 gap-4">
                    <div>
                        <p className="text-sm font-medium text-muted-foreground">Registration Date</p>
                        <p className="text-base font-semibold">{formatDate(data.registrationDate)}</p>
                    </div>
                    <div>
                        <p className="text-sm font-medium text-muted-foreground">Buying Date</p>
                        <p className="text-base font-semibold">{formatDate(data.buyingDate)}</p>
                    </div>
                    {data.notes && (
                        <div className="col-span-2">
                            <p className="text-sm font-medium text-muted-foreground">Notes</p>
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
                            Truck Photos
                        </CardTitle>
                    </CardHeader>
                    <CardContent className="space-y-6">
                        <div className="grid grid-cols-2 gap-4">
                            {[
                                { src: data.imageFrontRight, label: "Front-Right" },
                                { src: data.imageFrontLeft, label: "Front-Left" },
                                { src: data.imageBackRight, label: "Back-Right" },
                                { src: data.imageBackLeft, label: "Back-Left" },
                            ].map((photo, i) => (
                                <div key={i} className="space-y-2 cursor-pointer group" onClick={() => photo.src && handleFileClick(photo.src)}>
                                    <p className="text-sm font-medium text-muted-foreground">{photo.label}</p>
                                    <div className="relative aspect-video rounded-md overflow-hidden border bg-muted/30 group-hover:ring-2 group-hover:ring-primary transition-all">
                                        {photo.src ? (
                                            <Image src={photo.src} alt={photo.label} fill className="object-cover" />
                                        ) : (
                                            <div className="flex items-center justify-center h-full text-muted-foreground text-xs">No Image</div>
                                        )}
                                        {photo.src && (
                                            <div className="absolute inset-0 bg-black/40 opacity-0 group-hover:opacity-100 transition-opacity flex items-center justify-center">
                                                <Camera className="h-6 w-6 text-white" />
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
                            Truck Documents
                        </CardTitle>
                    </CardHeader>
                    <CardContent className="space-y-4">
                        {[
                            { url: data.documentTax, label: "Tax Document" },
                            { url: data.documentRegister, label: "Registration Document" },
                        ].map((doc, i) => (
                            <div key={i} className="space-y-2">
                                <p className="text-sm font-medium text-muted-foreground">{doc.label}</p>
                                {doc.url ? (
                                    <div className="p-3 border rounded-md flex items-center gap-2 bg-muted/30">
                                        <FileText className="h-4 w-4 text-primary" />
                                        <Button variant="link" className="p-0 h-auto text-blue-600 hover:underline" onClick={() => handleFileClick(doc.url!)}>
                                            View Document
                                        </Button>
                                    </div>
                                ) : (
                                    <p className="text-sm text-destructive">Missing</p>
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
            <div className="flex justify-end gap-4 pt-4 border-t">
                <Button
                    type="button"
                    variant="outline"
                    onClick={onEdit}
                    disabled={isSubmitting}
                    className="flex items-center gap-2"
                >
                    <Edit className="h-4 w-4" />
                    Edit
                </Button>
                <Button
                    type="button"
                    variant="outline"
                    onClick={onCancel}
                    disabled={isSubmitting}
                    className="flex items-center gap-2"
                >
                    <X className="h-4 w-4" />
                    Cancel
                </Button>
                <Button
                    type="button"
                    onClick={onConfirm}
                    disabled={isSubmitting}
                    className="flex items-center gap-2"
                >
                    <Check className="h-4 w-4" />
                    {isSubmitting ? "Saving..." : "Confirm & Save"}
                </Button>
            </div>
        </div>
    );
}
