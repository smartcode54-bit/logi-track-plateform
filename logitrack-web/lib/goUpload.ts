/**
 * Browser uploads to Go storage (developer-spec.md §9.4, §9.11; Appendix B `POST /v1/uploads/presign`).
 *
 * 1. `POST /v1/uploads/presign {purpose, entityId, contentType, sizeBytes, variant?, fileName?}` through
 *    the BFF returns `{key, url, method, headers}`.
 * 2. The file goes to `url` with exactly `headers` and its own `Content-Length` (the browser sets it
 *    for a `File` body): an absolute presigned URL on the S3 backend, or a path on the Go API
 *    (`/v1/uploads/local/...`) on the local backend, which the browser calls through the BFF as
 *    `/api/go` + path.
 * 3. The caller puts `key` in the entity's write (`logoKey`, `files`, `documentKeys`, ...), which
 *    commits it in the entity transaction.
 *
 * Every purpose but `user_photo` names an existing entity (`entityId`), so a form that creates an
 * entity with files writes the entity first, uploads, then sends the keys in a `PATCH`.
 */
import { ApiError, apiErrorFromResponse, networkError } from "./apiError";
import { GO_API_PREFIX, goFetch } from "./goFetch";

/** The purposes the master-data pages upload (internal/storage `Purposes`). */
export type UploadPurpose =
    | "customer_logo"
    | "company_logo"
    | "company_stamp"
    | "company_signature"
    | "driver_profile"
    | "driver_id_card"
    | "driver_license"
    | "truck_photo"
    | "truck_document"
    | "truck_receipt"
    | "insurance_document"
    | "tenant_document";

export interface UploadOptions {
    purpose: UploadPurpose;
    /** The entity the file belongs to (the key embeds it; the entity write commits it). */
    entityId: string;
    /** A purpose variant (`tenant_document`: `id_card`, `company_doc`, `other`). */
    variant?: string;
    signal?: AbortSignal;
}

export interface PresignResponse {
    key: string;
    url: string;
    method: string;
    headers?: Record<string, string>;
    expiresAt?: string;
    storageBackend?: string;
}

/** The content type Go is told: the browser's, or one derived from the extension when it is empty. */
export function uploadContentType(file: Pick<File, "type" | "name">): string {
    if (file.type) return file.type;
    const ext = file.name.split(".").pop()?.toLowerCase() ?? "";
    return (
        { jpg: "image/jpeg", jpeg: "image/jpeg", png: "image/png", webp: "image/webp", pdf: "application/pdf" }[ext] ??
        "application/octet-stream"
    );
}

/** Where the browser sends the bytes: a Go API path goes through the BFF, an absolute URL as is. */
export function uploadTarget(url: string): string {
    return url.startsWith("/v1/") ? `${GO_API_PREFIX}${url}` : url;
}

/** Presigns, uploads and returns the object key to put in the entity's write. */
export async function uploadToGo(file: File, options: UploadOptions): Promise<string> {
    const presign = await goFetch<PresignResponse>("/v1/uploads/presign", {
        method: "POST",
        signal: options.signal,
        body: {
            purpose: options.purpose,
            entityId: options.entityId,
            contentType: uploadContentType(file),
            sizeBytes: file.size,
            variant: options.variant,
            fileName: file.name,
        },
    });
    let res: Response;
    try {
        res = await fetch(uploadTarget(presign.url), {
            method: presign.method || "PUT",
            headers: presign.headers ?? {},
            body: file,
            signal: options.signal,
            // The local backend's route is same-origin; the S3 URL needs no cookie either.
            credentials: "same-origin",
        });
    } catch (err) {
        throw networkError(err);
    }
    // 412 on a retried S3 PUT: the object is already stored (main spec §9.4 step 2).
    if (!res.ok && res.status !== 412) {
        if (res.headers.get("Content-Type")?.includes("json")) throw await apiErrorFromResponse(res);
        throw new ApiError({ status: res.status, code: "unavailable", message: `upload failed (${res.status})` });
    }
    return presign.key;
}
