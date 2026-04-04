"use client";

import { useEffect } from "react";
import { Button } from "@/components/ui/button";
import { ChevronLeft, ChevronRight, ZoomIn, ZoomOut, RotateCcw } from "lucide-react";
import { cn } from "@/lib/utils";
import { useImageUrlPreview } from "@/hooks/use-image-url-preview";
import {
    IMAGE_PREVIEW_IMG_MAX_CLASS,
    IMAGE_PREVIEW_SCALE_UI_EPSILON,
    IMAGE_PREVIEW_STAGE_MIN_H_CLASS,
    IMAGE_PREVIEW_TOUCH_SWIPE_MIN_PX,
    IMAGE_PREVIEW_VIEWPORT_BASE_CLASS,
    IMAGE_PREVIEW_ZOOMED_NAV_THRESHOLD,
} from "@/components/image-preview/image-preview-constants";

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
    const {
        touchStartXRef,
        dragRef,
        viewportRef,
        index,
        scale,
        pan,
        isDragging,
        currentUrl,
        canNavigate,
        isImage,
        goPrev,
        goNext,
        zoomIn,
        zoomOut,
        resetZoomAndPan,
        setPan,
        setIsDragging,
        scaleMin,
        scaleMax,
    } = useImageUrlPreview({ urls, startIndex, active, isPreviewableImage });

    useEffect(() => {
        if (!active || !onCurrentUrlChange) return;
        onCurrentUrlChange(currentUrl);
    }, [active, onCurrentUrlChange, currentUrl]);

    if (!active || urls.length === 0) {
        return null;
    }

    return (
        <>
            {canNavigate ? (
                <p className="border-b px-4 py-2 text-sm font-normal text-muted-foreground sm:px-6">
                    {index + 1} / {urls.length}
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
                        disabled={
                            !isImage || scale >= scaleMax - IMAGE_PREVIEW_SCALE_UI_EPSILON
                        }
                        onClick={zoomIn}
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
                        disabled={
                            !isImage || scale <= scaleMin + IMAGE_PREVIEW_SCALE_UI_EPSILON
                        }
                        onClick={zoomOut}
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
                            !isImage || (scale === 1 && pan.x === 0 && pan.y === 0)
                        }
                        onClick={resetZoomAndPan}
                        title={labels.resetZoom}
                    >
                        <RotateCcw className="h-4 w-4" />
                        <span className="sr-only">{labels.resetZoom}</span>
                    </Button>
                </div>
                {canNavigate ? (
                    <div className="flex items-center justify-center gap-1">
                        <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            className="gap-1"
                            onClick={goPrev}
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
                            onClick={goNext}
                            title={labels.next}
                        >
                            {labels.next}
                            <ChevronRight className="h-4 w-4" />
                        </Button>
                    </div>
                ) : null}
            </div>

            <div
                ref={viewportRef}
                className={cn(IMAGE_PREVIEW_VIEWPORT_BASE_CLASS, viewportClassName)}
                onTouchStart={(e) => {
                    touchStartXRef.current = e.touches[0]?.clientX ?? 0;
                }}
                onTouchEnd={(e) => {
                    if (scale > IMAGE_PREVIEW_ZOOMED_NAV_THRESHOLD) return;
                    if (!canNavigate) return;
                    const endX = e.changedTouches[0]?.clientX ?? touchStartXRef.current;
                    const d = endX - touchStartXRef.current;
                    if (d > IMAGE_PREVIEW_TOUCH_SWIPE_MIN_PX) goPrev();
                    else if (d < -IMAGE_PREVIEW_TOUCH_SWIPE_MIN_PX) goNext();
                }}
            >
                {canNavigate ? (
                    <>
                        <Button
                            type="button"
                            variant="secondary"
                            size="icon"
                            className={cn(
                                "absolute left-2 top-1/2 z-10 h-10 w-10 -translate-y-1/2 rounded-full shadow-md",
                                "hidden sm:inline-flex"
                            )}
                            onClick={goPrev}
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
                            onClick={goNext}
                            aria-label={labels.next}
                        >
                            <ChevronRight className="h-5 w-5" />
                        </Button>
                    </>
                ) : null}

                <div
                    className={cn(
                        "flex items-center justify-center p-4",
                        IMAGE_PREVIEW_STAGE_MIN_H_CLASS,
                        isImage &&
                            scale > IMAGE_PREVIEW_ZOOMED_NAV_THRESHOLD &&
                            "cursor-grab active:cursor-grabbing touch-none"
                    )}
                    onPointerDown={(e) => {
                        if (
                            !isImage ||
                            scale <= IMAGE_PREVIEW_ZOOMED_NAV_THRESHOLD ||
                            e.button !== 0
                        )
                            return;
                        e.currentTarget.setPointerCapture(e.pointerId);
                        setIsDragging(true);
                        dragRef.current = {
                            active: true,
                            sx: e.clientX,
                            sy: e.clientY,
                            px: pan.x,
                            py: pan.y,
                            pointerId: e.pointerId,
                        };
                    }}
                    onPointerMove={(e) => {
                        const d = dragRef.current;
                        if (!d?.active) return;
                        setPan({
                            x: d.px + e.clientX - d.sx,
                            y: d.py + e.clientY - d.sy,
                        });
                    }}
                    onPointerUp={(e) => {
                        const d = dragRef.current;
                        if (!d?.active || d.pointerId !== e.pointerId) return;
                        dragRef.current = null;
                        setIsDragging(false);
                        try {
                            e.currentTarget.releasePointerCapture(e.pointerId);
                        } catch {
                            /* ignore */
                        }
                    }}
                    onPointerCancel={(e) => {
                        const d = dragRef.current;
                        if (!d?.active || d.pointerId !== e.pointerId) return;
                        dragRef.current = null;
                        setIsDragging(false);
                    }}
                >
                    {currentUrl && isImage ? (
                        <div
                            className="inline-block max-w-full will-change-transform"
                            style={{
                                transform: `translate(${pan.x}px, ${pan.y}px) scale(${scale})`,
                                transformOrigin: "center center",
                                transition: isDragging ? "none" : "transform 0.12s ease-out",
                            }}
                        >
                            {/* eslint-disable-next-line @next/next/no-img-element */}
                            <img
                                src={currentUrl}
                                alt=""
                                className={IMAGE_PREVIEW_IMG_MAX_CLASS}
                                draggable={false}
                            />
                        </div>
                    ) : currentUrl ? (
                        <p className="px-4 text-center text-sm text-muted-foreground">
                            {labels.notPreviewable}
                        </p>
                    ) : null}
                </div>
            </div>
        </>
    );
}
