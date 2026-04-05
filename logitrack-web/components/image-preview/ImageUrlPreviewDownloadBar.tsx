"use client";

import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Download, FolderArchive, Loader2 } from "lucide-react";
import { toast } from "sonner";
import { useLanguage } from "@/context/language";
import {
    downloadImagesAsZip,
    downloadSingleImageUrl,
    shouldSuggestCorsHint,
    type ZipImageEntryInput,
} from "@/lib/download-image-urls-zip";
import { looksLikeImageUrl } from "@/features/maintenance/utils/looksLikeImageUrl";

export type ImageUrlPreviewDownloadLabels = {
    downloadCurrent: string;
    downloadAllZip: string;
    downloadCurrentLoading: string;
};

export function ImageUrlPreviewDownloadBar({
    currentUrl,
    currentFilenameStem,
    zipEntries,
    zipFilename,
    labels,
}: {
    currentUrl: string;
    currentFilenameStem: string;
    zipEntries: ZipImageEntryInput[];
    zipFilename: string;
    labels: ImageUrlPreviewDownloadLabels;
}) {
    const { t } = useLanguage();
    const [singleLoading, setSingleLoading] = useState(false);
    const [zipLoading, setZipLoading] = useState(false);

    const showCurrent =
        Boolean(currentUrl?.trim()) &&
        looksLikeImageUrl(currentUrl) &&
        Boolean(currentFilenameStem?.trim());
    const showZip = zipEntries.length > 0;

    if (!showCurrent && !showZip) {
        return null;
    }

    const handleCurrent = async () => {
        if (!showCurrent || singleLoading) return;
        setSingleLoading(true);
        try {
            const result = await downloadSingleImageUrl(currentUrl, currentFilenameStem.trim());
            if (!result.ok) {
                toast.error(t("accounting.error.zipFailed"));
                if (shouldSuggestCorsHint([{ reason: result.reason }])) {
                    toast.message(t("accounting.error.corsHint"), { duration: 10_000 });
                }
            }
        } catch {
            toast.error(t("accounting.error.zipFailed"));
        } finally {
            setSingleLoading(false);
        }
    };

    const handleZip = async () => {
        if (zipEntries.length === 0 || zipLoading) return;
        setZipLoading(true);
        try {
            const result = await downloadImagesAsZip(zipFilename, zipEntries);
            if (result.added.length === 0) {
                toast.error(t("accounting.error.zipFailed"));
                if (shouldSuggestCorsHint(result.failed)) {
                    toast.message(t("accounting.error.corsHint"), { duration: 10_000 });
                }
                return;
            }
            if (result.failed.length > 0) {
                toast.warning(
                    t("accounting.error.partialDownload", {
                        failed: result.failed.length,
                        added: result.added.length,
                    })
                );
            }
        } catch {
            toast.error(t("accounting.error.zipFailed"));
        } finally {
            setZipLoading(false);
        }
    };

    return (
        <div className="shrink-0 flex flex-wrap items-center justify-center gap-2 border-t border-border bg-muted/15 px-3 py-2">
            {showCurrent ? (
                <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    disabled={singleLoading || zipLoading}
                    onClick={() => void handleCurrent()}
                >
                    {singleLoading ? (
                        <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                    ) : (
                        <Download className="mr-2 h-4 w-4" />
                    )}
                    {singleLoading ? labels.downloadCurrentLoading : labels.downloadCurrent}
                </Button>
            ) : null}
            {showZip ? (
                <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    disabled={zipLoading || singleLoading}
                    onClick={() => void handleZip()}
                >
                    {zipLoading ? (
                        <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                    ) : (
                        <FolderArchive className="mr-2 h-4 w-4" />
                    )}
                    {zipLoading ? t("accounting.preview.downloadZipLoading") : labels.downloadAllZip}
                </Button>
            ) : null}
        </div>
    );
}
