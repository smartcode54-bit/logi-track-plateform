"use client";

import { useEffect, useState } from "react";
import {
    Dialog,
    DialogContent,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { ExternalLink, Printer } from "lucide-react";
import { looksLikeImageUrl } from "@/features/maintenance/utils/looksLikeImageUrl";
import { printImageUrl } from "@/lib/print-image-url";
import {
    ImageUrlPreviewView,
    type ImageUrlPreviewLabels,
} from "@/components/image-preview/ImageUrlPreviewView";

export type MaintenancePreviewGallery = {
    urls: string[];
    startIndex: number;
} | null;

interface MaintenanceImagePreviewDialogProps {
    gallery: MaintenancePreviewGallery;
    onClose: () => void;
    title: string;
    openInNewTabLabel: string;
    zoomInLabel: string;
    zoomOutLabel: string;
    resetZoomLabel: string;
    prevLabel: string;
    nextLabel: string;
    notPreviewableLabel: string;
    printLabel: string;
}

export function MaintenanceImagePreviewDialog({
    gallery,
    onClose,
    title,
    openInNewTabLabel,
    zoomInLabel,
    zoomOutLabel,
    resetZoomLabel,
    prevLabel,
    nextLabel,
    notPreviewableLabel,
    printLabel,
}: MaintenanceImagePreviewDialogProps) {
    const open = !!gallery && gallery.urls.length > 0;
    const urls = gallery?.urls ?? [];
    const startIndex = gallery?.startIndex ?? 0;

    const [footerUrl, setFooterUrl] = useState("");

    useEffect(() => {
        if (!gallery || gallery.urls.length === 0) {
            setFooterUrl("");
            return;
        }
        const i = Math.max(0, Math.min(gallery.startIndex, gallery.urls.length - 1));
        setFooterUrl(gallery.urls[i] ?? "");
    }, [gallery]);

    const previewLabels: ImageUrlPreviewLabels = {
        zoomIn: zoomInLabel,
        zoomOut: zoomOutLabel,
        resetZoom: resetZoomLabel,
        prev: prevLabel,
        next: nextLabel,
        notPreviewable: notPreviewableLabel,
    };

    const isPrintableImage = Boolean(footerUrl && looksLikeImageUrl(footerUrl));

    return (
        <Dialog
            open={open}
            onOpenChange={(o) => {
                if (!o) onClose();
            }}
        >
            <DialogContent className="max-w-4xl max-h-[92vh] gap-0 overflow-hidden p-0 sm:rounded-lg">
                <DialogHeader className="border-b px-6 py-4 pr-12 text-left">
                    <DialogTitle>{title}</DialogTitle>
                </DialogHeader>

                <ImageUrlPreviewView
                    urls={urls}
                    startIndex={startIndex}
                    active={open}
                    labels={previewLabels}
                    isPreviewableImage={looksLikeImageUrl}
                    onCurrentUrlChange={setFooterUrl}
                />

                <DialogFooter className="flex flex-wrap gap-2 border-t px-6 py-4 sm:justify-end">
                    {footerUrl && isPrintableImage ? (
                        <Button
                            type="button"
                            variant="outline"
                            onClick={() => printImageUrl(footerUrl)}
                        >
                            <Printer className="mr-2 h-4 w-4" />
                            {printLabel}
                        </Button>
                    ) : null}
                    {footerUrl ? (
                        <Button variant="outline" asChild>
                            <a href={footerUrl} target="_blank" rel="noopener noreferrer">
                                <ExternalLink className="mr-2 h-4 w-4" />
                                {openInNewTabLabel}
                            </a>
                        </Button>
                    ) : null}
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
