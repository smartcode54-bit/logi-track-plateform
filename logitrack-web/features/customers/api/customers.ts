"use client";

import { db, storage } from "@/firebase/client";
import { ref, uploadBytes, getDownloadURL } from "firebase/storage";
import { collection, doc, getDocs, getDoc, setDoc, updateDoc, query, orderBy, Timestamp } from "firebase/firestore";
import { queryOptions } from "@tanstack/react-query";
import { COLLECTIONS } from "@/lib/collections";
import { getQueryClient } from "@/lib/queryClient";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
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

/** Every customer by code, read from Firestore: the P0 source of `['customers']` (developer-spec.md §10.6). */
export async function fetchCustomersFromFirestore(): Promise<CustomerData[]> {
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

/**
 * `['customers']` (developer-spec.md §10.7, Appendix E §E.4 row "customers"): every customer, 10 min
 * stale, shared by every picker and page of the tab. The Go source (`GET /v1/customers`) arrives
 * with the master-data pages (T25, P1); the key stays.
 */
export const customersQueryOptions = queryOptions({
    queryKey: queryKeys.customers.all(),
    queryFn: fetchCustomersFromFirestore,
    ...QUERY_POLICY.masterData,
    refetchOnWindowFocus: true,
});

/**
 * Every customer by code, from the tab's `['customers']` cache: read once per stale time however
 * many pages and dialogs ask (the 15 call sites read the collection on every mount before TW4). The
 * result is shared: never mutate it in place.
 */
export function getCustomers(): Promise<CustomerData[]> {
    return getQueryClient().fetchQuery(customersQueryOptions);
}

/** After a customer write: every `['customers', ...]` entry refetches where observed, the rest on next use. */
export function invalidateCustomers(): Promise<void> {
    return getQueryClient().invalidateQueries({ queryKey: queryKeys.customers.all() });
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
    await invalidateCustomers();
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
    await invalidateCustomers();
}
