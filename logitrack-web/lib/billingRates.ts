/**
 * The web's trip-pricing helpers over lib/billingCompute.ts: the reference implementation the Go
 * golden vectors are exported from (logitrack-api/testdata/golden/billing, §6.16). Pricing itself runs
 * on the server only. TW7 removed the Firestore readers of the rate entries and fuel adjustments
 * (`fetchRateEntriesForCustomers`, `fetchFuelAdjustmentsForCustomers`) with the Firestore pages.
 */
import {
    computeTripBillingFromParts,
    getTripBillingDateMs as getTripBillingDateMsFromTimestamps,
    resolveTaskCustomerId as resolveTaskCustomerFromTaskInput,
    type BillingRateEntry,
    type FuelRateAdjustment,
    type TaskBillingInput,
    type TripBillingComputed,
    type TripBillingTimestamps,
} from "@/lib/billingCompute";
import type { Task } from "@/validate/taskSchema";
import type { TripRecord } from "@/validate/tripRecordSchema";

export type { BillingRateEntry, FuelRateAdjustment, TripBillingComputed } from "@/lib/billingCompute";

export { extractHubId, normalizeDestinationCode } from "@/lib/billingCompute";

export function getTripBillingDateMs(trip: TripRecord): number {
    return getTripBillingDateMsFromTimestamps({
        deliveredTimestamp: trip.deliveredTimestamp,
        createdAt: trip.createdAt,
    });
}

export function resolveTaskCustomerId(task: Task | null | undefined): string {
    return resolveTaskCustomerFromTaskInput(task as TaskBillingInput | null | undefined);
}

function taskToBillingInput(task: Task | null | undefined): TaskBillingInput | null {
    if (!task) return null;
    return {
        sourceHub: task.sourceHub,
        destination: task.destination,
        truckType: task.truckType,
        // Explicit billing customer chosen at assign wins over the hub link (ADR 0027) — same as server.
        billingCustomerId: task.billingCustomerId,
        sourceHubLinkedCustomerId: task.sourceHubLinkedCustomerId,
        destinationLinkedCustomerId: task.destinationLinkedCustomerId,
    };
}

/**
 * Price a trip with the same หลัก/เสริม rule the server snapshot uses (`tripBillingOnDelivered`,
 * ADR-0006): an explicit category on the task is authoritative — เสริม prices from the เสริม card
 * only (never fuel-adjusted), หลัก from the หลัก card only. Only a legacy task with no category
 * falls back PRIMARY → SUPPLEMENTARY. Probing PRIMARY first for every trip is what priced เสริม
 * trips with fuel on the client.
 *
 * Display/estimate only — billing snapshots are written by the server callable, never from here.
 */
export function computeTripBilling(
    trip: TripRecord,
    task: Task | null | undefined,
    rateEntries: BillingRateEntry[],
    fuelAdjustments: FuelRateAdjustment[]
): TripBillingComputed | null {
    const tripParts: TripBillingTimestamps = {
        deliveredTimestamp: trip.deliveredTimestamp,
        createdAt: trip.createdAt,
    };
    const input = taskToBillingInput(task);
    const explicit = task?.jobCategory;
    if (explicit === "SUPPLEMENTARY" || explicit === "PRIMARY") {
        return computeTripBillingFromParts(tripParts, input, rateEntries, fuelAdjustments, explicit);
    }
    return (
        computeTripBillingFromParts(tripParts, input, rateEntries, fuelAdjustments, "PRIMARY") ??
        computeTripBillingFromParts(tripParts, input, rateEntries, fuelAdjustments, "SUPPLEMENTARY")
    );
}
