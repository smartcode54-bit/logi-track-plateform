/**
 * Place (origin / destination) resolution and filter matching for Driver Monitor.
 *
 * `trip_records.origin` and `.destination` are free text (`validate/tripRecordSchema.ts:82-83`)
 * written by three different writers, so one physical place exists in Firestore under several
 * spellings — `SPK890146`, `ประเวศ18`, `ALANG-A - วังทองหลาง`. Filtering on the raw string would
 * fragment one hub into several options, each silently hiding the others' trips. Everything the
 * origin/destination filters do goes through here. See
 * shared-docs/adr/0006-origin-destination-filter-driver-monitor.md.
 *
 * Three rules this module exists to enforce:
 *
 *  1. **Resolve in the `nameToCode` direction only**, plus code pass-through. The reverse map is
 *     never consulted: merging the two directions is the documented cause of the "No rate" billing
 *     failure (CLAUDE.md §39), where a value that was already a code got turned back into a name.
 *  2. **Never merge what did not resolve.** Each unresolvable raw string is its own option, badged
 *     as not-in-master. Two unrelated OCR errors must stay distinguishable, so the filter doubles as
 *     a data-quality report on the free-text writers.
 *  3. **Options come from the loaded trips, never from the `hubs` master.** Every offered option
 *     provably matches at least one visible trip, and no trip is unreachable by every option.
 */

import { SOC_KEYS, normalizeSocIdToKey } from "@/validate/taskSchema";
import { resolveHubOrSocDisplay } from "@/lib/hubDisplay";

/** Select value meaning "no place filter applied". */
export const PLACE_FILTER_ALL = "all";

/** Select value for trips carrying no origin/destination at all (mirrors PLATE_FILTER_NONE). */
export const PLACE_FILTER_NONE = "__none__";

/** The already-loaded hub maps the resolver reads. Owned by the hook; this module stays React-free. */
export interface PlaceMaps {
    /** hub/SOC code → display label (`buildHubCodeToDisplayMapFromEntries`). */
    codeToLabel: Record<string, string>;
    /** hub display name (TH or EN) → `source_id`. The ONLY direction used to resolve. */
    nameToCode: Map<string, string>;
}

export type PlaceKind = "resolved" | "unresolved" | "absent";

export interface ResolvedPlace {
    /** Stable filter key: `code:<CODE>` | `raw:<exact string>` | PLACE_FILTER_NONE. */
    key: string;
    kind: PlaceKind;
}

export interface PlaceFilterOption {
    value: string;
    /** What the dropdown shows — the canonical display name, or the raw string when unresolved. */
    label: string;
    /** True when the value matches no hub/SOC master record (an [[Unresolved place]]). */
    isUnresolved: boolean;
    /** How many loaded trips this option matches. */
    count: number;
}

const ABSENT: ResolvedPlace = { key: PLACE_FILTER_NONE, kind: "absent" };

function clean(value?: string | null): string {
    return typeof value === "string" ? value.trim() : "";
}

/**
 * Resolves one raw origin/destination string to a canonical place key.
 *
 * Order matters: a value that is already a code passes straight through, and only then is the
 * display-name map consulted. Never the reverse (rule 1).
 */
export function resolvePlace(raw: string | null | undefined, maps: PlaceMaps): ResolvedPlace {
    const value = clean(raw);
    if (!value) return ABSENT;

    // 1. Already a code the master knows.
    if (maps.codeToLabel[value]) return { key: `code:${value}`, kind: "resolved" };

    // 2. A SOC code in one of its spellings ("SOCE (บัวโรย)" → "SOCE").
    const soc = normalizeSocIdToKey(value);
    if (maps.codeToLabel[soc] || (SOC_KEYS as readonly string[]).includes(soc)) {
        return { key: `code:${soc}`, kind: "resolved" };
    }

    // 3. A hub display name (TH or EN) → its source_id.
    const byName = maps.nameToCode.get(value);
    if (byName) return { key: `code:${clean(byName)}`, kind: "resolved" };

    // 4. Nothing matched — keep the raw value as its own bucket rather than merging it away.
    return { key: `raw:${value}`, kind: "unresolved" };
}

/** The place keys a trip can be matched by — deduped, so a trip with two stops to the same place counts once. */
export function placeKeysOf(values: Array<string | null | undefined>, maps: PlaceMaps): string[] {
    const keys = new Set<string>();
    if (values.length === 0) {
        keys.add(PLACE_FILTER_NONE);
        return [...keys];
    }
    for (const value of values) keys.add(resolvePlace(value, maps).key);
    return [...keys];
}

/**
 * Builds the filter's options from the loaded trips (rule 3).
 *
 * `rows` is one entry per trip: the trip's origin as a single-element array, or all of its delivery
 * stops' destinations. Counts are per trip, not per stop.
 *
 * Sorted resolved-first by label, then unresolved by raw value, with the no-value bucket last —
 * so the junk an OCR writer produced never crowds out the real places.
 */
export function buildPlaceFilterOptions(
    rows: Array<Array<string | null | undefined>>,
    maps: PlaceMaps
): PlaceFilterOption[] {
    const byKey = new Map<string, PlaceFilterOption>();

    for (const values of rows) {
        for (const key of placeKeysOf(values, maps)) {
            const existing = byKey.get(key);
            if (existing) {
                existing.count += 1;
                continue;
            }
            byKey.set(key, {
                value: key,
                label: labelForKey(key, maps),
                isUnresolved: key.startsWith("raw:"),
                count: 1,
            });
        }
    }

    return [...byKey.values()].sort((a, b) => {
        if (a.value === PLACE_FILTER_NONE) return 1;
        if (b.value === PLACE_FILTER_NONE) return -1;
        if (a.isUnresolved !== b.isUnresolved) return a.isUnresolved ? 1 : -1;
        return a.label.localeCompare(b.label, "th");
    });
}

/** Display label for an option key — the canonical name for a code, the raw string otherwise. */
function labelForKey(key: string, maps: PlaceMaps): string {
    if (key === PLACE_FILTER_NONE) return "";
    if (key.startsWith("code:")) {
        return resolveHubOrSocDisplay(key.slice("code:".length), maps.codeToLabel);
    }
    return key.slice("raw:".length);
}

/**
 * True when a trip belongs under the selected filter value.
 *
 * `values` is every place the trip can match by: one origin, or every delivery stop's destination.
 * A multi-drop trip therefore matches if ANY stop matches (ADR 0006 §5) — matching only the planned
 * `trip.destination` would make multi-drop trips unfindable by the stops they actually served.
 */
export function rowMatchesPlaceFilter(
    values: Array<string | null | undefined>,
    selected: string,
    maps: PlaceMaps
): boolean {
    if (!selected || selected === PLACE_FILTER_ALL) return true;
    return placeKeysOf(values, maps).includes(selected);
}

/** True when one raw value matches the selection — used per delivery stop when exporting (ADR 0006 §6). */
export function valueMatchesPlaceFilter(
    value: string | null | undefined,
    selected: string,
    maps: PlaceMaps
): boolean {
    if (!selected || selected === PLACE_FILTER_ALL) return true;
    return resolvePlace(value, maps).key === selected;
}
