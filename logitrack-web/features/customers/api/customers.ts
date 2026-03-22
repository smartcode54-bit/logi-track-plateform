"use client";

import { db } from "@/firebase/client";
import { collection, doc, getDocs, getDoc, setDoc, updateDoc, query, orderBy, Timestamp } from "firebase/firestore";
import { COLLECTIONS } from "@/lib/collections";
import type { Customer } from "@/validate/customerSchema";

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

export async function createCustomer(data: Omit<Customer, "id">): Promise<string> {
    const ref = doc(collection(db, COLLECTIONS.CUSTOMERS));
    const payload = {
        ...data,
        createdAt: Timestamp.now(),
        updatedAt: Timestamp.now(),
    };
    await setDoc(ref, payload);
    return ref.id;
}

export async function updateCustomer(id: string, data: Partial<Customer>): Promise<void> {
    const ref = doc(db, COLLECTIONS.CUSTOMERS, id);
    await updateDoc(ref, {
        ...data,
        updatedAt: Timestamp.now(),
    });
}
