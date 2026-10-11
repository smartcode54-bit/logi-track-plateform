/**
 * Firestore source of the drivers module while the `masterdata` flag is off (developer-spec.md §10.6
 * "Domain adapter"): the `drivers` documents mapped to the Go DTOs, and the legacy profile writes
 * (`createDriverAccount` / `updateDriverAccount` callables, profile only: logins are created through
 * `POST /v1/users`, see `createDriverLogin`). Loaded only through `import()`. Deleted with the
 * Firebase SDK (TW7).
 */
import { collection, doc, getDoc, getDocs, orderBy, query } from "firebase/firestore";
import { getDownloadURL, ref, uploadBytes } from "firebase/storage";
import { httpsCallable } from "firebase/functions";
import { db, functions, storage } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { apiDate } from "@/lib/calendarDate";
import type { Driver } from "@/validate/driverSchema";
import {
    apiDriverStatus,
    apiEmploymentType,
    driverName,
    type DriverDetailDTO,
    type DriverDTO,
    type DriverListFilter,
    type DriverOptionDTO,
    type DriverPage,
    type DriverSave,
    type StatusCounts,
} from "./drivers";

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

/** One stored driver as the Go detail DTO (PII included: the legacy page showed it to every reader). */
export function driverDTOFromFirestore(id: string, d: Record<string, unknown>): DriverDetailDTO {
    const assignment = d.currentAssignment as Record<string, unknown> | null | undefined;
    const active = d.activeTruck as Record<string, unknown> | null | undefined;
    const employment = apiEmploymentType(text(d.employmentType));
    const ids = (d.customerDriverIds ?? {}) as Record<string, Record<string, unknown>>;
    const history = Array.isArray(d.statusHistory) ? (d.statusHistory as Array<Record<string, unknown>>) : [];
    const period = (d.employmentPeriod ?? {}) as Record<string, unknown>;
    const base: DriverDTO = {
        id,
        tenantId: employment === "subcontractor" ? String(d.subcontractorId ?? "") : "",
        tenantNameTh: employment === "subcontractor" ? text(d.subcontractorName) : null,
        tenantNameEn: null,
        tenantKind: employment === "subcontractor" && d.subcontractorId ? "carrier" : null,
        userId: null,
        legacyDocId: id,
        displayName: "",
        firstName: String(d.firstName ?? ""),
        lastName: String(d.lastName ?? ""),
        fullNameTh: text(d.fullNameTh),
        mobile: String(d.mobile ?? ""),
        email: text(d.email),
        status: apiDriverStatus(text(d.status)),
        employmentType: employment,
        contractYears: typeof d.contractYears === "number" ? d.contractYears : d.contractYears ? Number(d.contractYears) || null : null,
        employmentStart: apiDate(period.start),
        employmentEnd: apiDate(period.end),
        hireDate: apiDate(d.hireDate),
        probationPassed: d.probationPassed === true,
        assignToProject: text(d.assignToProject),
        truckLicenseId: text(d.truckLicenseId),
        licenseType: text(d.licenseType),
        licenseExpiredDate: apiDate(d.truckLicenseExpiredDate),
        activeTruck: active
            ? { truckId: String(active.truckId ?? ""), truckPlate: text(active.truckPlate), taskId: text(active.taskId), startedAt: iso(active.startedAt) }
            : null,
        currentAssignment:
            assignment && Object.keys(assignment).length > 0
                ? {
                      id: String(assignment.assignmentId ?? ""),
                      truckId: text(assignment.truckId),
                      truckPlate: text(assignment.truckPlate),
                      truckModel: text(assignment.truckModel),
                      assignedAt: iso(assignment.assignedAt),
                  }
                : null,
        createdAt: iso(d.createdAt),
        updatedAt: iso(d.updatedAt),
        profileUrl: text(d.profileImage),
        legacyAuthUid: text(d.authId),
    };
    base.displayName = driverName(base);
    return {
        ...base,
        profileKey: null,
        profileUrl: text(d.profileImage),
        customerDriverIds: Object.entries(ids).flatMap(([code, map]) =>
            Object.entries(map ?? {})
                .filter(([, v]) => v !== undefined && v !== null && String(v).trim() !== "")
                .map(([idTypeKey, value]) => ({ customerId: "", customerCode: code, customerName: code, idTypeKey, value: String(value) }))
        ),
        statusHistory: history
            .map((h) => ({
                status: String(h.status ?? ""),
                previousStatus: text(h.previousStatus),
                changedAt: iso(h.changedAt),
                changedBy: null,
                changedByLabel: text(h.changedByName ?? h.changedBy),
                reason: text(h.reason),
            }))
            .reverse(),
        login: text(d.authId) ? { userId: String(d.authId), email: text(d.email), status: "active", mustChangePassword: false } : null,
        idCard: text(d.idCard),
        idCardExpiredDate: apiDate(d.idCardExpiredDate),
        birthDate: apiDate(d.birthDate),
        idCardKey: null,
        idCardUrl: text(d.idCardImage),
        licenseKey: null,
        licenseUrl: text(d.truckLicenseImage),
    };
}

async function allDrivers(): Promise<DriverDetailDTO[]> {
    const snap = await getDocs(query(collection(db, COLLECTIONS.DRIVERS), orderBy("createdAt", "desc")));
    return snap.docs.map((d) => driverDTOFromFirestore(d.id, d.data() as Record<string, unknown>));
}

/** Every driver matching the search (no 100-row cap), with the status counts the API sends in `meta`. */
export async function listDriversFirestore(f: DriverListFilter): Promise<DriverPage> {
    const q = f.q.trim().toLowerCase();
    const matching = (await allDrivers()).filter(
        (d) =>
            !q ||
            [d.fullNameTh ?? "", `${d.firstName} ${d.lastName}`, d.truckLicenseId ?? "", d.mobile, d.email ?? ""].some((s) =>
                s.toLowerCase().includes(q)
            )
    );
    const statusCounts: StatusCounts = { active: 0, inactive: 0, on_duty: 0 };
    for (const d of matching) statusCounts[d.status as keyof StatusCounts] = (statusCounts[d.status as keyof StatusCounts] ?? 0) + 1;
    return { data: matching.filter((d) => !f.status || d.status === f.status), meta: { statusCounts } };
}

export async function listDriverOptionsFirestore(): Promise<DriverOptionDTO[]> {
    // The legacy picker: drivers without a home truck whose status is active or on duty.
    return (await allDrivers())
        .filter((d) => d.status !== "inactive" && !d.currentAssignment)
        .map((d) => ({ id: d.id, displayName: `${d.firstName} ${d.lastName}`.trim(), truckLicenseId: d.truckLicenseId, licenseType: d.licenseType }));
}

export async function getDriverFirestore(id: string): Promise<DriverDetailDTO | null> {
    const snap = await getDoc(doc(db, COLLECTIONS.DRIVERS, id));
    if (!snap.exists()) return null;
    return driverDTOFromFirestore(snap.id, snap.data() as Record<string, unknown>);
}

async function upload(file: File, path: string): Promise<string> {
    const snapshot = await uploadBytes(ref(storage, path), file);
    return getDownloadURL(snapshot.ref);
}

const removeUndefined = (obj: unknown): unknown => {
    if (obj instanceof Date) return obj;
    if (Array.isArray(obj)) return obj.map(removeUndefined);
    if (typeof obj === "object" && obj !== null) {
        return Object.fromEntries(
            Object.entries(obj)
                .map(([k, v]) => [k, removeUndefined(v)])
                .filter(([, v]) => v !== undefined)
        );
    }
    return obj;
};

async function withFiles(values: Driver, save: DriverSave): Promise<Record<string, unknown>> {
    const out: Record<string, unknown> = { ...values };
    const ts = Date.now();
    if (save.files.profile) out.profileImage = await upload(save.files.profile, `drivers/profile/${ts}_${save.files.profile.name}`);
    else if (save.removeProfile) out.profileImage = "";
    if (save.files.idCard) out.idCardImage = await upload(save.files.idCard, `drivers/documents/${ts}_id_card_${save.files.idCard.name}`);
    if (save.files.license) out.truckLicenseImage = await upload(save.files.license, `drivers/documents/${ts}_license_${save.files.license.name}`);
    return out;
}

/** The profile only (`skipAuthCreation`): the login goes through `POST /v1/users`. */
export async function createDriverFirestore(save: DriverSave): Promise<string> {
    const data = removeUndefined(await withFiles(save.values, save)) as Record<string, unknown>;
    const call = httpsCallable(functions, "createDriverAccount");
    const result = await call({ ...data, skipAuthCreation: true });
    return String((result.data as { driverId?: string } | undefined)?.driverId ?? "");
}

export async function updateDriverFirestore(id: string, save: DriverSave): Promise<void> {
    let values = save.values;
    if (values.customerDriverIds && typeof values.customerDriverIds === "object") {
        const cleaned: Record<string, Record<string, string>> = {};
        for (const [customer, idMap] of Object.entries(values.customerDriverIds)) {
            const nonEmpty = Object.fromEntries(Object.entries(idMap ?? {}).filter(([, v]) => v != null && String(v).trim() !== ""));
            if (Object.keys(nonEmpty).length > 0) cleaned[customer] = nonEmpty;
        }
        values = { ...values, customerDriverIds: Object.keys(cleaned).length > 0 ? cleaned : undefined };
    }
    const updates = await withFiles(values, save);
    // The form's synthetic fields are not stored.
    delete updates.id;
    delete updates.statusHistory;
    const call = httpsCallable(functions, "updateDriverAccount");
    await call({ id, updates: removeUndefined(updates) });
}
