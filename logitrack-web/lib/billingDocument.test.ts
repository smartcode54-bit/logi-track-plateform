import { describe, expect, it } from "vitest";
import {
    collectBillingRounds,
    formatFuelBand,
    groupToLineItems,
    type BillingTripRow,
} from "./billingDocument";

/**
 * Regression cover for ADR 0009 §6-7. Both defects these tests pin were shipped and found by hand:
 * multidrop rows reached the invoice with no round, and two rounds that happened to share a unit
 * price merged into one line carrying an arbitrary round label.
 */

function trip(over: Partial<BillingTripRow> = {}): BillingTripRow {
    return {
        id: "t1",
        billingEstimateThb: 1200,
        vehicleClass: "4WJ",
        hubDisplayName: "SPK-GW",
        destinationDisplayName: "ลาดกระบัง",
        deliveredTimestamp: new Date("2026-08-05T03:00:00Z"),
        rowType: "trip",
        ...over,
    };
}

describe("collectBillingRounds", () => {
    it("labels rounds by date order, oldest first", () => {
        const rounds = collectBillingRounds([
            trip({ id: "a", billingRoundEffectiveFromDateStr: "2026-08-16" }),
            trip({ id: "b", billingRoundEffectiveFromDateStr: "2026-08-01" }),
            trip({ id: "c", billingRoundEffectiveFromDateStr: "2026-08-25" }),
        ]);
        expect(rounds.map((r) => [r.label, r.effectiveFromDateStr])).toEqual([
            ["R1", "2026-08-01"],
            ["R2", "2026-08-16"],
            ["R3", "2026-08-25"],
        ]);
    });

    it("ignores rows that carry no round, so legacy trips cannot invent one", () => {
        expect(collectBillingRounds([trip(), trip()])).toEqual([]);
    });

    it("widens a round's delivery span across its rows", () => {
        const rounds = collectBillingRounds([
            trip({
                id: "a",
                billingRoundEffectiveFromDateStr: "2026-08-01",
                deliveredTimestamp: new Date("2026-08-05T03:00:00Z"),
            }),
            trip({
                id: "b",
                billingRoundEffectiveFromDateStr: "2026-08-01",
                deliveredTimestamp: new Date("2026-08-12T03:00:00Z"),
            }),
        ]);
        expect(rounds).toHaveLength(1);
        expect(rounds[0].firstDeliveredAt?.toISOString()).toBe("2026-08-05T03:00:00.000Z");
        expect(rounds[0].lastDeliveredAt?.toISOString()).toBe("2026-08-12T03:00:00.000Z");
    });
});

describe("formatFuelBand", () => {
    it("prints the contract's inclusive range", () => {
        expect(formatFuelBand(37.01, 38)).toBe("37.01–38.00");
    });

    it("prints a dash rather than half a range", () => {
        expect(formatFuelBand(undefined, 38)).toBe("-");
        expect(formatFuelBand(37.01, undefined)).toBe("-");
    });
});

describe("groupToLineItems", () => {
    it("keeps count × unitPrice = total on every line", () => {
        const items = groupToLineItems([
            trip({ id: "a", billingEstimateThb: 1200 }),
            trip({ id: "b", billingEstimateThb: 1200 }),
        ]);
        expect(items).toHaveLength(1);
        expect(items[0].count).toBe(2);
        expect(items[0].count * items[0].unitPrice).toBe(items[0].total);
    });

    it("never merges two rounds into one line, even at an identical unit price", () => {
        // The defect: a round that only moved OTHER routes leaves this route's price unchanged,
        // so price alone put both rounds on one line under whichever label came first.
        const rounds = collectBillingRounds([
            trip({ id: "a", billingRoundEffectiveFromDateStr: "2026-08-01" }),
            trip({ id: "b", billingRoundEffectiveFromDateStr: "2026-08-16" }),
        ]);
        const items = groupToLineItems(
            [
                trip({ id: "a", billingEstimateThb: 1200, billingRoundEffectiveFromDateStr: "2026-08-01" }),
                trip({ id: "b", billingEstimateThb: 1200, billingRoundEffectiveFromDateStr: "2026-08-16" }),
            ],
            rounds
        );
        expect(items).toHaveLength(2);
        expect(items.map((i) => i.roundLabel).sort()).toEqual(["R1", "R2"]);
        items.forEach((i) => expect(i.count * i.unitPrice).toBe(i.total));
    });

    it("splits a route priced differently in two rounds", () => {
        const trips = [
            trip({ id: "a", billingEstimateThb: 1200, billingRoundEffectiveFromDateStr: "2026-08-01" }),
            trip({ id: "b", billingEstimateThb: 1210, billingRoundEffectiveFromDateStr: "2026-08-16" }),
        ];
        const items = groupToLineItems(trips, collectBillingRounds(trips));
        expect(items).toHaveLength(2);
        expect(items.map((i) => i.unitPrice).sort()).toEqual([1200, 1210]);
    });

    it("labels a multidrop stop with its round like any other row", () => {
        const trips = [
            trip({
                id: "t1_s2",
                rowType: "multidrop_stop",
                billingEstimateThb: 300,
                billingRoundEffectiveFromDateStr: "2026-08-16",
            }),
        ];
        const items = groupToLineItems(trips, collectBillingRounds(trips));
        expect(items).toHaveLength(1);
        expect(items[0].route).toBe("ค่าโยก");
        expect(items[0].roundLabel).toBe("R1");
    });

    it("leaves the round label empty when a row carries no round", () => {
        const items = groupToLineItems([trip()], []);
        expect(items[0].roundLabel).toBe("");
    });
});
