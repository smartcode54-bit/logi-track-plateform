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
const PERMISSIONS_CONFIG_COLLECTION = "permissions_config";
const ACCOUNTING_VIEW_FUEL_CAPABILITY = "accounting:view_fuel";
const DEFAULT_ALLOWED_ROLES = new Set(["manager", "operation_staff"]);
function toNumber(value) {
    const n = Number(value);
    return Number.isFinite(n) ? n : null;
}
function toText(value) {
    return typeof value === "string" ? value.trim() : "";
}
function toPriceItem(raw) {
    const price = toNumber(raw["price"]) ??
        toNumber(raw["Price"]) ??
        toNumber(raw["pricePerLiter"]) ??
        toNumber(raw["PricePerLiter"]) ??
        toNumber(raw["ราคาน้ำมัน"]);
    if (price == null)
        return null;
    const nameTh = toText(raw["fuel_name_th"]) ||
        toText(raw["Fuel_Name_Th"]) ||
        toText(raw["name_th"]) ||
        toText(raw["NameTh"]) ||
        toText(raw["fuel_name"]) ||
        toText(raw["name"]) ||
        toText(raw["ชื่อ"]);
    const nameEn = toText(raw["fuel_name_en"]) ||
        toText(raw["Fuel_Name_En"]) ||
        toText(raw["name_en"]) ||
        toText(raw["NameEn"]) ||
        toText(raw["fuel_name"]) ||
        toText(raw["name"]);
    if (!nameTh && !nameEn)
        return null;
    const unit = toText(raw["unit"]) ||
        toText(raw["Unit"]) ||
        toText(raw["price_unit"]) ||
        toText(raw["หน่วย"]) ||
        "บาท/ลิตร";
    return {
        nameTh,
        nameEn,
        price,
        unit,
    };
}
function parseOilList(rawJson) {
    if (Array.isArray(rawJson)) {
        const first = rawJson[0];
        const oilListRaw = first?.OilList;
        if (typeof oilListRaw === "string" && oilListRaw.trim()) {
            try {
                const parsed = JSON.parse(oilListRaw);
                if (Array.isArray(parsed)) {
                    return parsed.map((entry) => ({
                        name: entry.OilName,
                        fuel_name: entry.OilName,
                        price: entry.PriceToday,
                        unit: entry.Unit ?? "บาท/ลิตร",
                    }));
                }
            }
            catch (error) {
                console.error("[getBangchakRetailOilPrices] Failed to parse OilList", error);
            }
        }
        return rawJson;
    }
    return Array.isArray(rawJson?.data)
        ? (rawJson.data)
        : [];
}
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
    const endpoint = `https://oil-price.bangchak.co.th/ApiOilPrice2/${locale}`;
    try {
        const response = await fetch(endpoint, {
            signal: AbortSignal.timeout(10000),
            headers: {
                Accept: "application/json",
            },
        });
        if (!response.ok) {
            throw new https_1.HttpsError("unavailable", `Bangchak API failed with status ${response.status}`);
        }
        const rawJson = (await response.json());
        const sourceItems = parseOilList(rawJson);
        const items = sourceItems
            .map((entry) => toPriceItem(entry))
            .filter((entry) => entry != null);
        return {
            locale,
            fetchedAt: new Date().toISOString(),
            source: "Bangchak ApiOilPrice2",
            items,
        };
    }
    catch (error) {
        if (error instanceof https_1.HttpsError)
            throw error;
        console.error("[getBangchakRetailOilPrices] Failed to fetch", error);
        throw new https_1.HttpsError("unavailable", "Unable to fetch Bangchak fuel prices");
    }
});
//# sourceMappingURL=bangchakOilPrice.js.map