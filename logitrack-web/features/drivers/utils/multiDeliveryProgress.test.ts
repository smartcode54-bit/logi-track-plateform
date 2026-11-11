import { describe, expect, it } from "vitest";
import type { DeliveryStopProgress } from "@/validate/tripRecordSchema";
import { mergeStopsForDisplay } from "./multiDeliveryProgress";

const plan = [
    { index: 2, destination: "B" },
    { index: 1, destination: "A" },
    { index: 3, destination: "C" },
];

describe("mergeStopsForDisplay", () => {
    it("shows every planned stop as pending before anything is delivered", () => {
        const out = mergeStopsForDisplay(undefined, plan);
        expect(out.map((s) => [s.index, s.destination, s.status])).toEqual([
            [1, "A", "pending"],
            [2, "B", "pending"],
            [3, "C", "pending"],
        ]);
    });

    it("overlays delivered progress on the plan by index", () => {
        const progress = [{ index: 1, destination: "A", status: "delivered" }] as DeliveryStopProgress[];
        const out = mergeStopsForDisplay(progress, plan);
        expect(out.map((s) => s.status)).toEqual(["delivered", "pending", "pending"]);
        expect(out[0]).toBe(progress[0]);
    });

    it("keeps progress entries the plan does not know about", () => {
        const progress = [{ index: 4, destination: "D", status: "delivered" }] as DeliveryStopProgress[];
        expect(mergeStopsForDisplay(progress, plan).map((s) => s.index)).toEqual([1, 2, 3, 4]);
    });

    it("returns progress unchanged when there is no plan", () => {
        const progress = [{ index: 1, destination: "A", status: "delivered" }] as DeliveryStopProgress[];
        expect(mergeStopsForDisplay(progress, undefined)).toBe(progress);
        expect(mergeStopsForDisplay(undefined, [])).toEqual([]);
    });
});
