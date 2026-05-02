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
import { COLLECTIONS } from "@/lib/collections";

export type VehicleExpenseType = "fuel" | "other";

export type VehicleExpenseStatus = "PENDING" | "APPROVED" | "REJECTED";

export interface VehicleExpenseRow {
    id: string;
    driverId: string;
    driverName?: string;
    truckId?: string;
    licensePlate?: string;
    type: VehicleExpenseType;
    date: Date;
    amount: number;
    volumeLiters?: number;
    pricePerLiter?: number;
    odometer?: number;
    stationTaxId?: string;
    taxInvId?: string;
    refillLocation?: string;
    note?: string;
    category?: string;
    description?: string;
    status: VehicleExpenseStatus;
    adminNote?: string;
    createdAt?: Date;
    updatedAt?: Date;
    receiptPhotoUrl?: string;
    odometerPhotoUrl?: string;
    /** Toll import / admin — sequence from source file */
    tollImportSequence?: number;
    tollLocation?: string;
    tollLane?: string;
    tollSourceType?: string;
}

export interface DriverOption {
    id: string;
    name: string;
}

export interface TruckOption {
    id: string;
    licensePlate: string;
}

/** Driver row for toll import: filterId matches vehicle_expenses.driverId (authUid ?? doc id). */
export interface DriverWithTruckAssignment {
    filterId: string;
    name: string;
    truckId?: string;
}

export interface TollImportRowInput {
    tollImportSequence?: number;
    tollLocation?: string;
    tollLane?: string;
    tollSourceType?: string;
    date: Date;
    amount: number;
    description?: string;
}

export interface CustomerRateEntryInput {
    hubId: string;
    rawHubName: string;
    destinationCode: string;
    vehicleClass: string;
    rateThb: number;
    distanceKm?: number;
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
}

export interface CustomerFuelRateAdjustmentRow extends CustomerFuelRateAdjustmentInput {
    id: string;
    createdAt?: Date;
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

export async function getVehicleExpensesByType(type: VehicleExpenseType): Promise<VehicleExpenseRow[]> {
    try {
        const q = query(
            collection(db, COLLECTIONS.VEHICLE_EXPENSES),
            orderBy("date", "desc")
        );
        const snapshot = await getDocs(q);
        const byType = snapshot.docs.filter((doc) => (doc.data().type as string) === type);
        const [driversSnapshot, trucksSnapshot] = await Promise.all([
            getDocs(collection(db, COLLECTIONS.DRIVERS)),
            getDocs(collection(db, COLLECTIONS.TRUCKS)),
        ]);
        const driverNameByKey = new Map<string, string>();
        const authIdToTruckId = new Map<string, string>();
        driversSnapshot.forEach((docSnap) => {
            const d = docSnap.data();
            const firstName = d.firstName ?? "";
            const lastName = d.lastName ?? "";
            const name = (d.name as string)?.trim()
                || [firstName, lastName].filter(Boolean).join(" ").trim()
                || (d.email as string)
                || docSnap.id;
            driverNameByKey.set(docSnap.id, name);
            const authId = (d.authId ?? d.authUid) as string | undefined;
            if (authId) driverNameByKey.set(authId, name);
            const assignment = d.currentAssignment ?? (Array.isArray(d.currentAssignments) && d.currentAssignments.length > 0 ? d.currentAssignments[0] : null);
            const assignedTruckId = assignment?.truckId as string | undefined;
            if (authId && assignedTruckId) authIdToTruckId.set(authId, assignedTruckId);
        });
        const truckPlateMap = new Map<string, string>();
        trucksSnapshot.forEach((docSnap) => {
            const d = docSnap.data();
            truckPlateMap.set(docSnap.id, (d.licensePlate as string) ?? "");
        });

        return byType.map((doc) => {
            const d = doc.data();
            const driverId = d.driverId ?? "";
            const truckId = (d.truckId as string | undefined) ?? authIdToTruckId.get(driverId);
            const driverName = driverNameByKey.get(driverId);
            const licensePlate = truckId ? truckPlateMap.get(truckId) : undefined;
            return {
                id: doc.id,
                driverId,
                driverName: driverName ?? undefined,
                truckId: truckId ?? undefined,
                licensePlate: licensePlate ?? undefined,
                type: (d.type as VehicleExpenseType) ?? type,
                date: parseDate(d.date) ?? new Date(),
                amount: Number(d.amount) ?? 0,
                volumeLiters: d.volumeLiters != null ? Number(d.volumeLiters) : undefined,
                pricePerLiter: d.pricePerLiter != null ? Number(d.pricePerLiter) : undefined,
                odometer: d.odometer != null ? Number(d.odometer) : undefined,
                stationTaxId: (d.stationTaxId as string) ?? undefined,
                taxInvId: (d.taxInvId as string) ?? undefined,
                refillLocation: (d.refillLocation as string) ?? undefined,
                note: d.note ?? undefined,
                category: d.category ?? undefined,
                description: d.description ?? undefined,
                status: ((d.status as string) ?? "PENDING") as VehicleExpenseStatus,
                adminNote: (d.adminNote as string) ?? undefined,
                createdAt: parseDate(d.createdAt),
                updatedAt: parseDate(d.updatedAt),
                receiptPhotoUrl: (d.receiptPhotoUrl as string) ?? undefined,
                odometerPhotoUrl: (d.odometerPhotoUrl as string) ?? undefined,
                tollImportSequence: d.tollImportSequence != null ? Number(d.tollImportSequence) : undefined,
                tollLocation: (d.tollLocation as string) ?? undefined,
                tollLane: (d.tollLane as string) ?? undefined,
                tollSourceType: (d.tollSourceType as string) ?? undefined,
            };
        });
    } catch (err) {
        console.error("Error fetching vehicle expenses:", err);
        return [];
    }
}

export async function getDriversForFilter(): Promise<DriverOption[]> {
    try {
        const snapshot = await getDocs(collection(db, COLLECTIONS.DRIVERS));
        return snapshot.docs.map((docSnap) => {
            const d = docSnap.data();
            const name = (d.name as string)?.trim()
                || [d.firstName, d.lastName].filter(Boolean).join(" ").trim()
                || (d.email as string)
                || docSnap.id;
            const authId = (d.authId ?? d.authUid) as string | undefined;
            return { id: authId ?? docSnap.id, name };
        }).filter((x) => x.name);
    } catch (err) {
        console.error("Error fetching drivers:", err);
        return [];
    }
}

export async function getTrucksForFilter(): Promise<TruckOption[]> {
    try {
        const snapshot = await getDocs(collection(db, COLLECTIONS.TRUCKS));
        return snapshot.docs.map((doc) => ({
            id: doc.id,
            licensePlate: (doc.data().licensePlate as string) ?? doc.id,
        })).filter((x) => x.licensePlate);
    } catch (err) {
        console.error("Error fetching trucks:", err);
        return [];
    }
}

/** สำหรับ import ค่าผ่านทาง: ดึงคนขับทั้งหมดพร้อม truck จาก currentAssignment (หรือ legacy truckId) */
export async function getDriversWithTruckAssignments(): Promise<DriverWithTruckAssignment[]> {
    try {
        const snapshot = await getDocs(collection(db, COLLECTIONS.DRIVERS));
        return snapshot.docs.map((docSnap) => {
            const d = docSnap.data();
            const name = (d.name as string)?.trim()
                || [d.firstName, d.lastName].filter(Boolean).join(" ").trim()
                || (d.email as string)
                || docSnap.id;
            const authId = (d.authId ?? d.authUid) as string | undefined;
            const filterId = authId ?? docSnap.id;
            const assignment = d.currentAssignment ?? (Array.isArray(d.currentAssignments) && d.currentAssignments.length > 0 ? d.currentAssignments[0] : null);
            const truckId = (assignment?.truckId as string | undefined) ?? (d.truckId as string | undefined);
            return { filterId, name, truckId };
        }).filter((x) => x.name);
    } catch (err) {
        console.error("Error fetching drivers with trucks:", err);
        return [];
    }
}

const BATCH_LIMIT = 450;

function tollImportDescription(row: TollImportRowInput): string {
    if (row.description?.trim()) return row.description.trim();
    const parts: string[] = [];
    if (row.tollSourceType?.trim()) parts.push(row.tollSourceType.trim());
    if (row.tollLocation?.trim()) parts.push(row.tollLocation.trim());
    if (row.tollLane?.trim()) parts.push(row.tollLane.trim());
    return parts.length > 0 ? parts.join(" · ") : "Toll";
}

/** สร้างรายการค่าผ่านทางแบบ batch (admin import) */
export async function batchCreateTollExpenseImports(
    rows: TollImportRowInput[],
    driverFilterId: string,
    truckId: string,
    status: VehicleExpenseStatus = "PENDING"
): Promise<void> {
    if (!driverFilterId.trim() || !truckId.trim()) {
        throw new Error("batchCreateTollExpenseImports: driver and truck required");
    }
    const col = collection(db, COLLECTIONS.VEHICLE_EXPENSES);
    for (let i = 0; i < rows.length; i += BATCH_LIMIT) {
        const chunk = rows.slice(i, i + BATCH_LIMIT);
        const batch = writeBatch(db);
        for (const row of chunk) {
            const ref = doc(col);
            const desc = tollImportDescription(row);
            const payload: Record<string, unknown> = {
                driverId: driverFilterId.trim(),
                truckId: truckId.trim(),
                type: "other",
                category: "toll",
                date: Timestamp.fromDate(row.date),
                amount: Number(row.amount),
                status,
                description: desc,
                createdAt: serverTimestamp(),
                updatedAt: serverTimestamp(),
            };
            if (row.tollImportSequence != null && Number.isFinite(row.tollImportSequence)) {
                payload.tollImportSequence = row.tollImportSequence;
            }
            if (row.tollLocation?.trim()) payload.tollLocation = row.tollLocation.trim();
            if (row.tollLane?.trim()) payload.tollLane = row.tollLane.trim();
            if (row.tollSourceType?.trim()) payload.tollSourceType = row.tollSourceType.trim();
            batch.set(ref, payload);
        }
        await batch.commit();
    }
}

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
            const destinationCode = normalizeCode(row.destinationCode);
            const vehicleClass = normalizeCode(row.vehicleClass || "4WJ");
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
    const destinationCode = normalizeCode(row.destinationCode);
    const vehicleClass = normalizeCode(row.vehicleClass || "4WJ");
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
            effectiveFrom: parseDate(d.effectiveFrom) ?? new Date(0),
            importedAt: parseDate(d.importedAt),
        };
    }).sort((a, b) => {
        const byDate = b.effectiveFrom.getTime() - a.effectiveFrom.getTime();
        if (byDate !== 0) return byDate;
        return b.importId.localeCompare(a.importId);
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
        effectiveFrom: Timestamp.fromDate(parseDateOnly(input.effectiveFrom)),
        rateMultiplier: Number(input.rateMultiplier),
        addThbPerTrip: Number(input.addThbPerTrip ?? 0),
        referenceFuelPriceThbPerLitre:
            input.referenceFuelPriceThbPerLitre != null
                ? Number(input.referenceFuelPriceThbPerLitre)
                : null,
        announcementNote: input.announcementNote?.trim() || "",
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
            createdAt: parseDate(d.createdAt),
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
        updatedAt: serverTimestamp(),
    });
}

/** ดึงรายการ vehicle expenses ทั้ง fuel และ other สำหรับหน้าตรวจสอบ (filter ตาม status ได้) */
export async function getVehicleExpensesForAudit(statusFilter?: VehicleExpenseStatus | "all"): Promise<VehicleExpenseRow[]> {
    try {
        const fuelRows = await getVehicleExpensesByType("fuel");
        const otherRows = await getVehicleExpensesByType("other");
        const all = [...fuelRows, ...otherRows].sort((a, b) => (b.date.getTime()) - (a.date.getTime()));
        if (statusFilter && statusFilter !== "all") {
            return all.filter((r) => r.status === statusFilter);
        }
        return all;
    } catch (err) {
        console.error("Error fetching vehicle expenses for audit:", err);
        return [];
    }
}

/** อัปเดตสถานะรายการค่าใช้จ่าย (อนุมัติ/ปฏิเสธ) */
export async function updateVehicleExpenseStatus(
    id: string,
    status: VehicleExpenseStatus,
    adminNote?: string
): Promise<void> {
    const ref = doc(db, COLLECTIONS.VEHICLE_EXPENSES, id);
    await updateDoc(ref, {
        status,
        ...(adminNote != null && adminNote.trim() !== "" ? { adminNote: adminNote.trim() } : {}),
        updatedAt: serverTimestamp(),
    });
}

const BATCH_STATUS_CHUNK = 450;

/** อัปเดตสถานะหลายรายการพร้อมกัน (สูงสุด ~450 รายการต่อ commit ตามขีดจำกัด batch ของ Firestore) */
export async function batchUpdateVehicleExpenseStatuses(
    items: { id: string; status: VehicleExpenseStatus; adminNote?: string }[]
): Promise<void> {
    if (items.length === 0) return;
    for (let i = 0; i < items.length; i += BATCH_STATUS_CHUNK) {
        const chunk = items.slice(i, i + BATCH_STATUS_CHUNK);
        const batch = writeBatch(db);
        let ops = 0;
        for (const u of chunk) {
            const normalizedId = u.id.trim();
            if (!normalizedId) continue;
            const ref = doc(db, COLLECTIONS.VEHICLE_EXPENSES, normalizedId);
            const payload: Record<string, unknown> = {
                status: u.status,
                updatedAt: serverTimestamp(),
            };
            if (u.adminNote != null && u.adminNote.trim() !== "") {
                payload.adminNote = u.adminNote.trim();
            }
            batch.update(ref, payload);
            ops += 1;
        }
        if (ops > 0) await batch.commit();
    }
}

/** อัปเดตรายการค่าใช้จ่าย (ทุก field ที่แก้ได้) */
export async function updateVehicleExpense(
    id: string,
    data: Partial<{
        date: Date;
        amount: number;
        volumeLiters: number;
        pricePerLiter: number;
        odometer: number;
        distanceKm: number;
        stationTaxId: string;
        taxInvId: string;
        refillLocation: string;
        note: string;
        category: string;
        description: string;
        status: VehicleExpenseStatus;
        adminNote: string;
        truckId: string;
        tollImportSequence: number;
        tollLocation: string;
        tollLane: string;
        tollSourceType: string;
    }>
): Promise<void> {
    const ref = doc(db, COLLECTIONS.VEHICLE_EXPENSES, id);
    const payload: Record<string, unknown> = { updatedAt: serverTimestamp() };
    if (data.date != null) payload.date = Timestamp.fromDate(data.date instanceof Date ? data.date : new Date(data.date));
    if (data.amount != null) payload.amount = Number(data.amount);
    if (data.volumeLiters != null) payload.volumeLiters = Number(data.volumeLiters);
    if (data.pricePerLiter != null) payload.pricePerLiter = Number(data.pricePerLiter);
    if (data.odometer != null) payload.odometer = Number(data.odometer);
    if (data.distanceKm != null) payload.distanceKm = Number(data.distanceKm);
    if (data.stationTaxId !== undefined) payload.stationTaxId = data.stationTaxId;
    if (data.taxInvId !== undefined) payload.taxInvId = data.taxInvId;
    if (data.refillLocation !== undefined) payload.refillLocation = data.refillLocation;
    if (data.note !== undefined) payload.note = data.note;
    if (data.category !== undefined) payload.category = data.category;
    if (data.description !== undefined) payload.description = data.description;
    if (data.status != null) payload.status = data.status;
    if (data.adminNote !== undefined) payload.adminNote = data.adminNote;
    if (data.truckId !== undefined) payload.truckId = data.truckId;
    if (data.tollImportSequence != null) payload.tollImportSequence = Number(data.tollImportSequence);
    if (data.tollLocation !== undefined) payload.tollLocation = data.tollLocation;
    if (data.tollLane !== undefined) payload.tollLane = data.tollLane;
    if (data.tollSourceType !== undefined) payload.tollSourceType = data.tollSourceType;
    await updateDoc(ref, payload);
}
