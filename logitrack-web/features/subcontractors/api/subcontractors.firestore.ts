/**
 * Firestore source of the subcontractors module while the `masterdata` flag is off (developer-spec.md
 * §10.6 "Domain adapter"). Loaded only through `import()` from `./subcontractors.ts`. Reads map the
 * `subcontractors` documents to the Go carrier-profile DTOs; writes store the legacy fields. Deleted
 * with the Firebase SDK (TW7).
 */
import { collection, doc, getDocs, getDoc, setDoc, updateDoc, query, orderBy, Timestamp } from "firebase/firestore";
import { ref, uploadBytes, getDownloadURL } from "firebase/storage";
import { db, storage } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { stripUndefined } from "@/lib/firestoreWrite";
import type { SubcontractorFormValues } from "@/validate/subcontractorSchema";
import type { SubcontractorDTO, SubcontractorOptionDTO, SubcontractorSummaryDTO } from "./subcontractors";

const iso = (v: unknown): string | null => {
    if (!v) return null;
    if (typeof v === "object" && v !== null && "toDate" in v) return (v as { toDate(): Date }).toDate().toISOString();
    if (typeof v === "object" && v !== null && "seconds" in v) return new Date((v as { seconds: number }).seconds * 1000).toISOString();
    if (v instanceof Date) return v.toISOString();
    if (typeof v === "string") return v;
    return null;
};

const text = (v: unknown): string | null => {
    if (v === undefined || v === null) return null;
    const s = String(v).trim();
    return s === "" ? null : s;
};

const strings = (v: unknown): string[] => (Array.isArray(v) ? v.map(String).filter(Boolean) : []);

/** One stored subcontractor as the carrier-profile DTO (its document id stands in for the tenant id). */
export function subcontractorDTOFromFirestore(id: string, data: Record<string, unknown>): SubcontractorDTO {
    const legal = data.type === "company" ? "company" : "individual";
    return {
        id,
        code: null,
        nameTh: String(data.name ?? data.companyName ?? "").trim(),
        nameEn: null,
        legalType: legal,
        status: (text(data.status) ?? "active") as SubcontractorDTO["status"],
        contactPerson: text(data.contactPerson),
        phone: text(data.phone),
        email: text(data.email),
        fleetSize: Number(data.fleetSize ?? 0) || 0,
        lineGroupId: text(data.lineGroupId),
        contractorTenantId: null,
        billingPartyId: null,
        billingDateBasis: data.billingDateBasis === "plan" ? "plan" : "delivered",
        createdAt: iso(data.createdAt),
        serviceArea: text(data.serviceArea),
        idCardNumber: text(data.idCardNumber),
        taxId: text(data.taxId),
        website: text(data.website),
        address: text(data.address),
        designation: text(data.designation),
        dispatchCenter: text(data.dispatchCenter),
        serviceRegions: strings(data.serviceRegions),
        vehicleTypes: strings(data.vehicleTypes),
        updatedAt: iso(data.updatedAt),
        documents: strings(data.documents).map((url, position) => ({ fileId: url, key: null, url, kind: "other", position })),
    };
}

async function allSubcontractors(): Promise<SubcontractorDTO[]> {
    const snap = await getDocs(query(collection(db, COLLECTIONS.SUBCONTRACTORS), orderBy("createdAt", "desc")));
    return snap.docs.map((d) => subcontractorDTOFromFirestore(d.id, d.data() as Record<string, unknown>));
}

export async function listSubcontractorsFirestore(): Promise<SubcontractorSummaryDTO[]> {
    return allSubcontractors();
}

export async function listSubcontractorOptionsFirestore(): Promise<SubcontractorOptionDTO[]> {
    // No orderBy: documents without createdAt would be left out of an ordered query.
    const snap = await getDocs(collection(db, COLLECTIONS.SUBCONTRACTORS));
    return snap.docs
        .map((d) => subcontractorDTOFromFirestore(d.id, d.data() as Record<string, unknown>))
        .map(({ id, code, nameTh, nameEn, status }) => ({ id, code, nameTh, nameEn, status }))
        .sort((a, b) => a.nameTh.localeCompare(b.nameTh, "th"));
}

export async function getSubcontractorFirestore(id: string): Promise<SubcontractorDTO | null> {
    const snap = await getDoc(doc(db, COLLECTIONS.SUBCONTRACTORS, id));
    if (!snap.exists()) return null;
    return subcontractorDTOFromFirestore(snap.id, snap.data() as Record<string, unknown>);
}

export async function uploadSubcontractorFileFirestore(file: File, folder: string): Promise<string> {
    const storageRef = ref(storage, `subcontractors/${folder}/${Date.now()}_${file.name}`);
    const snapshot = await uploadBytes(storageRef, file);
    return getDownloadURL(snapshot.ref);
}

export async function createSubcontractorFirestore(values: SubcontractorFormValues): Promise<string> {
    const subRef = doc(collection(db, COLLECTIONS.SUBCONTRACTORS));
    await setDoc(subRef, { ...stripUndefined(values as Record<string, unknown>), createdAt: Timestamp.now(), updatedAt: Timestamp.now() });
    return subRef.id;
}

export async function updateSubcontractorFirestore(id: string, values: SubcontractorFormValues): Promise<void> {
    await updateDoc(doc(db, COLLECTIONS.SUBCONTRACTORS, id), {
        ...stripUndefined(values as Record<string, unknown>),
        updatedAt: Timestamp.now(),
    });
}
