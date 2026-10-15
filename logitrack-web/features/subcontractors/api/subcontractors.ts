/**
 * Subcontractors (carrier profiles) for the web (T25; developer-spec.md §10.7, §10.13; Appendix B
 * §B.2.10 as built by T20: a subcontractor is a tenant of kind `carrier`, served by internal/iam;
 * Appendix E §E.2.4, §E.4 row "subcontractors").
 *
 * Keys (`['subcontractors', …]`, one prefix):
 * - `['subcontractors', {view:'full'}]`: the list page, every carrier (`GET /v1/subcontractors`, all
 *   pages; `fleet:manage_subcontractors` or `platform:manage_tenants`); the page filters and sorts the
 *   few dozen rows in the browser as before.
 * - `['subcontractors','options']`: every carrier as `{id, code, nameTh, nameEn, status?}` for the truck
 *   and driver forms and the hub dialog (`fields=minimal`, any staff).
 * - `['subcontractors','detail',id]`: `GET /v1/subcontractors/{id}` (profile + documents).
 *
 * Go is the only source (TW7 removed the Firestore adapter and the `masterdata` flag switch of T25).
 */
import { queryOptions, type QueryClient } from "@tanstack/react-query";
import { goFetch, goPath } from "@/lib/goFetch";
import { fetchAllPages } from "@/lib/goPages";
import { uploadToGo } from "@/lib/goUpload";
import { getQueryClient } from "@/lib/queryClient";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
import type { SubcontractorFormValues } from "@/validate/subcontractorSchema";

export const SUBCONTRACTORS_PATH = "/v1/subcontractors";

export type CarrierStatus = "active" | "pending" | "suspended";
export type CarrierLegalType = "individual" | "company";

/** `GET /v1/subcontractors?fields=minimal` (pickers). */
export interface SubcontractorOptionDTO {
    id: string;
    code: string | null;
    nameTh: string;
    nameEn: string | null;
    status?: CarrierStatus | string;
    billingPartyId?: string | null;
}

/** `GET /v1/subcontractors` (the list page). */
export interface SubcontractorSummaryDTO {
    id: string;
    code: string | null;
    nameTh: string;
    nameEn: string | null;
    legalType: CarrierLegalType | null;
    status: CarrierStatus | string;
    contactPerson: string | null;
    phone: string | null;
    email: string | null;
    fleetSize: number;
    lineGroupId: string | null;
    contractorTenantId: string | null;
    billingPartyId: string | null;
    billingDateBasis: "delivered" | "plan" | null;
    createdAt: string | null;
    /** Unset: Go keeps the regions on the profile (the retired Firestore list showed them). */
    serviceArea?: string | null;
}

/** One document of a carrier profile (`tenant_files`). */
export interface TenantDocumentDTO {
    fileId: string;
    key: string | null;
    url: string | null;
    kind: string;
    position: number;
}

/** `GET /v1/subcontractors/{id}` (the tenant profile, internal/iam `Subcontractor`). */
export interface SubcontractorDTO extends SubcontractorSummaryDTO {
    kind?: string;
    /** Absent unless the caller may see it (platform, stewards, the tenant's own admin). */
    idCardNumber?: string | null;
    taxId: string | null;
    website: string | null;
    address: string | null;
    designation: string | null;
    dispatchCenter: string | null;
    serviceRegions: string[];
    vehicleTypes: string[];
    updatedAt: string | null;
    documents: TenantDocumentDTO[];
}

/** A display name: Thai, else English, else the code. */
export function carrierName(c: Pick<SubcontractorOptionDTO, "nameTh" | "nameEn" | "code"> | null | undefined): string {
    if (!c) return "";
    return c.nameTh || c.nameEn || c.code || "";
}

// --- form <-> API ------------------------------------------------------------------------------

const blank = (v: string | null | undefined): string => (v ?? "").trim();

/** The body of `POST /v1/subcontractors` / `PATCH` from the wizard and edit form. */
export function subcontractorInputFromForm(values: SubcontractorFormValues): Record<string, unknown> {
    const legalType = values.type ?? "individual";
    return {
        nameTh: blank(values.name),
        legalType,
        idCardNumber: legalType === "individual" ? blank(values.idCardNumber) : "",
        taxId: blank(values.taxId),
        contactPerson: blank(values.contactPerson),
        phone: blank(values.phone),
        email: blank(values.email),
        website: blank(values.website),
        address: blank(values.address),
        designation: blank(values.designation),
        fleetSize: Number(values.fleetSize ?? 0) || 0,
        dispatchCenter: blank(values.dispatchCenter),
        serviceRegions: values.serviceRegions ?? [],
        vehicleTypes: values.vehicleTypes ?? [],
        lineGroupId: blank(values.lineGroupId),
        billingDateBasis: values.billingDateBasis ?? "delivered",
    };
}

/** The edit form's values for a stored profile; `documents` holds the file refs (keys or URLs). */
export function subcontractorFormValues(s: SubcontractorDTO): SubcontractorFormValues {
    return {
        name: s.nameTh,
        type: s.legalType ?? "individual",
        idCardNumber: s.idCardNumber ?? "",
        taxId: s.taxId ?? "",
        contactPerson: s.contactPerson ?? "",
        phone: s.phone ?? "",
        email: s.email ?? "",
        website: s.website ?? "",
        address: s.address ?? "",
        designation: s.designation ?? "",
        fleetSize: s.fleetSize ?? 0,
        dispatchCenter: s.dispatchCenter ?? "",
        serviceRegions: s.serviceRegions ?? [],
        vehicleTypes: s.vehicleTypes ?? [],
        status: (["active", "pending", "suspended"].includes(String(s.status)) ? s.status : "active") as CarrierStatus,
        documents: s.documents.map((d) => d.key ?? d.url ?? "").filter(Boolean),
        lineGroupId: s.lineGroupId ?? "",
        billingDateBasis: s.billingDateBasis ?? "delivered",
    };
}

/** Preview URL of each document ref of a stored profile (a key on Go; a legacy URL is shown as it is). */
export function documentPreviewUrls(s: SubcontractorDTO): Record<string, string> {
    const out: Record<string, string> = {};
    for (const d of s.documents) {
        const ref = d.key ?? d.url;
        if (ref && d.url) out[ref] = d.url;
    }
    return out;
}

// --- reads -------------------------------------------------------------------------------------

/** `['subcontractors', {view:'full'}]`: every carrier for the list page. */
export const subcontractorsListQuery = queryOptions({
    queryKey: queryKeys.subcontractors.list({ view: "full" }),
    queryFn: ({ signal }) => fetchAllPages<SubcontractorSummaryDTO>(SUBCONTRACTORS_PATH, {}, { signal, maxPages: 100 }),
    ...QUERY_POLICY.masterData,
    refetchOnWindowFocus: true,
});

/** `['subcontractors','options']`: every carrier for a picker. */
export const subcontractorOptionsQuery = queryOptions({
    queryKey: queryKeys.subcontractors.options(),
    queryFn: ({ signal }) => fetchAllPages<SubcontractorOptionDTO>(SUBCONTRACTORS_PATH, { fields: "minimal" }, { signal, maxPages: 100 }),
    ...QUERY_POLICY.masterData,
    refetchOnWindowFocus: true,
});

/** `['subcontractors','detail',id]`. */
export function subcontractorQuery(id: string) {
    return queryOptions({
        queryKey: queryKeys.subcontractors.detail(id),
        queryFn: ({ signal }) => goFetch<SubcontractorDTO>(goPath`/v1/subcontractors/${id}`, { signal }),
        ...QUERY_POLICY.entity,
        enabled: id !== "",
    });
}

/** The picker list from the tab's cache (async callers). */
export function fetchSubcontractorOptions(client: QueryClient = getQueryClient()): Promise<SubcontractorOptionDTO[]> {
    return client.fetchQuery(subcontractorOptionsQuery);
}

export function invalidateSubcontractors(client: QueryClient = getQueryClient()): Promise<void> {
    return client.invalidateQueries({ queryKey: queryKeys.subcontractors.all() });
}

// --- writes ------------------------------------------------------------------------------------

/** Suspends a carrier without changing any profile fields. */
export async function suspendSubcontractor(id: string): Promise<void> {
    await goFetch(goPath`/v1/tenants/${id}`, { method: "PATCH", body: { status: "suspended" } });
}

export interface SubcontractorSave {
    values: SubcontractorFormValues;
}

export interface SubcontractorUpdate extends SubcontractorSave {
    id: string;
    /** The status changed: Go writes it through `PATCH /v1/tenants/{id}` (platform admins). */
    statusChanged?: boolean;
}

/** Uploads a carrier document now: a `tenant_document` key (presigned or local upload, §9.4). */
export async function uploadSubcontractorDocument({
    subcontractorId,
    file,
    variant,
}: {
    subcontractorId: string;
    file: File;
    variant: "id_card" | "company_doc";
}): Promise<{ ref: string; previewUrl: string }> {
    const key = await uploadToGo(file, { purpose: "tenant_document", entityId: subcontractorId, variant });
    return { ref: key, previewUrl: URL.createObjectURL(file) };
}

/** Creates a carrier profile; returns its id. */
export async function createSubcontractor({ values }: SubcontractorSave): Promise<string> {
    const body = { ...subcontractorInputFromForm(values), status: values.status ?? "active" };
    const created = await goFetch<SubcontractorDTO>(SUBCONTRACTORS_PATH, { method: "POST", body });
    return created.id;
}

/** Updates a carrier profile, and its status through the tenants route when it changed. */
export async function updateSubcontractor({ id, values, statusChanged }: SubcontractorUpdate): Promise<void> {
    const body = { ...subcontractorInputFromForm(values), documentKeys: values.documents ?? [] };
    await goFetch(goPath`/v1/subcontractors/${id}`, { method: "PATCH", body });
    if (statusChanged && values.status) {
        await goFetch(goPath`/v1/tenants/${id}`, { method: "PATCH", body: { status: values.status } });
    }
}
