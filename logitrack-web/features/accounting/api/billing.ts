"use client";

import { db } from "@/firebase/client";
import {
    collection,
    deleteDoc,
    documentId,
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
    type TaskInfo = { truckType?: string; driverName?: string; driverPhone?: string; truckLicensePlate?: string; truckId?: string; sourceHub?: string; destination?: string };
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
                driverName: t.driverName,
                driverPhone: t.driverPhone,
                truckLicensePlate: t.licensePlate,
                truckId: t.truckId,
                sourceHub: t.sourceHub,
                destination: t.destination,
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
    const resolveDriverName = (driverId: unknown, fallback?: string): string | undefined => {
        const key = String(driverId ?? "").trim();
        return (key && driverNameByKey.get(key)) || fallback;
    };
    const resolveSubcontractor = (driverId: unknown): string | undefined => {
        const key = String(driverId ?? "").trim();
        return key ? driverSubByKey.get(key) : undefined;
    };

    const rows: BillingTripRow[] = [];

    tripSnap.forEach((d) => {
        const data = d.data();
        if (!Number(data.billingEstimateThb)) return; // skip no-billing trips

        const taskInfo = data.taskId ? taskMap.get(data.taskId) : undefined;
        const hubId = data.billingLookupHubId ?? "";

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
                    driverName: resolveDriverName(data.driverId, taskInfo?.driverName),
                    driverPhone: taskInfo?.driverPhone,
                    subcontractorName: resolveSubcontractor(data.driverId),
                    jobCategory: data.jobCategory === "SUPPLEMENTARY" ? "SUPPLEMENTARY" : "PRIMARY",
                    truckLicensePlate: taskInfo?.truckLicensePlate,
            truckId: taskInfo?.truckId,
                    hubDisplayName: resolveDisplayName(hubId),
                    originHubCode: resolveHubCode(hubId || (taskInfo?.sourceHub as string | undefined) || ""),
                    destinationDisplayName: resolveDisplayName(destCode),
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
            vehicleClass: taskInfo?.truckType,
            driverName: resolveDriverName(data.driverId, taskInfo?.driverName),
            driverPhone: taskInfo?.driverPhone,
            subcontractorName: resolveSubcontractor(data.driverId),
            jobCategory: data.jobCategory === "SUPPLEMENTARY" ? "SUPPLEMENTARY" : "PRIMARY",
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
            driverName: resolveDriverName(data.driverId, taskInfo?.driverName),
            driverPhone: taskInfo?.driverPhone,
            subcontractorName: resolveSubcontractor(data.driverId),
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
            rowType: "standby",
        });
    });

    rows.sort((a, b) => (a.deliveredTimestamp?.getTime() ?? 0) - (b.deliveredTimestamp?.getTime() ?? 0));
    return rows;
}
