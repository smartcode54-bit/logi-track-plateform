import { db } from "@/firebase/client";
import { collection, doc, addDoc, updateDoc, serverTimestamp, query, where, getDocs, orderBy } from "firebase/firestore";
import { COLLECTIONS } from "@/lib/collections";
import { TruckData } from "@/features/trucks/services/truckService";
import { z } from "zod";
import { maintenanceSchema, MaintenanceData } from "@/validate/maintenanceSchema";

export type MaintenanceRecord = z.infer<typeof maintenanceSchema> & { id: string };

export interface MaintenanceDashboardData extends MaintenanceRecord {
    truckLicensePlate: string;
    truckBrand: string;
    truckModel: string;
    truckOwnership: string;
    truckStatus?: string;
}

const removeUndefinedFields = <T extends Record<string, any>>(obj: T): Partial<T> => {
    const result: Partial<T> = {};
    for (const key in obj) {
        if (Object.prototype.hasOwnProperty.call(obj, key) && obj[key] !== undefined && obj[key] !== "") {
            result[key] = obj[key];
        }
    }
    return result;
};

export const getMaintenanceOverview = async (): Promise<MaintenanceDashboardData[]> => {
    try {
        const [trucksSnapshot, maintenanceSnapshot] = await Promise.all([
            getDocs(collection(db, COLLECTIONS.TRUCKS)),
            getDocs(query(collection(db, COLLECTIONS.MAINTENANCE), orderBy("createdAt", "desc")))
        ]);

        const truckMap = new Map<string, TruckData>();
        trucksSnapshot.forEach(doc => {
            truckMap.set(doc.id, { id: doc.id, ...doc.data() } as TruckData);
        });

        const dashboardData: MaintenanceDashboardData[] = maintenanceSnapshot.docs.map(doc => {
            const data = doc.data() as MaintenanceRecord;
            const truck = truckMap.get(data.truckId);

            return {
                ...data,
                id: doc.id,
                truckLicensePlate: truck?.licensePlate || "Unknown",
                truckBrand: truck?.brand || "",
                truckModel: truck?.model || "",
                truckOwnership: truck?.ownershipType || "own",
                truckStatus: truck?.truckStatus || "active",
            };
        });

        return dashboardData;
    } catch (error) {
        console.error("❌ Error fetching maintenance overview:", error);
        return [];
    }
};

export const saveMaintenanceRecord = async (
    data: z.infer<typeof maintenanceSchema>,
    userId: string
) => {
    try {
        const sanitizedData = removeUndefinedFields(data);
        const maintenanceRef = collection(db, COLLECTIONS.MAINTENANCE);

        const docRef = await addDoc(maintenanceRef, {
            ...sanitizedData,
            createdBy: userId,
            createdAt: serverTimestamp(),
            updatedAt: serverTimestamp(),
        });

        if (data.status === 'in_progress') {
            const truckRef = doc(db, COLLECTIONS.TRUCKS, data.truckId);
            await updateDoc(truckRef, {
                truckStatus: 'maintenance',
                updatedBy: userId,
                updatedAt: serverTimestamp(),
            });
        }

        if (data.status === 'completed') {
            const truckRef = doc(db, COLLECTIONS.TRUCKS, data.truckId);
            const truckUpdate: any = {
                truckStatus: 'active',
                updatedBy: userId,
                updatedAt: serverTimestamp(),
            };

            if (data.endDate) truckUpdate.lastServiceDate = data.endDate;
            if (data.currentMileage) truckUpdate.currentMileage = data.currentMileage;

            if (data.type === 'PM' && data.nextServiceMileage) {
                truckUpdate.nextServiceMileage = data.nextServiceMileage;
            }

            await updateDoc(truckRef, truckUpdate);
        }

        return { success: true, id: docRef.id };
    } catch (error) {
        console.error("Error saving maintenance record:", error);
        throw error;
    }
};

export const updateMaintenanceRecord = async (
    id: string,
    data: Partial<z.infer<typeof maintenanceSchema>>,
    userId: string
) => {
    try {
        const sanitizedData = removeUndefinedFields(data);
        const recordRef = doc(db, COLLECTIONS.MAINTENANCE, id);

        await updateDoc(recordRef, {
            ...sanitizedData,
            updatedBy: userId,
            updatedAt: serverTimestamp(),
        });

        if (data.status === 'completed' && data.truckId) {
            const truckRef = doc(db, COLLECTIONS.TRUCKS, data.truckId);
            const truckUpdate: any = {
                truckStatus: 'active',
                updatedBy: userId,
                updatedAt: serverTimestamp(),
            };

            if (data.endDate) truckUpdate.lastServiceDate = data.endDate;
            if (data.currentMileage) truckUpdate.currentMileage = data.currentMileage;
            if (data.type === 'PM' && data.nextServiceMileage) {
                truckUpdate.nextServiceMileage = data.nextServiceMileage;
            }

            await updateDoc(truckRef, truckUpdate);
        }

        return { success: true };
    } catch (error) {
        console.error("Error updating maintenance record:", error);
        throw error;
    }
};

export const getMaintenanceHistory = async (truckId: string): Promise<MaintenanceData[]> => {
    try {
        const maintenanceRef = collection(db, COLLECTIONS.MAINTENANCE);
        const q = query(
            maintenanceRef,
            where("truckId", "==", truckId),
            orderBy("createdAt", "desc")
        );

        const snapshot = await getDocs(q);
        return snapshot.docs.map(doc => ({
            id: doc.id,
            ...doc.data()
        } as MaintenanceData));
    } catch (error) {
        console.error("Error fetching maintenance history:", error);
        throw error;
    }
};

export const getTruckChoices = async (): Promise<TruckData[]> => {
    try {
        const snapshot = await getDocs(collection(db, COLLECTIONS.TRUCKS));
        return snapshot.docs.map(doc => ({ id: doc.id, ...doc.data() } as TruckData));
    } catch (error) {
        console.error("Error fetching trucks choices:", error);
        return [];
    }
};
