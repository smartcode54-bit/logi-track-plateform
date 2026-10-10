"use client";

import { useQuery, type UseQueryResult } from "@tanstack/react-query";
import { hubMapsQueryOptions, hubsQueryOptions, type HubDTO, type HubMaps } from "./hubs";

/**
 * `['hubs']`: every hub and SOC, one cached list for the whole tab (10 min stale). Pass a `select`
 * from `./selectors.ts` (a module-level function, so its result is memoised) to get a page's shape.
 */
export function useHubs(): UseQueryResult<HubDTO[]>;
export function useHubs<T>(select: (hubs: HubDTO[]) => T): UseQueryResult<T>;
export function useHubs<T>(select?: (hubs: HubDTO[]) => T): UseQueryResult<T | HubDTO[]> {
    return useQuery({ ...hubsQueryOptions, select });
}

/** `['hubs','maps']`: `nameToCode` and `codeToName`, two objects, never merged (display and filters only). */
export function useHubMaps(): UseQueryResult<HubMaps> {
    return useQuery(hubMapsQueryOptions);
}
