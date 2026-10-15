"use client";

import { ChangeEvent, useId, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Loader2, FileText, X } from "lucide-react";
import { toast } from "sonner";
import { useLanguage } from "@/context/language";
import { uploadContentType } from "@/lib/goUpload";

interface FileUploaderProps {
    label: string;
    upload: (file: File) => Promise<{ ref: string; previewUrl: string; contentType?: string }>;
    onUploadComplete: (ref: string, previewUrl: string, contentType: string) => void;
    onUploadPendingChange?: (pending: boolean) => void;
    currentUrls?: string[];
    previews?: Record<string, string | null>;
    contentTypes?: Record<string, string>;
    onRemove?: (ref: string) => void;
    disabled?: boolean;
}

function previewHref(value: string | null | undefined): string | undefined {
    if (!value) return undefined;
    try {
        const url = new URL(value);
        return ["https:", "http:", "blob:"].includes(url.protocol) ? value : undefined;
    } catch {
        return undefined;
    }
}

export default function FileUploader({ label, upload, onUploadComplete, onUploadPendingChange, currentUrls = [], previews = {}, contentTypes = {}, onRemove, disabled = false }: FileUploaderProps) {
    const inputId = useId();
    const [uploading, setUploading] = useState(false);
    const [uploadedTypes, setUploadedTypes] = useState<Record<string, string>>({});
    const { t } = useLanguage();

    const handleFileChange = async (e: ChangeEvent<HTMLInputElement>) => {
        const input = e.currentTarget;
        const file = input.files?.[0];
        if (!file || uploading || disabled) return;
        setUploading(true);
        onUploadPendingChange?.(true);
        try {
            const { ref, previewUrl, contentType = uploadContentType(file) } = await upload(file);
            setUploadedTypes(types => ({ ...types, [ref]: contentType }));
            onUploadComplete(ref, previewUrl, contentType);
            toast.success(t("subcontractors.toast.uploadSuccess"));
        } catch (error) {
            console.error(error);
            toast.error(t("subcontractors.toast.uploadError"));
        } finally {
            input.value = "";
            setUploading(false);
            onUploadPendingChange?.(false);
        }
    };

    return (
        <div className="space-y-4">
            <Label htmlFor={inputId}>{label}</Label>
            {currentUrls.length > 0 && (
                <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-4">
                    {currentUrls.map((ref, index) => {
                        const url = previewHref(Object.hasOwn(previews, ref) ? previews[ref] : ref);
                        const contentType = contentTypes[ref] ?? uploadedTypes[ref];
                        const isImage = url && (contentType ? contentType.startsWith("image/")
                            : /\.(jpg|jpeg|png|webp|gif)$/i.test(new URL(url).pathname) || /\.(jpg|jpeg|png|webp|gif)$/i.test(ref));
                        const content = isImage ? (
                            // eslint-disable-next-line @next/next/no-img-element
                            <img src={url} alt={`${t("subcontractors.file.document")} ${index + 1}`} className="h-full w-full object-cover" />
                        ) : (
                            <div className="flex h-full w-full flex-col items-center justify-center gap-2 p-4 text-muted-foreground">
                                <FileText className="h-8 w-8" aria-hidden />
                                <span className="w-full text-center text-xs">{t("subcontractors.file.document")} {index + 1}</span>
                                {!url && <span className="text-center text-xs">{t("subcontractors.ui4.documentUnavailable")}</span>}
                            </div>
                        );
                        return (
                            <div key={ref} className="group relative overflow-hidden rounded-md border bg-muted/30">
                                {url ? (
                                    <a href={url} target="_blank" rel="noopener noreferrer" aria-label={t("subcontractors.ui4.openDocumentNumber", { number: index + 1 })} className="block aspect-square w-full hover:bg-muted/50">
                                        {content}
                                    </a>
                                ) : <div className="aspect-square w-full">{content}</div>}
                                {onRemove && (
                                    <Button aria-label={t("common.removeFile")} variant="destructive" size="icon" type="button" disabled={disabled || uploading}
                                        onClick={() => onRemove(ref)}
                                        className="absolute right-1 top-1 max-md:size-11 md:opacity-0 md:group-hover:opacity-100 md:group-focus-within:opacity-100">
                                        <X className="h-4 w-4" aria-hidden />
                                    </Button>
                                )}
                            </div>
                        );
                    })}
                </div>
            )}
            <div className="flex items-center gap-2">
                <Input id={inputId} type="file" accept="image/jpeg,image/png,image/webp,application/pdf" onChange={handleFileChange}
                    disabled={uploading || disabled} aria-describedby={`${inputId}-formats`} className="max-w-xs" />
                {uploading && <span role="status" className="flex items-center gap-2 text-sm text-muted-foreground">
                    <Loader2 className="h-4 w-4 animate-spin" aria-hidden />{t("subcontractors.file.uploading")}
                </span>}
            </div>
            <p id={`${inputId}-formats`} className="text-xs text-muted-foreground">{t("subcontractors.file.acceptedFormats")}</p>
        </div>
    );
}
