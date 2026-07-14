import { doc, getDoc, type Firestore } from "firebase/firestore";

import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import type { Driver } from "@/validate/driverSchema";
import type { TripRecord } from "@/validate/tripRecordSchema";

const BANGKOK = "Asia/Bangkok";

function coerceToDate(val: unknown): Date | null {
    if (val == null) return null;
    if (val instanceof Date) return isNaN(val.getTime()) ? null : val;
    if (typeof (val as { toDate?: () => Date })?.toDate === "function") {
        const d = (val as { toDate: () => Date }).toDate();
        return d instanceof Date && !isNaN(d.getTime()) ? d : null;
    }
    if (typeof val === "string" || typeof val === "number") {
        const d = new Date(val);
        return isNaN(d.getTime()) ? null : d;
    }
    return null;
}

/** dd/MM/yy โดย yy = เลขท้าย พ.ศ. 2 หลัก (วันที่ตามปฏิทินในโซนกรุงเทพ) */
export function formatBuddhistShortDate(d: Date): string {
    const formatter = new Intl.DateTimeFormat("en-CA", {
        timeZone: BANGKOK,
        year: "numeric",
        month: "2-digit",
        day: "2-digit",
    });
    const parts = formatter.formatToParts(d);
    const get = (type: Intl.DateTimeFormatPartTypes) =>
        parts.find((p) => p.type === type)?.value ?? "";
    const y = parseInt(get("year"), 10);
    const month = get("month");
    const day = get("day");
    const beSuffix = ((y + 543) % 100).toString().padStart(2, "0");
    return `${day}/${month}/${beSuffix}`;
}

/** เวลา HH:mm โซนกรุงเทพ */
export function formatBangkokHm(input: Date | null | undefined): string {
    const d = input == null ? null : coerceToDate(input);
    if (!d) return "-";
    return new Intl.DateTimeFormat("en-GB", {
        timeZone: BANGKOK,
        hour: "2-digit",
        minute: "2-digit",
        hour12: false,
    }).format(d);
}

export function resolveDriverShareCode(driver: Driver | null | undefined): string {
    if (!driver) return "-";
    const nested = driver.customerDriverIds;
    if (nested && typeof nested === "object") {
        for (const outer of Object.values(nested)) {
            if (outer && typeof outer === "object") {
                for (const v of Object.values(outer)) {
                    const s = String(v).trim();
                    if (/^\d{4,}$/.test(s)) return s;
                }
            }
        }
    }
    const idCard = driver.idCard?.trim();
    if (idCard) return idCard;
    const lic = driver.truckLicenseId?.trim();
    if (lic) return lic;
    return "-";
}

/**
 * The plate that actually ran this trip. The trip's own snapshot wins: a driver runs the truck the
 * task assigned, which is not necessarily the one they are bound to now — reading the live binding
 * would restamp an old trip with today's truck.
 */
function plateForTrip(trip: TripRecord, driver: Driver | null | undefined): string {
    const fromTrip = trip.truckLicensePlate?.trim();
    if (fromTrip) return fromTrip;
    const p = driver?.currentAssignment?.truckPlate?.trim();
    return p && p.length > 0 ? p : "-";
}

function driverDisplayName(driver: Driver | null | undefined): string {
    if (!driver) return "-";
    const n = `${driver.firstName ?? ""} ${driver.lastName ?? ""}`.trim();
    return n.length > 0 ? n : "-";
}

function driverPhone(driver: Driver | null | undefined): string {
    if (!driver) return "-";
    const m = driver.mobile?.trim();
    return m && m.length > 0 ? m : "-";
}

function tripNumberLine(trip: TripRecord): string {
    const ocrId = trip.ocrData?.tripId?.trim();
    if (ocrId) return ocrId;
    const spx = trip.spxTripId?.trim();
    if (spx) return spx;
    return trip.id?.trim() || "-";
}

function parcelLine(trip: TripRecord): string {
    const n = trip.parcelCount;
    if (typeof n === "number" && Number.isFinite(n)) return String(n);
    return "-";
}

function refDateForHeader(trip: TripRecord): Date {
    const std = coerceToDate(trip.std as unknown);
    if (std) return std;
    const created = coerceToDate(trip.createdAt as unknown);
    if (created) return created;
    return new Date();
}

export type BuildTripShareTextInput = {
    trip: TripRecord;
    driver: Driver | null;
    originLabel: string;
    destLabel: string;
    partnerLine: string;
    incidentNote?: string | null;
    checkInAt: Date | null;
    truckType: string;
};

/** ข้อความรายงานเที่ยว (ภาษาไทย) — คัดลอกไปวางใน LINE เอง */
export function buildTripDeliveryShareText(p: BuildTripShareTextInput): string {
    const { trip, driver, originLabel, destLabel, partnerLine, incidentNote, checkInAt, truckType } = p;
    const dateLine = formatBuddhistShortDate(refDateForHeader(trip));
    const tripNo = tripNumberLine(trip);
    const name = driverDisplayName(driver);
    const code = resolveDriverShareCode(driver);
    const plate = plateForTrip(trip, driver);
    const phone = driverPhone(driver);
    const partner = partnerLine.trim() || "-";
    const truckTypeLine = truckType.trim() || "-";
    const parcels = parcelLine(trip);

    const checkInHm = formatBangkokHm(checkInAt);
    const departHm = formatBangkokHm(coerceToDate(trip.std as unknown));
    const arriveHm = formatBangkokHm(coerceToDate(trip.ata as unknown));
    const doneHm = formatBangkokHm(coerceToDate(trip.deliveredTimestamp as unknown));

    let notes = "-";
    if (incidentNote && incidentNote.trim()) {
        const oneLine = incidentNote.trim().replace(/\s+/g, " ");
        notes = oneLine.length > 200 ? `${oneLine.slice(0, 197)}...` : oneLine;
    }

    return [
        `วันที่ ${dateLine}`,
        `ต้นทาง : ${originLabel}`,
        `ปลายทาง : ${destLabel}`,
        `เลขทริป : ${tripNo}`,
        `ชื่อ : ${name}`,
        `รหัส : ${code}`,
        `ทะเบียนรถ : ${plate}`,
        `เบอร์โทร : ${phone}`,
        `ช่องทาง : ${partner}`,
        `ประเภทรถ : ${truckTypeLine}`,
        `จำนวนสินค้า : ${parcels}`,
        `เช็คอิน : ${checkInHm}`,
        `ออกเดินทาง : ${departHm}`,
        `ถึงปลายทาง : ${arriveHm}`,
        `จบงาน : ${doneHm}`,
        `หมายเหตุ : ${notes}`,
    ].join("\n");
}

async function fetchTaskCheckInAt(fs: Firestore, taskId: string | undefined): Promise<Date | null> {
    if (!taskId?.trim()) return null;
    try {
        const snap = await getDoc(doc(fs, COLLECTIONS.TASKS, taskId.trim()));
        if (!snap.exists()) return null;
        return coerceToDate(snap.data()?.checkInAt);
    } catch {
        return null;
    }
}

async function fetchTruckType(fs: Firestore, truckId: string | undefined): Promise<string> {
    if (!truckId?.trim()) return "-";
    try {
        const snap = await getDoc(doc(fs, COLLECTIONS.TRUCKS, truckId.trim()));
        if (!snap.exists()) return "-";
        const t = snap.data()?.type;
        return typeof t === "string" && t.trim() ? t.trim() : "-";
    } catch {
        return "-";
    }
}

export type BuildTripDeliveryShareTextParams = {
    trip: TripRecord;
    driver: Driver | null;
    originLabel: string;
    destLabel: string;
    partnerLine: string;
    incidentNote?: string | null;
    /** Defaults to shared Firestore client */
    firestore?: Firestore;
};

/** โหลด check-in + ประเภทรถ แล้วคืนข้อความสำหรับคัดลอกไป LINE */
export async function buildTripDeliveryShareTextAsync(
    params: BuildTripDeliveryShareTextParams
): Promise<string> {
    const fs = params.firestore ?? db;
    const { trip, driver } = params;
    // The truck that ran this trip, not the one the driver happens to be bound to today.
    const truckId = trip.truckId || driver?.currentAssignment?.truckId;
    const [checkInAt, truckTypeFromDoc] = await Promise.all([
        fetchTaskCheckInAt(fs, trip.taskId),
        fetchTruckType(fs, truckId),
    ]);
    const truckType = truckTypeFromDoc || trip.truckType || "";
    return buildTripDeliveryShareText({
        trip,
        driver,
        originLabel: params.originLabel,
        destLabel: params.destLabel,
        partnerLine: params.partnerLine,
        incidentNote: params.incidentNote,
        checkInAt,
        truckType,
    });
}
