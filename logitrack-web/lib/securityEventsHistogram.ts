import { Timestamp } from "firebase/firestore";
import type { SecurityEventRow } from "@/hooks/useSecurityEventsFeed";

const HOUR_MS = 60 * 60 * 1000;
const DAY_MS = 24 * HOUR_MS;

/**
 * Bucket counts for a bar chart: left = older window, right = newest.
 * Uses `rows` from a newest-first query; only events inside the window are counted.
 */
export function buildSecurityEventHistogram(rows: SecurityEventRow[], mode: "24h" | "7d"): number[] {
    const now = Date.now();
    const slotCount = mode === "24h" ? 24 : 7;
    const windowMs = mode === "24h" ? 24 * HOUR_MS : 7 * DAY_MS;
    const start = now - windowMs;
    const counts = new Array(slotCount).fill(0);

    for (const r of rows) {
        if (!r.createdAt || !(r.createdAt instanceof Timestamp)) continue;
        const t = r.createdAt.toMillis();
        if (t < start || t > now) continue;
        const age = now - t;
        if (mode === "24h") {
            const slot = Math.min(23, Math.floor(age / HOUR_MS));
            counts[23 - slot] += 1;
        } else {
            const slot = Math.min(6, Math.floor(age / DAY_MS));
            counts[6 - slot] += 1;
        }
    }

    return counts;
}
