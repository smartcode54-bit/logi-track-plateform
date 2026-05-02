/**
 * Pick Bangchak retail row used as fleet diesel reference (rate card / monthly snapshot summary).
 * Priority: Hi-Diesel S → other Hi-Diesel → any diesel-grade line.
 */

export interface BangchakLikeFuelRow {
    nameTh: string;
    nameEn: string;
    price: number;
    unit: string;
}

function combinedLower(i: BangchakLikeFuelRow): string {
    return `${i.nameTh} ${i.nameEn}`.trim().toLowerCase();
}

function isHiDieselS(i: BangchakLikeFuelRow): boolean {
    const th = i.nameTh.trim();
    const n = combinedLower(i);
    if (/ไฮดีเซล\s*[sS]/.test(th) || /ไฮดีเซล\s*เอส/.test(th)) return true;
    if (/hi[- ]?diesel\s*[sS]/i.test(n)) return true;
    if ((n.includes("ไฮดีเซล") || n.includes("hi-diesel") || n.includes("hi diesel")) && /\bs\b/i.test(n)) {
        return true;
    }
    return false;
}

function isHiDiesel(i: BangchakLikeFuelRow): boolean {
    const n = combinedLower(i);
    return n.includes("ไฮดีเซล") || n.includes("hi-diesel") || n.includes("hi diesel") || n.includes("hidiesel");
}

function isAnyDieselGrade(i: BangchakLikeFuelRow): boolean {
    const n = combinedLower(i);
    return /ดีเซล/i.test(i.nameTh) || n.includes("diesel");
}

/** Prefer Hi-Diesel S (fleet pump standard), then other Hi-Diesel, then any diesel line from Bangchak list order. */
export function pickFleetBangchakDieselReference(items: BangchakLikeFuelRow[]): BangchakLikeFuelRow | undefined {
    if (!items.length) return undefined;
    return (
        items.find(isHiDieselS) ?? items.find(isHiDiesel) ?? items.find(isAnyDieselGrade) ?? undefined
    );
}

export interface MonthlyFuelSnapshotLike {
    status: "ok" | "error";
    monthKey: string;
    items: BangchakLikeFuelRow[];
}

/**
 * First OK snapshot in list order (caller should pass newest-first e.g. getFuelMonthlySnapshots) → fleet diesel THB/L.
 */
export function latestFleetDieselFromMonthlySnapshots(
    snapshots: MonthlyFuelSnapshotLike[]
): { price: number; monthKey: string } | undefined {
    for (const snap of snapshots) {
        if (snap.status !== "ok") continue;
        const row = pickFleetBangchakDieselReference(snap.items);
        if (row != null && Number.isFinite(row.price)) {
            return { price: row.price, monthKey: snap.monthKey };
        }
    }
    return undefined;
}
