import { z } from "zod";

export const DRIVER_STATUS_ENUM = ["Active", "Inactive", "On-Duty"] as const;
export const EMPLOYMENT_TYPE_ENUM = ["FULL_TIME", "SUBCONTRACTOR", "PART_TIME"] as const;

export const driverSchema = z.object({
    id: z.string().optional(),

    /** Firebase Auth UID – trip_records.driverId references this */
    authId: z.string().optional(),

    /**
     * Past Auth UIDs this driver was linked to before an email / Google re-link
     * moved them to the current `authId`. Historical trip_records / vehicle_expenses
     * / standby_records store whichever uid was current when written, so name
     * resolution indexes these too (see lib/driverName.ts `driverIdentityKeys`) —
     * otherwise old trips show a raw uid instead of the driver's name. Appended
     * automatically by server callables whenever a re-link changes `authId`.
     */
    authIdHistory: z.array(z.string()).optional(),

    // Personal Info
    firstName: z.string({ error: "drivers.validation.firstName" }).trim().min(1, "drivers.validation.firstName"),
    lastName: z.string({ error: "drivers.validation.lastName" }).trim().min(1, "drivers.validation.lastName"),
    /** Full Thai name (ชื่อ-สกุล ภาษาไทย) — single combined field */
    fullNameTh: z.string({ error: "drivers.validation.fullNameTh" }).trim().min(1, "drivers.validation.fullNameTh"),
    mobile: z.string({ error: "drivers.validation.mobile" }).trim().min(1, "drivers.validation.mobile"),
    email: z.string().email("drivers.validation.email").optional().or(z.literal("")),
    profileImage: z.string().optional(),
    birthDate: z.coerce.date({ error: "drivers.validation.birthDate" })
        .refine((date) => {
            const today = new Date();
            const age = today.getFullYear() - date.getFullYear();
            const m = today.getMonth() - date.getMonth();
            if (m < 0 || (m === 0 && today.getDate() < date.getDate())) {
                return age - 1 >= 20 && age - 1 <= 55;
            }
            return age >= 20 && age <= 55;
        }, "drivers.validation.age"),

    // Identity & License
    idCard: z.string({ error: "drivers.validation.idCard" }).length(13, "drivers.validation.idCard"),
    idCardExpiredDate: z.coerce.date().optional(),
    idCardImage: z.string().optional(),
    truckLicenseId: z.string({ error: "drivers.validation.license" }).length(8, "drivers.validation.license"),
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

    // Compensation / payroll (driver compensation feature)
    /** วันเริ่มงาน — derive new/existing (SSO base) + probation start */
    hireDate: z.coerce.date().optional(),
    /** ผ่านทดลองงาน → เริ่มหักประกันสังคม (OQ8 manual flag) */
    probationPassed: z.boolean().optional().default(false),

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

    /**
     * The truck this driver is responsible for RIGHT NOW — set by mobile at check-in from the
     * task's truck, cleared when the trip completes. Distinct from currentAssignment (the driver's
     * home truck): a driver may run a different vehicle for a single job.
     * Fuel/other-expense forms and the maintenance Firestore rule read this first.
     */
    activeTruck: z.object({
        truckId: z.string(),
        truckPlate: z.string(),
        taskId: z.string(),
        startedAt: z.any().optional(),
    }).optional().nullable(),

    // Home truck binding — written by the Truck Assignment page. A DEFAULT at assign time, never a filter.
    currentTruckId: z.string().optional().nullable(), // legacy
    currentAssignment: z.object({
        truckId: z.string(),
        truckPlate: z.string(),
        truckModel: z.string().optional(),
        assignedAt: z.any(),
        assignmentId: z.string()
    }).optional().nullable(),

    createdAt: z.coerce.date().optional(),
    updatedAt: z.coerce.date().optional(),
});

export type Driver = z.infer<typeof driverSchema>;
