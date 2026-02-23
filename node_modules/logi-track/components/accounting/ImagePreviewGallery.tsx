"use client";

import { useState, useCallback, useRef, useEffect } from "react";
import { Button } from "@/components/ui/button";
import { ZoomIn, ZoomOut, X, ChevronLeft, ChevronRight } from "lucide-react";

const ZOOM_LEVELS = [0.5, 0.75, 1, 1.25, 1.5, 2];

export function ImagePreviewGallery({ items, compact }: { items: { url: string; label: string }[]; compact?: boolean }) {
    const [fullScreenIndex, setFullScreenIndex] = useState<number | null>(null);
    const [zoomIndex, setZoomIndex] = useState(1);
    const [pan, setPan] = useState({ x: 0, y: 0 });
    const dragRef = useRef({ isDragging: false, startX: 0, startY: 0, startPanX: 0, startPanY: 0 });

    const scale = ZOOM_LEVELS[zoomIndex] ?? 1;
    const currentLabel = fullScreenIndex != null ? items[fullScreenIndex]?.label : "";

    const openFullScreen = useCallback((index: number) => {
        setFullScreenIndex(index);
        setZoomIndex(1);
        setPan({ x: 0, y: 0 });
    }, []);

    const closeFullScreen = useCallback(() => setFullScreenIndex(null), []);

    const goPrev = useCallback(() => {
        if (fullScreenIndex == null) return;
        setFullScreenIndex((i) => (i <= 0 ? items.length - 1 : i - 1));
        setZoomIndex(1);
        setPan({ x: 0, y: 0 });
    }, [fullScreenIndex, items.length]);

    const goNext = useCallback(() => {
        if (fullScreenIndex == null) return;
        setFullScreenIndex((i) => (i >= items.length - 1 ? 0 : i + 1));
        setZoomIndex(1);
        setPan({ x: 0, y: 0 });
    }, [fullScreenIndex, items.length]);

    const containerRef = useRef<HTMLDivElement>(null);
    useEffect(() => {
        const el = containerRef.current;
        if (!el || fullScreenIndex == null) return;
        const onWheel = (e: WheelEvent) => {
            e.preventDefault();
            if (e.deltaY < 0) setZoomIndex((i) => Math.min(ZOOM_LEVELS.length - 1, i + 1));
            else setZoomIndex((i) => Math.max(0, i - 1));
        };
        el.addEventListener("wheel", onWheel, { passive: false });
        return () => el.removeEventListener("wheel", onWheel);
    }, [fullScreenIndex]);

    const handleMouseDown = useCallback(
        (e: React.MouseEvent) => {
            if (e.button !== 0) return;
            dragRef.current = {
                isDragging: true,
                startX: e.clientX,
                startY: e.clientY,
                startPanX: pan.x,
                startPanY: pan.y,
            };
        },
        [pan]
    );

    const handleMouseMove = useCallback((e: React.MouseEvent) => {
        if (!dragRef.current.isDragging) return;
        setPan({
            x: dragRef.current.startPanX + e.clientX - dragRef.current.startX,
            y: dragRef.current.startPanY + e.clientY - dragRef.current.startY,
        });
    }, []);

    const handleMouseUp = useCallback(() => {
        dragRef.current.isDragging = false;
    }, []);

    const handleMouseLeave = useCallback(() => {
        dragRef.current.isDragging = false;
    }, []);

    return (
        <div className="space-y-2">
            <p className="text-xs font-medium text-muted-foreground">Preview</p>
            {/* Thumbnails แบบ mobile: แถวรูปเล็ก กดเปิด full screen */}
            <div className="flex flex-wrap gap-3 pb-1">
                {items.map((item, idx) => (
                    <button
                        key={idx}
                        type="button"
                        onClick={() => openFullScreen(idx)}
                        className="shrink-0 flex flex-col items-center gap-1 rounded-lg overflow-hidden border border-border hover:border-primary/50 transition-colors text-left"
                    >
                        <p className="text-xs text-muted-foreground px-2 pt-1.5">{item.label}</p>
                        <div
                            className={compact ? "w-[180px] h-[200px] bg-muted/30 flex items-center justify-center overflow-hidden" : "w-[260px] h-[280px] bg-muted/30 flex items-center justify-center overflow-hidden"}
                        >
                            {/* eslint-disable-next-line @next/next/no-img-element */}
                            <img
                                src={item.url}
                                alt={item.label}
                                className="max-w-full max-h-full object-contain"
                                draggable={false}
                            />
                        </div>
                    </button>
                ))}
            </div>

            {/* Full-screen overlay แบบ mobile: พื้นหลังดำ, เลื่อนซ้ายขวา, pinch-zoom (ใช้ปุ่ม + wheel), ปิดได้ */}
            {fullScreenIndex != null && (
                <div
                    ref={containerRef}
                    className="fixed inset-0 z-50 bg-black/95 flex flex-col"
                    onMouseMove={handleMouseMove}
                    onMouseUp={handleMouseUp}
                    onMouseLeave={handleMouseLeave}
                >
                    <div className="absolute top-2 right-2 z-10">
                        <Button
                            type="button"
                            variant="ghost"
                            size="icon"
                            className="h-10 w-10 text-white hover:bg-white/20"
                            onClick={closeFullScreen}
                        >
                            <X className="h-6 w-6" />
                        </Button>
                    </div>

                    <div className="flex-1 flex items-center justify-center overflow-hidden relative">
                        {/* ปุ่มเลื่อนซ้าย/ขวา */}
                        {items.length > 1 && (
                            <>
                                <Button
                                    type="button"
                                    variant="ghost"
                                    size="icon"
                                    className="absolute left-2 z-10 h-12 w-12 rounded-full text-white hover:bg-white/20"
                                    onClick={goPrev}
                                >
                                    <ChevronLeft className="h-8 w-8" />
                                </Button>
                                <Button
                                    type="button"
                                    variant="ghost"
                                    size="icon"
                                    className="absolute right-2 z-10 h-12 w-12 rounded-full text-white hover:bg-white/20"
                                    onClick={goNext}
                                >
                                    <ChevronRight className="h-8 w-8" />
                                </Button>
                            </>
                        )}

                        <div
                            className="w-full h-full flex items-center justify-center overflow-hidden cursor-grab active:cursor-grabbing"
                            onMouseDown={handleMouseDown}
                            style={{ touchAction: "none" }}
                        >
                            <div
                                className="flex items-center justify-center"
                                style={{
                                    transform: `translate(${pan.x}px, ${pan.y}px) scale(${scale})`,
                                    transformOrigin: "center center",
                                }}
                            >
                                {/* eslint-disable-next-line @next/next/no-img-element */}
                                <img
                                    src={items[fullScreenIndex].url}
                                    alt={currentLabel}
                                    className="max-w-[90vw] max-h-[85vh] w-auto h-auto object-contain select-none pointer-events-none"
                                    draggable={false}
                                    style={{ maxHeight: "85vh" }}
                                />
                            </div>
                        </div>
                    </div>

                    {/* แถบล่าง: ปุ่ม zoom + ตัวเลขรูป */}
                    <div className="flex items-center justify-center gap-4 py-3 bg-black/50">
                        <div className="flex items-center gap-1 rounded-full bg-black/60 px-2 py-1">
                            <Button
                                type="button"
                                variant="ghost"
                                size="icon"
                                className="h-8 w-8 text-white hover:bg-white/20"
                                onClick={() => setZoomIndex((i) => Math.max(0, i - 1))}
                                disabled={zoomIndex === 0}
                            >
                                <ZoomOut className="h-4 w-4" />
                            </Button>
                            <span className="text-white text-sm w-10 text-center">{Math.round(scale * 100)}%</span>
                            <Button
                                type="button"
                                variant="ghost"
                                size="icon"
                                className="h-8 w-8 text-white hover:bg-white/20"
                                onClick={() => setZoomIndex((i) => Math.min(ZOOM_LEVELS.length - 1, i + 1))}
                                disabled={zoomIndex === ZOOM_LEVELS.length - 1}
                            >
                                <ZoomIn className="h-4 w-4" />
                            </Button>
                        </div>
                        {items.length > 1 && (
                            <div className="rounded-full bg-black/60 px-3 py-1.5 text-white text-sm">
                                {fullScreenIndex + 1} / {items.length}
                            </div>
                        )}
                    </div>
                </div>
            )}
        </div>
    );
}
