/** Navigation presentation only (UI3F); route gates, capabilities and API code must not use this module. */
import type { MeDTO } from "../api/me";
import { isCustomerView } from "./principalLabel";

export { isCustomerView };

export type NavigationProfile = "staff" | "customer" | "partner";

/** `me.tenant` is the current active membership, not another tenant available in the switcher. */
export function navigationProfileOf(me: MeDTO | null | undefined): NavigationProfile {
    if (!me || me.platformRoles.length > 0) return "staff";
    if (me.tenant?.kind === "carrier") return "partner";
    return isCustomerView(me) ? "customer" : "staff";
}

type ProfileRouteLabelKey = "nav.customerTrips" | "nav.partnerTrips" | "nav.scopedTasks" | "nav.deliveryIssues";

/** The override for a canonical menu href; other destinations keep their catalog label. */
export function profileRouteLabelKey(href: string, profile: NavigationProfile): ProfileRouteLabelKey | undefined {
    if (profile === "staff") return undefined;
    switch (href) {
        case "/app/driver-monitor":
            return profile === "customer" ? "nav.customerTrips" : "nav.partnerTrips";
        case "/app/job-assign":
            return "nav.scopedTasks";
        case "/app/incident-reports":
            return "nav.deliveryIssues";
        default:
            return undefined;
    }
}

/** Ordered presentation allowlists; consumers still intersect them with allowed routes and retain the home. */
export const CUSTOMER_MENU_HREFS = ["/app/driver-monitor", "/app/job-assign", "/app/incident-reports", "/app/support"] as const;

export const PARTNER_MENU = [
    { href: "/app/driver-monitor" },
    { href: "/app/job-assign" },
    { href: "/app/incident-reports" },
    { href: "/app/chat" },
    {
        labelKey: "nav.partnerFleet",
        items: [{ href: "/app/trucks" }, { href: "/app/drivers" }, { href: "/app/maintenance" }],
    },
    { href: "/app/accounting/billing-document" },
    { href: "/app/support" },
] as const;

export const PARTNER_MENU_HREFS: readonly string[] = PARTNER_MENU.flatMap((entry) => ("items" in entry ? entry.items.map((item) => item.href) : [entry.href]));
