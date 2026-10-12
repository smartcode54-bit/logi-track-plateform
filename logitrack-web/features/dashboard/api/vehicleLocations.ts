/**
 * `['vehicleLocations']`: `GET /v1/vehicle-locations` (T53, Appendix B §B.2.8; `fleet:view_live_map` |
 * `dispatch:view_operations`), the latest Cartrack position of each truck under RLS: the dashboard map
 * and the current-position list read this one key (the old page read `vehicle_locations` twice,
 * `DashboardVehicleMapClient.tsx:79-82`, `CurrentVehiclePosition.tsx:33-34`).
 *
 * Freshness (Appendix E §E.6): 60 s / 5 min. The `cartrack.sync` batch of each 3-minute slot arrives as
 * `vehicle_locations.updated` on `tenant:{tid}:vehicle_locations` and is written into the cache
 * (`mergeVehiclePositions`, lib/realtime/eventMap.ts), with no refetch unless it names a truck the list
 * lacks. The batch carries the position only and never removes a truck, so the key also refetches,
 * counted from the server's last answer (`pollFromLastAnswer`, which the batch writes do not move),
 * only while the tab is visible:
 *
 * - every 60 s while the tab's stream is not open;
 * - every 60 s for a dispatcher (`me.dispatcher`), stream or not: it reads the `scope_vehicle_locations`
 *   projection (position, plate, speed and heading only), whose `p_dispatch_read` rows belong to carrier
 *   tenants whose `tenant:{tid}:vehicle_locations` topic its stream never carries (`sse.WebTopics` gives
 *   it its own tenant's). Not gated on `fleet:view_live_map`: the fixed dispatcher scope set holds it;
 * - every 5 min for staff with the stream open, which corrects what the batches omit (address, engine,
 *   driver, odometer, fuel) and drops the rows `cartrack.sync` pruned.
 */
import { useQuery } from "@tanstack/react-query";

import type { MeDTO } from "@/features/auth/api/me";
import { useMe } from "@/features/auth/api/useMe";
import { goQueryFn } from "@/lib/goQuery";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
import { POLL_INTERVAL_MS, pollFromLastAnswer, type AnswerPollOptions } from "@/lib/realtime/polling";
import { useRealtimeStatus } from "@/lib/realtime/status";
import type { VehicleLocationDTO } from "./vehicleLocationEvents";

export type { VehicleLocationDTO, VehiclePositionEvent } from "./vehicleLocationEvents";

export const VEHICLE_LOCATIONS_PATH = "/v1/vehicle-locations";

/** The refetch of `['vehicleLocations']` for staff while the stream is open (the batches keep the positions). */
export const VEHICLE_LOCATIONS_STREAM_REFRESH_MS = 5 * 60_000;

/** The refetch rule of `['vehicleLocations']`: 60 s, or 5 min for a principal whose open stream carries every row it reads. */
export function vehicleLocationsRefresh(streamOpen: boolean, dispatcher: boolean): AnswerPollOptions {
    return pollFromLastAnswer(streamOpen && !dispatcher ? VEHICLE_LOCATIONS_STREAM_REFRESH_MS : POLL_INTERVAL_MS);
}

const isDispatcher = (me: MeDTO | null) => me?.dispatcher === true;

/** `['vehicleLocations']`; `enabled` only for a principal holding `fleet:view_live_map` or `dispatch:view_operations`. */
export function useVehicleLocations(enabled = true) {
    const streamOpen = useRealtimeStatus() === "open";
    const { data: dispatcher = false } = useMe(isDispatcher);
    return useQuery({
        queryKey: queryKeys.vehicleLocations(),
        queryFn: goQueryFn<VehicleLocationDTO[]>(VEHICLE_LOCATIONS_PATH),
        ...QUERY_POLICY.list,
        ...vehicleLocationsRefresh(streamOpen, dispatcher),
        enabled,
    });
}
