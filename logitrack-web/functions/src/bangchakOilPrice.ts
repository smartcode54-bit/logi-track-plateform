import * as admin from "firebase-admin";
import { onCall, HttpsError } from "firebase-functions/v2/https";

const PERMISSIONS_CONFIG_COLLECTION = "permissions_config";
const ACCOUNTING_VIEW_FUEL_CAPABILITY = "accounting:view_fuel";
const DEFAULT_ALLOWED_ROLES = new Set(["manager", "operation_staff"]);

interface BangchakRawItem {
    [key: string]: unknown;
}

interface BangchakOilListItem {
    OilName?: unknown;
    PriceToday?: unknown;
    Unit?: unknown;
    [key: string]: unknown;
}

interface BangchakPriceItem {
    nameTh: string;
    nameEn: string;
    price: number;
    unit: string;
}

type SupportedLocale = "th" | "en";

function toNumber(value: unknown): number | null {
    const n = Number(value);
    return Number.isFinite(n) ? n : null;
}

function toText(value: unknown): string {
    return typeof value === "string" ? value.trim() : "";
}

function toPriceItem(raw: BangchakRawItem): BangchakPriceItem | null {
    const price =
        toNumber(raw["price"]) ??
        toNumber(raw["Price"]) ??
        toNumber(raw["pricePerLiter"]) ??
        toNumber(raw["PricePerLiter"]) ??
        toNumber(raw["ราคาน้ำมัน"]);

    if (price == null) return null;

    const nameTh =
        toText(raw["fuel_name_th"]) ||
        toText(raw["Fuel_Name_Th"]) ||
        toText(raw["name_th"]) ||
        toText(raw["NameTh"]) ||
        toText(raw["fuel_name"]) ||
        toText(raw["name"]) ||
        toText(raw["ชื่อ"]);

    const nameEn =
        toText(raw["fuel_name_en"]) ||
        toText(raw["Fuel_Name_En"]) ||
        toText(raw["name_en"]) ||
        toText(raw["NameEn"]) ||
        toText(raw["fuel_name"]) ||
        toText(raw["name"]);

    if (!nameTh && !nameEn) return null;

    const unit =
        toText(raw["unit"]) ||
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

function parseOilList(rawJson: unknown): BangchakRawItem[] {
    if (Array.isArray(rawJson)) {
        const first = rawJson[0] as { OilList?: unknown } | undefined;
        const oilListRaw = first?.OilList;
        if (typeof oilListRaw === "string" && oilListRaw.trim()) {
            try {
                const parsed = JSON.parse(oilListRaw) as BangchakOilListItem[];
                if (Array.isArray(parsed)) {
                    return parsed.map((entry) => ({
                        name: entry.OilName,
                        fuel_name: entry.OilName,
                        price: entry.PriceToday,
                        unit: entry.Unit ?? "บาท/ลิตร",
                    }));
                }
            } catch (error) {
                console.error("[getBangchakRetailOilPrices] Failed to parse OilList", error);
            }
        }
        return rawJson as BangchakRawItem[];
    }

    return Array.isArray((rawJson as { data?: unknown })?.data)
        ? ((rawJson as { data: BangchakRawItem[] }).data)
        : [];
}

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

export const getBangchakRetailOilPrices = onCall(async (request) => {
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
    const locale: SupportedLocale = localeRaw === "en" ? "en" : "th";
    const endpoint = `https://oil-price.bangchak.co.th/ApiOilPrice2/${locale}`;

    try {
        const response = await fetch(endpoint, {
            signal: AbortSignal.timeout(10000),
            headers: {
                Accept: "application/json",
            },
        });

        if (!response.ok) {
            throw new HttpsError(
                "unavailable",
                `Bangchak API failed with status ${response.status}`
            );
        }

        const rawJson = (await response.json()) as unknown;
        const sourceItems: BangchakRawItem[] = parseOilList(rawJson);

        const items = sourceItems
            .map((entry) => toPriceItem(entry))
            .filter((entry): entry is BangchakPriceItem => entry != null);

        return {
            locale,
            fetchedAt: new Date().toISOString(),
            source: "Bangchak ApiOilPrice2",
            items,
            raw: rawJson,
        };
    } catch (error) {
        if (error instanceof HttpsError) throw error;
        console.error("[getBangchakRetailOilPrices] Failed to fetch", error);
        throw new HttpsError("unavailable", "Unable to fetch Bangchak fuel prices");
    }
});
