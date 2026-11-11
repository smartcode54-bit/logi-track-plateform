/**
 * Helper utilities for displaying multi-delivery trip progress.
 */
import type { TripRecord, DeliveryStopProgress } from "@/validate/tripRecordSchema";

export interface MultiDeliveryProgressInfo {
    isMultiDelivery: boolean;
    totalStops: number;
    deliveredStops: number;
    deliveredCount: string; // e.g., "2/3 stops delivered"
    progressPercentage: number; // 0-100
    stops: Array<{
        index: number;
        destination: string;
        status: "pending" | "delivered" | "failed";
    }>;
}

/**
 * Extract multi-delivery progress info from a trip record.
 * Returns object with progress details, or null if single-delivery or incomplete data.
 */
export function getMultiDeliveryProgress(trip: Partial<TripRecord>): MultiDeliveryProgressInfo | null {
    if (!trip.isMultiDelivery) {
        return null;
    }

    const stopsProgress = (trip.deliveryStopsProgress || []) as DeliveryStopProgress[];
    const totalStops = trip.totalDeliveryStops || stopsProgress.length;

    if (totalStops < 2) {
        return null;
    }

    const deliveredStops = stopsProgress.filter((s) => s.status === "delivered").length;
    const deliveredCount = `${deliveredStops}/${totalStops}`;
    const progressPercentage = totalStops > 0 ? Math.round((deliveredStops / totalStops) * 100) : 0;

    return {
        isMultiDelivery: true,
        totalStops,
        deliveredStops,
        deliveredCount,
        progressPercentage,
        stops: stopsProgress.map((s) => ({
            index: s.index,
            destination: s.destination,
            status: s.status,
        })),
    };
}

/** A task's planned stop — only the fields needed to show it before it is delivered. */
export type PlannedDeliveryStop = { index: number; destination: string };

/**
 * Stops to DISPLAY for a trip: the task's planned stops, overlaid with whatever the driver has
 * already reported in `trip.deliveryStopsProgress`.
 *
 * `deliveryStopsProgress` is only appended to as each stop is delivered (submitDeliveryStopProgress),
 * so a freshly loaded multi-drop trip has none and a half-done one lacks its pending stops. The plan
 * lives on the task. Display-only — never write the result back to the trip (pending entries would
 * leak into billing / the mobile multi-stop page).
 */
export function mergeStopsForDisplay(
    progress: DeliveryStopProgress[] | undefined,
    planned: PlannedDeliveryStop[] | undefined
): DeliveryStopProgress[] {
    const done = progress ?? [];
    const plan = planned ?? [];
    if (plan.length === 0) return done;
    const byIndex = new Map(done.map((s) => [s.index, s]));
    const merged: DeliveryStopProgress[] = plan.map(
        (p) => byIndex.get(p.index) ?? ({ index: p.index, destination: p.destination, status: "pending" } as DeliveryStopProgress)
    );
    const plannedIdx = new Set(plan.map((p) => p.index));
    for (const s of done) if (!plannedIdx.has(s.index)) merged.push(s);
    return merged.sort((a, b) => a.index - b.index);
}

/**
 * Build a display label for multi-delivery progress.
 * E.g., "2/3 stops delivered" or "Pickup" for loading state.
 */
export function getMultiDeliveryProgressLabel(trip: Partial<TripRecord>): string | null {
    const progress = getMultiDeliveryProgress(trip);
    if (!progress) {
        return null;
    }

    // During in-transit or delivery, show progress
    return `${progress.deliveredStops}/${progress.totalStops} stops delivered`;
}

/**
 * Build HTML badge display for multi-delivery stops (visual indicators).
 * E.g., "✓ SOC-E  ✓ SOC-N  ◯ SOC-W"
 */
export function buildMultiDeliveryStopBadges(progress: MultiDeliveryProgressInfo): Array<{ destination: string; status: "pending" | "delivered" | "failed" }> {
    return progress.stops.map((stop) => ({
        destination: stop.destination,
        status: stop.status,
    }));
}
