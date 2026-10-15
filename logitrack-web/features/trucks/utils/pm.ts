import type { TruckDTO } from "../api/trucks";

export const PM_WARNING_DAYS = 30;
export const PM_WARNING_KM = 2000;
export type PMStatus = "not_configured" | "normal" | "due" | "overdue";
type Schedule = Pick<TruckDTO, "nextServiceDate" | "nextServiceMileage" | "currentMileage">;

/** Dates use the Bangkok calendar; a mileage schedule needs both readings, including zero. */
export function classifyPM(truck: Schedule, now = new Date()): { status: PMStatus; km: number | null; days: number | null } {
    const today = new Intl.DateTimeFormat("en-CA", { timeZone: "Asia/Bangkok", year: "numeric", month: "2-digit", day: "2-digit" }).format(now);
    const days = truck.nextServiceDate ? Math.round((Date.parse(truck.nextServiceDate) - Date.parse(today)) / 86400000) : null;
    const km = truck.nextServiceMileage != null && truck.currentMileage != null ? truck.nextServiceMileage - truck.currentMileage : null;
    const validDays = days != null && Number.isFinite(days) ? days : null;
    const status: PMStatus = validDays == null && km == null ? "not_configured"
        : validDays != null && validDays < 0 || km != null && km < 0 ? "overdue"
        : validDays != null && validDays <= PM_WARNING_DAYS || km != null && km <= PM_WARNING_KM ? "due" : "normal";
    return { status, km, days: validDays };
}

export function pmLabel(pm: ReturnType<typeof classifyPM>, t: (key: string, params?: Record<string, number>) => string): string {
    if (pm.status === "not_configured") return t("trucks.pm.notConfigured");
    if (pm.status === "overdue") return pm.km != null && pm.km < 0 ? t("trucks.ui4.pmKmOverdue", { km: -pm.km }) : t("trucks.badge.pmOverdue");
    if (pm.status === "due") return pm.km != null && pm.km <= PM_WARNING_KM ? t("trucks.ui4.pmKmDue", { km: pm.km }) : t("trucks.ui4.pmDaysDue", { days: pm.days! });
    return t("trucks.ui4.pmOk");
}
