import { UPLOAD_MAX_BYTES_DEFAULT, uploadContentType, type UploadPurpose } from "@/lib/goUpload";

const IMAGE_TYPES = ["image/jpeg", "image/png", "image/webp"];
const DOCUMENT_TYPES = [...IMAGE_TYPES, "application/pdf"];

export type DriverUploadPurpose = Extract<UploadPurpose, "driver_profile" | "driver_id_card" | "driver_license">;

export function driverUploadAccept(purpose: DriverUploadPurpose): string {
    return (purpose === "driver_profile" ? IMAGE_TYPES : DOCUMENT_TYPES).join(",");
}

export class DriverUploadError extends Error {
    constructor(readonly fileName: string, readonly reason: "content_type" | "too_large" | "empty") {
        super(`Driver upload refused: ${reason}`);
    }
}

/** Match Go storage's purpose types and default size cap before the driver is written. */
export function assertDriverUpload(file: File | null, purpose: DriverUploadPurpose): void {
    if (!file) return;
    const types = purpose === "driver_profile" ? IMAGE_TYPES : DOCUMENT_TYPES;
    const reason = !types.includes(uploadContentType(file)) ? "content_type"
        : file.size === 0 ? "empty" : file.size > UPLOAD_MAX_BYTES_DEFAULT ? "too_large" : null;
    if (reason) throw new DriverUploadError(file.name, reason);
}
