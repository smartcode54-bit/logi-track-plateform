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

/** แปลง source_id ของ SOC เป็น key มาตรฐาน SOCE/SOCN/SOCW (ใช้ query hub_soc_distances สำหรับ SOC→Hub). */
export function normalizeSocIdToKey(sourceId: string): string {
    const u = (sourceId ?? "").trim().toUpperCase();
    for (const key of SOC_KEYS) {
        const k = key.toUpperCase();
        if (u === k || u.startsWith(k + " ") || u.startsWith(k + "(")) return key;
    }
    return sourceId;
}

export const TASK_STATUS_ENUM = ["Pending", "Assigned", "Checked in", "In-Transit", "Completed", "Cancelled"] as const;
export const TASK_TYPE_ENUM = ["FIRST_MILE", "LINE_HAUL"] as const;

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

    // Vehicle Requirements
    truckType: z.enum(["4WH", "4WJ", "6WH", "10WH", "18WH", "PICKUP", "VAN"]).optional(), // Based on image

    // Shipment Info
    taskId: z.string().optional(),
    taskType: z.enum(TASK_TYPE_ENUM),

    // Assignment
    driverId: z.string().optional(),
    driverName: z.string().optional(),
    driverPhone: z.string().optional(),
    licensePlate: z.string().optional(),

    status: z.enum(TASK_STATUS_ENUM).default("Pending"),

    /** Check-in at pickup: set by driver on mobile */
    checkInAt: z.any().optional(),
    checkInPhotoUrl: z.string().optional(),
    checkInLat: z.number().optional(),
    checkInLng: z.number().optional(),

    createdAt: z.coerce.date().optional(),
    updatedAt: z.coerce.date().optional(),
});

export type Task = z.infer<typeof taskSchema>;
