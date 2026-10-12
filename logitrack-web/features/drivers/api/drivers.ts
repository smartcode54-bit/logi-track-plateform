/**
 * Drivers for the web (T25; developer-spec.md §10.7, §10.10, §10.13; Appendix B §B.2.7 as built by
 * T23; Appendix C §C.8 driver logins; Appendix E §E.2.2, §E.4 row "drivers", §E.5 row 23, §E.6).
 *
 * Keys (`['drivers', …]`; `driver.updated` and the assignment events invalidate the prefix):
 * - `['drivers', {status, q}]` (infinite, 100 per page): the drivers page, server search, keyset;
 *   `meta.statusCounts` feeds the stat cards over every matching driver (not only the loaded page).
 *   Polled every 60 s while visible and refetched on focus once older than that (row 23); no
 *   100-row cap.
 * - `['drivers','options']`: active drivers as picker options (the truck-assignment page).
 * - `['drivers','detail',id]`: `GET /v1/drivers/{id}` (PII only with `drivers:view_pii`).
 *
 * Logins: a driver's account is a user (`POST /v1/users {role:"driver", driverId}`, a temporary
 * password shown once, R29); the forms never call the `createUser` / `createDriverAccount` login
 * paths any more.
 *
 * Go is the only source (TW7 removed the Firestore adapter and the `masterdata` flag switch of T25).
 */
import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import { goFetch, goFetchEnvelope, goPath, type GoEnvelope } from "@/lib/goFetch";
import { fetchAllPages } from "@/lib/goPages";
import { goNextPageParam } from "@/lib/goQuery";
import { uploadToGo } from "@/lib/goUpload";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
import { createUser, type CreateUserResult } from "@/features/users/api/users";
import { fieldViolations } from "@/features/auth/utils/fieldErrors";
import { isApiError } from "@/lib/apiError";
import type { Driver } from "@/validate/driverSchema";
import { apiDate, localDate } from "@/lib/calendarDate";

export const DRIVERS_PATH = "/v1/drivers";
export const DRIVERS_PAGE_SIZE = 100;

export type DriverStatus = "active" | "inactive" | "on_duty";
export type EmploymentType = "full_time" | "subcontractor" | "part_time";

/** `GET /v1/drivers` item (internal/drivers `Driver`): no PII, no file. */
export interface DriverDTO {
    id: string;
    tenantId: string;
    tenantNameTh: string | null;
    tenantNameEn: string | null;
    tenantKind: string | null;
    userId: string | null;
    /** The Firestore document id of a loaded driver (the legacy `drivers/{id}`). */
    legacyDocId: string | null;
    displayName: string;
    firstName: string;
    lastName: string;
    fullNameTh: string | null;
    mobile: string;
    email: string | null;
    status: DriverStatus | string;
    employmentType: EmploymentType | string;
    contractYears: number | null;
    employmentStart: string | null;
    employmentEnd: string | null;
    hireDate: string | null;
    probationPassed: boolean;
    assignToProject: string | null;
    truckLicenseId: string | null;
    licenseType: string | null;
    licenseExpiredDate: string | null;
    activeTruck: { truckId: string; truckPlate: string | null; taskId: string | null; startedAt: string | null } | null;
    currentAssignment: { id: string; truckId: string | null; truckPlate: string | null; truckModel: string | null; assignedAt: string | null } | null;
    createdAt: string | null;
    updatedAt: string | null;
    /** Unset in the list: Go keeps files on the detail (`DriverDetailDTO`). */
    profileUrl?: string | null;
    /** The driver's legacy Firebase uid (`drivers.legacy_auth_uid`): the chats the 3.x APKs wrote are keyed by it. */
    legacyAuthUid?: string | null;
}

export interface CustomerDriverIdDTO {
    customerId: string;
    customerCode: string;
    customerName: string;
    idTypeKey: string;
    value: string;
}

export interface DriverStatusEntry {
    status: string;
    previousStatus: string | null;
    changedAt: string | null;
    changedBy: string | null;
    changedByLabel: string | null;
    reason: string | null;
}

/** `GET /v1/drivers/{id}`: PII keys are absent without `drivers:view_pii`. */
export interface DriverDetailDTO extends DriverDTO {
    profileKey: string | null;
    profileUrl: string | null;
    customerDriverIds: CustomerDriverIdDTO[];
    statusHistory: DriverStatusEntry[];
    login: { userId: string; email: string | null; status: string; mustChangePassword: boolean } | null;
    idCard?: string | null;
    idCardExpiredDate?: string | null;
    birthDate?: string | null;
    idCardKey?: string | null;
    idCardUrl?: string | null;
    licenseKey?: string | null;
    licenseUrl?: string | null;
}

/** A driver picker option. */
export interface DriverOptionDTO {
    id: string;
    displayName: string;
    truckLicenseId: string | null;
    licenseType: string | null;
}

export type StatusCounts = Partial<Record<DriverStatus, number>>;

// --- vocabularies ------------------------------------------------------------------------------

const FORM_STATUS: Record<string, Driver["status"]> = { active: "Active", inactive: "Inactive", on_duty: "On-Duty" };
const API_STATUS: Record<string, DriverStatus> = { Active: "active", Inactive: "inactive", "On-Duty": "on_duty" };
const FORM_EMPLOYMENT: Record<string, Driver["employmentType"]> = {
    full_time: "FULL_TIME",
    subcontractor: "SUBCONTRACTOR",
    part_time: "PART_TIME",
};

/** The API status of a legacy value (`Active`, `On-Duty`, ...). */
export function apiDriverStatus(raw: string | null | undefined): DriverStatus {
    if (!raw) return "active";
    if (raw in API_STATUS) return API_STATUS[raw];
    const s = raw.toLowerCase().replace(/[- ]/g, "_");
    return s === "inactive" || s === "on_duty" ? s : "active";
}

export function apiEmploymentType(raw: string | null | undefined): EmploymentType {
    const s = (raw ?? "").toLowerCase();
    return s === "subcontractor" || s === "part_time" ? s : "full_time";
}

/** The legacy display name order: Thai full name, else first and last name. */
export function driverName(d: Pick<DriverDTO, "fullNameTh" | "firstName" | "lastName" | "displayName">): string {
    return d.fullNameTh || [d.firstName, d.lastName].filter(Boolean).join(" ") || d.displayName;
}

const toDate = localDate;

/** The driver form's values for a stored driver (`EditDriverForm`; customer ids keyed by code). */
export function driverFormValues(d: DriverDetailDTO): Driver {
    const customerDriverIds: Record<string, Record<string, string>> = {};
    for (const c of d.customerDriverIds) {
        customerDriverIds[c.customerCode] = { ...(customerDriverIds[c.customerCode] ?? {}), [c.idTypeKey]: c.value };
    }
    return {
        id: d.id,
        firstName: d.firstName,
        lastName: d.lastName,
        fullNameTh: d.fullNameTh ?? "",
        mobile: d.mobile,
        email: d.email ?? "",
        birthDate: toDate(d.birthDate) as Date,
        idCard: d.idCard ?? "",
        idCardExpiredDate: toDate(d.idCardExpiredDate),
        truckLicenseId: d.truckLicenseId ?? "",
        licenseType: (d.licenseType ?? undefined) as Driver["licenseType"],
        truckLicenseExpiredDate: toDate(d.licenseExpiredDate),
        employmentType: FORM_EMPLOYMENT[d.employmentType] ?? "FULL_TIME",
        subcontractorId: d.tenantKind === "carrier" ? d.tenantId : undefined,
        subcontractorName: d.tenantKind === "carrier" ? (d.tenantNameTh ?? undefined) : undefined,
        contractYears: d.contractYears ?? undefined,
        employmentPeriod: { start: toDate(d.employmentStart), end: toDate(d.employmentEnd) },
        hireDate: toDate(d.hireDate),
        probationPassed: d.probationPassed,
        status: FORM_STATUS[d.status] ?? "Active",
        assignToProject: d.assignToProject ?? undefined,
        customerDriverIds,
        statusHistory: [],
    } as Driver;
}

const text = (v: unknown): string | null => {
    const s = String(v ?? "").trim();
    return s === "" ? null : s;
};

/**
 * The Go body of the driver form (no files, no tenant). `customerIdByCode` resolves the form's
 * customer codes (`customerDriverIds.SPX.appId`) to customer ids; codes it does not know are left
 * out (the server would refuse them).
 */
export function driverBodyFromForm(v: Driver, customerIdByCode: ReadonlyMap<string, string>): Record<string, unknown> {
    const ids: Array<{ customerId: string; idTypeKey: string; value: string }> = [];
    for (const [code, map] of Object.entries(v.customerDriverIds ?? {})) {
        const customerId = customerIdByCode.get(code.toUpperCase());
        if (!customerId || !map) continue;
        for (const [idTypeKey, value] of Object.entries(map)) {
            if (value && String(value).trim()) ids.push({ customerId, idTypeKey, value: String(value).trim() });
        }
    }
    return {
        firstName: v.firstName.trim(),
        lastName: v.lastName.trim(),
        fullNameTh: v.fullNameTh.trim(),
        mobile: v.mobile.trim(),
        email: text(v.email),
        birthDate: apiDate(v.birthDate),
        idCard: text(v.idCard),
        idCardExpiredDate: apiDate(v.idCardExpiredDate),
        truckLicenseId: text(v.truckLicenseId),
        licenseType: text(v.licenseType),
        licenseExpiredDate: apiDate(v.truckLicenseExpiredDate),
        employmentType: apiEmploymentType(v.employmentType),
        contractYears: v.contractYears ?? null,
        employmentStart: apiDate(v.employmentPeriod?.start),
        employmentEnd: apiDate(v.employmentPeriod?.end),
        hireDate: apiDate(v.hireDate),
        probationPassed: v.probationPassed ?? false,
        status: apiDriverStatus(v.status),
        assignToProject: text(v.assignToProject),
        customerDriverIds: ids,
    };
}

/** PII fields: written only when they changed (writing one needs `drivers:view_pii`). */
const PII_FIELDS = ["idCard", "idCardExpiredDate", "birthDate"] as const;

/** The fields of `next` that differ from `prev` (a PATCH that changes nothing writes nothing). */
export function changedFields(next: Record<string, unknown>, prev: Record<string, unknown> | null): Record<string, unknown> {
    if (!prev) return next;
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(next)) if (JSON.stringify(v) !== JSON.stringify(prev[k])) out[k] = v;
    return out;
}

// --- reads -------------------------------------------------------------------------------------

export type DriverListFilter = {
    status: "" | DriverStatus;
    q: string;
};

/** One page of the drivers list with the status counts of every matching driver. */
export type DriverPage = GoEnvelope<DriverDTO[]> & { meta?: { statusCounts?: StatusCounts } };

type DriverListKey = ReturnType<typeof queryKeys.drivers.list<DriverListFilter>>;

/** `['drivers', {status, q}]` (infinite). */
export function driverPagesQuery(filter: DriverListFilter) {
    const params: DriverListFilter = { status: filter.status, q: filter.q.trim() };
    return infiniteQueryOptions({
        queryKey: queryKeys.drivers.list(params) as DriverListKey,
        queryFn: ({ queryKey: [, p], signal, pageParam }) =>
            goFetchEnvelope<DriverDTO[]>(DRIVERS_PATH, {
                signal,
                query: { status: p.status || undefined, q: p.q || undefined, limit: DRIVERS_PAGE_SIZE, cursor: pageParam },
            }) as Promise<DriverPage>,
        initialPageParam: undefined as string | undefined,
        getNextPageParam: goNextPageParam,
        ...QUERY_POLICY.entity,
    });
}

/** `['drivers','options']`: active drivers for a picker (Go: the full list, every page). */
export const driverOptionsQuery = queryOptions({
    queryKey: queryKeys.drivers.options(),
    queryFn: async ({ signal }): Promise<DriverOptionDTO[]> => {
        const rows = await fetchAllPages<DriverDTO>(DRIVERS_PATH, {}, { signal, maxPages: 200 });
        return rows
            .filter((d) => d.status !== "inactive")
            .map((d) => ({ id: d.id, displayName: driverName(d), truckLicenseId: d.truckLicenseId, licenseType: d.licenseType }));
    },
    ...QUERY_POLICY.entity,
});

/** `['drivers','detail',id]`. */
export function driverQuery(id: string) {
    return queryOptions({
        queryKey: queryKeys.drivers.detail(id),
        queryFn: ({ signal }) => goFetch<DriverDetailDTO>(goPath`/v1/drivers/${id}`, { signal }),
        ...QUERY_POLICY.entity,
        enabled: id !== "",
    });
}

// --- writes ------------------------------------------------------------------------------------

export interface DriverFiles {
    profile: File | null;
    idCard: File | null;
    license: File | null;
}

export interface DriverSave {
    values: Driver;
    files: DriverFiles;
    /** The profile photo was removed. */
    removeProfile?: boolean;
    /** Customer code -> id (Go: `['customers','options']`). */
    customerIdByCode: ReadonlyMap<string, string>;
}

async function fileKeys(driverId: string, files: DriverFiles, removeProfile?: boolean): Promise<Record<string, string | null>> {
    const keys: Record<string, string | null> = {};
    if (files.profile) keys.profileKey = await uploadToGo(files.profile, { purpose: "driver_profile", entityId: driverId });
    else if (removeProfile) keys.profileKey = null;
    if (files.idCard) keys.idCardKey = await uploadToGo(files.idCard, { purpose: "driver_id_card", entityId: driverId });
    if (files.license) keys.licenseKey = await uploadToGo(files.license, { purpose: "driver_license", entityId: driverId });
    return keys;
}

async function createOnGo(save: DriverSave): Promise<string> {
    const body: Record<string, unknown> = driverBodyFromForm(save.values, save.customerIdByCode);
    if (apiEmploymentType(save.values.employmentType) === "subcontractor" && save.values.subcontractorId) {
        body.tenantId = save.values.subcontractorId;
    }
    const created = await goFetch<DriverDetailDTO>(DRIVERS_PATH, { method: "POST", body });
    const keys = await fileKeys(created.id, save.files);
    if (Object.keys(keys).length > 0) await goFetch(goPath`/v1/drivers/${created.id}`, { method: "PATCH", body: keys });
    return created.id;
}

async function updateOnGo(id: string, save: DriverSave & { previous: DriverDetailDTO }): Promise<void> {
    const prev = driverBodyFromForm(driverFormValues(save.previous), save.customerIdByCode);
    const next = driverBodyFromForm(save.values, save.customerIdByCode);
    const body = changedFields(next, prev);
    // An unchanged PII field is never sent: writing one needs drivers:view_pii.
    for (const f of PII_FIELDS) if (!(f in body)) delete body[f];
    const wantsTenant =
        apiEmploymentType(save.values.employmentType) === "subcontractor" && save.values.subcontractorId
            ? save.values.subcontractorId
            : null;
    if (wantsTenant && wantsTenant !== save.previous.tenantId) {
        // The broker-carrier move travels alone (`422 tenantId exclusive`; stewards only).
        await goFetch(goPath`/v1/drivers/${id}`, { method: "PATCH", body: { tenantId: wantsTenant } });
    }
    Object.assign(body, await fileKeys(id, save.files, save.removeProfile));
    if (Object.keys(body).length > 0) await goFetch(goPath`/v1/drivers/${id}`, { method: "PATCH", body });
}

/** Registers a driver profile (no login: that is `createDriverLogin`); returns its id. */
export const createDriver = (save: DriverSave): Promise<string> => createOnGo(save);

export const updateDriver = ({ id, ...save }: DriverSave & { id: string; previous: DriverDetailDTO }): Promise<void> => updateOnGo(id, save);

/** The input of `createDriverLogin`. */
export interface DriverLoginInput {
    /**
     * The driver; `tenantId` is its tenant (Go; the carrier of a subcontractor driver), where the login's
     * membership must be. Unset: the caller's active tenant.
     */
    driver: Pick<DriverDTO, "id" | "mobile" | "fullNameTh" | "firstName" | "lastName" | "displayName"> & { tenantId?: string | null };
    email: string;
    sendInvite?: boolean;
}

/**
 * Creates the driver's login through the users administration (`POST /v1/users {role:"driver",
 * driverId, tenantId}`, Appendix C §C.8): Go generates a temporary password (shown once, R29) or
 * mails an invite. `tenantId` is the driver's tenant: an own-fleet admin creates the login of a carrier's
 * driver in that carrier (a tenant of its write reach, T25 review), where the driver link must be.
 * The answer holds the temporary password: callers keep it in their own state, never in a query or
 * mutation cache (`useCreateDriverLogin`).
 */
export const createDriverLogin = ({ driver, email, sendInvite }: DriverLoginInput): Promise<CreateUserResult> =>
    createUser({ email, displayName: driverName(driver), role: "driver", driverId: driver.id, tenantId: driver.tenantId || undefined, sendInvite });

/**
 * Whether a failed login creation means the driver is outside what the caller may manage: a driver
 * of another tenant (`422 driverId not_found`) or a tenant outside its reach (`403 platform_only`).
 */
export function isDriverLoginOutOfReach(error: unknown): boolean {
    if (fieldViolations(error).some((v) => v.field === "driverId" && v.reason === "not_found")) return true;
    return isApiError(error) && error.code === "permission_denied" && error.details.reason === "platform_only";
}
