import { ApiError } from "@/lib/apiError";

// storage/keys.go purposes and storage/service.go DefaultUploadMaxBytes.
export const TRUCK_IMAGE_ACCEPT = "image/jpeg,image/png,image/webp";
export const TRUCK_DOCUMENT_ACCEPT = `${TRUCK_IMAGE_ACCEPT},application/pdf`;
export const TRUCK_UPLOAD_MAX_BYTES = 10 * 1024 * 1024;
export type TruckUploadPurpose = "truck_photo" | "truck_document" | "insurance_document" | "truck_receipt";

export function truckUploadError(file: File, purpose: TruckUploadPurpose): string | null {
    const accepted = purpose === "truck_photo" ? TRUCK_IMAGE_ACCEPT : TRUCK_DOCUMENT_ACCEPT;
    if (!accepted.split(",").includes(file.type)) return purpose === "truck_photo" ? "trucks.upload.imageOnly" : "trucks.upload.documentTypes";
    if (file.size <= 0 || file.size > TRUCK_UPLOAD_MAX_BYTES) return "trucks.upload.sizeError";
    return null;
}

export function validateTruckUpload(file: File, purpose: TruckUploadPurpose, field: string): void {
    const key = truckUploadError(file, purpose);
    if (key) throw new ApiError({ status: 422, code: "invalid_argument", message: key,
        details: { fields: [{ field, reason: key.endsWith("sizeError") ? "out_of_range" : "invalid", params: {} }] } });
}

/** Go keys retain their content extension even when the signed URL has a query string. */
export function truckFileType(ref: string): "image" | "pdf" {
    return /\.pdf(?:[?#]|$)/i.test(decodeURIComponent(ref)) ? "pdf" : "image";
}
