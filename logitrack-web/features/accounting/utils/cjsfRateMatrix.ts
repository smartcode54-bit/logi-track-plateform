/**
 * Reads a customer's fuel-band rate MATRIX sheet (CJSF `เรทน้ำมันเขต 03 WRT.xlsx`) into base-price
 * rate rows (ADR 0029). Pure — no Firebase, no XLSX dependency: it takes the sheet as the array of
 * rows `XLSX.utils.sheet_to_json(sheet, { header: 1 })` returns.
 *
 * The sheet prices every route at every ฿2 diesel band (`32-34` … `52-54`). Only the anchor column
 * (`42-44` = 42.01–44.00) is stored — as the base price. Every OTHER band column is then re-derived
 * with `priceAtBandStep` and compared with the sheet, so a wrong contract term, a typo in the sheet or
 * a drift in our rounding is caught at import time instead of on an invoice.
 *
 * Prices are read AS THE SHEET DISPLAYS THEM when `displayRows` is given (owner decision 2026-11-16):
 * the CJSF anchor cell holds 922.5 but its `#,##0` format shows 923, and the customer bills the
 * figure it shows. 923 re-derives every other column exactly like 922.5 does, so the cross-check
 * still holds — and if rounding a cell ever changed another band, the mismatch would block import.
 */

import {
    normalizeDestinationCode,
    normalizeVehicleClass,
    priceAtBandStep,
    stepBandRange,
    type FuelBandStepParams,
} from "@/lib/billingCompute";

/** One base-price rate row the matrix produced (after merging duplicate time-window rows). */
export interface RateMatrixRow {
    destinationCode: string;
    destinationName: string;
    vehicleClass: string;
    baseRateThb: number;
    /** 1-based sheet row the price was first read from. */
    sheetRow: number;
}

export interface RateMatrixBandColumn {
    header: string;
    step: number;
    lowerThb: number;
    upperThb: number;
}

/** A sheet cell that disagrees with the price the contract terms produce. Blocks the import. */
export interface RateMatrixMismatch {
    sheetRow: number;
    destinationCode: string;
    header: string;
    sheetThb: number;
    computedThb: number;
}

/** The same route + vehicle at two different base prices. Blocks the import. */
export interface RateMatrixConflict {
    destinationCode: string;
    vehicleClass: string;
    sheetRows: number[];
    pricesThb: number[];
}

/** A base price taken as the sheet displays it, which differs from the number stored in the cell. */
export interface RateMatrixDisplayedBase {
    sheetRow: number;
    destinationCode: string;
    cellThb: number;
    displayedThb: number;
}

export type RateMatrixWarning =
    | { kind: "missingDestination"; sheetRow: number }
    | { kind: "invalidDestination"; sheetRow: number; value: string }
    | { kind: "missingBase"; sheetRow: number };

export interface ParsedRateMatrix {
    rows: RateMatrixRow[];
    bandColumns: RateMatrixBandColumn[];
    /** Rows dropped because they repeat a route at the same price (e.g. another time window). */
    mergedDuplicates: number;
    mismatches: RateMatrixMismatch[];
    conflicts: RateMatrixConflict[];
    /** Base prices read as displayed (e.g. 922.5 shown as 923) — informational, does not block. */
    displayedBases: RateMatrixDisplayedBase[];
    warnings: RateMatrixWarning[];
}

const BAND_HEADER = /^\s*(\d{1,3}(?:\.\d+)?)\s*[-–]\s*(\d{1,3}(?:\.\d+)?)\s*$/;

function text(v: unknown): string {
    return String(v ?? "").trim();
}

function numeric(v: unknown): number | null {
    if (typeof v === "number" && Number.isFinite(v)) return v;
    const s = text(v).replace(/,/g, "");
    if (!s) return null;
    const n = Number(s);
    return Number.isFinite(n) ? n : null;
}

function satang(thb: number): number {
    return Math.round(thb * 100);
}

/**
 * Lower edge of a band header such as `42-44`. Only the lower number is read: the sheet's last column
 * is the typo `52-42`, and the band is fully defined by its lower edge and the contract width anyway.
 */
function bandLowerOf(header: unknown): number | null {
    const m = BAND_HEADER.exec(text(header));
    return m ? Number(m[1]) : null;
}

function findHeaderRow(rows: unknown[][]): number {
    return rows.findIndex((r) => (r ?? []).filter((c) => bandLowerOf(c) != null).length >= 3);
}

/** True when the sheet has a row of ≥3 band headers — i.e. it is a matrix, not the lane template. */
export function isRateMatrixSheet(rows: unknown[][]): boolean {
    return findHeaderRow(rows) >= 0;
}

/**
 * Destination codes in one cell. A cell holding two codes on two lines is text (`890075\n890118`), but
 * Excel may store it as ONE number (`890052890199`) — split digit runs into 6-digit codes either way.
 * A bare 6-digit J&T code becomes `SPK` + code, the form tasks and rate cards carry.
 */
export function splitDestinationCodes(cell: unknown): string[] | null {
    const raw = typeof cell === "number" && Number.isFinite(cell) ? String(Math.trunc(cell)) : text(cell);
    if (!raw) return [];
    const codes: string[] = [];
    for (const token of raw.split(/[\s,/]+/).filter(Boolean)) {
        if (/^\d+$/.test(token)) {
            if (token.length % 6 !== 0) return null;
            for (let i = 0; i < token.length; i += 6) codes.push(`SPK${token.slice(i, i + 6)}`);
        } else if (/^[A-Za-z]+\d+/.test(token)) {
            codes.push(normalizeDestinationCode(token));
        } else {
            return null;
        }
    }
    return codes;
}

/**
 * Parse a matrix sheet. Returns `null` when the sheet has no band header row or no column for the
 * anchor band — nothing can be read as a base price then.
 *
 * `rows` are the cell values (`sheet_to_json(sheet, { header: 1 })`). `displayRows` is the same sheet
 * as formatted text (`{ header: 1, raw: false }`); when given, every band price is read as displayed.
 * Codes and names always come from `rows` — a 12-digit code would display as `8.90052E+11`.
 */
export function parseRateMatrixSheet(
    rows: unknown[][],
    params: FuelBandStepParams,
    displayRows?: unknown[][]
): ParsedRateMatrix | null {
    const headerIdx = findHeaderRow(rows);
    if (headerIdx < 0) return null;
    const header = rows[headerIdx] ?? [];

    const bandColumns: (RateMatrixBandColumn & { col: number })[] = [];
    header.forEach((cell, col) => {
        const lower = bandLowerOf(cell);
        if (lower == null) return;
        const offset = (satang(lower) - satang(params.anchorBandLowerThb)) / satang(params.bandWidthThb);
        if (!Number.isInteger(offset)) return; // not on this contract's band grid
        const range = stepBandRange(offset, params.anchorBandLowerThb, params.bandWidthThb);
        if (!range) return;
        bandColumns.push({ header: text(cell), step: offset, col, ...range });
    });
    const anchor = bandColumns.find((b) => b.step === 0);
    if (!anchor) return null;

    const headerTexts = header.map(text);
    const vehicleCol = headerTexts.findIndex((h) => h.includes("ประเภทรถ"));
    const destNameCol = headerTexts.findIndex((h) => h.includes("ปลายทาง"));
    let destCodeCol = -1;
    for (let c = destNameCol - 1; c >= 0; c -= 1) {
        if (headerTexts[c].includes("รหัส")) {
            destCodeCol = c;
            break;
        }
    }

    const byKey = new Map<string, RateMatrixRow>();
    const conflictByKey = new Map<string, RateMatrixConflict>();
    const mismatches: RateMatrixMismatch[] = [];
    const displayedBases: RateMatrixDisplayedBase[] = [];
    const warnings: RateMatrixWarning[] = [];
    let mergedDuplicates = 0;

    for (let i = headerIdx + 1; i < rows.length; i += 1) {
        const r = rows[i] ?? [];
        const sheetRow = i + 1;
        if (r.every((c) => text(c) === "")) continue;

        /** A price cell as the customer sees it; falls back to the cell value when no display text. */
        const priceAt = (col: number): number | null => numeric(displayRows?.[i]?.[col]) ?? numeric(r[col]);
        const base = priceAt(anchor.col);
        const codeCell = destCodeCol >= 0 ? r[destCodeCol] : undefined;
        const codes = splitDestinationCodes(codeCell);
        if (codes == null) {
            warnings.push({ kind: "invalidDestination", sheetRow, value: text(codeCell) });
            continue;
        }
        if (codes.length === 0) {
            warnings.push({ kind: "missingDestination", sheetRow });
            continue;
        }
        if (base == null || base <= 0) {
            warnings.push({ kind: "missingBase", sheetRow });
            continue;
        }

        const cellBase = numeric(r[anchor.col]);
        if (cellBase != null && satang(cellBase) !== satang(base)) {
            displayedBases.push({ sheetRow, destinationCode: codes.join(", "), cellThb: cellBase, displayedThb: base });
        }

        // Cross-check every band column of this row against the contract terms.
        for (const band of bandColumns) {
            const sheetThb = priceAt(band.col);
            if (sheetThb == null) continue;
            const computedThb = priceAtBandStep(base, band.step, params.stepBasisPoints, params.roundToThb);
            if (satang(sheetThb) !== satang(computedThb)) {
                mismatches.push({ sheetRow, destinationCode: codes.join(", "), header: band.header, sheetThb, computedThb });
            }
        }

        const vehicleClass = normalizeVehicleClass(vehicleCol >= 0 ? text(r[vehicleCol]) : "");
        const names = (destNameCol >= 0 ? text(r[destNameCol]) : "").split(/\r?\n/).map((n) => n.trim());
        codes.forEach((destinationCode, k) => {
            const key = `${destinationCode}::${vehicleClass}`;
            const existing = byKey.get(key);
            if (!existing) {
                byKey.set(key, {
                    destinationCode,
                    destinationName: names[k] ?? names[0] ?? "",
                    vehicleClass,
                    baseRateThb: base,
                    sheetRow,
                });
                return;
            }
            if (satang(existing.baseRateThb) === satang(base)) {
                mergedDuplicates += 1;
                return;
            }
            const conflict = conflictByKey.get(key) ?? {
                destinationCode,
                vehicleClass,
                sheetRows: [existing.sheetRow],
                pricesThb: [existing.baseRateThb],
            };
            conflict.sheetRows.push(sheetRow);
            conflict.pricesThb.push(base);
            conflictByKey.set(key, conflict);
        });
    }

    return {
        rows: Array.from(byKey.values()),
        bandColumns: bandColumns
            .map(({ header, step, lowerThb, upperThb }) => ({ header, step, lowerThb, upperThb }))
            .sort((a, b) => a.step - b.step),
        mergedDuplicates,
        mismatches,
        conflicts: Array.from(conflictByKey.values()),
        displayedBases,
        warnings,
    };
}
