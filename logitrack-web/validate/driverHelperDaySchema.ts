import { z } from "zod";

/**
 * `driver_helper_days` — one doc per driver per non-driving "helper"/training
 * attendance day, written from the mobile helper check-in. Pays a flat
 * helperDayRateThb (config) for days with NO delivered trip (driving days pay
 * as trips instead). driverId = Auth UID.
 */
export const driverHelperDaySchema = z.object({
    id: z.string().optional(),
    driverId: z.string().min(1),
    driverName: z.string().optional(),
    /** The attendance day (Bangkok). */
    date: z.coerce.date(),
    source: z.string().optional().default("mobile_checkin"),
    note: z.string().optional(),
    createdAt: z.coerce.date().optional(),
});

export type DriverHelperDay = z.infer<typeof driverHelperDaySchema>;
