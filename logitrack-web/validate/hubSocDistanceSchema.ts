import { z } from "zod";

const HUB_DISTANCE_NETWORK_ENUM = ["SPX", "SPK"] as const;

/** One cached row: driving distance/duration from a Hub to a SOC (from Google Distance Matrix) */
export const hubSocDistanceSchema = z.object({
    hubId: z.string(),
    socId: z.string(),
    /** เครือข่ายที่ใช้จับคู่ Hub↔SOC ตอนคำนวณ (SPX vs J&T/SPK) */
    network: z.enum(HUB_DISTANCE_NETWORK_ENUM).optional(),
    distanceMeters: z.number(),
    distanceKm: z.number(),
    durationSeconds: z.number(),
    durationMinutes: z.number(),
    hubLat: z.number(),
    hubLng: z.number(),
    socLat: z.number(),
    socLng: z.number(),
    createdBy: z.string().optional(),
    updatedBy: z.string().optional(),
    createdAt: z.unknown().optional(), // Firestore Timestamp
    updatedAt: z.unknown().optional(), // Firestore Timestamp
});

export type HubSocDistance = z.infer<typeof hubSocDistanceSchema>;

/** Document ID for Firestore: ต้นทาง_ปลายทาง (origin_destination). ใช้ทั้ง Hub→SOC (hubId_socId) และ SOC→Hub (socId_hubId). */
export function hubSocDistanceDocId(originId: string, destinationId: string): string {
    return `${originId}_${destinationId}`;
}
