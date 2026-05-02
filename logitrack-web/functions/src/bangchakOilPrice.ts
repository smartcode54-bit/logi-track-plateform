import * as admin from "firebase-admin";
import { onCall, HttpsError } from "firebase-functions/v2/https";
import {
    fetchBangchakRetailOilPrices,
    type BangchakSupportedLocale,
} from "./core/bangchakOilFetch";

const PERMISSIONS_CONFIG_COLLECTION = "permissions_config";
const ACCOUNTING_VIEW_FUEL_CAPABILITY = "accounting:view_fuel";
const DEFAULT_ALLOWED_ROLES = new Set(["manager", "operation_staff"]);

async function canViewFuelByRole(role: string): Promise<boolean> {
    if (!role) return false;
    if (DEFAULT_ALLOWED_ROLES.has(role)) return true;

    const snap = await admin
        .firestore()
        .collection(PERMISSIONS_CONFIG_COLLECTION)
        .doc(role)
        .get();

    if (!snap.exists) return false;
    const capabilities = snap.data()?.capabilities as Record<string, unknown> | undefined;
    if (!capabilities) return false;
    return capabilities[ACCOUNTING_VIEW_FUEL_CAPABILITY] === true;
}

// App Check off: prod static builds may omit reCAPTCHA; Callable still enforces Auth + RBAC.
// Gen2 = Cloud Run: invoker "public" lets browsers reach the endpoint; IAM auth is not used for Callable + httpsCallable.
export const getBangchakRetailOilPrices = onCall(
    {
        region: "asia-southeast1",
        enforceAppCheck: false,
        invoker: "public",
    },
    async (request) => {
        if (!request.auth) {
            throw new HttpsError("unauthenticated", "User must be authenticated");
        }

        if (request.auth.token.admin !== true) {
            const role = typeof request.auth.token.role === "string" ? request.auth.token.role : "";
            const allowed = await canViewFuelByRole(role);
            if (!allowed) {
                throw new HttpsError("permission-denied", "Insufficient permission to view fuel prices");
            }
        }

        const localeRaw = (request.data as { locale?: unknown } | undefined)?.locale;
        const locale: BangchakSupportedLocale = localeRaw === "en" ? "en" : "th";

        try {
            const { items, fetchedAtIso, source } = await fetchBangchakRetailOilPrices(locale);

            return {
                locale,
                fetchedAt: fetchedAtIso,
                source,
                items,
            };
        } catch (error) {
            console.error("[getBangchakRetailOilPrices] Failed to fetch", error);
            throw new HttpsError("unavailable", "Unable to fetch Bangchak fuel prices");
        }
    }
);
