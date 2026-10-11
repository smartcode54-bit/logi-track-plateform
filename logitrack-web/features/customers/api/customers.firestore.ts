/**
 * Firestore source of the customers module while the `masterdata` flag is off (developer-spec.md
 * §10.6 "Domain adapter"). Loaded only through `import()` from `./customers.ts`, so a page on the Go
 * source never loads it or the Firebase SDK. Reads map the stored document to the Go DTO; writes take
 * the Go body and store the legacy fields. Deleted with the Firebase SDK (TW7).
 */
import { ref, uploadBytes, getDownloadURL } from "firebase/storage";
import { collection, doc, getDocs, getDoc, setDoc, updateDoc, query, orderBy, Timestamp } from "firebase/firestore";
import { db, storage } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import type { CustomerData, CustomerDTO, CustomerInput, CustomerMinimalDTO, CustomerSave, DriverIdTypeDTO } from "./customers";

const toDate = (v: unknown): Date | undefined => {
    if (!v) return undefined;
    if (typeof v === "object" && v !== null && "toDate" in v) return (v as { toDate(): Date }).toDate();
    if (v instanceof Date) return v;
    if (typeof v === "string") return new Date(v);
    return undefined;
};

const text = (v: unknown): string | null => {
    if (v === undefined || v === null) return null;
    const s = String(v).trim();
    return s === "" ? null : s;
};

const iso = (v: unknown): string | null => toDate(v)?.toISOString() ?? null;

/** The Thai branch label of the legacy form -> the API vocabulary. */
function branchType(v: unknown): CustomerDTO["branchType"] {
    if (v === "สำนักงานใหญ่" || v === "hq") return "hq";
    if (v === "สาขา" || v === "branch") return "branch";
    return null;
}

/** One stored customer as the `GET /v1/customers/{id}` DTO. */
export function customerDTOFromFirestore(id: string, data: Record<string, unknown>): CustomerDTO {
    const types = Array.isArray(data.driverIdTypes) ? (data.driverIdTypes as Array<Record<string, unknown>>) : [];
    const terms = Number(data.paymentTermsDays);
    return {
        id,
        billingPartyId: null,
        code: String(data.code ?? "").trim(),
        name: String(data.name ?? "").trim(),
        description: text(data.description),
        logoKey: null,
        logoUrl: text(data.logoUrl),
        address: text(data.address),
        taxId: text(data.taxId),
        branchType: branchType(data.branchType),
        branchNumber: text(data.branchNumber),
        contactName: text(data.contactName),
        contactPhone: text(data.contactPhone),
        billingEmail: text(data.billingEmail),
        paymentTermsDays: data.paymentTermsDays === undefined || data.paymentTermsDays === null || !Number.isFinite(terms) ? null : terms,
        invoiceNote: text(data.invoiceNote),
        lineGroupId: text(data.lineGroupId),
        billingDateBasis: data.billingDateBasis === "plan" ? "plan" : "delivered",
        driverIdTypes: types
            .map((d): DriverIdTypeDTO => ({ key: String(d.key ?? ""), label: String(d.label ?? "") }))
            .filter((d) => d.key !== ""),
        createdAt: iso(data.createdAt),
        updatedAt: iso(data.updatedAt),
    };
}

/** The legacy `CustomerData` list of `getCustomers()` (callers outside the master-data pages). */
export async function fetchCustomersFromFirestore(): Promise<CustomerData[]> {
    const snap = await getDocs(query(collection(db, COLLECTIONS.CUSTOMERS), orderBy("code", "asc")));
    return snap.docs.map((d) => ({
        id: d.id,
        ...d.data(),
        createdAt: toDate(d.data().createdAt),
        updatedAt: toDate(d.data().updatedAt),
    })) as CustomerData[];
}

async function allCustomerDTOs(): Promise<CustomerDTO[]> {
    const snap = await getDocs(query(collection(db, COLLECTIONS.CUSTOMERS), orderBy("code", "asc")));
    return snap.docs.map((d) => customerDTOFromFirestore(d.id, d.data() as Record<string, unknown>));
}

/** The customers page: every customer, filtered by code or name (the collection is small). */
export async function listCustomerDTOs(q: string): Promise<CustomerDTO[]> {
    const all = await allCustomerDTOs();
    const term = q.trim().toLowerCase();
    if (!term) return all;
    return all.filter((c) => c.code.toLowerCase().includes(term) || c.name.toLowerCase().includes(term));
}

export async function listCustomerOptions(): Promise<CustomerMinimalDTO[]> {
    // No orderBy: legacy documents without `code` would be left out of an ordered query.
    const snap = await getDocs(collection(db, COLLECTIONS.CUSTOMERS));
    return snap.docs
        .map((d) => customerDTOFromFirestore(d.id, d.data() as Record<string, unknown>))
        .map(({ id, billingPartyId, code, name }) => ({ id, billingPartyId, code, name }))
        .sort((a, b) => a.code.localeCompare(b.code));
}

export async function getCustomerDTO(id: string): Promise<CustomerDTO | null> {
    const snap = await getDoc(doc(db, COLLECTIONS.CUSTOMERS, id));
    if (!snap.exists()) return null;
    return customerDTOFromFirestore(snap.id, snap.data() as Record<string, unknown>);
}

async function uploadLogo(file: File): Promise<string> {
    const storageRef = ref(storage, `customers/logos/${Date.now()}_${file.name}`);
    const snapshot = await uploadBytes(storageRef, file);
    return getDownloadURL(snapshot.ref);
}

const BRANCH_LABEL = { hq: "สำนักงานใหญ่", branch: "สาขา" } as const;

/** The stored fields of an API body (legacy names; `undefined` fields are left out). */
function legacyFields(input: CustomerInput): Record<string, unknown> {
    const out: Record<string, unknown> = {
        code: input.code,
        name: input.name,
        description: input.description ?? "",
        driverIdTypes: input.driverIdTypes,
        address: input.address ?? "",
        taxId: input.taxId ?? "",
        branchNumber: input.branchNumber ?? "",
        contactName: input.contactName ?? "",
        contactPhone: input.contactPhone ?? "",
        billingEmail: input.billingEmail ?? "",
        invoiceNote: input.invoiceNote ?? "",
        billingDateBasis: input.billingDateBasis,
        lineGroupId: input.lineGroupId ?? "",
    };
    if (input.branchType) out.branchType = BRANCH_LABEL[input.branchType];
    if (input.paymentTermsDays !== null) out.paymentTermsDays = input.paymentTermsDays;
    return out;
}

export async function createCustomerFirestore(save: CustomerSave): Promise<string> {
    const docRef = doc(collection(db, COLLECTIONS.CUSTOMERS));
    const logoUrl = save.logo ? await uploadLogo(save.logo) : "";
    await setDoc(docRef, { ...legacyFields(save.input), logoUrl, createdAt: Timestamp.now(), updatedAt: Timestamp.now() });
    return docRef.id;
}

export async function updateCustomerFirestore(id: string, save: CustomerSave): Promise<void> {
    const updates: Record<string, unknown> = { ...legacyFields(save.input), updatedAt: Timestamp.now() };
    if (save.logo) updates.logoUrl = await uploadLogo(save.logo);
    else if (save.removeLogo) updates.logoUrl = "";
    await updateDoc(doc(db, COLLECTIONS.CUSTOMERS, id), updates);
}
