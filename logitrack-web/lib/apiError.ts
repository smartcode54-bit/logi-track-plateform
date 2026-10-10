/**
 * The typed error of every Go call made through the BFF (developer-spec.md §2.5, §10.6; Appendix B
 * §B.1.5; R48, R76). Go and the BFF answer errors with exactly
 * `{"error": {"code", "message", "details", "requestId"}}`; the UI branches on `code` and renders
 * the text from the en/th catalogues (`apiError.<code>` keys, `context/locales/{en,th}/apiErrors.ts`)
 * through `apiErrorText`. `message` is only a fallback for an unknown code.
 */

export type ApiErrorDetails = Record<string, unknown>;

export interface ApiErrorInit {
    status: number;
    code: string;
    message: string;
    details?: ApiErrorDetails;
    requestId?: string;
}

export class ApiError extends Error {
    /** HTTP status; 0 when the request never got a response (network failure). */
    readonly status: number;
    /** Stable code of Appendix B §B.1.5, or one of the client codes below. */
    readonly code: string;
    readonly details: ApiErrorDetails;
    /** `X-Request-Id` of the failed call ("" when there was no response), for support and logs. */
    readonly requestId: string;

    constructor(init: ApiErrorInit) {
        super(init.message || init.code);
        this.name = "ApiError";
        this.status = init.status;
        this.code = init.code;
        this.details = init.details ?? {};
        this.requestId = init.requestId ?? "";
    }
}

export function isApiError(error: unknown): error is ApiError {
    return error instanceof ApiError;
}

/**
 * Codes raised by the browser itself, never by Go: the request got no response, or the response
 * was not the envelope (e.g. an HTML error page of a proxy in front of the web).
 */
export const CLIENT_ERROR_CODES = ["network_error", "bad_response"] as const;

/**
 * Every code the UI must be able to render: Appendix B §B.1.5 plus the client codes. The en/th
 * catalogues hold one `apiError.<code>` key per entry (checked by `lib/apiError.test.ts`).
 */
export const API_ERROR_CODES = [
    // 400
    "bad_request",
    "header_not_allowed",
    // 401
    "unauthenticated",
    "invalid_credentials",
    "token_expired",
    "session_revoked",
    "invalid_token",
    "invalid_signature",
    // 403
    "permission_denied",
    "tenant_required",
    "not_task_owner",
    "truck_not_in_tenant",
    "no_account",
    "account_disabled",
    "driver_profile_required",
    "password_change_required",
    // 404, 405
    "not_found",
    "method_not_allowed",
    // 409
    "already_exists",
    "failed_precondition",
    "idempotency_conflict",
    "duplicate_trip_id",
    "duplicate_seal",
    "duplicate_tax_invoice",
    "duplicate_destination",
    "billing_period_locked",
    "frozen_snapshot",
    "payroll_locked",
    "statement_not_draft",
    "announcement_immutable",
    // 413
    "payload_too_large",
    // 422
    "invalid_argument",
    "no_customer",
    "no_rate",
    "no_vehicle_class",
    "no_billing_date",
    "no_ended_at",
    "tenant_orphan",
    "capability_not_overridable",
    "too_many_scopes",
    // 423, 426, 429
    "locked",
    "version_blocked",
    "resource_exhausted",
    // 500, 503
    "internal",
    "unavailable",
    "bridge_unavailable",
    // client
    ...CLIENT_ERROR_CODES,
] as const;

/** The catalogue key of a code; `apiError.unknown` is the generic text. */
export const apiErrorKey = (code: string) => `apiError.${code}`;

function isRecord(value: unknown): value is Record<string, unknown> {
    return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** The ApiError for a fetch that threw before any response (offline, DNS, connection reset). */
export function networkError(cause: unknown): ApiError {
    return new ApiError({
        status: 0,
        code: "network_error",
        message: cause instanceof Error ? cause.message : "network error",
    });
}

/**
 * Reads a non-2xx response into an ApiError. The Go/BFF envelope is used as is; any other body is
 * `unavailable` for a 5xx (a proxy page while the web or api restarts) and `bad_response` otherwise.
 */
export async function apiErrorFromResponse(res: Response): Promise<ApiError> {
    const headerId = res.headers.get("X-Request-Id") ?? "";
    let body: unknown;
    try {
        body = JSON.parse(await res.text());
    } catch {
        body = undefined;
    }
    const e = isRecord(body) ? body.error : undefined;
    if (isRecord(e) && typeof e.code === "string" && e.code !== "") {
        return new ApiError({
            status: res.status,
            code: e.code,
            message: typeof e.message === "string" ? e.message : "",
            details: isRecord(e.details) ? e.details : {},
            requestId: typeof e.requestId === "string" && e.requestId !== "" ? e.requestId : headerId,
        });
    }
    return new ApiError({
        status: res.status,
        code: res.status >= 500 ? "unavailable" : "bad_response",
        message: `HTTP ${res.status}`,
        requestId: headerId,
    });
}

type Translate = (key: string, fallback?: string) => string;

/**
 * The user-facing text of an error in the active language: the catalogue entry of its code, else
 * the server's fallback `message`, else the generic `apiError.unknown` text.
 */
export function apiErrorText(error: unknown, t: Translate): string {
    if (isApiError(error)) {
        const text = t(apiErrorKey(error.code), "");
        if (text) return text;
        if (error.message && error.message !== error.code) return error.message;
    }
    return t(apiErrorKey("unknown"));
}
