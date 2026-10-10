/**
 * TanStack Query v5 `queryFn` / `mutationFn` factories over `goFetch` (developer-spec.md §10.6,
 * §10.7; Appendix E §E.6). Hooks in `features/<domain>/api/use*.ts` build their Go source from
 * these, so every query passes TanStack's `signal` (unmount or a key change aborts the BFF and Go
 * request), lists page by keyset cursor, and idempotent mutations send one Idempotency-Key per user
 * action. Choosing between a domain's Firestore and Go source is `domainQueryFn`
 * (`features/platform/api/webFlags.ts`). Paths built from values go through `goPath`.
 *
 *   useQuery({ queryKey: ["hubs"], queryFn: goQueryFn<HubDTO[]>("/v1/hubs") })
 *   useInfiniteQuery({
 *     queryKey: ["users", { q }],
 *     queryFn: goInfiniteQueryFn<UserDTO, ["users", { q: string }]>("/v1/users", { query: ([, p]) => p }),
 *     initialPageParam: undefined,
 *     getNextPageParam: goNextPageParam,
 *   })
 *   useMutation({ mutationFn: goMutationFn<TaskDTO, NewTask>({ method: "POST", path: "/v1/tasks", idempotent: true }) })
 *   // An id-only action carries its key in the variables, created once per click:
 *   const compute = useMutation({
 *     mutationFn: goMutationFn<Billing, { id: string; idempotencyKey: string }>({
 *       method: "POST",
 *       path: (v) => goPath`/v1/trips/${v.id}/billing/compute`,
 *       body: () => ({}),
 *       idempotencyKey: (v) => v.idempotencyKey,
 *     }),
 *   });
 *   compute.mutate({ id, idempotencyKey: newIdempotencyKey() });
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
    /**
     * Request headers, fixed or derived from the key (`X-Act-On-Tenant` of a platform read, Appendix C
     * §C.3.9). Whatever changes them belongs in the key, so another reach never reuses a cached page.
     */
    headers?: FromKey<TKey, Record<string, string> | undefined>;
}

/** A `queryFn` that GETs a Go path (fixed or derived from the key) and resolves its `data`. */
export function goQueryFn<TData, TKey extends QueryKey = QueryKey>(
    path: FromKey<TKey, string>,
    options: GoQueryFnOptions<TKey> = {}
): QueryFunction<TData, TKey> {
    return ({ queryKey, signal }) =>
        goFetch<TData>(fromKey(path, queryKey), { signal, query: fromKey(options.query, queryKey), headers: fromKey(options.headers, queryKey) });
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
            headers: fromKey(options.headers, queryKey),
        });
}

/** `getNextPageParam` for `goInfiniteQueryFn`. */
export function goNextPageParam(lastPage: GoEnvelope<unknown>): string | undefined {
    return lastPage.nextCursor || undefined;
}

const actionKeys = new WeakMap<object, string>();

/**
 * The Idempotency-Key of one user action. TanStack hands every retry of a `mutate(variables)` call
 * the same `variables` object, so an object gets one key for all its attempts. Pass a fresh object
 * per action (`mutate({ ...values })`), never a row taken from query data: structural sharing keeps
 * that reference across equal refetches, and Go replays a stored 2xx for a repeated key and
 * fingerprint without running the action again. `goMutationFn` forgets the key once the action
 * succeeds (no retry follows a success), which limits the damage of a reused reference. A primitive
 * (`mutate(id)`, `mutate()`) cannot be remembered, so it is refused: carry the key in the variables
 * (`GoMutationSpec.idempotencyKey`) instead.
 */
export function idempotencyKeyFor(variables: unknown): string {
    if (typeof variables !== "object" || variables === null) {
        throw new TypeError("goMutationFn: idempotent mutations need object variables or an idempotencyKey");
    }
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
    /**
     * Send an Idempotency-Key (rows marked ✱ in Appendix B, and any write a retry must not repeat),
     * one per `variables` object (`idempotencyKeyFor`); primitive variables are refused.
     */
    idempotent?: boolean;
    /**
     * The action's Idempotency-Key, read from the variables (`mutate({ ..., idempotencyKey:
     * newIdempotencyKey() })`, one per click): every retry of that call gets the same variables and so
     * the same key. Implies `idempotent`. The default body is the variables themselves, so give a
     * `body` that leaves the key out.
     */
    idempotencyKey?: (variables: TVariables) => string;
}

/**
 * A `mutationFn` that sends one Go write and resolves its `data` (`undefined` for 204). A spec error
 * (an idempotent mutation with primitive variables and no `idempotencyKey`) rejects before any
 * request is sent.
 */
export function goMutationFn<TData = unknown, TVariables = void>(spec: GoMutationSpec<TVariables>): MutationFunction<TData, TVariables> {
    return async (variables) => {
        const path = typeof spec.path === "function" ? spec.path(variables) : spec.path;
        const body = spec.body ? spec.body(variables) : spec.method === "DELETE" ? undefined : variables;
        const fromVariables = !spec.idempotencyKey && spec.idempotent === true;
        const idempotencyKey = spec.idempotencyKey
            ? spec.idempotencyKey(variables)
            : fromVariables
              ? idempotencyKeyFor(variables)
              : undefined;
        const data = await goFetch<TData>(path, {
            method: spec.method,
            body: body ?? undefined,
            query: spec.query?.(variables),
            idempotencyKey,
        });
        // A success ends the action: no retry follows, so the same object passed again is a new action.
        if (fromVariables) actionKeys.delete(variables as object);
        return data;
    };
}
