"use client";

/**
 * Pieces both sidebars of the app shell share (UI3, issue #143): the header tooltip of the collapsed rail and the
 * phone behaviour of their links.
 */
import { useCallback } from "react";

import { useSidebar } from "@/components/ui/sidebar";

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
