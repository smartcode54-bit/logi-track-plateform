"use client";

import { useState } from "react";
import { FileViewer } from "@/components/ui/file-viewer";
import { useLanguage } from "@/context/language";

export function ImagePreviewGallery({ items, compact }: { items: { url: string; label: string }[]; compact?: boolean }) {
    const { t } = useLanguage();
    const [index, setIndex] = useState<number | null>(null);
    return <div className="space-y-2">
        <p className="text-xs font-medium text-muted-foreground">{t("accounting.preview.title")}</p>
        <div className="flex flex-wrap gap-3 pb-1">
            {items.map((item, i) => <button key={`${item.url}-${i}`} type="button" onClick={() => setIndex(i)}
                className="flex shrink-0 flex-col items-center gap-1 overflow-hidden rounded-lg border border-border text-left transition-colors hover:border-primary/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                <span className="px-2 pt-1.5 text-xs text-muted-foreground">{item.label}</span>
                <span className={compact ? "flex h-[200px] w-[180px] max-w-full items-center justify-center overflow-hidden bg-muted/30" : "flex h-[280px] w-[260px] max-w-full items-center justify-center overflow-hidden bg-muted/30"}>
                    {/* eslint-disable-next-line @next/next/no-img-element */}
                    <img src={item.url} alt="" className="max-h-full max-w-full object-contain" draggable={false} loading="lazy" />
                </span>
            </button>)}
        </div>
        <FileViewer isOpen={index !== null} initialIndex={index ?? 0} onClose={() => setIndex(null)} files={items.map((item) => ({ ...item, type: "image" }))} />
    </div>;
}
