// T42: the maintenance pages over Go: the list request (server filters, keyset), the writes (images
// uploaded under the record and linked by PATCH, the kept keys sent with the new one) and the mapping
// between the Go record and the shared form / history components.
import { afterEach, describe, expect, it, vi } from "vitest";

import { getQueryClient } from "@/lib/queryClient";
import { queryKeys } from "@/lib/queryKeys";
import { PartialCreateError, UploadRefusedError } from "@/lib/goUpload";
import { fakeWeb, goErr, json } from "@/test-utils/fakeWeb";

import { maintenanceInputOf, goStatus, legacyStatus, pickupAppointmentOf, toMaintenanceData } from "../utils/goRecord";
import { createMaintenance, maintenanceCostTotals, maintenanceQuery, maintenanceStatusCounts, updateMaintenance, type MaintenanceRecordDTO } from "./maintenance.go";

afterEach(() => {
    vi.unstubAllGlobals();
    getQueryClient().clear();
});

const record = (over: Partial<MaintenanceRecordDTO> = {}): MaintenanceRecordDTO =>
    ({
        id: "m1",
        truckId: "t1",
        licensePlate: "70-1234",
        truckBrand: "Isuzu",
        type: "PM",
        serviceType: "periodic_check",
        startDate: "2026-10-01",
        endDate: null,
        status: "pm_booking",
        appointmentTime: null,
        pickupAppointmentAt: null,
        costLaborThb: null,
        costPartsThb: null,
        totalCostThb: null,
        invoiceAmountThb: 1500,
        displayCostThb: 1500,
        provider: null,
        providerLat: null,
        providerLng: null,
        paymentMethod: null,
        currentMileage: 120000,
        nextServiceMileage: null,
        driverSubmitted: true,
        notes: null,
        files: { images: ["maintenance/m1/image_1.jpg"], receipts: [], invoices: ["maintenance/m1/invoice_2.pdf"] },
        createdAt: "2026-10-01T00:00:00Z",
        updatedAt: "2026-10-01T00:00:00Z",
        ...over,
    }) as MaintenanceRecordDTO;

describe("maintenance over Go", () => {
    it("lists with the server's filters, 100 per page, and reads the status counts from the first page", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/maintenance", () => json(200, { data: [], meta: { statusCounts: { in_progress: 2 } } }));
        const q = maintenanceQuery({ truckId: "t1", status: "all", type: "PM" });
        expect(q.queryKey).toEqual(["maintenance", { truckId: "t1", type: "PM" }]);
        const page = (await (q.queryFn as (c: unknown) => Promise<unknown>)({ queryKey: q.queryKey, signal: new AbortController().signal, pageParam: undefined })) as never;
        expect(Object.fromEntries(new URL(web.calls[0].url, "http://w").searchParams)).toEqual({ truckId: "t1", type: "PM", limit: "100" });
        expect(maintenanceStatusCounts([page])).toEqual({ in_progress: 2 });
    });

    it("creates the record, then uploads its images under it and links them with a PATCH", async () => {
        const web = fakeWeb();
        web.on("POST", "/api/go/v1/maintenance", () => json(201, { data: record({ files: { images: [], receipts: [], invoices: [] } }) }));
        web.on("POST", "/api/go/v1/uploads/presign", () => json(200, { data: { key: "maintenance/m1/image_9.jpg", url: "/v1/uploads/local/x", method: "PUT", headers: {} } }));
        web.on("PUT", "/api/go/v1/uploads/local/x", () => new Response(null, { status: 200 }));
        web.on("PATCH", "/api/go/v1/maintenance/m1", (c) => json(200, { data: record({ files: (c.body as { files: MaintenanceRecordDTO["files"] }).files }) }));
        const client = getQueryClient();
        client.setQueryData(queryKeys.maintenance({}), 1);
        const out = await createMaintenance({ truckId: "t1", type: "CM", serviceType: "brakes", startDate: "2026-10-02", status: "in_progress" }, [new File(["x"], "a.jpg", { type: "image/jpeg" })], client);
        const presign = web.calls.find((c) => c.url === "/api/go/v1/uploads/presign")!;
        expect(presign.body).toMatchObject({ purpose: "maintenance_file", entityId: "m1", variant: "image" });
        expect(web.calls.find((c) => c.method === "PATCH")!.body).toEqual({ files: { images: ["maintenance/m1/image_9.jpg"] } });
        expect(out.files.images).toEqual(["maintenance/m1/image_9.jpg"]);
        expect(client.getQueryState(queryKeys.maintenance({}))?.isInvalidated).toBe(true);
    });

    it("an update with a new image keeps the record's images (a named kind is replaced as a whole)", async () => {
        const web = fakeWeb();
        web.on("POST", "/api/go/v1/uploads/presign", () => json(200, { data: { key: "maintenance/m1/image_9.jpg", url: "/v1/uploads/local/x", method: "PUT", headers: {} } }));
        web.on("PUT", "/api/go/v1/uploads/local/x", () => new Response(null, { status: 200 }));
        web.on("PATCH", "/api/go/v1/maintenance/m1", () => json(200, { data: record() }));
        await updateMaintenance(record(), { status: "scheduled" }, [new File(["x"], "b.jpg", { type: "image/jpeg" })]);
        expect(web.calls.find((c) => c.method === "PATCH")!.body).toEqual({
            status: "scheduled",
            files: { images: ["maintenance/m1/image_1.jpg", "maintenance/m1/image_9.jpg"] },
        });
    });

    it("maps statuses both ways and the record onto the shared components' shape", () => {
        expect(legacyStatus("pm_booking")).toBe("PM Booking");
        expect(goStatus("Scheduled")).toBe("scheduled");
        expect(goStatus("completed")).toBe("completed");
        const d = toMaintenanceData(record());
        expect(d).toMatchObject({ id: "m1", status: "PM Booking", invoiceAmount: 1500, driverSubmitted: true, currentMileage: 120000 });
        expect(d.images).toEqual(["/api/go/v1/files?key=maintenance%2Fm1%2Fimage_1.jpg"]);
        expect(d.invoiceUrl).toBe("/api/go/v1/files?key=maintenance%2Fm1%2Finvoice_2.pdf");
    });

    it("builds the Go body of a form save, with the PM next-service rule and a Bangkok wall time", () => {
        const body = maintenanceInputOf(
            {
                type: "PM",
                serviceType: "periodic_check",
                status: "completed",
                startDate: "2026-10-01",
                endDate: "2026-10-03",
                pickupAppointment: "2026-10-01T09:30",
                costLabor: "500",
                costParts: "",
                currentMileage: "120000",
                nextServiceMileage: "",
                provider: " Garage ",
                providerPos: { lat: 13.7, lng: 100.5 },
                paymentMethod: "cash",
                notes: "",
            },
            "t1",
            20000
        );
        expect(body).toEqual({
            truckId: "t1",
            type: "PM",
            serviceType: "periodic_check",
            status: "completed",
            startDate: "2026-10-01",
            endDate: "2026-10-03",
            pickupAppointmentAt: "2026-10-01T09:30:00",
            appointmentTime: "09:30",
            costLaborThb: 500,
            costPartsThb: null,
            totalCostThb: 500,
            provider: "Garage",
            providerLat: 13.7,
            providerLng: 100.5,
            paymentMethod: "cash",
            currentMileage: 120000,
            nextServiceMileage: 140000,
            notes: null,
        });
    });

    it("refuses a file the upload would refuse before the record is written", async () => {
        const web = fakeWeb();
        const input = { truckId: "t1", type: "CM" as const, serviceType: "brakes", startDate: "2026-10-02", status: "in_progress" as const };
        await expect(createMaintenance(input, [new File(["x"], "a.gif", { type: "image/gif" })])).rejects.toBeInstanceOf(UploadRefusedError);
        expect(web.calls).toHaveLength(0);
    });

    it("an upload failing after the record exists hands the record back instead of leaving the caller to post again", async () => {
        const web = fakeWeb();
        web.on("POST", "/api/go/v1/maintenance", () => json(201, { data: record({ status: "in_progress", files: { images: [], receipts: [], invoices: [] } }) }));
        web.on("POST", "/api/go/v1/uploads/presign", () => goErr(422, "invalid_argument", { fields: [{ field: "sizeBytes", reason: "out_of_range" }] }));
        const client = getQueryClient();
        client.setQueryData(queryKeys.maintenance({}), 1);
        const input = { truckId: "t1", type: "CM" as const, serviceType: "brakes", startDate: "2026-10-02", status: "in_progress" as const };
        const err = await createMaintenance(input, [new File(["x"], "a.jpg", { type: "image/jpeg" })], client).catch((e: unknown) => e);
        expect(err).toBeInstanceOf(PartialCreateError);
        expect((err as PartialCreateError<MaintenanceRecordDTO>).entity.id).toBe("m1");
        expect(web.count("POST", "/api/go/v1/maintenance")).toBe(1);
        expect(client.getQueryState(queryKeys.maintenance({}))?.isInvalidated).toBe(true);
    });

    it("keeps a booking's time when the record is edited without touching it", () => {
        // A PM booked on the overview stores the day and the time only.
        const booked = record({ status: "scheduled", startDate: "2026-10-15", appointmentTime: "09:00", pickupAppointmentAt: null });
        expect(pickupAppointmentOf(booked)).toBe("2026-10-15T09:00");
        const d = toMaintenanceData(booked);
        expect(d.pickupAppointment).toBe("2026-10-15T09:00");
        const body = maintenanceInputOf(
            {
                type: "PM",
                serviceType: d.serviceType,
                status: "in_progress",
                startDate: d.startDate,
                endDate: "",
                pickupAppointment: d.pickupAppointment ?? "",
                costLabor: "",
                costParts: "",
                currentMileage: "120000",
                nextServiceMileage: "",
                provider: "Garage",
                providerPos: null,
                paymentMethod: "cash",
                notes: "",
            },
            "t1",
            20000
        );
        expect(body.appointmentTime).toBe("09:00");
        expect(body.pickupAppointmentAt).toBe("2026-10-15T09:00:00");
        expect(pickupAppointmentOf(record({ appointmentTime: null, pickupAppointmentAt: null }))).toBeUndefined();
    });

    it("sums PM and CM with the server's display cost", () => {
        expect(maintenanceCostTotals([{ type: "PM", displayCostThb: 100 }, { type: "CM", displayCostThb: 50 }, { type: "PM", displayCostThb: 0 }])).toEqual({ pm: 100, cm: 50, total: 150 });
    });
});
