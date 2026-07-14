import { z } from "zod";

export const SOC_DESTINATIONS = {
    "SOCE": "SOCE (บัวโรย)",
    "SOCN": "SOCN (วังน้อย)",
    "SOCW": "SOCW (สมุทรสาคร)"
} as const;

export const SOC_KEYS = ["SOCE", "SOCN", "SOCW"] as const;

/** เช็คว่า socId จาก Firestore ตรงกับ key (SOCE/SOCN/SOCW) หรือไม่ รองรับทั้ง "SOCE" และ "SOCE (Bueroi)" */
export function socIdMatchesKey(socId: string, key: string): boolean {
    const u = (socId ?? "").trim().toUpperCase();
    const k = (key ?? "").trim().toUpperCase();
    if (!k) return false;
    return u === k || u.startsWith(k + " ") || u.startsWith(k + "(");
}

/** แปลง source_id ของ SOC เป็น key มาตรฐาน SOCE/SOCN/SOCW หรือรหัส SOC อื่น (UPPERCASE). */
export function normalizeSocIdToKey(sourceId: string): string {
    const u = (sourceId ?? "").trim().toUpperCase();
    for (const key of SOC_KEYS) {
        const k = key.toUpperCase();
        if (u === k || u.startsWith(k + " ") || u.startsWith(k + "(")) return key;
    }
    return u;
}

export const TASK_STATUS_ENUM = ["Pending", "Assigned", "Checked in", "In-Transit", "Completed", "Cancelled"] as const;
export const TASK_TYPE_ENUM = ["FIRST_MILE", "LINE_HAUL"] as const;

/** หลัก/เสริม, set explicitly by admin at assign time. Absent = legacy task, billing derives it (ADR-0005/0006). */
export const TASK_JOB_CATEGORY_ENUM = ["PRIMARY", "SUPPLEMENTARY"] as const;

/** Vehicle class carried on a task. Billing reads this to select the rate card (see billingCompute). */
export const TASK_TRUCK_TYPE_ENUM = ["4W", "4WJ", "6WH", "10WH", "18WH", "VAN"] as const;
export type TaskTruckType = typeof TASK_TRUCK_TYPE_ENUM[number];

// Multi-delivery stop schema
export const deliveryStopSchema = z.object({
    index: z.number().min(1, "Stop index must be >= 1"),
    destination: z.string().min(1, "Destination is required"),
    destinationLinkedCustomerId: z.string().optional(),
    destinationLinkedCustomerName: z.string().optional(),
    destinationLinkedCustomerCode: z.string().optional(),
    destinationCustomerLinkKind: z.enum(["customer", "partner"]).optional(),
    status: z.enum(["pending", "delivered", "failed"]).default("pending"),
    deliveredAt: z.any().optional(), // Firestore Timestamp
    deliveredLat: z.number().optional(),
    deliveredLng: z.number().optional(),
    estimatedRateThb: z.number().optional(),
    dropFeeThb: z.number().optional(),
});

export type DeliveryStop = z.infer<typeof deliveryStopSchema>;

export const taskSchema = z.object({
    id: z.string().optional(),

    // Time & Location
    date: z.coerce.date(),
    dateStr: z.string().optional(), // YYYYMMDD or DDMMYYYY for sequential ID counting
    time: z.string().regex(/^([0-1]?[0-9]|2[0-3]):[0-5][0-9]$/, "Time must be in HH:MM format (e.g. 15:00)"),

    sourceHub: z.string().min(1, "Source Hub is required"), // From SPX_Hub
    destination: z.string().min(1, "Destination (SOC) is required"),
    sourceHubLinkedCustomerId: z.string().optional(),
    sourceHubLinkedCustomerName: z.string().optional(),
    sourceHubLinkedCustomerCode: z.string().optional(),
    sourceHubCustomerLinkKind: z.enum(["customer", "partner"]).optional(),
    destinationLinkedCustomerId: z.string().optional(),
    destinationLinkedCustomerName: z.string().optional(),
    destinationLinkedCustomerCode: z.string().optional(),
    destinationCustomerLinkKind: z.enum(["customer", "partner"]).optional(),

    /** หลัก/เสริม, chosen by admin at assign time. Optional — absent means billing derives it (ADR-0005/0006). */
    jobCategory: z.enum(TASK_JOB_CATEGORY_ENUM).default("PRIMARY").optional(),

    // Vehicle for this job — chosen per task, NOT derived from drivers.currentAssignment.
    // truckId is the trucks/{id} doc; licensePlate + truckType are denormalized snapshots of it.
    truckId: z.string().optional(),
    truckType: z.enum(TASK_TRUCK_TYPE_ENUM).optional(),

    // Shipment Info
    taskId: z.string().optional(),
    taskType: z.enum(TASK_TYPE_ENUM),

    // Assignment
    driverId: z.string().optional(),
    driverName: z.string().optional(),
    driverPhone: z.string().optional(),
    licensePlate: z.string().optional(),

    status: z.enum(TASK_STATUS_ENUM).default("Pending"),

    /** Per-driver queue sequence (1-based). Set on create when driverId is set. */
    runOrder: z.number().int().min(1).optional(),

    /** Check-in at pickup: set by driver on mobile */
    checkInAt: z.any().optional(),
    checkInPhotoUrl: z.string().optional(),
    checkInLat: z.number().optional(),
    checkInLng: z.number().optional(),

    /** Auth UIDs of helpers selected by the main driver at check-in (see driverCompensation helper pay). */
    helperDriverIds: z.array(z.string()).optional(),

    // Multi-delivery support (backward compatible)
    isMultiDelivery: z.boolean().optional().default(false),
    deliveryStops: z.array(deliveryStopSchema).optional(), // Only set if isMultiDelivery === true

    createdAt: z.coerce.date().optional(),
    updatedAt: z.coerce.date().optional(),
}).refine(
    (data) => {
        // If multi-delivery, require 2+ stops
        if (data.isMultiDelivery) {
            return data.deliveryStops && data.deliveryStops.length >= 2;
        }
        return true;
    },
    { message: "Multi-delivery task requires 2 or more delivery stops", path: ["deliveryStops"] }
).refine(
    (data) => {
        // No duplicate destinations within stops
        if (data.isMultiDelivery && data.deliveryStops) {
            const dests = data.deliveryStops.map((s) => s.destination);
            return new Set(dests).size === dests.length;
        }
        return true;
    },
    { message: "Duplicate destinations in delivery stops", path: ["deliveryStops"] }
);

export type Task = z.infer<typeof taskSchema>;
