import { z } from "zod";

export const PAYROLL_STATUS_ENUM = ["DRAFT", "PENDING_APPROVAL", "APPROVED", "PAID", "CANCELLED"] as const;

export const payrollLineItemSchema = z.object({
    id: z.string().optional(),
    type: z.enum(["EARNING", "DEDUCTION"]),
    category: z.string(), // e.g., "BASE_SALARY", "TRIP_COMMISSION", "FUEL_INCENTIVE", "TAX", "SOCIAL_SECURITY"
    name: z.string(),
    amount: z.number(),
    description: z.string().optional(),
    referenceId: z.string().optional(), // ID of the trip or fuel record if applicable
});

export const payrollSchema = z.object({
    id: z.string().optional(),
    driverId: z.string().min(1, "Driver ID is required"),
    driverName: z.string().optional(),
    periodStart: z.coerce.date(),
    periodEnd: z.coerce.date(),
    /** Pay period key "YYYY-MM" + semi-monthly round (driver compensation). */
    payPeriod: z.string().optional(),
    round: z.enum(["R1", "R2"]).optional(),
    status: z.enum(PAYROLL_STATUS_ENUM).default("DRAFT"),
    
    lineItems: z.array(payrollLineItemSchema).default([]),
    
    totalEarnings: z.number().default(0),
    totalDeductions: z.number().default(0),
    netPay: z.number().default(0),
    
    currency: z.string().default("THB"),
    
    paymentDate: z.coerce.date().optional(),
    paymentMethod: z.string().optional(),
    remarks: z.string().optional(),
    
    approvedBy: z.string().optional(),
    approvedAt: z.coerce.date().optional(),
    
    createdAt: z.coerce.date().optional(),
    updatedAt: z.coerce.date().optional(),
});

export type Payroll = z.infer<typeof payrollSchema>;
export type PayrollLineItem = z.infer<typeof payrollLineItemSchema>;
export type PayrollStatus = (typeof PAYROLL_STATUS_ENUM)[number];
