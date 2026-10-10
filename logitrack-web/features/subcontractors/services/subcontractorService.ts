"use client";

import { db, storage } from "@/firebase/client";
import { collection, doc, getDocs, getDoc, setDoc, updateDoc, query, orderBy, Timestamp } from "firebase/firestore";
import { queryOptions } from "@tanstack/react-query";
import { COLLECTIONS } from "@/lib/collections";
import { getQueryClient } from "@/lib/queryClient";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
import { SubcontractorValidatedData } from "@/validate/subcontractorSchema";
import { ref, uploadBytes, getDownloadURL } from "firebase/storage";

export interface SubcontractorData {
    id: string;
    name: string;
    type: "individual" | "company";
    idCardNumber?: string;
    taxId?: string;
    contactPerson: string;
    phone: string;
    email?: string;
    address?: string;
    status: "active" | "pending" | "suspended";
    documents?: string[];
    lineGroupId?: string;
    /** Billing date basis when this partner is billed directly (ADR 0027/0028). */
    billingDateBasis?: "delivered" | "plan";
    fleetSize?: number;
    serviceArea?: string; 
    rating?: number; 
    createdAt: Date | null;
    updatedAt: Date | null;
}

const formatTimestamp = (timestamp: any): Date | null => {
    if (!timestamp) return null;
    if (timestamp.toDate) return timestamp.toDate();
    if (timestamp.toMillis) return new Date(timestamp.toMillis());
    if (timestamp.seconds) return new Date(timestamp.seconds * 1000);
    return timestamp;
};

export async function uploadSubcontractorFile(file: File, path: string): Promise<string> {
    try {
        const storageRef = ref(storage, path);
        const snapshot = await uploadBytes(storageRef, file);
        return await getDownloadURL(snapshot.ref);
    } catch (error) {
        console.error("Error uploading file:", error);
        throw error;
    }
}

/** Every subcontractor, newest first, read from Firestore: the P0 source of `['subcontractors']`. */
export async function fetchSubcontractorsFromFirestore(): Promise<SubcontractorData[]> {
    try {
        const subRef = collection(db, COLLECTIONS.SUBCONTRACTORS);
        const q = query(subRef, orderBy("createdAt", "desc"));
        const snapshot = await getDocs(q);

        const subcontractors: SubcontractorData[] = [];
        snapshot.forEach((doc) => {
            const data = doc.data();
            subcontractors.push({
                id: doc.id,
                name: data.name || "",
                type: data.type || "individual",
                idCardNumber: data.idCardNumber || "",
                taxId: data.taxId || "",
                contactPerson: data.contactPerson || "",
                phone: data.phone || "",
                email: data.email || "",
                address: data.address || "",
                status: data.status || "active",
                documents: data.documents || [],
                lineGroupId: data.lineGroupId || "",
                fleetSize: data.fleetSize || 0,
                serviceArea: data.serviceArea || "Unknown",
                rating: data.rating || 0,
                createdAt: formatTimestamp(data.createdAt),
                updatedAt: formatTimestamp(data.updatedAt),
            });
        });
        return subcontractors;
    } catch (error) {
        console.error("Error fetching subcontractors:", error);
        throw error;
    }
}

/**
 * `['subcontractors']` (developer-spec.md §10.7, Appendix E §E.4 row "subcontractors"): 10 min stale,
 * shared by the truck and driver forms and the list page. Go source `GET /v1/subcontractors` in P1.
 */
export const subcontractorsQueryOptions = queryOptions({
    queryKey: queryKeys.subcontractors.all(),
    queryFn: fetchSubcontractorsFromFirestore,
    ...QUERY_POLICY.masterData,
    refetchOnWindowFocus: true,
});

/** Every subcontractor from the tab's `['subcontractors']` cache. Shared: never mutate it in place. */
export function getSubcontractors(): Promise<SubcontractorData[]> {
    return getQueryClient().fetchQuery(subcontractorsQueryOptions);
}

/** After a subcontractor write. */
export function invalidateSubcontractors(): Promise<void> {
    return getQueryClient().invalidateQueries({ queryKey: queryKeys.subcontractors.all() });
}

export async function createSubcontractor(data: SubcontractorValidatedData): Promise<string> {
    try {
        const subRef = doc(collection(db, COLLECTIONS.SUBCONTRACTORS));
        await setDoc(subRef, {
            ...data,
            createdAt: Timestamp.now(),
            updatedAt: Timestamp.now(),
        });
        await invalidateSubcontractors();
        return subRef.id;
    } catch (error) {
        console.error("Error creating subcontractor:", error);
        throw error;
    }
}

export async function updateSubcontractor(id: string, data: Partial<SubcontractorValidatedData>): Promise<void> {
    try {
        const subRef = doc(db, COLLECTIONS.SUBCONTRACTORS, id);
        await updateDoc(subRef, {
            ...data,
            updatedAt: Timestamp.now(),
        });
        await invalidateSubcontractors();
    } catch (error) {
        console.error("Error updating subcontractor:", error);
        throw error;
    }
}

export async function getSubcontractorById(id: string): Promise<SubcontractorData | null> {
    try {
        const subRef = doc(db, COLLECTIONS.SUBCONTRACTORS, id);
        const docSnap = await getDoc(subRef);

        if (docSnap.exists()) {
            const data = docSnap.data();
            return {
                id: docSnap.id,
                name: data.name || "",
                type: data.type || "individual",
                idCardNumber: data.idCardNumber || "",
                taxId: data.taxId || "",
                contactPerson: data.contactPerson || "",
                phone: data.phone || "",
                email: data.email || "",
                address: data.address || "",
                status: data.status || "active",
                documents: data.documents || [],
                lineGroupId: data.lineGroupId || "",
                billingDateBasis: data.billingDateBasis === "plan" ? "plan" : "delivered",
                fleetSize: data.fleetSize || 0,
                serviceArea: data.serviceArea || "Unknown",
                rating: data.rating || 0,
                createdAt: formatTimestamp(data.createdAt),
                updatedAt: formatTimestamp(data.updatedAt),
            };
        } else {
            return null;
        }
    } catch (error) {
        console.error("Error fetching subcontractor:", error);
        throw error;
    }
}
