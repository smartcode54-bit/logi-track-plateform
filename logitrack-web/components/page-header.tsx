"use client";

/**
 * The page header of every `/app` page (UI3D, issue #143): one H1 in the Headline style of DESIGN.md (600, 24 px /
 * 32 px, `text-2xl font-semibold tracking-tight`), an optional line of description, and the page's actions (the
 * primary one last, so it sits on the right from `md`; below `md` they stack full width under the heading, icon-only
 * buttons keeping their size).
 *
 * - `title` is the page's name from its menu entry (`t("nav.…")`, the key the sidebar, the crumb and the tab title
 *   read; components/app-nav.ts, UI3B). On a menu page it is the H1.
 * - `recordName` is the page's own name on a detail, edit or new page: a truck's plate, a customer's name, "Add
 *   customer". It becomes the H1 and the last crumb, so the crumb and the tab read "<record> · <entry> · LogiTrack
 *   Pro" (`usePageCrumb`); the entry stays in the crumb above it and in the back link's name.
 * - `back` is a link to the page this one was opened from, an icon button named "Back to <title>" in both languages.
 * - `status` sits beside the heading (a truck's status badge).
 *
 * The shell owns the page's outer gutter (`p-gutter` on `<main>`, app/app/layout.tsx), so a page never pads itself.
 */
import { useEffect, type ReactNode } from "react";
import Link from "next/link";
import { ArrowLeft } from "lucide-react";

import { Button } from "@/components/ui/button";
import { useBreadcrumb } from "@/context/breadcrumb";
import { useLanguage } from "@/context/language";
import { cn } from "@/lib/utils";

/**
 * Names the current page in the header breadcrumb, and so in the tab title, while the page is mounted: a detail page's
 * record ("70-1234"), a state that replaces the page ("Page not found"). Nothing for an empty name, so the crumb keeps
 * the generic "Details" / "Edit" / "New" until the name is known.
 */
export function usePageCrumb(name: string | null | undefined): void {
    const { setCustomLastItem } = useBreadcrumb();
    const value = name?.trim() || null;
    useEffect(() => {
        if (!value) return;
        setCustomLastItem(value);
        return () => setCustomLastItem(null);
    }, [value, setCustomLastItem]);
}

export interface PageHeaderBack {
    href: string;
    /** The link's accessible name; "Back to <title>" by default. */
    label?: string;
}

export interface PageHeaderProps {
    /** The page's name from its menu entry: `t("nav.…")`. */
    title: string;
    /** A detail, edit or new page's own name: the H1 and the last crumb instead of the generic one. */
    recordName?: string | null;
    description?: ReactNode;
    /** The page's actions; the primary one last. */
    actions?: ReactNode;
    back?: PageHeaderBack;
    /** Shown beside the heading (a status badge). */
    status?: ReactNode;
    className?: string;
}

export function PageHeader({ title, recordName, description, actions, back, status, className }: PageHeaderProps) {
    const { t } = useLanguage();
    const record = recordName?.trim() || "";
    usePageCrumb(record);
    const heading = record || title;

    return (
        <div data-slot="page-header" className={cn("flex flex-col gap-4 md:flex-row md:items-start md:justify-between", className)}>
            <div className="flex min-w-0 items-start gap-2">
                {back ? (
                    <Button asChild variant="ghost" size="icon" className="-ml-2 shrink-0 max-md:size-11">
                        <Link href={back.href} prefetch={false} aria-label={back.label ?? t("pageHeader.back", { page: title })}>
                            <ArrowLeft aria-hidden />
                        </Link>
                    </Button>
                ) : null}
                <div className="flex min-w-0 flex-col gap-1">
                    <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
                        <h1 className="min-w-0 break-words text-2xl font-semibold tracking-tight">{heading}</h1>
                        {status}
                    </div>
                    {description ? <div className="max-w-prose text-sm leading-relaxed text-muted-foreground">{description}</div> : null}
                </div>
            </div>
            {actions ? (
                <div
                    data-slot="page-header-actions"
                    className="flex w-full flex-col items-start gap-2 md:w-auto md:shrink-0 md:flex-row md:flex-wrap md:items-center md:justify-end [&>*:not([data-size^=icon])]:w-full md:[&>*]:w-auto"
                >
                    {actions}
                </div>
            ) : null}
        </div>
    );
}
