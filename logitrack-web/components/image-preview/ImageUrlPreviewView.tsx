"use client";

import { useEffect } from "react";
import { Button } from "@/components/ui/button";
import { ChevronLeft, ChevronRight, ZoomIn, ZoomOut, RotateCcw } from "lucide-react";
import { cn } from "@/lib/utils";
import { useImageUrlPreview } from "@/hooks/use-image-url-preview";

export interface ImageUrlPreviewLabels {
    zoomIn: string;
    zoomOut: string;
    resetZoom: string;
    prev: string;
    next: string;
    notPreviewable: string;
}

export interface ImageUrlPreviewViewProps {
    urls: string[];
    startIndex: number;
    active: boolean;
    labels: ImageUrlPreviewLabels;
    isPreviewableImage: (url: string) => boolean;
    /** Fires when the visible URL changes (slide navigation). */
    onCurrentUrlChange?: (url: string) => void;
    /** e.g. max-h-[min(70vh,640px)] */
    viewportClassName?: string;
    toolbarClassName?: string;
}

/**
 * Reusable zoom / pan / multi-url image preview (toolbar + stage). Wrap in your own modal or page.
 */
export function ImageUrlPreviewView({
    urls,
    startIndex,
    active,
    labels,
    isPreviewableImage,
    onCurrentUrlChange,
    viewportClassName,
    toolbarClassName,
}: ImageUrlPreviewViewProps) {
    const p = useImageUrlPreview({ urls, startIndex, active, isPreviewableImage });

    useEffect(() => {
        if (!active || !onCurrentUrlChange) return;
        onCurrentUrlChange(p.currentUrl);
    }, [active, onCurrentUrlChange, p.currentUrl]);

    if (!active || urls.length === 0) {
        return null;
    }

    return (
        <>
            {p.canNavigate ? (
                <p className="border-b px-4 py-2 text-sm font-normal text-muted-foreground sm:px-6">
                    {p.index + 1} / {urls.length}
                </p>
            ) : null}
            <div
                className={cn(
                    "flex flex-col gap-2 border-b bg-muted/30 px-2 py-2 sm:flex-row sm:items-center sm:justify-between",
                    toolbarClassName
                )}
            >
                <div className="flex flex-wrap items-center justify-center gap-1 sm:justify-start">
                    <Button
                        type="button"
                        variant="outline"
                        size="icon"
                        className="h-9 w-9"
                        disabled={!p.isImage || p.scale >= p.scaleMax - 0.01}
                        onClick={p.zoomIn}
                        title={labels.zoomIn}
                    >
                        <ZoomIn className="h-4 w-4" />
                        <span className="sr-only">{labels.zoomIn}</span>
                    </Button>
                    <Button
                        type="button"
                        variant="outline"
                        size="icon"
                        className="h-9 w-9"
                        disabled={!p.isImage || p.scale <= p.scaleMin + 0.01}
                        onClick={p.zoomOut}
                        title={labels.zoomOut}
                    >
                        <ZoomOut className="h-4 w-4" />
                        <span className="sr-only">{labels.zoomOut}</span>
                    </Button>
                    <Button
                        type="button"
                        variant="outline"
                        size="icon"
                        className="h-9 w-9"
                        disabled={
                            !p.isImage || (p.scale === 1 && p.pan.x === 0 && p.pan.y === 0)
                        }
                        onClick={p.resetZoomAndPan}
                        title={labels.resetZoom}
                    >
                        <RotateCcw className="h-4 w-4" />
                        <span className="sr-only">{labels.resetZoom}</span>
                    </Button>
                </div>
                {p.canNavigate ? (
                    <div className="flex items-center justify-center gap-1">
                        <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            className="gap-1"
                            onClick={p.goPrev}
                            title={labels.prev}
                        >
                            <ChevronLeft className="h-4 w-4" />
                            {labels.prev}
                        </Button>
                        <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            className="gap-1"
                            onClick={p.goNext}
                            title={labels.next}
                        >
                            {labels.next}
                            <ChevronRight className="h-4 w-4" />
                        </Button>
                    </div>
                ) : null}
            </div>

            <div
                ref={p.viewportRef}
                className={cn(
                    "relative min-h-[200px] max-h-[min(70vh,640px)] overflow-hidden bg-muted/20",
                    viewportClassName
                )}
                onTouchStart={(e) => {
                    p.touchStartX.current = e.touches[0]?.clientX ?? 0;
                }}
                onTouchEnd={(e) => {
                    if (p.scale > 1.02) return;
                    if (!p.canNavigate) return;
                    const endX = e.changedTouches[0]?.clientX ?? p.touchStartX.current;
                    const d = endX - p.touchStartX.current;
                    if (d > 56) p.goPrev();
                    else if (d < -56) p.goNext();
                }}
            >
                {p.canNavigate ? (
                    <>
                        <Button
                            type="button"
                            variant="secondary"
                            size="icon"
                            className={cn(
                                "absolute left-2 top-1/2 z-10 h-10 w-10 -translate-y-1/2 rounded-full shadow-md",
                                "hidden sm:inline-flex"
                            )}
                            onClick={p.goPrev}
                            aria-label={labels.prev}
                        >
                            <ChevronLeft className="h-5 w-5" />
                        </Button>
                        <Button
                            type="button"
                            variant="secondary"
                            size="icon"
                            className={cn(
                                "absolute right-2 top-1/2 z-10 h-10 w-10 -translate-y-1/2 rounded-full shadow-md",
                                "hidden sm:inline-flex"
                            )}
                            onClick={p.goNext}
                            aria-label={labels.next}
                        >
                            <ChevronRight className="h-5 w-5" />
                        </Button>
                    </>
                ) : null}

                <div
                    className={cn(
                        "flex min-h-[200px] items-center justify-center p-4",
                        p.isImage && p.scale > 1.02 && "cursor-grab active:cursor-grabbing touch-none"
                    )}
                    onPointerDown={(e) => {
                        if (!p.isImage || p.scale <= 1.02 || e.button !== 0) return;
                        e.currentTarget.setPointerCapture(e.pointerId);
                        p.setIsDragging(true);
                        p.dragRef.current = {
                            active: true,
                            sx: e.clientX,
                            sy: e.clientY,
                            px: p.pan.x,
                            py: p.pan.y,
                            pointerId: e.pointerId,
                        };
                    }}
                    onPointerMove={(e) => {
                        const d = p.dragRef.current;
                        if (!d?.active) return;
                        p.setPan({
                            x: d.px + e.clientX - d.sx,
                            y: d.py + e.clientY - d.sy,
                        });
                    }}
                    onPointerUp={(e) => {
                        const d = p.dragRef.current;
                        if (!d?.active || d.pointerId !== e.pointerId) return;
                        p.dragRef.current = null;
                        p.setIsDragging(false);
                        try {
                            e.currentTarget.releasePointerCapture(e.pointerId);
                        } catch {
                            /* ignore */
                        }
                    }}
                    onPointerCancel={(e) => {
                        const d = p.dragRef.current;
                        if (!d?.active || d.pointerId !== e.pointerId) return;
                        p.dragRef.current = null;
                        p.setIsDragging(false);
                    }}
                >
                    {p.currentUrl && p.isImage ? (
                        <div
                            className="inline-block max-w-full will-change-transform"
                            style={{
                                transform: `translate(${p.pan.x}px, ${p.pan.y}px) scale(${p.scale})`,
                                transformOrigin: "center center",
                                transition: p.isDragging ? "none" : "transform 0.12s ease-out",
                            }}
                        >
                            {/* eslint-disable-next-line @next/next/no-img-element */}
                            <img
                                src={p.currentUrl}
                                alt=""
                                className="max-h-[min(65vh,600px)] w-auto max-w-full object-contain select-none"
                                draggable={false}
                            />
                        </div>
                    ) : p.currentUrl ? (
                        <p className="px-4 text-center text-sm text-muted-foreground">
                            {labels.notPreviewable}
                        </p>
                    ) : null}
                </div>
            </div>
        </>
    );
}
