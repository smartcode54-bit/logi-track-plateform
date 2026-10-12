// T25 (issue #45), TW7: the master-data hooks read and write through the BFF (`/api/go/v1/...`) only:
// TW7 removed the Firestore adapters, the Firebase SDK and the `masterdata` web flag, so no request
// goes anywhere else (the fake fetch below refuses every unknown route, the web flags included).
//
// Acceptance covered here: hubs and customers are fetched once per stale time whoever asks; the drivers
// and trucks lists page past 100 rows (keyset `nextCursor`) and poll every 60 s while visible; driver
// logins are created through `POST /v1/users`; files go through presign + PUT + the entity write.
import React from "react";
import { readdirSync, readFileSync, statSync } from "fs";
import path from "path";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClientProvider, useQuery } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { getQueryClient, resetQueryClientForTests } = await import("@/lib/queryClient");
const { useCustomerOptions } = await import("@/features/customers/api/useCustomers");
const { fetchCustomerOptions, createCustomer } = await import("@/features/customers/api/customers");
const { fetchHubsCached, createHub, hubsQueryOptions } = await import("@/features/hubs/api/hubs");
const useHubs = () => useQuery(hubsQueryOptions);
const { useDriverPages, useCreateDriverLogin } = await import("@/features/drivers/api/useDrivers");
const { createDriverLogin, updateDriver, driverFormValues } = await import("@/features/drivers/api/drivers");
const { useTruckPages } = await import("@/features/trucks/api/useTrucks");
const { createTruck, renewTruck, updateTruck, truckFormValues, storedFileKeys, truckFilePreviewUrls, previewOfRef } = await import(
    "@/features/trucks/api/trucks"
);
const { assignTruck, revokeAssignment } = await import("@/features/trucks/api/truckAssignments");
const { createSubcontractor } = await import("@/features/subcontractors/api/subcontractors");
const { saveCompany, getOwnerCompany } = await import("@/features/companies/api/companies");
const { POLL_INTERVAL_MS } = await import("@/lib/realtime/polling");

interface Call {
    method: string;
    url: string;
    body?: unknown;
}

let calls: Call[] = [];
let routes: Array<{ method: string; match: (url: string) => boolean; reply: (url: string, body: unknown) => Response }> = [];

const json = (data: unknown, extra: Record<string, unknown> = {}, status = 200) =>
    new Response(JSON.stringify({ data, ...extra }), { status, headers: { "Content-Type": "application/json" } });

function route(method: string, match: string | ((url: string) => boolean), reply: (url: string, body: unknown) => Response) {
    routes.push({ method, match: typeof match === "string" ? (u) => u === match : match, reply });
}

beforeEach(() => {
    calls = [];
    routes = [];
    resetQueryClientForTests();
    vi.stubGlobal(
        "fetch",
        vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
            const url = String(input);
            const method = (init?.method ?? "GET").toUpperCase();
            const body = typeof init?.body === "string" ? JSON.parse(init.body) : init?.body;
            calls.push({ method, url, body });
            const r = routes.find((x) => x.method === method && x.match(url));
            if (!r) throw new Error(`unexpected ${method} ${url}`);
            return r.reply(url, body);
        })
    );
});

afterEach(() => vi.unstubAllGlobals());

function wrapper({ children }: { children: React.ReactNode }) {
    return <QueryClientProvider client={getQueryClient()}>{children}</QueryClientProvider>;
}

const goCalls = (prefix: string) => calls.filter((c) => c.url.startsWith(prefix));

function driver(i: number) {
    return {
        id: `d-${i}`, tenantId: "t-own", tenantNameTh: null, tenantNameEn: null, tenantKind: "own_fleet", userId: null,
        legacyDocId: null, displayName: `คนขับ ${i}`, firstName: `F${i}`, lastName: `L${i}`, fullNameTh: `คนขับ ${i}`,
        mobile: `08${String(i).padStart(8, "0")}`, email: null, status: "active", employmentType: "full_time",
        contractYears: null, employmentStart: null, employmentEnd: null, hireDate: null, probationPassed: false,
        assignToProject: null, truckLicenseId: null, licenseType: null, licenseExpiredDate: null, activeTruck: null,
        currentAssignment: null, createdAt: "2026-10-01T00:00:00Z", updatedAt: "2026-10-01T00:00:00Z",
    };
}

function truck(i: number) {
    return { id: `t-${i}`, tenantId: "t-own", ownershipType: "own", licensePlate: `กข-${1000 + i}`, province: "กรุงเทพมหานคร", status: "active", type: "6 Wheels" };
}

describe("master data reads through /api/go", () => {
    it("customers and hubs: one request per stale time for every observer and async caller", async () => {
        route("GET", (u) => u.startsWith("/api/go/v1/customers?"), () => json([{ id: "c-1", billingPartyId: "bp-1", code: "CJ", name: "CJ Express" }]));
        route("GET", "/api/go/v1/hubs", () => json([{ id: "h-1", sourceId: "ALANG-A", sourceNameTh: "อลัง", sourceNameEn: null, latitude: null, longitude: null, stationType: "HUB", linkedCustomerId: null, linkedCustomerName: null, linkedCustomerKind: null, createdByDriver: false }]));

        const a = renderHook(() => useCustomerOptions(), { wrapper });
        const b = renderHook(() => useCustomerOptions(), { wrapper });
        const h1 = renderHook(() => useHubs(), { wrapper });
        const h2 = renderHook(() => useHubs(), { wrapper });
        await waitFor(() => expect(a.result.current.options).toHaveLength(1));
        await waitFor(() => expect(h1.result.current.data).toHaveLength(1));
        await fetchCustomerOptions();
        await fetchHubsCached();
        expect(b.result.current.options[0]).toMatchObject({ code: "CJ" });
        expect(h2.result.current.data?.[0]).toMatchObject({ sourceId: "ALANG-A" });

        // A later page mounting within the stale time reads the cache.
        const c = renderHook(() => useCustomerOptions(), { wrapper });
        expect(c.result.current.options).toHaveLength(1);

        expect(goCalls("/api/go/v1/customers").map((x) => x.url)).toEqual(["/api/go/v1/customers?fields=minimal&limit=100"]);
        expect(goCalls("/api/go/v1/hubs").map((x) => x.url)).toEqual(["/api/go/v1/hubs"]);
    });

    it("hubs: the one documented exception, Sources refetches ['hubs'] on each mount (it edits hubs)", async () => {
        // developer-spec.md §10.6 (TW4) and Appendix E §E.4 (T25): every reader shares one `GET /v1/hubs`
        // per stale time, except `/app/sources`, which reads with `refetchOnMount: "always"`.
        route("GET", "/api/go/v1/hubs", () => json([{ id: "h-1", sourceId: "ALANG-A", sourceNameTh: "อลัง", sourceNameEn: null, latitude: null, longitude: null, stationType: "HUB", linkedCustomerId: null, linkedCustomerName: null, linkedCustomerKind: null, createdByDriver: false }]));
        const useSourcesHubs = () => useQuery({ ...hubsQueryOptions, refetchOnMount: "always" });
        const firstMile = renderHook(() => useHubs(), { wrapper });
        await waitFor(() => expect(firstMile.result.current.data).toHaveLength(1));
        for (let visit = 1; visit <= 2; visit++) {
            const page = renderHook(useSourcesHubs, { wrapper });
            await waitFor(() => expect(goCalls("/api/go/v1/hubs")).toHaveLength(1 + visit));
            page.unmount();
            // Any other reader within the stale time still uses the cache.
            renderHook(() => useHubs(), { wrapper }).unmount();
            await fetchHubsCached();
            expect(goCalls("/api/go/v1/hubs")).toHaveLength(1 + visit);
        }
    });

    it("drivers: keyset pages past 100 rows, server status counts, 60 s visible poll", async () => {
        route("GET", (u) => u.startsWith("/api/go/v1/drivers?"), (url) => {
            const cursor = new URL(url, "http://x").searchParams.get("cursor");
            if (!cursor) return json(Array.from({ length: 100 }, (_, i) => driver(i)), { nextCursor: "page-2", meta: { statusCounts: { active: 140, on_duty: 5, inactive: 5 } } });
            return json(Array.from({ length: 50 }, (_, i) => driver(100 + i)), { meta: { statusCounts: { active: 140, on_duty: 5, inactive: 5 } } });
        });
        const { result } = renderHook(() => useDriverPages({ status: "", q: "" }), { wrapper });
        await waitFor(() => expect(result.current.drivers).toHaveLength(100));
        expect(result.current.hasNextPage).toBe(true);
        expect(result.current.statusCounts).toEqual({ active: 140, on_duty: 5, inactive: 5 });
        await act(() => result.current.fetchNextPage());
        await waitFor(() => expect(result.current.drivers).toHaveLength(150));
        expect(result.current.hasNextPage).toBe(false);
        expect(goCalls("/api/go/v1/drivers").map((x) => x.url)).toEqual([
            "/api/go/v1/drivers?limit=100",
            "/api/go/v1/drivers?limit=100&cursor=page-2",
        ]);
        const query = getQueryClient().getQueryCache().find({ queryKey: ["drivers", { status: "", q: "" }] });
        const observer = query?.observers[0];
        expect(observer?.options.refetchInterval).toBe(POLL_INTERVAL_MS);
        expect(observer?.options.refetchIntervalInBackground).toBe(false);
    });

    it("trucks: server filters in the request, keyset pages past 100 rows, 60 s visible poll", async () => {
        route("GET", (u) => u.startsWith("/api/go/v1/trucks?"), (url) => {
            const cursor = new URL(url, "http://x").searchParams.get("cursor");
            if (!cursor) return json(Array.from({ length: 100 }, (_, i) => truck(i)), { nextCursor: "t-2" });
            return json(Array.from({ length: 20 }, (_, i) => truck(100 + i)));
        });
        const { result } = renderHook(() => useTruckPages({ ownership: "own", status: "active", q: "กข" }, { poll: true }), { wrapper });
        await waitFor(() => expect(result.current.trucks).toHaveLength(100));
        await act(() => result.current.fetchNextPage());
        await waitFor(() => expect(result.current.trucks).toHaveLength(120));
        const first = new URL(goCalls("/api/go/v1/trucks")[0].url, "http://x").searchParams;
        expect(Object.fromEntries(first)).toEqual({ ownership: "own", status: "active", q: "กข", limit: "100" });
        const query = getQueryClient().getQueryCache().find({ queryKey: ["trucks"], exact: false });
        expect(query?.observers[0]?.options.refetchInterval).toBe(POLL_INTERVAL_MS);
    });
});

describe("master data writes through /api/go", () => {
    function presignRoutes(keys: string[]) {
        route("POST", "/api/go/v1/uploads/presign", (_u, body) => {
            const b = body as { purpose: string; entityId: string };
            const key = `${b.purpose}/${b.entityId}`;
            keys.push(key);
            return json({ key, url: `/v1/uploads/local/${key}?X-LT-Signature=s`, method: "PUT", headers: { "Content-Type": "image/png" } });
        });
        route("PUT", (u) => u.startsWith("/api/go/v1/uploads/local/"), () => new Response(null, { status: 200 }));
    }

    it("driver logins: POST /v1/users {role: driver, driverId, tenantId: the driver's}, a temporary password once", async () => {
        route("POST", "/api/go/v1/users", (_u, body) => json({ user: { id: "u-1", email: (body as { email: string }).email }, temporaryPassword: "Tmp-1234" }, {}, 201));
        const result = await createDriverLogin({ driver: driver(7), email: "d7@example.com" });
        expect(result.temporaryPassword).toBe("Tmp-1234");
        // A carrier's driver (registered by the own fleet in the carrier): the login goes to the carrier.
        await createDriverLogin({ driver: { ...driver(8), tenantId: "t-carrier" }, email: "d8@example.com" });
        // A new own-fleet driver from the wizard (no tenant known): the caller's active tenant.
        await createDriverLogin({ driver: { ...driver(9), tenantId: undefined }, email: "d9@example.com" });
        expect(goCalls("/api/go/v1/users").map((c) => c.body)).toEqual([
            { email: "d7@example.com", displayName: "คนขับ 7", role: "driver", driverId: "d-7", tenantId: "t-own" },
            { email: "d8@example.com", displayName: "คนขับ 8", role: "driver", driverId: "d-8", tenantId: "t-carrier" },
            { email: "d9@example.com", displayName: "คนขับ 9", role: "driver", driverId: "d-9" },
        ]);
    });

    it("driver logins: the temporary password never enters a query or mutation cache (R29, T54S rule)", async () => {
        // A distinctive value built at runtime, so the source holds no password-looking literal.
        const SENTINEL = ["tmp", "sentinel", "7f3a"].join("-");
        route("POST", "/api/go/v1/users", (_u, body) => json({ user: { id: "u-1", email: (body as { email: string }).email }, temporaryPassword: SENTINEL }, {}, 201));
        route("GET", (u) => u.startsWith("/api/go/v1/drivers?"), () => json([driver(1)]));
        // A drivers list is mounted: the login makes it stale (and it refetches).
        const list = renderHook(() => useDriverPages({ status: "", q: "" }), { wrapper });
        await waitFor(() => expect(list.result.current.drivers).toHaveLength(1));
        const { result, unmount } = renderHook(() => useCreateDriverLogin(), { wrapper });
        let got: { temporaryPassword?: string } | undefined;
        await act(async () => {
            got = await result.current.create({ driver: driver(1), email: "d1@example.com" });
        });
        expect(got?.temporaryPassword).toBe(SENTINEL);
        await waitFor(() => expect(goCalls("/api/go/v1/drivers")).toHaveLength(2));
        unmount();
        const client = getQueryClient();
        const cached = JSON.stringify([
            client.getMutationCache().getAll().map((m) => [m.state.data, m.state.variables]),
            client.getQueryCache().getAll().map((q) => q.state.data),
        ]);
        expect(cached).not.toContain(SENTINEL);
        expect(client.getMutationCache().getAll()).toHaveLength(0);
    });

    it("drivers: the Go list carries the legacy Firebase uid the Chat action needs", async () => {
        route("GET", (u) => u.startsWith("/api/go/v1/drivers?"), () => json([{ ...driver(1), legacyAuthUid: "fb-1" }, driver(2)]));
        const { result } = renderHook(() => useDriverPages({ status: "", q: "" }), { wrapper });
        await waitFor(() => expect(result.current.drivers).toHaveLength(2));
        expect(result.current.drivers.map((d) => d.legacyAuthUid ?? null)).toEqual(["fb-1", null]);
    });

    it("driver update: only the changed fields, files as keys after presign + PUT", async () => {
        const keys: string[] = [];
        presignRoutes(keys);
        route("PATCH", "/api/go/v1/drivers/d-7", () => json({}));
        const previous = { ...driver(7), profileKey: null, profileUrl: null, customerDriverIds: [], statusHistory: [], login: null, idCard: "1234567890123", birthDate: "1990-01-01" };
        const values = { ...driverFormValues(previous as never), mobile: "0899999999" };
        const profile = new File(["x"], "me.png", { type: "image/png" });
        await updateDriver({ id: "d-7", values, files: { profile, idCard: null, license: null }, customerIdByCode: new Map(), previous: previous as never });
        const patch = goCalls("/api/go/v1/drivers/d-7");
        expect(patch).toHaveLength(1);
        expect(patch[0].body).toEqual({ mobile: "0899999999", profileKey: "driver_profile/d-7" });
        expect(keys).toEqual(["driver_profile/d-7"]);
    });

    it("customer create: POST, then the logo upload and PATCH logoKey (the key embeds the customer)", async () => {
        const keys: string[] = [];
        presignRoutes(keys);
        route("POST", "/api/go/v1/customers", () => json({ id: "c-9" }, {}, 201));
        route("PATCH", "/api/go/v1/customers/c-9", () => json({ id: "c-9" }));
        const id = await createCustomer({
            input: {
                code: "NEW", name: "New", description: null, address: null, taxId: null, branchType: "hq", branchNumber: null,
                contactName: null, contactPhone: null, billingEmail: null, paymentTermsDays: 30, invoiceNote: null,
                lineGroupId: null, billingDateBasis: "plan", driverIdTypes: [{ key: "appId", label: "App ID" }],
            },
            logo: new File(["x"], "logo.png", { type: "image/png" }),
        });
        expect(id).toBe("c-9");
        expect(calls.filter((c) => c.method !== "GET").map((c) => `${c.method} ${c.url.split("?")[0]}`)).toEqual([
            "POST /api/go/v1/customers",
            "POST /api/go/v1/uploads/presign",
            "PUT /api/go/v1/uploads/local/customer_logo/c-9",
            "PATCH /api/go/v1/customers/c-9",
        ]);
        expect(goCalls("/api/go/v1/customers/c-9")[0].body).toEqual({ logoKey: "customer_logo/c-9" });
    });

    it("truck create: POST without files, then the picked photos as keys in one PATCH", async () => {
        const keys: string[] = [];
        presignRoutes(keys);
        route("POST", "/api/go/v1/trucks", () => json({ id: "t-new" }, {}, 201));
        route("PATCH", "/api/go/v1/trucks/t-new", () => json({ id: "t-new" }));
        const blob = "blob:http://localhost/photo-1";
        const values = {
            ownershipType: "own", subcontractorId: "", licensePlate: "กข-1234", province: "กรุงเทพมหานคร", vin: "", engineNumber: "",
            GPSVehicleId: "", truckStatus: "insurance-claim", brand: "Isuzu", model: "FRR", year: "2022", color: "White", type: "6 Wheels",
            seats: "3", imageFrontRight: blob, imageFrontLeft: "", imageBackRight: "", imageBackLeft: "", documentTax: "", documentRegister: "",
            insuranceDocuments: [], paymentMethod: "Company Credit",
        };
        await createTruck({ values: values as never, pending: new Map([[blob, new File(["x"], "fr.png", { type: "image/png" })]]) });
        const post = goCalls("/api/go/v1/trucks")[0];
        expect(post.body).toMatchObject({ licensePlate: "กข-1234", status: "insurance_claim", year: 2022, seats: 3, type: "6 Wheels" });
        expect(post.body).not.toHaveProperty("files");
        expect(post.body).not.toHaveProperty("tenantId");
        const patch = goCalls("/api/go/v1/trucks/t-new")[0];
        expect(patch.body).toEqual({ files: { imageFrontRight: ["truck_photo/t-new"] } });
    });

    it("truck update: stored files without a URL are kept; only the kinds the user changed go out (T25 review)", async () => {
        const keys: string[] = [];
        presignRoutes(keys);
        route("PATCH", "/api/go/v1/trucks/t-1", () => json({ id: "t-1" }));
        // Go lists a file it cannot sign (ETL missing_at_source, an unconfigured backend) as {key, url: null}.
        const dto = {
            ...truck(1), province: "กทม", vin: null, engineNumber: null, gpsVehicleId: null, legacyStatus: null, brand: "Isuzu", model: "FRR",
            year: 2021, color: "W", vehicleClass: null, seats: null, fuelType: null, engineCapacity: null, fuelCapacity: null, maxLoadWeightKg: null,
            registrationDate: null, buyingDate: null, notes: null, taxExpiryDate: null, taxExpenseThb: null, taxResponsible: "Operation Admin",
            taxRenewalStatus: null, maintenanceResponsible: "Driver", lastServiceDate: null, nextServiceDate: null, nextServiceMileage: null,
            pmIntervalKm: null, currentMileage: null, insurancePolicyId: null, insurancePolicyNumber: null, insuranceCompany: null, insuranceType: null,
            insuranceStartDate: null, insuranceExpiryDate: null, insurancePremiumThb: null, insuranceNotes: null, insuranceRenewalStatus: null,
            paymentMethod: null, createdAt: null, updatedAt: null, statusHistory: [], currentAssignments: [],
            files: {
                imageFrontRight: [{ key: "trucks/t-1/photos/fr.jpg", url: null }],
                imageFrontLeft: [{ key: "trucks/t-1/photos/fl.jpg", url: "https://s3/fl.jpg?sig" }],
                insuranceDocument: [
                    { key: "trucks/t-1/insurance/a.pdf", url: "https://s3/a.pdf?sig" },
                    { key: "trucks/t-1/insurance/b.pdf", url: null },
                ],
            },
        };
        const values = truckFormValues(dto as never);
        // The form holds refs (keys), so the url-less files are present (and required photos validate).
        expect(values).toMatchObject({
            imageFrontRight: "trucks/t-1/photos/fr.jpg",
            imageFrontLeft: "trucks/t-1/photos/fl.jpg",
            insuranceDocuments: ["trucks/t-1/insurance/a.pdf", "trucks/t-1/insurance/b.pdf"],
        });
        const previews = truckFilePreviewUrls(dto.files as never);
        expect(previewOfRef(previews, "trucks/t-1/photos/fr.jpg")).toBeNull(); // shown as unavailable
        expect(previewOfRef(previews, "trucks/t-1/photos/fl.jpg")).toBe("https://s3/fl.jpg?sig");
        expect(previewOfRef(previews, "blob:http://localhost/x")).toBe("blob:http://localhost/x");
        const stored = storedFileKeys(dto.files as never);

        // Only notes changed: no files at all in the PATCH.
        await updateTruck({ id: "t-1", values: { ...values, notes: "edited" } as never, pending: new Map(), stored });
        expect(goCalls("/api/go/v1/trucks/t-1")[0].body).toMatchObject({ notes: "edited" });
        expect(goCalls("/api/go/v1/trucks/t-1")[0].body).not.toHaveProperty("files");

        // The user removes the front-left photo and document a, and adds a document: those kinds only,
        // the url-less document b kept as its key, the url-less front-right photo untouched.
        const blob = "blob:http://localhost/new-doc";
        await updateTruck({
            id: "t-1",
            values: { ...values, imageFrontLeft: "", insuranceDocuments: ["trucks/t-1/insurance/b.pdf", blob] } as never,
            pending: new Map([[blob, new File(["x"], "c.pdf", { type: "application/pdf" })]]),
            stored,
        });
        expect((goCalls("/api/go/v1/trucks/t-1")[1].body as { files: unknown }).files).toEqual({
            imageFrontLeft: [],
            insuranceDocument: ["trucks/t-1/insurance/b.pdf", "insurance_document/t-1"],
        });
    });

    it("renewal, assignment, revoke, carrier, company and hub writes reach their Go routes", async () => {
        route("POST", "/api/go/v1/trucks/t-1/renewals", () => json({ truck: {}, transaction: {} }, {}, 201));
        route("POST", "/api/go/v1/truck-assignments", () => json({ id: "a-1" }, {}, 201));
        route("POST", "/api/go/v1/truck-assignments/a-1/revoke", () => json({ id: "a-1" }));
        route("POST", "/api/go/v1/subcontractors", () => json({ id: "s-1" }, {}, 201));
        route("POST", "/api/go/v1/companies", () => json({ id: "co-1" }, {}, 201));
        route("POST", "/api/go/v1/hubs", () => json({ id: "h-9" }, {}, 201));

        await renewTruck({
            truck: { id: "t-1", files: {}, statusHistory: [] } as never, type: "tax", status: "completed", amount: 1200, paymentMethod: "Transfer",
            expiryDate: "2027-01-31", responsible: "Operation Admin", receipt: null, insuranceNotes: "Claim #42 pending with insurer",
        });
        await assignTruck({ truckId: "t-1", driverId: "d-1", driverName: "x", truckPlate: "x", truckModel: "x", adminName: "x" });
        await revokeAssignment({ id: "a-1", truckId: "t-1", driverId: "d-1" });
        await createSubcontractor({ values: { name: "Carrier", type: "company", taxId: "0105551234567", contactPerson: "A", phone: "02" } as never });
        await saveCompany({ id: null, values: { nameTh: "บริษัท", taxId: "0105551234567", branchType: "headquarters", address: "BKK", withholdingTaxRate: 3, companyType: "owner", isActive: true } as never });
        await createHub({ sourceId: "NEW-1", sourceNameTh: "ใหม่", sourceNameEn: null, latitude: 13.7, longitude: 100.5, stationType: "HUB", linkedCustomerId: "c-1", linkedCustomerKind: "customer", linkedCustomerName: "CJ — CJ" });

        // The ledger note is never sent (Go writes the legacy "Expiry: …"), nor the insurance notes on a tax renewal.
        expect(goCalls("/api/go/v1/trucks/t-1/renewals")[0].body).toEqual({ type: "tax", status: "completed", amount: 1200, paymentMethod: "transfer", expiryDate: "2027-01-31", responsible: "Operation Admin" });
        expect(goCalls("/api/go/v1/truck-assignments")[0].body).toEqual({ truckId: "t-1", driverId: "d-1" });
        expect(goCalls("/api/go/v1/subcontractors")[0].body).toMatchObject({ nameTh: "Carrier", legalType: "company", taxId: "0105551234567", status: "active" });
        expect(goCalls("/api/go/v1/companies")[0].body).toMatchObject({ nameTh: "บริษัท", companyType: "owner", withholdingTaxRate: 3 });
        // The legacy denormalised name never goes to Go (it derives the name from the party).
        expect(goCalls("/api/go/v1/hubs")[0].body).toEqual({ sourceId: "NEW-1", sourceNameTh: "ใหม่", sourceNameEn: null, latitude: 13.7, longitude: 100.5, stationType: "HUB", linkedCustomerId: "c-1", linkedCustomerKind: "customer" });
    });

    it("insurance renewal: the truck's notes, the person in charge and the document list (T25 review)", async () => {
        const keys: string[] = [];
        presignRoutes(keys);
        route("POST", "/api/go/v1/trucks/t-2/renewals", () => json({ truck: {}, transaction: null }));
        const truckDto = {
            id: "t-2", statusHistory: [],
            files: { insuranceDocument: [{ key: "trucks/t-2/insurance/u1.pdf", url: "https://s3/u1" }, { key: "trucks/t-2/insurance/u2.pdf", url: null }] },
        };
        const base = {
            truck: truckDto as never, type: "insurance" as const, status: "in_progress" as const, amount: null, paymentMethod: "", expiryDate: "",
            responsible: "Somchai", receipt: null, provider: "Viriyah", policyNumber: "P-1", coverage: "2+", insuranceNotes: "new insurance note",
        };
        // Document u2 removed, one new document added.
        await renewTruck({ ...base, documents: ["trucks/t-2/insurance/u1.pdf"], newDocuments: [new File(["x"], "n.pdf", { type: "application/pdf" })] });
        // Nothing changed in the list: no documents field (the stored ones stay).
        await renewTruck({ ...base, documents: ["trucks/t-2/insurance/u1.pdf", "trucks/t-2/insurance/u2.pdf"], newDocuments: [] });
        const [changed, same] = goCalls("/api/go/v1/trucks/t-2/renewals").map((c) => c.body as Record<string, unknown>);
        expect(changed).toMatchObject({
            type: "insurance", status: "in_progress", responsible: "Somchai", insuranceNotes: "new insurance note", provider: "Viriyah",
            documents: ["trucks/t-2/insurance/u1.pdf", "insurance_document/t-2"],
        });
        expect(changed).not.toHaveProperty("notes");
        expect(changed).not.toHaveProperty("documentKeys");
        expect(same).not.toHaveProperty("documents");
        expect(same).toMatchObject({ responsible: "Somchai", insuranceNotes: "new insurance note" });
    });

    it("the owner company: 404 means none (a carrier principal, or not set up yet)", async () => {
        route("GET", "/api/go/v1/companies/owner", () => new Response(JSON.stringify({ error: { code: "not_found", message: "not found" } }), { status: 404, headers: { "Content-Type": "application/json" } }));
        expect(await getOwnerCompany()).toBeNull();
    });
});

describe("driver forms create logins through POST /v1/users only", () => {
    it("no driver component or API calls the createUser callable or sends a login password", () => {
        const root = path.resolve(__dirname, "../../..");
        const files: string[] = [];
        const walk = (dir: string) => {
            for (const name of readdirSync(dir)) {
                const p = path.join(dir, name);
                if (statSync(p).isDirectory()) walk(p);
                else if (/\.(ts|tsx)$/.test(name) && !/\.test\.tsx?$/.test(name)) files.push(p);
            }
        };
        walk(path.join(root, "features/drivers"));
        walk(path.join(root, "app/app/drivers"));
        const offenders = files.filter((f) => {
            const src = readFileSync(f, "utf8");
            return /httpsCallable\([^)]*["']createUser["']/.test(src) || /loginPassword/.test(src);
        });
        expect(files.length).toBeGreaterThan(5);
        expect(offenders.map((f) => path.relative(root, f))).toEqual([]);
    });
});
