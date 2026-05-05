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

/**
 * Build a display label for multi-delivery progress.
 * E.g., "2/3 stops delivered" or "Pickup" for loading state.
 */
export function getMultiDeliveryProgressLabel(trip: Partial<TripRecord>): string | null {
    const progress = getMultiDeliveryProgress(trip);
    if (!progress) {
        return null;
    }

    // During loading/departure phase, show "Pickup"
    if (trip.status === "loading" || trip.status === "departure") {
        return "Pickup";
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
