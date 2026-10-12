/**
 * The `vehicle_locations.updated` side of `['vehicleLocations']` (T54R; Appendix B §B.4.3, Appendix E
 * §E.5.1): the batch of positions of one Cartrack sync slot is data the realtime map writes into the
 * cache (lib/realtime/eventMap.ts), not a thin invalidation. Pure: no hook, so the event map imports it
 * without the dashboard's query modules.
 */

/** One row of `GET /v1/vehicle-locations`; the fields after `updatedAt` are staff-only (absent for a dispatcher). */
export interface VehicleLocationDTO {
    truckId: string;
    tenantId: string;
    licensePlate: string | null;
    lat: number;
    lng: number;
    speedKmh: number | null;
    heading: number | null;
    lastGpsAt: string | null;
    updatedAt: string;
    gpsVehicleId?: string;
    engineOn?: boolean | null;
    positionDescription?: string | null;
    driverName?: string | null;
    odometerKm?: number | null;
    fuelLevel?: number | null;
    fuelPercentage?: number | null;
}

/** One entry of the `vehicle_locations.updated` batch (Appendix B §B.4.3; Go `locations.Position`). */
export interface VehiclePositionEvent {
    truckId: string;
    lat: number;
    lng: number;
    speed: number | null;
    heading: number | null;
    at: string;
}

function isRecord(v: unknown): v is Record<string, unknown> {
    return typeof v === "object" && v !== null && !Array.isArray(v);
}

const finite = (v: unknown): v is number => typeof v === "number" && Number.isFinite(v);
const numOrNull = (v: unknown): number | null => (finite(v) ? v : null);

/** The well-formed positions of a `vehicle_locations.updated` payload (`{positions: [...]}`); the rest is dropped. */
export function parsePositions(data: unknown): VehiclePositionEvent[] {
    if (!isRecord(data) || !Array.isArray(data.positions)) return [];
    const out: VehiclePositionEvent[] = [];
    for (const p of data.positions) {
        if (!isRecord(p) || typeof p.truckId !== "string" || p.truckId === "" || !finite(p.lat) || !finite(p.lng)) continue;
        if (Math.abs(p.lat) > 90 || Math.abs(p.lng) > 180 || typeof p.at !== "string" || Number.isNaN(Date.parse(p.at))) continue;
        out.push({ truckId: p.truckId, lat: p.lat, lng: p.lng, speed: numOrNull(p.speed), heading: numOrNull(p.heading), at: p.at });
    }
    return out;
}

/**
 * The cached list with a batch applied: each known truck takes the newer fix (latest only, as Go stores
 * it); a truck the list does not hold yet is left to the refetch the same batch causes
 * (`namesUnlistedTruck`: its plate and telemetry are not in the event). `undefined` (nothing cached)
 * stays `undefined`, so no query is created.
 */
export function mergeVehiclePositions(cached: unknown, positions: readonly VehiclePositionEvent[]): unknown {
    if (!Array.isArray(cached) || positions.length === 0) return cached;
    const byTruck = new Map(positions.map((p) => [p.truckId, p]));
    let changed = false;
    const next = (cached as VehicleLocationDTO[]).map((row) => {
        const p = byTruck.get(row.truckId);
        if (!p) return row;
        const seen = row.lastGpsAt ? Date.parse(row.lastGpsAt) : Number.NEGATIVE_INFINITY;
        if (Date.parse(p.at) <= seen) return row;
        changed = true;
        return { ...row, lat: p.lat, lng: p.lng, speedKmh: p.speed, heading: p.heading, lastGpsAt: p.at };
    });
    return changed ? next : cached;
}

/**
 * Whether a batch names a truck the cached list does not hold (its first fix after the page loaded, or a
 * GPS id just given to it): the realtime map then makes `['vehicleLocations']` stale. False without a
 * cached list (the first load is still to come). Go sends a principal the batches of the tenants in its
 * RLS reach only (Appendix B §B.4.2, `authz.TenantReach`), so a miss is a new row, not a hidden one.
 */
export function namesUnlistedTruck(cached: unknown, positions: readonly VehiclePositionEvent[]): boolean {
    if (!Array.isArray(cached) || positions.length === 0) return false;
    const listed = new Set((cached as VehicleLocationDTO[]).map((row) => row.truckId));
    return positions.some((p) => !listed.has(p.truckId));
}
