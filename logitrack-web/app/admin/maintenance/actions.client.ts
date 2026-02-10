"use client";

import { db } from "@/firebase/client";
import { collection, getDocs, query, orderBy } from "firebase/firestore";
import { COLLECTIONS } from "@/lib/collections";
import { TruckData } from "../trucks/actions.client";
import { z } from "zod";
import { maintenanceSchema } from "@/validate/maintenanceSchema";

export type MaintenanceRecord = z.infer<typeof maintenanceSchema> & { id: string };

export interface MaintenanceDashboardData extends MaintenanceRecord {
    truckLicensePlate: string;
    truckBrand: string;
    truckModel: string;
    truckOwnership: string;
}

export const getMaintenanceOverview = async (): Promise<MaintenanceDashboardData[]> => {
    try {
        // Parallel fetch for efficiency
        const [trucksSnapshot, maintenanceSnapshot] = await Promise.all([
            getDocs(collection(db, COLLECTIONS.TRUCKS)),
            getDocs(query(collection(db, COLLECTIONS.MAINTENANCE), orderBy("createdAt", "desc")))
        ]);

        // Create a lookup map for trucks
        const truckMap = new Map<string, TruckData>();
        trucksSnapshot.forEach(doc => {
            truckMap.set(doc.id, { id: doc.id, ...doc.data() } as TruckData);
        });

        // Map maintenance records with truck details
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
            };
        });

        return dashboardData;
    } catch (error) {
        console.error("❌ Error fetching maintenance overview:", error);
        return [];
    }
};
