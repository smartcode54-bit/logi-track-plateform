import { z } from "zod";

// --- Status (Driver workflow state machine) ---
export const TRIP_STATUS_ENUM = [
    "loading",
    "departure",
    "in_transit",
    "incident",
    "delivered",
] as const;

// --- Job Type (auto-detected from origin: Hub = first_mile, SOC = line_haul) ---
export const TRIP_JOB_TYPE_ENUM = ["FIRST_MILE", "LINE_HAUL"] as const;

// --- Photo type (Loading: pre-close, closing, seal, runsheet + extra handover pages; Delivery: pre_open, opening, empty_container, runsheet_received) ---
export const TRIP_PHOTO_TYPE_ENUM = [
    "pre_close",
    "closing",
    "seal",
    "runsheet",
    "runsheet_extra_1",
    "runsheet_extra_2",
    "runsheet_extra_3",
    "pre_open",
    "opening",
    "empty_container",
    "runsheet_received",
] as const;

// --- Geocoding metadata (overlay on in-app camera photos) ---
export const tripPhotoGeocodingSchema = z.object({
    lat: z.number().optional(),
    lng: z.number().optional(),
    address: z.string().optional(),
    timestamp: z.date().optional().or(z.string().optional()), // Overlay timestamp
});

// --- Single photo entry (URL, type, geocoding) ---
export const tripPhotoSchema = z.object({
    url: z.string().url("Photo URL must be valid"),
    type: z.enum(TRIP_PHOTO_TYPE_ENUM),
    geocoding: tripPhotoGeocodingSchema.optional(),
});

// --- OCR data from Shopee screenshot (tripId LTQ..., sealCode SPX..., route info) ---
export const tripOcrDataSchema = z.object({
    tripId: z.string().optional(), // Pattern: LTQ...
    sealCode: z.string().optional(), // Pattern: SPX...
    secondarySealCode: z.string().optional(),
    routeInfo: z.string().optional(), // Extracted from Shopee screenshot
    partnerCode: z.string().optional(), // e.g. TTP, JWT from LH-XXX / FM-XXX on runsheet OCR
    supplierCode: z.string().optional(),
    releaseTime: z.string().optional(),
    sealSource: z.enum(["scanned", "ocr_fallback"]).optional(),
    ocrProfile: z.enum(["spx_runsheet", "zx_waybill", "seal"]).optional(),
});

// --- Main Trip Record schema (TripRecords collection - SSOT for Web Dashboard & Billing) ---
export const tripRecordSchema = z.object({
    id: z.string().optional(),

    status: z.enum(TRIP_STATUS_ENUM).default("loading"),
    jobType: z.enum(TRIP_JOB_TYPE_ENUM),

    photos: z.array(tripPhotoSchema).optional().default([]),

    ocrData: tripOcrDataSchema.optional(),

    // Cross-reference (e.g. First Mile task, SPX Runsheet)
    spxTripId: z.string().optional(),
    taskId: z.string().optional(),

    // ต้นทาง / ปลายทาง (จากฟอร์มบันทึกรับงาน)
    origin: z.string().optional(),
    destination: z.string().optional(),

    // Seal code (จากฟอร์มหรือ OCR)
    sealCode: z.string().optional(),

    // ช่องทาง / พาร์ทเนอร์มอบงาน (จาก OCR รันชีท LH-XXX / FM-XXX)
    partnerCode: z.string().optional(),

    // Delivery completion (เมื่อส่งงานเสร็จ)
    deliveredTimestamp: z.date().optional(),
    deliveredLat: z.number().optional(),
    deliveredLng: z.number().optional(),

    // Audit
    createdAt: z.date().optional(),
    updatedAt: z.date().optional(),
});

export type TripStatus = (typeof TRIP_STATUS_ENUM)[number];
export type TripJobType = (typeof TRIP_JOB_TYPE_ENUM)[number];
export type TripPhotoType = (typeof TRIP_PHOTO_TYPE_ENUM)[number];
export type TripPhotoGeocoding = z.infer<typeof tripPhotoGeocodingSchema>;
export type TripPhoto = z.infer<typeof tripPhotoSchema>;
export type TripOcrData = z.infer<typeof tripOcrDataSchema>;
export type TripRecord = z.infer<typeof tripRecordSchema>;
