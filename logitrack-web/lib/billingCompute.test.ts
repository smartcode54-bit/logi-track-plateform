import { describe, expect, it } from "vitest";
import {
    bangkokDateStrFromMillis,
    computeFinalRateThb,
    computeFuelSurchargeThb,
    computeTripBillingFromParts,
    fuelBandFloor,
    fuelBandRange,
    normalizeVehicleClass,
    resolveBillingRoundProvenance,
    selectBillingRateEntry,
    selectFuelAdjustmentForBillingDate,
    type BillingRateEntry,
    type FuelRateAdjustment,
} from "./billingCompute";

describe("normalizeVehicleClass", () => {
    it("folds the truck master's names onto the class a task carries", () => {
        expect(normalizeVehicleClass("Pickup")).toBe("4W");
        expect(normalizeVehicleClass("4 Wheels Jumbo")).toBe("4WJ");
        expect(normalizeVehicleClass("6 Wheels")).toBe("6WH");
        expect(normalizeVehicleClass("10 Wheels")).toBe("10WH");
        expect(normalizeVehicleClass("18 Wheels")).toBe("18WH");
    });

    it("folds the codes an earlier normalize pass produced", () => {
        // These matched no task: the enum has 6WH/10WH, never 6W/10W.
        expect(normalizeVehicleClass("6W")).toBe("6WH");
        expect(normalizeVehicleClass("10W")).toBe("10WH");
    });

    it("folds the pre-2026-07 names for 4W", () => {
        expect(normalizeVehicleClass("PICKUP")).toBe("4W");
        expect(normalizeVehicleClass("4WH")).toBe("4W");
    });

    it("leaves a current class untouched, so both sides of a lookup agree", () => {
        for (const code of ["4W", "4WJ", "6WH", "10WH", "18WH", "VAN"]) {
            expect(normalizeVehicleClass(code)).toBe(code);
        }
    });

    it("defaults a missing class to 4WJ", () => {
        expect(normalizeVehicleClass("")).toBe("4WJ");
        expect(normalizeVehicleClass(null)).toBe("4WJ");
    });
});

describe("computeFinalRateThb", () => {
    it("rounds to 2 decimal places like billing snapshot", () => {
        expect(computeFinalRateThb(100, 1.05, 0)).toBe(105);
        expect(computeFinalRateThb(100, 1.055, 12.345)).toBe(Math.round((100 * 1.055 + 12.345) * 100) / 100);
        expect(computeFinalRateThb(333.33, 1.01, 0.005)).toBe(Math.round((333.33 * 1.01 + 0.005) * 100) / 100);
    });
});

describe("fuelBandFloor (ADR 0009 §3 — the band is (n, n+1])", () => {
    it("keeps a price at the exact top of a band inside that band", () => {
        // The whole point: Math.floor(42.00) === 42 put this one band too high and overcharged
        // every trip of the round.
        expect(fuelBandFloor(42.0)).toBe(41);
        expect(fuelBandFloor(37.0)).toBe(36);
    });

    it("moves to the next band one satang later", () => {
        expect(fuelBandFloor(42.01)).toBe(42);
        expect(fuelBandFloor(36.01)).toBe(36);
    });

    it("agrees with the old floor behaviour everywhere inside a band", () => {
        expect(fuelBandFloor(42.5)).toBe(42);
        expect(fuelBandFloor(42.99)).toBe(42);
        expect(fuelBandFloor(36.5)).toBe(36);
    });

    it("is exact on values that are not representable in binary floating point", () => {
        // 0.1 + 0.2 style drift: 41.1 * 100 is 4109.999… before rounding.
        expect(fuelBandFloor(41.1)).toBe(41);
        expect(fuelBandFloor(41.7)).toBe(41);
        expect(fuelBandFloor(43.29)).toBe(43);
    });

    it("returns NaN rather than a plausible number for junk input", () => {
        expect(fuelBandFloor(Number.NaN)).toBeNaN();
        expect(fuelBandFloor(Number.POSITIVE_INFINITY)).toBeNaN();
    });
});

describe("fuelBandRange", () => {
    it("reports the inclusive bounds the contract is written in", () => {
        expect(fuelBandRange(42.0)).toEqual({ lowerThb: 41.01, upperThb: 42 });
        expect(fuelBandRange(42.01)).toEqual({ lowerThb: 42.01, upperThb: 43 });
        expect(fuelBandRange(36.5)).toEqual({ lowerThb: 36.01, upperThb: 37 });
    });

    it("is null when there is no price to derive a band from", () => {
        expect(fuelBandRange(Number.NaN)).toBeNull();
    });
});

describe("computeFuelSurchargeThb", () => {
    // baselineBandFloor 41 means the band 41.01–42.00 carries +0.
    it("charges nothing at the baseline band, including its top edge", () => {
        expect(computeFuelSurchargeThb(41.5, 41, 10)).toBe(0);
        expect(computeFuelSurchargeThb(42.0, 41, 10)).toBe(0);
    });

    it("charges one step per band above the baseline", () => {
        expect(computeFuelSurchargeThb(42.01, 41, 10)).toBe(10);
        expect(computeFuelSurchargeThb(43.0, 41, 10)).toBe(10);
        expect(computeFuelSurchargeThb(43.01, 41, 10)).toBe(20);
    });

    it("discounts below the baseline — the adjustment is symmetric and never clamped", () => {
        expect(computeFuelSurchargeThb(40.5, 41, 10)).toBe(-10); // band 40.01–41.00, one below
        // 39.00 is the TOP of band 38.01–39.00, i.e. three bands below 41.01–42.00 — not two.
        expect(computeFuelSurchargeThb(39.0, 41, 10)).toBe(-30);
        expect(computeFuelSurchargeThb(39.01, 41, 10)).toBe(-20); // band 39.01–40.00
    });
});

describe("bangkokDateStrFromMillis", () => {
    it("reports the Bangkok calendar date, not the UTC one", () => {
        // Bangkok midnight of 16 Aug 2026 is 17:00Z on 15 Aug — toISOString would say "2026-08-15".
        expect(bangkokDateStrFromMillis(Date.UTC(2026, 7, 15, 17, 0, 0))).toBe("2026-08-16");
    });

    it("holds at both ends of a Bangkok day", () => {
        expect(bangkokDateStrFromMillis(Date.UTC(2026, 7, 15, 17, 0, 0))).toBe("2026-08-16");
        expect(bangkokDateStrFromMillis(Date.UTC(2026, 7, 16, 16, 59, 59))).toBe("2026-08-16");
        expect(bangkokDateStrFromMillis(Date.UTC(2026, 7, 16, 17, 0, 0))).toBe("2026-08-17");
    });
});

describe("voided announcements are never selected (ADR 0009 §1)", () => {
    const base = {
        customerId: "cust1",
        importId: "imp1",
        hubId: "HUB",
        destinationCode: "DEST",
        vehicleClass: "4WJ",
    };
    const jan = Date.UTC(2026, 0, 1);
    const feb = Date.UTC(2026, 1, 1);
    const billDate = Date.UTC(2026, 2, 1);

    it("falls back to the previous round when the newest one is voided", () => {
        const entries: BillingRateEntry[] = [
            { ...base, id: "r1", rateThb: 1000, effectiveFromMs: jan },
            { ...base, id: "r2", rateThb: 1200, effectiveFromMs: feb, voided: true },
        ];
        expect(selectBillingRateEntry("cust1", "HUB", "DEST", "4WJ", billDate, entries)?.id).toBe("r1");
    });

    it("ignores a voided fuel adjustment", () => {
        const adjustments: FuelRateAdjustment[] = [
            { id: "a1", customerId: "cust1", effectiveFromMs: jan, rateMultiplier: 1, addThbPerTrip: 10 },
            {
                id: "a2",
                customerId: "cust1",
                effectiveFromMs: feb,
                rateMultiplier: 1,
                addThbPerTrip: 20,
                voided: true,
            },
        ];
        expect(selectFuelAdjustmentForBillingDate("cust1", billDate, adjustments)?.id).toBe("a1");
    });
});

describe("resolveBillingRoundProvenance (ADR 0009 §4)", () => {
    const adj = (ms: number, price?: number): FuelRateAdjustment => ({
        id: `a${ms}`,
        customerId: "cust1",
        effectiveFromMs: ms,
        rateMultiplier: 1,
        addThbPerTrip: 0,
        referenceFuelPriceThb: price,
    });

    it("keeps two fuel rounds distinct even when the rate card was imported after both", () => {
        // The defect this pins: with max(rateEntry, adjustment) a rate card imported on 20 Jul
        // stamped its own date on both rounds, collapsing them to one and hiding the legend.
        const rateEntryImportedLate = Date.UTC(2026, 6, 19, 17, 0, 0); // 20 Jul, Bangkok
        const r1 = resolveBillingRoundProvenance(
            rateEntryImportedLate,
            adj(Date.UTC(2026, 5, 30, 17, 0, 0), 34.5) // 1 Jul
        );
        const r2 = resolveBillingRoundProvenance(
            rateEntryImportedLate,
            adj(Date.UTC(2026, 6, 15, 17, 0, 0), 36.5) // 16 Jul
        );
        expect(r1.roundEffectiveFromDateStr).toBe("2026-07-01");
        expect(r2.roundEffectiveFromDateStr).toBe("2026-07-16");
        expect(r1.roundEffectiveFromDateStr).not.toBe(r2.roundEffectiveFromDateStr);
    });

    it("carries the band derived from the announced price", () => {
        const p = resolveBillingRoundProvenance(0, adj(Date.UTC(2026, 6, 15, 17, 0, 0), 34.5));
        expect(p.fuelBandLowerThb).toBe(34.01);
        expect(p.fuelBandUpperThb).toBe(35);
        expect(p.referenceFuelPriceThb).toBe(34.5);
    });

    it("falls back to the rate entry only when no fuel adjustment applied", () => {
        const p = resolveBillingRoundProvenance(Date.UTC(2026, 6, 19, 17, 0, 0), null);
        expect(p.roundEffectiveFromDateStr).toBe("2026-07-20");
        expect(p.fuelBandLowerThb).toBeUndefined();
    });

    it("reports no band when the round was announced without a reference price", () => {
        const p = resolveBillingRoundProvenance(0, adj(Date.UTC(2026, 6, 15, 17, 0, 0)));
        expect(p.roundEffectiveFromDateStr).toBe("2026-07-16");
        expect(p.fuelBandLowerThb).toBeUndefined();
        expect(p.referenceFuelPriceThb).toBeUndefined();
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

    it("SUPPLEMENTARY is a fixed rate — a fuel adjustment does NOT change it", () => {
        const fuel: FuelRateAdjustment[] = [
            {
                id: "f1",
                customerId: "c1",
                effectiveFromMs: new Date("2020-01-01").getTime(),
                rateMultiplier: 1.1,
                addThbPerTrip: 50,
            },
        ];
        const supp = computeTripBillingFromParts(
            { deliveredTimestamp: deliveredMs },
            task("WANGTHONGLANG12"),
            entries,
            fuel,
            "SUPPLEMENTARY"
        );
        expect(supp!.finalRateThb).toBe(1250);
        expect(supp!.rateMultiplier).toBe(1);
        expect(supp!.addThbPerTrip).toBe(0);
        expect(supp!.fuelAdjustmentId).toBeUndefined();

        // Same fuel rule still applies normally to a PRIMARY route.
        const primary = computeTripBillingFromParts(
            { deliveredTimestamp: deliveredMs },
            task("SOCE"),
            entries,
            fuel,
            "PRIMARY"
        );
        expect(primary!.finalRateThb).toBe(computeFinalRateThb(1000, 1.1, 50));
    });
});
