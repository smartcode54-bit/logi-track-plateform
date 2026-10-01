import { describe, expect, it } from "vitest";
import { computeTripBilling, type BillingRateEntry, type FuelRateAdjustment } from "./billingRates";
import type { Task } from "@/validate/taskSchema";
import type { TripRecord } from "@/validate/tripRecordSchema";

/**
 * The client estimate must apply the same หลัก/เสริม rule as the server snapshot (ADR-0006): it is
 * what the Income "missing billing" tab and Edit Billing dialog show, and it used to probe PRIMARY
 * first for every trip — which priced เสริม trips with the fuel adjustment.
 */

const DELIVERED = new Date("2026-09-10T05:00:00Z");

const trip = { deliveredTimestamp: DELIVERED } as unknown as TripRecord;

const task = (over: Partial<Task> = {}): Task =>
    ({
        sourceHub: "SPK-GW",
        destination: "SPK890103",
        truckType: "4WJ",
        sourceHubLinkedCustomerId: "cjsf",
        ...over,
    }) as Task;

const rate = (over: Partial<BillingRateEntry>): BillingRateEntry => ({
    id: "r",
    customerId: "cjsf",
    importId: "imp",
    hubId: "SPK-GW",
    destinationCode: "SPK890103",
    vehicleClass: "4WJ",
    rateThb: 1000,
    effectiveFromMs: new Date("2026-09-01T00:00:00+07:00").getTime(),
    jobCategory: "PRIMARY",
    ...over,
});

const primaryCard = rate({ id: "p", rateThb: 1200, jobCategory: "PRIMARY" });
const suppCard = rate({ id: "s", rateThb: 950, jobCategory: "SUPPLEMENTARY" });

const fuel: FuelRateAdjustment[] = [
    {
        id: "f1",
        customerId: "cjsf",
        effectiveFromMs: new Date("2026-09-01T00:00:00+07:00").getTime(),
        rateMultiplier: 1,
        addThbPerTrip: -40,
    },
];

describe("computeTripBilling — หลัก/เสริม follows the task (ADR-0006)", () => {
    it("an explicit เสริม task prices from the เสริม card with NO fuel, even when a หลัก card exists", () => {
        const r = computeTripBilling(trip, task({ jobCategory: "SUPPLEMENTARY" }), [primaryCard, suppCard], fuel);
        expect(r?.baseRateThb).toBe(950);
        expect(r?.finalRateThb).toBe(950);
        expect(r?.fuelAdjustmentId).toBeUndefined();
        expect(r?.addThbPerTrip).toBe(0);
        expect(r?.rateMultiplier).toBe(1);
    });

    it("an explicit หลัก task never falls back to the เสริม card", () => {
        expect(computeTripBilling(trip, task({ jobCategory: "PRIMARY" }), [suppCard], fuel)).toBeNull();
    });

    it("an explicit หลัก task prices from the หลัก card with fuel", () => {
        const r = computeTripBilling(trip, task({ jobCategory: "PRIMARY" }), [primaryCard, suppCard], fuel);
        expect(r?.finalRateThb).toBe(1160);
        expect(r?.fuelAdjustmentId).toBe("f1");
    });

    it("a legacy task with no category tries หลัก first, then falls back to เสริม (no fuel)", () => {
        expect(computeTripBilling(trip, task(), [primaryCard, suppCard], fuel)?.finalRateThb).toBe(1160);
        const r = computeTripBilling(trip, task(), [suppCard], fuel);
        expect(r?.finalRateThb).toBe(950);
        expect(r?.fuelAdjustmentId).toBeUndefined();
    });

    it("prices under the explicit billing customer chosen at assign (ADR 0027)", () => {
        const other = rate({ id: "o", customerId: "other", rateThb: 700 });
        const r = computeTripBilling(trip, task({ billingCustomerId: "other" }), [primaryCard, other], []);
        expect(r?.customerId).toBe("other");
        expect(r?.finalRateThb).toBe(700);
    });
});
