/**
 * The browser side of the BFF session routes (developer-spec.md §10.4, Appendix C §C.4.5, Appendix E
 * §E.8.3; R36-R38, R79, R80). Tokens never reach this code: the BFF sets and reads the HttpOnly
 * `lt_at` / `lt_rt` cookies; these calls only send credentials or ids and read what the BFF returns.
 *
 * | Call | Route | Notes |
 * |---|---|---|
 * | `signInWithPassword` | `POST /api/auth/login` | 200 sets both cookies; 403 `password_change_required` carries `details.passwordChangeTicket` and sets none (R79) |
 * | `googleNonce`, `signInWithGoogle` | `GET /api/auth/google/nonce`, `POST /api/auth/google` | GIS ID token + single-use nonce (Appendix C §C.4.10) |
 * | `changePasswordWithTicket` | `POST /api/go/v1/auth/password/change` | `{passwordChangeTicket, newPassword}`, no session (anonymous) |
 * | `requestPasswordReset`, `resetPassword` | `POST /api/go/v1/auth/password/forgot`, `/reset` | anonymous; forgot always 202 (R4) |
 * | `switchTenant` | `POST /api/auth/tenant` | replaces `lt_at` (R83) |
 * | `firebaseCustomToken` | `POST /api/auth/firebase-token` | the bridge (R40, R80), until TW7 |
 * | `signOutSession` | `POST /api/auth/logout` | revokes the session, expires both cookies; never throws |
 *
 * The two session-bound routes (`tenant`, `firebase-token`) send the access cookie as the bearer, so
 * they follow `goFetch`'s 401 rule: one shared refresh and one retry on `token_expired` (forced for
 * `claims_changed`) or a missing access cookie; anything else ends the session (lib/sessionEnd.ts).
 */
import { ApiError, apiErrorFromResponse, networkError } from "./apiError";
import { goFetch } from "./goFetch";
import type { LoginGeoCoords } from "./loginGeo";
import { endSession } from "./sessionEnd";
import { sharedRefresh } from "./sharedRefresh";

export const AUTH_LOGIN_PATH = "/api/auth/login";
export const AUTH_GOOGLE_PATH = "/api/auth/google";
export const AUTH_GOOGLE_NONCE_PATH = "/api/auth/google/nonce";
export const AUTH_TENANT_PATH = "/api/auth/tenant";
export const AUTH_FIREBASE_TOKEN_PATH = "/api/auth/firebase-token";
export const AUTH_SIGN_OUT_PATH = "/api/auth/logout";

/** The body the BFF returns for a sign-in: Go's answer without the two tokens (Appendix C §C.8). */
export interface SignInResult {
    tenants: { id: string; nameTh: string; nameEn: string | null; kind: string; role: string }[];
    defaultTenantId: string | null;
    expiresIn: number;
}


const REFRESHABLE = new Set(["token_expired", "unauthenticated"]);

async function send(path: string, init: RequestInit): Promise<Response> {
    try {
        return await fetch(path, { credentials: "same-origin", cache: "no-store", ...init });
    } catch (cause) {
        throw networkError(cause);
    }
}

function jsonInit(method: "GET" | "POST", body?: unknown): RequestInit {
    const headers: Record<string, string> = { Accept: "application/json" };
    if (body === undefined) return { method, headers };
    headers["Content-Type"] = "application/json";
    return { method, headers, body: JSON.stringify(body) };
}

/** `data` of a `{data}` body; `undefined` for 204 or an empty body. */
async function readData<T>(res: Response): Promise<T> {
    if (res.status === 204) return undefined as T;
    const text = await res.text();
    if (text === "") return undefined as T;
    try {
        const body = JSON.parse(text) as { data?: T };
        return body.data as T;
    } catch {
        throw new ApiError({ status: res.status, code: "bad_response", message: "response is not JSON", requestId: res.headers.get("X-Request-Id") ?? "" });
    }
}

/** A sign-in or nonce route: no session yet, so a 401 is just the answer (wrong password, ...). */
async function anonymousCall<T>(path: string, method: "GET" | "POST", body?: unknown): Promise<T> {
    const res = await send(path, jsonInit(method, body));
    if (!res.ok) throw await apiErrorFromResponse(res);
    return readData<T>(res);
}

/** A session-bound auth route: `goFetch`'s refresh-once rule over the BFF auth path. */
async function sessionCall<T>(path: string, body?: unknown): Promise<T> {
    const init = jsonInit("POST", body);
    const startedAt = Date.now();
    let res = await send(path, init);
    if (res.status === 401) {
        const first = await apiErrorFromResponse(res);
        if (!REFRESHABLE.has(first.code)) {
            endSession(first);
            throw first;
        }
        const refreshed = await sharedRefresh({ force: first.details.reason === "claims_changed", since: startedAt });
        if (!refreshed) {
            endSession(first);
            throw first;
        }
        res = await send(path, init);
        if (res.status === 401) {
            const second = await apiErrorFromResponse(res);
            endSession(second);
            throw second;
        }
    }
    if (!res.ok) throw await apiErrorFromResponse(res);
    return readData<T>(res);
}

/** `POST /api/auth/login`. Throws ApiError: `invalid_credentials`, `locked`, `account_disabled`, `password_change_required`, ... */
export function signInWithPassword(email: string, password: string): Promise<SignInResult> {
    return anonymousCall<SignInResult>(AUTH_LOGIN_PATH, "POST", { email, password });
}

/** `GET /api/auth/google/nonce`: a single-use nonce for the GIS button, valid `expiresIn` seconds. */
export function googleNonce(): Promise<{ nonce: string; expiresIn: number }> {
    return anonymousCall(AUTH_GOOGLE_NONCE_PATH, "GET");
}

/** `POST /api/auth/google` with the GIS ID token and the nonce it was issued for. */
export function signInWithGoogle(idToken: string, nonce: string): Promise<SignInResult> {
    return anonymousCall<SignInResult>(AUTH_GOOGLE_PATH, "POST", { idToken, nonce });
}

/** The single-use ticket of a `403 password_change_required` sign-in (R79), or undefined. */
export function passwordChangeTicket(error: unknown): string | undefined {
    if (!(error instanceof ApiError) || error.code !== "password_change_required") return undefined;
    const t = error.details.passwordChangeTicket;
    return typeof t === "string" && t !== "" ? t : undefined;
}

/** Redeems the ticket with the new password (`204`); the user then signs in with it (R79, Appendix C §C.4.8). */
export function changePasswordWithTicket(passwordChangeTicket: string, newPassword: string): Promise<void> {
    return goFetch<void>("/v1/auth/password/change", {
        method: "POST",
        body: { passwordChangeTicket, newPassword },
        anonymous: true,
    });
}

/** `POST /v1/auth/password/forgot`: always 202, whether or not the address has an account (R4). */
export function requestPasswordReset(email: string, locale: string): Promise<void> {
    return goFetch<void>("/v1/auth/password/forgot", { method: "POST", body: { email, locale }, anonymous: true });
}

/** `POST /v1/auth/password/reset` with the token of the emailed link (`/reset-password#token=`). */
export function resetPassword(token: string, newPassword: string): Promise<void> {
    return goFetch<void>("/v1/auth/password/reset", { method: "POST", body: { token, newPassword }, anonymous: true });
}

/** `POST /api/auth/tenant`: the session's active tenant becomes `tenantId` (204, new `lt_at`). */
export function switchTenant(tenantId: string): Promise<void> {
    return sessionCall<void>(AUTH_TENANT_PATH, { tenantId });
}

/** `POST /api/auth/firebase-token`: a Firebase custom token for `signInWithCustomToken` (R40). */
export function firebaseCustomToken(): Promise<{ customToken: string; expiresIn: number }> {
    return sessionCall(AUTH_FIREBASE_TOKEN_PATH);
}

/** `POST /api/auth/logout`: revokes this session and expires both cookies. Never throws. */
export async function signOutSession(): Promise<void> {
    try {
        await fetch(AUTH_SIGN_OUT_PATH, { method: "POST", credentials: "same-origin", cache: "no-store", keepalive: true });
    } catch {
        // The BFF clears the cookies whatever Go answers; a network failure leaves them to expire.
    }
}

/** Records the login location (`PATCH /v1/me {lastLoginGeo}`); best effort, never throws. */
export async function recordLoginGeo(geo: LoginGeoCoords | null): Promise<void> {
    if (!geo || !geo.source || !Number.isFinite(geo.lat) || !Number.isFinite(geo.lng)) return;
    if (Math.abs(geo.lat) > 90 || Math.abs(geo.lng) > 180) return;
    const lastLoginGeo: LoginGeoCoords = { lat: geo.lat, lng: geo.lng, source: geo.source };
    if (geo.accuracyM !== undefined && Number.isFinite(geo.accuracyM) && geo.accuracyM >= 0) lastLoginGeo.accuracyM = geo.accuracyM;
    try {
        await goFetch("/v1/me", { method: "PATCH", body: { lastLoginGeo } });
    } catch {
        // Location is a convenience for the session map; a failure must not disturb the sign-in.
    }
}
