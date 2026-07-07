"use client";

import { db } from "@/firebase/client";
import {
    collection,
    deleteDoc,
    getDocs,
    query,
    orderBy,
    doc,
    limit,
    updateDoc,
    serverTimestamp,
    Timestamp,
    writeBatch,
    where,
} from "firebase/firestore";
import { httpsCallable } from "firebase/functions";
import { functions } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { normalizeDestinationCode, normalizeVehicleClass } from "@/lib/billingCompute";

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

export interface CustomerRateEntryRow extends CustomerRateEntryInput {
    id: string;
    customerId: string;
    importId: string;
    effectiveFrom: Date;
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

export interface CustomerFuelRateAdjustmentRow extends CustomerFuelRateAdjustmentInput {
    id: string;
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

function parseDateOnly(value: Date): Date {
    return new Date(Date.UTC(value.getFullYear(), value.getMonth(), value.getDate(), 0, 0, 0, 0));
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
    const effectiveFromTs = Timestamp.fromDate(parseDateOnly(effectiveFrom));
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
                effectiveFrom: effectiveFromTs,
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
    const effectiveFromTs = Timestamp.fromDate(parseDateOnly(effectiveFrom));
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
            effectiveFrom: effectiveFromTs,
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
            importedAt: parseDate(d.importedAt),
        };
    }).sort((a, b) => {
        const byDate = b.effectiveFrom.getTime() - a.effectiveFrom.getTime();
        if (byDate !== 0) return byDate;
        return b.importId.localeCompare(a.importId);
    });
}

export async function deleteCustomerRateEntry(id: string): Promise<void> {
    const ref = doc(db, COLLECTIONS.CUSTOMER_RATE_ENTRIES, id);
    await deleteDoc(ref);
}

export async function updateCustomerRateEntry(
    id: string,
    updates: Partial<Pick<CustomerRateEntryInput, "rateThb" | "distanceKm" | "vehicleClass">> & {
        effectiveFrom?: Date;
    }
): Promise<void> {
    const ref = doc(db, COLLECTIONS.CUSTOMER_RATE_ENTRIES, id);
    const payload: Record<string, unknown> = { updatedAt: serverTimestamp() };
    if (updates.rateThb != null && Number.isFinite(updates.rateThb)) {
        payload.rateThb = Number(updates.rateThb);
    }
    if (updates.distanceKm != null && Number.isFinite(updates.distanceKm)) {
        payload.distanceKm = Number(updates.distanceKm);
    }
    if (updates.vehicleClass?.trim()) {
        payload.vehicleClass = normalizeVehicleClass(updates.vehicleClass);
    }
    if (updates.effectiveFrom) {
        payload.effectiveFrom = Timestamp.fromDate(parseDateOnly(updates.effectiveFrom));
    }
    await updateDoc(ref, payload);
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
        effectiveFrom: Timestamp.fromDate(parseDateOnly(input.effectiveFrom)),
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
            createdAt: parseDate(d.createdAt),
            updatedAt: parseDate(d.updatedAt),
        };
    }).sort((a, b) => b.effectiveFrom.getTime() - a.effectiveFrom.getTime());
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

export async function deleteCustomerFuelRateAdjustment(id: string): Promise<void> {
    const normalizedId = id.trim();
    if (!normalizedId) throw new Error("Adjustment id is required");
    await deleteDoc(doc(db, COLLECTIONS.CUSTOMER_FUEL_RATE_ADJUSTMENTS, normalizedId));
}

export async function updateCustomerFuelRateAdjustment(
    id: string,
    input: CustomerFuelRateAdjustmentInput
): Promise<void> {
    const normalizedId = id.trim();
    const customerId = input.customerId.trim();
    if (!normalizedId) throw new Error("Adjustment id is required");
    if (!customerId) throw new Error("Customer is required");
    if (!Number.isFinite(input.rateMultiplier) || input.rateMultiplier <= 0) {
        throw new Error("rateMultiplier must be greater than 0");
    }

    await updateDoc(doc(db, COLLECTIONS.CUSTOMER_FUEL_RATE_ADJUSTMENTS, normalizedId), {
        customerId,
        effectiveFrom: Timestamp.fromDate(parseDateOnly(input.effectiveFrom)),
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
}
