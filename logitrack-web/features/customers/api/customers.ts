"use client";

import { db, storage } from "@/firebase/client";
import { ref, uploadBytes, getDownloadURL } from "firebase/storage";
import { collection, doc, getDocs, getDoc, setDoc, updateDoc, query, orderBy, Timestamp } from "firebase/firestore";
import { COLLECTIONS } from "@/lib/collections";
import type { Customer } from "@/validate/customerSchema";

export const uploadCustomerLogo = async (file: File, path: string): Promise<string> => {
    try {
        const storageRef = ref(storage, path);
        const snapshot = await uploadBytes(storageRef, file);
        return await getDownloadURL(snapshot.ref);
    } catch (error) {
        console.error("Error uploading logo:", error);
        throw error;
    }
};

export interface CustomerData extends Customer {
    id: string;
}

const toDate = (v: unknown): Date | undefined => {
    if (!v) return undefined;
    if (typeof v === "object" && v !== null && "toDate" in v) return (v as { toDate(): Date }).toDate();
    if (v instanceof Date) return v;
    if (typeof v === "string") return new Date(v);
    return undefined;
};

export async function getCustomers(): Promise<CustomerData[]> {
    const ref = collection(db, COLLECTIONS.CUSTOMERS);
    const q = query(ref, orderBy("code", "asc"));
    const snap = await getDocs(q);
    return snap.docs.map((d) => ({
        id: d.id,
        ...d.data(),
        createdAt: toDate(d.data().createdAt),
        updatedAt: toDate(d.data().updatedAt),
    })) as CustomerData[];
}

/** โหลด customers ทั้งหมดโดยไม่ orderBy — ใช้ตอน import PDP (หลีกเลี่ยงข้อกำหนด index) */
export async function getAllCustomersForCodeLookup(): Promise<CustomerData[]> {
    const ref = collection(db, COLLECTIONS.CUSTOMERS);
    const snap = await getDocs(ref);
    return snap.docs.map((d) => ({
        id: d.id,
        ...d.data(),
        createdAt: toDate(d.data().createdAt),
        updatedAt: toDate(d.data().updatedAt),
    })) as CustomerData[];
}

export async function getCustomerById(id: string): Promise<CustomerData | null> {
    const ref = doc(db, COLLECTIONS.CUSTOMERS, id);
    const snap = await getDoc(ref);
    if (!snap.exists()) return null;
    const data = snap.data();
    return {
        id: snap.id,
        ...data,
        createdAt: toDate(data.createdAt),
        updatedAt: toDate(data.updatedAt),
    } as CustomerData;
}

export async function createCustomer(data: Omit<Customer, "id">, logoFile?: File): Promise<string> {
    const ref = doc(collection(db, COLLECTIONS.CUSTOMERS));
    let logoUrl = undefined;
    if (logoFile) {
        const timestamp = Date.now();
        logoUrl = await uploadCustomerLogo(logoFile, `customers/logos/${timestamp}_${logoFile.name}`);
    }
    const payload = {
        ...data,
        logoUrl: logoUrl || "",
        createdAt: Timestamp.now(),
        updatedAt: Timestamp.now(),
    };
    await setDoc(ref, payload);
    return ref.id;
}

export async function updateCustomer(id: string, data: Partial<Customer>, logoFile?: File): Promise<void> {
    const ref = doc(db, COLLECTIONS.CUSTOMERS, id);
    let logoUrl = undefined;
    if (logoFile) {
        const timestamp = Date.now();
        logoUrl = await uploadCustomerLogo(logoFile, `customers/logos/${timestamp}_${logoFile.name}`);
    }
    const updates = {
        ...data,
        updatedAt: Timestamp.now(),
    } as any;
    if (logoUrl) updates.logoUrl = logoUrl;
    await updateDoc(ref, updates);
}
