/**
 * Firestore source of the companies module while the `masterdata` flag is off (developer-spec.md
 * §10.6 "Domain adapter"). Loaded only through `import()` from `./companies.ts`. Deleted with the
 * Firebase SDK (TW7).
 */
import { collection, doc, getDocs, addDoc, updateDoc, query, where, orderBy, serverTimestamp, limit } from "firebase/firestore";
import { ref, uploadBytes, getDownloadURL } from "firebase/storage";
import { db, storage } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { stripUndefined } from "@/lib/firestoreWrite";
import type { CompanyAsset, CompanyDTO, CompanySave } from "./companies";

const text = (v: unknown): string | null => {
    if (v === undefined || v === null) return null;
    const s = String(v).trim();
    return s === "" ? null : s;
};

const iso = (v: unknown): string | null => {
    if (!v) return null;
    if (typeof v === "object" && v !== null && "toDate" in v) return (v as { toDate(): Date }).toDate().toISOString();
    if (v instanceof Date) return v.toISOString();
    if (typeof v === "string") return v;
    return null;
};

const int = (v: unknown): number | null => (typeof v === "number" && Number.isFinite(v) ? v : null);

/** One stored company as the Go DTO. */
export function companyDTOFromFirestore(id: string, d: Record<string, unknown>): CompanyDTO {
    return {
        id,
        tenantId: null,
        nameTh: String(d.nameTh ?? "").trim(),
        nameEn: text(d.nameEn),
        shortName: text(d.shortName),
        taxId: String(d.taxId ?? "").trim(),
        branchType: d.branchType === "branch" ? "branch" : "headquarters",
        branchNumber: text(d.branchNumber),
        address: String(d.address ?? "").trim(),
        phone: text(d.phone),
        email: text(d.email),
        logoKey: null,
        logoUrl: text(d.logoUrl),
        stampKey: null,
        stampUrl: text(d.stampUrl),
        signatureKey: null,
        signatureUrl: text(d.signatureUrl),
        signatoryName: text(d.signatoryName),
        bankName: text(d.bankName),
        accountNumber: text(d.accountNumber),
        accountName: text(d.accountName),
        withholdingTaxRate: typeof d.withholdingTaxRate === "number" ? d.withholdingTaxRate : 3,
        companyType: d.companyType === "subcontractor" ? "subcontractor" : "owner",
        isActive: d.isActive !== false,
        maxTrucks: int(d.maxTrucks),
        maxDrivers: int(d.maxDrivers),
        createdAt: iso(d.createdAt),
        updatedAt: iso(d.updatedAt),
    };
}

export async function fetchOwnerCompanyFirestore(): Promise<CompanyDTO | null> {
    const snap = await getDocs(query(collection(db, COLLECTIONS.COMPANIES), where("companyType", "==", "owner"), limit(1)));
    if (snap.empty) return null;
    const d = snap.docs[0];
    return companyDTOFromFirestore(d.id, d.data() as Record<string, unknown>);
}

export async function fetchCompaniesFirestore(): Promise<CompanyDTO[]> {
    const snap = await getDocs(query(collection(db, COLLECTIONS.COMPANIES), orderBy("companyType", "asc"), orderBy("nameTh", "asc")));
    return snap.docs.map((d) => companyDTOFromFirestore(d.id, d.data() as Record<string, unknown>));
}

async function uploadAsset(companyId: string, type: CompanyAsset, file: File): Promise<string> {
    const ext = file.name.split(".").pop() ?? "png";
    const storageRef = ref(storage, `companies/${companyId}/${type}.${ext}`);
    await uploadBytes(storageRef, file);
    return getDownloadURL(storageRef);
}

export async function saveCompanyFirestore({ id, values, files = {} }: CompanySave): Promise<string> {
    let companyId = id;
    const data = stripUndefined(values as Record<string, unknown>);
    if (!companyId) {
        const docRef = await addDoc(collection(db, COLLECTIONS.COMPANIES), { ...data, createdAt: serverTimestamp(), updatedAt: serverTimestamp() });
        companyId = docRef.id;
    }
    const urls: Record<string, string> = {};
    for (const [asset, file] of Object.entries(files) as [CompanyAsset, File | undefined][]) {
        if (file) urls[`${asset}Url`] = await uploadAsset(companyId, asset, file);
    }
    if (id || Object.keys(urls).length > 0) {
        await updateDoc(doc(db, COLLECTIONS.COMPANIES, companyId), { ...(id ? data : {}), ...urls, updatedAt: serverTimestamp() });
    }
    return companyId;
}
