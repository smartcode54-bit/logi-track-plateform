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
