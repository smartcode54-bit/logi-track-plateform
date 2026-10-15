"use client";

import { useCallback, useEffect, useState, useRef, useId } from "react";
import { Button } from "@/components/ui/button";
import { X, ZoomIn, ZoomOut, ChevronLeft, ChevronRight, RotateCcw, Download } from "lucide-react";
import { useLanguage } from "@/context/language";

interface FileViewerProps {
    isOpen: boolean;
    onClose: () => void;
    files: { url: string; type: "image" | "pdf"; label: string; contentType?: string }[];
    initialIndex?: number;
}

export function FileViewer({ isOpen, onClose, files, initialIndex = 0 }: FileViewerProps) {
    const { t } = useLanguage();
    const dialog = useRef<HTMLDialogElement>(null);
    const titleId = useId();
    useEffect(() => {
        const node = dialog.current;
        if (!node || !isOpen) return;
        node.showModal?.();
        // jsdom has no modal-dialog methods; the open attribute still exposes the same controls.
        if (!node.showModal) node.setAttribute("open", "");
        return () => { node.close?.(); };
    }, [isOpen]);
    const [currentIndex, setCurrentIndex] = useState(initialIndex);
    const [scale, setScale] = useState(1);
    const [position, setPosition] = useState({ x: 0, y: 0 });
    const [drag, setDrag] = useState<{ x: number; y: number } | null>(null);
    const currentFile = files[Math.min(currentIndex, files.length - 1)];
    // Old callers labelled all documents PDF. The actual MIME or content extension takes precedence.
    const isImage = currentFile && (currentFile.contentType ? currentFile.contentType.startsWith("image/")
        : /\.(png|jpe?g|webp)(?:[?#]|$)/i.test(currentFile.url) || currentFile.type === "image");
    const resetView = useCallback(() => { setScale(1); setPosition({ x: 0, y: 0 }); setDrag(null); }, []);
    const navigate = useCallback((delta: number) => {
        setCurrentIndex((index) => Math.max(0, Math.min(files.length - 1, index + delta)));
        resetView();
    }, [files.length, resetView]);

    useEffect(() => {
        if (isOpen) { setCurrentIndex(initialIndex); resetView(); }
    }, [isOpen, initialIndex, resetView]);

    useEffect(() => {
        if (!isOpen) return;
        const onKey = (event: KeyboardEvent) => {
            if (event.key === "ArrowRight") navigate(1);
            if (event.key === "ArrowLeft") navigate(-1);
            if (!isImage) return;
            if (["+", "="].includes(event.key)) setScale((value) => Math.min(4, value + 0.5));
            if (event.key === "-") setScale((value) => Math.max(0.5, value - 0.5));
            if (event.key === "0") resetView();
        };
        window.addEventListener("keydown", onKey);
        return () => window.removeEventListener("keydown", onKey);
    }, [isOpen, isImage, navigate, resetView]);

    if (!isOpen || !currentFile) return null;
    return (
        <dialog ref={dialog} aria-labelledby={titleId} aria-modal="true" onCancel={(event) => { event.preventDefault(); onClose(); }} className="fixed inset-0 z-modal m-0 flex h-dvh max-h-none w-screen max-w-none flex-col gap-0 border-0 bg-background p-0 text-foreground">
                <div className="flex flex-wrap items-center justify-between gap-2 border-b bg-background p-4">
                    <h2 id={titleId} className="text-sm font-semibold">{currentFile.label} ({currentIndex + 1} / {files.length})</h2>
                    <div className="flex flex-wrap items-center gap-2">
                        {isImage && <>
                            <Button variant="ghost" size="icon" className="max-md:min-h-11 max-md:min-w-11" aria-label={t("maintenance.preview.resetZoom")} onClick={resetView}><RotateCcw className="h-4 w-4" /></Button>
                            <Button variant="ghost" size="icon" className="max-md:min-h-11 max-md:min-w-11" aria-label={t("maintenance.preview.zoomOut")} onClick={() => setScale((value) => Math.max(0.5, value - 0.5))}><ZoomOut className="h-4 w-4" /></Button>
                            <Button variant="ghost" size="icon" className="max-md:min-h-11 max-md:min-w-11" aria-label={t("maintenance.preview.zoomIn")} onClick={() => setScale((value) => Math.min(4, value + 0.5))}><ZoomIn className="h-4 w-4" /></Button>
                        </>}
                        <Button variant="outline" className="max-md:min-h-11 max-md:min-w-11" asChild><a href={currentFile.url} target="_blank" rel="noopener noreferrer" download><Download className="h-4 w-4" aria-hidden />{t("trucks.viewer.openDownload")}</a></Button>
                        <Button variant="ghost" size="icon" className="max-md:min-h-11 max-md:min-w-11" aria-label={t("common.close")} onClick={onClose}><X className="h-4 w-4" /></Button>
                    </div>
                </div>
                <div className="relative flex min-h-0 flex-1 items-center justify-center overflow-hidden bg-muted"
                    onPointerDown={(event) => {
                        if ((event.target as Element).closest("button, a, iframe")) return;
                        if (isImage && scale > 1) {
                            event.currentTarget.setPointerCapture(event.pointerId);
                            setDrag({ x: event.clientX - position.x, y: event.clientY - position.y });
                        }
                    }}
                    onPointerMove={(event) => { if (drag && isImage) setPosition({ x: event.clientX - drag.x, y: event.clientY - drag.y }); }}
                    onPointerUp={() => setDrag(null)} onPointerCancel={() => setDrag(null)} onLostPointerCapture={() => setDrag(null)}
                    onWheel={(event) => {
                        if (!isImage || !event.ctrlKey) return;
                        setScale((value) => Math.min(4, Math.max(0.5, value + (event.deltaY < 0 ? 0.5 : -0.5))));
                    }}>
                    {isImage ? <div className="flex items-center justify-center" style={{ transform: `translate(${position.x}px, ${position.y}px) scale(${scale})` }}>
                        {/* eslint-disable-next-line @next/next/no-img-element */}
                        <img src={currentFile.url} alt={currentFile.label} draggable={false} className="pointer-events-none max-h-[80vh] max-w-[90vw] select-none object-contain" />
                    </div> : <iframe src={currentFile.url} title={currentFile.label} className="h-full w-full bg-background" />}
                    {currentIndex > 0 && <Button variant="secondary" size="icon" className="absolute left-4 top-1/2 h-12 w-12 max-md:min-h-11 max-md:min-w-11" aria-label={t("maintenance.preview.previous")} onClick={() => navigate(-1)}><ChevronLeft className="h-4 w-4" /></Button>}
                    {currentIndex < files.length - 1 && <Button variant="secondary" size="icon" className="absolute right-4 top-1/2 h-12 w-12 max-md:min-h-11 max-md:min-w-11" aria-label={t("maintenance.preview.next")} onClick={() => navigate(1)}><ChevronRight className="h-4 w-4" /></Button>}
                </div>
        </dialog>
    );
}
