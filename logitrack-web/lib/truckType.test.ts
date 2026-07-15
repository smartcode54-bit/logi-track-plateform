import { describe, it, expect } from "vitest";
import { taskTruckTypeFromTruckDoc, truckMatchesTaskTruckType } from "./truckType";

describe("taskTruckTypeFromTruckDoc", () => {
    it("maps every type the truck form offers", () => {
        expect(taskTruckTypeFromTruckDoc("Pickup")).toBe("4W");
        expect(taskTruckTypeFromTruckDoc("4 Wheels Jumbo")).toBe("4WJ");
        expect(taskTruckTypeFromTruckDoc("6 Wheels")).toBe("6WH");
        expect(taskTruckTypeFromTruckDoc("10 Wheels")).toBe("10WH");
        expect(taskTruckTypeFromTruckDoc("18 Wheels")).toBe("18WH");
        expect(taskTruckTypeFromTruckDoc("Van")).toBe("VAN");
    });

    it("maps the legacy '4 Wheels' type to 4WJ", () => {
        expect(taskTruckTypeFromTruckDoc("4 Wheels")).toBe("4WJ");
    });

    it("passes through truck docs that already store the abbreviation", () => {
        expect(taskTruckTypeFromTruckDoc("6WH")).toBe("6WH");
        expect(taskTruckTypeFromTruckDoc("van")).toBe("VAN");
    });

    it("returns undefined rather than guessing a class", () => {
        // A guess here would silently bill the trip against the wrong rate card.
        expect(taskTruckTypeFromTruckDoc("")).toBeUndefined();
        expect(taskTruckTypeFromTruckDoc(undefined)).toBeUndefined();
        expect(taskTruckTypeFromTruckDoc("Spaceship")).toBeUndefined();
    });
});

describe("truckMatchesTaskTruckType", () => {
    it("filters the plate list by the selected type", () => {
        expect(truckMatchesTaskTruckType("6 Wheels", "6WH")).toBe(true);
        expect(truckMatchesTaskTruckType("Pickup", "6WH")).toBe(false);
    });

    it("treats both 4-wheel spellings as 4WJ", () => {
        expect(truckMatchesTaskTruckType("4 Wheels", "4WJ")).toBe(true);
        expect(truckMatchesTaskTruckType("4 Wheels Jumbo", "4WJ")).toBe(true);
    });

    it("matches everything when no type is selected yet", () => {
        expect(truckMatchesTaskTruckType("6 Wheels", undefined)).toBe(true);
    });

    it("excludes trucks with an unrecognised type from a typed list", () => {
        expect(truckMatchesTaskTruckType("Spaceship", "6WH")).toBe(false);
    });
});
