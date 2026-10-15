"use client";

import { ChangeEvent, useState, useRef, useId } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Loader2, FileText, X } from "lucide-react";

import { truckUploadError, TRUCK_IMAGE_ACCEPT, TRUCK_DOCUMENT_ACCEPT, type TruckUploadPurpose } from "../../utils/uploads";
import { useLanguage } from "@/context/language";

interface TruckFileUploaderProps {
    label: string;
    purpose?: TruckUploadPurpose;
    /** Legacy storage folder (unused since T25: the save decides where the file goes). */
    folder?: string;
    onUploadComplete: (url: string) => void;
    /** Preview URLs of the current files; null is a stored file that cannot be read (shown as unavailable). */
    currentUrls?: Array<string | null>;
    onRemove?: (url: string | null) => void;
    multiple?: boolean;
    /**
     * Receives the picked file and its local `blob:` URL; the form uploads it when it is saved
     * (Go presign or Firebase Storage, by the `masterdata` flag, T25). Required: nothing uploads here.
     */
    onFileSelect: (file: File, blobUrl: string) => void;
    /** Kept by the form so MIME information survives wizard step remounts. */
    pendingFiles?: ReadonlyMap<string, File>;
}

export default function TruckFileUploader({ label, purpose = "truck_document", onUploadComplete, currentUrls = [], onRemove, onFileSelect, pendingFiles }: TruckFileUploaderProps) {
    const { t } = useLanguage();
    // Files upload with the form's save (T25); nothing is in flight here.
    const uploading = false;
    const [selectedFileName, setSelectedFileName] = useState<string | null>(null);
    const [fileError, setFileError] = useState<string | null>(null);
    const [fileTypes] = useState(() => new Map<string, string>());
    const inputId = useId();
    const inputRef = useRef<HTMLInputElement>(null);

    const isImageUrl = (url: string) => {
        try {
            const urlObj = new URL(url);
            const pathname = urlObj.pathname;
            return /\.(jpg|jpeg|png|webp|gif)$/i.test(pathname) ||
                /\.(jpg|jpeg|png|webp|gif)\?/.test(url) ||
                (url.startsWith("blob:") && (pendingFiles?.get(url)?.type ?? fileTypes.get(url))?.startsWith("image/"));
        } catch (e) {
            return false;
        }
    };

    const handleFileChange = async (e: ChangeEvent<HTMLInputElement>) => {
        if (!e.target.files || e.target.files.length === 0) return;

        const file = e.target.files[0];
        const error = truckUploadError(file, purpose);
        setFileError(error);
        e.target.value = "";
        if (error) return;
        setSelectedFileName(file.name);

        const blobUrl = URL.createObjectURL(file);
        fileTypes.set(blobUrl, file.type);
        onFileSelect(file, blobUrl);
        onUploadComplete(blobUrl);
        e.target.value = "";
    };

    const getFileNameFromUrl = (url: string) => {
        try {
            // Decode URL to get the path
            const decodedUrl = decodeURIComponent(url);
            // Extract the path from the URL (Firebase Storage usually has /o/Path%2To%2File)
            const fullPath = decodedUrl;

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

    const last = currentUrls.length > 0 ? currentUrls[currentUrls.length - 1] : undefined;
    const displayFileName = last === undefined
            ? t("trucks.upload.noFileChosen")
            : last === null
                ? t("masterData.files.unavailable")
                : selectedFileName || getFileNameFromUrl(last);



    return (
        <div className="space-y-2">
            <Label htmlFor={inputId}>{label}</Label>

            {/* Existing Files List */}
            {currentUrls.length > 0 && (
                <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-4 mb-2">
                    {currentUrls.map((url, index) => {
                        if (url === null) {
                            // A stored file without a readable object (missing at source): kept unless removed.
                            return (
                                <div key={index} className="relative group border rounded-md overflow-hidden bg-muted/30">
                                    <div className="flex flex-col items-center justify-center aspect-square w-full p-4 gap-2 text-muted-foreground">
                                        <FileText className="h-8 w-8" />
                                        <span className="text-xs text-center">{t("masterData.files.unavailable")}</span>
                                    </div>
                                    {onRemove && (
                                        <Button aria-label={t("common.removeFile")}
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
                        }
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
                                                className="w-full h-full object-cover"
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
                                    <Button aria-label={t("common.removeFile")}
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

            {fileError && <p role="alert" className="text-sm text-destructive">{t(fileError)}</p>}
            {/* Upload Input - Custom UI */}
            <div className="relative w-full h-auto">
                <button type="button" aria-label={label}
                    className="flex items-center gap-3 w-full h-12 px-3 rounded-md border border-input bg-background/50 hover:bg-accent/50 transition-colors cursor-pointer"
                    onClick={() => inputRef.current?.click()}
                >
                    <div className="bg-primary text-primary-foreground hover:bg-primary/90 py-1.5 px-4 rounded-md text-xs font-semibold transition-colors shrink-0">
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
                </button>

                <Input
                    ref={inputRef}
                    id={inputId}
                    aria-label={label}
                    type="file"
                    accept={purpose === "truck_photo" ? TRUCK_IMAGE_ACCEPT : TRUCK_DOCUMENT_ACCEPT}
                    onChange={handleFileChange}
                    disabled={uploading}
                    className="hidden"
                />
            </div>

            <p className="text-xs text-muted-foreground pt-1">
                {t(purpose === "truck_photo" ? "trucks.upload.imageOnly" : "trucks.upload.documentTypes")}
            </p>
        </div>
    );
}

