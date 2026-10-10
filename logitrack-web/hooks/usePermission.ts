"use client";

import { useCan } from "@/features/auth/api/useMe";
import type { CapabilityId } from "@/lib/capabilities";

/**
 * Whether the signed-in principal holds `capabilityId`: a selector over `['me']`, whose
 * capabilities Go resolved with the tenant's role overrides (developer-spec.md §10.6, Appendix C
 * §C.2.5). It makes no read of its own, so any number of instances cost no request (it used to
 * read `permissions_config/{role}` once per instance, `hooks/usePermission.ts:51-52`).
 *
 * This gates buttons, dialogs and in-page content only: `proxy.ts` has already decided the route
 * and Go authorises every request again.
 */
export function usePermission(capabilityId: CapabilityId) {
    const { allowed, loading } = useCan(capabilityId);
    return { hasPermission: allowed, loading };
}
