/**
 * Actual maintenance bills of a P&L month (ADR 0031 §6, spec R13 + §4.3 decision 1).
 *
 * A bill counts in the month the job was COMPLETED: `endDate` → `checkOutAt` (driver submitted the
 * invoice) → `startDate`. The amount is only known at completion, and a start-dated bill would
 * reach back into a month already reviewed when the job closes later. `updatedAt` is never used —
 * it moves on every edit. The driver app dates completed repairs by `endDate` too.
 */
import { bangkokDateKey } from "@/lib/compensationCompute";
import type { PnlMaintenanceInput } from "./types";

const DATE_KEY = /^\d{4}-\d{2}-\d{2}/;

export function maintenanceBillDateKey(m: PnlMaintenanceInput): { dateKey: string; estimated: boolean } | null {
    if (m.endDate && DATE_KEY.test(m.endDate)) return { dateKey: m.endDate.slice(0, 10), estimated: false };
    if (m.checkOutAt) return { dateKey: bangkokDateKey(m.checkOutAt), estimated: true };
    if (m.startDate && DATE_KEY.test(m.startDate)) return { dateKey: m.startDate.slice(0, 10), estimated: true };
    return null;
}

const statusOf = (m: PnlMaintenanceInput) => m.status.trim().toLowerCase();
export const isCompletedMaintenance = (m: PnlMaintenanceInput) => statusOf(m) === "completed";
export const isCancelledMaintenance = (m: PnlMaintenanceInput) => statusOf(m) === "cancelled";
export const isInsuranceClaim = (m: PnlMaintenanceInput) => m.paymentMethod === "insurance_claim";

/** A bill the driver submitted (or that carries an amount) but that is not closed yet (F11). */
export function isMaintenanceNotClosed(m: PnlMaintenanceInput): boolean {
    if (isCompletedMaintenance(m) || isCancelledMaintenance(m)) return false;
    return m.driverSubmitted === true || m.amountThb > 0;
}

/** Bangkok date the open bill was submitted / started — used to list it from that month on. */
export function openMaintenanceDateKey(m: PnlMaintenanceInput): string | null {
    if (m.checkOutAt) return bangkokDateKey(m.checkOutAt);
    if (m.startDate && DATE_KEY.test(m.startDate)) return m.startDate.slice(0, 10);
    return null;
}
