/**
 * Write Bangchak retail prices into fuel_monthly_snapshots/{yyyy-MM} (Bangkok calendar month key).
 * Same doc is overwritten on each successful sync (scheduled daily or manual).
 */

import type { Firestore } from "firebase-admin/firestore";
import { FieldValue } from "firebase-admin/firestore";
import { fetchBangchakRetailOilPrices } from "./bangchakOilFetch";

export const FUEL_MONTHLY_SNAPSHOTS_COLLECTION = "fuel_monthly_snapshots";

export function bangkokMonthKey(date: Date): string {
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

export type PersistBangchakSnapshotResult =
    | { monthKey: string; ok: true; itemCount: number }
    | { monthKey: string; ok: false; itemCount: 0; errorMessage: string };

export async function persistBangchakFuelMonthlySnapshot(
    db: Firestore,
    at: Date = new Date()
): Promise<PersistBangchakSnapshotResult> {
    const monthKey = bangkokMonthKey(at);

    try {
        const { items, fetchedAtIso, source } = await fetchBangchakRetailOilPrices("th");

        await db.collection(FUEL_MONTHLY_SNAPSHOTS_COLLECTION).doc(monthKey).set({
            monthKey,
            capturedAt: FieldValue.serverTimestamp(),
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
    } catch (err) {
        const message = err instanceof Error ? err.message : String(err);

        await db.collection(FUEL_MONTHLY_SNAPSHOTS_COLLECTION).doc(monthKey).set({
            monthKey,
            capturedAt: FieldValue.serverTimestamp(),
            source: "bangchak_api",
            locale: "th",
            status: "error",
            errorMessage: message,
            items: [],
        });

        return { monthKey, ok: false, itemCount: 0, errorMessage: message };
    }
}
