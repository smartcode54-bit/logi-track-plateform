"use client";

import { db } from "@/firebase/client";
import { collection, getDocs, query, orderBy } from "firebase/firestore";
import { COLLECTIONS } from "@/lib/collections";

export type VehicleExpenseType = "fuel" | "other";

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
    note?: string;
    category?: string;
    description?: string;
    createdAt?: Date;
    receiptPhotoUrl?: string;
    odometerPhotoUrl?: string;
}

export interface DriverOption {
    id: string;
    name: string;
}

export interface TruckOption {
    id: string;
    licensePlate: string;
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
                note: d.note ?? undefined,
                category: d.category ?? undefined,
                description: d.description ?? undefined,
                createdAt: parseDate(d.createdAt),
                receiptPhotoUrl: (d.receiptPhotoUrl as string) ?? undefined,
                odometerPhotoUrl: (d.odometerPhotoUrl as string) ?? undefined,
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
