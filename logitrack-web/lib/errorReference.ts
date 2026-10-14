import { isApiError } from "./apiError";

/**
 * The reference support can look a failure up by (UI3D: app/app/error.tsx, app/global-error.tsx): Next's digest of a
 * server error, else the request id of a failed Go call; "" when there is none. Never the message, which is for
 * developers and may name internals.
 */
export function errorReference(error: (Error & { digest?: string }) | null | undefined): string {
    if (!error) return "";
    if (typeof error.digest === "string" && error.digest) return error.digest;
    if (isApiError(error) && error.requestId) return error.requestId;
    return "";
}
