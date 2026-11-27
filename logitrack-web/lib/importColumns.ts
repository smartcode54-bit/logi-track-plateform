/**
 * Map spreadsheet header cells to fields for the import dialogs.
 *
 * Fields are resolved in the order given, and each column goes to one field only. Within a field,
 * terms are tried in order, so put the most specific term first. This stops a loose term from
 * grabbing a neighbouring column: "pickup" matches both "Actual pickup (วันเวลารับงานจริง)" and
 * "Pickup Location (จุดรับงาน)", and the date serial from the first one ended up as the pickup place.
 */
export type ImportColumnSpec<K extends string> = ReadonlyArray<readonly [K, ReadonlyArray<string>]>;

export function resolveImportColumns<K extends string>(
    headerCells: ReadonlyArray<unknown>,
    specs: ImportColumnSpec<K>,
): Partial<Record<K, number>> {
    const headers = Array.from(headerCells, (h) => String(h ?? "").toLowerCase().trim());
    const claimed = new Set<number>();
    const out: Partial<Record<K, number>> = {};
    for (const [field, terms] of specs) {
        for (const term of terms) {
            const idx = headers.findIndex((h, i) => !claimed.has(i) && h !== "" && h.includes(term));
            if (idx !== -1) {
                out[field] = idx;
                claimed.add(idx);
                break;
            }
        }
    }
    return out;
}

/** A cell as trimmed text; empty → undefined. Excel hands back numbers for numeric-looking cells. */
export function cellText(v: unknown): string | undefined {
    if (v == null) return undefined;
    const s = String(v).trim();
    return s === "" ? undefined : s;
}
