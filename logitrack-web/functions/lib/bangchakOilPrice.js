"use strict";
var __createBinding = (this && this.__createBinding) || (Object.create ? (function(o, m, k, k2) {
    if (k2 === undefined) k2 = k;
    var desc = Object.getOwnPropertyDescriptor(m, k);
    if (!desc || ("get" in desc ? !m.__esModule : desc.writable || desc.configurable)) {
      desc = { enumerable: true, get: function() { return m[k]; } };
    }
    Object.defineProperty(o, k2, desc);
}) : (function(o, m, k, k2) {
    if (k2 === undefined) k2 = k;
    o[k2] = m[k];
}));
var __setModuleDefault = (this && this.__setModuleDefault) || (Object.create ? (function(o, v) {
    Object.defineProperty(o, "default", { enumerable: true, value: v });
}) : function(o, v) {
    o["default"] = v;
});
var __importStar = (this && this.__importStar) || (function () {
    var ownKeys = function(o) {
        ownKeys = Object.getOwnPropertyNames || function (o) {
            var ar = [];
            for (var k in o) if (Object.prototype.hasOwnProperty.call(o, k)) ar[ar.length] = k;
            return ar;
        };
        return ownKeys(o);
    };
    return function (mod) {
        if (mod && mod.__esModule) return mod;
        var result = {};
        if (mod != null) for (var k = ownKeys(mod), i = 0; i < k.length; i++) if (k[i] !== "default") __createBinding(result, mod, k[i]);
        __setModuleDefault(result, mod);
        return result;
    };
})();
Object.defineProperty(exports, "__esModule", { value: true });
exports.getBangchakRetailOilPrices = void 0;
const admin = __importStar(require("firebase-admin"));
const https_1 = require("firebase-functions/v2/https");
const bangchakOilFetch_1 = require("./core/bangchakOilFetch");
const PERMISSIONS_CONFIG_COLLECTION = "permissions_config";
const ACCOUNTING_VIEW_FUEL_CAPABILITY = "accounting:view_fuel";
const DEFAULT_ALLOWED_ROLES = new Set(["manager", "operation_staff"]);
async function canViewFuelByRole(role) {
    if (!role)
        return false;
    if (DEFAULT_ALLOWED_ROLES.has(role))
        return true;
    const snap = await admin
        .firestore()
        .collection(PERMISSIONS_CONFIG_COLLECTION)
        .doc(role)
        .get();
    if (!snap.exists)
        return false;
    const capabilities = snap.data()?.capabilities;
    if (!capabilities)
        return false;
    return capabilities[ACCOUNTING_VIEW_FUEL_CAPABILITY] === true;
}
// App Check off: prod static builds may omit reCAPTCHA; Callable still enforces Auth + RBAC.
// Gen2 = Cloud Run: invoker "public" lets browsers reach the endpoint; IAM auth is not used for Callable + httpsCallable.
exports.getBangchakRetailOilPrices = (0, https_1.onCall)({
    region: "asia-southeast1",
    enforceAppCheck: false,
    invoker: "public",
}, async (request) => {
    if (!request.auth) {
        throw new https_1.HttpsError("unauthenticated", "User must be authenticated");
    }
    if (request.auth.token.admin !== true) {
        const role = typeof request.auth.token.role === "string" ? request.auth.token.role : "";
        const allowed = await canViewFuelByRole(role);
        if (!allowed) {
            throw new https_1.HttpsError("permission-denied", "Insufficient permission to view fuel prices");
        }
    }
    const localeRaw = request.data?.locale;
    const locale = localeRaw === "en" ? "en" : "th";
    try {
        const { items, fetchedAtIso, source } = await (0, bangchakOilFetch_1.fetchBangchakRetailOilPrices)(locale);
        return {
            locale,
            fetchedAt: fetchedAtIso,
            source,
            items,
        };
    }
    catch (error) {
        console.error("[getBangchakRetailOilPrices] Failed to fetch", error);
        throw new https_1.HttpsError("unavailable", "Unable to fetch Bangchak fuel prices");
    }
});
//# sourceMappingURL=bangchakOilPrice.js.map