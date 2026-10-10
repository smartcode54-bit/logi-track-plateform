/**
 * TanStack Query v5 `queryFn` / `mutationFn` factories over `goFetch` (developer-spec.md §10.6,
 * §10.7; Appendix E §E.6). Hooks in `features/<domain>/api/use*.ts` build their Go source from
 * these, so every query passes TanStack's `signal` (unmount or a key change aborts the BFF and Go
 * request), lists page by keyset cursor, and idempotent mutations send one Idempotency-Key per user
 * action. Choosing between a domain's Firestore and Go source is `domainQuery`
 * (`features/platform/api/webFlags.ts`).
 *
 *   useQuery({ queryKey: ["hubs"], queryFn: goQueryFn<HubDTO[]>("/v1/hubs") })
 *   useInfiniteQuery({
 *     queryKey: ["users", { q }],
 *     queryFn: goInfiniteQueryFn<UserDTO, ["users", { q: string }]>("/v1/users", { query: ([, p]) => p }),
 *     initialPageParam: undefined,
 *     getNextPageParam: goNextPageParam,
 *   })
 *   useMutation({ mutationFn: goMutationFn<TaskDTO, NewTask>({ method: "POST", path: "/v1/tasks", idempotent: true }) })
 */
import type { MutationFunction, QueryFunction, QueryKey } from "@tanstack/react-query";
import { goFetch, goFetchEnvelope, newIdempotencyKey, type GoEnvelope, type QueryParams } from "./goFetch";

type FromKey<TKey, R> = R | ((queryKey: TKey) => R);

function fromKey<TKey, R>(value: FromKey<TKey, R>, queryKey: TKey): R {
    return typeof value === "function" ? (value as (k: TKey) => R)(queryKey) : value;
}

export interface GoQueryFnOptions<TKey> {
    /** Query parameters, fixed or derived from the query key (the key holds every filter, W6). */
    query?: FromKey<TKey, QueryParams | undefined>;
}

/** A `queryFn` that GETs a Go path (fixed or derived from the key) and resolves its `data`. */
export function goQueryFn<TData, TKey extends QueryKey = QueryKey>(
    path: FromKey<TKey, string>,
    options: GoQueryFnOptions<TKey> = {}
): QueryFunction<TData, TKey> {
    return ({ queryKey, signal }) => goFetch<TData>(fromKey(path, queryKey), { signal, query: fromKey(options.query, queryKey) });
}

/** One keyset page: `data` holds the page's items, `nextCursor` is absent on the last page. */
export type GoPage<TItem> = GoEnvelope<TItem[]>;

/**
 * An infinite `queryFn` over a keyset list (W9): the page param is the opaque `cursor`
 * (`initialPageParam: undefined`, `getNextPageParam: goNextPageParam`).
 */
export function goInfiniteQueryFn<TItem, TKey extends QueryKey = QueryKey>(
    path: FromKey<TKey, string>,
    options: GoQueryFnOptions<TKey> = {}
): QueryFunction<GoPage<TItem>, TKey, string | undefined> {
    return ({ queryKey, signal, pageParam }) =>
        goFetchEnvelope<TItem[]>(fromKey(path, queryKey), {
            signal,
            query: { ...fromKey(options.query, queryKey), cursor: pageParam },
        });
}

/** `getNextPageParam` for `goInfiniteQueryFn`. */
export function goNextPageParam(lastPage: GoEnvelope<unknown>): string | undefined {
    return lastPage.nextCursor || undefined;
}

const actionKeys = new WeakMap<object, string>();

/**
 * The Idempotency-Key of one user action. TanStack hands every retry of a `mutate(variables)` call
 * the same `variables` object, so an object gets one key for all its attempts; pass a fresh object
 * per action (`mutate({ ...values })`). A primitive cannot be remembered and gets a new key.
 */
export function idempotencyKeyFor(variables: unknown): string {
    if (typeof variables !== "object" || variables === null) return newIdempotencyKey();
    let key = actionKeys.get(variables);
    if (!key) {
        key = newIdempotencyKey();
        actionKeys.set(variables, key);
    }
    return key;
}

export interface GoMutationSpec<TVariables> {
    method: "POST" | "PUT" | "PATCH" | "DELETE";
    path: string | ((variables: TVariables) => string);
    /** The JSON body; default: the variables themselves (none for DELETE). */
    body?: (variables: TVariables) => unknown;
    query?: (variables: TVariables) => QueryParams | undefined;
    /** Send an Idempotency-Key (rows marked ✱ in Appendix B, and any write a retry must not repeat). */
    idempotent?: boolean;
}

/** A `mutationFn` that sends one Go write and resolves its `data` (`undefined` for 204). */
export function goMutationFn<TData = unknown, TVariables = void>(spec: GoMutationSpec<TVariables>): MutationFunction<TData, TVariables> {
    return (variables) => {
        const path = typeof spec.path === "function" ? spec.path(variables) : spec.path;
        const body = spec.body ? spec.body(variables) : spec.method === "DELETE" ? undefined : variables;
        return goFetch<TData>(path, {
            method: spec.method,
            body: body ?? undefined,
            query: spec.query?.(variables),
            idempotencyKey: spec.idempotent ? idempotencyKeyFor(variables) : undefined,
        });
    };
}
