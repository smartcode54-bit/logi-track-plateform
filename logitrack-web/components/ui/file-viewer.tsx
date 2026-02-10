"use client";

import { useState, useEffect, useCallback, useRef } from "react";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { X, ZoomIn, ZoomOut, ChevronLeft, ChevronRight, RotateCcw, Download } from "lucide-react";
import { cn } from "@/lib/utils";
import Image from "next/image";

interface FileViewerProps {
    isOpen: boolean;
    onClose: () => void;
    files: { url: string; type: "image" | "pdf"; label: string }[];
    initialIndex?: number;
}

export function FileViewer({ isOpen, onClose, files, initialIndex = 0 }: FileViewerProps) {
    const [currentIndex, setCurrentIndex] = useState(initialIndex);
    const [scale, setScale] = useState(1);
    const [position, setPosition] = useState({ x: 0, y: 0 });
    const [isDragging, setIsDragging] = useState(false);
    const [startPos, setStartPos] = useState({ x: 0, y: 0 });

    const containerRef = useRef<HTMLDivElement>(null);

    useEffect(() => {
        if (isOpen) {
            setCurrentIndex(initialIndex);
            resetView();
        }
    }, [isOpen, initialIndex]);

    const resetView = () => {
        setScale(1);
        setPosition({ x: 0, y: 0 });
    };

    const handleZoomIn = () => setScale(s => Math.min(s + 0.5, 4));
    const handleZoomOut = () => setScale(s => Math.max(s - 0.5, 0.5));

    const handleNext = useCallback(() => {
        if (currentIndex < files.length - 1) {
            setCurrentIndex(prev => prev + 1);
            resetView();
        }
    }, [currentIndex, files.length]);

    const handlePrev = useCallback(() => {
        if (currentIndex > 0) {
            setCurrentIndex(prev => prev - 1);
            resetView();
        }
    }, [currentIndex]);

    // Keyboard navigation
    useEffect(() => {
        const handleKeyDown = (e: KeyboardEvent) => {
            if (!isOpen) return;

            switch (e.key) {
                case "ArrowRight":
                    handleNext();
                    break;
                case "ArrowLeft":
                    handlePrev();
                    break;
                case "Escape":
                    onClose();
                    break;
                case "+":
                case "=":
                    handleZoomIn();
                    break;
                case "-":
                    handleZoomOut();
                    break;
                case "0":
                    resetView();
                    break;
            }
        };

        window.addEventListener("keydown", handleKeyDown);
        return () => window.removeEventListener("keydown", handleKeyDown);
    }, [isOpen, handleNext, handlePrev, onClose]);

    // Mouse drag handling
    const handleMouseDown = (e: React.MouseEvent) => {
        if (scale > 1) {
            setIsDragging(true);
            setStartPos({ x: e.clientX - position.x, y: e.clientY - position.y });
        }
    };

    const handleMouseMove = (e: React.MouseEvent) => {
        if (isDragging && scale > 1) {
            e.preventDefault();
            setPosition({
                x: e.clientX - startPos.x,
                y: e.clientY - startPos.y
            });
        }
    };

    const handleMouseUp = () => setIsDragging(false);

    if (!isOpen || files.length === 0) return null;

    const currentFile = files[currentIndex];

    return (
        <div className="fixed inset-0 z-50 bg-black/95 flex flex-col animate-in fade-in duration-200">
            {/* Header/Controls */}
            <div className="flex items-center justify-between p-4 text-white bg-black/50 backdrop-blur-sm z-50">
                <div className="flex items-center gap-4">
                    <span className="text-sm font-medium">
                        {currentIndex + 1} / {files.length}
                    </span>
                    <span className="text-sm text-gray-400">
                        {currentFile.label}
                    </span>
                </div>

                <div className="flex items-center gap-2">
                    <Button variant="ghost" size="icon" onClick={() => resetView()} className="text-white hover:bg-white/20">
                        <RotateCcw className="h-4 w-4" />
                    </Button>
                    <Button variant="ghost" size="icon" onClick={handleZoomOut} className="text-white hover:bg-white/20">
                        <ZoomOut className="h-4 w-4" />
                    </Button>
                    <Button variant="ghost" size="icon" onClick={handleZoomIn} className="text-white hover:bg-white/20">
                        <ZoomIn className="h-4 w-4" />
                    </Button>
                    <Button variant="ghost" size="icon" asChild className="text-white hover:bg-white/20">
                        <a href={currentFile.url} target="_blank" rel="noopener noreferrer" download>
                            <Download className="h-4 w-4" />
                        </a>
                    </Button>
                    <div className="w-px h-6 bg-white/20 mx-2" />
                    <Button variant="ghost" size="icon" onClick={onClose} className="text-white hover:bg-red-500/50">
                        <X className="h-5 w-5" />
                    </Button>
                </div>
            </div>

            {/* Main Content */}
            <div
                className="flex-1 relative overflow-hidden flex items-center justify-center select-none"
                ref={containerRef}
                onMouseDown={handleMouseDown}
                onMouseMove={handleMouseMove}
                onMouseUp={handleMouseUp}
                onMouseLeave={handleMouseUp}
                onWheel={(e) => {
                    if (e.ctrlKey) {
                        e.preventDefault();
                        if (e.deltaY < 0) handleZoomIn();
                        else handleZoomOut();
                    }
                }}
            >
                {currentFile.type === "image" ? (
                    <div
                        style={{
                            transform: `translate(${position.x}px, ${position.y}px) scale(${scale})`,
                            transition: isDragging ? 'none' : 'transform 0.2s ease-out',
                            cursor: scale > 1 ? (isDragging ? 'grabbing' : 'grab') : 'default'
                        }}
                        className="flex items-center justify-center"
                    >
                        {/* We use standard img tag here for better direct manipulation capabilities vs next/image in this specific interactive context, 
                            though next/image could also work with wrapper divs. */}
                        {/* eslint-disable-next-line @next/next/no-img-element */}
                        <img
                            src={currentFile.url}
                            alt={currentFile.label}
                            style={{
                                maxWidth: '90vw',
                                maxHeight: '85vh',
                                objectFit: 'contain'
                            }}
                            className="pointer-events-none"
                            draggable={false}
                        />
                    </div>
                ) : (
                    <div className="w-full h-full p-8 flex flex-col items-center justify-center text-white gap-4">
                        <iframe
                            src={currentFile.url}
                            className="w-full h-full max-w-4xl bg-white rounded-lg"
                            title={currentFile.label}
                        />
                    </div>
                )}

                {/* Navigation Buttons */}
                {currentIndex > 0 && (
                    <Button
                        variant="ghost"
                        size="icon"
                        className="absolute left-4 top-1/2 -translate-y-1/2 text-white hover:bg-white/20 h-12 w-12 rounded-full"
                        onClick={handlePrev}
                    >
                        <ChevronLeft className="h-8 w-8" />
                    </Button>
                )}

                {currentIndex < files.length - 1 && (
                    <Button
                        variant="ghost"
                        size="icon"
                        className="absolute right-4 top-1/2 -translate-y-1/2 text-white hover:bg-white/20 h-12 w-12 rounded-full"
                        onClick={handleNext}
                    >
                        <ChevronRight className="h-8 w-8" />
                    </Button>
                )}
            </div>
        </div>
    );
}
