/**
 * Customers for the web (T25; developer-spec.md §10.6, §10.7, §10.13; Appendix B §B.2.10 as built by
 * T20; Appendix E §E.2.4, §E.4 row "customers", §E.6).
 *
 * Keys (one family, `['customers', …]`, so `customers.changed` and every customer write reach all of
 * them with one prefix invalidation):
 * - `['customers', {q}]` (infinite): the customers page, `GET /v1/customers?q&cursor` (full record,
 *   `fleet:manage_customers`), server search, keyset by code.
 * - `['customers','detail',id]`: `GET /v1/customers/{id}`.
 * - `['customers','options']`: every customer as `{id, billingPartyId, code, name}`
 *   (`GET /v1/customers?fields=minimal`, all pages) for the master-data pickers (hub dialog, hub
 *   import, sources filter).
 * - `['customers']` (exact): the legacy `CustomerData[]` of `getCustomers()`, whose callers outside
 *   the master-data pages (billing, income, rate card, task dialogs, trip editor) match Firestore ids
 *   on Firestore trips, tasks and rate tables; it stays on Firestore until those pages move (P2, P3).
 *
 * Source per fetch and per write: flag `masterdata` (`domainQueryFn`, `domainMutationFn`). The
 * Firestore side is `./customers.firestore.ts`, loaded with `import()` only on that path, so with the
 * flag on the customer pages load no Firebase module (T25 acceptance; `masterdata.firebase.test.ts`).
 */
import { infiniteQueryOptions, queryOptions, type QueryClient } from "@tanstack/react-query";
import { goFetch, goPath } from "@/lib/goFetch";
import { fetchAllPages } from "@/lib/goPages";
import { goInfiniteQueryFn, goNextPageParam, type GoPage } from "@/lib/goQuery";
import { uploadToGo } from "@/lib/goUpload";
import { getQueryClient } from "@/lib/queryClient";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
import { domainMutationFn, domainQueryFn } from "@/features/platform/api/webFlags";
import type { Customer } from "@/validate/customerSchema";

const firestore = () => import("./customers.firestore");

export const CUSTOMERS_PATH = "/v1/customers";
export const CUSTOMERS_PAGE_SIZE = 50;

/** Legacy Firestore shape of the `['customers']` list (callers outside the master-data pages). */
export interface CustomerData extends Customer {
    id: string;
}

export type CustomerBranchType = "hq" | "branch";
export type BillingDateBasis = "delivered" | "plan";

/** One driver id type of a customer (`customer_driver_id_types`). */
export interface DriverIdTypeDTO {
    key: string;
    label: string;
}

/** `GET /v1/customers/{id}` and the full list item (Appendix B §B.2.10, internal/parties `Customer`). */
export interface CustomerDTO {
    id: string;
    billingPartyId: string | null;
    code: string;
    name: string;
    description: string | null;
    /** Private object: the key goes back in a PATCH, the URL is a short-lived presigned GET. */
    logoKey: string | null;
    logoUrl: string | null;
    address: string | null;
    taxId: string | null;
    branchType: CustomerBranchType | null;
    branchNumber: string | null;
    contactName: string | null;
    contactPhone: string | null;
    billingEmail: string | null;
    paymentTermsDays: number | null;
    invoiceNote: string | null;
    lineGroupId: string | null;
    billingDateBasis: BillingDateBasis;
    driverIdTypes: DriverIdTypeDTO[];
    createdAt: string | null;
    updatedAt: string | null;
}

/** `GET /v1/customers?fields=minimal`. */
export interface CustomerMinimalDTO {
    id: string;
    billingPartyId: string | null;
    code: string;
    name: string;
}

/** The body of `POST /v1/customers` and `PATCH /v1/customers/{id}` (no file: `logoKey` is separate). */
export interface CustomerInput {
    code: string;
    name: string;
    description: string | null;
    address: string | null;
    taxId: string | null;
    branchType: CustomerBranchType | null;
    branchNumber: string | null;
    contactName: string | null;
    contactPhone: string | null;
    billingEmail: string | null;
    paymentTermsDays: number | null;
    invoiceNote: string | null;
    lineGroupId: string | null;
    billingDateBasis: BillingDateBasis;
    driverIdTypes: DriverIdTypeDTO[];
}

/** A save of the customer form: the record, a new logo file, or the logo removed. */
export interface CustomerSave {
    input: CustomerInput;
    logo?: File | null;
    removeLogo?: boolean;
}

// --- form <-> API ------------------------------------------------------------------------------

const BRANCH_FROM_FORM: Record<string, CustomerBranchType> = { สำนักงานใหญ่: "hq", สาขา: "branch" };
const BRANCH_TO_FORM: Record<CustomerBranchType, NonNullable<Customer["branchType"]>> = { hq: "สำนักงานใหญ่", branch: "สาขา" };

const blank = (v: string | null | undefined): string | null => {
    const s = (v ?? "").trim();
    return s === "" ? null : s;
};

/** The API body of the customer form (`validate/customerSchema.ts` keeps the Thai branch labels). */
export function customerInputFromForm(values: Customer): CustomerInput {
    const branchType = values.branchType ? (BRANCH_FROM_FORM[values.branchType] ?? null) : null;
    return {
        code: values.code.trim().toUpperCase(),
        name: values.name.trim(),
        description: blank(values.description),
        address: blank(values.address),
        taxId: blank(values.taxId),
        branchType,
        branchNumber: branchType === "branch" ? blank(values.branchNumber) : null,
        contactName: blank(values.contactName),
        contactPhone: blank(values.contactPhone),
        billingEmail: blank(values.billingEmail),
        paymentTermsDays: values.paymentTermsDays ?? null,
        invoiceNote: blank(values.invoiceNote),
        lineGroupId: blank(values.lineGroupId),
        billingDateBasis: values.billingDateBasis ?? "delivered",
        driverIdTypes: (values.driverIdTypes ?? [])
            .map((d) => ({ key: d.key.trim(), label: d.label.trim() }))
            .filter((d) => d.key !== ""),
    };
}

/** The customer form's values for a stored customer. */
export function customerFormValues(c: CustomerDTO): Customer {
    return {
        code: c.code,
        name: c.name,
        description: c.description ?? "",
        logoUrl: c.logoUrl ?? "",
        driverIdTypes: c.driverIdTypes ?? [],
        address: c.address ?? "",
        taxId: c.taxId ?? "",
        branchType: c.branchType ? BRANCH_TO_FORM[c.branchType] : undefined,
        branchNumber: c.branchNumber ?? "",
        contactName: c.contactName ?? "",
        contactPhone: c.contactPhone ?? "",
        billingEmail: c.billingEmail ?? "",
        paymentTermsDays: c.paymentTermsDays ?? undefined,
        invoiceNote: c.invoiceNote ?? "",
        billingDateBasis: c.billingDateBasis ?? "delivered",
        lineGroupId: c.lineGroupId ?? "",
    };
}

/** The label a picker shows: `CODE - Name`. */
export function customerLabel(c: Pick<CustomerMinimalDTO, "code" | "name">): string {
    return [c.code, c.name].filter(Boolean).join(" - ");
}

// --- reads -------------------------------------------------------------------------------------

export interface CustomerListFilter {
    q: string;
}

export function customersListKey(filter: CustomerListFilter) {
    return queryKeys.customers.list({ q: filter.q.trim() });
}

type CustomersListKey = ReturnType<typeof customersListKey>;

/** `['customers', {q}]`: the customers page, keyset by code, server search (Go) or the whole list (Firestore). */
export function customersListQuery(filter: CustomerListFilter) {
    return infiniteQueryOptions({
        queryKey: customersListKey(filter),
        queryFn: domainQueryFn<GoPage<CustomerDTO>, CustomersListKey, string | undefined>("masterdata", {
            firestore: async ({ queryKey: [, p] }) => ({ data: await (await firestore()).listCustomerDTOs(p?.q ?? "") }),
            go: goInfiniteQueryFn<CustomerDTO, CustomersListKey>(CUSTOMERS_PATH, {
                query: ([, p]) => ({ q: p?.q || undefined, limit: CUSTOMERS_PAGE_SIZE }),
            }),
        }),
        initialPageParam: undefined as string | undefined,
        getNextPageParam: goNextPageParam,
        ...QUERY_POLICY.masterData,
        refetchOnWindowFocus: true,
    });
}

/** `['customers','detail',id]`. */
export function customerQuery(id: string) {
    return queryOptions({
        queryKey: queryKeys.customers.detail(id),
        queryFn: domainQueryFn<CustomerDTO | null>("masterdata", {
            firestore: async () => (await firestore()).getCustomerDTO(id),
            go: ({ signal }) => goFetch<CustomerDTO>(goPath`/v1/customers/${id}`, { signal }),
        }),
        ...QUERY_POLICY.entity,
        enabled: id !== "",
    });
}

/** `['customers','options']`: every customer for a master-data picker. */
export const customerOptionsQuery = queryOptions({
    queryKey: queryKeys.customers.options(),
    queryFn: domainQueryFn<CustomerMinimalDTO[]>("masterdata", {
        firestore: async () => (await firestore()).listCustomerOptions(),
        go: ({ signal }) => fetchAllPages<CustomerMinimalDTO>(CUSTOMERS_PATH, { fields: "minimal" }, { signal, maxPages: 100 }),
    }),
    ...QUERY_POLICY.masterData,
    refetchOnWindowFocus: true,
});

/**
 * `['customers']` (exact): the legacy list of `getCustomers()` (15 callers outside the master-data
 * pages, Appendix E §E.4). Firestore only, see the module comment.
 */
export const customersQueryOptions = queryOptions({
    queryKey: queryKeys.customers.all(),
    queryFn: async (): Promise<CustomerData[]> => (await firestore()).fetchCustomersFromFirestore(),
    ...QUERY_POLICY.masterData,
    refetchOnWindowFocus: true,
});

/** Every customer by code, from the tab's `['customers']` cache. Shared: never mutate it in place. */
export function getCustomers(): Promise<CustomerData[]> {
    return getQueryClient().fetchQuery(customersQueryOptions);
}

/** The picker list from the tab's cache (async callers such as the hub import). */
export function fetchCustomerOptions(client: QueryClient = getQueryClient()): Promise<CustomerMinimalDTO[]> {
    return client.fetchQuery(customerOptionsQuery);
}

/** After a customer write: every `['customers', ...]` entry refetches where observed, the rest on next use. */
export function invalidateCustomers(client: QueryClient = getQueryClient()): Promise<void> {
    return client.invalidateQueries({ queryKey: queryKeys.customers.all() });
}

// --- writes ------------------------------------------------------------------------------------

async function createOnGo(save: CustomerSave): Promise<string> {
    const created = await goFetch<CustomerDTO>(CUSTOMERS_PATH, { method: "POST", body: save.input });
    if (save.logo) {
        // The key embeds the customer, so the logo follows the create (developer-spec.md §9.4).
        const logoKey = await uploadToGo(save.logo, { purpose: "customer_logo", entityId: created.id });
        await goFetch(goPath`/v1/customers/${created.id}`, { method: "PATCH", body: { logoKey } });
    }
    return created.id;
}

async function updateOnGo(id: string, save: CustomerSave): Promise<void> {
    const body: Record<string, unknown> = { ...save.input };
    if (save.logo) body.logoKey = await uploadToGo(save.logo, { purpose: "customer_logo", entityId: id });
    else if (save.removeLogo) body.logoKey = null;
    await goFetch(goPath`/v1/customers/${id}`, { method: "PATCH", body });
}

/** Creates a customer (and its billing party, on Go); returns its id. */
export const createCustomer = domainMutationFn<string, CustomerSave>("masterdata", {
    firestore: async (save) => (await firestore()).createCustomerFirestore(save),
    go: createOnGo,
});

/** Updates a customer. */
export const updateCustomer = domainMutationFn<void, { id: string } & CustomerSave>("masterdata", {
    firestore: async ({ id, ...save }) => (await firestore()).updateCustomerFirestore(id, save),
    go: ({ id, ...save }) => updateOnGo(id, save),
});
