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
 * Mounted once under the QueryClientProvider (the app providers, TW4): keeps `['webFlags']` polled
 * and refetches a domain's queries when its source flips, so an env change on the api reaches an
 * open tab within 60 s (R41).
 */
export function useWebFlagsSync(): void {
    const client = useQueryClient();
    useQuery(webFlagsQueryOptions);
    useEffect(() => watchWebFlagFlips(client), [client]);
}
