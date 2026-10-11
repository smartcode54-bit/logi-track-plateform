"use client";

import { useFormContext } from "react-hook-form";
import { useLanguage } from "@/context/language";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Camera, FileText } from "lucide-react";
import { TruckFormValues } from "@/validate/truckSchema";
import TruckFileUploader from "./TruckFileUploader";

interface PhotosSectionProps {
    onFileSelect: (field: keyof TruckFormValues, file: File, blobUrl: string) => void;
    /**
     * The preview URL of a field's value (a stored file's ref, or a picked file's `blob:` URL); null
     * when the stored file cannot be read (shown as unavailable, still kept). Default: the value itself.
     */
    previewOf?: (ref: string) => string | null;
}

const identity = (ref: string) => ref;

export function PhotosSection({ onFileSelect, previewOf = identity }: PhotosSectionProps) {
    const { t } = useLanguage();
    const { watch, setValue, formState: { errors } } = useFormContext<TruckFormValues>();

    // Helper to handle single file upload
    const handleUpload = (field: keyof TruckFormValues) => (url: string) => {
        setValue(field, url, { shouldValidate: true, shouldDirty: true, shouldTouch: true });
    };

    const handleRemove = (field: keyof TruckFormValues) => () => {
        setValue(field, "", { shouldValidate: true, shouldDirty: true, shouldTouch: true });
    };

    const current = (field: keyof TruckFormValues): Array<string | null> => {
        const ref = watch(field) as string | undefined;
        return ref ? [previewOf(ref)] : [];
    };

    return (
        <div className="space-y-6">
            {/* Truck Photos */}
            <Card>
                <CardHeader>
                    <CardTitle className="flex items-center gap-2">
                        <Camera className="h-5 w-5 text-primary" />
                        {t("trucks.section.photos")}
                    </CardTitle>
                </CardHeader>
                <CardContent>
                    <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                        <div className="space-y-2">
                            <TruckFileUploader
                                label={t("trucks.photos.frontRight") + " *"}
                                folder="photos/front-right"
                                currentUrls={current("imageFrontRight")}
                                onUploadComplete={handleUpload("imageFrontRight")}
                                onRemove={handleRemove("imageFrontRight")}
                                onFileSelect={(file, blobUrl) => onFileSelect("imageFrontRight", file, blobUrl)}
                            />
                            {errors.imageFrontRight && <p className="text-sm text-destructive">{errors.imageFrontRight.message}</p>}
                        </div>
                        <div className="space-y-2">
                            <TruckFileUploader
                                label={t("trucks.photos.frontLeft") + " *"}
                                folder="photos/front-left"
                                currentUrls={current("imageFrontLeft")}
                                onUploadComplete={handleUpload("imageFrontLeft")}
                                onRemove={handleRemove("imageFrontLeft")}
                                onFileSelect={(file, blobUrl) => onFileSelect("imageFrontLeft", file, blobUrl)}
                            />
                            {errors.imageFrontLeft && <p className="text-sm text-destructive">{errors.imageFrontLeft.message}</p>}
                        </div>
                        <div className="space-y-2">
                            <TruckFileUploader
                                label={t("trucks.photos.backRight") + " *"}
                                folder="photos/back-right"
                                currentUrls={current("imageBackRight")}
                                onUploadComplete={handleUpload("imageBackRight")}
                                onRemove={handleRemove("imageBackRight")}
                                onFileSelect={(file, blobUrl) => onFileSelect("imageBackRight", file, blobUrl)}
                            />
                            {errors.imageBackRight && <p className="text-sm text-destructive">{errors.imageBackRight.message}</p>}
                        </div>
                        <div className="space-y-2">
                            <TruckFileUploader
                                label={t("trucks.photos.backLeft") + " *"}
                                folder="photos/back-left"
                                currentUrls={current("imageBackLeft")}
                                onUploadComplete={handleUpload("imageBackLeft")}
                                onRemove={handleRemove("imageBackLeft")}
                                onFileSelect={(file, blobUrl) => onFileSelect("imageBackLeft", file, blobUrl)}
                            />
                            {errors.imageBackLeft && <p className="text-sm text-destructive">{errors.imageBackLeft.message}</p>}
                        </div>
                    </div>
                </CardContent>
            </Card>

            {/* Truck Documents */}
            <Card>
                <CardHeader>
                    <CardTitle className="flex items-center gap-2">
                        <FileText className="h-5 w-5 text-primary" />
                        {t("trucks.section.documents")}
                    </CardTitle>
                </CardHeader>
                <CardContent>
                    <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                        <div className="space-y-2">
                            <TruckFileUploader
                                label={t("trucks.documents.tax") + " *"}
                                folder="documents/tax"
                                currentUrls={current("documentTax")}
                                onUploadComplete={handleUpload("documentTax")}
                                onRemove={handleRemove("documentTax")}
                                onFileSelect={(file, blobUrl) => onFileSelect("documentTax", file, blobUrl)}
                            />
                            {errors.documentTax && <p className="text-sm text-destructive">{errors.documentTax.message}</p>}
                        </div>
                        <div className="space-y-2">
                            <TruckFileUploader
                                label={t("trucks.documents.register") + " *"}
                                folder="documents/register"
                                currentUrls={current("documentRegister")}
                                onUploadComplete={handleUpload("documentRegister")}
                                onRemove={handleRemove("documentRegister")}
                                onFileSelect={(file, blobUrl) => onFileSelect("documentRegister", file, blobUrl)}
                            />
                            {errors.documentRegister && <p className="text-sm text-destructive">{errors.documentRegister.message}</p>}
                        </div>
                    </div>
                </CardContent>
            </Card>
        </div>
    );
}
