"use client";

import { db, storage } from "@/firebase/client";
import { collection, doc, getDocs, getDoc, setDoc, updateDoc, deleteDoc, query, orderBy, Timestamp } from "firebase/firestore";
import { COLLECTIONS } from "@/lib/collections";
import { SubcontractorValidatedData } from "@/validate/subcontractorSchema";
import { ref, uploadBytes, getDownloadURL } from "firebase/storage";

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
    fleetSize?: number; // Added
    serviceArea?: string; // Added
    rating?: number; // Added
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

export async function getSubcontractors(): Promise<SubcontractorData[]> {
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
                fleetSize: data.fleetSize || 0, // Default to 0
                serviceArea: data.serviceArea || "Unknown", // Default
                rating: data.rating || 0, // Default
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

export async function createSubcontractor(data: SubcontractorValidatedData): Promise<string> {
    try {
        const subRef = doc(collection(db, COLLECTIONS.SUBCONTRACTORS));
        await setDoc(subRef, {
            ...data,
            createdAt: Timestamp.now(),
            updatedAt: Timestamp.now(),
        });
        return subRef.id;
    } catch (error) {
        console.error("Error creating subcontractor:", error);
        throw error;
    }
}

export async function updateSubcontractor(id: string, data: SubcontractorValidatedData): Promise<void> {
    try {
        const subRef = doc(db, COLLECTIONS.SUBCONTRACTORS, id);
        await updateDoc(subRef, {
            ...data,
            updatedAt: Timestamp.now(),
        });
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
