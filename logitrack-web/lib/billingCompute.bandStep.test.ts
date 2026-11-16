import { describe, expect, it } from "vitest";
import {
    DEFAULT_FUEL_BAND_STEP_PARAMS,
    computeMultiDeliveryBilling,
    computeTripBillingFromParts,
    diagnoseRoundPricingMismatch,
    parseBandStepParams,
    priceAtBandStep,
    priceRateEntryForRound,
    resolveBillingRoundProvenance,
    stepBandIndex,
    stepBandRange,
    type BillingRateEntry,
    type FuelRateAdjustment,
} from "./billingCompute";

/**
 * ADR 0029 — CJSF zone 03 (`เรทน้ำมันเขต 03 WRT.xlsx`). Every row of the contract sheet, bands
 * 32-34 … 52-54 (steps −5 … +5), as Excel computed them. Base = the 42-44 column.
 */
const CJSF_ZONE_03_SHEET: { route: string; prices: number[] }[] = [
    { route: "ยานนาวา14/16 21:30", prices: [1005, 1010, 1015, 1020, 1025, 1030, 1035, 1040, 1045, 1050, 1055] },
    { route: "ยานนาวา14/16 06:00", prices: [1005, 1010, 1015, 1020, 1025, 1030, 1035, 1040, 1045, 1050, 1055] },
    { route: "สาทร05 03:00", prices: [895, 900, 905, 910, 915, 922.5, 930, 935, 940, 945, 950] },
    { route: "สาทร05 00:00", prices: [895, 900, 905, 910, 915, 922.5, 930, 935, 940, 945, 950] },
    { route: "สาทร05 09:00", prices: [895, 900, 905, 910, 915, 922.5, 930, 935, 940, 945, 950] },
    { route: "ห้วยขวาง10 04:00", prices: [915, 920, 925, 930, 935, 942.5, 950, 955, 960, 965, 970] },
    { route: "ห้วยขวาง10 09:00", prices: [915, 920, 925, 930, 935, 942.5, 950, 955, 960, 965, 970] },
    { route: "วัฒนา12/15 22:00", prices: [915, 920, 925, 930, 935, 942.5, 950, 955, 960, 965, 970] },
    { route: "ลาดกระบัง26 08:00", prices: [915, 920, 925, 930, 935, 942.5, 950, 955, 960, 965, 970] },
    { route: "บึงกุ่ม11 00:00", prices: [915, 920, 925, 930, 935, 942.5, 950, 955, 960, 965, 970] },
];
const ANCHOR_INDEX = 5; // the 42-44 column

const P = DEFAULT_FUEL_BAND_STEP_PARAMS;

describe("priceAtBandStep reproduces the CJSF sheet (ADR 0029)", () => {
    it("matches all 110 cells of the zone-03 sheet", () => {
        let cells = 0;
        for (const row of CJSF_ZONE_03_SHEET) {
            const base = row.prices[ANCHOR_INDEX];
            row.prices.forEach((expected, i) => {
                const step = i - ANCHOR_INDEX;
                expect(
                    priceAtBandStep(base, step, P.stepBasisPoints, P.roundToThb),
                    `${row.route} step ${step}`
                ).toBe(expected);
                cells += 1;
            });
        }
        expect(cells).toBe(110);
    });

    it("keeps an off-grid base price unchanged at step 0", () => {
        expect(priceAtBandStep(922.5, 0, 70, 5)).toBe(922.5);
    });

    it("compounds from the previous ROUNDED step, not base × (1+r)^n", () => {
        // 1030 × 1.007² = 1044.5 → 1045 un-chained; the sheet chains 1030 → 1035 → 1040.
        expect(priceAtBandStep(1030, 2, 70, 5)).toBe(1040);
    });

    it("rounds an exact tie half-up like Excel (float would give 2515)", () => {
        // 2500 × 1.007 = 2517.5 exactly → ROUND(503.5) = 504 → 2520.
        expect(priceAtBandStep(2500, 1, 70, 5)).toBe(2520);
    });

    it("extends the rule past the printed table", () => {
        expect(priceAtBandStep(1030, 6, 70, 5)).toBe(1060);
        expect(priceAtBandStep(1030, -6, 70, 5)).toBe(1000);
    });

    it("refuses nonsense input instead of guessing", () => {
        expect(priceAtBandStep(1030, 1.5, 70, 5)).toBeNaN();
        expect(priceAtBandStep(-1, 1, 70, 5)).toBeNaN();
        expect(priceAtBandStep(1030, 1, 0.7, 5)).toBeNaN();
        expect(priceAtBandStep(1030, 1, 70, 0)).toBeNaN();
    });
});

describe("stepBandIndex — bands are 42.01–44.00, upper-inclusive", () => {
    const idx = (price: number) => stepBandIndex(price, 42, 2);
    it("puts each edge in the right band", () => {
        expect(idx(42.01)).toBe(0);
        expect(idx(43)).toBe(0);
        expect(idx(44.0)).toBe(0);
        expect(idx(44.01)).toBe(1);
        expect(idx(42.0)).toBe(-1);
        expect(idx(32.01)).toBe(-5);
        expect(idx(31.99)).toBe(-6);
        expect(idx(54.0)).toBe(5);
        expect(idx(54.01)).toBe(6);
    });

    it("returns the inclusive bounds of a step", () => {
        expect(stepBandRange(0, 42, 2)).toEqual({ lowerThb: 42.01, upperThb: 44 });
        expect(stepBandRange(1, 42, 2)).toEqual({ lowerThb: 44.01, upperThb: 46 });
        expect(stepBandRange(-5, 42, 2)).toEqual({ lowerThb: 32.01, upperThb: 34 });
    });
});

describe("parseBandStepParams", () => {
    it("accepts the CJSF terms and rejects broken maps", () => {
        expect(parseBandStepParams({ anchorBandLowerThb: 42, bandWidthThb: 2, stepBasisPoints: 70, roundToThb: 5 })).toEqual(P);
        expect(parseBandStepParams(null)).toBeNull();
        expect(parseBandStepParams({ ...P, stepBasisPoints: 0.7 })).toBeNull();
        expect(parseBandStepParams({ ...P, bandWidthThb: 0 })).toBeNull();
    });
});

const day = (s: string) => new Date(`${s}T00:00:00+07:00`).getTime();

const finalEntry: BillingRateEntry = {
    id: "final-old",
    customerId: "cjsf",
    importId: "rc_old",
    hubId: "SPK-GW",
    destinationCode: "SPK890103",
    vehicleClass: "4WJ",
    rateThb: 1200,
    effectiveFromMs: day("2026-06-01"),
};
const baseEntry: BillingRateEntry = {
    ...finalEntry,
    id: "base-new",
    importId: "rc_new",
    rateThb: 942.5,
    effectiveFromMs: day("2026-12-01"),
    priceBasis: "base",
};
const legacyRound: FuelRateAdjustment = {
    id: "legacy-50",
    customerId: "cjsf",
    effectiveFromMs: day("2026-08-01"),
    rateMultiplier: 1,
    addThbPerTrip: -50,
};
const stepRound = (step: number, effective = "2026-12-01", id = `step${step}`): FuelRateAdjustment => ({
    id,
    customerId: "cjsf",
    effectiveFromMs: day(effective),
    rateMultiplier: 1,
    addThbPerTrip: 0,
    referenceFuelPriceThb: 45,
    pricingModel: "base_band_step",
    bandStep: step,
    bandStepParams: P,
});
const task = {
    sourceHub: "SPK-GW",
    destination: "SPK890103",
    truckType: "4WJ",
    billingCustomerId: "cjsf",
};

describe("priceRateEntryForRound — basis/round guard (ADR 0029)", () => {
    it("steps a base price", () => {
        expect(priceRateEntryForRound(baseEntry, stepRound(1))).toEqual({
            ok: true,
            finalRateThb: 950,
            rateMultiplier: 1,
            addThbPerTrip: 7.5,
        });
    });

    it("prices a base row with no round at the base price", () => {
        expect(priceRateEntryForRound(baseEntry, null)).toMatchObject({ ok: true, finalRateThb: 942.5 });
    });

    it("refuses a band-step round on a final-price row", () => {
        expect(priceRateEntryForRound(finalEntry, stepRound(1))).toEqual({
            ok: false,
            reason: "final_rate_with_band_step_round",
        });
    });

    it("refuses a legacy round on a base-price row", () => {
        expect(priceRateEntryForRound(baseEntry, legacyRound)).toEqual({
            ok: false,
            reason: "base_rate_with_legacy_round",
        });
    });

    it("refuses a band-step round that lost its step", () => {
        const broken = { ...stepRound(1), bandStep: undefined };
        expect(priceRateEntryForRound(baseEntry, broken)).toEqual({ ok: false, reason: "invalid_band_step_round" });
    });

    it("leaves legacy pricing exactly as before", () => {
        expect(priceRateEntryForRound(finalEntry, legacyRound)).toMatchObject({ ok: true, finalRateThb: 1150 });
    });
});

describe("computeTripBillingFromParts with band-step rounds (ADR 0029)", () => {
    const entries = [finalEntry, baseEntry];
    const rounds = [legacyRound, stepRound(1)];

    it("prices a trip after the cutover from the base row and the step round", () => {
        const r = computeTripBillingFromParts({ deliveredTimestamp: day("2026-12-05") }, task, entries, rounds);
        expect(r).not.toBeNull();
        expect(r!.baseRateThb).toBe(942.5);
        expect(r!.finalRateThb).toBe(950);
        expect(r!.rateMultiplier).toBe(1);
        expect(r!.addThbPerTrip).toBe(7.5);
        expect(r!.baseRateThb * r!.rateMultiplier + r!.addThbPerTrip).toBe(r!.finalRateThb);
        expect(r!.fuelPricingModel).toBe("base_band_step");
        expect(r!.fuelBandStep).toBe(1);
        expect(r!.fuelStepBasisPoints).toBe(70);
        expect(r!.fuelBandLowerThb).toBe(44.01);
        expect(r!.fuelBandUpperThb).toBe(46);
        expect(r!.roundEffectiveFromDateStr).toBe("2026-12-01");
    });

    it("keeps trips before the cutover on the final price and legacy round", () => {
        const r = computeTripBillingFromParts({ deliveredTimestamp: day("2026-11-20") }, task, entries, rounds);
        expect(r!.baseRateThb).toBe(1200);
        expect(r!.finalRateThb).toBe(1150);
        expect(r!.fuelPricingModel).toBeUndefined();
    });

    it("fails loud when a route was never re-imported as a base price", () => {
        const r = computeTripBillingFromParts({ deliveredTimestamp: day("2026-12-05") }, task, [finalEntry], rounds);
        expect(r).toBeNull();
        expect(
            diagnoseRoundPricingMismatch({ deliveredTimestamp: day("2026-12-05") }, task, [finalEntry], rounds)
        ).toBe("final_rate_with_band_step_round");
    });

    it("fails loud when the base rate card exists but no step round was announced yet", () => {
        const r = computeTripBillingFromParts({ deliveredTimestamp: day("2026-12-05") }, task, entries, [legacyRound]);
        expect(r).toBeNull();
        expect(
            diagnoseRoundPricingMismatch({ deliveredTimestamp: day("2026-12-05") }, task, entries, [legacyRound])
        ).toBe("base_rate_with_legacy_round");
    });

    it("diagnoses nothing for a route that simply has no rate", () => {
        expect(
            diagnoseRoundPricingMismatch(
                { deliveredTimestamp: day("2026-12-05") },
                { ...task, destination: "SPK999999" },
                entries,
                rounds
            )
        ).toBeNull();
    });

    it("never fuel-adjusts a เสริม base row", () => {
        const supp: BillingRateEntry = { ...baseEntry, id: "supp", rateThb: 950, jobCategory: "SUPPLEMENTARY" };
        const r = computeTripBillingFromParts(
            { deliveredTimestamp: day("2026-12-05") },
            task,
            [supp],
            rounds,
            "SUPPLEMENTARY"
        );
        expect(r!.finalRateThb).toBe(950);
        expect(r!.fuelAdjustmentId).toBeUndefined();
    });
});

describe("computeMultiDeliveryBilling with band-step rounds (ADR 0029)", () => {
    const stops = [
        { index: 1, destination: "SPK890103" },
        { index: 2, destination: "SPK890049" },
    ];
    const sathornBase: BillingRateEntry = { ...baseEntry, id: "base-sathorn", destinationCode: "SPK890049", rateThb: 922.5 };

    it("steps only the base leg in flat extra-stop mode", () => {
        const r = computeMultiDeliveryBilling(
            { deliveredTimestamp: day("2026-12-05") },
            task,
            stops,
            "4WJ",
            [baseEntry, sathornBase],
            [stepRound(1)],
            100
        );
        expect(r!.baseRateThb).toBe(950);
        expect(r!.stopChargeThb).toBe(100);
        expect(r!.totalBillingThb).toBe(1050);
        expect(r!.addThbPerTrip).toBe(7.5);
        expect(r!.fuelPricingModel).toBe("base_band_step");
    });

    it("steps every leg in per-route mode", () => {
        const r = computeMultiDeliveryBilling(
            { deliveredTimestamp: day("2026-12-05") },
            task,
            stops,
            "4WJ",
            [baseEntry, sathornBase],
            [stepRound(1)]
        );
        expect(r!.baseRateThb).toBe(950);
        expect(r!.stopChargeThb).toBe(930);
    });

    it("fails the whole trip when one leg is still a final price", () => {
        const sathornFinal: BillingRateEntry = { ...finalEntry, id: "final-sathorn", destinationCode: "SPK890049" };
        const r = computeMultiDeliveryBilling(
            { deliveredTimestamp: day("2026-12-05") },
            task,
            stops,
            "4WJ",
            [baseEntry, sathornFinal],
            [stepRound(1)]
        );
        expect(r).toBeNull();
    });
});

describe("resolveBillingRoundProvenance prints the ฿2.00 band of a step round", () => {
    it("uses the step frozen on the round, not the ฿1.00 rule", () => {
        const prov = resolveBillingRoundProvenance(day("2026-12-01"), stepRound(-1));
        expect(prov.fuelBandLowerThb).toBe(40.01);
        expect(prov.fuelBandUpperThb).toBe(42);
        expect(prov.fuelBandStep).toBe(-1);
    });
});
