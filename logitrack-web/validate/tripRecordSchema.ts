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
export const TRIP_JOB_TYPE_ENUM = ["first_mile", "line_haul"] as const;

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

export const tripRecordSchema = z.object({
    id: z.string().optional(),

    status: z.enum(TRIP_STATUS_ENUM).default("loading"),
    jobType: z.enum(TRIP_JOB_TYPE_ENUM),

    photos: z.array(tripPhotoSchema).optional().default([]),

    ocrData: tripOcrDataSchema.optional(),

    spxTripId: z.string().optional(),
    taskId: z.string().optional(),

    origin: z.string().optional(),
    destination: z.string().optional(),

    sealCode: z.string().optional(),

    partnerCode: z.string().optional(),

    driverId: z.string().optional(),

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

    // Billing snapshot fields (optional)
    billingEstimateThb: z.number().optional(),
    billingBaseRateThb: z.number().optional(),
    billingRateImportId: z.string().optional(),
    billingLookupHubId: z.string().optional(),
    billingLookupDestination: z.string().optional(),
    billingFuelAdjustmentId: z.string().optional(),
    billingRateMultiplier: z.number().optional(),
    billingAddThbPerTrip: z.number().optional(),
    billingEffectiveFromDateStr: z.string().optional(),
    /** Customer id used for billing lookup (matches task linked customer). */
    billingCustomerId: z.string().optional(),

    createdAt: z.any().optional(),
    updatedAt: z.any().optional(),
});

export type TripStatus = (typeof TRIP_STATUS_ENUM)[number];
export type TripJobType = (typeof TRIP_JOB_TYPE_ENUM)[number];
export type TripPhotoType = (typeof TRIP_PHOTO_TYPE_ENUM)[number];
export type TripPhotoGeocoding = z.infer<typeof tripPhotoGeocodingSchema>;
export type TripPhoto = z.infer<typeof tripPhotoSchema>;
export type TripOcrData = z.infer<typeof tripOcrDataSchema>;
export type TripRecord = z.infer<typeof tripRecordSchema>;
