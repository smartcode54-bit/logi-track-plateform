import { z } from "zod";

/**
 * Zod schema for the `driver_penalties` collection — admin-entered deductions
 * (e.g. late/SLA breach) applied to a driver's payout. Supports installments
 * spanning pay rounds; the outstanding balance is carried until cleared
 * (FR15, FR15.1). Penalties feed DEDUCTION line items in `payroll`.
 */

export const PENALTY_STATUS_ENUM = [
    "pending",
    "partially_deducted",
    "cleared",
    "cancelled",
] as const;

export const driverPenaltySchema = z.object({
    id: z.string().optional(),
    /** Driver Auth UID (matches payroll.driverId / trip_records.driverId). */
    driverId: z.string().min(1, "Driver is required"),
    driverName: z.string().optional(),

    /** Penalty type code from config penaltyTypes (e.g. "LATE_SLA"). */
    typeCode: z.string().min(1),
    typeName: z.string().optional(),

    totalThb: z.number().int().nonnegative(),
    /** Outstanding balance not yet deducted. */
    remainingThb: z.number().int().nonnegative(),
    /** Number of pay rounds to spread across; 1 = deduct in full. */
    installmentsTotal: z.number().int().positive().default(1),
    installmentsPaid: z.number().int().nonnegative().default(0),

    reason: z.string().optional(),
    evidenceUrl: z.string().optional(),

    status: z.enum(PENALTY_STATUS_ENUM).default("pending"),
    incurredAt: z.coerce.date(),

    createdAt: z.coerce.date().optional(),
    updatedAt: z.coerce.date().optional(),
    createdBy: z.string().optional(),
});

export type DriverPenalty = z.infer<typeof driverPenaltySchema>;
export type PenaltyStatus = (typeof PENALTY_STATUS_ENUM)[number];

/** Scheduled deduction for one pay round = total/installments, capped at remaining. */
export function penaltyInstallmentThb(p: Pick<DriverPenalty, "totalThb" | "remainingThb" | "installmentsTotal">): number {
    const per = p.installmentsTotal > 1 ? Math.round(p.totalThb / p.installmentsTotal) : p.remainingThb;
    return Math.min(Math.max(0, per), Math.max(0, p.remainingThb));
}
