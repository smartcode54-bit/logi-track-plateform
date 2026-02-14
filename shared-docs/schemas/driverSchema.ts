import { z } from "zod";

export const DRIVER_STATUS_ENUM = ["Active", "Inactive", "On-Duty"] as const;
export const EMPLOYMENT_TYPE_ENUM = ["FULL_TIME", "SUBCONTRACTOR", "PART_TIME"] as const;

export const driverSchema = z.object({
    id: z.string().optional(),

    // Personal Info
    firstName: z.string().min(1, "First Name is required"), // ชื่อจริง
    lastName: z.string().min(1, "Last Name is required"), // นามสกุล
    mobile: z.string().min(1, "Mobile number is required"), // เบอร์โทรศัพท์
    email: z.string().email("Invalid email address").optional().or(z.literal("")), // อีเมล
    profileImage: z.string().optional(), // ลิงก์รูปโปรไฟล์จาก Storage
    birthDate: z.date()
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
    idCard: z.string().length(13, "ID Card must be exactly 13 digits"), // เลขบัตรประชาชน
    idCardExpiredDate: z.date().optional(), // วันหมดอายุบัตรประชาชน
    idCardImage: z.string().optional(), // รูปถ่ายบัตรประชาชน
    truckLicenseId: z.string().length(8, "Truck License ID must be exactly 8 characters"), // เลขที่ใบอนุญาตขับขี่รถบรรทุก
    licenseType: z.enum(["บ.1", "บ.2", "บ.3", "บ.4", "ท.1", "ท.2", "ท.3", "ท.4"]).optional(), // ประเภทใบขับขี่
    truckLicenseExpiredDate: z.date().optional(), // วันหมดอายุใบขับขี่
    truckLicenseImage: z.string().optional(), // รูปถ่ายใบขับขี่

    // Employment
    employmentType: z.enum(EMPLOYMENT_TYPE_ENUM).default("FULL_TIME"),
    subcontractorId: z.string().optional(), // ID ของ Subcontractor
    subcontractorName: z.string().optional(), // ระบุเมื่อเป็น SUBCONTRACTOR (can be derived from ID)
    contractYears: z.union([z.string(), z.number()]).optional().transform((val) => {
        if (val === "" || val === null || val === undefined) return undefined;
        const num = Number(val);
        return isNaN(num) ? undefined : num;
    }), // ระยะสัญญา (1, 2, 3 ปี)
    employmentPeriod: z.object({
        start: z.date().optional(),
        end: z.date().optional(),
    }).optional(), // สำหรับ PART_TIME

    // Status & Work
    status: z.enum(DRIVER_STATUS_ENUM).default("Active"), // เช่น Active, Inactive, On-Duty
    assignToProject: z.string().optional(), // โปรเจกต์ที่ได้รับมอบหมายในปัจจุบัน

    // Audit Log
    statusHistory: z.array(z.object({
        status: z.string(),
        changedAt: z.any(), // Firestore Timestamp
        changedBy: z.string(),
        changedByName: z.string().optional(),
        reason: z.string().optional(),
        previousStatus: z.string().optional()
    })).optional().default([]),

    // Legacy/Compatibility fields (optional or derived) - keeping for safety based on previous usage
    currentTruckId: z.string().optional().nullable(),
    currentAssignment: z.object({
        truckId: z.string(),
        truckPlate: z.string(),
        assignedAt: z.any(), // Firestore Timestamp
        assignmentId: z.string()
    }).optional().nullable(),

    createdAt: z.date().optional(),
    updatedAt: z.date().optional(),
});

export type Driver = z.infer<typeof driverSchema>;
