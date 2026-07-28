import { describe, it, expect } from "vitest";
import {
    parseSemver,
    compareSemver,
    getVersionStatus,
    countVersionStatuses,
    countBlockedByFloor,
} from "./mobileVersion";

describe("parseSemver", () => {
    it("parses a plain MAJOR.MINOR.PATCH", () => {
        expect(parseSemver("2.9.3")).toEqual([2, 9, 3]);
    });

    it("drops the pubspec +build suffix", () => {
        expect(parseSemver("2.9.3+1")).toEqual([2, 9, 3]);
        expect(parseSemver("2.9.3+42")).toEqual([2, 9, 3]);
    });

    it("tolerates surrounding whitespace", () => {
        expect(parseSemver("  2.9.3  ")).toEqual([2, 9, 3]);
    });

    it("returns null for anything that is not three numeric parts", () => {
        expect(parseSemver("v2.9.3")).toBeNull();
        expect(parseSemver("2.9")).toBeNull();
        expect(parseSemver("2.9.3.1")).toBeNull();
        expect(parseSemver("2.9.x")).toBeNull();
        expect(parseSemver("")).toBeNull();
        expect(parseSemver("   ")).toBeNull();
        expect(parseSemver("—")).toBeNull();
    });

    it("returns null for non-strings rather than coercing", () => {
        expect(parseSemver(null)).toBeNull();
        expect(parseSemver(undefined)).toBeNull();
        expect(parseSemver(293)).toBeNull();
        expect(parseSemver({ version: "2.9.3" })).toBeNull();
    });
});

describe("compareSemver", () => {
    it("orders 2.10.0 above 2.9.3 — the string-compare trap this module exists for", () => {
        expect(compareSemver("2.10.0", "2.9.3")).toBeGreaterThan(0);
        expect(compareSemver("2.9.3", "2.10.0")).toBeLessThan(0);
        // Guard against a regression to lexicographic ordering.
        expect("2.10.0" < "2.9.3").toBe(true);
    });

    it("reports equality", () => {
        expect(compareSemver("2.9.3", "2.9.3")).toBe(0);
        expect(compareSemver("2.9.3+1", "2.9.3+7")).toBe(0);
    });

    it("orders by major, then minor, then patch", () => {
        expect(compareSemver("3.0.0", "2.99.99")).toBeGreaterThan(0);
        expect(compareSemver("2.10.0", "2.9.99")).toBeGreaterThan(0);
        expect(compareSemver("2.9.4", "2.9.3")).toBeGreaterThan(0);
    });

    it("returns null — not 0 — when either side is unparseable", () => {
        expect(compareSemver("2.9.3", "")).toBeNull();
        expect(compareSemver("", "2.9.3")).toBeNull();
        expect(compareSemver(null, undefined)).toBeNull();
    });
});

describe("getVersionStatus", () => {
    it("marks an install below the floor as blocked, even though it is also outdated", () => {
        expect(getVersionStatus("2.8.0", "2.9.3", "2.9.0")).toBe("blocked");
    });

    it("marks an install behind the latest but above the floor as outdated", () => {
        expect(getVersionStatus("2.9.1", "2.9.3", "2.9.0")).toBe("outdated");
    });

    it("marks an install on the latest as current", () => {
        expect(getVersionStatus("2.9.3", "2.9.3", "2.9.0")).toBe("current");
        expect(getVersionStatus("2.9.3+1", "2.9.3", "2.9.0")).toBe("current");
    });

    it("marks an install newer than what we published as ahead", () => {
        expect(getVersionStatus("2.10.0", "2.9.3", "2.9.0")).toBe("ahead");
    });

    it("treats an install exactly on the floor as not blocked", () => {
        expect(getVersionStatus("2.9.0", "2.9.3", "2.9.0")).toBe("outdated");
    });

    it("returns unknown when the installed version cannot be parsed", () => {
        expect(getVersionStatus("", "2.9.3", "2.9.0")).toBe("unknown");
        expect(getVersionStatus(null, "2.9.3", "2.9.0")).toBe("unknown");
        expect(getVersionStatus("v2.9.3", "2.9.3", "2.9.0")).toBe("unknown");
    });

    it("degrades to unknown rather than throwing when latest is missing", () => {
        expect(getVersionStatus("2.9.3", null, "2.9.0")).toBe("unknown");
        expect(getVersionStatus("2.9.3", "", "")).toBe("unknown");
    });

    it("still blocks when latest is missing but the floor is set", () => {
        expect(getVersionStatus("2.8.0", null, "2.9.0")).toBe("blocked");
    });

    it("skips the floor check when no floor is set", () => {
        expect(getVersionStatus("2.8.0", "2.9.3", null)).toBe("outdated");
        expect(getVersionStatus("2.8.0", "2.9.3", "")).toBe("outdated");
    });
});

describe("countVersionStatuses", () => {
    it("tallies every bucket", () => {
        const counts = countVersionStatuses(
            ["2.9.3", "2.9.1", "2.8.0", "2.10.0", "not-a-version"],
            "2.9.3",
            "2.9.0",
        );
        expect(counts).toEqual({
            current: 1,
            outdated: 1,
            blocked: 1,
            ahead: 1,
            unknown: 1,
        });
    });

    it("returns all-zero for an empty fleet", () => {
        expect(countVersionStatuses([], "2.9.3", "2.9.0")).toEqual({
            current: 0,
            outdated: 0,
            blocked: 0,
            ahead: 0,
            unknown: 0,
        });
    });
});

describe("countBlockedByFloor", () => {
    it("counts installs strictly below the target", () => {
        expect(countBlockedByFloor(["2.8.0", "2.9.2", "2.9.3", "2.10.0"], "2.9.3")).toBe(2);
    });

    it("does not count an install already on the target", () => {
        expect(countBlockedByFloor(["2.9.3", "2.9.3+1"], "2.9.3")).toBe(0);
    });

    it("does not count unparseable versions — the dialog must not overstate the impact", () => {
        expect(countBlockedByFloor(["", null, "—", "v2.8.0"], "2.9.3")).toBe(0);
    });

    it("counts nothing when the target itself is unparseable", () => {
        expect(countBlockedByFloor(["2.8.0", "2.9.0"], "")).toBe(0);
    });
});
