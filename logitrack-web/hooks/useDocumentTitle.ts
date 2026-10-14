"use client";

import { useEffect, useLayoutEffect } from "react";

// A layout effect in the browser; on the server neither runs.
const useBrowserLayoutEffect = typeof window === "undefined" ? useEffect : useLayoutEffect;

/**
 * Keeps `document.title` at `title` while the caller is mounted: the public pages (UI3E) and the console's breadcrumb
 * (components/app-breadcrumb.tsx, whose title names a detail page's record, UI3D). Next writes the
 * route's metadata `<title>` back after a load and on some client navigations, so the title is put back whenever
 * anything in `<head>` changes, not only when `title` does.
 *
 * On unmount the title found on mount comes back, as the console's does, but only while the tab still shows this
 * caller's title: when the commit that unmounts it has already written the next route's metadata title, that one
 * stays. The observer lives in a layout effect, so it is disconnected (and its pending records dropped) within that
 * commit, before it could put this title back over the next route's.
 */
export function useDocumentTitle(title: string): void {
    useBrowserLayoutEffect(() => {
        const previous = document.title;
        const apply = () => {
            if (document.title !== title) document.title = title;
        };
        apply();
        // Setting the title is itself a mutation; `apply` only writes when the text differs, so this settles.
        const observer = new MutationObserver(apply);
        observer.observe(document.head, { childList: true, subtree: true, characterData: true });
        return () => {
            observer.disconnect();
            if (document.title === title) document.title = previous;
        };
    }, [title]);
}
