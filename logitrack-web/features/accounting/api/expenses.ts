"use client";

import { auth, db, storage } from "@/firebase/client";
import {
    collection,
    getDocs,
    query,
    orderBy,
    doc,
    setDoc,
    updateDoc,
    serverTimestamp,
    Timestamp,
    writeBatch,
} from "firebase/firestore";
import { ref as storageRefFn, uploadBytes, getDownloadURL } from "firebase/storage";
import { COLLECTIONS } from "@/lib/collections";
import { driverDisplayName } from "@/lib/driverName";

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

function parseDate(v: unknown): Date | undefined {
    if (!v) return undefined;
    if (v instanceof Date) return v;
    if (typeof (v as { toDate?: () => Date }).toDate === "function") return (v as { toDate: () => Date }).toDate();
    if (typeof v === "string") return new Date(v);
    return undefined;
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
        driversSnapshot.forEach((docSnap) => {
            const d = docSnap.data();
            const name = driverDisplayName(d, docSnap.id);
            driverNameByKey.set(docSnap.id, name);
            const authId = (d.authId ?? d.authUid) as string | undefined;
            if (authId) driverNameByKey.set(authId, name);
        });
        const truckPlateMap = new Map<string, string>();
        trucksSnapshot.forEach((docSnap) => {
            const d = docSnap.data();
            truckPlateMap.set(docSnap.id, (d.licensePlate as string) ?? "");
        });

        return byType.map((doc) => {
            const d = doc.data();
            const driverId = d.driverId ?? "";
            // ใช้เฉพาะ snapshot ที่บันทึกไว้ในเอกสาร — ไม่ fallback ไป assignment ปัจจุบันของคนขับ
            // มิฉะนั้นรายการเก่าจะเปลี่ยนรถ/ทะเบียนตามเมื่อคนขับถูกย้ายไปคันใหม่
            const truckId = (d.truckId as string | undefined) ?? undefined;
            const driverName = driverNameByKey.get(driverId);
            // truckLicensePlate ที่ snapshot ไว้มาก่อน; ถ้าไม่มี ค่อยใช้ทะเบียนของรถที่ระบุไว้ในเอกสาร
            // (ทะเบียนต่อรถคงที่ ปลอดภัยกว่าการ resolve ผ่านคนขับ)
            const storedPlate = d.truckLicensePlate as string | undefined;
            const licensePlate = storedPlate || (truckId ? truckPlateMap.get(truckId) : undefined);
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
            const name = driverDisplayName(d, docSnap.id);
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
            const name = driverDisplayName(d, docSnap.id);
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
        truckLicensePlate: string;
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
    if (data.truckLicensePlate !== undefined) payload.truckLicensePlate = data.truckLicensePlate;
    if (data.tollImportSequence != null) payload.tollImportSequence = Number(data.tollImportSequence);
    if (data.tollLocation !== undefined) payload.tollLocation = data.tollLocation;
    if (data.tollLane !== undefined) payload.tollLane = data.tollLane;
    if (data.tollSourceType !== undefined) payload.tollSourceType = data.tollSourceType;
    await updateDoc(ref, payload);
}

export interface CreateFuelExpenseInput {
    driverId: string;
    truckId?: string;
    truckLicensePlate?: string;
    date: Date;
    amount: number;
    volumeLiters?: number;
    pricePerLiter?: number;
    odometer?: number;
    stationTaxId?: string;
    taxInvId?: string;
    note?: string;
    /** Optional receipt / odometer photos — admin-entered records source data from an external petro app, so photos aren't required. */
    receiptPhotoFile?: File;
    odometerPhotoFile?: File;
}

/**
 * Admin-entered fuel expense (on behalf of a driver who can't record it themselves via
 * mobile). Written directly with status "APPROVED" since the admin already trusts the
 * source data (petro app), skipping the normal driver-submitted PENDING review step.
 */
export async function createFuelExpense(input: CreateFuelExpenseInput): Promise<string> {
    const ref = doc(collection(db, COLLECTIONS.VEHICLE_EXPENSES));

    async function uploadPhoto(file: File, photoType: string): Promise<string> {
        const storagePath = `vehicle_expenses/${ref.id}/${photoType}.jpg`;
        const storageRef = storageRefFn(storage, storagePath);
        await uploadBytes(storageRef, file, { contentType: "image/jpeg" });
        return getDownloadURL(storageRef);
    }
    const receiptPhotoUrl = input.receiptPhotoFile ? await uploadPhoto(input.receiptPhotoFile, "receipt") : undefined;
    const odometerPhotoUrl = input.odometerPhotoFile ? await uploadPhoto(input.odometerPhotoFile, "odometer") : undefined;

    const payload: Record<string, unknown> = {
        driverId: input.driverId,
        type: "fuel",
        date: Timestamp.fromDate(input.date),
        amount: Number(input.amount),
        status: "APPROVED",
        createdAt: serverTimestamp(),
        updatedAt: serverTimestamp(),
        adminNote: "บันทึกโดย admin (ข้อมูลจาก petro app)",
        createdByUid: auth.currentUser?.uid ?? null,
    };
    if (input.truckId) payload.truckId = input.truckId;
    if (input.truckLicensePlate) payload.truckLicensePlate = input.truckLicensePlate;
    if (input.volumeLiters != null) payload.volumeLiters = Number(input.volumeLiters);
    if (input.pricePerLiter != null) payload.pricePerLiter = Number(input.pricePerLiter);
    if (input.odometer != null) payload.odometer = Number(input.odometer);
    if (input.stationTaxId) payload.stationTaxId = input.stationTaxId;
    if (input.taxInvId) payload.taxInvId = input.taxInvId;
    if (input.note) payload.note = input.note;
    if (receiptPhotoUrl) payload.receiptPhotoUrl = receiptPhotoUrl;
    if (odometerPhotoUrl) payload.odometerPhotoUrl = odometerPhotoUrl;

    await setDoc(ref, payload);
    return ref.id;
}

export interface CreateOtherExpenseInput {
    driverId: string;
    truckId?: string;
    truckLicensePlate?: string;
    date: Date;
    amount: number;
    category?: string;
    description?: string;
    note?: string;
    /** Optional proof photo (receipt, etc.) — admin-entered records don't require one. */
    receiptPhotoFile?: File;
}

/**
 * Admin-entered "other" vehicle expense (toll, parking, repair, etc.) on behalf of a
 * driver who can't record it themselves via mobile. Written directly with status
 * "APPROVED" since the admin already trusts the source data.
 */
export async function createOtherExpense(input: CreateOtherExpenseInput): Promise<string> {
    const ref = doc(collection(db, COLLECTIONS.VEHICLE_EXPENSES));

    let receiptPhotoUrl: string | undefined;
    if (input.receiptPhotoFile) {
        const storagePath = `vehicle_expenses/${ref.id}/receipt.jpg`;
        const storageRef = storageRefFn(storage, storagePath);
        await uploadBytes(storageRef, input.receiptPhotoFile, { contentType: "image/jpeg" });
        receiptPhotoUrl = await getDownloadURL(storageRef);
    }

    const payload: Record<string, unknown> = {
        driverId: input.driverId,
        type: "other",
        date: Timestamp.fromDate(input.date),
        amount: Number(input.amount),
        status: "APPROVED",
        createdAt: serverTimestamp(),
        updatedAt: serverTimestamp(),
        adminNote: "บันทึกโดย admin",
        createdByUid: auth.currentUser?.uid ?? null,
    };
    if (input.truckId) payload.truckId = input.truckId;
    if (input.truckLicensePlate) payload.truckLicensePlate = input.truckLicensePlate;
    if (input.category) payload.category = input.category;
    if (input.description) payload.description = input.description;
    if (input.note) payload.note = input.note;
    if (receiptPhotoUrl) payload.receiptPhotoUrl = receiptPhotoUrl;

    await setDoc(ref, payload);
    return ref.id;
}
