"use client";

import { ChangeEvent, useState, useRef } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Loader2, Upload, FileText, X } from "lucide-react";
import { uploadTruckFile } from "@/features/trucks/services/truckService";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";

interface TruckFileUploaderProps {
    label: string;
    folder: string; // e.g., "photos" or "documents"
    onUploadComplete: (url: string) => void;
    currentUrls?: string[];
    onRemove?: (url: string) => void;
    multiple?: boolean;
    onFileSelect?: (file: File, blobUrl: string) => void;
}

export default function TruckFileUploader({ label, folder, onUploadComplete, currentUrls = [], onRemove, multiple = false, onFileSelect }: TruckFileUploaderProps) {
    const { t } = useLanguage();
    const [uploading, setUploading] = useState(false);
    const [selectedFileName, setSelectedFileName] = useState<string | null>(null);
    const inputRef = useRef<HTMLInputElement>(null);

    const isImageUrl = (url: string) => {
        try {
            const urlObj = new URL(url);
            const pathname = urlObj.pathname;
            return /\.(jpg|jpeg|png|webp|gif)$/i.test(pathname) ||
                /\.(jpg|jpeg|png|webp|gif)\?/.test(url) ||
                url.startsWith("blob:");
        } catch (e) {
            return false;
        }
    };

    const handleFileChange = async (e: ChangeEvent<HTMLInputElement>) => {
        if (!e.target.files || e.target.files.length === 0) return;

        const file = e.target.files[0];
        setSelectedFileName(file.name);

        // If onFileSelect is provided, we skip immediate upload
        if (onFileSelect) {
            const blobUrl = URL.createObjectURL(file);
            onFileSelect(file, blobUrl);
            onUploadComplete(blobUrl);
            e.target.value = "";
            return;
        }

        try {
            setUploading(true);
            const path = `trucks/${folder}/${Date.now()}_${file.name}`;
            const url = await uploadTruckFile(file, path);
            onUploadComplete(url);
            toast.success(t("trucks.upload.success"));

            e.target.value = "";
        } catch (error) {
            console.error(error);
            toast.error(t("trucks.upload.error"));
        } finally {
            setUploading(false);
        }
    };

    const getFileNameFromUrl = (url: string) => {
        try {
            // Decode URL to get the path
            const decodedUrl = decodeURIComponent(url);
            // Extract the path from the URL (Firebase Storage usually has /o/Path%2To%2File)
            const pathStartIndex = decodedUrl.indexOf("/o/") + 3;
            const pathEndIndex = decodedUrl.indexOf("?");
            let fullPath = decodedUrl;

            // Get just the filename
            const basename = fullPath.split('/').pop() || fullPath;

            // Try to remove the timestamp prefix we added (format: timestamp_filename)
            const parts = basename.split('_');
            if (parts.length > 1 && !isNaN(Number(parts[0]))) {
                return parts.slice(1).join('_');
            }
            return basename;
        } catch (e) {
            return url;
        }
    };

    const displayFileName = selectedFileName
        ? selectedFileName
        : (currentUrls.length > 0
            ? getFileNameFromUrl(currentUrls[currentUrls.length - 1])
            : t("trucks.upload.noFileChosen"));



    return (
        <div className="space-y-2">
            <Label>{label}</Label>

            {/* Existing Files List */}
            {currentUrls.length > 0 && (
                <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-4 mb-2">
                    {currentUrls.map((url, index) => {
                        const isImage = isImageUrl(url);
                        return (
                            <div key={index} className="relative group border rounded-md overflow-hidden bg-muted/30">
                                <a href={url} target="_blank" rel="noopener noreferrer" className="block aspect-square w-full">
                                    {isImage ? (
                                        <div className="relative w-full h-full">
                                            {/* eslint-disable-next-line @next/next/no-img-element */}
                                            <img
                                                src={url}
                                                alt={`${t("trucks.upload.document")} ${index + 1}`}
                                                className="w-full h-full object-cover transition-transform hover:scale-105"
                                            />
                                        </div>
                                    ) : (
                                        <div className="flex flex-col items-center justify-center w-full h-full p-4 gap-2 text-muted-foreground hover:text-foreground hover:bg-muted/50 transition-colors">
                                            <FileText className="h-8 w-8" />
                                            <span className="text-xs text-center truncate w-full px-2">{t("trucks.upload.document")} {index + 1}</span>
                                        </div>
                                    )}
                                </a>
                                {onRemove && (
                                    <Button
                                        variant="destructive"
                                        size="icon"
                                        type="button"
                                        onClick={(e) => {
                                            e.preventDefault();
                                            onRemove(url);
                                        }}
                                        className="absolute top-1 right-1 h-6 w-6 opacity-0 group-hover:opacity-100 transition-opacity"
                                    >
                                        <X className="h-3 w-3" />
                                    </Button>
                                )}
                            </div>
                        );
                    })}
                </div>
            )}

            {/* Upload Input - Custom UI */}
            <div className="relative w-full h-auto">
                <div
                    className="flex items-center gap-3 w-full h-12 px-3 rounded-md border border-input bg-background/50 hover:bg-accent/50 transition-colors cursor-pointer"
                    onClick={() => inputRef.current?.click()}
                >
                    <div className="bg-primary text-primary-foreground hover:bg-primary/90 py-1.5 px-4 rounded-full text-xs font-semibold transition-colors shrink-0">
                        {t("trucks.upload.chooseFile")}
                    </div>
                    <span className="text-sm text-muted-foreground truncate flex-1">
                        {displayFileName}
                    </span>
                    {uploading && (
                        <div className="ml-auto flex items-center gap-2 text-sm text-muted-foreground">
                            <Loader2 className="h-4 w-4 animate-spin" />
                            <span className="hidden sm:inline">{t("trucks.upload.uploading")}</span>
                        </div>
                    )}
                </div>

                <Input
                    ref={inputRef}
                    type="file"
                    accept="image/*,application/pdf"
                    onChange={handleFileChange}
                    disabled={uploading}
                    className="hidden"
                />
            </div>

            <p className="text-xs text-muted-foreground pt-1">
                {t("trucks.upload.acceptedFormats")}
            </p>
        </div>
    );
}

