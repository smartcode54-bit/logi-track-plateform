"use client";

import { db } from "@/firebase/client";
import { collection, getDocs, query, orderBy } from "firebase/firestore";
import { COLLECTIONS } from "@/lib/collections";
import { TruckData } from "../trucks/actions.client";

export interface RenewalTruckData extends TruckData {
    taxStatus: "ok" | "expiring_soon" | "overdue" | "in_progress";
    insuranceStatus: "ok" | "expiring_soon" | "overdue" | "in_progress";
}

/**
 * Helper to determine status based on days remaining
 */
function getStatus(expiryDate?: string | null): "ok" | "expiring_soon" | "overdue" {
    if (!expiryDate) return "overdue"; // Treat missing date as overdue/critical

    const today = new Date();
    const expiry = new Date(expiryDate);

    // Reset time parts for accurate day diff
    today.setHours(0, 0, 0, 0);
    expiry.setHours(0, 0, 0, 0);

    const diffTime = expiry.getTime() - today.getTime();
    const diffDays = Math.ceil(diffTime / (1000 * 60 * 60 * 24));

    if (diffDays < 0) return "overdue";
    if (diffDays <= 30) return "expiring_soon";
    return "ok";
}

export const getRenewalOverview = async (): Promise<RenewalTruckData[]> => {
    try {
        const trucksRef = collection(db, COLLECTIONS.TRUCKS);
        // We fetch all trucks and filter/compute status client-side to avoid complex compound indexes
        // given the dataset size is likely manageable for fleet management.
        const q = query(trucksRef, orderBy("updatedAt", "desc"));
        const snapshot = await getDocs(q);

        const trucks = snapshot.docs.map(doc => {
            const data = doc.data() as TruckData;

            let taxStatus: RenewalTruckData["taxStatus"] = getStatus(data.taxExpiryDate);
            if (data.taxRenewalStatus === 'in_progress') {
                taxStatus = 'in_progress';
            }

            let insuranceStatus: RenewalTruckData["insuranceStatus"] = getStatus(data.insuranceExpiryDate);
            if (data.insuranceRenewalStatus === 'in_progress') {
                insuranceStatus = 'in_progress';
            }

            return {
                ...data,
                id: doc.id,
                taxStatus,
                insuranceStatus
            } as RenewalTruckData;
        });

        return trucks;
    } catch (error) {
        console.error("❌ Error fetching renewal overview:", error);
        return [];
    }
};
