import { z } from "zod";

/** ID type config for a customer's driver identification (e.g. SPX: appId, workId) */
export const customerSchema = z.object({
    id: z.string().optional(),

    /** Unique code used in driver.customerDriverIds key (e.g. "SPX") */
    code: z.string().min(1, "Customer code is required"),
    /** Display name */
    name: z.string().min(1, "Customer name is required"),
    /** Optional description */
    description: z.string().optional(),
    logoUrl: z.string().optional(),

    /** Driver ID types this customer requires – defines keys in driver.customerDriverIds[customerCode] */
    driverIdTypes: z.array(z.object({
        key: z.string().min(1),
        label: z.string().min(1),
    })).default([]),

    // ── Tax / Legal (ใช้ในเอกสารใบวางบิล) ─────────────────────────────────────
    /** ที่อยู่เต็มตามนิติบุคคล (พิมพ์ในใบวางบิล) */
    address: z.string().optional(),
    /** เลขประจำตัวผู้เสียภาษี 13 หลัก */
    taxId: z.string().optional(),
    /** สำนักงานใหญ่ / สาขา */
    branchType: z.enum(["สำนักงานใหญ่", "สาขา"]).optional(),
    /** เลขที่สาขา (กรณี branchType = "สาขา", เช่น 00001) */
    branchNumber: z.string().optional(),

    // ── Billing contact ──────────────────────────────────────────────────────
    /** ชื่อผู้ติดต่อสำหรับวางบิล */
    contactName: z.string().optional(),
    /** เบอร์โทรผู้ติดต่อ */
    contactPhone: z.string().optional(),
    /** อีเมลสำหรับส่งเอกสารวางบิล */
    billingEmail: z.string().optional(),

    // ── Payment / Invoice settings ───────────────────────────────────────────
    /** ระยะเวลาชำระเงิน (วัน) เช่น 30, 45, 60 */
    paymentTermsDays: z.coerce.number().int().nonnegative().optional(),
    /** หมายเหตุที่พิมพ์ในใบวางบิล/ใบแจ้งหนี้ */
    invoiceNote: z.string().optional(),

    // ── Billing date basis (ADR 0027) ─────────────────────────────────────────
    /**
     * แกนวันที่ที่ใช้ตัดรอบบิล + เลือก rate round ของเที่ยวลูกค้ารายนี้:
     * "delivered" (ค่าเริ่มต้นเมื่อไม่ระบุ) = วันที่ส่งสำเร็จ (ADR 0008 §3);
     * "plan" = วันที่ของแผนงานบน task (`tasks.date`) สำหรับลูกค้า (เช่น CJSF) ที่ยึดแผนงานที่แจ้งวันต่อวัน
     * เพื่อให้เที่ยวที่ส่งช้าข้ามวันยังตกบิลในเดือนของแผนงาน (ADR 0027).
     */
    billingDateBasis: z.enum(["delivered", "plan"]).optional(),

    // ── Fuel pricing model (ADR 0029) ─────────────────────────────────────────
    /**
     * ไม่ระบุ = แบบเดิม (rate card เป็นราคาสุดท้าย + รอบน้ำมัน ×/±บาท).
     * "base_band_step" = rate card เป็น **ราคาฐาน** ที่ช่วงดีเซล anchor (CJSF: 42.01–44.00) แล้วระบบคิดราคาของ
     * แต่ละช่วงเอง: ช่วงละ `bandWidthThb` บาท ขยับ `stepBasisPoints` (70 = 0.7%) ทบต้น ปัดเป็น `roundToThb` บาททุกขั้น.
     */
    fuelPricing: z
        .object({
            model: z.literal("base_band_step"),
            anchorBandLowerThb: z.coerce.number(),
            bandWidthThb: z.coerce.number().positive(),
            stepBasisPoints: z.coerce.number().int().positive().lt(10000),
            roundToThb: z.coerce.number().positive(),
        })
        .optional(),

    // ── Pickup / delivery points ─────────────────────────────────────────────
    /**
     * ใช้จุดรับส่งของลูกค้ารายอื่น (doc id ใน `customers`) — เช่น JWT วิ่งงาน SPX ที่จุดเดียวกับ TTP.
     * ฟอร์มงาน FM/LH ของลูกค้ารายนี้แสดงจุดที่ผูกกับตัวเอง + จุดที่ผูกกับลูกค้ารายนั้น (ชั้นเดียว ไม่ไล่ต่อ)
     * และงานที่สร้างยังบันทึกลูกค้าของงานเอง (lib/customerPlaces.ts). ว่าง = ใช้เฉพาะจุดของตัวเอง.
     */
    usePlacesOfCustomerId: z.string().optional(),

    // ── LINE notifications ───────────────────────────────────────────────────
    /** LINE group id (ขึ้นต้นด้วย "C…") ที่จะส่งแจ้งเตือนเช็คอิน/จบงานเข้าไป — ว่าง = ปิดการแจ้งเตือน */
    lineGroupId: z.string().optional(),

    createdAt: z.coerce.date().optional(),
    updatedAt: z.coerce.date().optional(),
});

export type Customer = z.infer<typeof customerSchema>;
