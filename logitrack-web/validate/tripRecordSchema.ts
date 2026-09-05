import { z } from "zod";

// --- Status (Driver workflow state machine) ---
export const TRIP_STATUS_ENUM = [
    "in_transit",
    "incident",
    "delivered",
    "standby",
    "cancelled", // ปิด/ยกเลิกงานผิดจากเว็บ (admin) — ไม่นับรายได้ และมือถือ auto-clear งานค้าง
] as const;

// --- Job Type (auto-detected from origin: Hub = first_mile, SOC = line_haul) ---
export const TRIP_JOB_TYPE_ENUM = ["first_mile", "line_haul"] as const;

// --- Job Category (หลัก/เสริม) — a DIFFERENT axis from jobType; เสริม uses a separate
//     rate card and its billing price is frozen (see ADR-0005). Default PRIMARY. ---
export const TRIP_JOB_CATEGORY_ENUM = ["PRIMARY", "SUPPLEMENTARY"] as const;

// --- Photo type ---
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
    // Un-overlaid customer-app screenshots (ADR 0019): captured from the customer app, not the camera.
    "checkin_app", // "arrived / entered stand", copied forward from the task at loading
    "truck_release", // "ปล่อยรถ", last loading step
    "arrived", // "มาถึง", first delivery step (single delivery)
    // Multi-delivery photo types (per-stop): stop_{index}_pre_open, stop_{index}_opening, etc.
    // Pattern: stop_{index}_(arrived|pre_open|opening|empty_container|runsheet_received)
] as const;

export const tripPhotoGeocodingSchema = z.object({
    lat: z.number().optional(),
    lng: z.number().optional(),
    address: z.string().optional(),
    timestamp: z.date().optional().or(z.string().optional()),
});

export const tripPhotoSchema = z.object({
    url: z.string(),
    type: z.enum(TRIP_PHOTO_TYPE_ENUM),
    geocoding: tripPhotoGeocodingSchema.optional(),
});

export const tripOcrDataSchema = z.object({
    tripId: z.string().optional(),
    sealCode: z.string().optional(),
    routeInfo: z.string().optional(),
    partnerCode: z.string().optional(),
});

// Multi-delivery stop progress schema
export const deliveryStopProgressSchema = z.object({
    index: z.number().min(1),
    destination: z.string(),
    status: z.enum(["pending", "delivered", "failed"]).default("pending"),
    deliveredAt: z.any().optional(), // Firestore Timestamp
    deliveredLat: z.number().optional(),
    deliveredLng: z.number().optional(),
    photos: z.array(tripPhotoSchema).optional().default([]),
});

export const tripRecordSchema = z.object({
    id: z.string().optional(),

    status: z.enum(TRIP_STATUS_ENUM).default("in_transit"),
    jobType: z.enum(TRIP_JOB_TYPE_ENUM),
    /** หลัก/เสริม (inherited from the task). เสริม → separate rate card + frozen price. Default PRIMARY. */
    jobCategory: z.enum(TRIP_JOB_CATEGORY_ENUM).default("PRIMARY"),

    photos: z.array(tripPhotoSchema).optional().default([]),

    ocrData: tripOcrDataSchema.optional(),

    spxTripId: z.string().optional(),
    taskId: z.string().optional(),

    origin: z.string().optional(),
    destination: z.string().optional(),

    sealCode: z.string().optional(),

    partnerCode: z.string().optional(),

    driverId: z.string().optional(),
    // Truck snapshot — recorded at trip creation; immutable after save
    truckId: z.string().optional(),
    truckLicensePlate: z.string().optional(),
    truckType: z.string().optional(),

    // Mobile-only fields
    distance: z.string().optional(),
    parcelCount: z.number().optional(),
    sealTime: z.string().optional(),
    totalWeight: z.string().optional(),
    lat: z.number().optional(),
    lng: z.number().optional(),

    deliveredTimestamp: z.any().optional(),
    deliveredLat: z.number().optional(),
    deliveredLng: z.number().optional(),

    /** Actual departure / arrival timeline (Firestore Timestamp from mobile; client coerces to Date). */
    std: z.any().optional(),
    sta: z.any().optional(),
    ata: z.any().optional(),

    // Multi-delivery support (backward compatible)
    isMultiDelivery: z.boolean().optional().default(false),
    deliveryStopsProgress: z.array(deliveryStopProgressSchema).optional(), // Only set if isMultiDelivery === true
    totalDeliveryStops: z.number().optional(), // Total stops for this trip (cached for queries)

    // Billing snapshot fields (optional)
    billingEstimateThb: z.number().optional(),
    billingBaseRateThb: z.number().optional(),
    billingStopChargeThb: z.number().optional(), // Multi-delivery stop surcharge (stops 3+)
    billingRateImportId: z.string().optional(),
    billingLookupHubId: z.string().optional(),
    billingLookupDestination: z.string().optional(),
    billingFuelAdjustmentId: z.string().optional(),
    billingRateMultiplier: z.number().optional(),
    billingAddThbPerTrip: z.number().optional(),
    billingEffectiveFromDateStr: z.string().optional(),
    /** Customer id used for billing lookup (matches task linked customer). */
    billingCustomerId: z.string().optional(),
    // Multi-delivery billing snapshot (optional, only set when isMultiDelivery=true)
    billingIsMultiDelivery: z.boolean().optional(),
    billingMultiDeliveryBreakdown: z.array(z.object({
        stopIndex: z.number(),
        destination: z.string(),
        baseRateThb: z.number(),
        finalRateThb: z.number(),
    })).optional(),
    // Rate round + fuel band provenance (ADR 0009 §4). Denormalized at compute time: the invoice
    // must never resolve a band by following billingFuelAdjustmentId, because that row can be
    // voided or superseded and would then contradict the frozen amount printed beside it.
    /** `yyyy-MM-dd` the price of this trip last changed — the later of rate-entry / fuel-adjustment. */
    billingRoundEffectiveFromDateStr: z.string().optional(),
    /** Inclusive lower bound of the ฿1.00 diesel band, e.g. 41.01 for the band 41.01–42.00. */
    billingFuelBandLowerThb: z.number().optional(),
    /** Inclusive upper bound of the same band, e.g. 42. */
    billingFuelBandUpperThb: z.number().optional(),
    /** The announced retail diesel price the band was derived from. */
    billingReferenceFuelPriceThb: z.number().optional(),

    /** Set by sendCustomerLineNotification once the job-complete LINE message was pushed (idempotency). */
    lineDeliveredNotifiedAt: z.any().optional(),

    createdAt: z.any().optional(),
    updatedAt: z.any().optional(),
});

export type TripStatus = (typeof TRIP_STATUS_ENUM)[number];
export type TripJobType = (typeof TRIP_JOB_TYPE_ENUM)[number];
export type TripPhotoType = (typeof TRIP_PHOTO_TYPE_ENUM)[number];
export type TripPhotoGeocoding = z.infer<typeof tripPhotoGeocodingSchema>;
export type TripPhoto = z.infer<typeof tripPhotoSchema>;
export type TripOcrData = z.infer<typeof tripOcrDataSchema>;
export type DeliveryStopProgress = z.infer<typeof deliveryStopProgressSchema>;
export type TripRecord = z.infer<typeof tripRecordSchema>;
