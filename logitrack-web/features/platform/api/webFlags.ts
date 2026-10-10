/**
 * Runtime web domain flags, TanStack `['webFlags']` over `GET /v1/config/web-flags` (developer-spec.md
 * §10.6, §10.7, §12.1; Appendix E §E.6, §E.8.6; R35, R41). For each of the nine domains Go answers
 * whether the web reads it through Go (`go`) or still from Firestore (`firebase`): its env
 * `PG_OWNED_DOMAINS` with `WEB_FLAG_OVERRIDES` layered on top. No build-time API URL or domain-flag
 * variable exists, so switching or rolling back a domain is an env change on the api container that
 * reaches every open tab within 60 s, without a web rebuild.
 *
 * A hook whose domain has not cut over yet keeps both sources and picks one per fetch:
 *
 *   useQuery({
 *     queryKey: ["hubs"],
 *     queryFn: domainQueryFn("masterdata", { firestore: fetchHubsFromFirestore, go: goQueryFn<HubDTO[]>("/v1/hubs") }),
 *   })
 *
 * Keys never change between sources (both return the OpenAPI DTO); `useWebFlagsSync` (mounted once
 * by the app providers) polls the flags and refetches a domain's queries when its source flips.
 */
import { queryOptions, type QueryClient, type QueryFunction, type QueryKey } from "@tanstack/react-query";
import { ApiError } from "@/lib/apiError";
import { goFetch } from "@/lib/goFetch";

/** The nine coexistence domains (developer-spec.md §12.1), the keys of the flags. */
export const WEB_DOMAINS = [
    "auth",
    "masterdata",
    "operations",
    "billing",
    "hr",
    "comms",
    "security",
    "mobile_release",
    "dashboard",
] as const;

export type WebDomain = (typeof WEB_DOMAINS)[number];
export type DomainSource = "go" | "firebase";

export interface WebFlags {
    domains: Record<WebDomain, DomainSource>;
}

export const WEB_FLAGS_KEY = ["webFlags"] as const;
export const WEB_FLAGS_PATH = "/v1/config/web-flags";
/** Stale time and poll interval: a flip reaches a visible tab within this long (R41). */
export const WEB_FLAGS_REFRESH_MS = 60_000;

function isRecord(value: unknown): value is Record<string, unknown> {
    return typeof value === "object" && value !== null && !Array.isArray(value);
}

/**
 * Reads the `data` of `GET /v1/config/web-flags`. A domain that is missing or has an unknown value
 * stays on Firestore (the pre-migration source); a body without `domains` is a `bad_response`.
 */
export function parseWebFlags(data: unknown): WebFlags {
    if (!isRecord(data) || !isRecord(data.domains)) {
        throw new ApiError({ status: 200, code: "bad_response", message: "web flags without domains" });
    }
    const raw = data.domains;
    const domains = {} as Record<WebDomain, DomainSource>;
    for (const d of WEB_DOMAINS) domains[d] = raw[d] === "go" ? "go" : "firebase";
    return { domains };
}

export const webFlagsQueryOptions = queryOptions({
    queryKey: WEB_FLAGS_KEY,
    queryFn: async ({ signal }) => parseWebFlags(await goFetch<unknown>(WEB_FLAGS_PATH, { signal })),
    staleTime: WEB_FLAGS_REFRESH_MS,
    gcTime: 30 * 60_000,
    // Polled while an observer is mounted and the tab is visible (useWebFlagsSync); hidden tabs catch
    // up on focus. The stale time alone would refetch only on the next mount or focus.
    refetchInterval: WEB_FLAGS_REFRESH_MS,
    refetchOnWindowFocus: true,
});

/**
 * The source of `domain` for a fetch starting now: the cached flags (a stale copy is returned at
 * once and revalidated in the background), fetched first when there is none. If the flags cannot
 * be read and none were ever loaded, the domain stays on Firestore, its source before cut-over.
 */
export async function resolveDomainSource(client: QueryClient, domain: WebDomain): Promise<DomainSource> {
    try {
        const flags = await client.ensureQueryData({ ...webFlagsQueryOptions, revalidateIfStale: true });
        return flags.domains[domain];
    } catch {
        return client.getQueryData<WebFlags>(WEB_FLAGS_KEY)?.domains[domain] ?? "firebase";
    }
}

// The domain and source of each domain query's latest fetch, keyed by the cached Query object.
const lastSource = new WeakMap<object, { domain: WebDomain; source: DomainSource }>();

/**
 * A `queryFn` that runs the domain's Go or Firestore source, as the flags say when the fetch starts.
 * It records the source it used, so `watchWebFlagFlips` refetches the query when the flags move the
 * domain to the other source.
 */
export function domainQueryFn<TData, TKey extends QueryKey = QueryKey>(
    domain: WebDomain,
    sources: { firestore: QueryFunction<TData, TKey>; go: QueryFunction<TData, TKey> }
): QueryFunction<TData, TKey> {
    return async (context) => {
        const source = await resolveDomainSource(context.client, domain);
        const query = context.client.getQueryCache().find({ queryKey: context.queryKey, exact: true });
        if (query) lastSource.set(query, { domain, source });
        return source === "go" ? sources.go(context) : sources.firestore(context);
    };
}

function isWebFlagsKey(key: QueryKey): boolean {
    return key.length === WEB_FLAGS_KEY.length && key[0] === WEB_FLAGS_KEY[0];
}

/**
 * Whenever flags arrive, invalidates every domain query whose latest fetch used the other source (a
 * flip, or a fetch that fell back to Firestore before the flags could first be read): active ones
 * refetch at once, so no page keeps showing data from the source it just left. Returns the
 * unsubscribe function.
 */
export function watchWebFlagFlips(client: QueryClient): () => void {
    return client.getQueryCache().subscribe((event) => {
        if (event.type !== "updated" || event.action.type !== "success" || !isWebFlagsKey(event.query.queryKey)) return;
        const flags = event.query.state.data as WebFlags | undefined;
        if (!flags) return;
        const flipped = (query: object) => {
            const last = lastSource.get(query);
            return last !== undefined && flags.domains[last.domain] !== last.source;
        };
        if (client.getQueryCache().findAll({ predicate: flipped }).length > 0) {
            void client.invalidateQueries({ predicate: flipped });
        }
    });
}
