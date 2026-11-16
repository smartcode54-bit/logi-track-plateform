/**
 * Job distance for the P&L (ADR 0031 §2, spec R8 — owner revisions 2026-11-16).
 *
 * The distance tables hold Hub↔SOC pairs only (`hub_soc_distances` / `soc_hub_distances`); there is
 * no stop→stop distance. So:
 *   - single trip    = leg(O, D) + leg(D, O)
 *   - multi-drop     = the round trip to the FARTHEST stop (km detoured between stops is off-plan km)
 *   - standby        = 2 × the one-way leg of the same truck's previous trip (it includes the drive
 *                      back to the yard); fallback = the standby's own task route (F18)
 */
import { DAY_MS } from "./settings";
import type { PnlDistanceRow, PnlRoute, PnlStandbyInput, PnlTripInput } from "./types";

export interface DistanceIndex {
    /** hub_soc_distances: "HUB>SOC" → km */
    hubSoc: Map<string, number>;
    /** soc_hub_distances: "SOC>HUB" → km */
    socHub: Map<string, number>;
}

const SOC_PREFIXES = ["SOCE", "SOCN", "SOCW"];

/** Upper-case; any "SOCN…" spelling folds onto its SOC key, as the distance callable stores it. */
function stationCode(code: string): string {
    const u = code.trim().toUpperCase();
    return SOC_PREFIXES.find((k) => u.startsWith(k)) ?? u;
}

const key = (a: string, b: string) => `${stationCode(a)}>${stationCode(b)}`;

/**
 * Key by the doc FIELDS, upper-cased — not by doc id: `hubId` is stored as the hub's raw
 * `source_id`, while trip codes come upper-cased from `extractHubId` / `normalizeDestinationCode`.
 */
export function buildDistanceIndex(rows: PnlDistanceRow[]): DistanceIndex {
    const hubSoc = new Map<string, number>();
    const socHub = new Map<string, number>();
    for (const r of rows) {
        if (!r.hubId || !r.socId || !(r.distanceKm > 0)) continue;
        if (r.table === "hub_soc") hubSoc.set(key(r.hubId, r.socId), r.distanceKm);
        else socHub.set(key(r.socId, r.hubId), r.distanceKm);
    }
    return { hubSoc, socHub };
}

/** One leg a → b: hub_soc (hubId=a, socId=b) first, else soc_hub (socId=a, hubId=b). */
export function legKm(index: DistanceIndex, a: string, b: string): number | undefined {
    const k = key(a, b);
    return index.hubSoc.get(k) ?? index.socHub.get(k);
}

export type JobDistanceResult =
    | { km: number; oneWayKm: number; farthest: string }
    | { missingLeg: [string, string] };

/** Round trip to the farthest destination (one destination = a single trip). */
export function resolveJobDistanceKm(
    origin: string | undefined,
    destinations: string[],
    index: DistanceIndex,
): JobDistanceResult {
    const o = (origin ?? "").trim();
    const dests = destinations.map((d) => d.trim()).filter(Boolean);
    if (!o || dests.length === 0) return { missingLeg: [o || "?", dests[0] ?? "?"] };
    let best: { km: number; oneWayKm: number; farthest: string } | null = null;
    for (const d of dests) {
        const out = legKm(index, o, d);
        if (out === undefined) return { missingLeg: [o, d] };
        const back = legKm(index, d, o);
        if (back === undefined) return { missingLeg: [d, o] };
        const km = out + back;
        if (!best || km > best.km) best = { km, oneWayKm: out, farthest: d };
    }
    return best!;
}

export const STANDBY_PREVIOUS_TRIP_LOOKBACK_MS = 3 * DAY_MS;

export type StandbyDistanceResult =
    | { km: number; source: "previousTrip" | "standbyTask"; previousTripId?: string }
    | { missingLeg: [string, string] };

/**
 * Standby distance = 2 × one-way leg of the previous route. "Previous route" = the latest delivered
 * trip on the SAME truck whose actual time is before the standby started, at most 3 days earlier,
 * whose route resolves (counted or not). Otherwise the standby's own task route (F18).
 */
export function standbyJobDistanceKm(
    standby: PnlStandbyInput,
    trips: PnlTripInput[],
    index: DistanceIndex,
): StandbyDistanceResult {
    const startedAt = standby.startedAt ?? standby.endedAt ?? standby.createdAt;
    if (standby.truckId && startedAt) {
        const t0 = startedAt.getTime();
        const candidates = trips
            .filter((t) => t.status === "delivered" && t.truckId === standby.truckId)
            .map((t) => ({ t, at: (t.checkInAt ?? t.deliveredAt)?.getTime() }))
            .filter((c): c is { t: PnlTripInput; at: number } =>
                c.at !== undefined && c.at < t0 && t0 - c.at <= STANDBY_PREVIOUS_TRIP_LOOKBACK_MS)
            .sort((a, b) => b.at - a.at);
        for (const c of candidates) {
            const r = resolveJobDistanceKm(c.t.originCode, c.t.destinationCodes, index);
            if ("km" in r) return { km: 2 * r.oneWayKm, source: "previousTrip", previousTripId: c.t.id };
        }
    }
    const route: PnlRoute | null = standby.taskRoute;
    if (!route) return { missingLeg: ["?", "?"] };
    const r = resolveJobDistanceKm(route.originCode, route.destinationCodes, index);
    if ("missingLeg" in r) return r;
    return { km: 2 * r.oneWayKm, source: "standbyTask" };
}
