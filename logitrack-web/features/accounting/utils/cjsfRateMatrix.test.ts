import { describe, expect, it } from "vitest";
import { DEFAULT_FUEL_BAND_STEP_PARAMS } from "@/lib/billingCompute";
import { isRateMatrixSheet, parseRateMatrixSheet, splitDestinationCodes } from "./cjsfRateMatrix";

const HEADER = [
    "", "ซัพพลายเออร์", "ประเภทรถ", "รหัส", "ต้นทาง", "รหัส", "ปลายทาง", "เวลาเข้า", "เวลาออก",
    "32-34", "34-36", "36-38", "38-40", "40-42", "42-44", "44-46", "46-48", "48-50", "50-52", "52-42",
];
const YANNAWA = [1005, 1010, 1015, 1020, 1025, 1030, 1035, 1040, 1045, 1050, 1055];
const SATHORN = [895, 900, 905, 910, 915, 922.5, 930, 935, 940, 945, 950];
const DEFAULT = [915, 920, 925, 930, 935, 942.5, 950, 955, 960, 965, 970];

const row = (dest: unknown, name: string, inTime: number, prices: number[]) => [
    "", "WRT", "4wj", 999001, "SPK", dest, name, inTime, inTime + 0.06, ...prices,
];

/** The CJSF zone-03 sheet as `sheet_to_json(..., { header: 1 })` returns it. */
const SHEET: unknown[][] = [
    [0.007, "เขต 03"],
    [],
    [],
    HEADER,
    row(890052890199, "01Yan Nawa14\n01Yan Nawa16", 0.89, YANNAWA),
    row(890052890199, "01Yan Nawa14\n01Yan Nawa16", 0.25, YANNAWA),
    row(890049, "01Sathon05", 0.125, SATHORN),
    row(890049, "01Sathon05", 0, SATHORN),
    row(890049, "01Sathon05", 0.375, SATHORN),
    row(890174, "01Huai Khwang10", 0.17, DEFAULT),
    row(890174, "01Huai Khwang10", 0.375, DEFAULT),
    row("890075\n890118", "01Wattana12\n01Wattana15", 0.92, DEFAULT),
    row(890103, "01Lat Krabang26", 0.33, DEFAULT),
    row(890153, "01Bung Kum11", 0, DEFAULT),
];

const P = DEFAULT_FUEL_BAND_STEP_PARAMS;
const ANCHOR_COL = 14; // the 42-44 column

/**
 * The same sheet as `sheet_to_json(..., { header: 1, raw: false })` returns it: the price cells carry
 * the CJSF `#,##0` format (922.5 → "923"), and a 12-digit code in General format shows as exponent.
 */
const DISPLAY: unknown[][] = SHEET.map((r) =>
    r.map((c, col) => {
        if (typeof c !== "number") return c;
        if (col >= 9) return c.toLocaleString("en-US", { maximumFractionDigits: 0 });
        return c > 99_999_999_999 ? c.toExponential(5).replace("e+", "E+") : String(c);
    })
);

describe("splitDestinationCodes", () => {
    it("splits a 12-digit number Excel stored for a two-line cell", () => {
        expect(splitDestinationCodes(890052890199)).toEqual(["SPK890052", "SPK890199"]);
    });
    it("splits a text cell on new lines", () => {
        expect(splitDestinationCodes("890075\n890118")).toEqual(["SPK890075", "SPK890118"]);
    });
    it("keeps a code that already carries its prefix", () => {
        expect(splitDestinationCodes("SPK890103")).toEqual(["SPK890103"]);
    });
    it("rejects digits that are not 6-digit codes", () => {
        expect(splitDestinationCodes(89010)).toBeNull();
        expect(splitDestinationCodes("ลาดกระบัง")).toBeNull();
    });
});

describe("parseRateMatrixSheet — CJSF zone 03", () => {
    it("is recognised as a matrix", () => {
        expect(isRateMatrixSheet(SHEET)).toBe(true);
        expect(isRateMatrixSheet([["ต้นทาง", "ปลายทาง", "ราคา"], ["SPK-GW", "SPK890103", 1200]])).toBe(false);
    });

    it("reads the 42-44 column as base prices, one row per destination", () => {
        const parsed = parseRateMatrixSheet(SHEET, P)!;
        expect(parsed.mismatches).toEqual([]);
        expect(parsed.conflicts).toEqual([]);
        expect(parsed.warnings).toEqual([]);
        const byCode = Object.fromEntries(parsed.rows.map((r) => [r.destinationCode, r.baseRateThb]));
        expect(byCode).toEqual({
            SPK890052: 1030,
            SPK890199: 1030,
            SPK890049: 922.5,
            SPK890174: 942.5,
            SPK890075: 942.5,
            SPK890118: 942.5,
            SPK890103: 942.5,
            SPK890153: 942.5,
        });
        expect(parsed.rows.every((r) => r.vehicleClass === "4WJ")).toBe(true);
        expect(parsed.rows.find((r) => r.destinationCode === "SPK890199")?.destinationName).toBe("01Yan Nawa16");
    });

    it("merges rows that differ only by time window", () => {
        // 2 Yan Nawa rows × 2 codes → 2 merged; 3 Sathon → 2; 2 Huai Khwang → 1.
        expect(parseRateMatrixSheet(SHEET, P)!.mergedDuplicates).toBe(5);
    });

    it("maps the band columns onto steps −5 … +5, including the 52-42 typo", () => {
        const bands = parseRateMatrixSheet(SHEET, P)!.bandColumns;
        expect(bands.map((b) => b.step)).toEqual([-5, -4, -3, -2, -1, 0, 1, 2, 3, 4, 5]);
        expect(bands[10]).toMatchObject({ header: "52-42", lowerThb: 52.01, upperThb: 54 });
    });

    it("blocks a sheet cell that the contract terms do not produce", () => {
        const broken = SHEET.map((r) => [...r]);
        broken[12][15] = 955; // ลาดกระบัง 44-46 should be 950
        const parsed = parseRateMatrixSheet(broken, P)!;
        expect(parsed.mismatches).toEqual([
            { sheetRow: 13, destinationCode: "SPK890103", header: "44-46", sheetThb: 955, computedThb: 950 },
        ]);
    });

    it("blocks the same route at two base prices", () => {
        const conflicting = [...SHEET, row(890103, "01Lat Krabang26", 0.5, YANNAWA)];
        const parsed = parseRateMatrixSheet(conflicting, P)!;
        expect(parsed.conflicts).toEqual([
            { destinationCode: "SPK890103", vehicleClass: "4WJ", sheetRows: [13, 15], pricesThb: [942.5, 1030] },
        ]);
    });

    it("flags a wrong contract term on every off-anchor cell", () => {
        const parsed = parseRateMatrixSheet(SHEET, { ...P, stepBasisPoints: 80 })!;
        expect(parsed.mismatches.length).toBeGreaterThan(0);
    });

    it("returns null when the sheet has no anchor column", () => {
        expect(parseRateMatrixSheet(SHEET, { ...P, anchorBandLowerThb: 43 })).toBeNull();
    });
});

describe("parseRateMatrixSheet — prices as the sheet displays them", () => {
    it("reads 922.5 / 942.5 as the 923 / 943 the customer sees, and every band still matches", () => {
        expect(DISPLAY[6][ANCHOR_COL]).toBe("923");
        const parsed = parseRateMatrixSheet(SHEET, P, DISPLAY)!;
        expect(parsed.mismatches).toEqual([]);
        expect(parsed.warnings).toEqual([]);
        const byCode = Object.fromEntries(parsed.rows.map((r) => [r.destinationCode, r.baseRateThb]));
        expect(byCode).toEqual({
            SPK890052: 1030,
            SPK890199: 1030,
            SPK890049: 923,
            SPK890174: 943,
            SPK890075: 943,
            SPK890118: 943,
            SPK890103: 943,
            SPK890153: 943,
        });
    });

    it("reports each base it took as displayed, but not the ones already whole", () => {
        const { displayedBases } = parseRateMatrixSheet(SHEET, P, DISPLAY)!;
        expect(displayedBases).toHaveLength(8); // 3 Sathon + 5 at 942.5; Yan Nawa 1030 shows as 1,030
        expect(displayedBases[0]).toEqual({ sheetRow: 7, destinationCode: "SPK890049", cellThb: 922.5, displayedThb: 923 });
        expect(parseRateMatrixSheet(SHEET, P)!.displayedBases).toEqual([]);
    });

    it("takes codes from the cell values, never the exponent the sheet displays", () => {
        expect(DISPLAY[4][5]).toBe("8.90053E+11");
        const codes = parseRateMatrixSheet(SHEET, P, DISPLAY)!.rows.map((r) => r.destinationCode);
        expect(codes).toContain("SPK890052");
        expect(codes).toContain("SPK890199");
    });

    it("still blocks when a displayed base no longer produces the other bands", () => {
        const display = DISPLAY.map((r) => [...r]);
        display[6][ANCHOR_COL] = "925"; // Sathon shown at 925: step −1 → 920, the sheet says 915
        const parsed = parseRateMatrixSheet(SHEET, P, display)!;
        expect(parsed.mismatches).toContainEqual({
            sheetRow: 7,
            destinationCode: "SPK890049",
            header: "40-42",
            sheetThb: 915,
            computedThb: 920,
        });
    });
});
