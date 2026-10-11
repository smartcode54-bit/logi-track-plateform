/**
 * Callable shims to the Go API (mv-go, developer-spec.md §13.8 "Shims"; Appendix C §C.6.5; R25, R45).
 *
 * While installed 3.x APKs call the legacy callables (until P7b), a migrated callable forwards the mobile call to its
 * `/v1/mobile/*` shim target on the PUBLIC Go listener: `X-Api-Key` (a platform `api_keys` row of scope `cf_shim`) and
 * the caller's own Firebase ID token as `Authorization: Bearer` (Go verifies it and acts as that user, driver:self;
 * there is no shared token and no acting-user header). The callable keeps its response contract.
 *
 * Functions params (developer-spec.md §16.1; set before the deploy that enables a shim; names only, never values):
 *   LOGITRACK_API_BASE_URL    the public API origin; empty = every shim off
 *   LOGITRACK_API_KEY         the cf_shim key (a Secret Manager secret: firebase functions:secrets:set LOGITRACK_API_KEY)
 *   LOGITRACK_SHIM_CALLABLES  comma list of the callables that forward (each goes live in its own phase: setDriverClaims
 *                             and addDeliveryStop at P2, computeTripBillingSnapshot at P3 with billing in
 *                             PG_OWNED_DOMAINS, sendCustomerLineNotification and markBroadcastRead at P5); empty = none
 *
 * First shim (T38): computeTripBillingSnapshot -> POST /v1/mobile/trips/{id}/billing/compute. T32 adds the others.
 */

import { defineSecret, defineString } from "firebase-functions/params";

export const logitrackApiBaseUrl = defineString("LOGITRACK_API_BASE_URL", {
    default: "",
    description: "Public origin of the LogiTrack Go API (mv-go shim targets); empty keeps the legacy callables.",
});

export const logitrackApiKey = defineSecret("LOGITRACK_API_KEY");

export const logitrackShimCallables = defineString("LOGITRACK_SHIM_CALLABLES", {
    default: "",
    description: "Comma list of the callables that forward to their Go shim target (empty: none).",
});

/** Whether callable is in a LOGITRACK_SHIM_CALLABLES value (names trimmed, exact, case-sensitive). */
export function shimListed(list: string, callable: string): boolean {
    return list
        .split(",")
        .map((s) => s.trim())
        .includes(callable);
}

/** The callable's name in LOGITRACK_SHIM_CALLABLES (T38: forwarded to POST /v1/mobile/trips/{id}/billing/compute). */
export const COMPUTE_TRIP_SHIM = "computeTripBillingSnapshot";

/**
 * Whether billing is priced by the Go API (P3): the compute shim is listed and the API origin set. The legacy 15-minute
 * sweeps (autoComputeBillingOnDelivery, autoComputeStandbyBilling) then stand down: billing.compute and its safety net
 * price every delivered trip and standby record in PostgreSQL and firestore.shadow writes the billing fields back, so
 * two writers never price one trip.
 */
export function goPricesBilling(): boolean {
    return shimBaseUrl(logitrackApiBaseUrl.value()) !== "" && shimListed(logitrackShimCallables.value(), COMPUTE_TRIP_SHIM);
}

/** Bound on one shim call; the APK treats the callable as best-effort. */
export const SHIM_TIMEOUT_MS = 10_000;

/** The base URL of the shim targets, or "" when the shims are off (a trailing slash dropped). */
export function shimBaseUrl(raw: string): string {
    const v = raw.trim().replace(/\/+$/, "");
    return /^https?:\/\/[^\s/]+(\/\S*)?$/.test(v) ? v : "";
}

/** The shim target of a trip compute: `{base}/v1/mobile/trips/{id}/billing/compute`, the id path-escaped. */
export function computeTripShimUrl(base: string, tripId: string): string {
    return `${base}/v1/mobile/trips/${encodeURIComponent(tripId)}/billing/compute`;
}

/** The legacy callable's answer (fn tripBillingOnDelivered ComputeBillingResponse) the shim maps to. */
export interface ShimComputeResponse {
    ok: boolean;
    skipped?: boolean;
    error?: string;
}

/**
 * Maps a shim target's HTTP answer onto the callable contract. Go answers `{data: {ok, skipped?, error?}}` (never an
 * amount: drivers never see billing); an error envelope `{error: {code}}` becomes `{ok: false, error: code}`. The APK
 * ignores the result (delivery_trip_repository.dart:107-115), so nothing is thrown: a failed shim call is priced by the
 * Go safety net and the mirror's trip.delivered anyway.
 */
export function mapComputeShimAnswer(status: number, body: unknown): ShimComputeResponse {
    const obj = (body && typeof body === "object" ? body : {}) as Record<string, unknown>;
    if (status >= 200 && status < 300) {
        const d = (obj.data && typeof obj.data === "object" ? obj.data : {}) as Record<string, unknown>;
        const out: ShimComputeResponse = { ok: d.ok === true };
        if (d.skipped === true) out.skipped = true;
        if (typeof d.error === "string" && d.error !== "") out.error = d.error;
        return out;
    }
    const e = (obj.error && typeof obj.error === "object" ? obj.error : {}) as Record<string, unknown>;
    const code = typeof e.code === "string" && e.code !== "" ? e.code : `http_${status}`;
    return { ok: false, error: code };
}

/**
 * Whether a callable request is a mobile call a shim forwards: a driver (the claims setDriverClaims gives: role driver
 * or a driverId), never an admin, never a forced recompute (an admin action the legacy page keeps until its web flag).
 */
export function isMobileShimCall(token: Record<string, unknown> | undefined, forceRecompute: boolean): boolean {
    if (!token || forceRecompute || token.admin === true) return false;
    return token.role === "driver" || (typeof token.driverId === "string" && token.driverId !== "");
}

/** Posts one shim call; network failures and timeouts answer `{ok: false, error: "go_unavailable"}`. */
export async function postShim(url: string, apiKey: string, bearer: string, body: unknown): Promise<ShimComputeResponse> {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), SHIM_TIMEOUT_MS);
    try {
        const res = await fetch(url, {
            method: "POST",
            headers: { "Content-Type": "application/json", "X-Api-Key": apiKey, Authorization: bearer },
            body: JSON.stringify(body ?? {}),
            signal: controller.signal,
        });
        let parsed: unknown = null;
        try {
            parsed = await res.json();
        } catch {
            parsed = null;
        }
        return mapComputeShimAnswer(res.status, parsed);
    } catch {
        return { ok: false, error: "go_unavailable" };
    } finally {
        clearTimeout(timer);
    }
}
