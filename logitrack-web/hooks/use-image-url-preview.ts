"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import {
    IMAGE_PREVIEW_SCALE_MAX,
    IMAGE_PREVIEW_SCALE_MIN,
    IMAGE_PREVIEW_SCALE_STEP,
} from "@/components/image-preview/image-preview-constants";

export interface UseImageUrlPreviewOptions {
    urls: string[];
    startIndex: number;
    /** e.g. dialog open — when false, keyboard/touch helpers stand down */
    active: boolean;
    isPreviewableImage: (url: string) => boolean;
}

export function useImageUrlPreview({
    urls,
    startIndex,
    active,
    isPreviewableImage,
}: UseImageUrlPreviewOptions) {
    const [index, setIndex] = useState(0);
    const [scale, setScale] = useState(1);
    const [pan, setPan] = useState({ x: 0, y: 0 });
    const [isDragging, setIsDragging] = useState(false);
    const viewportRef = useRef<HTMLDivElement>(null);
    const touchStartX = useRef(0);
    const dragRef = useRef<{
        active: boolean;
        sx: number;
        sy: number;
        px: number;
        py: number;
        pointerId: number;
    } | null>(null);

    const resetZoomAndPan = useCallback(() => {
        setScale(1);
        setPan({ x: 0, y: 0 });
    }, []);

    const urlsKey = urls.join("\0");
    useEffect(() => {
        if (!active || urls.length === 0) return;
        const i = Math.max(0, Math.min(startIndex, urls.length - 1));
        setIndex(i);
        resetZoomAndPan();
    }, [active, startIndex, urls.length, urlsKey, resetZoomAndPan]);

    const currentUrl = urls[index] ?? "";
    const canNavigate = urls.length > 1;
    const isImage = Boolean(currentUrl && isPreviewableImage(currentUrl));

    const goPrev = useCallback(() => {
        if (urls.length <= 1) return;
        setIndex((i) => (i <= 0 ? urls.length - 1 : i - 1));
        resetZoomAndPan();
    }, [urls.length, resetZoomAndPan]);

    const goNext = useCallback(() => {
        if (urls.length <= 1) return;
        setIndex((i) => (i >= urls.length - 1 ? 0 : i + 1));
        resetZoomAndPan();
    }, [urls.length, resetZoomAndPan]);

    useEffect(() => {
        if (!active || urls.length <= 1) return;
        const onKey = (e: KeyboardEvent) => {
            if (e.key === "ArrowLeft") {
                e.preventDefault();
                goPrev();
            } else if (e.key === "ArrowRight") {
                e.preventDefault();
                goNext();
            }
        };
        window.addEventListener("keydown", onKey);
        return () => window.removeEventListener("keydown", onKey);
    }, [active, urls.length, goPrev, goNext]);

    useEffect(() => {
        if (scale <= 1.001) setPan({ x: 0, y: 0 });
    }, [scale]);

    useEffect(() => {
        if (!active) setIsDragging(false);
    }, [active]);

    useEffect(() => {
        const el = viewportRef.current;
        if (!el || !active || !isImage) return;
        const onWheel = (e: WheelEvent) => {
            if (!(e.ctrlKey || e.metaKey)) return;
            e.preventDefault();
            e.stopPropagation();
            const delta = -e.deltaY;
            const factor = delta > 0 ? 1.09 : 1 / 1.09;
            setScale((s) =>
                Math.min(IMAGE_PREVIEW_SCALE_MAX, Math.max(IMAGE_PREVIEW_SCALE_MIN, s * factor))
            );
        };
        el.addEventListener("wheel", onWheel, { passive: false });
        return () => el.removeEventListener("wheel", onWheel);
    }, [active, isImage, currentUrl]);

    const zoomIn = useCallback(() => {
        setScale((s) => Math.min(IMAGE_PREVIEW_SCALE_MAX, s * IMAGE_PREVIEW_SCALE_STEP));
    }, []);

    const zoomOut = useCallback(() => {
        setScale((s) => Math.max(IMAGE_PREVIEW_SCALE_MIN, s / IMAGE_PREVIEW_SCALE_STEP));
    }, []);

    return {
        index,
        scale,
        pan,
        isDragging,
        viewportRef,
        touchStartX,
        currentUrl,
        canNavigate,
        isImage,
        resetZoomAndPan,
        goPrev,
        goNext,
        zoomIn,
        zoomOut,
        setScale,
        setPan,
        setIsDragging,
        dragRef,
        scaleMin: IMAGE_PREVIEW_SCALE_MIN,
        scaleMax: IMAGE_PREVIEW_SCALE_MAX,
        scaleStep: IMAGE_PREVIEW_SCALE_STEP,
    };
}
