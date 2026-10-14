"use client";

import { useCallback, useEffect, useRef, useState, type RefObject } from "react";
import { X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { CommandNavigation } from "@/components/ui/command-navigation";
import { useLanguage } from "@/context/language";
import { foldSearchText, matchesTerms, searchTerms } from "@/lib/searchText";
import type { MenuLink } from "./app-nav";

export interface QuickJumpDialogProps {
    entries: readonly MenuLink[];
    dialogRef: RefObject<HTMLDivElement | null>;
    onClose: () => void;
    onSelect: (href: string) => void;
    onRestoreFocus: () => void;
}

export function QuickJumpDialog({ entries, dialogRef, onClose, onSelect, onRestoreFocus }: QuickJumpDialogProps) {
    const { t } = useLanguage();
    const [query, setQuery] = useState("");
    const [content, setContent] = useState<HTMLDivElement | null>(null);
    const input = useRef<HTMLInputElement>(null);
    const setDialogRef = useCallback((node: HTMLDivElement | null) => {
        dialogRef.current = node;
        setContent(node);
    }, [dialogRef]);
    useEffect(() => {
        const viewport = window.visualViewport;
        if (!viewport || !content) return;
        const resize = () => {
            content.style.setProperty("--quick-jump-height", `${viewport.height}px`);
            content.style.setProperty("--quick-jump-width", `${viewport.width}px`);
            content.style.setProperty("--quick-jump-top", `${viewport.offsetTop + viewport.height / 2}px`);
            content.style.setProperty("--quick-jump-left", `${viewport.offsetLeft + viewport.width / 2}px`);
        };
        resize();
        viewport.addEventListener("resize", resize);
        viewport.addEventListener("scroll", resize);
        return () => {
            viewport.removeEventListener("resize", resize);
            viewport.removeEventListener("scroll", resize);
        };
    }, [content]);
    const terms = searchTerms(query);
    const results = entries.filter((entry) => matchesTerms(foldSearchText([
        t(entry.labelKey), entry.groupLabelKey ? t(entry.groupLabelKey) : "", entry.href,
    ].join(" ")), terms));

    return (
        <Dialog open onOpenChange={(next) => { if (!next) onClose(); }}>
            <DialogContent ref={setDialogRef} showCloseButton={false} data-quick-jump-dialog=""
                className="left-[var(--quick-jump-left,50%)] top-[var(--quick-jump-top,50%)] flex max-h-[calc(var(--quick-jump-height,100dvh)-2rem)] w-[calc(var(--quick-jump-width,100vw)-2rem)] flex-col gap-0 overflow-hidden p-0"
                onOpenAutoFocus={(event) => { event.preventDefault(); input.current?.focus(); }}
                onCloseAutoFocus={(event) => { event.preventDefault(); onRestoreFocus(); }}>
                <DialogHeader className="shrink-0 p-4 pr-16 text-left">
                    <DialogTitle className="leading-normal">{t("shell.quickJump.open")}</DialogTitle>
                    <DialogDescription>{t("shell.quickJump.description")}</DialogDescription>
                </DialogHeader>
                <DialogClose asChild>
                    <Button type="button" variant="ghost" size="icon" className="absolute right-2 top-2 size-11"
                        aria-label={t("shell.quickJump.close")}><X className="size-4" aria-hidden="true" /></Button>
                </DialogClose>
                <CommandNavigation inputRef={input} query={query} onQueryChange={setQuery} onSelect={onSelect}
                    label={t("shell.quickJump.search")} placeholder={t("shell.quickJump.placeholder")} emptyText={t("shell.quickJump.empty")}
                    items={results.map((entry) => ({ value: entry.href, label: t(entry.labelKey), group: entry.groupLabelKey ? t(entry.groupLabelKey) : undefined }))} />
                <p role="status" aria-live="polite" aria-atomic="true" className="shrink-0 border-t px-4 py-3 text-sm text-muted-foreground">
                    {t("shell.quickJump.results", { count: results.length })}
                </p>
            </DialogContent>
        </Dialog>
    );
}
