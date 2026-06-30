import { describe, expect, it } from "vitest";
import {
    computeFinalRateThb,
    computeTripBillingFromParts,
    selectFuelAdjustmentForBillingDate,
    type BillingRateEntry,
    type FuelRateAdjustment,
} from "./billingCompute";

describe("computeFinalRateThb", () => {
    it("rounds to 2 decimal places like billing snapshot", () => {
        expect(computeFinalRateThb(100, 1.05, 0)).toBe(105);
        expect(computeFinalRateThb(100, 1.055, 12.345)).toBe(Math.round((100 * 1.055 + 12.345) * 100) / 100);
        expect(computeFinalRateThb(333.33, 1.01, 0.005)).toBe(Math.round((333.33 * 1.01 + 0.005) * 100) / 100);
    });
});

describe("computeTripBillingFromParts", () => {
    const entries: BillingRateEntry[] = [
        {
            id: "e1",
            customerId: "c1",
            importId: "imp1",
            hubId: "HUBA",
            destinationCode: "SOCE",
            vehicleClass: "4WJ",
            rateThb: 1000,
            effectiveFromMs: new Date("2020-01-01").getTime(),
        },
    ];
    const fuel: FuelRateAdjustment[] = [
        {
            id: "f1",
            customerId: "c1",
            effectiveFromMs: new Date("2025-06-01").getTime(),
            rateMultiplier: 1.1,
            addThbPerTrip: 50,
        },
    ];

    it("matches manual composition for bill date and fuel rule", () => {
        const deliveredMs = new Date("2025-07-15T10:00:00").getTime();
        const result = computeTripBillingFromParts(
            { deliveredTimestamp: deliveredMs },
            {
                sourceHub: "HUBA - Name",
                destination: "SOCE",
                truckType: "4WJ",
                sourceHubLinkedCustomerId: "c1",
            },
            entries,
            fuel
        );
        expect(result).not.toBeNull();
        expect(result!.finalRateThb).toBe(computeFinalRateThb(1000, 1.1, 50));
        expect(result!.baseRateThb).toBe(1000);
        expect(result!.customerId).toBe("c1");
    });

    it("selectFuelAdjustmentForBillingDate picks latest rule on or before bill date", () => {
        const adj = selectFuelAdjustmentForBillingDate(
            "c1",
            new Date("2025-07-01").getTime(),
            fuel
        );
        expect(adj?.id).toBe("f1");
    });
});

describe("jobCategory dimension (ADR-0005 — supplementary trips)", () => {
    // A route that exists ONLY in the supplementary card (e.g. J&T บางปู → Wang Thonglang12 @ 1250).
    const entries: BillingRateEntry[] = [
        {
            id: "primary1",
            customerId: "c1",
            importId: "imp-primary",
            hubId: "HUBA",
            destinationCode: "SOCE",
            vehicleClass: "4WJ",
            rateThb: 1000,
            effectiveFromMs: new Date("2020-01-01").getTime(),
            jobCategory: "PRIMARY",
        },
        {
            id: "supp1",
            customerId: "c1",
            importId: "imp-supp",
            hubId: "HUBA",
            destinationCode: "WANGTHONGLANG12",
            vehicleClass: "4WJ",
            rateThb: 1250,
            effectiveFromMs: new Date("2020-01-01").getTime(),
            jobCategory: "SUPPLEMENTARY",
        },
    ];
    const noFuel: FuelRateAdjustment[] = [];
    const deliveredMs = new Date("2026-05-02T10:00:00").getTime();
    const task = (destination: string) => ({
        sourceHub: "HUBA - Name",
        destination,
        truckType: "4WJ",
        sourceHubLinkedCustomerId: "c1",
    });

    it("PRIMARY lookup (default) does NOT match a supplementary-only route", () => {
        const result = computeTripBillingFromParts(
            { deliveredTimestamp: deliveredMs },
            task("WANGTHONGLANG12"),
            entries,
            noFuel
            // jobCategory defaults to PRIMARY
        );
        expect(result).toBeNull();
    });

    it("SUPPLEMENTARY lookup resolves the supplementary route at its agreed price", () => {
        const result = computeTripBillingFromParts(
            { deliveredTimestamp: deliveredMs },
            task("WANGTHONGLANG12"),
            entries,
            noFuel,
            "SUPPLEMENTARY"
        );
        expect(result).not.toBeNull();
        expect(result!.finalRateThb).toBe(1250);
    });

    it("derivation: PRIMARY first, fall back to SUPPLEMENTARY", () => {
        const tripParts = { deliveredTimestamp: deliveredMs };
        // Primary route → resolves on the primary pass.
        const primary =
            computeTripBillingFromParts(tripParts, task("SOCE"), entries, noFuel, "PRIMARY") ??
            computeTripBillingFromParts(tripParts, task("SOCE"), entries, noFuel, "SUPPLEMENTARY");
        expect(primary!.finalRateThb).toBe(1000);
        // Supplementary route → null on primary, resolves on the fallback.
        const supp =
            computeTripBillingFromParts(tripParts, task("WANGTHONGLANG12"), entries, noFuel, "PRIMARY") ??
            computeTripBillingFromParts(tripParts, task("WANGTHONGLANG12"), entries, noFuel, "SUPPLEMENTARY");
        expect(supp!.finalRateThb).toBe(1250);
    });

    it("a SUPPLEMENTARY lookup does not leak into PRIMARY pricing", () => {
        // The primary route still bills at its own rate regardless of the supplementary entry.
        const result = computeTripBillingFromParts(
            { deliveredTimestamp: deliveredMs },
            task("SOCE"),
            entries,
            noFuel,
            "PRIMARY"
        );
        expect(result!.finalRateThb).toBe(1000);
    });
});
