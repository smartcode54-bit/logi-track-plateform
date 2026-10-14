"use client";

/**
 * Pieces both sidebars of the app shell share (UI3, issue #143; UI3B): the header tooltip of the collapsed rail, the
 * phone behaviour of their links, the first state of the desktop sidebar and the reveal of the current page's entry.
 */
import { useCallback, useEffect, useRef } from "react";

import { sidebarStateFromCookie, useSidebar } from "@/components/ui/sidebar";

/** Tailwind's `lg`: from here a sidebar the user never collapsed starts open (UI3B). */
export const SIDEBAR_OPEN_FROM_PX = 1024;

/**
 * The desktop sidebar's first state: the user's stored choice (the `sidebar_state` cookie the sidebar writes on every
 * toggle), else open from `lg` and the icon rail below it (UI3B: every load started as the rail and the cookie was
 * never read). The shell renders only in the browser, after `['me']` (app/app/layout.tsx), so reading the cookie
 * here causes no hydration mismatch and no flash; the `/app` pages stay static.
 */
export function initialSidebarOpen(): boolean {
    if (typeof document === "undefined") return true;
    const stored = sidebarStateFromCookie(document.cookie);
    if (stored !== undefined) return stored;
    return window.innerWidth >= SIDEBAR_OPEN_FROM_PX;
}

/** The sidebar's keyboard shortcut as this platform writes it: ⌘B on Apple devices, Ctrl+B elsewhere. */
export function sidebarShortcutKeys(): string {
    if (typeof navigator === "undefined") return "Ctrl+B";
    return /Mac|iPhone|iPad|iPod/.test(navigator.userAgent) ? "⌘B" : "Ctrl+B";
}

/** The collapsed rail's tooltip on the sidebar header: the title, and the scope line on its own line. */
export function sidebarHeaderTooltip(title: string, scope: string) {
    return {
        children: (
            <span className="flex flex-col gap-0.5">
                <span className="font-medium">{title}</span>
                {scope ? <span data-testid="sidebar-scope-tooltip">{scope}</span> : null}
            </span>
        ),
    };
}

/**
 * The `onClick` of every sidebar link: on a phone the sidebar is a sheet over the page, so it closes once a link is
 * tapped instead of covering the page it opened. On a desktop it does nothing.
 */
export function useCloseSidebarOnPhone(): () => void {
    const { isMobile, setOpenMobile } = useSidebar();
    return useCallback(() => {
        if (isMobile) setOpenMobile(false);
    }, [isMobile, setOpenMobile]);
}

/**
 * A ref for the sidebar's menu: whenever the sidebar or the phone sheet opens, and on every change of page, the
 * entry of the current page (`aria-current="page"`) is scrolled into view, nearest edge only (UI3B: the expanded
 * list ran 1272 px in a 664 px window and the current entry could sit below the fold).
 */
export function useRevealCurrentEntry<T extends HTMLElement>(active: string | undefined) {
    const { isMobile, open, openMobile } = useSidebar();
    const ref = useRef<T | null>(null);
    const shown = isMobile ? openMobile : open;
    useEffect(() => {
        if (!shown) return;
        const current = ref.current?.querySelector<HTMLElement>('[aria-current="page"]');
        // jsdom has no scrollIntoView.
        current?.scrollIntoView?.({ block: "nearest" });
    }, [shown, active, isMobile]);
    return ref;
}
