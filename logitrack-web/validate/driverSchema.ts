import { z } from "zod";

export const DRIVER_STATUS_ENUM = ["Active", "Inactive", "On-Duty"] as const;
export const EMPLOYMENT_TYPE_ENUM = ["FULL_TIME", "SUBCONTRACTOR", "PART_TIME"] as const;

export const driverSchema = z.object({
    id: z.string().optional(),

    /** Firebase Auth UID – trip_records.driverId references this */
    authId: z.string().optional(),

    // Personal Info
    firstName: z.string().min(1, "First Name is required"),
    lastName: z.string().min(1, "Last Name is required"),
    /** Full Thai name (ชื่อ-สกุล ภาษาไทย) — single combined field */
    fullNameTh: z.string().min(1, "Thai full name is required"),
    mobile: z.string().min(1, "Mobile number is required"),
    email: z.string().email("Invalid email address").optional().or(z.literal("")),
    profileImage: z.string().optional(),
    birthDate: z.coerce.date()
        .refine((date) => {
            const today = new Date();
            const age = today.getFullYear() - date.getFullYear();
            const m = today.getMonth() - date.getMonth();
            if (m < 0 || (m === 0 && today.getDate() < date.getDate())) {
                return age - 1 >= 20 && age - 1 <= 55;
            }
            return age >= 20 && age <= 55;
        }, "Driver must be between 20 and 55 years old"),

    // Identity & License
    idCard: z.string().length(13, "ID Card must be exactly 13 digits"),
    idCardExpiredDate: z.coerce.date().optional(),
    idCardImage: z.string().optional(),
    truckLicenseId: z.string().length(8, "Truck License ID must be exactly 8 characters"),
    licenseType: z.enum(["บ.1", "บ.2", "บ.3", "บ.4", "ท.1", "ท.2", "ท.3", "ท.4"]).optional(),
    truckLicenseExpiredDate: z.coerce.date().optional(),
    truckLicenseImage: z.string().optional(),

    // Employment
    employmentType: z.enum(EMPLOYMENT_TYPE_ENUM).default("FULL_TIME"),
    subcontractorId: z.string().optional(),
    subcontractorName: z.string().optional(),
    contractYears: z.union([z.string(), z.number()]).optional().transform((val) => {
        if (val === "" || val === null || val === undefined) return undefined;
        const num = Number(val);
        return isNaN(num) ? undefined : num;
    }),
    employmentPeriod: z.object({
        start: z.coerce.date().optional(),
        end: z.coerce.date().optional(),
    }).optional(),

    // Status & Work
    status: z.enum(DRIVER_STATUS_ENUM).default("Active"),
    assignToProject: z.string().optional(),

    /** Customer/LSP driver IDs – each customer may have different ID types (e.g. SPX: appId, workId) for billing & internal systems */
    customerDriverIds: z.record(z.string(), z.record(z.string(), z.string())).optional(),

    // Audit Log
    statusHistory: z.array(z.object({
        status: z.string(),
        changedAt: z.any(),
        changedBy: z.string(),
        changedByName: z.string().optional(),
        reason: z.string().optional(),
        previousStatus: z.string().optional()
    })).optional().default([]),

    /** FCM token for push notifications (e.g. task assigned) */
    fcmToken: z.string().optional(),

    // Legacy/Compatibility fields
    currentTruckId: z.string().optional().nullable(),
    currentAssignment: z.object({
        truckId: z.string(),
        truckPlate: z.string(),
        assignedAt: z.any(),
        assignmentId: z.string()
    }).optional().nullable(),

    createdAt: z.coerce.date().optional(),
    updatedAt: z.coerce.date().optional(),
});

export type Driver = z.infer<typeof driverSchema>;
