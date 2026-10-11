// T25 (issue #45): with the `masterdata` web flag off the master-data pages keep working on Firestore,
// through the `*.firestore.ts` adapters that map the stored documents to the Go DTOs (developer-spec.md
// §10.6 "Domain adapter"), so one page renders both sources. The lists no longer stop at 100 rows on
// this path either, and a driver login is still created through `POST /v1/users` (users are
// PostgreSQL's from P0): the Firestore driver is resolved to its PostgreSQL id by `legacyDocId`.
import React from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const store = vi.hoisted(() => ({
    collections: {} as Record<string, Array<{ id: string; data: Record<string, unknown> }>>,
    reads: [] as string[],
    callables: [] as Array<{ name: string; data: unknown }>,
    updates: [] as Array<{ name: string; id: string; data: Record<string, unknown> }>,
}));

vi.mock("@/firebase/client", () => ({ db: {}, auth: {}, functions: {}, storage: {} }));
vi.mock("firebase/storage", () => ({ ref: vi.fn(), uploadBytes: vi.fn(), getDownloadURL: vi.fn() }));
vi.mock("firebase/functions", () => ({
    httpsCallable: (_f: unknown, name: string) => async (data: unknown) => {
        store.callables.push({ name, data });
        return { data: { driverId: "fs-new" } };
    },
}));
vi.mock("firebase/firestore", () => {
    const docsOf = (name: string) => (store.collections[name] ?? []).map((d) => ({ id: d.id, data: () => d.data }));
    return {
        collection: (_db: unknown, name: string) => ({ name }),
        doc: (_db: unknown, name: string, id: string) => ({ name, id }),
        query: (ref: { name: string }) => ref,
        where: () => ({}),
        orderBy: () => ({}),
        limit: () => ({}),
        getDocs: async (ref: { name: string }) => {
            store.reads.push(ref.name);
            const docs = docsOf(ref.name);
            return { docs, empty: docs.length === 0, forEach: (fn: (d: unknown) => void) => docs.forEach(fn), size: docs.length };
        },
        getDoc: async (ref: { name: string; id: string }) => {
            const d = (store.collections[ref.name] ?? []).find((x) => x.id === ref.id);
            return { id: ref.id, exists: () => Boolean(d), data: () => d?.data };
        },
        updateDoc: async (ref: { name: string; id: string }, data: Record<string, unknown>) => {
            store.updates.push({ name: ref.name, id: ref.id, data });
        },
    };
});

const { getQueryClient, resetQueryClientForTests } = await import("@/lib/queryClient");
const { useDriverPages } = await import("@/features/drivers/api/useDrivers");
const { createDriverLogin, driverQuery, driverFormValues, driverBodyFromForm, updateDriver } = await import("@/features/drivers/api/drivers");
const { useAssignmentHistory, useTruckPages } = await import("@/features/trucks/api/useTrucks");
const { truckQuery, truckFormValues, truckBodyFromForm, storedFileKeys, truckFilePreviewUrls } = await import("@/features/trucks/api/trucks");
const { customerQuery, customerFormValues, customerInputFromForm } = await import("@/features/customers/api/customers");
const { subcontractorQuery, subcontractorFormValues } = await import("@/features/subcontractors/api/subcontractors");

const calls: Array<{ method: string; url: string; body?: unknown }> = [];

beforeEach(() => {
    calls.length = 0;
    store.reads = [];
    store.callables = [];
    store.updates = [];
    store.collections = {};
    resetQueryClientForTests();
    vi.stubGlobal(
        "fetch",
        vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
            const url = String(input);
            const method = (init?.method ?? "GET").toUpperCase();
            calls.push({ method, url, body: typeof init?.body === "string" ? JSON.parse(init.body) : undefined });
            if (url === "/api/go/v1/config/web-flags") return new Response(JSON.stringify({ data: { domains: { masterdata: "firebase" } } }), { status: 200 });
            if (url.startsWith("/api/go/v1/drivers?")) {
                return new Response(
                    JSON.stringify({ data: [{ id: "pg-1", legacyDocId: "fs-1" }, { id: "pg-2", legacyDocId: "fs-2" }, { id: "pg-3", legacyDocId: "fs-3", tenantId: "carrier-a" }] }),
                    { status: 200 }
                );
            }
            if (url === "/api/go/v1/users" && method === "POST") return new Response(JSON.stringify({ data: { user: { id: "u-1", email: "a@b.c" }, temporaryPassword: "Tmp-9" } }), { status: 201 });
            throw new Error(`unexpected ${method} ${url}`);
        })
    );
});

afterEach(() => vi.unstubAllGlobals());

function wrapper({ children }: { children: React.ReactNode }) {
    return <QueryClientProvider client={getQueryClient()}>{children}</QueryClientProvider>;
}

describe("masterdata flag off: the Firestore adapters", () => {
    it("drivers: every document (no 100-row cap), legacy statuses mapped, counts like the API's meta", async () => {
        store.collections.drivers = Array.from({ length: 150 }, (_, i) => ({
            id: `fs-${i}`,
            data: { firstName: `F${i}`, lastName: "L", mobile: `08${i}`, status: i < 10 ? "On-Duty" : i < 20 ? "Inactive" : "Active", employmentType: "FULL_TIME" },
        }));
        const { result } = renderHook(() => useDriverPages({ status: "", q: "" }), { wrapper });
        await waitFor(() => expect(result.current.drivers).toHaveLength(150));
        expect(result.current.hasNextPage).toBe(false);
        expect(result.current.statusCounts).toEqual({ active: 130, on_duty: 10, inactive: 10 });
        expect(result.current.drivers[0]).toMatchObject({ id: "fs-0", status: "on_duty", employmentType: "full_time", legacyDocId: "fs-0" });
        expect(store.reads).toEqual(["drivers"]);
    });

    it("trucks: every document, legacy statuses and payment methods in the API vocabulary", async () => {
        store.collections.trucks = Array.from({ length: 120 }, (_, i) => ({
            id: `tr-${i}`,
            data: { licensePlate: `กข-${i}`, ownershipType: i % 2 ? "subcontractor" : "own", subcontractorId: i % 2 ? "sub-1" : undefined, truckStatus: i === 0 ? "Available" : "active", type: "6 Wheels", year: "2020" },
        }));
        const { result } = renderHook(() => useTruckPages({}, { poll: true }), { wrapper });
        await waitFor(() => expect(result.current.trucks).toHaveLength(120));
        expect(result.current.trucks[0]).toMatchObject({ id: "tr-0", status: "active", legacyStatus: null, ownershipType: "own", tenantId: "", year: 2020 });
        expect(result.current.trucks[1]).toMatchObject({ ownershipType: "subcontractor", tenantId: "sub-1" });
        const own = renderHook(() => useTruckPages({ ownership: "own" }), { wrapper });
        await waitFor(() => expect(own.result.current.trucks).toHaveLength(60));
    });

    it("a stored truck round-trips through the form: statuses, payment, files", async () => {
        store.collections.trucks = [
            {
                id: "tr-1",
                data: {
                    licensePlate: "กข-1", province: "กทม", truckStatus: "insurance-claim", brand: "Isuzu", model: "FRR", year: "2021", color: "W", type: "6 Wheels",
                    paymentMethod: "Company Credit", taxExpense: 1500, insurancePremium: 20000, maxLoadWeight: 6000, imageFrontRight: "https://s/fr.png",
                    insuranceDocuments: ["https://s/ins1.pdf"], statusHistory: [{ status: "Tax Renewed", date: "2026-01-01T00:00:00Z", changedBy: "Admin", notes: "Expiry: x" }],
                },
            },
        ];
        const dto = (await getQueryClient().fetchQuery(truckQuery("tr-1")))!;
        expect(dto).toMatchObject({ status: "insurance_claim", paymentMethod: "company_credit", taxExpenseThb: 1500, insurancePremiumThb: 20000, maxLoadWeightKg: 6000 });
        expect(dto.statusHistory[0]).toMatchObject({ status: "Tax Renewed", changedByLabel: "Admin", reason: "Expiry: x" });
        const values = truckFormValues(dto);
        expect(values).toMatchObject({ truckStatus: "insurance-claim", paymentMethod: "Company Credit", imageFrontRight: "https://s/fr.png", insuranceDocuments: ["https://s/ins1.pdf"], year: "2021" });
        // On Firestore a file's key is its URL: the form's refs are the stored URLs, as before.
        expect(storedFileKeys(dto.files)).toMatchObject({ imageFrontRight: ["https://s/fr.png"], insuranceDocument: ["https://s/ins1.pdf"] });
        expect(truckFilePreviewUrls(dto.files)).toMatchObject({ "https://s/fr.png": "https://s/fr.png" });
        expect(truckBodyFromForm(values as never)).toMatchObject({ status: "insurance_claim", year: 2021, maxLoadWeightKg: 6000, insurancePremiumThb: 20000 });
    });

    it("customers and carriers map to the Go DTOs and back to their forms", async () => {
        store.collections.customers = [{ id: "c-1", data: { code: "CJ", name: "CJ", branchType: "สาขา", branchNumber: "00002", paymentTermsDays: 45, driverIdTypes: [{ key: "appId", label: "App" }] } }];
        store.collections.subcontractors = [{ id: "s-1", data: { name: "Carrier", type: "individual", idCardNumber: "1234567890123", documents: ["https://s/doc.pdf"], status: "pending" } }];
        const c = (await getQueryClient().fetchQuery(customerQuery("c-1")))!;
        expect(c).toMatchObject({ branchType: "branch", branchNumber: "00002", paymentTermsDays: 45, billingDateBasis: "delivered" });
        const form = customerFormValues(c);
        expect(form.branchType).toBe("สาขา");
        expect(customerInputFromForm(form)).toMatchObject({ code: "CJ", branchType: "branch", branchNumber: "00002", driverIdTypes: [{ key: "appId", label: "App" }] });
        const s = (await getQueryClient().fetchQuery(subcontractorQuery("s-1")))!;
        expect(subcontractorFormValues(s)).toMatchObject({ name: "Carrier", type: "individual", idCardNumber: "1234567890123", status: "pending", documents: ["https://s/doc.pdf"] });
    });

    it("a driver login: the Firestore driver is resolved to its PostgreSQL id, then POST /v1/users", async () => {
        store.collections.drivers = [{ id: "fs-2", data: { firstName: "A", lastName: "B", fullNameTh: "เอ บี", mobile: "0812345678", status: "Active", employmentType: "FULL_TIME" } }];
        const d = (await getQueryClient().fetchQuery(driverQuery("fs-2")))!;
        const result = await createDriverLogin({ driver: d, email: "a@b.c" });
        expect(result.temporaryPassword).toBe("Tmp-9");
        const go = calls.filter((c) => c.url.startsWith("/api/go/v1/") && !c.url.includes("web-flags"));
        expect(go.map((c) => `${c.method} ${c.url}`)).toEqual(["GET /api/go/v1/drivers?q=0812345678&limit=100", "POST /api/go/v1/users"]);
        expect(go[1].body).toEqual({ email: "a@b.c", displayName: "เอ บี", role: "driver", driverId: "pg-2" });
        // No Firebase login callable was used.
        expect(store.callables).toEqual([]);
    });

    it("a carrier driver's login goes to the tenant of the resolved PostgreSQL driver", async () => {
        store.collections.drivers = [{ id: "fs-3", data: { firstName: "C", lastName: "D", mobile: "0899999999", status: "Active", employmentType: "SUBCONTRACTOR", subcontractorId: "s-legacy" } }];
        const d = (await getQueryClient().fetchQuery(driverQuery("fs-3")))!;
        await createDriverLogin({ driver: d, email: "c@d.e" });
        const post = calls.find((c) => c.method === "POST" && c.url === "/api/go/v1/users");
        // The Firestore subcontractor id is not a tenant: the Go driver's tenant is used.
        expect(post?.body).toEqual({ email: "c@d.e", displayName: "C D", role: "driver", driverId: "pg-3", tenantId: "carrier-a" });
    });

    it("a profile save never makes the legacy callable create a Firebase account (T25 review)", async () => {
        // updateDriverAccount provisions an account (password: the mobile digits) when it gets an email
        // for a driver without authId: the email is written to the document directly instead.
        store.collections.drivers = [
            { id: "fs-5", data: { firstName: "A", lastName: "B", fullNameTh: "เอ", mobile: "0812345678", email: "old@x.th", status: "Active", employmentType: "FULL_TIME" } },
        ];
        const d = (await getQueryClient().fetchQuery(driverQuery("fs-5")))!;
        const values = { ...driverFormValues(d), email: "p@x.th", truckLicenseId: "12345678" };
        await updateDriver({ id: "fs-5", values, files: { profile: null, idCard: null, license: null }, customerIdByCode: new Map(), previous: d });
        expect(store.callables).toHaveLength(1);
        const sent = store.callables[0].data as { id: string; updates: Record<string, unknown> };
        expect(sent.id).toBe("fs-5");
        expect(sent.updates).not.toHaveProperty("email");
        expect(sent.updates).toMatchObject({ truckLicenseId: "12345678", mobile: "0812345678" });
        expect(store.updates).toEqual([{ name: "drivers", id: "fs-5", data: { email: "p@x.th" } }]);

        // An unchanged email is not rewritten.
        store.callables = [];
        store.updates = [];
        store.collections.drivers[0].data.email = "p@x.th";
        await updateDriver({ id: "fs-5", values, files: { profile: null, idCard: null, license: null }, customerIdByCode: new Map(), previous: d });
        expect((store.callables[0].data as { updates: Record<string, unknown> }).updates).not.toHaveProperty("email");
        expect(store.updates).toEqual([]);

        // A driver that has an account keeps the legacy sync of the account's address.
        store.callables = [];
        store.collections.drivers[0].data.authId = "fb-uid";
        await updateDriver({ id: "fs-5", values: { ...values, email: "new@x.th" }, files: { profile: null, idCard: null, license: null }, customerIdByCode: new Map(), previous: d });
        expect((store.callables[0].data as { updates: Record<string, unknown> }).updates).toMatchObject({ email: "new@x.th" });
        expect(store.updates).toEqual([]);
    });

    it("the driver form maps statuses, employment and customer ids by code", async () => {
        store.collections.drivers = [
            { id: "fs-3", data: { firstName: "A", lastName: "B", fullNameTh: "ก", mobile: "08", status: "On-Duty", employmentType: "SUBCONTRACTOR", subcontractorId: "s-1", subcontractorName: "Carrier", customerDriverIds: { SPX: { appId: "77" } } } },
        ];
        const d = (await getQueryClient().fetchQuery(driverQuery("fs-3")))!;
        const values = driverFormValues(d);
        expect(values).toMatchObject({ status: "On-Duty", employmentType: "SUBCONTRACTOR", subcontractorId: "s-1", customerDriverIds: { SPX: { appId: "77" } } });
        const body = driverBodyFromForm(values, new Map([["SPX", "cust-spx"]]));
        expect(body).toMatchObject({ status: "on_duty", employmentType: "subcontractor", customerDriverIds: [{ customerId: "cust-spx", idTypeKey: "appId", value: "77" }] });
    });

    it("a load-more on the Firestore path has nothing more to fetch", async () => {
        store.collections.drivers = [{ id: "fs-9", data: { firstName: "A", lastName: "B", mobile: "08", status: "Active" } }];
        const { result } = renderHook(() => useDriverPages({ status: "active", q: "" }), { wrapper });
        await waitFor(() => expect(result.current.drivers).toHaveLength(1));
        await act(() => result.current.fetchNextPage());
        expect(result.current.drivers).toHaveLength(1);
    });

    it("assignment history: the status and the search filter every document, as Go's ?status&q do (TW8)", async () => {
        const at = (i: number) => ({ toDate: () => new Date(Date.UTC(2026, 9, 1) - i * 60_000) });
        store.collections.truckAssignment = Array.from({ length: 150 }, (_, i) => ({
            id: `as-${i}`,
            data: {
                truckId: `tr-${i}`, truckPlate: `กข-${1000 + i}`, truckModel: i === 7 ? "FRR Special" : "FRR", driverId: `fs-${i}`,
                driverName: `Driver ${i}`, status: i % 2 ? "revoked" : "active", adminName: i === 3 ? "Night Desk" : "Admin", createdAt: at(i),
            },
        }));
        const all = renderHook(() => useAssignmentHistory({ status: "", q: "" }), { wrapper });
        await waitFor(() => expect(all.result.current.rows).toHaveLength(150));
        expect(all.result.current.rows[0]).toMatchObject({ id: "as-0", truckPlate: "กข-1000", adminLabel: "Admin" });
        expect(all.result.current.hasNextPage).toBe(false);

        const cases: Array<[{ status: "" | "active" | "revoked"; q: string }, string[]]> = [
            [{ status: "active", q: "" }, Array.from({ length: 75 }, (_, i) => `as-${i * 2}`)],
            [{ status: "", q: "กข-1149" }, ["as-149"]],
            [{ status: "", q: " special " }, ["as-7"]],
            [{ status: "", q: "NIGHT" }, ["as-3"]],
            [{ status: "", q: "driver 14" }, ["as-14", ...Array.from({ length: 10 }, (_, i) => `as-${140 + i}`)]],
            [{ status: "revoked", q: "driver 14" }, ["as-141", "as-143", "as-145", "as-147", "as-149"]],
        ];
        for (const [filter, ids] of cases) {
            const { result } = renderHook(() => useAssignmentHistory(filter), { wrapper });
            await waitFor(() => expect(result.current.rows.map((r) => r.id)).toEqual(ids));
        }
    });
});
