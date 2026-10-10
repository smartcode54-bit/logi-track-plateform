"use client";

import { useCallback } from "react";
import { useQuery, type UseQueryResult } from "@tanstack/react-query";
import type { CapabilityId } from "@/lib/capabilities";
import { hasCapability, meQueryOptions, myTenantsQueryOptions, type MeDTO } from "./me";

/**
 * `['me']` (`GET /v1/me`): the signed-in principal, `null` when signed out. Pass a `select` to
 * re-render only when the selected part changes (wrap it in `useCallback` or define it at module
 * scope); every caller shares one request and one cache entry.
 */
export function useMe(): UseQueryResult<MeDTO | null>;
export function useMe<T>(select: (me: MeDTO | null) => T): UseQueryResult<T>;
export function useMe<T>(select?: (me: MeDTO | null) => T): UseQueryResult<T | MeDTO | null> {
    return useQuery({ ...meQueryOptions, select });
}

/**
 * Whether the principal holds `capability` (a catalog key, or a legacy id from lib/capabilities.ts):
 * a selector over `['me']` with no read of its own. `loading` is true until `['me']` first resolves.
 */
export function useCan(capability: CapabilityId | string): { allowed: boolean; loading: boolean } {
    const select = useCallback((me: MeDTO | null) => hasCapability(me, capability), [capability]);
    const { data, isPending } = useMe(select);
    return { allowed: data === true, loading: isPending };
}

/** `['me','tenants']` (`GET /v1/me/tenants`, the tenant switcher); disabled until a principal is signed in. */
export function useMyTenants(enabled: boolean) {
    return useQuery(myTenantsQueryOptions(enabled));
}
