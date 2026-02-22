import { z } from "zod";

/** One cached row: driving distance/duration from a SOC to a Hub (Google Distance Matrix) */
export const socHubDistanceSchema = z.object({
    socId: z.string(),
    hubId: z.string(),
    distanceMeters: z.number(),
    distanceKm: z.number(),
    durationSeconds: z.number(),
    durationMinutes: z.number(),
    socLat: z.number(),
    socLng: z.number(),
    hubLat: z.number(),
    hubLng: z.number(),
    createdBy: z.string().optional(),
    updatedBy: z.string().optional(),
    createdAt: z.unknown().optional(),
    updatedAt: z.unknown().optional(),
});

export type SocHubDistance = z.infer<typeof socHubDistanceSchema>;

/** Document ID: socId_hubId (ต้นทาง SOC _ ปลายทาง Hub) */
export function socHubDistanceDocId(socId: string, hubId: string): string {
    return `${socId}_${hubId}`;
}
