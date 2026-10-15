import { describe, expect, it, vi } from "vitest";
import en from "@/context/locales/en/trucks";
import th from "@/context/locales/th/trucks";
import { truckFuelLabel, truckTypeLabel } from "./labels";

// List/renewal labels must stay usable without loading the runtime validation stack.
vi.mock("zod", () => { throw new Error("Truck labels must not load Zod"); });

describe.each([en, th])("truck class labels", (dictionary) => {
    const t = (key: string) => dictionary[key as keyof typeof dictionary] ?? key;
    it.each([
        ["Pickup", "4W"], ["4W", "4W"],
        ["4 Wheels", "4WJ"], ["4-wheel", "4WJ"], ["4 Wheels Jumbo", "4WJ"],
        ["6 Wheels", "6WH"], ["6WH", "6WH"], ["6w", "6WH"],
        ["10 Wheels", "10WH"], ["10WH", "10WH"], ["10-wheel", "10WH"],
        ["18 Wheels", "18WH"], ["18WH", "18WH"],
        ["4wj", "4WJ"], ["Van", "VAN"], ["  van  ", "VAN"],
    ])("labels %s as %s", (raw, expected) => {
        expect(truckTypeLabel(raw, t)).toBe(expected);
    });
    it("preserves free text and localizes missing values", () => {
        expect(truckTypeLabel("Special vehicle", t)).toBe("Special vehicle");
        for (const value of [undefined, null, ""]) {
            expect(truckTypeLabel(value, t)).toBe(t("trucks.ui4.notAvailable"));
            expect(truckFuelLabel(value, t)).toBe(t("trucks.ui4.notAvailable"));
        }
    });
});
