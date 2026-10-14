"use client";

import { useEffect, useLayoutEffect } from "react";

// A layout effect in the browser; on the server neither runs.
const useBrowserLayoutEffect = typeof window === "undefined" ? useEffect : useLayoutEffect;

/**
 * Keeps `document.title` at `title` while the caller is mounted (UI3E, the public pages; the console's shell does the
 * same in components/app-breadcrumb.tsx). Next writes the route's metadata `<title>` back after a load and on some
 * client navigations, so the title is put back whenever anything in `<head>` changes, not only when `title` does.
 *
 * The observer lives in a layout effect: when the page unmounts in the commit that writes the next route's metadata
 * title, it is disconnected (which drops its pending records) before it could put this page's title back over the
 * next one. Nothing is restored on unmount, for the same reason: the next route's metadata names it.
 */
export function useDocumentTitle(title: string): void {
    useBrowserLayoutEffect(() => {
        const apply = () => {
            if (document.title !== title) document.title = title;
        };
        apply();
        // Setting the title is itself a mutation; `apply` only writes when the text differs, so this settles.
        const observer = new MutationObserver(apply);
        observer.observe(document.head, { childList: true, subtree: true, characterData: true });
        return () => observer.disconnect();
    }, [title]);
}
