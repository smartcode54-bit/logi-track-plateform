// TW4 (developer-spec.md §10.7; Appendix E §E.4): customers, subcontractors and the owner company are
// read once per stale time for the whole tab, whichever page or dialog asks, and their writes
// invalidate the cached copy.
import { beforeEach, describe, expect, it, vi } from "vitest";

const store = vi.hoisted(() => ({ reads: [] as string[], writes: [] as string[] }));

vi.mock("@/firebase/client", () => ({ db: {}, auth: {}, functions: {}, storage: {} }));
vi.mock("firebase/storage", () => ({ ref: vi.fn(), uploadBytes: vi.fn(), getDownloadURL: vi.fn() }));
vi.mock("firebase/firestore", () => {
    const snapshot = (name: string) => {
        const docs =
            name === "customers"
                ? [{ id: "c1", data: () => ({ code: "CJ", name: "CJ Express" }) }]
                : name === "subcontractors"
                  ? [{ id: "s1", data: () => ({ name: "Carrier A" }) }]
                  : [{ id: "co1", data: () => ({ nameTh: "บริษัท", companyType: "owner" }) }];
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

const { getCustomers, updateCustomer, customersQueryOptions } = await import("./customers");
const { getSubcontractors, updateSubcontractor } = await import("@/features/subcontractors/services/subcontractorService");
const { getOwnerCompany, updateCompany } = await import("@/features/companies/api/companies");
const { resetQueryClientForTests } = await import("@/lib/queryClient");

beforeEach(() => {
    store.reads = [];
    store.writes = [];
    resetQueryClientForTests();
});

describe("cached master data", () => {
    it("['customers']: fifteen callers, one read; a write refreshes it", async () => {
        expect(customersQueryOptions.queryKey).toEqual(["customers"]);
        expect(customersQueryOptions.staleTime).toBe(600_000);
        const lists = await Promise.all(Array.from({ length: 15 }, () => getCustomers()));
        expect(lists[14][0]).toMatchObject({ id: "c1", code: "CJ" });
        expect(store.reads).toEqual(["customers"]);
        await updateCustomer("c1", { name: "CJ" });
        await getCustomers();
        expect(store.reads).toEqual(["customers", "customers"]);
    });

    it("['subcontractors']: one read for the truck and driver forms; a write refreshes it", async () => {
        await getSubcontractors();
        await getSubcontractors();
        expect(store.reads).toEqual(["subcontractors"]);
        await updateSubcontractor("s1", { name: "Carrier B" } as never);
        await getSubcontractors();
        expect(store.reads).toEqual(["subcontractors", "subcontractors"]);
    });

    it("['companies',{owner:true}]: shared by three pages; the profile form reads fresh", async () => {
        await getOwnerCompany();
        await getOwnerCompany();
        expect(store.reads).toEqual(["companies"]);
        await getOwnerCompany({ fresh: true });
        expect(store.reads).toEqual(["companies", "companies"]);
        await updateCompany("co1", { nameTh: "บริษัท" } as never);
        await getOwnerCompany();
        expect(store.reads).toEqual(["companies", "companies", "companies"]);
    });
});
