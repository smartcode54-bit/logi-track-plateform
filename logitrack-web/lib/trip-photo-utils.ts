import type { TripPhoto } from "@/validate/tripRecordSchema";

/** One entry per [type], keeping the last occurrence in array order; stable order by first-seen type index. */
export function dedupeTripPhotosByTypeLastWins(photos: TripPhoto[]): TripPhoto[] {
    const lastByType = new Map<string, TripPhoto>();
    const firstIndex = new Map<string, number>();
    for (let i = 0; i < photos.length; i++) {
        const p = photos[i];
        lastByType.set(p.type, p);
        if (!firstIndex.has(p.type)) firstIndex.set(p.type, i);
    }
    return Array.from(lastByType.entries())
        .sort((a, b) => (firstIndex.get(a[0]) ?? 0) - (firstIndex.get(b[0]) ?? 0))
        .map(([, p]) => p);
}
