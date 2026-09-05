"use client";

import { db } from "@/firebase/client";
import {
    collection,
    deleteDoc,
    documentId,
    getDoc,
    getDocs,
    getDocsFromServer,
    query,
    orderBy,
    doc,
    limit,
    updateDoc,
    serverTimestamp,
    Timestamp,
    writeBatch,
    where,
    type QuerySnapshot,
    type DocumentData,
} from "firebase/firestore";
import { httpsCallable } from "firebase/functions";
import { functions } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { normalizeDestinationCode, normalizeVehicleClass } from "@/lib/billingCompute";
import { bangkokMidnightFromPickedDate, pickedDateToDateStr } from "@/lib/billingDate";
import { driverDisplayName } from "@/lib/driverName";
import { billingHubLabelFromFirestoreData } from "@/lib/hubDisplay";
import { SOC_DESTINATIONS, normalizeSocIdToKey } from "@/validate/taskSchema";
import type { BillingTripRow } from "@/lib/billingDocument";

export interface CustomerRateEntryInput {
    hubId: string;
    rawHubName: string;
    destinationCode: string;
    vehicleClass: string;
    rateThb: number;
    distanceKm?: number;
    /** หลัก/เสริม (ADR-0005). Tagged per import. Missing = PRIMARY. */
    jobCategory?: "PRIMARY" | "SUPPLEMENTARY";
}

/** Void state shared by both announcement collections (ADR 0009 §1). */
export interface AnnouncementVoidState {
    voided?: boolean;
    voidedAt?: Date;
    voidedBy?: string;
    voidedReason?: string;
}

export interface CustomerRateEntryRow extends CustomerRateEntryInput, AnnouncementVoidState {
    id: string;
    customerId: string;
    importId: string;
    effectiveFrom: Date;
    /** `yyyy-MM-dd` the announcement was made for; absent on rows written before ADR 0009. */
    effectiveFromDateStr?: string;
    importedAt?: Date;
}

export interface CustomerFuelRateAdjustmentInput {
    customerId: string;
    effectiveFrom: Date;
    rateMultiplier: number;
    addThbPerTrip?: number;
    referenceFuelPriceThbPerLitre?: number;
    announcementNote?: string;
    fuelBandEnabled?: boolean;
    fuelBandBaselineFuelFloor?: number;
    fuelBandThbPerBaht?: number;
}

export interface CustomerFuelRateAdjustmentRow
    extends CustomerFuelRateAdjustmentInput,
        AnnouncementVoidState {
    id: string;
    /** `yyyy-MM-dd` the announcement was made for; absent on rows written before ADR 0009. */
    effectiveFromDateStr?: string;
    createdAt?: Date;
    updatedAt?: Date;
}

export interface FuelMonthlySnapshotItem {
    nameTh: string;
    nameEn: string;
    price: number;
    unit: string;
}

export interface FuelMonthlySnapshotRow {
    id: string;
    monthKey: string;
    capturedAt?: Date;
    fetchedAtIso?: string;
    source: string;
    locale: string;
    status: "ok" | "error";
    errorMessage?: string;
    items: FuelMonthlySnapshotItem[];
}

function parseDate(v: unknown): Date | undefined {
    if (!v) return undefined;
    if (v instanceof Date) return v;
    if (typeof (v as { toDate?: () => Date }).toDate === "function") return (v as { toDate: () => Date }).toDate();
    if (typeof v === "string") return new Date(v);
    return undefined;
}

function normalizeCode(v: string): string {
    return (v ?? "").trim().toUpperCase();
}

/**
 * The instant an announcement takes effect: **Bangkok midnight** of the picked day (ADR 0009 §2).
 *
 * Was `Date.UTC(...)`, i.e. 07:00 ICT, which priced every overnight delivery on a switch day at the
 * previous round. `effectiveFromDateStr` is stored alongside it as the human-readable fact, so the
 * calendar day survives independently of any future timezone policy.
 */
function effectiveFromFields(value: Date): { effectiveFrom: Timestamp; effectiveFromDateStr: string } {
    return {
        effectiveFrom: Timestamp.fromDate(bangkokMidnightFromPickedDate(value)),
        effectiveFromDateStr: pickedDateToDateStr(value),
    };
}

const BATCH_LIMIT = 450;

export async function batchCreateCustomerRateEntries(
    customerId: string,
    rows: CustomerRateEntryInput[],
    effectiveFrom: Date = new Date()
): Promise<{ importId: string; written: number }> {
    const normalizedCustomerId = customerId.trim();
    if (!normalizedCustomerId) throw new Error("Customer is required");
    if (rows.length === 0) throw new Error("No rows to import");

    const importId = `rc_${Date.now()}`;
    const effective = effectiveFromFields(effectiveFrom);
    const colRef = collection(db, COLLECTIONS.CUSTOMER_RATE_ENTRIES);
    let written = 0;

    for (let i = 0; i < rows.length; i += BATCH_LIMIT) {
        const chunk = rows.slice(i, i + BATCH_LIMIT);
        const batch = writeBatch(db);
        for (const row of chunk) {
            const hubId = normalizeCode(row.hubId);
            const destinationCode = normalizeDestinationCode(row.destinationCode);
            const vehicleClass = normalizeVehicleClass(row.vehicleClass || "4WJ");
            const ref = doc(colRef);
            batch.set(ref, {
                customerId: normalizedCustomerId,
                importId,
                hubId,
                rawHubName: row.rawHubName.trim(),
                destinationCode,
                vehicleClass,
                rateThb: Number(row.rateThb),
                distanceKm: row.distanceKm != null ? Number(row.distanceKm) : null,
                jobCategory: row.jobCategory === "SUPPLEMENTARY" ? "SUPPLEMENTARY" : "PRIMARY",
                ...effective,
                importedAt: serverTimestamp(),
                createdAt: serverTimestamp(),
                updatedAt: serverTimestamp(),
            });
            written += 1;
        }
        await batch.commit();
    }
    return { importId, written };
}

export async function createCustomerRateEntry(
    customerId: string,
    row: CustomerRateEntryInput,
    effectiveFrom: Date = new Date()
): Promise<{ id: string; importId: string }> {
    const normalizedCustomerId = customerId.trim();
    if (!normalizedCustomerId) throw new Error("Customer is required");
    const hubId = normalizeCode(row.hubId);
    const destinationCode = normalizeDestinationCode(row.destinationCode);
    const vehicleClass = normalizeVehicleClass(row.vehicleClass || "4WJ");
    if (!hubId || !destinationCode) throw new Error("hubId and destinationCode are required");
    if (!Number.isFinite(row.rateThb)) throw new Error("rateThb is required");

    const importId = `manual_${Date.now()}`;
    const effective = effectiveFromFields(effectiveFrom);
    const ref = doc(collection(db, COLLECTIONS.CUSTOMER_RATE_ENTRIES));
    await writeBatch(db)
        .set(ref, {
            customerId: normalizedCustomerId,
            importId,
            hubId,
            rawHubName: row.rawHubName.trim() || hubId,
            destinationCode,
            vehicleClass,
            rateThb: Number(row.rateThb),
            distanceKm: row.distanceKm != null ? Number(row.distanceKm) : null,
            jobCategory: row.jobCategory === "SUPPLEMENTARY" ? "SUPPLEMENTARY" : "PRIMARY",
            ...effective,
            importedAt: serverTimestamp(),
            createdAt: serverTimestamp(),
            updatedAt: serverTimestamp(),
        })
        .commit();
    return { id: ref.id, importId };
}

export async function getCustomerRateEntries(customerId?: string): Promise<CustomerRateEntryRow[]> {
    const colRef = collection(db, COLLECTIONS.CUSTOMER_RATE_ENTRIES);
    const useCustomer = customerId?.trim();
    const q = useCustomer
        ? query(colRef, where("customerId", "==", useCustomer))
        : query(colRef);
    const snap = await getDocs(q);
    return snap.docs.map((docSnap) => {
        const d = docSnap.data();
        return {
            id: docSnap.id,
            customerId: String(d.customerId ?? ""),
            importId: String(d.importId ?? ""),
            hubId: String(d.hubId ?? ""),
            rawHubName: String(d.rawHubName ?? ""),
            destinationCode: String(d.destinationCode ?? ""),
            vehicleClass: String(d.vehicleClass ?? "4WJ"),
            rateThb: Number(d.rateThb ?? 0),
            distanceKm: d.distanceKm != null ? Number(d.distanceKm) : undefined,
            jobCategory: (d.jobCategory === "SUPPLEMENTARY" ? "SUPPLEMENTARY" : "PRIMARY") as "PRIMARY" | "SUPPLEMENTARY",
            effectiveFrom: parseDate(d.effectiveFrom) ?? new Date(0),
            effectiveFromDateStr: d.effectiveFromDateStr != null ? String(d.effectiveFromDateStr) : undefined,
            importedAt: parseDate(d.importedAt),
            voided: d.voided === true,
            voidedAt: parseDate(d.voidedAt),
            voidedBy: d.voidedBy != null ? String(d.voidedBy) : undefined,
            voidedReason: d.voidedReason != null ? String(d.voidedReason) : undefined,
        };
    }).sort((a, b) => {
        const byDate = b.effectiveFrom.getTime() - a.effectiveFrom.getTime();
        if (byDate !== 0) return byDate;
        return b.importId.localeCompare(a.importId);
    });
}

/**
 * Void a rate-card announcement (ADR 0009 §1).
 *
 * Replaces the former `updateCustomerRateEntry` / `deleteCustomerRateEntry`. An announcement row
 * records that a price *was announced*; editing it in place silently changes the meaning of every
 * frozen amount already computed from it, and deleting it destroys the provenance an invoice is
 * defended with. A wrong rate is corrected by importing a new row with a later effective date; a
 * round that should never have existed is voided.
 */
export async function voidCustomerRateEntry(id: string, reason: string, voidedBy: string): Promise<void> {
    const normalizedId = id.trim();
    if (!normalizedId) throw new Error("Rate entry id is required");
    const trimmedReason = reason.trim();
    if (!trimmedReason) throw new Error("A reason is required to void an announcement");
    await updateDoc(doc(db, COLLECTIONS.CUSTOMER_RATE_ENTRIES, normalizedId), {
        voided: true,
        voidedAt: serverTimestamp(),
        voidedBy,
        voidedReason: trimmedReason,
        updatedAt: serverTimestamp(),
    });
}

export async function createCustomerFuelRateAdjustment(
    input: CustomerFuelRateAdjustmentInput
): Promise<void> {
    const customerId = input.customerId.trim();
    if (!customerId) throw new Error("Customer is required");
    if (!Number.isFinite(input.rateMultiplier) || input.rateMultiplier <= 0) {
        throw new Error("rateMultiplier must be greater than 0");
    }
    const colRef = collection(db, COLLECTIONS.CUSTOMER_FUEL_RATE_ADJUSTMENTS);
    const ref = doc(colRef);
    const batch = writeBatch(db);
    batch.set(ref, {
        customerId,
        ...effectiveFromFields(input.effectiveFrom),
        rateMultiplier: Number(input.rateMultiplier),
        addThbPerTrip: Number(input.addThbPerTrip ?? 0),
        referenceFuelPriceThbPerLitre:
            input.referenceFuelPriceThbPerLitre != null
                ? Number(input.referenceFuelPriceThbPerLitre)
                : null,
        announcementNote: input.announcementNote?.trim() || "",
        fuelBandEnabled: input.fuelBandEnabled ?? false,
        fuelBandBaselineFuelFloor: input.fuelBandBaselineFuelFloor ?? null,
        fuelBandThbPerBaht: input.fuelBandThbPerBaht ?? null,
        createdAt: serverTimestamp(),
        updatedAt: serverTimestamp(),
    });
    await batch.commit();
}

export async function getCustomerFuelRateAdjustments(
    customerId?: string
): Promise<CustomerFuelRateAdjustmentRow[]> {
    const colRef = collection(db, COLLECTIONS.CUSTOMER_FUEL_RATE_ADJUSTMENTS);
    const useCustomer = customerId?.trim();
    const q = useCustomer
        ? query(colRef, where("customerId", "==", useCustomer))
        : query(colRef);
    const snap = await getDocs(q);
    return snap.docs.map((docSnap) => {
        const d = docSnap.data();
        return {
            id: docSnap.id,
            customerId: String(d.customerId ?? ""),
            effectiveFrom: parseDate(d.effectiveFrom) ?? new Date(0),
            rateMultiplier: Number(d.rateMultiplier ?? 1),
            addThbPerTrip: d.addThbPerTrip != null ? Number(d.addThbPerTrip) : 0,
            referenceFuelPriceThbPerLitre:
                d.referenceFuelPriceThbPerLitre != null
                    ? Number(d.referenceFuelPriceThbPerLitre)
                    : undefined,
            announcementNote: String(d.announcementNote ?? ""),
            fuelBandEnabled: Boolean(d.fuelBandEnabled ?? false),
            fuelBandBaselineFuelFloor: d.fuelBandBaselineFuelFloor != null ? Number(d.fuelBandBaselineFuelFloor) : undefined,
            fuelBandThbPerBaht: d.fuelBandThbPerBaht != null ? Number(d.fuelBandThbPerBaht) : undefined,
            effectiveFromDateStr: d.effectiveFromDateStr != null ? String(d.effectiveFromDateStr) : undefined,
            createdAt: parseDate(d.createdAt),
            updatedAt: parseDate(d.updatedAt),
            voided: d.voided === true,
            voidedAt: parseDate(d.voidedAt),
            voidedBy: d.voidedBy != null ? String(d.voidedBy) : undefined,
            voidedReason: d.voidedReason != null ? String(d.voidedReason) : undefined,
        };
    }).sort((a, b) => b.effectiveFrom.getTime() - a.effectiveFrom.getTime());
}

/**
 * The retail fuel prices captured on one Bangkok day (ADR 0009 §5).
 *
 * An announcement must be priced from the diesel price of **its own effective date**. The monthly
 * doc cannot answer that: it is overwritten on every sync, so entering a round on the 20th that
 * takes effect on the 16th would read the 20th's price and land in the wrong band. Daily docs are
 * written create-only and never overwritten.
 *
 * Returns `null` when that day was never captured — the caller must show that plainly rather than
 * substitute another day's price.
 */
export async function getFuelDailySnapshot(dayKey: string): Promise<FuelMonthlySnapshotRow | null> {
    if (!/^\d{4}-\d{2}-\d{2}$/.test(dayKey)) return null;
    const snap = await getDoc(doc(db, COLLECTIONS.FUEL_DAILY_SNAPSHOTS, dayKey));
    if (!snap.exists()) return null;
    const d = snap.data();
    if (d.status === "error") return null;
    const rawItems = d.items;
    const items: FuelMonthlySnapshotItem[] = Array.isArray(rawItems)
        ? rawItems.map((i: Record<string, unknown>) => ({
              nameTh: String(i.nameTh ?? ""),
              nameEn: String(i.nameEn ?? ""),
              price: Number(i.price ?? 0),
              unit: String(i.unit ?? ""),
          }))
        : [];
    return {
        id: dayKey,
        monthKey: String(d.monthKey ?? dayKey.slice(0, 7)),
        capturedAt: parseDate(d.capturedAt),
        fetchedAtIso: d.fetchedAtIso != null ? String(d.fetchedAtIso) : undefined,
        source: String(d.source ?? ""),
        locale: String(d.locale ?? "th"),
        status: "ok",
        items,
    };
}

export async function getFuelMonthlySnapshots(limitCount = 36): Promise<FuelMonthlySnapshotRow[]> {
    const colRef = collection(db, COLLECTIONS.FUEL_MONTHLY_SNAPSHOTS);
    const q = query(colRef, orderBy("monthKey", "desc"), limit(limitCount));
    const snap = await getDocs(q);
    return snap.docs.map((docSnap) => {
        const d = docSnap.data();
        const rawItems = d.items;
        const items: FuelMonthlySnapshotItem[] = Array.isArray(rawItems)
            ? rawItems.map((x: Record<string, unknown>) => ({
                  nameTh: String(x.nameTh ?? ""),
                  nameEn: String(x.nameEn ?? ""),
                  price: Number(x.price ?? 0),
                  unit: String(x.unit ?? ""),
              }))
            : [];
        return {
            id: docSnap.id,
            monthKey: String(d.monthKey ?? docSnap.id),
            capturedAt: parseDate(d.capturedAt),
            fetchedAtIso: d.fetchedAtIso != null ? String(d.fetchedAtIso) : undefined,
            source: String(d.source ?? ""),
            locale: String(d.locale ?? "th"),
            status: d.status === "error" ? "error" : "ok",
            errorMessage: d.errorMessage != null ? String(d.errorMessage) : undefined,
            items,
        };
    });
}

/**
 * Void a fuel-adjustment announcement (ADR 0009 §1) — replaces the former update/delete pair.
 *
 * The old `updateCustomerFuelRateAdjustment` overwrote the whole field set with no version, so a
 * trip priced under band 41.01–42.00 could later print a band of 43.01–44.00 beside an amount that
 * was never computed from it. Correct a round by announcing a new one.
 */
export async function voidCustomerFuelRateAdjustment(
    id: string,
    reason: string,
    voidedBy: string
): Promise<void> {
    const normalizedId = id.trim();
    if (!normalizedId) throw new Error("Adjustment id is required");
    const trimmedReason = reason.trim();
    if (!trimmedReason) throw new Error("A reason is required to void an announcement");
    await updateDoc(doc(db, COLLECTIONS.CUSTOMER_FUEL_RATE_ADJUSTMENTS, normalizedId), {
        voided: true,
        voidedAt: serverTimestamp(),
        voidedBy,
        voidedReason: trimmedReason,
        updatedAt: serverTimestamp(),
    });
}

export interface WriteTripBillingInput {
    tripId: string;
    billingEstimateThb: number;
    billingBaseRateThb: number;
    billingRateImportId: string;
    billingLookupHubId: string;
    billingLookupDestination: string;
    billingFuelAdjustmentId?: string | null;
    billingRateMultiplier: number;
    billingAddThbPerTrip: number;
    billingEffectiveFromDateStr?: string | null;
    billingCustomerId: string;
    billingManualOverride?: boolean;
}

export async function writeTripBillingSnapshot(input: WriteTripBillingInput): Promise<void> {
    const ref = doc(db, COLLECTIONS.TRIP_RECORDS, input.tripId);
    const payload: Record<string, unknown> = {
        billingEstimateThb: input.billingEstimateThb,
        billingBaseRateThb: input.billingBaseRateThb,
        billingRateImportId: input.billingRateImportId,
        billingLookupHubId: input.billingLookupHubId,
        billingLookupDestination: input.billingLookupDestination,
        billingFuelAdjustmentId: input.billingFuelAdjustmentId ?? null,
        billingRateMultiplier: input.billingRateMultiplier,
        billingAddThbPerTrip: input.billingAddThbPerTrip,
        billingEffectiveFromDateStr: input.billingEffectiveFromDateStr ?? null,
        billingCustomerId: input.billingCustomerId,
        updatedAt: serverTimestamp(),
    };
    if (input.billingManualOverride) {
        payload.billingManualOverride = true;
    }
    await updateDoc(ref, payload);
}

export async function updateTaskBillingFields(
    taskId: string,
    fields: { sourceHub: string; destination: string; truckType: string }
): Promise<void> {
    const ref = doc(db, COLLECTIONS.TASKS, taskId);
    await updateDoc(ref, {
        sourceHub: fields.sourceHub,
        destination: fields.destination,
        truckType: fields.truckType,
        updatedAt: serverTimestamp(),
    });
}

export type ServiceFeeType = "extra_stop" | "waiting_time" | "special_handling" | "service_charge" | "standby" | "custom";
export type ServiceFeeUnit = "per_trip" | "per_stop";

export interface CustomerServiceFeeInput {
    customerId: string;
    feeType: ServiceFeeType;
    customTypeName?: string;
    amountThb: number;
    unit: ServiceFeeUnit;
    note?: string;
}

export interface CustomerServiceFeeRow extends CustomerServiceFeeInput {
    id: string;
    createdAt?: Date;
}

export async function createCustomerServiceFee(input: CustomerServiceFeeInput): Promise<void> {
    const customerId = input.customerId.trim();
    if (!customerId) throw new Error("Customer is required");
    if (!Number.isFinite(input.amountThb) || input.amountThb < 0) {
        throw new Error("Amount must be >= 0");
    }
    if (input.feeType === "custom" && !input.customTypeName?.trim()) {
        throw new Error("Custom type name is required when fee type is custom");
    }

    const colRef = collection(db, COLLECTIONS.CUSTOMER_SERVICE_FEES);
    const ref = doc(colRef);
    const batch = writeBatch(db);
    const payload: Record<string, unknown> = {
        customerId,
        feeType: input.feeType,
        amountThb: Number(input.amountThb),
        unit: input.unit,
        createdAt: serverTimestamp(),
        updatedAt: serverTimestamp(),
    };
    if (input.feeType === "custom" && input.customTypeName?.trim()) {
        payload.customTypeName = input.customTypeName.trim();
    }
    if (input.note?.trim()) {
        payload.note = input.note.trim();
    }
    batch.set(ref, payload);
    await batch.commit();
}

export async function getCustomerServiceFees(customerId?: string): Promise<CustomerServiceFeeRow[]> {
    const colRef = collection(db, COLLECTIONS.CUSTOMER_SERVICE_FEES);
    const useCustomer = customerId?.trim();
    const q = useCustomer
        ? query(colRef, where("customerId", "==", useCustomer), orderBy("createdAt", "desc"))
        : query(colRef, orderBy("createdAt", "desc"));
    const snap = await getDocs(q);
    return snap.docs.map((docSnap) => {
        const d = docSnap.data();
        return {
            id: docSnap.id,
            customerId: String(d.customerId ?? ""),
            feeType: (d.feeType as ServiceFeeType) ?? "extra_stop",
            customTypeName: d.customTypeName != null ? String(d.customTypeName) : undefined,
            amountThb: Number(d.amountThb ?? 0),
            unit: (d.unit as ServiceFeeUnit) ?? "per_trip",
            note: d.note != null ? String(d.note) : undefined,
            createdAt: parseDate(d.createdAt),
        };
    });
}

export async function updateCustomerServiceFee(
    id: string,
    input: CustomerServiceFeeInput
): Promise<void> {
    const normalizedId = id.trim();
    const customerId = input.customerId.trim();
    if (!normalizedId) throw new Error("Service fee id is required");
    if (!customerId) throw new Error("Customer is required");
    if (!Number.isFinite(input.amountThb) || input.amountThb < 0) {
        throw new Error("Amount must be >= 0");
    }
    if (input.feeType === "custom" && !input.customTypeName?.trim()) {
        throw new Error("Custom type name is required when fee type is custom");
    }

    const ref = doc(db, COLLECTIONS.CUSTOMER_SERVICE_FEES, normalizedId);
    const payload: Record<string, unknown> = {
        customerId,
        feeType: input.feeType,
        amountThb: Number(input.amountThb),
        unit: input.unit,
        updatedAt: serverTimestamp(),
    };
    if (input.feeType === "custom" && input.customTypeName?.trim()) {
        payload.customTypeName = input.customTypeName.trim();
    } else if (input.feeType !== "custom") {
        payload.customTypeName = null;
    }
    if (input.note?.trim()) {
        payload.note = input.note.trim();
    } else {
        payload.note = null;
    }
    await updateDoc(ref, payload);
}

export async function deleteCustomerServiceFee(id: string): Promise<void> {
    const normalizedId = id.trim();
    if (!normalizedId) throw new Error("Service fee id is required");
    await deleteDoc(doc(db, COLLECTIONS.CUSTOMER_SERVICE_FEES, normalizedId));
}

export interface NormalizeVehicleClassResponse {
    scanned: number;
    needsUpdate: number;
    updated: number;
    samples: Array<{ docId: string; old: string; new: string }>;
    capped: boolean;
}

export async function normalizeRateEntryVehicleClasses(
    maxScan?: number,
    maxUpdate?: number
): Promise<NormalizeVehicleClassResponse> {
    const callable = httpsCallable<
        { maxScan?: number; maxUpdate?: number },
        NormalizeVehicleClassResponse
    >(functions, "normalizeRateEntryVehicleClasses");

    const result = await callable({ maxScan, maxUpdate });
    return result.data;
}

// ─── Standby Rate Entries ─────────────────────────────────────────────────────

export interface StandbyRateEntryInput {
    customerId: string;
    rateThb: number;
    effectiveFrom: Date;
    note?: string;
}

export interface StandbyRateEntryRow extends StandbyRateEntryInput {
    id: string;
    createdAt?: Date;
}

export async function getStandbyRateEntries(customerId?: string): Promise<StandbyRateEntryRow[]> {
    const colRef = collection(db, COLLECTIONS.STANDBY_RATE_ENTRIES);
    const useCustomer = customerId?.trim();
    const q = useCustomer
        ? query(colRef, where("customerId", "==", useCustomer), orderBy("effectiveFrom", "desc"))
        : query(colRef, orderBy("effectiveFrom", "desc"));
    const snap = await getDocs(q);
    return snap.docs.map((d) => {
        const data = d.data();
        const eff = data.effectiveFrom;
        const effDate =
            eff instanceof Timestamp
                ? eff.toDate()
                : eff instanceof Date
                ? eff
                : new Date(eff);
        return {
            id: d.id,
            customerId: String(data.customerId ?? ""),
            rateThb: Number(data.rateThb ?? 0),
            effectiveFrom: effDate,
            note: data.note != null ? String(data.note) : undefined,
            createdAt: parseDate(data.createdAt),
        };
    });
}

export async function createStandbyRateEntry(input: StandbyRateEntryInput): Promise<void> {
    const customerId = input.customerId.trim();
    if (!customerId) throw new Error("Customer is required");
    if (!Number.isFinite(input.rateThb) || input.rateThb < 0) throw new Error("Rate must be a non-negative number");
    const colRef = collection(db, COLLECTIONS.STANDBY_RATE_ENTRIES);
    const batch = writeBatch(db);
    batch.set(doc(colRef), {
        customerId,
        rateThb: input.rateThb,
        effectiveFrom: Timestamp.fromDate(input.effectiveFrom),
        note: input.note?.trim() || null,
        createdAt: serverTimestamp(),
    });
    await batch.commit();
}

export async function updateStandbyRateEntry(id: string, input: StandbyRateEntryInput): Promise<void> {
    const normalizedId = id.trim();
    if (!normalizedId) throw new Error("Standby rate entry id is required");
    const customerId = input.customerId.trim();
    if (!customerId) throw new Error("Customer is required");
    if (!Number.isFinite(input.rateThb) || input.rateThb < 0) throw new Error("Rate must be a non-negative number");
    await updateDoc(doc(db, COLLECTIONS.STANDBY_RATE_ENTRIES, normalizedId), {
        customerId,
        rateThb: input.rateThb,
        effectiveFrom: Timestamp.fromDate(input.effectiveFrom),
        note: input.note?.trim() || null,
    });
}

export async function deleteStandbyRateEntry(id: string): Promise<void> {
    const normalizedId = id.trim();
    if (!normalizedId) throw new Error("Standby rate entry id is required");
    await deleteDoc(doc(db, COLLECTIONS.STANDBY_RATE_ENTRIES, normalizedId));
}

export interface MissingBillingRow {
    id: string;
    spxTripId?: string;
    taskId?: string;
    deliveredTimestamp?: Date;
    createdAt?: Date;
    sourceHub?: string;
    destination?: string;
    truckType?: string;
    customerId?: string;
    customerName?: string;
    lookupHubId?: string;
    lookupDestination?: string;
    lookupVehicleClass?: string;
    computedRate?: number;
    failureReason?: string;
    /** หลัก/เสริม resolved trip → task (ADR 0010); undefined ⇒ show the loud "unverified" marker. */
    jobCategory?: "PRIMARY" | "SUPPLEMENTARY";
}

// ─── Shared trip-row fetcher for Billing Document / Result pages ──────────────

function toBillingDate(val: unknown): Date | undefined {
    if (!val) return undefined;
    if (val instanceof Date) return val;
    if (typeof (val as { toDate?: () => Date }).toDate === "function") {
        return (val as { toDate: () => Date }).toDate();
    }
    return undefined;
}

/** Extra keys so billing destination codes (e.g. SPK890103) resolve to hub rows whose source_id includes a Thai suffix. */
function extraDestinationLookupKeys(sourceId: string): string[] {
    const u = sourceId.trim().toUpperCase();
    const norm = normalizeDestinationCode(sourceId);
    if (!norm || norm === u) return [];
    if (/^SPK-[A-Z0-9]+$/.test(u)) return [];
    return [norm];
}

/**
 * Fetch billed trip/standby/multidrop-stop rows for one customer + period, resolved to
 * display-ready `BillingTripRow[]` (driver names, hub display names, origin codes). Used
 * by the Billing Document page (initial download) and the Billing Result page (redownload,
 * issue receipt) so both always regenerate from the same real data instead of an empty array.
 */
export async function fetchBillingTripRows(
    customerId: string | "all",
    period: { month: number; year: number }
): Promise<BillingTripRow[]> {
    const start = new Date(period.year, period.month - 1, 1);
    const end = new Date(period.year, period.month, 1);

    // ── Hub display-name / code maps (rebuilt per call — same shape as billing-document page) ──
    const hubNameMap = new Map<string, string>();
    const hubCodeMap = new Map<string, string>();
    const hubsSnap = await getDocs(collection(db, COLLECTIONS.HUBS));
    hubsSnap.forEach((d) => {
        const data = d.data();
        const label = billingHubLabelFromFirestoreData(data);
        const sourceId = String(data.source_id ?? data.hubId ?? data.hubCode ?? "").trim();
        if (data.source_id) hubNameMap.set(String(data.source_id).trim().toUpperCase(), label);
        if (data.hubId) hubNameMap.set(String(data.hubId).trim().toUpperCase(), label);
        if (data.hubCode) hubNameMap.set(String(data.hubCode).trim().toUpperCase(), label);
        for (const extra of extraDestinationLookupKeys(sourceId)) hubNameMap.set(extra, label);
        hubNameMap.set(d.id, label);
        hubNameMap.set(d.id.toUpperCase(), label);

        if (sourceId) {
            const codeKey = sourceId.toUpperCase();
            hubCodeMap.set(codeKey, sourceId);
            hubCodeMap.set(label.trim().toUpperCase(), sourceId);
            for (const nameField of [data.source_name_en, data.source_name_th, data.hubName, data.hubTHName]) {
                const name = typeof nameField === "string" ? nameField.trim() : "";
                if (name) hubCodeMap.set(name.toUpperCase(), sourceId);
            }
            hubCodeMap.set(d.id.toUpperCase(), sourceId);
        }
    });

    const resolveDisplayName = (code: string | undefined): string => {
        if (!code) return "-";
        const trimmed = code.trim();
        if (!trimmed) return "-";
        const upper = trimmed.toUpperCase();
        if (hubNameMap.get(upper)) return hubNameMap.get(upper)!;
        if (trimmed !== upper && hubNameMap.get(trimmed)) return hubNameMap.get(trimmed)!;
        const norm = normalizeDestinationCode(trimmed);
        if (norm && norm !== upper && hubNameMap.get(norm)) return hubNameMap.get(norm)!;
        const socKey = normalizeSocIdToKey(upper);
        if (socKey && (SOC_DESTINATIONS as Record<string, string>)[socKey]) {
            return (SOC_DESTINATIONS as Record<string, string>)[socKey];
        }
        return trimmed;
    };

    const resolveHubCode = (value: string | undefined): string => {
        const trimmed = (value ?? "").trim();
        if (!trimmed) return trimmed;
        return hubCodeMap.get(trimmed.toUpperCase()) ?? trimmed;
    };

    // ── trip_records + standby_records for this customer (or all) + period ───────
    const tripConstraints = [
        where("status", "==", "delivered"),
        where("deliveredTimestamp", ">=", Timestamp.fromDate(start)),
        where("deliveredTimestamp", "<", Timestamp.fromDate(end)),
    ];
    if (customerId !== "all") tripConstraints.push(where("billingCustomerId", "==", customerId));
    const tripSnap = await getDocsFromServer(
        query(collection(db, COLLECTIONS.TRIP_RECORDS), ...tripConstraints)
    );

    let standbySnap: QuerySnapshot<DocumentData> | null = null;
    try {
        standbySnap = await getDocsFromServer(
            query(
                collection(db, COLLECTIONS.STANDBY_RECORDS),
                where("status", "==", "completed"),
                where("endedAt", ">=", Timestamp.fromDate(start)),
                where("endedAt", "<", Timestamp.fromDate(end))
            )
        );
    } catch (e) {
        console.warn("[fetchBillingTripRows] standby_records query failed (index may be building):", e);
    }

    // ── Batch-fetch linked tasks (driverName/licensePlate/customer denormalized) ──
    type TaskInfo = { truckType?: string; driverId?: string; driverName?: string; driverPhone?: string; truckLicensePlate?: string; truckId?: string; sourceHub?: string; destination?: string; jobCategory?: "PRIMARY" | "SUPPLEMENTARY" };
    const taskMap = new Map<string, TaskInfo>();
    const taskIds = new Set<string>();
    tripSnap.forEach((d) => { const tid = d.data().taskId; if (tid) taskIds.add(tid); });
    standbySnap?.forEach((d) => { const tid = d.data().taskId; if (tid) taskIds.add(tid); });

    const allTaskIds = Array.from(taskIds);
    const taskIdChunks: string[][] = [];
    for (let i = 0; i < allTaskIds.length; i += 30) taskIdChunks.push(allTaskIds.slice(i, i + 30));
    await Promise.allSettled(taskIdChunks.map(async (chunk) => {
        const taskSnap = await getDocs(query(collection(db, COLLECTIONS.TASKS), where(documentId(), "in", chunk)));
        taskSnap.forEach((taskDoc) => {
            const t = taskDoc.data();
            taskMap.set(taskDoc.id, {
                truckType: t.truckType,
                // Driver DOC ID chosen in the assign dialog — the most reliable key into the master.
                driverId: t.driverId,
                driverName: t.driverName,
                driverPhone: t.driverPhone,
                truckLicensePlate: t.licensePlate,
                truckId: t.truckId,
                sourceHub: t.sourceHub,
                destination: t.destination,
                // Authoritative หลัก/เสริม (ADR 0010): the trip's copy falls back to this when absent.
                jobCategory:
                    t.jobCategory === "SUPPLEMENTARY"
                        ? "SUPPLEMENTARY"
                        : t.jobCategory === "PRIMARY"
                        ? "PRIMARY"
                        : undefined,
            });
        });
    }));

    // ── Drivers: resolve names to Thai + subcontractor name ───────────────────────
    const driverNameByKey = new Map<string, string>();
    const driverSubByKey = new Map<string, string>();
    try {
        const driversSnap = await getDocs(collection(db, COLLECTIONS.DRIVERS));
        driversSnap.forEach((ds) => {
            const dd = ds.data();
            const name = driverDisplayName(dd, ds.id);
            const sub = (dd.subcontractorName as string | undefined)?.trim();
            driverNameByKey.set(ds.id, name);
            if (sub) driverSubByKey.set(ds.id, sub);
            const authId = (dd.authId ?? dd.authUid) as string | undefined;
            if (authId) {
                driverNameByKey.set(authId, name);
                if (sub) driverSubByKey.set(authId, sub);
            }
        });
    } catch (e) {
        console.warn("[fetchBillingTripRows] failed to load drivers for Thai name resolution:", e);
    }
    /**
     * Try every identifier that might point at the driver master before giving up.
     *
     * The last resort — `tasks.driverName` — is written by the assign dialogs as
     * `${firstName} ${lastName}` and therefore can NEVER be Thai, so any miss here silently turns a
     * Thai report row into a Latin one. `trip_records.driverId` alone is not enough: it can be
     * absent, or hold an auth uid that no longer matches the driver doc's `authId`. `tasks.driverId`
     * is the driver DOC ID the admin picked in the dialog, so it resolves those cases.
     */
    const lookupDriver = <T,>(map: Map<string, T>, candidates: unknown[]): T | undefined => {
        for (const candidate of candidates) {
            const key = String(candidate ?? "").trim();
            if (!key) continue;
            const hit = map.get(key);
            if (hit !== undefined) return hit;
        }
        return undefined;
    };
    const resolveDriverName = (candidates: unknown[], fallback?: string): string | undefined =>
        lookupDriver(driverNameByKey, candidates) ?? fallback;
    const resolveSubcontractor = (candidates: unknown[]): string | undefined =>
        lookupDriver(driverSubByKey, candidates);

    // หลัก/เสริม resolution (ADR 0010): the trip's own value wins; when it never got one (billing
    // skipped/failed, or a legacy trip), fall back to the authoritative task value. Neither present
    // ⇒ undefined, so the UI shows a loud "unverified" marker instead of silently guessing หลัก.
    const resolveJobCategory = (
        tripVal: unknown,
        taskVal: "PRIMARY" | "SUPPLEMENTARY" | undefined
    ): "PRIMARY" | "SUPPLEMENTARY" | undefined =>
        tripVal === "SUPPLEMENTARY" || tripVal === "PRIMARY" ? tripVal : taskVal;

    const rows: BillingTripRow[] = [];

    tripSnap.forEach((d) => {
        const data = d.data();
        if (!Number(data.billingEstimateThb)) return; // skip no-billing trips

        const taskInfo = data.taskId ? taskMap.get(data.taskId) : undefined;
        const hubId = data.billingLookupHubId ?? "";
        // Round + band provenance as frozen onto the record (ADR 0009 §4). Built once and spread
        // into EVERY row type: a multidrop stop belongs to the same round as its parent trip, and
        // omitting it here is what made the invoice legend and the รอบ column stay empty.
        const roundProvenance = {
            billingRoundEffectiveFromDateStr:
                data.billingRoundEffectiveFromDateStr != null
                    ? String(data.billingRoundEffectiveFromDateStr)
                    : undefined,
            billingFuelBandLowerThb:
                data.billingFuelBandLowerThb != null ? Number(data.billingFuelBandLowerThb) : undefined,
            billingFuelBandUpperThb:
                data.billingFuelBandUpperThb != null ? Number(data.billingFuelBandUpperThb) : undefined,
            billingReferenceFuelPriceThb:
                data.billingReferenceFuelPriceThb != null
                    ? Number(data.billingReferenceFuelPriceThb)
                    : undefined,
        };

        if (data.billingIsMultiDelivery && Array.isArray(data.billingMultiDeliveryBreakdown) && data.billingMultiDeliveryBreakdown.length > 0) {
            for (const stop of data.billingMultiDeliveryBreakdown as { stopIndex: number; destination: string; baseRateThb: number; finalRateThb: number }[]) {
                if (!stop.finalRateThb) continue;
                const destCode = stop.destination ?? "";
                rows.push({
                    id: `${d.id}_s${stop.stopIndex}`,
                    taskId: data.taskId,
                    spxTripId: data.spxTripId ? `${data.spxTripId}-s${stop.stopIndex}` : undefined,
                    deliveredTimestamp: toBillingDate(data.deliveredTimestamp),
                    billingEstimateThb: stop.finalRateThb,
                    billingBaseRateThb: stop.baseRateThb || undefined,
                    billingLookupHubId: hubId,
                    billingLookupDestination: destCode,
                    billingCustomerId: data.billingCustomerId,
                    vehicleClass: taskInfo?.truckType,
                    driverName: resolveDriverName([data.driverId, taskInfo?.driverId], taskInfo?.driverName),
                    driverPhone: taskInfo?.driverPhone,
                    subcontractorName: resolveSubcontractor([data.driverId, taskInfo?.driverId]),
                    jobCategory: resolveJobCategory(data.jobCategory, taskInfo?.jobCategory),
                    truckLicensePlate: taskInfo?.truckLicensePlate,
            truckId: taskInfo?.truckId,
                    hubDisplayName: resolveDisplayName(hubId),
                    originHubCode: resolveHubCode(hubId || (taskInfo?.sourceHub as string | undefined) || ""),
                    destinationDisplayName: resolveDisplayName(destCode),
                    ...roundProvenance,
                    rowType: "multidrop_stop",
                    stopIndex: stop.stopIndex,
                });
            }
            return;
        }

        const dest = data.billingLookupDestination ?? "";
        rows.push({
            id: d.id,
            taskId: data.taskId,
            spxTripId: data.spxTripId,
            deliveredTimestamp: toBillingDate(data.deliveredTimestamp),
            billingEstimateThb: Number(data.billingEstimateThb),
            billingBaseRateThb: Number(data.billingBaseRateThb) || undefined,
            billingLookupHubId: hubId,
            billingLookupDestination: dest,
            billingRateMultiplier: Number(data.billingRateMultiplier) || undefined,
            billingAddThbPerTrip: Number(data.billingAddThbPerTrip) || undefined,
            billingCustomerId: data.billingCustomerId,
            ...roundProvenance,
            vehicleClass: taskInfo?.truckType,
            driverName: resolveDriverName([data.driverId, taskInfo?.driverId], taskInfo?.driverName),
            driverPhone: taskInfo?.driverPhone,
            subcontractorName: resolveSubcontractor([data.driverId, taskInfo?.driverId]),
            jobCategory: resolveJobCategory(data.jobCategory, taskInfo?.jobCategory),
            truckLicensePlate: taskInfo?.truckLicensePlate,
            truckId: taskInfo?.truckId,
            hubDisplayName: resolveDisplayName(hubId),
            originHubCode: resolveHubCode(hubId || (taskInfo?.sourceHub as string | undefined) || ""),
            destinationDisplayName: resolveDisplayName(dest),
            rowType: "trip",
        });
    });

    standbySnap?.forEach((d) => {
        const data = d.data();
        const billingAmt = Number(data.billingEstimateThb);
        if (!billingAmt) return;

        const cid = (data.billingCustomerId as string | undefined)?.trim() || undefined;
        if (customerId !== "all" && cid !== customerId) return;

        const taskInfo = data.taskId ? taskMap.get(data.taskId) : undefined;

        rows.push({
            id: d.id,
            taskId: data.taskId ?? undefined,
            spxTripId: (data.spxTripId as string | undefined)
                ?? (data.migratedFromSpxTripId as string | undefined)
                ?? (data.migratedFromTripId as string | undefined)
                ?? undefined,
            deliveredTimestamp: toBillingDate(data.endedAt) ?? toBillingDate(data.startedAt) ?? undefined,
            billingEstimateThb: billingAmt,
            billingCustomerId: cid,
            vehicleClass: taskInfo?.truckType,
            driverName: resolveDriverName([data.driverId, taskInfo?.driverId], taskInfo?.driverName),
            driverPhone: taskInfo?.driverPhone,
            subcontractorName: resolveSubcontractor([data.driverId, taskInfo?.driverId]),
            truckLicensePlate: taskInfo?.truckLicensePlate,
            truckId: taskInfo?.truckId,
            hubDisplayName: resolveDisplayName(
                (taskInfo?.sourceHub as string | undefined) ?? (data.startLocation as string | undefined)
            ),
            originHubCode: resolveHubCode(
                (taskInfo?.sourceHub as string | undefined) ?? (data.startLocation as string | undefined) ?? ""
            ),
            destinationDisplayName: resolveDisplayName(
                (taskInfo?.destination as string | undefined) ?? (data.endLocation as string | undefined)
            ),
            // หลัก/เสริม (ADR 0010): mirror the trip rows — the record's own value wins, else the
            // authoritative task value. Standby billing is a flat per-event rate that never depends
            // on หลัก/เสริม, so this is a display/label only; it never changes the price.
            jobCategory: resolveJobCategory(data.jobCategory, taskInfo?.jobCategory),
            rowType: "standby",
        });
    });

    rows.sort((a, b) => (a.deliveredTimestamp?.getTime() ?? 0) - (b.deliveredTimestamp?.getTime() ?? 0));
    return rows;
}

// ─── Shopee Express (TTP) billing support report ──────────────────────────────
//
// A price-free customer-facing pack: every delivered trip of one customer in a month
// (INCLUDING trips that were never billed) plus the signed run-sheet photos. Kept
// separate from `fetchBillingTripRows` on purpose — that fetcher drops trips with no
// `billingEstimateThb` and filters by `billingCustomerId` server-side, neither of
// which is correct here. Read-only; never touches any billing amount.

/**
 * Billing round within a month. TTP is billed in two half-month rounds:
 *  - "first"  → days 1–15
 *  - "second" → day 16 to end of month
 *  - "full"   → the whole month (both rounds)
 */
export type BillingHalf = "full" | "first" | "second";

export interface ShopeeReportTripRow {
    id: string;
    spxTripId?: string;
    deliveredTimestamp?: Date;
    driverId?: string;
    driverName: string;
    originDisplay: string;
    destinationDisplay: string;
    vehicleClass?: string;
    truckLicensePlate?: string;
    parcelCount?: number;
    /** URLs of run-sheets that carry the recipient's signature (runsheet_received + per-stop variants). */
    signedRunsheetPhotos: string[];
}

/** Build a hub/SOC code → display-name resolver (same rules as fetchBillingTripRows). */
async function buildHubDisplayResolver(): Promise<{
    resolveDisplayName: (code: string | undefined) => string;
}> {
    const hubNameMap = new Map<string, string>();
    const hubsSnap = await getDocs(collection(db, COLLECTIONS.HUBS));
    hubsSnap.forEach((d) => {
        const data = d.data();
        const label = billingHubLabelFromFirestoreData(data);
        const sourceId = String(data.source_id ?? data.hubId ?? data.hubCode ?? "").trim();
        if (data.source_id) hubNameMap.set(String(data.source_id).trim().toUpperCase(), label);
        if (data.hubId) hubNameMap.set(String(data.hubId).trim().toUpperCase(), label);
        if (data.hubCode) hubNameMap.set(String(data.hubCode).trim().toUpperCase(), label);
        for (const extra of extraDestinationLookupKeys(sourceId)) hubNameMap.set(extra, label);
        hubNameMap.set(d.id, label);
        hubNameMap.set(d.id.toUpperCase(), label);
    });
    const resolveDisplayName = (code: string | undefined): string => {
        if (!code) return "-";
        const trimmed = code.trim();
        if (!trimmed) return "-";
        const upper = trimmed.toUpperCase();
        if (hubNameMap.get(upper)) return hubNameMap.get(upper)!;
        if (trimmed !== upper && hubNameMap.get(trimmed)) return hubNameMap.get(trimmed)!;
        const norm = normalizeDestinationCode(trimmed);
        if (norm && norm !== upper && hubNameMap.get(norm)) return hubNameMap.get(norm)!;
        const socKey = normalizeSocIdToKey(upper);
        if (socKey && (SOC_DESTINATIONS as Record<string, string>)[socKey]) {
            return (SOC_DESTINATIONS as Record<string, string>)[socKey];
        }
        return trimmed;
    };
    return { resolveDisplayName };
}

/** Build a driver-identifier → Thai display-name resolver (same rules as fetchBillingTripRows). */
async function buildDriverNameResolver(): Promise<{
    resolveDriverName: (candidates: unknown[], fallback?: string) => string | undefined;
}> {
    const driverNameByKey = new Map<string, string>();
    try {
        const driversSnap = await getDocs(collection(db, COLLECTIONS.DRIVERS));
        driversSnap.forEach((ds) => {
            const dd = ds.data();
            const name = driverDisplayName(dd, ds.id);
            driverNameByKey.set(ds.id, name);
            const authId = (dd.authId ?? dd.authUid) as string | undefined;
            if (authId) driverNameByKey.set(authId, name);
        });
    } catch (e) {
        console.warn("[buildDriverNameResolver] failed to load drivers:", e);
    }
    const resolveDriverName = (candidates: unknown[], fallback?: string): string | undefined => {
        for (const candidate of candidates) {
            const key = String(candidate ?? "").trim();
            if (!key) continue;
            const hit = driverNameByKey.get(key);
            if (hit !== undefined) return hit;
        }
        return fallback;
    };
    return { resolveDriverName };
}

const SIGNED_RUNSHEET_STOP_RE = /^stop_\d+_runsheet_received$/;

/**
 * Every delivered trip of one customer in a period, with the signed run-sheet URLs.
 *
 * Unlike `fetchBillingTripRows` this includes un-billed trips: it queries all delivered
 * trips in the period and resolves the customer per-trip (billingCustomerId, then the
 * task's linked customer), because un-billed trips carry no `billingCustomerId`.
 */
export async function fetchShopeeExpressReportTrips(
    customerId: string,
    period: { month: number; year: number },
    half: BillingHalf = "full"
): Promise<ShopeeReportTripRow[]> {
    const cid = customerId.trim();
    if (!cid) return [];
    // Half-month rounds split at day 16 midnight (local). The status+deliveredTimestamp range
    // query is unchanged in shape, so it reuses the same composite index as the full-month query.
    const monthStart = new Date(period.year, period.month - 1, 1);
    const monthEnd = new Date(period.year, period.month, 1); // exclusive (first of next month)
    const mid = new Date(period.year, period.month - 1, 16); // day 16, 00:00
    const start = half === "second" ? mid : monthStart;
    const end = half === "first" ? mid : monthEnd;

    const tripSnap = await getDocsFromServer(
        query(
            collection(db, COLLECTIONS.TRIP_RECORDS),
            where("status", "==", "delivered"),
            where("deliveredTimestamp", ">=", Timestamp.fromDate(start)),
            where("deliveredTimestamp", "<", Timestamp.fromDate(end))
        )
    );

    // Batch-fetch linked tasks for driver / route / vehicle / linked-customer.
    type TaskInfo = {
        truckType?: string;
        driverId?: string;
        driverName?: string;
        truckLicensePlate?: string;
        sourceHub?: string;
        destination?: string;
        sourceHubLinkedCustomerId?: string;
        destinationLinkedCustomerId?: string;
    };
    const taskMap = new Map<string, TaskInfo>();
    const taskIds = new Set<string>();
    tripSnap.forEach((d) => {
        const tid = d.data().taskId;
        if (tid) taskIds.add(tid);
    });
    const allTaskIds = Array.from(taskIds);
    const taskIdChunks: string[][] = [];
    for (let i = 0; i < allTaskIds.length; i += 30) taskIdChunks.push(allTaskIds.slice(i, i + 30));
    await Promise.allSettled(
        taskIdChunks.map(async (chunk) => {
            const snap = await getDocs(
                query(collection(db, COLLECTIONS.TASKS), where(documentId(), "in", chunk))
            );
            snap.forEach((td) => {
                const t = td.data();
                taskMap.set(td.id, {
                    truckType: t.truckType,
                    driverId: t.driverId,
                    driverName: t.driverName,
                    truckLicensePlate: t.licensePlate,
                    sourceHub: t.sourceHub,
                    destination: t.destination,
                    sourceHubLinkedCustomerId:
                        typeof t.sourceHubLinkedCustomerId === "string"
                            ? t.sourceHubLinkedCustomerId.trim()
                            : undefined,
                    destinationLinkedCustomerId:
                        typeof t.destinationLinkedCustomerId === "string"
                            ? t.destinationLinkedCustomerId.trim()
                            : undefined,
                });
            });
        })
    );

    const [{ resolveDisplayName }, { resolveDriverName }] = await Promise.all([
        buildHubDisplayResolver(),
        buildDriverNameResolver(),
    ]);

    const rows: ShopeeReportTripRow[] = [];
    tripSnap.forEach((d) => {
        const data = d.data();
        const taskInfo = data.taskId ? taskMap.get(data.taskId) : undefined;

        // Resolve customer: direct billing field, else the task's linked customer.
        const resolvedCustomer =
            (typeof data.billingCustomerId === "string" && data.billingCustomerId.trim()) ||
            taskInfo?.sourceHubLinkedCustomerId ||
            taskInfo?.destinationLinkedCustomerId ||
            "";
        if (resolvedCustomer !== cid) return;

        const photos = Array.isArray(data.photos)
            ? (data.photos as Array<{ type?: string; url?: string }>)
            : [];
        const signedRunsheetPhotos = photos
            .filter(
                (p) =>
                    typeof p?.url === "string" &&
                    p.url.trim() !== "" &&
                    typeof p?.type === "string" &&
                    (p.type === "runsheet_received" || SIGNED_RUNSHEET_STOP_RE.test(p.type))
            )
            .map((p) => p.url as string);

        const originCode =
            (data.billingLookupHubId as string) || taskInfo?.sourceHub || (data.origin as string) || "";
        const destCode =
            (data.billingLookupDestination as string) ||
            taskInfo?.destination ||
            (data.destination as string) ||
            "";

        rows.push({
            id: d.id,
            spxTripId: data.spxTripId,
            deliveredTimestamp: toBillingDate(data.deliveredTimestamp),
            driverId: (data.driverId as string) ?? taskInfo?.driverId,
            driverName: resolveDriverName([data.driverId, taskInfo?.driverId], taskInfo?.driverName) ?? "-",
            originDisplay: resolveDisplayName(originCode),
            destinationDisplay: resolveDisplayName(destCode),
            vehicleClass: taskInfo?.truckType,
            truckLicensePlate: taskInfo?.truckLicensePlate,
            parcelCount: typeof data.parcelCount === "number" ? data.parcelCount : undefined,
            signedRunsheetPhotos,
        });
    });

    rows.sort((a, b) => {
        const byDate = (a.deliveredTimestamp?.getTime() ?? 0) - (b.deliveredTimestamp?.getTime() ?? 0);
        if (byDate !== 0) return byDate;
        return a.driverName.localeCompare(b.driverName, "th");
    });
    return rows;
}

// ─── Standby billing diagnostics (ADR 0008 §6, §8-9) ──────────────────────────
//
// `fetchBillingTripRows` returns only rows that can actually be billed. Anything failing a billing
// precondition used to be dropped there with no counter and no warning, so unbilled work was
// indistinguishable from no work. These diagnostics are a SEPARATE channel on purpose: a row with no
// price must never be able to reach the invoice set (ADR 0005), so it is never mixed into the rows
// the download path bills.

export type StandbyIssueReason =
    /** No customerId on the record and none resolvable through its task — nothing can price it. */
    | "no_customer"
    /** Customer known, but no standby_rate_entries row and no "standby" customer_service_fees entry. */
    | "no_rate"
    /** Customer and rate both exist — billing simply has not been computed yet. */
    | "not_computed"
    /** No endedAt at all: excluded by every period range query, so invisible in every month. */
    | "no_ended_at";

export interface StandbyBillingIssue {
    id: string;
    reason: StandbyIssueReason;
    driverId?: string;
    driverName?: string;
    startLocation?: string;
    endLocation?: string;
    startedAt?: Date;
    endedAt?: Date;
    createdAt?: Date;
    durationMinutes?: number;
    /** Resolved customer (direct field or via task), when one was found. */
    customerId?: string;
}

export interface StandbyBillingDiagnostics {
    /** Completed standby inside the period that produced no billable row. */
    unpriced: StandbyBillingIssue[];
    /** Completed standby with no `endedAt` — belongs to no month at all. Not period-scoped. */
    missingEndedAt: StandbyBillingIssue[];
    /** True when the standby query itself failed (missing/building index, rules) — ADR 0008 §8. */
    queryFailed: boolean;
    queryError?: string;
}

/** How many recent standby docs to scan when hunting for records with no `endedAt`. */
const STANDBY_DIAGNOSTIC_SCAN_LIMIT = 300;

function standbyIssueFromDoc(
    id: string,
    data: DocumentData,
    reason: StandbyIssueReason,
    driverNameByKey: Map<string, string>,
    resolvedCustomerId?: string
): StandbyBillingIssue {
    return {
        id,
        reason,
        driverId: typeof data.driverId === "string" ? data.driverId : undefined,
        driverName: driverNameByKey.get(String(data.driverId ?? "").trim()),
        startLocation: typeof data.startLocation === "string" ? data.startLocation : undefined,
        endLocation: typeof data.endLocation === "string" ? data.endLocation : undefined,
        startedAt: toBillingDate(data.startedAt),
        endedAt: toBillingDate(data.endedAt),
        createdAt: toBillingDate(data.createdAt),
        durationMinutes: typeof data.durationMinutes === "number" ? data.durationMinutes : undefined,
        customerId: resolvedCustomerId,
    };
}

/**
 * Why standby is missing from a billing period. Runs the same period query the Billing Document uses,
 * then classifies every completed record that produced no price, so the admin is told which fix
 * applies — assign a customer, add a standby rate, or just run the compute.
 */
export async function fetchStandbyBillingDiagnostics(
    customerId: string | "all",
    period: { month: number; year: number }
): Promise<StandbyBillingDiagnostics> {
    const start = new Date(period.year, period.month - 1, 1);
    const end = new Date(period.year, period.month, 1);

    let periodSnap: QuerySnapshot<DocumentData>;
    let recentSnap: QuerySnapshot<DocumentData>;
    try {
        [periodSnap, recentSnap] = await Promise.all([
            getDocsFromServer(
                query(
                    collection(db, COLLECTIONS.STANDBY_RECORDS),
                    where("status", "==", "completed"),
                    where("endedAt", ">=", Timestamp.fromDate(start)),
                    where("endedAt", "<", Timestamp.fromDate(end))
                )
            ),
            // Records with no `endedAt` cannot be found by a range filter — Firestore never returns
            // documents that lack the field — so they are hunted through a bounded recent scan.
            getDocsFromServer(
                query(
                    collection(db, COLLECTIONS.STANDBY_RECORDS),
                    where("status", "==", "completed"),
                    orderBy("createdAt", "desc"),
                    limit(STANDBY_DIAGNOSTIC_SCAN_LIMIT)
                )
            ),
        ]);
    } catch (e) {
        return {
            unpriced: [],
            missingEndedAt: [],
            queryFailed: true,
            queryError: e instanceof Error ? e.message : String(e),
        };
    }

    const candidates: Array<{ id: string; data: DocumentData }> = [];
    periodSnap.forEach((d) => {
        const data = d.data();
        if (Number(data.billingEstimateThb)) return;
        candidates.push({ id: d.id, data });
    });

    const orphans: Array<{ id: string; data: DocumentData }> = [];
    recentSnap.forEach((d) => {
        const data = d.data();
        if (data.endedAt) return;
        orphans.push({ id: d.id, data });
    });

    if (candidates.length === 0 && orphans.length === 0) {
        return { unpriced: [], missingEndedAt: [], queryFailed: false };
    }

    // Resolve the customer exactly the way the Cloud Function does: direct field, else via the task.
    const all = [...candidates, ...orphans];
    const taskIds = new Set<string>();
    all.forEach(({ data }) => {
        if (typeof data.customerId === "string" && data.customerId.trim()) return;
        const tid = typeof data.taskId === "string" ? data.taskId.trim() : "";
        if (tid) taskIds.add(tid);
    });

    const taskCustomerById = new Map<string, string>();
    const taskIdList = [...taskIds];
    const taskIdChunks: string[][] = [];
    for (let i = 0; i < taskIdList.length; i += 30) taskIdChunks.push(taskIdList.slice(i, i + 30));
    await Promise.allSettled(
        taskIdChunks.map(async (chunk) => {
            const snap = await getDocs(
                query(collection(db, COLLECTIONS.TASKS), where(documentId(), "in", chunk))
            );
            snap.forEach((t) => {
                const d = t.data();
                const cid =
                    (typeof d.sourceHubLinkedCustomerId === "string" ? d.sourceHubLinkedCustomerId.trim() : "") ||
                    (typeof d.destinationLinkedCustomerId === "string" ? d.destinationLinkedCustomerId.trim() : "");
                if (cid) taskCustomerById.set(t.id, cid);
            });
        })
    );

    const resolveCustomer = (data: DocumentData): string | undefined => {
        const direct = typeof data.customerId === "string" ? data.customerId.trim() : "";
        if (direct) return direct;
        const tid = typeof data.taskId === "string" ? data.taskId.trim() : "";
        return tid ? taskCustomerById.get(tid) : undefined;
    };

    // Which customers actually have a standby price configured (rate entry or flat service fee)?
    const customersWithRate = new Set<string>();
    await Promise.allSettled([
        (async () => {
            const snap = await getDocs(collection(db, COLLECTIONS.STANDBY_RATE_ENTRIES));
            snap.forEach((d) => {
                const cid = String(d.data().customerId ?? "").trim();
                if (cid) customersWithRate.add(cid);
            });
        })(),
        (async () => {
            const snap = await getDocs(collection(db, COLLECTIONS.CUSTOMER_SERVICE_FEES));
            snap.forEach((d) => {
                const data = d.data();
                if (data.feeType !== "standby") return;
                const cid = String(data.customerId ?? "").trim();
                if (cid) customersWithRate.add(cid);
            });
        })(),
    ]);

    const driverNameByKey = new Map<string, string>();
    try {
        const driversSnap = await getDocs(collection(db, COLLECTIONS.DRIVERS));
        driversSnap.forEach((ds) => {
            const dd = ds.data();
            const name = driverDisplayName(dd, ds.id);
            driverNameByKey.set(ds.id, name);
            const authId = (dd.authId ?? dd.authUid) as string | undefined;
            if (authId) driverNameByKey.set(authId, name);
        });
    } catch (e) {
        console.warn("[fetchStandbyBillingDiagnostics] driver name lookup failed:", e);
    }

    const classify = (data: DocumentData): { reason: StandbyIssueReason; customerId?: string } => {
        const cid = resolveCustomer(data);
        if (!cid) return { reason: "no_customer" };
        if (!customersWithRate.has(cid)) return { reason: "no_rate", customerId: cid };
        return { reason: "not_computed", customerId: cid };
    };

    const unpriced = candidates
        .filter(({ data }) => {
            // With one customer selected, surface only records that belong to it — or that cannot be
            // placed at all, since those are exactly the ones at risk of never being billed.
            if (customerId === "all") return true;
            const cid = resolveCustomer(data);
            return !cid || cid === customerId;
        })
        .map(({ id, data }) => {
            const { reason, customerId: cid } = classify(data);
            return standbyIssueFromDoc(id, data, reason, driverNameByKey, cid);
        });

    const missingEndedAt = orphans.map(({ id, data }) =>
        standbyIssueFromDoc(id, data, "no_ended_at", driverNameByKey, resolveCustomer(data))
    );

    return { unpriced, missingEndedAt, queryFailed: false };
}

/**
 * Admin repair for one unpriced standby (ADR 0008 §7 — case by case, never a bulk default customer):
 * stamp the chosen customer on the record, then price it through the existing callable.
 */
export async function assignStandbyCustomerAndPrice(
    standbyId: string,
    newCustomerId: string
): Promise<{ ok: boolean; billingEstimateThb?: number; error?: string; blocked?: boolean; invoiceNumber?: string }> {
    const id = standbyId.trim();
    const cid = newCustomerId.trim();
    if (!id) throw new Error("standbyId is required");
    if (!cid) throw new Error("customerId is required");

    await updateDoc(doc(db, COLLECTIONS.STANDBY_RECORDS, id), {
        customerId: cid,
        customerResolved: true,
        customerResolvedFrom: "manual",
        updatedAt: serverTimestamp(),
    });

    const fn = httpsCallable<
        { standbyId: string; forceRecompute?: boolean },
        { ok: boolean; billingEstimateThb?: number; error?: string; blocked?: boolean; invoiceNumber?: string }
    >(functions, "computeStandbyBillingSnapshot");
    const res = await fn({ standbyId: id, forceRecompute: true });
    return res.data;
}
