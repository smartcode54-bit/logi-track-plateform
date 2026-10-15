// T54R (issue #74): the pure parts of the dashboard over Go: the Bangkok period, the Cartrack batch
// written into ['vehicleLocations'], the incident flags of the map and the activity chart's paths.
import { describe, expect, it } from "vitest";

import { addDays, activeDrivers, dashboardSummaryKey, periodRange } from "./summary";
import { mergeVehiclePositions, parsePositions, type VehicleLocationDTO } from "./vehicleLocationEvents";
import { hasIncident, incidentTrucks } from "./incidentMarkers";
import { default as en } from "@/context/locales/en/dashboard";
import { default as th } from "@/context/locales/th/dashboard";
import { chartPaths } from "../components/DeliveryActivityChart";

function location(truckId: string, over: Partial<VehicleLocationDTO> = {}): VehicleLocationDTO {
    return {
        truckId,
        tenantId: "t-own",
        licensePlate: `plate-${truckId}`,
        lat: 13.7,
        lng: 100.5,
        speedKmh: 0,
        heading: null,
        lastGpsAt: "2026-10-12T01:00:00Z",
        updatedAt: "2026-10-12T01:00:05Z",
        ...over,
    };
}

describe("the dashboard period", () => {
    it("is Bangkok calendar days ending today, both included", () => {
        // 2026-10-11T18:30Z is already 12 Oct in Bangkok (UTC+7).
        const now = new Date("2026-10-11T18:30:00Z");
        expect(periodRange("week", now)).toEqual({ from: "2026-10-06", to: "2026-10-12" });
        expect(periodRange("month", now)).toEqual({ from: "2026-09-13", to: "2026-10-12" });
        // Before Bangkok midnight it is still the 11th.
        expect(periodRange("week", new Date("2026-10-11T16:59:59Z")).to).toBe("2026-10-11");
    });

    it("adds days across months and years without a time zone", () => {
        expect(addDays("2026-03-01", -1)).toBe("2026-02-28");
        expect(addDays("2028-03-01", -1)).toBe("2028-02-29");
        expect(addDays("2026-12-31", 1)).toBe("2027-01-01");
    });

    it("keys the summary by its range (W6)", () => {
        expect(dashboardSummaryKey({ from: "2026-10-06", to: "2026-10-12" })).toEqual(["dashboard", "summary", { from: "2026-10-06", to: "2026-10-12" }]);
    });

    it("counts active drivers as active + on duty, every driver when none is either (the old card's rule)", () => {
        expect(activeDrivers({ active: 3, onDuty: 2, inactive: 1, total: 6 })).toBe(5);
        expect(activeDrivers({ active: 0, onDuty: 0, inactive: 4, total: 4 })).toBe(4);
    });
});

describe("vehicle_locations.updated", () => {
    it("keeps only well-formed positions of the batch", () => {
        const at = "2026-10-12T01:03:00Z";
        expect(
            parsePositions({
                positions: [
                    { truckId: "a", lat: 13.8, lng: 100.6, speed: 42, heading: 90, at },
                    { truckId: "b", lat: "13", lng: 100, at },
                    { truckId: "", lat: 1, lng: 1, at },
                    { truckId: "c", lat: 95, lng: 100, at },
                    { truckId: "d", lat: 13, lng: 100, at: "yesterday" },
                    { truckId: "e", lat: 13, lng: 100, speed: "fast", heading: null, at },
                    null,
                ],
            })
        ).toEqual([
            { truckId: "a", lat: 13.8, lng: 100.6, speed: 42, heading: 90, at },
            { truckId: "e", lat: 13, lng: 100, speed: null, heading: null, at },
        ]);
        expect(parsePositions(null)).toEqual([]);
        expect(parsePositions({ positions: "x" })).toEqual([]);
    });

    it("moves each known truck to its newer fix and leaves the rest (latest only)", () => {
        const cached = [location("a"), location("b"), location("c", { lastGpsAt: null })];
        const next = mergeVehiclePositions(cached, [
            { truckId: "a", lat: 14, lng: 101, speed: 50, heading: 180, at: "2026-10-12T01:03:00Z" },
            { truckId: "b", lat: 1, lng: 1, speed: 1, heading: 1, at: "2026-10-12T00:59:00Z" }, // older: ignored
            { truckId: "c", lat: 15, lng: 102, speed: null, heading: null, at: "2026-10-12T01:03:00Z" },
            { truckId: "z", lat: 15, lng: 102, speed: null, heading: null, at: "2026-10-12T01:03:00Z" }, // not listed yet
        ]) as VehicleLocationDTO[];
        expect(next).toHaveLength(3);
        expect(next[0]).toMatchObject({ truckId: "a", lat: 14, lng: 101, speedKmh: 50, heading: 180, lastGpsAt: "2026-10-12T01:03:00Z", licensePlate: "plate-a" });
        expect(next[1]).toBe(cached[1]);
        expect(next[2]).toMatchObject({ truckId: "c", lat: 15, lastGpsAt: "2026-10-12T01:03:00Z" });
    });

    it("creates nothing without a cached list and keeps the reference when nothing changed", () => {
        const p = [{ truckId: "a", lat: 14, lng: 101, speed: null, heading: null, at: "2026-10-12T01:03:00Z" }];
        expect(mergeVehiclePositions(undefined, p)).toBeUndefined();
        const cached = [location("a", { lastGpsAt: "2026-10-12T02:00:00Z" })];
        expect(mergeVehiclePositions(cached, p)).toBe(cached);
        expect(mergeVehiclePositions(cached, [])).toBe(cached);
    });
});

describe("the map's incident flags", () => {
    it("names trucks by id and by plate", () => {
        const flagged = incidentTrucks([
            { id: "i1", truckId: "a", truckLicensePlate: null, createdAt: "" },
            { id: "i2", truckId: null, truckLicensePlate: " 70-1234 ", createdAt: "" },
            { id: "i3", truckId: null, truckLicensePlate: "", createdAt: "" },
        ]);
        expect([...flagged.ids]).toEqual(["a"]);
        expect([...flagged.plates]).toEqual(["70-1234"]);
        expect(hasIncident({ truckId: "a", licensePlate: null }, flagged.ids, flagged.plates)).toBe(true);
        expect(hasIncident({ truckId: "b", licensePlate: "70-1234" }, flagged.ids, flagged.plates)).toBe(true);
        expect(hasIncident({ truckId: "c", licensePlate: "" }, flagged.ids, flagged.plates)).toBe(false);
    });
});

describe("the activity chart", () => {
    it("draws a flat line without data and scales the highest day to the top", () => {
        expect(chartPaths([]).line).toBe("M0 180 L700 180");
        const { line, area } = chartPaths([0, 5, 10]);
        expect(line).toBe("M 20 180 L 350 100 L 680 20");
        expect(area).toBe(`${line} L 680 180 L 20 180 Z`);
    });
});


describe("the fleet map HTML", () => {
    for (const [language, dictionary] of [["en", en], ["th", th]] as const) {
        it(`uses theme tokens and escaped, readable status text in ${language}`, async () => {
            const { fleetMarkerHtml, fleetPopupHtml } = await import("../components/FleetMapClient");
            const t = (key: string) => dictionary[key as keyof typeof dictionary] ?? key;
            for (const engineOn of [true, false]) {
                const v = location("a", { licensePlate: '<plate>"&', engineOn, speedKmh: 42, odometerKm: 1234, fuelLevel: 8, fuelPercentage: 25 });
                const marker = fleetMarkerHtml(v.licensePlate!, engineOn, true);
                const popup = fleetPopupHtml(v, true, t);
                for (const html of [marker, popup]) {
                    expect(html).not.toMatch(/#[0-9a-f]{3,8}\b/i);
                    expect(html).not.toMatch(/\p{Extended_Pictographic}/u);
                    expect(html).toContain("font-size:12px");
                    expect(html).toContain("&lt;plate&gt;&quot;&amp;");
                    expect(html).toContain("var(--card)");
                    expect(html).toContain("var(--foreground)");
                }
                expect(popup).toContain(t(engineOn ? "dashboard.vehicleMap.engineOn" : "dashboard.vehicleMap.engineOff"));
                expect(popup).toContain("var(--destructive)");
            }
        });
    }
});
