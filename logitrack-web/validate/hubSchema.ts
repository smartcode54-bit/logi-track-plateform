import { z } from "zod";

/** Station type for geofencing / check-in: Hub or SOC only */
export const STATION_TYPE_ENUM = ["HUB", "SOC"] as const;
export type StationType = (typeof STATION_TYPE_ENUM)[number];
export const CUSTOMER_LINK_KIND_ENUM = ["customer", "partner"] as const;
export type CustomerLinkKind = (typeof CUSTOMER_LINK_KIND_ENUM)[number];

export const hubSchema = z.object({
    id: z.string().optional(),
    /** Source ID (e.g. ALANG-A, SOCN – ตามใบงาน) */
    source_id: z.string().min(1, "Source ID is required"),
    /** Display name in English */
    source_name_en: z.string().min(1, "Source name (EN) is required"),
    /** Latitude for Check-in / Geofencing */
    latitude: z.number().optional(),
    /** Longitude for Check-in / Geofencing */
    longitude: z.number().optional(),
    /** Station type: HUB or SOC */
    station_type: z.enum(STATION_TYPE_ENUM).default("HUB"),
    /** Selected customer document id from customers collection */
    linkedCustomerId: z.string().optional(),
    /** Business relationship type for selected customer */
    customerLinkKind: z.enum(CUSTOMER_LINK_KIND_ENUM).optional(),
    createdAt: z.any().optional(),
    updatedAt: z.any().optional(),
});

export type Hub = z.infer<typeof hubSchema>;

/** Hub Code / source_id ลงท้าย SPX → ใช้รหัสลูกค้า "SPX" ใน task (ตรงกับ migration backfill) */
export function hubSourceIdHasSpxSuffix(sourceIdOrHubCode: string): boolean {
    const s = String(sourceIdOrHubCode ?? "").trim().toUpperCase();
    if (!s) return false;
    if (s === "SPX") return true;
    return s.endsWith("-SPX") || s.endsWith("_SPX") || s.endsWith(".SPX");
}
