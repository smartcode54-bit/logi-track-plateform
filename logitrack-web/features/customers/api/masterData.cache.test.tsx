// TW4 / T25 / TW7 (developer-spec.md §10.7; Appendix E §E.4): customer and carrier pickers and the owner
// company are read once per stale time for the whole tab, whichever page or dialog asks, and their writes
// invalidate the cached copy. Since TW7 Go is the only source (the Firestore adapters and the `masterdata`
// flag are gone). The writes go through the save hooks the forms use (`useSaveCustomer`,
// `useSaveSubcontractor`, `useSaveCompany`): the write functions themselves invalidate nothing, the
// hooks' `meta.invalidates` does (T25 review), so these tests fail when a hook loses its meta.
import React from "react";
import { act, renderHook } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { fetchCustomerOptions } = await import("./customers");
const { useSaveCustomer } = await import("./useCustomers");
const { fetchSubcontractorOptions } = await import("@/features/subcontractors/api/subcontractors");
const { useSaveSubcontractor } = await import("@/features/subcontractors/api/useSubcontractors");
const { getOwnerCompany } = await import("@/features/companies/api/companies");
const { useSaveCompany } = await import("@/features/companies/api/useCompanies");
const { getQueryClient, resetQueryClientForTests } = await import("@/lib/queryClient");

function wrapper({ children }: { children: React.ReactNode }) {
    return <QueryClientProvider client={getQueryClient()}>{children}</QueryClientProvider>;
}

/** Every browser request, as `METHOD path` (query string dropped). */
let calls: string[] = [];
const reads = (prefix: string) => calls.filter((c) => c.startsWith(`GET /api/go/v1/${prefix}`));
const writes = () => calls.filter((c) => !c.startsWith("GET "));

const ok = (data: unknown) => new Response(JSON.stringify({ data }), { status: 200, headers: { "Content-Type": "application/json" } });

const OWNER = {
    id: "co1", tenantId: null, nameTh: "บริษัท", nameEn: null, shortName: null, taxId: "1234567890123", branchType: "headquarters",
    branchNumber: null, address: "กรุงเทพฯ", phone: null, email: null, signatoryName: null, bankName: null, accountNumber: null,
    accountName: null, withholdingTaxRate: 1, companyType: "owner", isActive: true, maxTrucks: null, maxDrivers: null,
};

beforeEach(() => {
    calls = [];
    resetQueryClientForTests();
    vi.stubGlobal(
        "fetch",
        vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
            const url = String(input);
            const method = (init.method ?? "GET").toUpperCase();
            calls.push(`${method} ${url.split("?")[0]}`);
            if (url.includes("firebase") || url.includes("web-flags")) throw new Error(`no Firebase and no web flags since TW7: ${url}`);
            if (method === "GET" && url.startsWith("/api/go/v1/customers?")) return ok([{ id: "c1", billingPartyId: null, code: "CJ", name: "CJ Express" }]);
            if (method === "GET" && url.startsWith("/api/go/v1/subcontractors?")) {
                return ok([{ id: "s1", code: null, nameTh: "Carrier A", nameEn: null, status: "active" }]);
            }
            if (method === "GET" && url === "/api/go/v1/companies/owner") return ok(OWNER);
            if (method === "PATCH") return ok({});
            throw new Error(`unexpected ${method} ${url}`);
        })
    );
});

afterEach(() => vi.unstubAllGlobals());

describe("cached master data over Go", () => {
    it("['customers','options']: shared by every caller, one read; a write through the form's hook refreshes it", async () => {
        const lists = await Promise.all(Array.from({ length: 15 }, () => fetchCustomerOptions()));
        expect(lists[14]).toEqual([{ id: "c1", billingPartyId: null, code: "CJ", name: "CJ Express" }]);
        expect(lists[14]).toBe(lists[0]);
        expect(reads("customers")).toHaveLength(1);
        const { result } = renderHook(() => useSaveCustomer(), { wrapper });
        await act(() =>
            result.current.update.mutateAsync({
                id: "c1",
                input: {
                    code: "CJ", name: "CJ", description: null, address: null, taxId: null, branchType: "branch", branchNumber: "00001",
                    contactName: null, contactPhone: null, billingEmail: null, paymentTermsDays: null, invoiceNote: null,
                    lineGroupId: null, billingDateBasis: "delivered", driverIdTypes: [],
                },
            })
        );
        expect(writes()).toEqual(["PATCH /api/go/v1/customers/c1"]);
        await fetchCustomerOptions();
        expect(reads("customers")).toHaveLength(2);
    });

    it("['subcontractors','options']: one read for the truck and driver forms; a write through the form's hook refreshes it", async () => {
        const options = await fetchSubcontractorOptions();
        await fetchSubcontractorOptions();
        expect(options).toEqual([{ id: "s1", code: null, nameTh: "Carrier A", nameEn: null, status: "active" }]);
        expect(reads("subcontractors")).toHaveLength(1);
        const { result } = renderHook(() => useSaveSubcontractor(), { wrapper });
        await act(() =>
            result.current.update.mutateAsync({ id: "s1", values: { name: "Carrier B", type: "company", taxId: "", contactPerson: "a", phone: "1" } as never })
        );
        expect(writes()).toEqual(["PATCH /api/go/v1/subcontractors/s1"]);
        await fetchSubcontractorOptions();
        expect(reads("subcontractors")).toHaveLength(2);
    });

    it("['companies',{owner:true}]: shared by three pages; the profile form reads fresh; its save refreshes it", async () => {
        const owner = await getOwnerCompany();
        await getOwnerCompany();
        expect(owner).toMatchObject({ id: "co1", nameTh: "บริษัท", companyType: "owner", branchType: "headquarters" });
        expect(reads("companies")).toHaveLength(1);
        await getOwnerCompany({ fresh: true });
        expect(reads("companies")).toHaveLength(2);
        const { result } = renderHook(() => useSaveCompany(), { wrapper });
        await act(() =>
            result.current.mutateAsync({
                id: "co1",
                values: { nameTh: "บริษัท", taxId: "1234567890123", branchType: "headquarters", address: "กรุงเทพฯ", companyType: "owner", isActive: true, withholdingTaxRate: 1 } as never,
            })
        );
        expect(writes()).toEqual(["PATCH /api/go/v1/companies/co1"]);
        await getOwnerCompany();
        expect(reads("companies")).toHaveLength(3);
    });
});
