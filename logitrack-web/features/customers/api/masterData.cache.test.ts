// TW4 / T25 (developer-spec.md §10.7; Appendix E §E.4): customers, subcontractors and the owner company
// are read once per stale time for the whole tab, whichever page or dialog asks, from Firestore while the
// `masterdata` flag is off, and their writes invalidate the cached copy.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const store = vi.hoisted(() => ({ reads: [] as string[], writes: [] as string[] }));

vi.mock("@/firebase/client", () => ({ db: {}, auth: {}, functions: {}, storage: {} }));
vi.mock("firebase/storage", () => ({ ref: vi.fn(), uploadBytes: vi.fn(), getDownloadURL: vi.fn() }));
vi.mock("firebase/firestore", () => {
    const snapshot = (name: string) => {
        const docs =
            name === "customers"
                ? [{ id: "c1", data: () => ({ code: "CJ", name: "CJ Express", branchType: "สาขา", branchNumber: "00001" }) }]
                : name === "subcontractors"
                  ? [{ id: "s1", data: () => ({ name: "Carrier A", type: "company" }) }]
                  : [{ id: "co1", data: () => ({ nameTh: "บริษัท", companyType: "owner", taxId: "1234567890123" }) }];
        return { docs, empty: docs.length === 0, forEach: (fn: (d: unknown) => void) => docs.forEach(fn) };
    };
    return {
        collection: (_db: unknown, name: string) => ({ name }),
        doc: (_db: unknown, name?: string) => ({ name: typeof name === "string" ? name : (_db as { name: string }).name, id: "new-id" }),
        query: (ref: { name: string }) => ref,
        where: () => ({}),
        orderBy: () => ({}),
        limit: () => ({}),
        getDocs: async (ref: { name: string }) => {
            store.reads.push(ref.name);
            return snapshot(ref.name);
        },
        getDoc: vi.fn(),
        setDoc: async (ref: { name: string }) => void store.writes.push(ref.name),
        updateDoc: async (ref: { name: string }) => void store.writes.push(ref.name),
        addDoc: async (ref: { name: string }) => {
            store.writes.push(ref.name);
            return { id: "new-id" };
        },
        serverTimestamp: () => "now",
        Timestamp: { now: () => "now" },
    };
});

const { getCustomers, updateCustomer, customersQueryOptions, fetchCustomerOptions } = await import("./customers");
const { fetchSubcontractorOptions, updateSubcontractor } = await import("@/features/subcontractors/api/subcontractors");
const { getOwnerCompany, saveCompany, invalidateCompanies } = await import("@/features/companies/api/companies");
const { getQueryClient, resetQueryClientForTests } = await import("@/lib/queryClient");
const { queryKeys } = await import("@/lib/queryKeys");

beforeEach(() => {
    store.reads = [];
    store.writes = [];
    resetQueryClientForTests();
    // Every domain on Firestore (flags without `masterdata: go`).
    vi.stubGlobal(
        "fetch",
        vi.fn(async (input: RequestInfo | URL) => {
            if (String(input) === "/api/go/v1/config/web-flags") return new Response(JSON.stringify({ data: { domains: {} } }), { status: 200 });
            throw new Error(`unexpected ${String(input)}`);
        })
    );
});

afterEach(() => vi.unstubAllGlobals());

describe("cached master data (masterdata flag off)", () => {
    it("['customers']: fifteen callers, one read; a write refreshes it", async () => {
        expect(customersQueryOptions.queryKey).toEqual(["customers"]);
        expect(customersQueryOptions.staleTime).toBe(600_000);
        const lists = await Promise.all(Array.from({ length: 15 }, () => getCustomers()));
        expect(lists[14][0]).toMatchObject({ id: "c1", code: "CJ" });
        expect(store.reads).toEqual(["customers"]);
        await updateCustomer({
            id: "c1",
            input: {
                code: "CJ", name: "CJ", description: null, address: null, taxId: null, branchType: "branch", branchNumber: "00001",
                contactName: null, contactPhone: null, billingEmail: null, paymentTermsDays: null, invoiceNote: null,
                lineGroupId: null, billingDateBasis: "delivered", driverIdTypes: [],
            },
        });
        expect(store.writes).toEqual(["customers"]);
        await getQueryClient().invalidateQueries({ queryKey: queryKeys.customers.all() });
        await getCustomers();
        expect(store.reads).toEqual(["customers", "customers"]);
    });

    it("['customers','options']: the picker list maps the Thai branch label away and is shared", async () => {
        const [a, b] = await Promise.all([fetchCustomerOptions(), fetchCustomerOptions()]);
        expect(a).toEqual([{ id: "c1", billingPartyId: null, code: "CJ", name: "CJ Express" }]);
        expect(b).toBe(a);
        expect(store.reads).toEqual(["customers"]);
    });

    it("['subcontractors','options']: one read for the truck and driver forms", async () => {
        const options = await fetchSubcontractorOptions();
        await fetchSubcontractorOptions();
        expect(options).toEqual([{ id: "s1", code: null, nameTh: "Carrier A", nameEn: null, status: "active" }]);
        expect(store.reads).toEqual(["subcontractors"]);
        await updateSubcontractor({ id: "s1", values: { name: "Carrier B", type: "company", taxId: "", contactPerson: "a", phone: "1" } as never });
        expect(store.writes).toEqual(["subcontractors"]);
    });

    it("['companies',{owner:true}]: shared by three pages; the profile form reads fresh", async () => {
        const owner = await getOwnerCompany();
        await getOwnerCompany();
        expect(owner).toMatchObject({ id: "co1", nameTh: "บริษัท", companyType: "owner", branchType: "headquarters" });
        expect(store.reads).toEqual(["companies"]);
        await getOwnerCompany({ fresh: true });
        expect(store.reads).toEqual(["companies", "companies"]);
        await saveCompany({ id: "co1", values: { nameTh: "บริษัท" } as never });
        await invalidateCompanies();
        await getOwnerCompany();
        expect(store.reads).toEqual(["companies", "companies", "companies"]);
    });
});
