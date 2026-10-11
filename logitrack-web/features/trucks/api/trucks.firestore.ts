/**
 * Firestore source of the trucks module while the `masterdata` flag is off (developer-spec.md §10.6
 * "Domain adapter"). Loaded only through `import()` from `./trucks.ts`. Reads map the `trucks`
 * documents to the Go DTOs (statuses and payment methods to the API vocabulary, files to `files`);
 * writes keep the legacy fields and the legacy upload paths. Deleted with the Firebase SDK (TW7).
 */
import { collection, doc, getDoc, getDocs, query, orderBy, writeBatch } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import {
    formatTimestamp,
    logTransaction,
    saveNewTruckToFirestoreClient,
    updateTruckInFirestoreClient,
    uploadTruckFile,
} from "../services/truckService";
import { getRenewalOverview } from "@/app/app/renewals/actions.client";
import {
    apiDate,
    apiPaymentMethod,
    apiTruckStatus,
    type RenewalDTO,
    type RenewalSave,
    type TruckDetailDTO,
    type TruckDTO,
    type TruckFile,
    type TruckFileKind,
    type TruckImportResult,
    type TruckListFilter,
    type TruckSave,
} from "./trucks";

const text = (v: unknown): string | null => {
    if (v === undefined || v === null) return null;
    const s = String(v).trim();
    return s === "" ? null : s;
};

const num = (v: unknown): number | null => {
    if (v === undefined || v === null || v === "") return null;
    const n = typeof v === "number" ? v : Number(v);
    return Number.isFinite(n) ? n : null;
};

const iso = (v: unknown): string | null => {
    if (typeof v === "string") return v === "" ? null : v;
    const d = formatTimestamp(v) as unknown;
    return d instanceof Date && !Number.isNaN(d.getTime()) ? d.toISOString() : null;
};

const renewal = (v: unknown) => (v === "pending" || v === "in_progress" || v === "completed" ? v : null);

const file = (url: unknown): TruckFile[] => {
    const u = text(url);
    return u ? [{ key: u, url: u }] : [];
};

const fileList = (urls: unknown): TruckFile[] =>
    Array.isArray(urls) ? urls.map(String).filter(Boolean).map((u) => ({ key: u, url: u })) : [];

/** One stored truck as the Go detail DTO (its document id is the truck id; files are keyed by URL). */
export function truckDTOFromFirestore(id: string, d: Record<string, unknown>): TruckDetailDTO {
    const ownership = d.ownershipType === "subcontractor" ? "subcontractor" : "own";
    const status = apiTruckStatus(text(d.truckStatus));
    const history = Array.isArray(d.statusHistory) ? (d.statusHistory as Array<Record<string, unknown>>) : [];
    const assignments = Array.isArray(d.currentAssignments)
        ? (d.currentAssignments as Array<Record<string, unknown>>)
        : d.currentAssignment
          ? [d.currentAssignment as Record<string, unknown>]
          : [];
    const files: Partial<Record<TruckFileKind, TruckFile[]>> = {
        imageFrontRight: file(d.imageFrontRight),
        imageFrontLeft: file(d.imageFrontLeft),
        imageBackRight: file(d.imageBackRight),
        imageBackLeft: file(d.imageBackLeft),
        documentTax: file(d.documentTax),
        documentRegister: file(d.documentRegister),
        taxReceipt: file(d.taxReceipt),
        insuranceReceipt: file(d.insuranceReceipt),
        insuranceDocument: fileList(d.insuranceDocuments),
        legacyImage: fileList(d.images),
    };
    for (const kind of Object.keys(files) as TruckFileKind[]) if (files[kind]?.length === 0) delete files[kind];
    return {
        id,
        tenantId: ownership === "subcontractor" ? String(d.subcontractorId ?? "") : "",
        ownershipType: ownership,
        licensePlate: String(d.licensePlate ?? ""),
        province: String(d.province ?? ""),
        vin: text(d.vin),
        engineNumber: text(d.engineNumber),
        gpsVehicleId: text(d.GPSVehicleId),
        status: status ?? "active",
        legacyStatus: status ? null : text(d.truckStatus),
        brand: String(d.brand ?? ""),
        model: String(d.model ?? ""),
        year: num(d.year) ?? 0,
        color: String(d.color ?? ""),
        type: String(d.type ?? ""),
        vehicleClass: null,
        seats: num(d.seats),
        fuelType: text(d.fuelType),
        engineCapacity: num(d.engineCapacity),
        fuelCapacity: num(d.fuelCapacity),
        maxLoadWeightKg: num(d.maxLoadWeight),
        registrationDate: apiDate(d.registrationDate),
        buyingDate: apiDate(d.buyingDate),
        notes: text(d.notes),
        taxExpiryDate: apiDate(d.taxExpiryDate),
        taxExpenseThb: num(d.taxExpense),
        taxResponsible: text(d.taxResponsible) ?? "Operation Admin",
        taxRenewalStatus: renewal(d.taxRenewalStatus),
        maintenanceResponsible: text(d.maintenanceResponsible) ?? "Driver",
        lastServiceDate: apiDate(d.lastServiceDate),
        nextServiceDate: apiDate(d.nextServiceDate),
        nextServiceMileage: num(d.nextServiceMileage),
        pmIntervalKm: num(d.pmIntervalKm),
        currentMileage: num(d.currentMileage),
        insurancePolicyId: text(d.insurancePolicyId),
        insurancePolicyNumber: text(d.insurancePolicyNumber),
        insuranceCompany: text(d.insuranceCompany),
        insuranceType: text(d.insuranceType),
        insuranceStartDate: apiDate(d.insuranceStartDate),
        insuranceExpiryDate: apiDate(d.insuranceExpiryDate),
        insurancePremiumThb: num(d.insurancePremium),
        insuranceNotes: text(d.insuranceNotes),
        insuranceRenewalStatus: renewal(d.insuranceRenewalStatus),
        paymentMethod: apiPaymentMethod(text(d.paymentMethod)),
        createdAt: iso(d.createdAt),
        updatedAt: iso(d.updatedAt),
        statusHistory: history
            .map((h) => ({
                status: String(h.status ?? ""),
                previousStatus: text(h.previousStatus),
                changedAt: iso(h.date ?? h.changedAt),
                changedBy: null,
                changedByLabel: text(h.changedBy),
                reason: text(h.notes ?? h.reason),
            }))
            .reverse(),
        currentAssignments: assignments.map((a) => ({
            id: String(a.assignmentId ?? ""),
            driverId: String(a.driverId ?? ""),
            driverName: text(a.driverName),
            assignedAt: iso(a.assignedAt),
        })),
        files,
    };
}

async function allTrucks(): Promise<TruckDetailDTO[]> {
    const snap = await getDocs(query(collection(db, COLLECTIONS.TRUCKS), orderBy("createdAt", "desc")));
    return snap.docs.map((d) => truckDTOFromFirestore(d.id, d.data() as Record<string, unknown>));
}

/** Every truck matching the list filters (the collection, then the filters the API applies). */
export async function listTrucksFirestore(f: TruckListFilter): Promise<TruckDTO[]> {
    const q = (f.q ?? "").trim().toLowerCase();
    return (await allTrucks()).filter(
        (t) =>
            (!f.ownership || t.ownershipType === f.ownership) &&
            (!f.status || t.status === f.status) &&
            (!f.type || t.type === f.type) &&
            (!f.tenantId || t.tenantId === f.tenantId) &&
            (!q || [t.licensePlate, t.province, t.brand, t.model, t.vin ?? "", t.gpsVehicleId ?? ""].some((s) => s.toLowerCase().includes(q)))
    );
}

export async function getTruckFirestore(id: string): Promise<TruckDetailDTO | null> {
    const snap = await getDoc(doc(db, COLLECTIONS.TRUCKS, id));
    if (!snap.exists()) return null;
    return truckDTOFromFirestore(snap.id, snap.data() as Record<string, unknown>);
}

const DAY_MS = 86_400_000;

function state(expiry: string | null, status: string | null, today: Date): RenewalDTO["tax"]["state"] {
    if (status === "in_progress") return "in_progress";
    if (!expiry) return "overdue";
    const days = Math.ceil((new Date(expiry).setHours(0, 0, 0, 0) - today.getTime()) / DAY_MS);
    if (days < 0) return "overdue";
    if (days <= 30) return "expiring_soon";
    return "ok";
}

export async function listRenewalsFirestore(): Promise<RenewalDTO[]> {
    const rows = await getRenewalOverview();
    const today = new Date();
    today.setHours(0, 0, 0, 0);
    const days = (expiry: string | null) => (expiry ? Math.ceil((new Date(expiry).setHours(0, 0, 0, 0) - today.getTime()) / DAY_MS) : null);
    return rows.map((t) => {
        const taxExpiry = apiDate(t.taxExpiryDate);
        const insExpiry = apiDate(t.insuranceExpiryDate);
        return {
            truckId: t.id,
            tenantId: t.ownershipType === "subcontractor" ? (t.subcontractorId ?? "") : "",
            ownershipType: t.ownershipType === "subcontractor" ? "subcontractor" : "own",
            licensePlate: t.licensePlate ?? "",
            province: t.province ?? "",
            status: apiTruckStatus(t.truckStatus) ?? "active",
            type: t.type ?? "",
            vehicleClass: null,
            paymentMethod: apiPaymentMethod(t.paymentMethod),
            nextDueDate: !taxExpiry || !insExpiry ? null : taxExpiry < insExpiry ? taxExpiry : insExpiry,
            tax: {
                expiryDate: taxExpiry,
                renewalStatus: t.taxRenewalStatus ?? null,
                state: state(taxExpiry, t.taxRenewalStatus ?? null, today),
                daysRemaining: days(taxExpiry),
                amountThb: t.taxExpense ?? null,
                responsible: t.taxResponsible ?? "Operation Admin",
            },
            insurance: {
                expiryDate: insExpiry,
                renewalStatus: t.insuranceRenewalStatus ?? null,
                state: state(insExpiry, t.insuranceRenewalStatus ?? null, today),
                daysRemaining: days(insExpiry),
                amountThb: t.insurancePremium ?? null,
                company: t.insuranceCompany ?? null,
                policyNumber: t.insurancePolicyNumber ?? null,
                coverage: t.insuranceType ?? null,
            },
        };
    });
}

/** Uploads the form's picked files (blob URLs) to Storage, as the legacy forms did. */
async function withUploads(save: TruckSave): Promise<Record<string, unknown>> {
    const data: Record<string, unknown> = { ...save.values };
    const upload = async (value: unknown, prefix: string): Promise<string | undefined> => {
        const v = typeof value === "string" ? value : "";
        if (!v || !v.startsWith("blob:")) return v || undefined;
        const f = save.pending.get(v);
        if (!f) return undefined;
        return uploadTruckFile(f, `trucks/${prefix}/${Date.now()}_${f.name}`);
    };
    for (const field of ["imageFrontRight", "imageFrontLeft", "imageBackRight", "imageBackLeft"] as const) {
        const url = await upload(data[field], `photos/${field.replace("image", "").toLowerCase()}`);
        if (url) data[field] = url;
    }
    if (data.documentTax) data.documentTax = (await upload(data.documentTax, "documents/tax")) ?? data.documentTax;
    if (data.documentRegister) data.documentRegister = (await upload(data.documentRegister, "documents/register")) ?? data.documentRegister;
    const docs = Array.isArray(data.insuranceDocuments) ? (data.insuranceDocuments as string[]) : [];
    if (docs.length > 0) {
        const out: string[] = [];
        for (const d of docs) {
            const url = await upload(d, "insurance");
            if (url) out.push(url);
        }
        data.insuranceDocuments = out;
    }
    return data;
}

export async function createTruckFirestore(save: TruckSave): Promise<string> {
    const data = await withUploads(save);
    const { truckId } = await saveNewTruckToFirestoreClient(data, save.userId ?? "");
    return truckId;
}

export async function updateTruckFirestore(id: string, save: TruckSave): Promise<void> {
    const data = await withUploads(save);
    // The renewal fields are written by the renewal form only (the edit form always left them out).
    for (const k of ["taxRenewalStatus", "insuranceRenewalStatus", "taxExpense", "taxReceipt", "insuranceReceipt"]) delete data[k];
    await updateTruckInFirestoreClient(id, data, save.userId ?? "");
}

/** The legacy renewal write: truck fields, the status history entry and, when completed, the ledger row. */
export async function renewTruckFirestore(r: RenewalSave): Promise<void> {
    const t = r.truck;
    const ext = (f: File) => f.name.split(".").pop();
    let receiptUrl = r.type === "tax" ? t.files.taxReceipt?.[0]?.url ?? undefined : t.files.insuranceReceipt?.[0]?.url ?? undefined;
    if (r.receipt) {
        receiptUrl = await uploadTruckFile(r.receipt, `trucks/${t.id}/${r.type}_receipt_${new Date().getFullYear()}_${Date.now()}.${ext(r.receipt)}`);
    }
    const docs = [...(r.documents ?? [])];
    for (const f of r.newDocuments ?? []) {
        docs.push(await uploadTruckFile(f, `trucks/${t.id}/insurance_doc_${new Date().getFullYear()}_${Date.now()}.${ext(f)}`));
    }
    const completed = r.status === "completed";
    const history = [...t.statusHistory].reverse().map((h) => ({ status: h.status, date: h.changedAt ?? "", changedBy: h.changedByLabel ?? "", notes: h.reason ?? undefined }));
    const payload: Record<string, unknown> = {};
    if (r.paymentMethod) payload.paymentMethod = r.paymentMethod;
    if (r.type === "tax") {
        payload.taxRenewalStatus = r.status;
        payload.taxResponsible = r.responsible;
        if (r.amount !== null) payload.taxExpense = r.amount;
        if (receiptUrl) payload.taxReceipt = receiptUrl;
        if (completed) {
            payload.taxExpiryDate = r.expiryDate;
            if (receiptUrl) payload.documentTax = receiptUrl;
            payload.statusHistory = [...history, { status: "Tax Renewed", date: new Date().toISOString(), changedBy: r.actorLabel ?? "Unknown", notes: `Expiry: ${r.expiryDate}` }];
        } else if (r.expiryDate) payload.taxExpiryDate = r.expiryDate;
    } else {
        payload.insuranceRenewalStatus = r.status;
        payload.maintenanceResponsible = r.responsible;
        if (r.provider) payload.insuranceCompany = r.provider;
        if (r.amount !== null) payload.insurancePremium = r.amount;
        if (receiptUrl) payload.insuranceReceipt = receiptUrl;
        if (r.policyId) payload.insurancePolicyId = r.policyId;
        if (r.policyNumber) payload.insurancePolicyNumber = r.policyNumber;
        if (r.coverage) payload.insuranceType = r.coverage;
        if (r.insuranceNotes) payload.insuranceNotes = r.insuranceNotes;
        if (r.expiryDate) payload.insuranceExpiryDate = r.expiryDate;
        if (r.startDate) payload.insuranceStartDate = r.startDate;
        if (completed && receiptUrl && !docs.includes(receiptUrl)) docs.push(receiptUrl);
        if (docs.length > 0) payload.insuranceDocuments = docs;
        if (completed) {
            payload.statusHistory = [
                ...history,
                { status: "Insurance Renewed", date: new Date().toISOString(), changedBy: r.actorLabel ?? "Unknown", notes: `Policy: ${r.policyNumber ?? ""} | Expiry: ${r.expiryDate}` },
            ];
        }
    }
    await updateTruckInFirestoreClient(t.id, payload, r.userId ?? "");
    if (completed) {
        await logTransaction({
            truckId: t.id,
            type: r.type,
            subType: r.type === "tax" ? "Tax Renewal" : `Insurance Renewal (${r.coverage || "Unknown"})`,
            amount: r.amount ?? 0,
            paymentMethod: r.paymentMethod || "Unknown",
            date: new Date().toISOString().split("T")[0],
            receiptUrl: receiptUrl || null,
            performedBy: r.actorLabel ?? "Unknown",
            notes: r.type === "tax" ? `Expiry: ${r.expiryDate}` : `Policy: ${r.policyNumber ?? ""} | Expiry: ${r.expiryDate}`,
        });
    }
}

/** The legacy import: one new document per validated row. */
export async function importTrucksFirestore(rows: Record<string, unknown>[]): Promise<TruckImportResult> {
    const batch = writeBatch(db);
    for (const row of rows) {
        batch.set(doc(collection(db, COLLECTIONS.TRUCKS)), {
            ...row,
            createdAt: new Date(),
            updatedAt: new Date(),
            currentMileage: 0,
            images: [],
            statusHistory: [],
        });
    }
    await batch.commit();
    return { created: rows.length, updated: 0, unchanged: 0, errors: [] };
}
