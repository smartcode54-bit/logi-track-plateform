import { z } from "zod";

/** Station type for geofencing / check-in: Hub or SOC only */
export const STATION_TYPE_ENUM = ["HUB", "SOC"] as const;
export type StationType = (typeof STATION_TYPE_ENUM)[number];

export const hubSchema = z.object({
    id: z.string().optional(),
    /** Source ID (e.g. ALANG-A, SOCN – ตามใบงาน) */
    source_id: z.string().min(1, "Source ID is required"),
    /** English name (SPX Name in UI) */
    source_name_en: z.string().min(1, "Source name (EN) is required"),
    /** Latitude for Check-in / Geofencing */
    latitude: z.number().optional(),
    /** Longitude for Check-in / Geofencing */
    longitude: z.number().optional(),
    /** Station type: HUB or SOC */
    station_type: z.enum(STATION_TYPE_ENUM).default("HUB"),
    createdAt: z.any().optional(),
    updatedAt: z.any().optional(),
});

export type Hub = z.infer<typeof hubSchema>;
