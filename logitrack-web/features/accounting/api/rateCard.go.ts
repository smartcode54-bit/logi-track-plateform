/**
 * The rate card over Go (T42, P3; routes of T37, Appendix B §B.2.14; developer-spec.md §10.7 row
 * `['rateCard', …]`; Appendix E §E.2.3 row "rate-card").
 *
 * One key per table, so a write reloads only what it touched (the Firestore page reloaded all seven
 * collections after each of its twelve writes, `rate-card/page.tsx:384-418`):
 * - `['rateCard','entries',cust]` (infinite, 100 per page): `GET /v1/billing/rate-entries?customerId&includeVoided`;
 * - `['rateCard','fuelAdj',cust]`: `GET /v1/billing/fuel-adjustments?customerId&includeVoided`;
 * - `['rateCard','serviceFees']`, `['rateCard','standby']`: every fee and standby rate in reach (small tables);
 * - `['fuel','monthly']`, `['fuel','daily',dayKey]`: the Bangchak snapshots (`./fuel.go.ts`).
 * `cust` is a customer id, or `all` for every customer in reach. Rate entries and fuel adjustments are
 * void-only (R16, ADR 0009): a correction is a new row with its own effective date.
 *
 * SSE `ratecard.changed {customerId, table}` (tenant:{tid}:billing) makes only the matching key stale in
 * other tabs (`rateCardChangedKeys`, lib/realtime/eventMap.ts).
 */
import { infiniteQueryOptions, queryOptions, type QueryClient } from "@tanstack/react-query";
import { goFetch, goPath } from "@/lib/goFetch";
import { goInfiniteQueryFn, goNextPageParam, type GoPage } from "@/lib/goQuery";
import { getQueryClient } from "@/lib/queryClient";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
import { ALL_CUSTOMERS, rateCardChangedKeys, type RateCardTable } from "./rateCardKeys";

export { ALL_CUSTOMERS, isRateCardTable, rateCardChangedKeys, type RateCardTable } from "./rateCardKeys";

export const RATE_ENTRIES_PATH = "/v1/billing/rate-entries";
export const FUEL_ADJUSTMENTS_PATH = "/v1/billing/fuel-adjustments";
export const SERVICE_FEES_PATH = "/v1/billing/service-fees";
export const STANDBY_RATES_PATH = "/v1/billing/standby-rates";
export const RATE_ENTRIES_PAGE_SIZE = 100;

export type JobCategory = "PRIMARY" | "SUPPLEMENTARY";

/** `GET /v1/billing/rate-entries` item (internal/billing `RateEntry`). */
export interface RateEntryDTO {
    id: string;
    tenantId: string;
    billingPartyId: string;
    customerId: string;
    importId: string;
    hubId: string;
    rawHubName: string | null;
    destinationCode: string;
    vehicleClass: string;
    rateThb: number;
    distanceKm: number | null;
    jobCategory: JobCategory | string;
    effectiveFrom: string;
    effectiveFromDateStr: string;
    importedAt: string | null;
    createdBy: string | null;
    createdAt: string;
    updatedAt: string;
    voided: boolean;
    voidedAt: string | null;
    voidedBy: string | null;
    voidedReason: string | null;
}

/** `GET /v1/billing/fuel-adjustments` item (internal/billing `FuelAdjustment`). */
export interface FuelAdjustmentDTO {
    id: string;
    tenantId: string;
    billingPartyId: string;
    customerId: string;
    effectiveFrom: string;
    effectiveFromDateStr: string;
    rateMultiplier: number;
    addThbPerTrip: number;
    referenceFuelPriceThbPerLitre: number | null;
    fuelBand: { lowerThb: number; upperThb: number } | null;
    announcementNote: string;
    fuelBandEnabled: boolean;
    fuelBandBaselineFuelFloor: number | null;
    fuelBandThbPerBaht: number | null;
    createdBy: string | null;
    createdAt: string;
    updatedAt: string;
    voided: boolean;
    voidedAt: string | null;
    voidedBy: string | null;
    voidedReason: string | null;
}

export type ServiceFeeType = "extra_stop" | "standby" | "custom" | string;
export type ServiceFeeUnit = "per_stop" | "per_event" | "per_trip" | string;

/** `GET /v1/billing/service-fees` item (internal/billing `ServiceFee`). */
export interface ServiceFeeDTO {
    id: string;
    tenantId: string;
    billingPartyId: string;
    customerId: string;
    feeType: ServiceFeeType;
    customTypeName: string | null;
    amountThb: number;
    unit: ServiceFeeUnit;
    note: string | null;
    createdAt: string;
    updatedAt: string;
}

/** `GET /v1/billing/standby-rates` item (internal/billing `StandbyRate`). */
export interface StandbyRateDTO {
    id: string;
    tenantId: string;
    billingPartyId: string;
    customerId: string;
    rateThb: number;
    effectiveFrom: string;
    effectiveFromDateStr: string;
    note: string | null;
    createdAt: string;
    updatedAt: string;
    voided: boolean;
    voidedAt: string | null;
    voidedBy: string | null;
    voidedReason: string | null;
}

/** One rate row of a manual add or an import (`RateRowInput`); `jobCategory` blank = PRIMARY. */
export interface RateRowInput {
    hubId: string;
    rawHubName?: string;
    destinationCode: string;
    vehicleClass: string;
    rateThb: number;
    distanceKm?: number | null;
    jobCategory?: JobCategory;
}

export interface CreateRateEntryInput extends RateRowInput {
    customerId: string;
    effectiveFromDateStr: string;
}

export interface ImportRateEntriesInput {
    customerId: string;
    effectiveFromDateStr: string;
    rows: RateRowInput[];
}

/** `POST /v1/billing/fuel-adjustments`: `percent` takes `pct`, `fixed_thb` `addThbPerTrip`, `band` the reference price and the band. */
export interface FuelAdjustmentInput {
    customerId: string;
    effectiveFromDateStr: string;
    mode: "percent" | "fixed_thb" | "band";
    pct?: number;
    addThbPerTrip?: number;
    referenceFuelPriceThbPerLitre?: number | null;
    fuelBandBaselineFuelFloor?: number | null;
    fuelBandThbPerBaht?: number | null;
    announcementNote?: string;
}

export interface ServiceFeeInput {
    customerId: string;
    feeType: ServiceFeeType;
    customTypeName?: string | null;
    amountThb: number;
    unit: ServiceFeeUnit;
    note?: string | null;
}

export interface StandbyRateInput {
    customerId: string;
    rateThb: number;
    effectiveFromDateStr: string;
    note?: string | null;
}

/** Whether a list should send `customerId` (absent = every customer in reach). */
function customerQuery(customerId: string) {
    return customerId && customerId !== ALL_CUSTOMERS ? customerId : undefined;
}

type EntriesKey = ReturnType<typeof queryKeys.rateCard.entries>;

/** `['rateCard','entries',cust]`: server pages of 100 (Appendix E §E.2.3: "server paged"). */
export function rateEntriesQuery(customerId: string, enabled = true) {
    return infiniteQueryOptions({
        queryKey: queryKeys.rateCard.entries(customerId),
        queryFn: goInfiniteQueryFn<RateEntryDTO, EntriesKey>(RATE_ENTRIES_PATH, {
            query: ([, , cust]) => ({ customerId: customerQuery(cust), includeVoided: true, limit: RATE_ENTRIES_PAGE_SIZE }),
        }),
        initialPageParam: undefined as string | undefined,
        getNextPageParam: goNextPageParam,
        ...QUERY_POLICY.masterData,
        enabled,
    });
}

/** Every loaded rate entry of the infinite query, in order. */
export function rateEntryRows(pages: GoPage<RateEntryDTO>[] | undefined): RateEntryDTO[] {
    return pages?.flatMap((p) => p.data ?? []) ?? [];
}

/** `['rateCard','fuelAdj',cust]`. */
export function fuelAdjustmentsQuery(customerId: string, enabled = true) {
    return queryOptions({
        queryKey: queryKeys.rateCard.fuelAdj(customerId),
        queryFn: ({ signal }) =>
            goFetch<FuelAdjustmentDTO[]>(FUEL_ADJUSTMENTS_PATH, { signal, query: { customerId: customerQuery(customerId), includeVoided: true } }),
        ...QUERY_POLICY.masterData,
        enabled,
    });
}

/** `['rateCard','serviceFees']`: every service fee in reach. */
export function serviceFeesQuery(enabled = true) {
    return queryOptions({
        queryKey: queryKeys.rateCard.serviceFees(),
        queryFn: ({ signal }) => goFetch<ServiceFeeDTO[]>(SERVICE_FEES_PATH, { signal }),
        ...QUERY_POLICY.masterData,
        enabled,
    });
}

/** `['rateCard','standby']`: every standby rate in reach, voided ones included (shown struck through). */
export function standbyRatesQuery(enabled = true) {
    return queryOptions({
        queryKey: queryKeys.rateCard.standby(),
        queryFn: ({ signal }) => goFetch<StandbyRateDTO[]>(STANDBY_RATES_PATH, { signal, query: { includeVoided: true } }),
        ...QUERY_POLICY.masterData,
        enabled,
    });
}

// --- invalidation ------------------------------------------------------------------------------

/** Makes the keys of one table change stale (exact keys: another customer's cache stays). */
export async function invalidateRateCard(table: RateCardTable, customerId: string | null | undefined, client: QueryClient = getQueryClient()) {
    await Promise.all(rateCardChangedKeys(table, customerId).map((queryKey) => client.invalidateQueries({ queryKey })));
}

// --- writes (each invalidates only the key it touched) ------------------------------------------

export async function createRateEntry(input: CreateRateEntryInput): Promise<RateEntryDTO> {
    const out = await goFetch<RateEntryDTO>(RATE_ENTRIES_PATH, { method: "POST", body: input });
    await invalidateRateCard("entries", input.customerId);
    return out;
}

export async function importRateEntries(input: ImportRateEntriesInput): Promise<{ importId: string; count: number }> {
    const out = await goFetch<{ importId: string; count: number }>(`${RATE_ENTRIES_PATH}/import`, { method: "POST", body: input });
    await invalidateRateCard("entries", input.customerId);
    return out;
}

export async function voidRateEntry(entry: Pick<RateEntryDTO, "id" | "customerId">, reason: string): Promise<RateEntryDTO> {
    const out = await goFetch<RateEntryDTO>(goPath`/v1/billing/rate-entries/${entry.id}/void`, { method: "POST", body: { reason } });
    await invalidateRateCard("entries", entry.customerId);
    return out;
}

export async function createFuelAdjustment(input: FuelAdjustmentInput): Promise<FuelAdjustmentDTO> {
    const out = await goFetch<FuelAdjustmentDTO>(FUEL_ADJUSTMENTS_PATH, { method: "POST", body: input });
    await invalidateRateCard("fuelAdj", input.customerId);
    return out;
}

export async function voidFuelAdjustment(row: Pick<FuelAdjustmentDTO, "id" | "customerId">, reason: string): Promise<FuelAdjustmentDTO> {
    const out = await goFetch<FuelAdjustmentDTO>(goPath`/v1/billing/fuel-adjustments/${row.id}/void`, { method: "POST", body: { reason } });
    await invalidateRateCard("fuelAdj", row.customerId);
    return out;
}

export async function createServiceFee(input: ServiceFeeInput): Promise<ServiceFeeDTO> {
    const out = await goFetch<ServiceFeeDTO>(SERVICE_FEES_PATH, { method: "POST", body: input });
    await invalidateRateCard("serviceFees", input.customerId);
    return out;
}

export async function patchServiceFee(id: string, input: Omit<ServiceFeeInput, "customerId">): Promise<ServiceFeeDTO> {
    const out = await goFetch<ServiceFeeDTO>(goPath`/v1/billing/service-fees/${id}`, { method: "PATCH", body: input });
    await invalidateRateCard("serviceFees", out.customerId);
    return out;
}

export async function deleteServiceFee(fee: Pick<ServiceFeeDTO, "id" | "customerId">): Promise<void> {
    await goFetch<void>(goPath`/v1/billing/service-fees/${fee.id}`, { method: "DELETE" });
    await invalidateRateCard("serviceFees", fee.customerId);
}

export async function createStandbyRate(input: StandbyRateInput): Promise<StandbyRateDTO> {
    const out = await goFetch<StandbyRateDTO>(STANDBY_RATES_PATH, { method: "POST", body: input });
    await invalidateRateCard("standby", input.customerId);
    return out;
}

export async function patchStandbyRate(id: string, input: Omit<StandbyRateInput, "customerId">): Promise<StandbyRateDTO> {
    const out = await goFetch<StandbyRateDTO>(goPath`/v1/billing/standby-rates/${id}`, { method: "PATCH", body: input });
    await invalidateRateCard("standby", out.customerId);
    return out;
}

export async function voidStandbyRate(row: Pick<StandbyRateDTO, "id" | "customerId">, reason: string): Promise<StandbyRateDTO> {
    const out = await goFetch<StandbyRateDTO>(goPath`/v1/billing/standby-rates/${row.id}/void`, { method: "POST", body: { reason } });
    await invalidateRateCard("standby", row.customerId);
    return out;
}

// --- pure helpers --------------------------------------------------------------------------------

/** `+5.00%` for a multiplier of 1.05 (the Firestore page's display). */
export function multiplierPercentText(multiplier: number): string {
    const pct = (multiplier - 1) * 100;
    return `${pct >= 0 ? "+" : ""}${pct.toFixed(2)}%`;
}

export interface RateEntryFilter {
    hubId?: string;
    destinationCode?: string;
    vehicleClass?: string;
    q?: string;
    showVoided?: boolean;
}

/**
 * The loaded entries that match the page's table filters. The server pages by customer only
 * (`?customerId&includeVoided`), so hub, destination, class and search narrow the loaded pages; the
 * page says how many entries are loaded and offers "load more".
 */
export function filterRateEntries(rows: RateEntryDTO[], f: RateEntryFilter): RateEntryDTO[] {
    const q = (f.q ?? "").trim().toLowerCase();
    return rows.filter((r) => {
        if (!f.showVoided && r.voided) return false;
        if (f.hubId && f.hubId !== "all" && r.hubId !== f.hubId) return false;
        if (f.destinationCode && f.destinationCode !== "all" && r.destinationCode !== f.destinationCode) return false;
        if (f.vehicleClass && f.vehicleClass !== "all" && r.vehicleClass !== f.vehicleClass) return false;
        if (q) {
            const hay = [r.hubId, r.rawHubName ?? "", r.destinationCode, r.vehicleClass, r.importId].join(" ").toLowerCase();
            if (!hay.includes(q)) return false;
        }
        return true;
    });
}
