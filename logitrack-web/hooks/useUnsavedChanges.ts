"use client";

import { useEffect } from "react";
import { registerNavigationGuard } from "@/lib/navigationGuard";

type TraversalEvent = Event & { navigationType: string; destination: { url: string } };

/** Protect a live editor across links, shell actions, browser history, reload and tab close. */
export function useUnsavedChanges(dirty: boolean, message: string, onDiscard: () => void): void {
    useEffect(() => {
        if (!dirty) return;
        let active = true;
        const href = window.location.href;
        const state = window.history.state;
        const confirm = () => {
            if (!active) return true;
            if (!window.confirm(message)) return false;
            active = false;
            onDiscard();
            return true;
        };
        const unregister = registerNavigationGuard(confirm);
        const leavesEditor = (destination: string) => {
            const next = new URL(destination, href);
            const current = new URL(href);
            return next.origin !== current.origin || next.pathname !== current.pathname || next.search !== current.search;
        };
        const click = (event: MouseEvent) => {
            if (!active || event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
            const link = event.target instanceof Element ? event.target.closest<HTMLAnchorElement>("a[href]") : null;
            if (!link || link.hasAttribute("download") || (link.target && link.target !== "_self") || !leavesEditor(link.href)) return;
            if (!confirm()) {
                event.preventDefault();
                event.stopImmediatePropagation();
            }
        };
        const unload = (event: BeforeUnloadEvent) => {
            if (!active) return;
            event.preventDefault();
            event.returnValue = "";
        };
        // Native traversal can be cancelled before Next handles popstate. Older browsers restore the
        // current entry synchronously on cancellation, before Next can replace the still-edited page.
        const navigation = (window as Window & { navigation?: EventTarget }).navigation;
        const traverse = (event: Event) => {
            const e = event as TraversalEvent;
            if (e.navigationType === "traverse" && e.cancelable && leavesEditor(e.destination.url) && !confirm()) e.preventDefault();
        };
        const pop = (event: PopStateEvent) => {
            if (!active || !leavesEditor(window.location.href) || confirm()) return;
            event.stopImmediatePropagation();
            window.history.pushState(state, "", href);
        };
        document.addEventListener("click", click, true);
        window.addEventListener("beforeunload", unload);
        if (navigation) navigation.addEventListener("navigate", traverse);
        else window.addEventListener("popstate", pop, true);
        return () => {
            unregister();
            document.removeEventListener("click", click, true);
            window.removeEventListener("beforeunload", unload);
            navigation?.removeEventListener("navigate", traverse);
            window.removeEventListener("popstate", pop, true);
        };
    }, [dirty, message, onDiscard]);
}
