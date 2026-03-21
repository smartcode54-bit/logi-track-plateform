import { db } from "@/firebase/client";
import { collection, doc, addDoc, updateDoc, serverTimestamp, query, where, getDocs, orderBy } from "firebase/firestore";
import { COLLECTIONS } from "@/lib/collections";
import { maintenanceSchema, MaintenanceData } from "@/validate/maintenanceSchema";
import * as z from "zod";

const removeUndefinedFields = <T extends Record<string, any>>(obj: T): Partial<T> => {
    const result: Partial<T> = {};
    for (const key in obj) {
        if (Object.prototype.hasOwnProperty.call(obj, key) && obj[key] !== undefined && obj[key] !== "") {
            result[key] = obj[key];
        }
    }
    return result;
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
