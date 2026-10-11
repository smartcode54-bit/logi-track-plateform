"use client";

import type { ComponentType } from "react";
import { Loader2 } from "lucide-react";

import { useDomainPageSource } from "../api/useWebFlags";
import type { WebDomain } from "../api/webFlags";

/** The page-area spinner while the flags or a page chunk load (the shell stays). */
export function PageLoading() {
    return (
        <div className="flex min-h-[50vh] items-center justify-center" role="status" aria-busy="true">
            <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
        </div>
    );
}

/**
 * Renders a page from its domain's source (T54S): the Go page while the web flag of `domain` is `go`,
 * the Firestore page otherwise (developer-spec.md §10.6, §10.13; R35, R41). Pass both through
 * `next/dynamic`, so a route loads only the source it renders: the Firestore SDK stays out of a Go
 * page's chunks (the auth bridge still loads it on every /app route until TW7), and a flip (an env
 * change on the api) swaps the page within 60 s, no rebuild.
 */
export function DomainPage({ domain, go: Go, firestore: Firestore }: { domain: WebDomain; go: ComponentType; firestore: ComponentType }) {
    const source = useDomainPageSource(domain);
    if (source === undefined) return <PageLoading />;
    return source === "go" ? <Go /> : <Firestore />;
}
