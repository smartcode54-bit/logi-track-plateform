/**
 * Income over Go (T42, P3; developer-spec.md §10.7 row `['income', …]`, §10.10; Appendix E §E.2.3 row
 * "income"; Appendix B §B.2.12, §B.2.13).
 *
 * Two keyset lists with the date range in the key and in the request, so a trip older than the newest
 * 500 is never silently missing (the Firestore page filtered dates in the browser over that window,
 * `income/page.tsx:336-342,597-621`):
 * - `['income',{from,to,party,kind:'trips'}]`: `GET /v1/trips?axis=delivered&from&to&customerId`
 *   (keyset on `(delivered_at, id)`; every row carries the `billing` block for `accounting:view_income`).
 *   No `status`: Go takes it on the created axis only and answers 422 `status: not_on_axis` on any other
 *   (Appendix B §B.2.12); the delivered axis already lists delivered trips only;
 * - `['income',{from,to,party,kind:'standby'}]`: `GET /v1/standby?sort=ended&from&to&customerId`.
 * `party` is the billing party id (the trip and standby filters match `billing_party_id`), `all` for none.
 * Writes that reprice invalidate `['income']` (`./billingWrites.go.ts`); SSE `trip.priced`,
 * `trip.repriced`, `standby.priced` do it in other tabs (2 s window).
 */
import { infiniteQueryOptions } from "@tanstack/react-query";
import { goInfiniteQueryFn, goNextPageParam, type GoPage } from "@/lib/goQuery";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";

export const INCOME_PAGE_SIZE = 200;

export interface StopChargeDTO {
    stopIndex: number;
    destinationCode: string;
    baseRateThb: number;
    finalRateThb: number;
}

/** The `billing` block of a trip (internal/billing `TripBilling` + `billingAxisDate`). */
export interface TripBillingDTO {
    billingPartyId: string | null;
    customerId: string | null;
    billingDate: string | null;
    priced: boolean;
    estimateThb: number | null;
    unpricedReason: string | null;
    baseRateThb: number | null;
    stopChargeThb: number | null;
    rateEntryId: string | null;
    rateImportId: string | null;
    lookupHubCode: string | null;
    lookupDestinationCode: string | null;
    fuelAdjustmentId: string | null;
    rateMultiplier: number | null;
    addThbPerTrip: number | null;
    fuelEffectiveFromDate: string | null;
    roundEffectiveFromDate: string | null;
    fuelBandLowerThb: number | null;
    fuelBandUpperThb: number | null;
    referenceFuelPriceThb: number | null;
    isMultiDelivery: boolean;
    manualOverride: boolean;
    jobCategoryAtPricing: string | null;
    computedBy: string | null;
    computedAt: string | null;
    stops: StopChargeDTO[];
    billingAxisDate: string | null;
}

export interface PlaceDTO {
    raw: string | null;
    key: string;
    label: string;
    resolved: boolean;
}

/** `GET /v1/trips` item for staff (internal/operations `TripView`, the fields this page reads). */
export interface IncomeTripDTO {
    id: string;
    tenantId: string;
    tripNo: string;
    status: string;
    jobType: string;
    jobCategory: "PRIMARY" | "SUPPLEMENTARY" | null;
    taskId: string | null;
    driverId: string | null;
    driverName: string | null;
    origin: string | null;
    originPlace: PlaceDTO;
    destination: string | null;
    destinationPlace: PlaceDTO;
    truckId: string | null;
    truckLicensePlate: string | null;
    vehicleClass: string | null;
    truckType?: string | null;
    deliveredAt: string | null;
    isMultiDelivery: boolean;
    createdAt: string;
    billing?: TripBillingDTO;
}

/** `GET /v1/standby` item (internal/operations `StandbyListView`, the fields this page reads). */
export interface IncomeStandbyDTO {
    id: string;
    tenantId: string;
    driverId: string | null;
    driverName: string | null;
    taskId: string | null;
    tripId: string | null;
    jobCategory: string | null;
    startLocation: string | null;
    endLocation: string | null;
    startedAt: string | null;
    endedAt: string | null;
    durationMinutes: number | null;
    truckLicensePlate: string | null;
    status: string;
    customerPartyId: string | null;
    createdAt: string;
    billing?: {
        estimateThb: number | null;
        partyId: string | null;
        rateSource: string | null;
        rateEntryId: string | null;
        effectiveFromDate: string | null;
        unpricedReason: string | null;
        computedAt: string | null;
        billingTenantId: string | null;
    };
}

export type IncomeFilter = {
    /** Inclusive Bangkok days, `yyyy-MM-dd`. */
    from: string;
    to: string;
    /** A billing party id, or `all`. */
    party: string;
};

type IncomeKey = readonly ["income", IncomeFilter & { kind: "trips" | "standby" }];

export function incomeTripsQuery(filter: IncomeFilter, enabled = true) {
    return infiniteQueryOptions({
        queryKey: queryKeys.income({ ...filter, kind: "trips" as const }) as IncomeKey,
        queryFn: goInfiniteQueryFn<IncomeTripDTO, IncomeKey>("/v1/trips", {
            query: ([, f]) => ({
                axis: "delivered",
                from: f.from || undefined,
                to: f.to || undefined,
                customerId: f.party && f.party !== "all" ? f.party : undefined,
                limit: INCOME_PAGE_SIZE,
            }),
        }),
        initialPageParam: undefined as string | undefined,
        getNextPageParam: goNextPageParam,
        ...QUERY_POLICY.entity,
        refetchOnWindowFocus: false,
        enabled: enabled && Boolean(filter.from && filter.to),
    });
}

export function incomeStandbyQuery(filter: IncomeFilter, enabled = true) {
    return infiniteQueryOptions({
        queryKey: queryKeys.income({ ...filter, kind: "standby" as const }) as IncomeKey,
        queryFn: goInfiniteQueryFn<IncomeStandbyDTO, IncomeKey>("/v1/standby", {
            query: ([, f]) => ({
                sort: "ended",
                from: f.from || undefined,
                to: f.to || undefined,
                customerId: f.party && f.party !== "all" ? f.party : undefined,
                limit: INCOME_PAGE_SIZE,
            }),
        }),
        initialPageParam: undefined as string | undefined,
        getNextPageParam: goNextPageParam,
        ...QUERY_POLICY.entity,
        refetchOnWindowFocus: false,
        enabled: enabled && Boolean(filter.from && filter.to),
    });
}

export function pageItems<T>(pages: GoPage<T>[] | undefined): T[] {
    return pages?.flatMap((p) => p.data ?? []) ?? [];
}

// --- the income rows ----------------------------------------------------------------------------

export type IncomeRecordType = "trip" | "multi_drop" | "standby";

/** One row of the income table: a priced trip or a priced standby record. */
export interface IncomeRow {
    id: string;
    recordType: IncomeRecordType;
    tripId: string | null;
    standbyId: string | null;
    taskId: string | null;
    label: string;
    customerId: string | null;
    partyId: string | null;
    origin: string;
    destination: string;
    vehicleClass: string | null;
    jobCategory: "PRIMARY" | "SUPPLEMENTARY" | null;
    baseRateThb: number | null;
    rateMultiplier: number | null;
    addThbPerTrip: number | null;
    estimateThb: number | null;
    deliveredAt: string | null;
    billingDate: string | null;
    rateImportId: string | null;
    fuelAdjustmentId: string | null;
    fuelEffectiveFromDate: string | null;
    manualOverride: boolean;
    unpricedReason: string | null;
    driverName: string | null;
    durationMinutes: number | null;
    rateEntryId?: string | null;
    lookupHubCode?: string | null;
    lookupDestinationCode?: string | null;
    referenceFuelPriceThb?: number | null;
    fuelBandLowerThb?: number | null;
    fuelBandUpperThb?: number | null;
    stops?: StopChargeDTO[];
    stopChargeThb?: number | null;
    rateSource?: string | null;
    rateEffectiveFromDate?: string | null;
    truckLicensePlate?: string | null;
}

export function tripIncomeRow(t: IncomeTripDTO): IncomeRow {
    const b = t.billing;
    return {
        id: t.id,
        recordType: t.isMultiDelivery || b?.isMultiDelivery ? "multi_drop" : "trip",
        tripId: t.id,
        standbyId: null,
        taskId: t.taskId,
        label: t.tripNo,
        customerId: b?.customerId ?? null,
        partyId: b?.billingPartyId ?? null,
        origin: t.originPlace?.label || t.origin || "",
        destination: t.destinationPlace?.label || t.destination || "",
        vehicleClass: t.vehicleClass,
        jobCategory: (b?.jobCategoryAtPricing as IncomeRow["jobCategory"]) ?? t.jobCategory,
        baseRateThb: b?.baseRateThb ?? null,
        rateMultiplier: b?.rateMultiplier ?? null,
        addThbPerTrip: b?.addThbPerTrip ?? null,
        estimateThb: b?.estimateThb ?? null,
        deliveredAt: t.deliveredAt,
        billingDate: b?.billingAxisDate ?? null,
        rateImportId: b?.rateImportId ?? null,
        fuelAdjustmentId: b?.fuelAdjustmentId ?? null,
        fuelEffectiveFromDate: b?.fuelEffectiveFromDate ?? null,
        manualOverride: b?.manualOverride ?? false,
        unpricedReason: b?.unpricedReason ?? (b && !b.priced ? "not_computed" : null),
        driverName: t.driverName,
        durationMinutes: null,
        rateEntryId: b?.rateEntryId,
        lookupHubCode: b?.lookupHubCode,
        lookupDestinationCode: b?.lookupDestinationCode,
        referenceFuelPriceThb: b?.referenceFuelPriceThb,
        fuelBandLowerThb: b?.fuelBandLowerThb,
        fuelBandUpperThb: b?.fuelBandUpperThb,
        stops: b?.stops,
        stopChargeThb: b?.stopChargeThb,
        truckLicensePlate: t.truckLicensePlate,
    };
}

export function standbyIncomeRow(s: IncomeStandbyDTO, customerIdOfParty: (party: string | null) => string | null): IncomeRow {
    const b = s.billing;
    const party = b?.partyId ?? s.customerPartyId;
    return {
        id: `standby_${s.id}`,
        recordType: "standby",
        tripId: s.tripId,
        standbyId: s.id,
        taskId: s.taskId,
        label: [s.driverName, s.truckLicensePlate].filter(Boolean).join(" · "),
        customerId: customerIdOfParty(party),
        partyId: party,
        origin: s.startLocation ?? "",
        destination: s.endLocation ?? "",
        vehicleClass: null,
        jobCategory: null,
        baseRateThb: null,
        rateMultiplier: null,
        addThbPerTrip: null,
        estimateThb: b?.estimateThb ?? null,
        deliveredAt: s.endedAt,
        billingDate: null,
        rateImportId: null,
        fuelAdjustmentId: null,
        fuelEffectiveFromDate: null,
        manualOverride: false,
        unpricedReason: b?.unpricedReason ?? null,
        driverName: s.driverName,
        durationMinutes: s.durationMinutes,
        rateEntryId: b?.rateEntryId,
        rateSource: b?.rateSource,
        rateEffectiveFromDate: b?.effectiveFromDate,
        truckLicensePlate: s.truckLicensePlate,
    };
}

/**
 * The customer ids the loaded pages name, to look up before the rows are built (`useCustomerLookup`): a
 * trip's billing customer (else its party), a standby record's billing party (it carries no customer id).
 */
export function incomeCustomerRefs(tripPages: GoPage<IncomeTripDTO>[] | undefined, standbyPages: GoPage<IncomeStandbyDTO>[] | undefined): (string | null)[] {
    return [
        ...pageItems(tripPages).map((t) => t.billing?.customerId ?? t.billing?.billingPartyId ?? null),
        ...pageItems(standbyPages).map((s) => s.billing?.partyId ?? s.customerPartyId),
    ];
}

/**
 * The income table's rows: the loaded trips and standby records of the wanted kinds, narrowed to one
 * record type when asked, newest delivery first. Standby rows name their customer through its billing party.
 */
export function incomeRows(
    tripPages: GoPage<IncomeTripDTO>[] | undefined,
    standbyPages: GoPage<IncomeStandbyDTO>[] | undefined,
    opts: { trips: boolean; standby: boolean; recordType: "all" | IncomeRecordType; customerOfParty: (party: string | null) => string | null }
): IncomeRow[] {
    const out: IncomeRow[] = [];
    if (opts.trips) for (const tr of pageItems(tripPages)) out.push(tripIncomeRow(tr));
    if (opts.standby) for (const s of pageItems(standbyPages)) out.push(standbyIncomeRow(s, opts.customerOfParty));
    const typed = opts.recordType === "all" ? out : out.filter((r) => r.recordType === opts.recordType);
    return typed.sort((a, b) => (b.deliveredAt ?? "").localeCompare(a.deliveredAt ?? ""));
}

/** The rows without a stored price (the missing-billing tab of a user who cannot read `/v1/billing/rows`). */
export function unpricedIncomeRows(rows: IncomeRow[]): IncomeRow[] {
    return rows.filter((r) => r.estimateThb == null);
}

export interface IncomeSummary {
    total: number;
    count: number;
    average: number;
    byCustomer: { customerId: string; count: number; total: number }[];
}

/** Totals over the priced rows given (the page passes the rows it has loaded and says so). */
export function incomeSummary(rows: IncomeRow[]): IncomeSummary {
    let total = 0;
    let count = 0;
    const by = new Map<string, { customerId: string; count: number; total: number }>();
    for (const r of rows) {
        if (r.estimateThb == null) continue;
        total += r.estimateThb;
        count += 1;
        const id = r.customerId ?? "";
        const cur = by.get(id) ?? { customerId: id, count: 0, total: 0 };
        cur.count += 1;
        cur.total += r.estimateThb;
        by.set(id, cur);
    }
    return {
        total,
        count,
        average: count > 0 ? total / count : 0,
        byCustomer: [...by.values()].sort((a, b) => b.total - a.total),
    };
}
