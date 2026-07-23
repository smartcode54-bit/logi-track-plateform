import { describe, it, expect } from "vitest";
import {
    PLACE_FILTER_ALL,
    PLACE_FILTER_NONE,
    buildPlaceFilterOptions,
    placeKeysOf,
    resolvePlace,
    rowMatchesPlaceFilter,
    valueMatchesPlaceFilter,
    type PlaceMaps,
} from "./placeFilter";

/**
 * Mirrors what the hook builds: `codeToLabel` from `buildHubCodeToDisplayMapFromEntries` (TH name
 * preferred, SOC keys injected) and `nameToCode` from both TH and EN hub names.
 */
const maps: PlaceMaps = {
    codeToLabel: {
        SPK890146: "ประเวศ18",
        "SPK-GW": "J&T EXPRESS บางปู",
        SOCE: "SOCE (บัวโรย)",
        SOCN: "SOCN (วังน้อย)",
        SOCW: "SOCW (สมุทรสาคร)",
    },
    nameToCode: new Map([
        ["ประเวศ18", "SPK890146"],
        ["Prawet 18", "SPK890146"],
        ["J&T EXPRESS บางปู", "SPK-GW"],
        ["JNT Bangpu", "SPK-GW"],
    ]),
};

describe("resolvePlace", () => {
    it("passes a known code straight through", () => {
        expect(resolvePlace("SPK890146", maps)).toEqual({ key: "code:SPK890146", kind: "resolved" });
    });

    it("resolves a TH display name to its code", () => {
        expect(resolvePlace("ประเวศ18", maps)).toEqual({ key: "code:SPK890146", kind: "resolved" });
    });

    it("resolves an EN display name to the SAME code as the TH name (one option, not two)", () => {
        expect(resolvePlace("Prawet 18", maps).key).toBe(resolvePlace("ประเวศ18", maps).key);
        expect(resolvePlace("JNT Bangpu", maps).key).toBe(resolvePlace("J&T EXPRESS บางปู", maps).key);
    });

    it("normalises SOC spellings to the canonical key", () => {
        expect(resolvePlace("SOCE (บัวโรย)", maps).key).toBe("code:SOCE");
        expect(resolvePlace("SOCE", maps).key).toBe("code:SOCE");
        expect(resolvePlace("soce", maps).key).toBe("code:SOCE");
    });

    it("keeps an OCR composite string unresolved under its own key", () => {
        expect(resolvePlace("ALANG-A - วังทองหลาง", maps)).toEqual({
            key: "raw:ALANG-A - วังทองหลาง",
            kind: "unresolved",
        });
    });

    it("never resolves in the reverse direction — a code does not become a display-name key", () => {
        // The bug this guards (CLAUDE.md §39): a bidirectional map turns "SPK890146" into "ประเวศ18".
        const resolved = resolvePlace("SPK890146", maps);
        expect(resolved.key).toBe("code:SPK890146");
        expect(resolved.key).not.toContain("ประเวศ18");
    });

    it("treats empty, blank and null as absent", () => {
        expect(resolvePlace("", maps)).toEqual({ key: PLACE_FILTER_NONE, kind: "absent" });
        expect(resolvePlace("   ", maps)).toEqual({ key: PLACE_FILTER_NONE, kind: "absent" });
        expect(resolvePlace(null, maps)).toEqual({ key: PLACE_FILTER_NONE, kind: "absent" });
        expect(resolvePlace(undefined, maps)).toEqual({ key: PLACE_FILTER_NONE, kind: "absent" });
    });

    it("trims surrounding whitespace before resolving", () => {
        expect(resolvePlace("  ประเวศ18  ", maps).key).toBe("code:SPK890146");
    });
});

describe("placeKeysOf", () => {
    it("dedupes two stops to the same place", () => {
        expect(placeKeysOf(["ประเวศ18", "SPK890146"], maps)).toEqual(["code:SPK890146"]);
    });

    it("returns one key per distinct place", () => {
        expect(placeKeysOf(["ประเวศ18", "SOCE"], maps).sort()).toEqual(["code:SOCE", "code:SPK890146"]);
    });

    it("treats an empty list as absent", () => {
        expect(placeKeysOf([], maps)).toEqual([PLACE_FILTER_NONE]);
    });
});

describe("buildPlaceFilterOptions", () => {
    const rows = [
        ["ประเวศ18"], // trip 1 — TH name
        ["SPK890146"], // trip 2 — same place, as a code
        ["Prawet 18"], // trip 3 — same place, EN name
        ["ALANG-A - วังทองหลาง"], // trip 4 — unresolved
        ["SOCE (บัวโรย)"], // trip 5
        [null], // trip 6 — no destination
    ];

    it("collapses every spelling of one place into a single option", () => {
        const options = buildPlaceFilterOptions(rows, maps);
        const prawet = options.filter((o) => o.value === "code:SPK890146");
        expect(prawet).toHaveLength(1);
        expect(prawet[0].count).toBe(3);
        expect(prawet[0].label).toBe("ประเวศ18");
    });

    it("gives each unresolved string its own badged option", () => {
        const options = buildPlaceFilterOptions(
            [["ALANG-A - วังทองหลาง"], ["BLANG-B - ห้วยขวาง"]],
            maps
        );
        expect(options).toHaveLength(2);
        expect(options.every((o) => o.isUnresolved)).toBe(true);
    });

    it("counts trips, not stops — a trip with two stops to one place counts once", () => {
        const options = buildPlaceFilterOptions([["ประเวศ18", "SPK890146"]], maps);
        expect(options).toHaveLength(1);
        expect(options[0].count).toBe(1);
    });

    it("accounts for every trip exactly once across all options", () => {
        const options = buildPlaceFilterOptions(rows, maps);
        expect(options.reduce((sum, o) => sum + o.count, 0)).toBe(rows.length);
    });

    it("sorts resolved places first, then unresolved, with the no-value bucket last", () => {
        const options = buildPlaceFilterOptions(rows, maps);
        expect(options[options.length - 1].value).toBe(PLACE_FILTER_NONE);

        // Everything before the first unresolved option must itself be resolved — i.e. the two
        // blocks never interleave.
        const ranked = options
            .filter((o) => o.value !== PLACE_FILTER_NONE)
            .map((o) => (o.isUnresolved ? 1 : 0));
        expect(ranked).toEqual([...ranked].sort());
        expect(ranked).toContain(0);
        expect(ranked).toContain(1);
    });

    it("offers no option that matches zero trips", () => {
        const options = buildPlaceFilterOptions(rows, maps);
        for (const option of options) {
            const matched = rows.filter((r) => rowMatchesPlaceFilter(r, option.value, maps));
            expect(matched.length).toBe(option.count);
            expect(matched.length).toBeGreaterThan(0);
        }
    });
});

describe("rowMatchesPlaceFilter", () => {
    it("matches everything when no filter is applied", () => {
        expect(rowMatchesPlaceFilter([null], PLACE_FILTER_ALL, maps)).toBe(true);
        expect(rowMatchesPlaceFilter(["anything"], "", maps)).toBe(true);
    });

    it("matches a multi-drop trip on ANY of its stops (ADR 0006 §5)", () => {
        const stops = ["SPK-GW", "SOCN", "ประเวศ18"];
        expect(rowMatchesPlaceFilter(stops, "code:SPK890146", maps)).toBe(true);
        expect(rowMatchesPlaceFilter(stops, "code:SOCN", maps)).toBe(true);
        expect(rowMatchesPlaceFilter(stops, "code:SOCW", maps)).toBe(false);
    });

    it("matches the no-value bucket only for trips with no place", () => {
        expect(rowMatchesPlaceFilter([null], PLACE_FILTER_NONE, maps)).toBe(true);
        expect(rowMatchesPlaceFilter([""], PLACE_FILTER_NONE, maps)).toBe(true);
        expect(rowMatchesPlaceFilter(["SOCE"], PLACE_FILTER_NONE, maps)).toBe(false);
    });

    it("matches an unresolved selection only on its exact raw string", () => {
        expect(rowMatchesPlaceFilter(["ALANG-A - วังทองหลาง"], "raw:ALANG-A - วังทองหลาง", maps)).toBe(true);
        expect(rowMatchesPlaceFilter(["ALANG-A - ลาดกระบัง"], "raw:ALANG-A - วังทองหลาง", maps)).toBe(false);
    });
});

describe("valueMatchesPlaceFilter", () => {
    it("matches a single stop for the per-stop export filter (ADR 0006 §6)", () => {
        expect(valueMatchesPlaceFilter("ประเวศ18", "code:SPK890146", maps)).toBe(true);
        expect(valueMatchesPlaceFilter("SOCN", "code:SPK890146", maps)).toBe(false);
    });

    it("passes every stop through when no filter is applied", () => {
        expect(valueMatchesPlaceFilter("SOCN", PLACE_FILTER_ALL, maps)).toBe(true);
    });
});
