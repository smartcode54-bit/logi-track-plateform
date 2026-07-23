/**
 * Truck licence-plate resolution and filter matching — the single source of truth shared by
 * Driver Monitor (`features/drivers/`) and Billing Document (`app/app/accounting/billing-document/`).
 *
 * Both pages used to resolve a plate differently, so the same trip could show one plate on screen
 * and another on the invoice. Everything plate-related now goes through here. See
 * shared-docs/adr/0005-truck-plate-filter-billing-document-driver-monitor.md.
 *
 * Two rules this module exists to enforce:
 *
 *  1. **A plate is a display string, not an identity.** There is deliberately no normalisation
 *     (`70-1234` ≠ `70 - 1234`); stripping a province suffix can collide across provinces. Match on
 *     `truckId` — the plate string is only a fallback for rows that predate `tasks.truckId`.
 *  2. **Never fall back to live state.** `drivers.activeTruck` says what a driver is in *right now*,
 *     so using it to label a *historical* trip restamps old rows with today's truck and makes a
 *     filter return rows that truck never ran. Resolution stops at the trip's own task.
 */

/** Select value meaning "no plate filter applied". */
export const PLATE_FILTER_ALL = "all";

/** Select value for rows with no resolvable plate at all (mirrors DRIVER_MONITOR_PARTNER_NONE). */
export const PLATE_FILTER_NONE = "__none__";

/** The minimum a row must expose to take part in plate filtering. */
export interface PlateFilterRow {
    truckId?: string | null;
    plate?: string | null;
}

export interface PlateFilterOption {
    /** Stable select value: `id:<truckId>` | `plate:<raw>` | PLATE_FILTER_NONE. */
    value: string;
    /** Plate string for display; null for the no-plate bucket. */
    label: string | null;
    /** True when rows carry a plate but no truckId — an orphan plate (no fleet doc). */
    isOrphan: boolean;
    /** How many loaded rows this option matches. */
    count: number;
}

function clean(value?: string | null): string {
    return typeof value === "string" ? value.trim() : "";
}

/**
 * Resolves the plate to show for a trip: the loading-phase snapshot first, then the plate on the
 * trip's own task, then nothing.
 *
 * Deliberately has no `activeTruck` branch — see rule 2 above.
 */
export function resolveTripPlate(input: {
    tripPlate?: string | null;
    taskPlate?: string | null;
}): string | null {
    return clean(input.tripPlate) || clean(input.taskPlate) || null;
}

/**
 * Identity key for grouping and matching a row to a truck.
 *
 * `truckId` wins whenever present, so the same truck recorded with two different plate spellings
 * still groups as one. A row with only a plate falls back to the raw string; a row with neither
 * lands in the no-plate bucket.
 */
export function truckFilterKey(row: PlateFilterRow): string {
    const id = clean(row.truckId);
    if (id) return `id:${id}`;
    const plate = clean(row.plate);
    if (plate) return `plate:${plate}`;
    return PLATE_FILTER_NONE;
}

/**
 * Builds the filter's options from the rows currently loaded — never from the `trucks` collection,
 * so every option provably matches at least one row and orphan plates stay reachable.
 *
 * Sorted by plate; the no-plate bucket always sorts last.
 */
export function buildPlateFilterOptions(rows: PlateFilterRow[]): PlateFilterOption[] {
    const byKey = new Map<string, PlateFilterOption>();

    for (const row of rows) {
        const key = truckFilterKey(row);
        const existing = byKey.get(key);
        if (existing) {
            existing.count += 1;
            // A later row may carry the plate an earlier one lacked (same truckId).
            if (!existing.label) existing.label = clean(row.plate) || null;
            continue;
        }
        byKey.set(key, {
            value: key,
            label: key === PLATE_FILTER_NONE ? null : clean(row.plate) || null,
            isOrphan: key.startsWith("plate:"),
            count: 1,
        });
    }

    return [...byKey.values()].sort((a, b) => {
        if (a.value === PLATE_FILTER_NONE) return 1;
        if (b.value === PLATE_FILTER_NONE) return -1;
        return (a.label ?? "").localeCompare(b.label ?? "", "th");
    });
}

/**
 * True when a row belongs under the selected filter value.
 *
 * `PLATE_FILTER_ALL` matches everything. Otherwise the row's own key must equal the selection —
 * which means an `id:` selection matches by truck identity regardless of plate spelling, and a
 * `plate:` selection only ever matches rows that have no `truckId`.
 */
export function rowMatchesPlateFilter(row: PlateFilterRow, selected: string): boolean {
    if (!selected || selected === PLATE_FILTER_ALL) return true;
    return truckFilterKey(row) === selected;
}
