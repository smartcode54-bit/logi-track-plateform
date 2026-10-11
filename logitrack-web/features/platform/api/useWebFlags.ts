"use client";

import { useEffect } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { watchWebFlagFlips, webFlagsQueryOptions, type DomainSource, type WebDomain } from "./webFlags";

/** `['webFlags']`: the per-domain source of the web, refreshed every 60 s while the tab is visible. */
export function useWebFlags() {
    return useQuery(webFlagsQueryOptions);
}

/** One domain's source, or `undefined` while the flags load (e.g. to pick a Go-only UI branch). */
export function useDomainSource(domain: WebDomain): DomainSource | undefined {
    return useQuery({ ...webFlagsQueryOptions, select: (flags) => flags.domains[domain] }).data;
}

/**
 * The source a whole page renders from (T54S): `go` or `firebase` once the flags are known,
 * `undefined` while the first flags request is in flight. A page whose Go read model has no
 * Firestore equivalent (the tenant-role matrix, the server-side Security Center counters) switches
 * here instead of per fetch (`domainQueryFn`); the Firestore page stays until TW7 and is shown while
 * the flag is off. Flags that cannot be read and were never loaded mean Firestore, as for
 * `resolveDomainSource`; a later flip re-renders the page from the other source within 60 s.
 */
export function useDomainPageSource(domain: WebDomain): DomainSource | undefined {
    const flags = useWebFlags();
    if (flags.data) return flags.data.domains[domain];
    if (flags.isError) return "firebase";
    return undefined;
}

/**
 * Mounted once under the QueryClientProvider (the app providers, TW4): keeps `['webFlags']` polled
 * and refetches a domain's queries when its source flips, so an env change on the api reaches an
 * open tab within 60 s (R41).
 */
export function useWebFlagsSync(): void {
    const client = useQueryClient();
    useQuery(webFlagsQueryOptions);
    useEffect(() => watchWebFlagFlips(client), [client]);
}
