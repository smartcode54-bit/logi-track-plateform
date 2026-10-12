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

/** The purposes the web uploads (internal/storage `Purposes`). */
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
    | "tenant_document"
    // T42: the expense photos and the maintenance files (variants image, receipt, invoice).
    | "expense_receipt"
    | "expense_odometer"
    | "maintenance_file"
    // T34: a trip's evidence photo (variant = its photo type) and a backfilled standby record's two photos.
    | "trip_photo"
    | "standby_photo";

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

const IMAGE_TYPES = ["image/jpeg", "image/png", "image/webp"] as const;
const DOCUMENT_TYPES = [...IMAGE_TYPES, "application/pdf"] as const;

/**
 * The content types Go's presign accepts per purpose (internal/storage `Purposes`: `imageTypes`,
 * `documentTypes`), for the purposes a form checks before it writes its entity. A purpose missing here is
 * left to the presign.
 */
export const UPLOAD_CONTENT_TYPES: Partial<Record<UploadPurpose, readonly string[]>> = {
    expense_receipt: IMAGE_TYPES,
    expense_odometer: IMAGE_TYPES,
    maintenance_file: DOCUMENT_TYPES,
    trip_photo: IMAGE_TYPES,
    standby_photo: IMAGE_TYPES,
};

/** `UPLOAD_MAX_BYTES`' default (developer-spec.md §16.1); a server with a lower cap still refuses at presign. */
export const UPLOAD_MAX_BYTES_DEFAULT = 10 * 1024 * 1024;

/** The `accept` of a file input for a purpose (its content types), or undefined when the purpose is unchecked. */
export function uploadAccept(purpose: UploadPurpose): string | undefined {
    return UPLOAD_CONTENT_TYPES[purpose]?.join(",");
}

/** Why a file cannot be uploaded for a purpose, as the presign would say it; null when it can. */
export function uploadRefusal(file: Pick<File, "type" | "name" | "size">, purpose: UploadPurpose): "content_type" | "too_large" | null {
    const types = UPLOAD_CONTENT_TYPES[purpose];
    if (types && !types.includes(uploadContentType(file))) return "content_type";
    if (file.size > UPLOAD_MAX_BYTES_DEFAULT) return "too_large";
    return null;
}

/** A file refused before any request (the entity is not written, so nothing is left half done). */
export class UploadRefusedError extends Error {
    constructor(
        readonly fileName: string,
        readonly reason: "content_type" | "too_large"
    ) {
        super(`upload refused: ${fileName} (${reason})`);
        this.name = "UploadRefusedError";
    }
}

/** Throws `UploadRefusedError` for the first file a purpose would refuse (call it before writing the entity). */
export function assertUploadable(files: { file: File | null | undefined; purpose: UploadPurpose }[]): void {
    for (const { file, purpose } of files) {
        if (!file) continue;
        const reason = uploadRefusal(file, purpose);
        if (reason) throw new UploadRefusedError(file.name, reason);
    }
}

/**
 * The entity was written but a file upload or the `PATCH` linking it failed afterwards: `entity` is the
 * stored row, so a retry attaches the files to it instead of creating a second one.
 */
export class PartialCreateError<T> extends Error {
    constructor(
        readonly entity: T,
        /** The upload or link error (an `ApiError` as a rule). */
        readonly failure: unknown
    ) {
        super("the entity was saved but its files were not attached");
        this.name = "PartialCreateError";
    }
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
