"use strict";
/**
 * Write Bangchak retail prices into fuel_monthly_snapshots/{yyyy-MM} (Bangkok calendar month key).
 * Same doc is overwritten on each successful sync (scheduled daily or manual).
 */
Object.defineProperty(exports, "__esModule", { value: true });
exports.FUEL_MONTHLY_SNAPSHOTS_COLLECTION = void 0;
exports.bangkokMonthKey = bangkokMonthKey;
exports.persistBangchakFuelMonthlySnapshot = persistBangchakFuelMonthlySnapshot;
const firestore_1 = require("firebase-admin/firestore");
const bangchakOilFetch_1 = require("./bangchakOilFetch");
exports.FUEL_MONTHLY_SNAPSHOTS_COLLECTION = "fuel_monthly_snapshots";
function bangkokMonthKey(date) {
    const fmt = new Intl.DateTimeFormat("en-CA", {
        timeZone: "Asia/Bangkok",
        year: "numeric",
        month: "2-digit",
    });
    const parts = fmt.formatToParts(date);
    const y = parts.find((p) => p.type === "year")?.value ?? "1970";
    const m = parts.find((p) => p.type === "month")?.value ?? "01";
    return `${y}-${m}`;
}
async function persistBangchakFuelMonthlySnapshot(db, at = new Date()) {
    const monthKey = bangkokMonthKey(at);
    try {
        const { items, fetchedAtIso, source } = await (0, bangchakOilFetch_1.fetchBangchakRetailOilPrices)("th");
        await db.collection(exports.FUEL_MONTHLY_SNAPSHOTS_COLLECTION).doc(monthKey).set({
            monthKey,
            capturedAt: firestore_1.FieldValue.serverTimestamp(),
            fetchedAtIso,
            source,
            locale: "th",
            status: "ok",
            items: items.map((i) => ({
                nameTh: i.nameTh,
                nameEn: i.nameEn,
                price: i.price,
                unit: i.unit,
            })),
        });
        return { monthKey, ok: true, itemCount: items.length };
    }
    catch (err) {
        const message = err instanceof Error ? err.message : String(err);
        await db.collection(exports.FUEL_MONTHLY_SNAPSHOTS_COLLECTION).doc(monthKey).set({
            monthKey,
            capturedAt: firestore_1.FieldValue.serverTimestamp(),
            source: "bangchak_api",
            locale: "th",
            status: "error",
            errorMessage: message,
            items: [],
        });
        return { monthKey, ok: false, itemCount: 0, errorMessage: message };
    }
}
//# sourceMappingURL=persistFuelMonthlySnapshot.js.map