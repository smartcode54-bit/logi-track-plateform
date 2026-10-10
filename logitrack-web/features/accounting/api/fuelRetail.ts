/**
 * `['fuel','bangchak',locale]`: Bangchak retail fuel prices in one language (developer-spec.md §10.6
 * "Context fixes", §10.7; Appendix B `GET /v1/fuel/retail`). The fuel page used to call the Cloud
 * Function again on every language toggle (`fuel/page.tsx:345-348`); with the locale in the key a
 * toggle reads the other language once and a toggle back reads nothing (60 min stale, 2 h gc).
 *
 * Source per fetch (flag `billing`): the `getBangchakRetailOilPrices` callable until P3, then
 * `GET /v1/fuel/retail?locale=` (T37) without a key change.
 */
import { queryOptions, type QueryFunctionContext } from "@tanstack/react-query";
import { httpsCallable } from "firebase/functions";
import { functions } from "@/firebase/client";
import { goFetch } from "@/lib/goFetch";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
import { domainQueryFn } from "@/features/platform/api/webFlags";

export interface FuelRetailItem {
    nameTh: string;
    nameEn: string;
    price: number;
    unit: string;
}

/** The body of `GET /v1/fuel/retail` (and of the callable it replaces). */
export interface FuelRetailPrices {
    locale: "th" | "en";
    fetchedAt: string;
    source: string;
    items: FuelRetailItem[];
}

export const FUEL_RETAIL_PATH = "/v1/fuel/retail";

type FuelRetailKey = ReturnType<typeof queryKeys.fuel.bangchak>;

async function fetchFromCallable({ queryKey }: QueryFunctionContext<FuelRetailKey>): Promise<FuelRetailPrices> {
    const callable = httpsCallable<{ locale: "th" | "en" }, FuelRetailPrices>(functions, "getBangchakRetailOilPrices");
    return (await callable({ locale: queryKey[2] })).data;
}

async function fetchFromGo({ queryKey, signal }: QueryFunctionContext<FuelRetailKey>): Promise<FuelRetailPrices> {
    return goFetch<FuelRetailPrices>(FUEL_RETAIL_PATH, { signal, query: { locale: queryKey[2] } });
}

export function fuelRetailQueryOptions(locale: "th" | "en") {
    return queryOptions({
        queryKey: queryKeys.fuel.bangchak(locale),
        queryFn: domainQueryFn<FuelRetailPrices, FuelRetailKey>("billing", { firestore: fetchFromCallable, go: fetchFromGo }),
        ...QUERY_POLICY.fuel,
        refetchOnWindowFocus: false,
    });
}
