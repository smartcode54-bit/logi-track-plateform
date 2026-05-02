import { db, functions } from "@/firebase/client";
import { collection, doc, addDoc, updateDoc, serverTimestamp, query, where, getDocs, orderBy, getDoc, deleteField } from "firebase/firestore";
import { httpsCallable } from "firebase/functions";
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

function primaryDriverIdFromTruckDoc(data: Record<string, unknown> | undefined): string | undefined {
    if (!data) return undefined;
    const list = data.currentAssignments as { driverId?: string }[] | undefined;
    if (Array.isArray(list) && list.length > 0 && list[0]?.driverId) {
        return list[0].driverId;
    }
    const legacy = data.currentAssignment as { driverId?: string } | undefined;
    return legacy?.driverId;
}

async function tryNotifyMaintenanceInProgress(params: {
    truckId: string;
    maintenanceId: string;
    startDate?: string;
    appointmentTime?: string;
    provider?: string;
    licensePlate?: string;
}) {
    try {
        const truckSnap = await getDoc(doc(db, COLLECTIONS.TRUCKS, params.truckId));
        const raw = truckSnap.data() as Record<string, unknown> | undefined;
        const driverId = primaryDriverIdFromTruckDoc(raw);
        if (!driverId) return;
        const plate = params.licensePlate || (raw?.licensePlate as string) || "";
        const fn = httpsCallable(functions, "notifyMaintenanceReminder");
        const datePart = params.startDate ? ` วันที่ ${params.startDate}` : "";
        const timePart = params.appointmentTime ? ` เวลา ${params.appointmentTime}` : "";
        const placePart = params.provider ? ` ที่ ${params.provider}` : "";
        await fn({
            driverId,
            maintenanceId: params.maintenanceId,
            title: "นัดเช็คระยะ",
            body: `รถ ${plate}${datePart}${timePart}${placePart} — กรุณาเข้าอู่ตามนัด`.trim(),
        });
    } catch (e) {
        console.warn("[maintenance] notifyMaintenanceReminder skipped:", e);
    }
}

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
            const truckUpdate: Record<string, unknown> = {
                truckStatus: 'active',
                updatedBy: userId,
                updatedAt: serverTimestamp(),
                activeMaintenanceId: deleteField(),
                lastAlertMileage: deleteField(),
            };

            if (data.endDate) truckUpdate.lastServiceDate = data.endDate;
            if (data.currentMileage) truckUpdate.currentMileage = data.currentMileage;

            if (data.type === 'PM' && data.nextServiceMileage) {
                truckUpdate.nextServiceMileage = data.nextServiceMileage;
            }

            await updateDoc(truckRef, truckUpdate as any);
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
        const prevSnap = await getDoc(recordRef);
        const prev = prevSnap.data() as Record<string, unknown> | undefined;
        const prevStatus = prev?.status as string | undefined;

        await updateDoc(recordRef, {
            ...sanitizedData,
            updatedBy: userId,
            updatedAt: serverTimestamp(),
        });

        const truckId = (data.truckId ?? prev?.truckId) as string | undefined;
        const nextStatus = data.status ?? prevStatus;

        if (truckId && prevStatus === "PM Booking" && nextStatus === "Scheduled") {
            await tryNotifyMaintenanceInProgress({
                truckId,
                maintenanceId: id,
                startDate: data.startDate ?? (prev?.startDate as string | undefined),
                appointmentTime: data.appointmentTime ?? (prev?.appointmentTime as string | undefined),
                provider: data.provider ?? (prev?.provider as string | undefined),
            });
        }

        if (truckId && prevStatus === "PM Booking" && nextStatus === "in_progress") {
            const truckRef = doc(db, COLLECTIONS.TRUCKS, truckId);
            await updateDoc(truckRef, {
                truckStatus: "maintenance",
                updatedBy: userId,
                updatedAt: serverTimestamp(),
            });
            await tryNotifyMaintenanceInProgress({
                truckId,
                maintenanceId: id,
                startDate: data.startDate ?? (prev?.startDate as string | undefined),
                appointmentTime: data.appointmentTime ?? (prev?.appointmentTime as string | undefined),
                provider: data.provider ?? (prev?.provider as string | undefined),
            });
        }

        if (truckId && prevStatus === "Scheduled" && nextStatus === "in_progress") {
            const truckRef = doc(db, COLLECTIONS.TRUCKS, truckId);
            await updateDoc(truckRef, {
                truckStatus: "maintenance",
                updatedBy: userId,
                updatedAt: serverTimestamp(),
            });
        }

        if (data.status === "cancelled" && truckId) {
            const truckRef = doc(db, COLLECTIONS.TRUCKS, truckId);
            await updateDoc(truckRef, {
                truckStatus: "active",
                activeMaintenanceId: deleteField(),
                lastAlertMileage: deleteField(),
                updatedBy: userId,
                updatedAt: serverTimestamp(),
            });
        }

        if (data.status === 'completed' && truckId) {
            const truckRef = doc(db, COLLECTIONS.TRUCKS, truckId);
            const endDate = data.endDate ?? (prev?.endDate as string | undefined);
            const curMi = data.currentMileage ?? (prev?.currentMileage as number | undefined);
            const nsm = data.nextServiceMileage ?? (prev?.nextServiceMileage as number | undefined);
            const jobType = data.type ?? (prev?.type as "PM" | "CM" | undefined);
            const truckUpdate: Record<string, unknown> = {
                truckStatus: 'active',
                updatedBy: userId,
                updatedAt: serverTimestamp(),
                activeMaintenanceId: deleteField(),
                lastAlertMileage: deleteField(),
            };

            if (endDate) truckUpdate.lastServiceDate = endDate;
            if (curMi !== undefined && curMi !== null) truckUpdate.currentMileage = curMi;
            if (jobType === 'PM' && nsm !== undefined && nsm !== null) {
                truckUpdate.nextServiceMileage = nsm;
            }

            await updateDoc(truckRef, truckUpdate as any);
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
