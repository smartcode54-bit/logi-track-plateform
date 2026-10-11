/**
 * Bangchak fuel snapshots over Go (T42, P3; routes of T37: `GET /v1/fuel/daily`, `/v1/fuel/daily/{dayKey}`,
 * `/v1/fuel/monthly`, `POST /v1/jobs/bangchak.snapshot`; developer-spec.md §10.7 row `['fuel', …]`, 60 min /
 * 2 h). The scheduler writes one daily row (insert-only) and the month's row; the rate card reads the
 * month list and the day of an announcement's effective date, the price-history page the days since a date.
 */
import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import { goFetch, goPath, isApiError } from "@/lib/goFetch";
import { goInfiniteQueryFn, goNextPageParam, type GoPage } from "@/lib/goQuery";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
import type { FuelRetailItem, FuelRetailPrices } from "./fuelRetail";

export const FUEL_DAILY_PATH = "/v1/fuel/daily";
export const FUEL_MONTHLY_PATH = "/v1/fuel/monthly";
export const BANGCHAK_SNAPSHOT_JOB_PATH = "/v1/jobs/bangchak.snapshot";
/** Days per page of the price history (Go caps a page at 500). */
export const FUEL_DAILY_PAGE_SIZE = 100;

/** A daily or monthly Bangchak snapshot (internal/billing `FuelSnapshot`). */
export interface FuelSnapshotDTO {
    dayKey?: string;
    monthKey: string;
    capturedAt: string;
    fetchedAt: string | null;
    source: string;
    locale: string;
    status: "ok" | "error" | string;
    errorMessage?: string | null;
    items: FuelRetailItem[];
    updatedAt?: string | null;
}

type DailyKey = ReturnType<typeof queryKeys.fuel.daily>;

/**
 * `['fuel','daily',{from}]`: the days since `from` (`yyyy-MM-dd`), newest first, page by page. The
 * Firestore page read at most 400 days (`fuel-price-history/page.tsx:88-95`); here every page of the
 * range is reachable.
 */
export function fuelDailyRangeQuery(from: string) {
    return infiniteQueryOptions({
        queryKey: queryKeys.fuel.daily({ from }),
        queryFn: goInfiniteQueryFn<FuelSnapshotDTO, DailyKey>(FUEL_DAILY_PATH, {
            query: ([, , p]) => ({ from: typeof p === "object" ? p.from : undefined, limit: FUEL_DAILY_PAGE_SIZE }),
        }),
        initialPageParam: undefined as string | undefined,
        getNextPageParam: goNextPageParam,
        ...QUERY_POLICY.fuel,
        refetchOnWindowFocus: false,
    });
}

export function fuelSnapshotRows(pages: GoPage<FuelSnapshotDTO>[] | undefined): FuelSnapshotDTO[] {
    return pages?.flatMap((p) => p.data ?? []) ?? [];
}

/**
 * `['fuel','daily',dayKey]`: one day's snapshot, `null` when there is none (404): the rate card's
 * announcement dialog shows "no snapshot for that day" rather than an error.
 */
export function fuelDayQuery(dayKey: string | null) {
    return queryOptions({
        queryKey: queryKeys.fuel.daily(dayKey ?? ""),
        queryFn: async ({ signal }): Promise<FuelSnapshotDTO | null> => {
            try {
                return await goFetch<FuelSnapshotDTO>(goPath`/v1/fuel/daily/${dayKey ?? ""}`, { signal });
            } catch (e) {
                if (isApiError(e) && e.status === 404) return null;
                throw e;
            }
        },
        ...QUERY_POLICY.fuel,
        refetchOnWindowFocus: false,
        enabled: Boolean(dayKey && /^\d{4}-\d{2}-\d{2}$/.test(dayKey)),
    });
}

/** `['fuel','monthly']`: the newest 36 months. */
export const fuelMonthlyQuery = queryOptions({
    queryKey: queryKeys.fuel.monthly(),
    queryFn: ({ signal }) => goFetch<FuelSnapshotDTO[]>(FUEL_MONTHLY_PATH, { signal, query: { limit: 36 } }),
    ...QUERY_POLICY.fuel,
    refetchOnWindowFocus: false,
});

/** `POST /v1/jobs/bangchak.snapshot` (steward only, R60): `202 {jobId}`, followed through `useJob`. */
export function startBangchakSnapshot(): Promise<{ jobId: string }> {
    return goFetch<{ jobId: string }>(BANGCHAK_SNAPSHOT_JOB_PATH, { method: "POST" });
}

const DIESEL_KEYWORDS = ["ดีเซล", "diesel", "hi-diesel", "ไฮดีเซล"];
const GASOHOL_KEYWORDS = ["แก๊สโซฮอล", "gasohol", "เบนซิน", "benzin"];

export type FuelTypeFilter = "all" | "diesel" | "gasohol";

export function isDieselName(name: string): boolean {
    const n = name.toLowerCase();
    return DIESEL_KEYWORDS.some((k) => n.includes(k.toLowerCase()));
}

export function matchesFuelType(name: string, filter: FuelTypeFilter): boolean {
    if (filter === "diesel") return isDieselName(name);
    if (filter === "gasohol") {
        const n = name.toLowerCase();
        return GASOHOL_KEYWORDS.some((k) => n.includes(k.toLowerCase()));
    }
    return true;
}

/**
 * The price-history table: one column per fuel name of the type (in first-seen order), one row per
 * day (newest first) with each price and its change from the previous (older) day.
 */
export function fuelHistoryTable(days: FuelSnapshotDTO[], filter: FuelTypeFilter) {
    const names: string[] = [];
    const seen = new Set<string>();
    for (const d of days) {
        for (const i of d.items ?? []) {
            if (!seen.has(i.nameTh) && matchesFuelType(i.nameTh, filter)) {
                seen.add(i.nameTh);
                names.push(i.nameTh);
            }
        }
    }
    const rows = days.map((day, idx) => {
        const prices = new Map((day.items ?? []).map((i) => [i.nameTh, i.price]));
        const older = days[idx + 1];
        const olderPrices = older ? new Map((older.items ?? []).map((i) => [i.nameTh, i.price])) : null;
        const diff = (name: string): number | null => {
            const cur = prices.get(name);
            const prev = olderPrices?.get(name);
            if (cur == null || prev == null) return null;
            return Math.round((cur - prev) * 100) / 100;
        };
        return { day, prices, diff };
    });
    return { names, rows };
}

/**
 * `['fuel','bangchak',locale]` read from Go only (`GET /v1/fuel/retail?locale=`): the key of
 * `fuelRetailQueryOptions` (`./fuelRetail.ts`, which picks the source per fetch and imports the callable
 * for the Firestore side), without that module, so the Go fuel page loads no Firebase module.
 */
export function fuelRetailGoQuery(locale: "th" | "en") {
    return queryOptions({
        queryKey: queryKeys.fuel.bangchak(locale),
        queryFn: ({ signal }) => goFetch<FuelRetailPrices>("/v1/fuel/retail", { signal, query: { locale } }),
        ...QUERY_POLICY.fuel,
        refetchOnWindowFocus: false,
    });
}
