/**
 * The incident flags of the dashboard map (T54R; Appendix E §E.2.1): the first keyset page of
 * `GET /v1/incidents?from&to` (T30; `operations:view_incidents` | `dispatch:view_operations`), the newest
 * reports of the dashboard's period, labelled "latest N" on the map. Replaces the 2000-document read the
 * map and the stats card each made (`DashboardVehicleMapClient.tsx:79-82`, `DashboardStats.tsx:140-146`);
 * the on-time figures come from the summary.
 *
 * `['incidents',{from,to}]` is the infinite key of the incident list (Appendix E §E.6), so the incident
 * page shares the cache; the dashboard never asks for a second page. 30 s / 5 min, refetched on focus when
 * stale (`incident.created` invalidates `['incidents']` once the operations rows of the event map land).
 */
import { useInfiniteQuery } from "@tanstack/react-query";

import { goInfiniteQueryFn, goNextPageParam } from "@/lib/goQuery";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
import type { DashboardRange } from "./summary";

export const INCIDENTS_PATH = "/v1/incidents";

/** The fields of a `GET /v1/incidents` row the map reads (the row carries more). */
export interface IncidentMarkerDTO {
    id: string;
    truckId: string | null;
    truckLicensePlate: string | null;
    createdAt: string;
}

export function incidentsKey(range: DashboardRange) {
    return queryKeys.incidents({ from: range.from, to: range.to });
}

type IncidentsKey = ReturnType<typeof incidentsKey>;

/** The first page of `['incidents',{from,to}]`; `enabled` only while the map is shown to a principal who may list incidents. */
export function useRecentIncidents(range: DashboardRange, enabled = true) {
    return useInfiniteQuery({
        queryKey: incidentsKey(range),
        queryFn: goInfiniteQueryFn<IncidentMarkerDTO, IncidentsKey>(INCIDENTS_PATH, {
            query: (key) => {
                const p = key[1] as DashboardRange;
                return { from: p.from, to: p.to };
            },
        }),
        initialPageParam: undefined as string | undefined,
        getNextPageParam: goNextPageParam,
        ...QUERY_POLICY.live,
        enabled,
    });
}

/** The trucks (by id and by plate) with an incident on the loaded page. */
export function incidentTrucks(rows: readonly IncidentMarkerDTO[]): { ids: Set<string>; plates: Set<string> } {
    const ids = new Set<string>();
    const plates = new Set<string>();
    for (const r of rows) {
        if (r.truckId) ids.add(r.truckId);
        const plate = r.truckLicensePlate?.trim();
        if (plate) plates.add(plate);
    }
    return { ids, plates };
}

/** Whether a position's truck is named by the loaded incident page (by id, else by plate). */
export function hasIncident(v: { truckId: string; licensePlate: string | null }, ids: ReadonlySet<string>, plates: ReadonlySet<string>): boolean {
    const plate = v.licensePlate?.trim();
    return ids.has(v.truckId) || (plate !== undefined && plate !== "" && plates.has(plate));
}
