/**
 * `['vehicleLocations']`: `GET /v1/vehicle-locations` (T53, Appendix B §B.2.8; `fleet:view_live_map` |
 * `dispatch:view_operations`), the latest Cartrack position of each truck under RLS: the dashboard map
 * and the current-position list read this one key (the old page read `vehicle_locations` twice,
 * `DashboardVehicleMapClient.tsx:79-82`, `CurrentVehiclePosition.tsx:33-34`).
 *
 * Freshness (Appendix E §E.6): 60 s / 5 min, stream-driven. The `cartrack.sync` batch of each 3-minute
 * slot arrives as `vehicle_locations.updated` on `tenant:{tid}:vehicle_locations` and is written into the
 * cache (`mergeVehiclePositions`, lib/realtime/eventMap.ts), no refetch; while the tab's stream is not
 * open the key polls every 60 s instead (`useStreamFallbackPoll`). A dispatcher reads the
 * `scope_vehicle_locations` projection: position, plate, speed and heading only.
 */
import { useQuery } from "@tanstack/react-query";

import { goQueryFn } from "@/lib/goQuery";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
import { useStreamFallbackPoll } from "@/lib/realtime/polling";
import type { VehicleLocationDTO } from "./vehicleLocationEvents";

export type { VehicleLocationDTO, VehiclePositionEvent } from "./vehicleLocationEvents";

export const VEHICLE_LOCATIONS_PATH = "/v1/vehicle-locations";

/** `['vehicleLocations']`; `enabled` only for a principal holding `fleet:view_live_map` or `dispatch:view_operations`. */
export function useVehicleLocations(enabled = true) {
    const poll = useStreamFallbackPoll();
    return useQuery({
        queryKey: queryKeys.vehicleLocations(),
        queryFn: goQueryFn<VehicleLocationDTO[]>(VEHICLE_LOCATIONS_PATH),
        ...QUERY_POLICY.list,
        ...poll,
        enabled,
    });
}
