/**
 * Maps a Go maintenance record (`GET /v1/maintenance`, canonical lower_snake statuses, object keys) onto
 * the legacy `MaintenanceData` shape the shared presentational components render (`MaintenanceForm`,
 * `MaintenanceHistoryList`), and the form's values back onto the Go request (T42). Files become
 * `GET /v1/files?key=` URLs through the BFF.
 */
import { bangkokWallTime, maintenanceDay } from "./maintenanceDates";
import type { MaintenanceData } from "@/validate/maintenanceSchema";
import { maintenanceFileHref, type MaintenanceInput, type MaintenanceRecordDTO, type MaintenanceStatus } from "../api/maintenance.go";

const TO_LEGACY: Record<string, MaintenanceData["status"]> = {
    pm_booking: "PM Booking",
    scheduled: "Scheduled",
    in_progress: "in_progress",
    completed: "completed",
    cancelled: "cancelled",
};

const TO_GO: Record<string, MaintenanceStatus> = {
    "PM Booking": "pm_booking",
    Scheduled: "scheduled",
    in_progress: "in_progress",
    completed: "completed",
    cancelled: "cancelled",
};

export function legacyStatus(status: string): MaintenanceData["status"] {
    return TO_LEGACY[status] ?? "in_progress";
}

export function goStatus(status: MaintenanceData["status"] | string): MaintenanceStatus {
    return TO_GO[status] ?? (status as MaintenanceStatus);
}

/** A Bangkok wall time `yyyy-MM-dd'T'HH:mm` of an instant (the form's datetime-local value). */
function wallTime(iso: string | null): string | undefined {
    if (!iso) return undefined;
    return bangkokWallTime(iso) || undefined;
}

const n = (v: number | null | undefined) => (v == null ? undefined : v);

/**
 * The form's pickup appointment: the stored instant, else the booked day and time (a PM booked on the
 * overview stores `startDate` + `appointmentTime` only). The Firestore page's `pickupAppointmentFromRecord`
 * rule: without it the form shows no appointment and its save sends `appointmentTime: null`, which clears
 * the booking time.
 */
export function pickupAppointmentOf(r: Pick<MaintenanceRecordDTO, "pickupAppointmentAt" | "appointmentTime" | "startDate">): string | undefined {
    const stored = wallTime(r.pickupAppointmentAt);
    if (stored) return stored;
    const time = r.appointmentTime?.trim();
    if (!time || !r.startDate || !/^\d{2}:\d{2}/.test(time)) return undefined;
    return `${r.startDate.slice(0, 10)}T${time.slice(0, 5)}`;
}

export function toMaintenanceData(r: MaintenanceRecordDTO): MaintenanceData {
    const href = (keys: string[] | undefined) => (keys ?? []).map(maintenanceFileHref);
    return {
        id: r.id,
        truckId: r.truckId ?? "",
        type: r.type === "CM" ? "CM" : "PM",
        serviceType: r.serviceType,
        startDate: r.startDate,
        endDate: r.endDate ?? undefined,
        status: legacyStatus(r.status),
        appointmentTime: r.appointmentTime ?? undefined,
        pickupAppointment: pickupAppointmentOf(r),
        costLabor: n(r.costLaborThb),
        costParts: n(r.costPartsThb),
        totalCost: n(r.totalCostThb),
        provider: r.provider ?? undefined,
        providerLat: n(r.providerLat),
        providerLng: n(r.providerLng),
        paymentMethod: (r.paymentMethod as MaintenanceData["paymentMethod"]) ?? undefined,
        currentMileage: n(r.currentMileage),
        nextServiceMileage: n(r.nextServiceMileage),
        images: href(r.files?.images),
        receipts: href(r.files?.receipts),
        invoices: href(r.files?.invoices),
        invoiceUrl: href(r.files?.invoices)[0],
        driverSubmitted: r.driverSubmitted,
        invoiceAmount: n(r.invoiceAmountThb),
        notes: r.notes ?? undefined,
        createdAt: r.createdAt,
        updatedAt: r.updatedAt,
    };
}

/** The values of the shared maintenance form. */
export interface MaintenanceFormValuesGo {
    type: "PM" | "CM";
    serviceType: string;
    status: MaintenanceData["status"];
    startDate: string;
    endDate: string;
    pickupAppointment: string;
    costLabor: string;
    costParts: string;
    currentMileage: string;
    nextServiceMileage: string;
    provider: string;
    providerPos: { lat: number; lng: number } | null;
    paymentMethod: string;
    notes: string;
}

/**
 * The Go body of a form save: absent fields stay unchanged on a `PATCH`, blank numbers clear; a completed
 * PM without a next-service mileage gets the current mileage plus the truck's PM interval (the Firestore
 * page's rule).
 */
export function maintenanceInputOf(v: MaintenanceFormValuesGo, truckId: string, pmIntervalKm: number, original?: MaintenanceRecordDTO): MaintenanceInput {
    const num = (s: string) => {
        const x = parseFloat(s);
        return Number.isFinite(x) ? x : null;
    };
    const current = num(v.currentMileage);
    let next = num(v.nextServiceMileage);
    if (v.type === "PM" && v.status === "completed" && current != null && next == null) next = current + pmIntervalKm;
    // A booking time and pickup time can differ. Keep the stored booking unless the pickup field changed.
    const pickupUnchanged = original != null && v.pickupAppointment === (pickupAppointmentOf(original) ?? "");
    const appointmentTime = pickupUnchanged
        ? original.appointmentTime ?? null
        : v.pickupAppointment.slice(11, 16) || null;
    return {
        truckId,
        type: v.type,
        serviceType: v.serviceType,
        status: goStatus(v.status),
        startDate: maintenanceDay(v.startDate),
        endDate: v.status === "completed" && v.endDate ? maintenanceDay(v.endDate) : null,
        pickupAppointmentAt: v.pickupAppointment ? `${v.pickupAppointment.length === 16 ? `${v.pickupAppointment}:00` : v.pickupAppointment}` : null,
        appointmentTime,
        ...maintenanceCostInputOf(v, original),
        provider: v.provider.trim() || null,
        providerLat: v.providerPos?.lat ?? null,
        providerLng: v.providerPos?.lng ?? null,
        paymentMethod: v.paymentMethod || null,
        currentMileage: current != null ? Math.round(current) : null,
        nextServiceMileage: next != null ? Math.round(next) : null,
        notes: v.notes.trim() || null,
    };
}

/** An unrelated edit must not replace a stored total with a derived empty breakdown. */
export function maintenanceCostInputOf(v: Pick<MaintenanceFormValuesGo, "costLabor" | "costParts">, original?: Pick<MaintenanceRecordDTO, "costLaborThb" | "costPartsThb" | "totalCostThb">): MaintenanceInput {
    const number = (value: string) => value.trim() === "" ? null : Number(value);
    const labor = number(v.costLabor);
    const parts = number(v.costParts);
    if (original && labor === original.costLaborThb && parts === original.costPartsThb) return {};
    const out: MaintenanceInput = { totalCostThb: labor == null && parts == null ? null : (labor ?? 0) + (parts ?? 0) };
    if (!original || labor !== original.costLaborThb) out.costLaborThb = labor;
    if (!original || parts !== original.costPartsThb) out.costPartsThb = parts;
    return out;
}
