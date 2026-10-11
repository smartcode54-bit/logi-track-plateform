/**
 * Companies (invoice issuers) for the web (T25; developer-spec.md §10.7, §10.13; Appendix B §B.2.10 as
 * built by T20; Appendix E §E.2.4, §E.4 row "owner company").
 *
 * Keys (`['companies', …]`, one prefix; `companies.changed` invalidates it in every tab):
 * - `['companies', {owner:true}]`: the owner company, the invoice and receipt header shared by the
 *   company profile, Billing Document and the Shopee report (`GET /v1/companies/owner`, `404` = none).
 * - `['companies', {view:'all'}]`: the companies page (`GET /v1/companies`, every page).
 *
 * Source: flag `masterdata`; the Firestore side is `./companies.firestore.ts`, loaded with `import()`.
 * The Go DTO has the stored field names of the Firestore document (T20 kept them), plus the private
 * asset keys; `getOwnerCompany()` returns the legacy `CompanyWithId` shape for its billing callers.
 */
import { queryOptions, type QueryClient } from "@tanstack/react-query";
import { ApiError, goFetch, goPath } from "@/lib/goFetch";
import { fetchAllPages } from "@/lib/goPages";
import { uploadToGo, type UploadPurpose } from "@/lib/goUpload";
import { getQueryClient } from "@/lib/queryClient";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
import { domainMutationFn, domainQueryFn } from "@/features/platform/api/webFlags";
import type { Company, CompanyFormValues } from "@/validate/companySchema";

const firestore = () => import("./companies.firestore");

export const COMPANIES_PATH = "/v1/companies";

/** The legacy record (Firestore document fields + id) that the billing pages read. */
export type CompanyWithId = Company & { id: string };

export type CompanyAsset = "logo" | "stamp" | "signature";

/** `GET /v1/companies`, `/v1/companies/owner` (internal/parties `Company`). */
export interface CompanyDTO {
    id: string;
    tenantId: string | null;
    nameTh: string;
    nameEn: string | null;
    shortName: string | null;
    taxId: string;
    branchType: "headquarters" | "branch";
    branchNumber: string | null;
    address: string;
    phone: string | null;
    email: string | null;
    logoKey: string | null;
    logoUrl: string | null;
    stampKey: string | null;
    stampUrl: string | null;
    signatureKey: string | null;
    signatureUrl: string | null;
    signatoryName: string | null;
    bankName: string | null;
    accountNumber: string | null;
    accountName: string | null;
    withholdingTaxRate: number;
    companyType: "owner" | "subcontractor";
    isActive: boolean;
    maxTrucks: number | null;
    maxDrivers: number | null;
    createdAt: string | null;
    updatedAt: string | null;
}

const undef = <T>(v: T | null | undefined): T | undefined => (v === null ? undefined : v);

/** The legacy record of a DTO (`null` -> absent), for `getOwnerCompany()` callers. */
export function companyRecord(c: CompanyDTO): CompanyWithId {
    return {
        id: c.id,
        nameTh: c.nameTh,
        nameEn: undef(c.nameEn),
        shortName: undef(c.shortName),
        taxId: c.taxId,
        branchType: c.branchType,
        branchNumber: undef(c.branchNumber),
        address: c.address,
        phone: undef(c.phone),
        email: undef(c.email) ?? "",
        logoUrl: undef(c.logoUrl) ?? "",
        stampUrl: undef(c.stampUrl) ?? "",
        signatureUrl: undef(c.signatureUrl) ?? "",
        signatoryName: undef(c.signatoryName),
        bankName: undef(c.bankName),
        accountNumber: undef(c.accountNumber),
        accountName: undef(c.accountName),
        withholdingTaxRate: c.withholdingTaxRate,
        companyType: c.companyType,
        isActive: c.isActive,
        maxTrucks: undef(c.maxTrucks),
        maxDrivers: undef(c.maxDrivers),
        createdAt: c.createdAt ?? undefined,
        updatedAt: c.updatedAt ?? undefined,
    };
}

/** The profile form's values (no asset URLs: the assets are staged files). */
export function companyFormValues(c: CompanyDTO): CompanyFormValues {
    const { id: _id, createdAt: _c, updatedAt: _u, ...values } = companyRecord(c);
    void _id;
    void _c;
    void _u;
    return values;
}

const blank = (v: string | undefined | null): string | null => {
    const s = (v ?? "").trim();
    return s === "" ? null : s;
};

/** The Go body of the profile form (asset keys are added by the save). */
export function companyInputFromForm(values: CompanyFormValues): Record<string, unknown> {
    return {
        nameTh: values.nameTh.trim(),
        nameEn: blank(values.nameEn),
        shortName: blank(values.shortName),
        taxId: values.taxId.trim(),
        branchType: values.branchType,
        branchNumber: values.branchType === "branch" ? blank(values.branchNumber) : null,
        address: values.address.trim(),
        phone: blank(values.phone),
        email: blank(values.email),
        signatoryName: blank(values.signatoryName),
        bankName: blank(values.bankName),
        accountNumber: blank(values.accountNumber),
        accountName: blank(values.accountName),
        withholdingTaxRate: values.withholdingTaxRate,
        companyType: values.companyType,
        isActive: values.isActive,
        maxTrucks: values.maxTrucks ?? null,
        maxDrivers: values.maxDrivers ?? null,
    };
}

// --- reads -------------------------------------------------------------------------------------

async function ownerFromGo(signal?: AbortSignal): Promise<CompanyDTO | null> {
    try {
        return await goFetch<CompanyDTO>(`${COMPANIES_PATH}/owner`, { signal });
    } catch (err) {
        // 404: no owner company in the caller's reach (none yet, or a carrier principal).
        if (err instanceof ApiError && err.status === 404) return null;
        throw err;
    }
}

/** `['companies',{owner:true}]`: shared by the profile form, Billing Document and the Shopee report. */
export const ownerCompanyQueryOptions = queryOptions({
    queryKey: queryKeys.companies.owner(),
    queryFn: domainQueryFn<CompanyDTO | null>("masterdata", {
        firestore: async () => (await firestore()).fetchOwnerCompanyFirestore(),
        go: ({ signal }) => ownerFromGo(signal),
    }),
    ...QUERY_POLICY.masterData,
    refetchOnWindowFocus: true,
});

/** `['companies',{view:'all'}]`: the companies page. */
export const companiesQueryOptions = queryOptions({
    queryKey: queryKeys.companies.list({ view: "all" }),
    queryFn: domainQueryFn<CompanyDTO[]>("masterdata", {
        firestore: async () => (await firestore()).fetchCompaniesFirestore(),
        go: ({ signal }) => fetchAllPages<CompanyDTO>(COMPANIES_PATH, {}, { signal, maxPages: 100 }),
    }),
    ...QUERY_POLICY.masterData,
    refetchOnWindowFocus: true,
});

/**
 * The owner company from the tab's cache, as the legacy record (one read per stale time for the three
 * pages). `{ fresh: true }` reads it again (an edit form must not start from an old copy).
 */
export async function getOwnerCompany(options: { fresh?: boolean } = {}): Promise<CompanyWithId | null> {
    const dto = await getQueryClient().fetchQuery(options.fresh ? { ...ownerCompanyQueryOptions, staleTime: 0 } : ownerCompanyQueryOptions);
    return dto ? companyRecord(dto) : null;
}

/** After a company write: the owner company and any company list refetch. */
export function invalidateCompanies(client: QueryClient = getQueryClient()): Promise<void> {
    return client.invalidateQueries({ queryKey: queryKeys.companies.all() });
}

// --- writes ------------------------------------------------------------------------------------

export interface CompanySave {
    /** The stored company, or null to create it. */
    id: string | null;
    values: CompanyFormValues;
    /** New asset files, uploaded with the save (the keys embed the company id). */
    files?: Partial<Record<CompanyAsset, File>>;
}

const ASSET_PURPOSE: Record<CompanyAsset, UploadPurpose> = {
    logo: "company_logo",
    stamp: "company_stamp",
    signature: "company_signature",
};

async function saveOnGo({ id, values, files = {} }: CompanySave): Promise<string> {
    const body = companyInputFromForm(values);
    let companyId = id;
    if (!companyId) {
        const created = await goFetch<CompanyDTO>(COMPANIES_PATH, { method: "POST", body });
        companyId = created.id;
    }
    const keys: Record<string, string> = {};
    for (const [asset, file] of Object.entries(files) as [CompanyAsset, File | undefined][]) {
        if (file) keys[`${asset}Key`] = await uploadToGo(file, { purpose: ASSET_PURPOSE[asset], entityId: companyId });
    }
    if (id) await goFetch(goPath`/v1/companies/${companyId}`, { method: "PATCH", body: { ...body, ...keys } });
    else if (Object.keys(keys).length > 0) await goFetch(goPath`/v1/companies/${companyId}`, { method: "PATCH", body: keys });
    return companyId;
}

/** Creates or updates a company with its staged asset files; returns its id. */
export const saveCompany = domainMutationFn<string, CompanySave>("masterdata", {
    firestore: async (save) => (await firestore()).saveCompanyFirestore(save),
    go: saveOnGo,
});
