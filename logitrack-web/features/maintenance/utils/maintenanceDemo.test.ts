import { describe, expect, it } from "vitest";
import { maintenanceInputOf, pickupAppointmentOf, toMaintenanceData, type MaintenanceFormValuesGo } from "./goRecord";
import { buildMaintenanceGalleryUrls, buildDriverReceiptUrls } from "./buildMaintenanceGalleryUrls";
import { looksLikeImageUrl } from "./looksLikeImageUrl";
import { MAINTENANCE_STATUSES, refreshMaintenance, type MaintenanceRecordDTO } from "../api/maintenance.go";
import { canTransitionMaintenance } from "./maintenanceTransitions";
import { maintenanceServiceLabel, maintenanceStatusLabel, maintenanceTruckStatusLabel } from "./maintenanceLabels";
import en from "@/context/locales/en/maintenance";
import th from "@/context/locales/th/maintenance";
import enTrucks from "@/context/locales/en/trucks";
import thTrucks from "@/context/locales/th/trucks";
import { QueryClient } from "@tanstack/react-query";

const values: MaintenanceFormValuesGo = {
    type: "PM", serviceType: "periodic_check", status: "completed",
    startDate: "2026-10-15T09:30:00", endDate: "2026-10-16T17:00:00",
    pickupAppointment: "2026-10-16T17:00", costLabor: "", costParts: "",
    currentMileage: "120000", nextServiceMileage: "", provider: "Garage", providerPos: null,
    paymentMethod: "cash", notes: "",
};
const record = {
    id: "m1", truckId: "t1", type: "PM", serviceType: "periodic_check", status: "cancelled",
    startDate: "2026-10-15", endDate: null, costLaborThb: null, costPartsThb: null, totalCostThb: 3500,
    files: { images: ["maintenance/a.jpg"], receipts: ["maintenance/r.pdf"], invoices: Array.from({ length: 20 }, (_, i) => `maintenance/invoice-${i}.pdf`) },
} as MaintenanceRecordDTO;

describe("fleet maintenance demo regressions", () => {
    it("sends date-only start/end days while retaining the dedicated appointment time", () => {
        const input = maintenanceInputOf(values, "t1", 20000);
        expect(input.startDate).toBe("2026-10-15");
        expect(input.endDate).toBe("2026-10-16");
        expect(input.pickupAppointmentAt).toBe("2026-10-16T17:00:00");
        expect(input.appointmentTime).toBe("17:00");
    });
    it("uses the Bangkok calendar day for zoned timestamps", () => {
        const input = maintenanceInputOf({ ...values, startDate: "2026-10-14T19:00:00Z" }, "t1", 20000);
        expect(input.startDate).toBe("2026-10-15");
    });
    it("shows a stored pickup instant in Bangkok regardless of the browser timezone", () => {
        expect(pickupAppointmentOf({ pickupAppointmentAt: "2026-10-15T23:30:00Z", startDate: "2026-10-15", appointmentTime: null })).toBe("2026-10-16T06:30");
    });
    it("carries all twenty invoices through the history and form galleries", () => {
        const data = toMaintenanceData(record);
        expect(buildDriverReceiptUrls(data)).toHaveLength(21);
        expect(buildMaintenanceGalleryUrls(data)).toHaveLength(22);
    });
    it("recognizes encoded Go file keys and never treats a PDF as an image", () => {
        expect(looksLikeImageUrl("/api/go/v1/files?key=maintenance%2Fa.jpg")).toBe(true);
        expect(looksLikeImageUrl("https://firebasestorage.googleapis.com/o/receipt.pdf?alt=media")).toBe(false);
    });
});

it("keeps a booking time distinct from the pickup time on edit", () => {
    const original = { ...record, appointmentTime: "09:30", pickupAppointmentAt: "2026-10-16T10:00:00Z" };
    expect(maintenanceInputOf(values, "t1", 20000, original)).toMatchObject({ appointmentTime: "09:30", pickupAppointmentAt: "2026-10-16T17:00:00" });
});
    it("derives appointmentTime from a changed pickup appointment", () => {
        const original = { ...record, appointmentTime: "09:30", pickupAppointmentAt: "2026-10-16T10:00:00Z" };
        const input = maintenanceInputOf({ ...values, pickupAppointment: "2026-10-16T18:45" }, "t1", 20000, original);
        expect(input).toMatchObject({ appointmentTime: "18:45", pickupAppointmentAt: "2026-10-16T18:45:00" });
    });

describe("maintenance patch costs", () => {
    it("omits all unchanged costs, preserving a total without a breakdown", () => {
        const input = maintenanceInputOf(values, "t1", 20000, record);
        expect(input).not.toHaveProperty("costLaborThb");
        expect(input).not.toHaveProperty("costPartsThb");
        expect(input).not.toHaveProperty("totalCostThb");
        expect({ ...record, ...input }.totalCostThb).toBe(3500);
    });
    it("keeps a stored total different from an unchanged breakdown", () => {
        const original = { ...record, costLaborThb: 200, costPartsThb: 500 };
        expect(maintenanceInputOf({ ...values, costLabor: "200.00", costParts: "500" }, "t1", 20000, original)).not.toHaveProperty("totalCostThb");
    });
    it("recalculates an explicitly edited breakdown, preserving the other cost field", () => {
        const original = { ...record, costLaborThb: 200, costPartsThb: 500 };
        const input = maintenanceInputOf({ ...values, costLabor: "300", costParts: "500" }, "t1", 20000, original);
        expect(input).toMatchObject({ costLaborThb: 300, totalCostThb: 800 });
        expect(input).not.toHaveProperty("costPartsThb");
    });
    it("allows explicitly clearing or zeroing a cost", () => {
        const original = { ...record, costLaborThb: 200 };
        expect(maintenanceInputOf(values, "t1", 20000, original)).toMatchObject({ costLaborThb: null, totalCostThb: null });
        expect(maintenanceInputOf({ ...values, costLabor: "0" }, "t1", 20000, original)).toMatchObject({ costLaborThb: 0, totalCostThb: 0 });
    });
});

describe("Go CanTransition parity", () => {
    const allowed: Record<string, string[]> = {
        pm_booking: ["scheduled", "in_progress", "completed", "cancelled"],
        scheduled: ["pm_booking", "in_progress", "completed", "cancelled"],
        in_progress: ["completed", "cancelled"], completed: [], cancelled: [],
    };
    for (const from of MAINTENANCE_STATUSES) for (const to of MAINTENANCE_STATUSES) {
        it(`${from} → ${to}`, () => expect(canTransitionMaintenance(from, to)).toBe(from === to || allowed[from].includes(to)));
    }
    it("accepts the presentation spellings without reopening terminal jobs", () => {
        expect(canTransitionMaintenance("PM Booking", "Scheduled")).toBe(true);
        expect(canTransitionMaintenance("Scheduled", "completed")).toBe(true);
        expect(canTransitionMaintenance("cancelled", "completed")).toBe(false);
    });
});

describe("maintenance localized labels", () => {
    it("has identical English and Thai keys", () => expect(Object.keys(en).sort()).toEqual(Object.keys(th).sort()));
    it("uses a count label that reads correctly for one or many loaded records", () => {
        expect(en["maintenance.go.loadedCosts"]).toBe("Loaded records: {count}");
        expect(th["maintenance.go.loadedCosts"]).toBe("รายการที่โหลดแล้ว: {count} รายการ");
        for (const count of [1, 2]) {
            expect(en["maintenance.go.loadedCosts"].replace("{count}", String(count))).toBe(`Loaded records: ${count}`);
            expect(th["maintenance.go.loadedCosts"].replace("{count}", String(count))).toBe(`รายการที่โหลดแล้ว: ${count} รายการ`);
        }
    });
    for (const [language, dictionary] of [["en", { ...en, ...enTrucks }], ["th", { ...th, ...thTrucks }]] as const) {
        const t = (key: string) => dictionary[key as keyof typeof dictionary] ?? key;
        it(`${language}: translates API and presentation statuses and service codes`, () => {
            for (const status of [...MAINTENANCE_STATUSES, "Scheduled", "PM Booking"]) expect(maintenanceStatusLabel(status, t)).not.toMatch(/maintenance\.|^(in_progress|Scheduled|pm_booking)$/);
            expect(maintenanceServiceLabel("periodic_check", t)).toBe(dictionary["maintenance.service.periodic_check"]);
            for (const status of ["active", "inactive", "sold", "insurance_claim", "maintenance", "in_transit", "available", "unexpected"]) expect(maintenanceTruckStatusLabel(status, t)).not.toMatch(/trucks\.|maintenance\.|^(active|inactive|sold|insurance_claim|maintenance)$/);
        });
        it(`${language}: retains user descriptions verbatim`, () => expect(maintenanceServiceLabel("  กระจกแตก / inspect mirror  ", t)).toBe("  กระจกแตก / inspect mirror  "));
    }
});

it("Refresh invalidates every maintenance query, including both queues", async () => {
    const client = new QueryClient();
    const keys = [["maintenance", {}], ["maintenance", { status: "pm_booking" }], ["maintenance", { status: "scheduled" }], ["maintenance", { truckId: "t1" }]];
    for (const key of keys) client.setQueryData(key, { pages: [] });
    client.setQueryData(["trucks"], []);
    await refreshMaintenance(client);
    for (const key of keys) expect(client.getQueryState(key)?.isInvalidated).toBe(true);
    expect(client.getQueryState(["trucks"])?.isInvalidated).toBe(false);
    client.clear();
});
