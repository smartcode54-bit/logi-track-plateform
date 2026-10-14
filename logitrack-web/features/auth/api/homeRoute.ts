import { homeRouteFor } from "@/lib/routeCapabilities";
import type { MeDTO } from "./me";

/** Where `/app` lands for this principal (R89; the same rule as the `proxy.ts` gate). */
export function homeRouteOf(me: MeDTO): string {
    return homeRouteFor({
        tenantRole: me.tenant?.role || undefined,
        tenantKind: me.tenant?.kind || undefined,
        customerScope: me.customerScopes.length > 0,
        dispatcher: me.dispatcher,
        capabilities: me.capabilities,
    });
}

/**
 * A principal whose only role is driver: the `proxy.ts` gate (lib/bff/edgeGate.ts `driverOnly`, over the token's
 * `rol`, `plt`, `dsp`, `cs`) sends it to `/app/unauthorized` on every `/app` route, so it has no console to open.
 * Drivers work in the LogiTrack mobile app (UI3C: `/` and the public navigation say so instead of linking the console).
 */
export function isDriverOnly(me: MeDTO): boolean {
    return me.tenant?.role === "driver" && me.platformRoles.length === 0 && !me.dispatcher && me.customerScopes.length === 0;
}
