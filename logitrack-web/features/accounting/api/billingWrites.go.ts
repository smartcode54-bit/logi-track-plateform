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
 * | plan date | `PATCH /v1/tasks/{id}` `{date}` | T34 (T28 shipped only the tenant reassignment) |
 * | delivered time | `PATCH /v1/trips/{id}` `{deliveredAt}` | T34 |
 *
 * The routes are the catalogue's contracts. T38 serves the trip and standby billing routes and the three billing
 * jobs. Until the other owners merge their routes fail with a Go error, which the pages show like any other
 * (measured on mv-go against the mounted route groups):
 * - plan date: `PATCH /v1/tasks/{id}` exists as T28's reassignment-only route, so `{date}` gets
 *   `422 invalid_argument` (`date: not_supported`, `tenantId: required`), or `403 permission_denied`
 *   without `operations:manage_tasks`; it can never reassign a task (unknown fields are refused);
 * - delivered time: `GET /v1/trips/:id` exists, so `PATCH /v1/trips/{id}` gets `405 method_not_allowed`.
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
 * `skipInvalidate` leaves the cache alone (a loop over many trips invalidates once, with its last call). A forced
 * recompute whose old or new period is sent or paid is `409 billing_period_locked` with
 * `details.blockedInvoiceNumber` (T38, main spec §6.10 state 6); it is answered here in the callable's shape
 * (`{ok: false, blockedInvoiceNumber}`), which the pages count as blocked rather than failed. Nothing was written, so
 * nothing is invalidated.
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
        if (locked) return locked;
        throw e;
    }
    if (!opts.skipInvalidate) await afterBillingWrite(opts.month ?? null, client);
    return out;
}

/** The callable-shaped answer of a `409 billing_period_locked`, or null for any other error. */
export function periodLocked(e: unknown): ComputeBillingResult | null {
    if (!isApiError(e) || e.code !== "billing_period_locked") return null;
    const invoice = e.details.blockedInvoiceNumber;
    return {
        ok: false,
        blockedInvoiceNumber: typeof invoice === "string" ? invoice : "",
        error: e.message,
    };
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

/** The plan date of a task (`yyyy-MM-dd`); a delivered trip's billing is restamped under the period lock. */
export async function patchTaskPlanDate(taskId: string, date: string, client: QueryClient = getQueryClient()) {
    const out = await goFetch<unknown>(goPath`/v1/tasks/${taskId}`, { method: "PATCH", body: { date } });
    await Promise.all([afterBillingWrite(null, client), client.invalidateQueries({ queryKey: queryKeys.tasks.all() })]);
    return out;
}

/** A trip's delivered time (RFC 3339); a priced trip is repriced, or refused with `409 billing_period_locked`. */
export async function patchTripDeliveredAt(tripId: string, deliveredAt: string, client: QueryClient = getQueryClient()) {
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
    failures?: { tripId?: string; standbyId?: string; id?: string; error?: string }[];
    capped?: boolean;
}

export type BillingJobType = "billing.backfill-trips" | "billing.backfill-standby" | "billing.impact-report";

/** Starts a billing job (`202 {jobId}`); the page follows it through `useJob(id)`. */
export function startBillingJob(type: BillingJobType, input: BackfillInput | { fromDateStr: string; toDateStr: string; maxScan?: number }) {
    const body = Object.fromEntries(Object.entries(input).filter(([, v]) => v !== undefined && v !== ""));
    return goFetch<{ jobId: string }>(`/v1/jobs/${type}`, { method: "POST", body });
}
