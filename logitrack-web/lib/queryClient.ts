/**
 * The tab's TanStack QueryClient (developer-spec.md §10.6). Defaults: 30 s stale, 5 min gc, refetch on
 * focus, at most two retries and only for a network failure or a 5xx (a 4xx is an answer); mutations
 * never retry. A 401 never reaches a retry: `goFetch` has already refreshed once and, when that failed,
 * ended the session (lib/sessionEnd.ts). TW4 adds the query-key factory and `meta.invalidates`.
 */
import { QueryClient } from "@tanstack/react-query";
import { isApiError } from "./apiError";

export const DEFAULT_STALE_MS = 30_000;
export const DEFAULT_GC_MS = 300_000;

/** Whether a failed query may be retried: no response at all, or a 5xx. */
export function retryableError(error: unknown): boolean {
    return !isApiError(error) || error.status === 0 || error.status >= 500;
}

export function makeQueryClient(): QueryClient {
    return new QueryClient({
        defaultOptions: {
            queries: {
                staleTime: DEFAULT_STALE_MS,
                gcTime: DEFAULT_GC_MS,
                refetchOnWindowFocus: true,
                retry: (count, error) => count < 2 && retryableError(error),
            },
            mutations: { retry: false },
        },
    });
}
