/**
 * Billing writes on trips, standby and tasks over Go (T42, P3; Appendix B §B.2.11-§B.2.14): the server
 * prices every trip (`.vibe-rules.md` "ราคาเที่ยวคิดที่ server เท่านั้น"), so the pages only ask it to.
 *
 * | Write | Route | Owner of the Go route |
 * |---|---|---|
 * | recompute one trip | `POST /v1/trips/{id}/billing/compute` `{forceRecompute?}` | T38 |
 * | manual price (EditBillingDialog) | `PUT /v1/trips/{id}/billing/manual` `{billingEstimateThb, jobCategory}` | T38 |
 * | standby customer | `PATCH /v1/standby/{id}/customer` `{customerId}` | T38 |
 * | recompute one standby | `POST /v1/standby/{id}/billing/compute` `{forceRecompute?}` | T38 |
 * | backfills, impact report | `POST /v1/jobs/billing.backfill-trips|backfill-standby|impact-report` -> `202 {jobId}` | T38 |
 * | delivered time | `PATCH /v1/trips/{id}` `{deliveredAt}` | T34 |
 *
 * Trip deliveredAt PATCH routes accept this field and reprice under the period lock.
 * Each write invalidates the keys it changes: the month's billing prefix and `['income']` (R89: never the
 * whole cache).
 */
import type { QueryClient } from "@tanstack/react-query";
import { goFetch, goPath, isApiError } from "@/lib/goFetch";
import { getQueryClient } from "@/lib/queryClient";
import { queryKeys } from "@/lib/queryKeys";
import { invalidateBillingMonth } from "./billing.go";

/** `POST /v1/trips/{id}/billing/compute` response (shape kept from the callable). */
export interface ComputeBillingResult {
    ok: boolean;
    skipped?: boolean;
    blocked?: boolean;
    billingEstimateThb?: number;
    error?: string;
    invoiceNumber?: string;
    blockedInvoiceNumber?: string;
    billingDateMoved?: boolean;
}

/** The month a write touches, so its billing prefix can be invalidated (`null`: income only). */
export interface BillingMonthRef {
    customerId: string;
    year: number;
    month: number;
}

async function afterBillingWrite(month: BillingMonthRef | null, client: QueryClient) {
    await Promise.all([
        client.invalidateQueries({ queryKey: ["income"] }),
        month ? invalidateBillingMonth(month.customerId, month.year, month.month, client) : client.invalidateQueries({ queryKey: ["billing"] }),
    ]);
}

/**
 * `skipInvalidate` leaves the cache alone (a loop over many trips invalidates once, at its end). A forced recompute
 * whose old or new period is sent or paid is `409 billing_period_locked` with `details.blockedInvoiceNumber` (T38,
 * main spec §6.10 state 6); it is answered here in the callable's shape (`{ok: false, blocked: true,
 * blockedInvoiceNumber}`), which the pages count as blocked rather than failed. The refused call wrote nothing, but it
 * still refreshes unless told to skip: a caller's earlier `skipInvalidate` calls may have written.
 */
export async function computeTripBilling(
    tripId: string,
    opts: { forceRecompute?: boolean; month?: BillingMonthRef | null; skipInvalidate?: boolean } = {},
    client: QueryClient = getQueryClient()
): Promise<ComputeBillingResult> {
    let out: ComputeBillingResult;
    try {
        out = await goFetch<ComputeBillingResult>(goPath`/v1/trips/${tripId}/billing/compute`, {
            method: "POST",
            body: opts.forceRecompute ? { forceRecompute: true } : {},
        });
    } catch (e) {
        const locked = periodLocked(e);
        if (!locked) throw e;
        if (!opts.skipInvalidate) await afterBillingWrite(opts.month ?? null, client);
        return locked;
    }
    if (!opts.skipInvalidate) await afterBillingWrite(opts.month ?? null, client);
    return out;
}

/** The callable-shaped answer of a `409 billing_period_locked` (`blocked` even without an invoice number), or null. */
export function periodLocked(e: unknown): ComputeBillingResult | null {
    if (!isApiError(e) || e.code !== "billing_period_locked") return null;
    const invoice = e.details.blockedInvoiceNumber;
    return {
        ok: false,
        blocked: true,
        blockedInvoiceNumber: typeof invoice === "string" ? invoice : "",
        error: e.message,
    };
}

/** What a repair loop did: the counts the result line shows and the invoices that blocked it. */
export interface RepairTally {
    repriced: number;
    moved: number;
    unchanged: number;
    failed: number;
    blocked: string[];
}

/**
 * The Billing Document's repair (คำนวณใหม่ตามใบงาน): a forced recompute of each trip, one at a time, none of them
 * refreshing the cache; the month's keys are refreshed once at the end whatever the last call answered (a priced
 * trip, a blocked period or an error), so the trips repriced earlier never keep their old rows on screen.
 */
export async function repairTrips(
    ids: string[],
    month: BillingMonthRef,
    onProgress: (done: number) => void = () => {},
    client: QueryClient = getQueryClient()
): Promise<RepairTally> {
    const tally: RepairTally = { repriced: 0, moved: 0, unchanged: 0, failed: 0, blocked: [] };
    try {
        for (let n = 0; n < ids.length; n++) {
            try {
                const out = await computeTripBilling(ids[n], { forceRecompute: true, month, skipInvalidate: true }, client);
                if (out.blocked) {
                    if (out.blockedInvoiceNumber && !tally.blocked.includes(out.blockedInvoiceNumber)) tally.blocked.push(out.blockedInvoiceNumber);
                } else if (!out.ok) tally.failed += 1;
                else if (out.billingDateMoved) tally.moved += 1;
                else if (out.skipped) tally.unchanged += 1;
                else tally.repriced += 1;
            } catch {
                tally.failed += 1;
            }
            onProgress(n + 1);
        }
    } finally {
        if (ids.length > 0) await afterBillingWrite(month, client);
    }
    return tally;
}

/** The single manual-price exception (`accounting:override_price`, R17): `manual_override=true`. */
export async function setTripManualPrice(
    tripId: string,
    input: { billingEstimateThb: number; jobCategory: "PRIMARY" | "SUPPLEMENTARY" },
    month: BillingMonthRef | null = null,
    client: QueryClient = getQueryClient()
) {
    const out = await goFetch<unknown>(goPath`/v1/trips/${tripId}/billing/manual`, { method: "PUT", body: input });
    await afterBillingWrite(month, client);
    return out;
}

/** Assigns a standby record's customer (`customer_resolved_from='manual'`), then Go prices it. */
export async function assignStandbyCustomer(
    standbyId: string,
    customerId: string,
    month: BillingMonthRef | null = null,
    client: QueryClient = getQueryClient()
) {
    const out = await goFetch<unknown>(goPath`/v1/standby/${standbyId}/customer`, { method: "PATCH", body: { customerId } });
    await afterBillingWrite(month, client);
    return out;
}

export async function computeStandbyBilling(
    standbyId: string,
    opts: { forceRecompute?: boolean; month?: BillingMonthRef | null } = {},
    client: QueryClient = getQueryClient()
): Promise<ComputeBillingResult> {
    const out = await goFetch<ComputeBillingResult>(goPath`/v1/standby/${standbyId}/billing/compute`, {
        method: "POST",
        body: opts.forceRecompute ? { forceRecompute: true } : {},
    });
    await afterBillingWrite(opts.month ?? null, client);
    return out;
}

/** A trip's delivered time (RFC 3339); a priced trip is repriced, or refused with `409 billing_period_locked`. */
export async function patchTripDeliveredAt(tripId: string, deliveredAt: string, client: QueryClient = getQueryClient(), previousDeliveredAt?: string | null) {
    if (previousDeliveredAt && new Date(previousDeliveredAt).getTime() === new Date(deliveredAt).getTime()) return;
    const out = await goFetch<unknown>(goPath`/v1/trips/${tripId}`, { method: "PATCH", body: { deliveredAt } });
    await Promise.all([afterBillingWrite(null, client), client.invalidateQueries({ queryKey: queryKeys.trips.all() })]);
    return out;
}

// --- jobs ----------------------------------------------------------------------------------------

export interface BackfillInput {
    fromDateStr: string;
    toDateStr: string;
    customerId?: string;
    forceRecompute?: boolean;
    maxScan?: number;
    maxWrite?: number;
}

/** The result of a finished backfill job (`job.result`). */
export interface BackfillResult {
    scanned: number;
    eligible?: number;
    written: number;
    skipped: number;
    failed: number;
    blocked?: number;
    blockedInvoices?: string[];
    failures?: { tripId?: string; standbyId?: string; id?: string; tripNo?: string; driverName?: string; truckLicensePlate?: string; error?: string }[];
    capped?: boolean;
}

export type BillingJobType = "billing.backfill-trips" | "billing.backfill-standby" | "billing.impact-report";

/** Starts a billing job (`202 {jobId}`); the page follows it through `useJob(id)`. */
export function startBillingJob(type: BillingJobType, input: BackfillInput | { fromDateStr: string; toDateStr: string; maxScan?: number }) {
    const body = Object.fromEntries(Object.entries(input).filter(([, v]) => v !== undefined && v !== ""));
    return goFetch<{ jobId: string }>(`/v1/jobs/${type}`, { method: "POST", body });
}
