"use client";

import { useFormContext } from "react-hook-form";
import { useLanguage } from "@/context/language";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Camera, FileText } from "lucide-react";
import { TruckFormValues } from "@/validate/truckSchema";
import TruckFileUploader from "./TruckFileUploader";

interface PhotosSectionProps {
    onFileSelect?: (field: keyof TruckFormValues, file: File, blobUrl: string) => void;
}

export function PhotosSection({ onFileSelect }: PhotosSectionProps) {
    const { t } = useLanguage();
    const { watch, setValue, formState: { errors } } = useFormContext<TruckFormValues>();

    // Helper to handle single file upload
    const handleUpload = (field: keyof TruckFormValues) => (url: string) => {
        setValue(field, url, { shouldValidate: true, shouldDirty: true, shouldTouch: true });
    };

    const handleRemove = (field: keyof TruckFormValues) => () => {
        setValue(field, "", { shouldValidate: true, shouldDirty: true, shouldTouch: true });
    };

    return (
        <div className="space-y-6">
            {/* Truck Photos */}
            <Card>
                <CardHeader>
                    <CardTitle className="flex items-center gap-2">
                        <Camera className="h-5 w-5 text-primary" />
                        Truck Photos (Required)
                    </CardTitle>
                </CardHeader>
                <CardContent>
                    <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                        <div className="space-y-2">
                            <TruckFileUploader
                                label="Front-Right View *"
                                folder="photos/front-right"
                                currentUrls={watch("imageFrontRight") ? [watch("imageFrontRight") as string] : []}
                                onUploadComplete={handleUpload("imageFrontRight")}
                                onRemove={handleRemove("imageFrontRight")}
                                onFileSelect={onFileSelect ? ((file, blobUrl) => onFileSelect("imageFrontRight", file, blobUrl)) : undefined}
                            />
                            {errors.imageFrontRight && <p className="text-sm text-destructive">{errors.imageFrontRight.message}</p>}
                        </div>
                        <div className="space-y-2">
                            <TruckFileUploader
                                label="Front-Left View *"
                                folder="photos/front-left"
                                currentUrls={watch("imageFrontLeft") ? [watch("imageFrontLeft") as string] : []}
                                onUploadComplete={handleUpload("imageFrontLeft")}
                                onRemove={handleRemove("imageFrontLeft")}
                                onFileSelect={onFileSelect ? ((file, blobUrl) => onFileSelect("imageFrontLeft", file, blobUrl)) : undefined}
                            />
                            {errors.imageFrontLeft && <p className="text-sm text-destructive">{errors.imageFrontLeft.message}</p>}
                        </div>
                        <div className="space-y-2">
                            <TruckFileUploader
                                label="Back-Right View *"
                                folder="photos/back-right"
                                currentUrls={watch("imageBackRight") ? [watch("imageBackRight") as string] : []}
                                onUploadComplete={handleUpload("imageBackRight")}
                                onRemove={handleRemove("imageBackRight")}
                                onFileSelect={onFileSelect ? ((file, blobUrl) => onFileSelect("imageBackRight", file, blobUrl)) : undefined}
                            />
                            {errors.imageBackRight && <p className="text-sm text-destructive">{errors.imageBackRight.message}</p>}
                        </div>
                        <div className="space-y-2">
                            <TruckFileUploader
                                label="Back-Left View *"
                                folder="photos/back-left"
                                currentUrls={watch("imageBackLeft") ? [watch("imageBackLeft") as string] : []}
                                onUploadComplete={handleUpload("imageBackLeft")}
                                onRemove={handleRemove("imageBackLeft")}
                                onFileSelect={onFileSelect ? ((file, blobUrl) => onFileSelect("imageBackLeft", file, blobUrl)) : undefined}
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
                        Truck Documents (Required)
                    </CardTitle>
                </CardHeader>
                <CardContent>
                    <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                        <div className="space-y-2">
                            <TruckFileUploader
                                label="Tax Document *"
                                folder="documents/tax"
                                currentUrls={watch("documentTax") ? [watch("documentTax") as string] : []}
                                onUploadComplete={handleUpload("documentTax")}
                                onRemove={handleRemove("documentTax")}
                                onFileSelect={onFileSelect ? ((file, blobUrl) => onFileSelect("documentTax", file, blobUrl)) : undefined}
                            />
                            {errors.documentTax && <p className="text-sm text-destructive">{errors.documentTax.message}</p>}
                        </div>
                        <div className="space-y-2">
                            <TruckFileUploader
                                label="Truck Register Document *"
                                folder="documents/register"
                                currentUrls={watch("documentRegister") ? [watch("documentRegister") as string] : []}
                                onUploadComplete={handleUpload("documentRegister")}
                                onRemove={handleRemove("documentRegister")}
                                onFileSelect={onFileSelect ? ((file, blobUrl) => onFileSelect("documentRegister", file, blobUrl)) : undefined}
                            />
                            {errors.documentRegister && <p className="text-sm text-destructive">{errors.documentRegister.message}</p>}
                        </div>
                    </div>
                </CardContent>
            </Card>
        </div>
    );
}
