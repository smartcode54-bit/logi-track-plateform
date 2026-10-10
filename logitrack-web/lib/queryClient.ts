/**
 * The web's one TanStack Query client (plan W5; developer-spec.md §10.6, Appendix E §E.6).
 *
 * Defaults: `staleTime` 30 s, `gcTime` 5 min, refetch on window focus, at most 2 retries and only
 * for a network failure or a 5xx; mutations never retry. Errors:
 * - A 401 never reaches the cache as something to handle: `goFetch` refreshes once and, when that
 *   fails, ends the session itself (lib/sessionEnd.ts), whose listener here clears the cache. On a
 *   public page a signed-out 401 only means signed out (`['me']` resolves to `null`); the cache is
 *   emptied then too, because it follows every change of principal in `['me']`.
 * - A 403 shows one toast (the notifier the providers register with the active language).
 * Mutations declare the keys they make stale in `meta.invalidates`; one `MutationCache.onSuccess`
 * invalidates them, so no page reloads "everything" after a write.
 *
 * In the browser there is one client per tab (`getQueryClient`), shared by the providers and by
 * plain async functions that read cached master data (`fetchHubsCached`, ...), so a hub list read
 * by one page is the one every other page sees until its stale time ends.
 */
import { MutationCache, QueryCache, QueryClient, type Query, type QueryKey } from "@tanstack/react-query";
import { isApiError, type ApiError } from "./apiError";
import { onClaimsRefreshed } from "./sharedRefresh";
import { onSessionEnd } from "./sessionEnd";

declare module "@tanstack/react-query" {
    interface Register {
        mutationMeta: {
            /** Query keys (prefixes) made stale by a successful run of this mutation. */
            invalidates?: readonly QueryKey[];
        };
    }
}

export const QUERY_DEFAULTS = { staleTime: 30_000, gcTime: 5 * 60_000 } as const;
export const MAX_QUERY_RETRIES = 2;

// Firestore SDK codes of a transient failure (P0 query functions still read Firestore).
const TRANSIENT_FIRESTORE_CODES = new Set(["unavailable", "deadline-exceeded"]);

/** Whether a failed query may be tried again: a network failure or a 5xx, nothing else. */
export function isRetryableError(error: unknown): boolean {
    if (isApiError(error)) return error.status === 0 || error.status >= 500;
    const code = (error as { code?: unknown } | null)?.code;
    return typeof code === "string" && TRANSIENT_FIRESTORE_CODES.has(code);
}

/** The `retry` option of every query: up to `MAX_QUERY_RETRIES` for retryable errors. */
export function shouldRetryQuery(failureCount: number, error: unknown): boolean {
    return failureCount < MAX_QUERY_RETRIES && isRetryableError(error);
}

export type ForbiddenNotifier = (error: ApiError) => void;

let forbiddenNotifier: ForbiddenNotifier | undefined;

/**
 * Registers what a 403 from any query or mutation shows (the providers pass a toast in the active
 * language); returns the function that removes it.
 */
export function setForbiddenNotifier(notifier: ForbiddenNotifier): () => void {
    forbiddenNotifier = notifier;
    return () => {
        if (forbiddenNotifier === notifier) forbiddenNotifier = undefined;
    };
}

function notifyForbidden(error: unknown): void {
    if (isApiError(error) && error.status === 403) forbiddenNotifier?.(error);
}

/** A client with the W5 defaults; each call is a new, empty cache (tests, the server). */
export function createQueryClient(): QueryClient {
    const client: QueryClient = new QueryClient({
        queryCache: new QueryCache({ onError: notifyForbidden }),
        mutationCache: new MutationCache({
            onError: notifyForbidden,
            onSuccess: (_data, _variables, _context, mutation) => {
                const keys = mutation.meta?.invalidates;
                if (!keys || keys.length === 0) return;
                // Returned so the mutation stays pending until the invalidated queries refetched.
                return Promise.all(keys.map((queryKey) => client.invalidateQueries({ queryKey })));
            },
        }),
        defaultOptions: {
            queries: {
                staleTime: QUERY_DEFAULTS.staleTime,
                gcTime: QUERY_DEFAULTS.gcTime,
                refetchOnWindowFocus: true,
                retry: shouldRetryQuery,
            },
            mutations: { retry: false },
        },
    });
    return client;
}

let browserClient: QueryClient | undefined;

/**
 * The tab's client: one per browser tab, created on first use. On the server (prerendering) every
 * call returns a new client, so no request ever sees another's data.
 */
export function getQueryClient(): QueryClient {
    if (typeof window === "undefined") return createQueryClient();
    browserClient ??= createQueryClient();
    return browserClient;
}

/** Tests: forget the tab's client. */
export function resetQueryClientForTests(): void {
    browserClient = undefined;
}

const ME_KEY = ["me"] as const;

function isMeKey(key: QueryKey): boolean {
    return key.length === ME_KEY.length && key[0] === ME_KEY[0];
}

const notMe = (query: Query) => !isMeKey(query.queryKey);

/**
 * Empties the cache for a signed-out tab: in-flight fetches are cancelled, every query but `['me']`
 * is dropped, and `['me']` reads `null` at once, so observers that stay mounted (the providers, a
 * public page) show the signed-out state without another request. Used by a session end and by
 * the user's own logout.
 */
export function resetSessionCache(client: QueryClient): void {
    void client.cancelQueries();
    client.getMutationCache().clear();
    client.removeQueries({ predicate: notMe });
    client.setQueryData(ME_KEY, null);
}

/**
 * Drops everything but `['me']` before a sign-in into a tab with no principal (context/auth.tsx
 * `completeSignIn`; `watchPrincipal` resets the cache when `['me']` changes from one principal to
 * another), so the next principal starts from an empty cache (`['me']` itself is set by the caller).
 */
export function clearCacheExceptMe(client: QueryClient): void {
    void client.cancelQueries({ predicate: notMe });
    client.getMutationCache().clear();
    client.removeQueries({ predicate: notMe });
}

/** Who `['me']` holds: `undefined` while unknown, `null` signed out, else the user and the active tenant. */
function principalOf(data: unknown): string | null | undefined {
    if (data === undefined) return undefined;
    if (typeof data !== "object" || data === null) return null;
    const me = data as { id?: unknown; tenant?: { id?: unknown } | null };
    if (typeof me.id !== "string") return null;
    const tenant = me.tenant && typeof me.tenant.id === "string" ? me.tenant.id : "";
    return JSON.stringify([me.id, tenant]);
}

/**
 * Watches `['me']` and empties the cache whenever a known principal goes away or becomes another one
 * (another user, or the same user in another tenant: no key carries the tenant). This covers what no
 * session end reports: a session that lapses quietly on a public page (a 401 `unauthenticated` whose
 * refresh is refused resolves `['me']` to `null` without ending anything), a sign-in as someone else
 * in the same tab, a tenant switch. Signed out: every other query is dropped. Another principal: they
 * are cancelled and reset, so mounted observers drop the old data at once and refetch as the new
 * one. A refetch of the same principal (`claims_changed`) and the first sign-in of the tab keep the
 * cache. It runs inside the cache's own notification, before any observer of `['me']` re-renders.
 */
function watchPrincipal(client: QueryClient): () => void {
    let principal = principalOf(client.getQueryData(ME_KEY));
    return client.getQueryCache().subscribe((event) => {
        if (event.type !== "updated" || !isMeKey(event.query.queryKey)) return;
        const next = principalOf(event.query.state.data);
        if (next === undefined || next === principal) return;
        const previous = principal;
        principal = next;
        if (!previous) return;
        if (next === null) {
            clearCacheExceptMe(client);
            return;
        }
        void client.cancelQueries({ predicate: notMe });
        client.getMutationCache().clear();
        void client.resetQueries({ predicate: notMe });
    });
}

/**
 * Ties the cache to the session (developer-spec.md §10.4 steps 3-4): a session end empties it, and
 * so does any change of principal in `['me']` (`watchPrincipal`), so no data of the previous user
 * survives a sign-out or a sign-in as someone else in this tab; a forced refresh after
 * `claims_changed` invalidates `['me']` and every active query, so pages refetch with the new role
 * or scope while the user stays signed in. Returns the function that unbinds the three listeners.
 */
export function bindQueryClientToSession(client: QueryClient): () => void {
    const offEnd = onSessionEnd(() => resetSessionCache(client));
    // One call: every query (`['me']` included, which the shell always observes) is marked stale and
    // the active ones refetch; a second call would cancel and restart the refetch of the first.
    const offClaims = onClaimsRefreshed(() => void client.invalidateQueries());
    const offPrincipal = watchPrincipal(client);
    return () => {
        offEnd();
        offClaims();
        offPrincipal();
    };
}
