import { z } from "zod";

export const LEAVE_TYPE_ENUM = ["SICK", "ANNUAL", "PERSONAL", "BUSINESS", "OTHER"] as const;
export const LEAVE_STATUS_ENUM = ["PENDING", "APPROVED", "REJECTED", "CANCELLED"] as const;

export const leaveRequestSchema = z.object({
    id: z.string().optional(),
    driverId: z.string().min(1, "Driver ID is required"),
    driverName: z.string().optional(),
    type: z.enum(LEAVE_TYPE_ENUM).default("PERSONAL"),
    status: z.enum(LEAVE_STATUS_ENUM).default("PENDING"),
    startDate: z.coerce.date(),
    endDate: z.coerce.date(),
    reason: z.string().min(1, "Reason is required"),
    approverId: z.string().optional(),
    approvedAt: z.coerce.date().optional(),
    rejectionReason: z.string().optional(),
    attachments: z.array(z.string()).optional().default([]), // URLs to medical certs, etc.
    createdAt: z.coerce.date().optional(),
    updatedAt: z.coerce.date().optional(),
});

export type LeaveRequest = z.infer<typeof leaveRequestSchema>;
export type LeaveType = (typeof LEAVE_TYPE_ENUM)[number];
export type LeaveStatus = (typeof LEAVE_STATUS_ENUM)[number];
