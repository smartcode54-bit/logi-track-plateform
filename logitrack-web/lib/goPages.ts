/**
 * Every row of a keyset list (Appendix B §B.1.5: `?limit=&cursor=`, `nextCursor` absent on the last
 * page), page by page: for pickers over small lists (tenants, customers, a tenant's members). Bounded
 * by `maxPages`, so a picker never walks an unbounded list. Big lists use `useInfiniteQuery`.
 */
import { goFetchEnvelope, type QueryParams } from "./goFetch";

export const PAGE_LIMIT = 100;
export const DEFAULT_MAX_PAGES = 20;

export interface FetchAllPagesOptions {
    signal?: AbortSignal;
    requireProjection?: boolean;
    /** Page bound (default `DEFAULT_MAX_PAGES`). */
    maxPages?: number;
    /** Conditional request headers, applied to every page. */
    headers?: Record<string, string>;
}

export async function fetchAllPages<T>(path: string, query: QueryParams = {}, options: FetchAllPagesOptions = {}): Promise<T[]> {
    const { signal, maxPages = DEFAULT_MAX_PAGES, headers, requireProjection } = options;
    const out: T[] = [];
    let cursor: string | undefined;
    for (let i = 0; i < maxPages; i++) {
        const page = await goFetchEnvelope<T[]>(path, { signal, headers, requireProjection, query: { ...query, limit: PAGE_LIMIT, cursor } });
        // goFetchEnvelope associates relative redaction paths with each row before flattening.
        if (Array.isArray(page.data)) out.push(...page.data);
        cursor = page.nextCursor;
        if (!cursor) break;
    }
    return out;
}
