import { z } from "zod";

/** One cached row: driving distance/duration from a Hub to a SOC (from Google Distance Matrix) */
export const hubSocDistanceSchema = z.object({
    hubId: z.string(),
    socId: z.string(),
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

/** Document ID for Firestore: hubId_socId */
export function hubSocDistanceDocId(hubId: string, socId: string): string {
    return `${hubId}_${socId}`;
}
