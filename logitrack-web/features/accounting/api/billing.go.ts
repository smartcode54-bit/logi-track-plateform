/**
 * Billing aggregates, statements and documents over Go (T42, P3; routes of T37 and T39, Appendix B
 * §B.2.14; developer-spec.md §6.14, §9.9, §10.7, §10.9; Appendix E §E.2.3 rows "billing-document",
 * "billing-result", "shopee-express-report"; R18, R69, R89).
 *
 * - `['billing','rows',cust,y,m,{types,categories,plate,vehicleClass,reviewMonth}]`: `GET /v1/billing/rows`,
 *   one request instead of the six-hop browser chain (`billing.ts:737`). `types` / `categories` choose the
 *   invoice set (`inInvoiceSet`, the lines `POST /v1/billing/statements` inserts); `plate`,
 *   `vehicleClass` and `reviewMonth` narrow only the preview (`inPreview`, `preview` totals).
 * - `['billing','missingDate',cust,y,m]`, `['billing','standbyDiag',cust,y,m]`.
 * - `['statements',{customerId,status,year,month}]` (infinite), `['statements','detail',id]` (polled 5 s
 *   while a document renders and the stream is closed; `documents.rendered` refreshes it).
 * - `['billing','shopee',cust,y,m,half]`.
 * Every write invalidates the narrowest prefix it changed; rows and missingDate are never event-invalidated.
 */
import { infiniteQueryOptions, queryOptions, type QueryClient } from "@tanstack/react-query";
import { goFetch, goPath, type QueryParams } from "@/lib/goFetch";
import { goInfiniteQueryFn, goNextPageParam, type GoPage } from "@/lib/goQuery";
import { getQueryClient } from "@/lib/queryClient";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";

export const BILLING_ROWS_PATH = "/v1/billing/rows";
export const STATEMENTS_PATH = "/v1/billing/statements";
export const STATEMENTS_PAGE_SIZE = 50;
/** The `customerId` of the every-party view (the delivered axis; never documented). */
export const ALL_PARTIES = "all";
/** The preview filters' bucket of rows without a value (plate, vehicle class, review month). */
export const FILTER_NONE = "__none__";

export const INVOICE_TYPES = ["trip", "multidrop", "standby"] as const;
export const INVOICE_CATEGORIES = ["PRIMARY", "SUPPLEMENTARY", "unverified"] as const;
export type InvoiceType = (typeof INVOICE_TYPES)[number];
export type InvoiceCategory = (typeof INVOICE_CATEGORIES)[number];

export type BillingRowType = "trip" | "multidrop_stop" | "standby";
export type RowFlag =
    | "plan_date_drifted"
    | "plan_date_out_of_period"
    | "no_plan_date"
    | "standby_plan_other_month"
    | "supp_priced_with_fuel"
    | "supp_manual_price_with_fuel"
    | "other_carrier";

/** One billed row (internal/billing `BillingRow`). */
export interface BillingRowDTO {
    id: string;
    rowType: BillingRowType | string;
    tripId: string | null;
    standbyId: string | null;
    taskId: string | null;
    tripNo: string | null;
    stopIndex: number | null;
    deliveredAt: string | null;
    billingDate: string | null;
    axisDate: string | null;
    planDate: string | null;
    actualPickupAt: string | null;
    billingDateBasis: string | null;
    estimateThb: number;
    baseRateThb: number | null;
    lookupHubCode: string;
    lookupDestinationCode: string;
    rateMultiplier: number | null;
    addThbPerTrip: number | null;
    fuelAdjustmentId: string | null;
    manualOverride: boolean;
    billingPartyId: string | null;
    customerId: string | null;
    vehicleClass: string | null;
    driverId: string | null;
    driverName: string | null;
    driverPhone: string | null;
    subcontractorName: string | null;
    truckId: string | null;
    truckLicensePlate: string | null;
    hubDisplayName: string;
    originHubCode: string;
    destinationDisplayName: string;
    jobCategory: "PRIMARY" | "SUPPLEMENTARY" | null;
    roundEffectiveFromDate: string | null;
    fuelBandLowerThb: number | null;
    fuelBandUpperThb: number | null;
    referenceFuelPriceThb: number | null;
    inInvoiceSet: boolean;
    inPreview: boolean;
    flags: (RowFlag | string)[];
}

/** An unpriced row with its reason (R62): `stored` (`no_customer`, `no_rate`, ...) or `dry_run`. */
export interface UnpricedRowDTO {
    id: string;
    rowType: "trip" | "standby" | string;
    tripId: string | null;
    standbyId: string | null;
    taskId: string | null;
    tripNo: string | null;
    deliveredAt: string | null;
    billingDate: string | null;
    planDate: string | null;
    createdAt: string;
    billingPartyId: string | null;
    customerId: string | null;
    sourceHub: string | null;
    destination: string | null;
    hubDisplayName: string;
    destinationDisplayName: string;
    vehicleClass: string | null;
    lookupHubCode: string | null;
    lookupDestinationCode: string | null;
    lookupVehicleClass: string | null;
    driverId: string | null;
    driverName: string | null;
    jobCategory: string | null;
    durationMinutes?: number | null;
    reason: string;
    reasonSource: "stored" | "dry_run" | string;
    dryRunEstimateThb: number | null;
}

export interface BillingTotalsDTO {
    grandTotal: number;
    withholdingTaxRate: number | null;
    withholdingTax: number;
    net: number;
    rowCount: number;
    tripOnlyCount: number;
    tripSubtotal: number;
    standbyCount: number;
    standbySubtotal: number;
    multiDropCount: number;
    multiDropSubtotal: number;
}

export interface FilterOptionDTO {
    value: string;
    label: string;
    count: number;
}

export interface RowIssuesDTO {
    planDateDrifted: number;
    planDateOutOfPeriod: number;
    noPlanDate: number;
    standbyPlanOtherMonth: number;
    suppPricedWithFuel: number;
    suppManualPriceWithFuel: number;
    repairTripIds: string[];
}

/** `GET /v1/billing/rows` (internal/billing `Rows`). */
export interface BillingRowsDTO {
    customerId: string;
    billingPartyId: string | null;
    year: number;
    month: number;
    basis: "plan" | "delivered" | null;
    periodFrom: string;
    periodTo: string;
    types: string[];
    categories: string[];
    rows: BillingRowDTO[];
    unpriced: UnpricedRowDTO[];
    invoice: BillingTotalsDTO;
    preview: BillingTotalsDTO;
    typeCounts: Record<string, number>;
    categoryCounts: Record<string, number>;
    issues: RowIssuesDTO;
    filterOptions: Record<string, FilterOptionDTO[]>;
    /** Invoice-set rows of the month the caller cannot see (a statement would refuse them). */
    outsideReach: number;
    /** One party, nothing outside reach, at least one row in the invoice set. */
    documentable: boolean;
}

export interface BillingRowsParams {
    types: readonly string[];
    categories: readonly string[];
    plate: string;
    vehicleClass: string;
    reviewMonth: string;
}

export const DEFAULT_ROWS_PARAMS: BillingRowsParams = {
    types: INVOICE_TYPES,
    categories: INVOICE_CATEGORIES,
    plate: "",
    vehicleClass: "",
    reviewMonth: "",
};

/** The query of `GET /v1/billing/rows` (an empty toggle list is sent: "none of them"). */
export function billingRowsQueryParams(customerId: string, year: number, month: number, p: BillingRowsParams): QueryParams {
    return {
        customerId,
        year,
        month,
        types: p.types.length > 0 ? p.types.join(",") : "",
        categories: p.categories.length > 0 ? p.categories.join(",") : "",
        plate: p.plate || undefined,
        vehicleClass: p.vehicleClass || undefined,
        reviewMonth: p.reviewMonth || undefined,
    };
}

export function billingRowsQuery(customerId: string, year: number, month: number, params: BillingRowsParams, enabled = true) {
    const keyParams = {
        types: [...params.types].sort(),
        categories: [...params.categories].sort(),
        plate: params.plate,
        vehicleClass: params.vehicleClass,
        reviewMonth: params.reviewMonth,
    };
    return queryOptions({
        queryKey: queryKeys.billing.rows(customerId, year, month, keyParams),
        queryFn: ({ signal }) =>
            goFetch<BillingRowsDTO>(BILLING_ROWS_PATH, { signal, query: billingRowsQueryParams(customerId, year, month, params) }),
        ...QUERY_POLICY.entity,
        refetchOnWindowFocus: false,
        enabled: enabled && customerId !== "" && year > 0 && month > 0,
    });
}

/** `GET /v1/billing/rows/missing-billing-date` item. */
export interface TripMissingBillingDateDTO {
    tripId: string;
    tripNo: string;
    deliveredAt: string | null;
    priced: boolean;
}

export function missingBillingDateQuery(customerId: string, year: number, month: number, enabled = true) {
    return queryOptions({
        queryKey: queryKeys.billing.missingDate(customerId, year, month),
        queryFn: ({ signal }) =>
            goFetch<TripMissingBillingDateDTO[]>(`${BILLING_ROWS_PATH}/missing-billing-date`, { signal, query: { customerId, year, month } }),
        ...QUERY_POLICY.entity,
        refetchOnWindowFocus: false,
        enabled: enabled && customerId !== "" && customerId !== ALL_PARTIES,
    });
}

/** One standby problem of `GET /v1/billing/standby-diagnostics`. */
export interface StandbyIssueDTO {
    id: string;
    reason: "no_customer" | "no_rate" | "not_computed" | "no_ended_at" | string;
    reasonSource: string;
    driverId: string | null;
    driverName: string | null;
    startLocation: string | null;
    endLocation: string | null;
    startedAt: string | null;
    endedAt: string | null;
    createdAt: string;
    durationMinutes: number | null;
    billingPartyId: string | null;
    customerId: string | null;
    dryRunEstimateThb: number | null;
}

export interface StandbyDiagnosticsDTO {
    unpriced: StandbyIssueDTO[];
    missingEndedAt: StandbyIssueDTO[];
    missingEndedAtTruncated: boolean;
}

export function standbyDiagnosticsQuery(customerId: string, year: number, month: number, enabled = true) {
    return queryOptions({
        queryKey: queryKeys.billing.standbyDiag(customerId, year, month),
        queryFn: ({ signal }) =>
            goFetch<StandbyDiagnosticsDTO>("/v1/billing/standby-diagnostics", { signal, query: { customerId, year, month } }),
        ...QUERY_POLICY.entity,
        refetchOnWindowFocus: false,
        enabled: enabled && customerId !== "",
    });
}

/** Every month of a party: the prefix the recompute, fix, manual-price and plan-date writes invalidate. */
export async function invalidateBillingMonth(customerId: string, year: number, month: number, client: QueryClient = getQueryClient()) {
    await Promise.all([
        client.invalidateQueries({ queryKey: queryKeys.billing.rows(customerId, year, month) }),
        client.invalidateQueries({ queryKey: queryKeys.billing.missingDate(customerId, year, month) }),
        client.invalidateQueries({ queryKey: queryKeys.billing.standbyDiag(customerId, year, month) }),
        // The every-party view of the same month shows the same rows.
        client.invalidateQueries({ queryKey: queryKeys.billing.rows(ALL_PARTIES, year, month) }),
        client.invalidateQueries({ queryKey: queryKeys.billing.standbyDiag(ALL_PARTIES, year, month) }),
    ]);
}

// --- statements --------------------------------------------------------------------------------

export type StatementStatus = "draft" | "sent" | "paid" | "cancelled";
export type DocumentKind = "invoice_summary_pdf" | "invoice_detail_xlsx" | "bundle_zip" | "receipt_pdf";
export type DocumentStatus = "pending" | "rendering" | "ready" | "failed";

/** One stored document of a statement (`statement_documents`). */
export interface DocumentRefDTO {
    kind: DocumentKind | string;
    status: DocumentStatus | string;
    error?: string | null;
    /** The download route `/v1/billing/statements/{id}/documents/{kind}` (302 to a signed GET). */
    url?: string | null;
    renderedAt: string | null;
    updatedAt: string;
}

/** `GET /v1/billing/statements[/{id}]` (internal/billing `Statement`). */
export interface StatementDTO {
    id: string;
    tenantId: string;
    billingPartyId: string;
    customerId: string;
    issuerCompanyId: string | null;
    invoiceNumber: string;
    customerName: string | null;
    customerCode: string | null;
    period: { year: number; month: number };
    totalAmount: number;
    /** The fraction snapshotted from the issuing company when the statement was made (R18). */
    withholdingTaxRate: number;
    withholdingTax: number;
    netAmount: number;
    tripCount: number;
    tripOnlyCount: number;
    tripSubtotal: number;
    standbyCount: number;
    standbySubtotal: number;
    multiDropCount: number;
    multiDropSubtotal: number;
    status: StatementStatus | string;
    generatedAt: string;
    sentAt: string | null;
    paidAt: string | null;
    cancelledAt: string | null;
    dueDate: string | null;
    note: string | null;
    showActualPickup: boolean;
    generatedBy: string | null;
    generatedByName: string | null;
    createdAt: string;
    updatedAt: string;
    documents?: DocumentRefDTO[];
}

export type StatementsFilter = {
    customerId: string;
    status: string;
    year: number;
    month: number;
};

type StatementsKey = ReturnType<typeof queryKeys.statements.list<StatementsFilter>>;

/** `['statements',{customerId,status,year,month}]`: newest first, keyset; `all`/0 = no filter. */
export function statementsQuery(filter: StatementsFilter) {
    return infiniteQueryOptions({
        queryKey: queryKeys.statements.list(filter),
        queryFn: goInfiniteQueryFn<StatementDTO, StatementsKey>(STATEMENTS_PATH, {
            query: ([, f]) => ({
                customerId: f.customerId && f.customerId !== "all" ? f.customerId : undefined,
                status: f.status && f.status !== "all" ? f.status : undefined,
                year: f.year > 0 ? f.year : undefined,
                month: f.year > 0 && f.month > 0 ? f.month : undefined,
                limit: STATEMENTS_PAGE_SIZE,
            }),
        }),
        initialPageParam: undefined as string | undefined,
        getNextPageParam: goNextPageParam,
        ...QUERY_POLICY.entity,
    });
}

export function statementRows(pages: GoPage<StatementDTO>[] | undefined): StatementDTO[] {
    return pages?.flatMap((p) => p.data ?? []) ?? [];
}

/** Poll interval of a statement detail while a document renders and the stream is closed. */
export const DOCUMENT_POLL_MS = 5_000;
/** The poll stops this long after the statement was last loaded with a document still rendering. */
export const DOCUMENT_POLL_LIMIT_MS = 2 * 60_000;

export function documentsPending(s: Pick<StatementDTO, "documents"> | null | undefined): boolean {
    return (s?.documents ?? []).some((d) => d.status === "pending" || d.status === "rendering");
}

/**
 * `['statements','detail',id]` (30 s stale). While a document renders and the session stream is closed it
 * polls every 5 s, until `DOCUMENT_POLL_LIMIT_MS` after `openedAt` (when the caller started watching).
 */
export function statementQuery(id: string | null, streamOpen: boolean, openedAt: number = Date.now()) {
    return queryOptions({
        queryKey: queryKeys.statements.detail(id ?? ""),
        queryFn: ({ signal }) => goFetch<StatementDTO>(goPath`/v1/billing/statements/${id ?? ""}`, { signal }),
        staleTime: 30_000,
        gcTime: QUERY_POLICY.entity.gcTime,
        enabled: Boolean(id),
        refetchInterval: (query) =>
            !streamOpen && documentsPending(query.state.data) && Date.now() - openedAt < DOCUMENT_POLL_LIMIT_MS ? DOCUMENT_POLL_MS : false,
    });
}

/** The Go route of a stored document (302 to a signed GET, 409 while not ready; `lib/goDownload.ts`). */
export function documentPath(statementId: string, kind: string): string {
    return goPath`/v1/billing/statements/${statementId}/documents/${kind}`;
}

export interface CreateStatementInput {
    customerId: string;
    year: number;
    month: number;
    types: string[];
    categories: string[];
    note?: string;
    issuerCompanyId?: string;
    showActualPickup: boolean;
}

/** `POST /v1/billing/statements`: the invoice number and the statement in one transaction; Go renders the documents. */
export async function createStatement(input: CreateStatementInput, client: QueryClient = getQueryClient()): Promise<StatementDTO> {
    const out = await goFetch<StatementDTO>(STATEMENTS_PATH, { method: "POST", body: input });
    await Promise.all([
        client.invalidateQueries({ queryKey: queryKeys.statements.all() }),
        invalidateBillingMonth(input.customerId, input.year, input.month, client),
    ]);
    return out;
}

export async function setStatementStatus(s: Pick<StatementDTO, "id">, status: StatementStatus, client: QueryClient = getQueryClient()) {
    const out = await goFetch<StatementDTO>(goPath`/v1/billing/statements/${s.id}/status`, { method: "POST", body: { status } });
    await client.invalidateQueries({ queryKey: queryKeys.statements.all() });
    return out;
}

export async function deleteStatement(s: Pick<StatementDTO, "id" | "customerId" | "period">, client: QueryClient = getQueryClient()) {
    await goFetch<void>(goPath`/v1/billing/statements/${s.id}`, { method: "DELETE" });
    await Promise.all([
        client.invalidateQueries({ queryKey: queryKeys.statements.all() }),
        invalidateBillingMonth(s.customerId, s.period.year, s.period.month, client),
    ]);
}

/** `POST /v1/billing/statements/{id}/documents/regenerate` -> `202 {jobId}`. */
export async function regenerateDocuments(id: string, showActualPickup?: boolean, client: QueryClient = getQueryClient()) {
    const out = await goFetch<{ jobId: string }>(goPath`/v1/billing/statements/${id}/documents/regenerate`, {
        method: "POST",
        body: showActualPickup === undefined ? {} : { showActualPickup },
    });
    await client.invalidateQueries({ queryKey: queryKeys.statements.detail(id) });
    return out;
}

// --- Shopee Express report -----------------------------------------------------------------------

export type ShopeeHalf = "full" | "first" | "second";

export interface ShopeeTripDTO {
    id: string;
    tripNo: string;
    deliveredAt: string | null;
    driverId: string | null;
    driverName: string;
    originDisplay: string;
    destinationDisplay: string;
    vehicleClass: string | null;
    truckLicensePlate: string | null;
    parcelCount: number | null;
    signedRunsheetPhotos: { photoType: string; fileId: string }[];
}

export interface ShopeeReportDTO {
    customerId: string;
    billingPartyId: string | null;
    year: number;
    month: number;
    half: ShopeeHalf;
    periodFrom: string;
    periodTo: string;
    trips: ShopeeTripDTO[];
    tripCount: number;
    driverCount: number;
    photoCount: number;
    missingPhotoCount: number;
}

/** `['billing','shopee',cust,y,m,half]`. */
export function shopeeReportQuery(customerId: string, year: number, month: number, half: ShopeeHalf, enabled = true) {
    return queryOptions({
        queryKey: queryKeys.billing.shopee(customerId, year, month, half),
        queryFn: ({ signal }) =>
            goFetch<ShopeeReportDTO>("/v1/billing/shopee-report", { signal, query: { customerId, year, month, half } }),
        ...QUERY_POLICY.entity,
        refetchOnWindowFocus: false,
        enabled: enabled && customerId !== "",
    });
}

/** The PDF route: `302` to the newest current render, else `202 {jobId}` while one renders (`lib/goDownload.ts`). */
export const SHOPEE_PDF_PATH = "/v1/billing/shopee-report.pdf";

export function shopeePdfQuery(customerId: string, year: number, month: number, half: ShopeeHalf): QueryParams {
    return { customerId, year, month, half };
}
