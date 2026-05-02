"use strict";
/**
 * Shared Bangchak retail oil price fetch + parsing (callable + scheduled snapshot).
 */
Object.defineProperty(exports, "__esModule", { value: true });
exports.fetchBangchakRetailOilPrices = fetchBangchakRetailOilPrices;
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
                console.error("[bangchakOilFetch] Failed to parse OilList", error);
            }
        }
        return rawJson;
    }
    return Array.isArray(rawJson?.data)
        ? rawJson.data
        : [];
}
async function fetchBangchakRetailOilPrices(locale) {
    const endpoint = `https://oil-price.bangchak.co.th/ApiOilPrice2/${locale}`;
    const response = await fetch(endpoint, {
        signal: AbortSignal.timeout(15000),
        headers: {
            Accept: "application/json",
        },
    });
    if (!response.ok) {
        throw new Error(`Bangchak API HTTP ${response.status}`);
    }
    const rawJson = (await response.json());
    const sourceItems = parseOilList(rawJson);
    const items = sourceItems
        .map((entry) => toPriceItem(entry))
        .filter((entry) => entry != null);
    return {
        items,
        fetchedAtIso: new Date().toISOString(),
        source: "Bangchak ApiOilPrice2",
    };
}
//# sourceMappingURL=bangchakOilFetch.js.map