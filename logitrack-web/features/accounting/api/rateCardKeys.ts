/**
 * The keys of the rate card's tables (T42): which `['rateCard', …]` keys a change of one table makes
 * stale. Pure, shared by the writes (`./rateCard.go.ts`) and the realtime map (`ratecard.changed`,
 * lib/realtime/eventMap.ts), which loads with the providers on every route.
 */
import type { QueryKey } from "@tanstack/react-query";
import { queryKeys } from "@/lib/queryKeys";

/** The customer filter value of every customer in reach. */
export const ALL_CUSTOMERS = "all";

/** The rate-card table names of `ratecard.changed` (internal/billing `Table*`). */
export type RateCardTable = "entries" | "fuelAdj" | "serviceFees" | "standby";

/**
 * The keys a change of `table` for `customerId` makes stale: that customer's key and the `all` view for
 * the per-customer tables, the one key of the small tables. Shared by the mutations and the SSE map.
 */
export function rateCardChangedKeys(table: RateCardTable, customerId: string | null | undefined): QueryKey[] {
    switch (table) {
        case "entries":
        case "fuelAdj": {
            const key = table === "entries" ? queryKeys.rateCard.entries : queryKeys.rateCard.fuelAdj;
            return customerId ? [key(customerId), key(ALL_CUSTOMERS)] : [["rateCard", table]];
        }
        case "serviceFees":
            return [queryKeys.rateCard.serviceFees()];
        case "standby":
            return [queryKeys.rateCard.standby()];
    }
}

export function isRateCardTable(v: unknown): v is RateCardTable {
    return v === "entries" || v === "fuelAdj" || v === "serviceFees" || v === "standby";
}
