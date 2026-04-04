"use client";

import { useState } from "react";
import { Button } from "@/components/ui/button";
import { FolderArchive, Loader2, Printer } from "lucide-react";
import { toast } from "sonner";
import { useLanguage } from "@/context/language";
import { looksLikeImageUrl } from "@/features/maintenance/utils/looksLikeImageUrl";
import { printImageUrl } from "@/lib/print-image-url";
import {
    downloadImagesAsZip,
    shouldSuggestCorsHint,
    type ZipImageEntryInput,
} from "@/lib/download-image-urls-zip";

export function AccountingPreviewImageActions({
    printUrl,
    zipEntries,
    zipFilename,
    includeZipDownload = true,
}: {
    printUrl: string;
    zipEntries: ZipImageEntryInput[];
    zipFilename: string;
    /** Fuel/other use batch ZIP under dashboard; keep Print in dialog only. */
    includeZipDownload?: boolean;
}) {
    const { t } = useLanguage();
    const [zipping, setZipping] = useState(false);

    const showPrint = Boolean(printUrl?.trim()) && looksLikeImageUrl(printUrl);
    const showZip = includeZipDownload && zipEntries.length > 0;

    if (!showPrint && !showZip) {
        return null;
    }

    const handleZip = async () => {
        if (zipEntries.length === 0 || zipping) return;
        setZipping(true);
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
            setZipping(false);
        }
    };

    return (
        <div className="shrink-0 flex flex-wrap items-center justify-center gap-2 border-t border-border bg-muted/15 px-3 py-2">
            {showZip ? (
                <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    disabled={zipping}
                    onClick={() => void handleZip()}
                >
                    {zipping ? (
                        <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                    ) : (
                        <FolderArchive className="mr-2 h-4 w-4" />
                    )}
                    {zipping
                        ? t("accounting.preview.downloadZipLoading")
                        : t("accounting.preview.downloadZip")}
                </Button>
            ) : null}
            {showPrint ? (
                <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => printImageUrl(printUrl)}
                    disabled={zipping}
                >
                    <Printer className="mr-2 h-4 w-4" />
                    {t("accounting.preview.print")}
                </Button>
            ) : null}
        </div>
    );
}
