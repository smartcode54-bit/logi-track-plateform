import { z } from "zod";

export const hubSchema = z.object({
    id: z.string().optional(),
    hubId: z.string().min(1, "Hub ID is required"), // Renamed from hubCode
    hubName: z.string().min(1, "Hub Name (SPX) is required"),
    hubTHName: z.string().optional(), // Thai Name
    lat: z.number().optional(),
    lng: z.number().optional(),
    source: z.literal("custom").default("custom"),
    createdAt: z.any().optional(), // Firestore Timestamp
    updatedAt: z.any().optional(),
});

export type Hub = z.infer<typeof hubSchema>;
