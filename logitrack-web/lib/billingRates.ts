import {
    collection,
    getDocs,
    query,
    where,
    type Firestore,
} from "firebase/firestore";
import { COLLECTIONS } from "@/lib/collections";
import {
    computeTripBillingFromParts,
    extractHubId,
    getTripBillingDateMs as getTripBillingDateMsFromTimestamps,
    normalizeDestinationCode,
    resolveTaskCustomerId as resolveTaskCustomerFromTaskInput,
    timestampLikeToMillis,
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

function toMillis(val: unknown): number {
    return timestampLikeToMillis(val);
}

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
        sourceHubLinkedCustomerId: task.sourceHubLinkedCustomerId,
        destinationLinkedCustomerId: task.destinationLinkedCustomerId,
    };
}

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
    return computeTripBillingFromParts(tripParts, taskToBillingInput(task), rateEntries, fuelAdjustments);
}

export async function fetchRateEntriesForCustomers(
    db: Firestore,
    customerIds: string[]
): Promise<BillingRateEntry[]> {
    const ids = Array.from(new Set(customerIds.map((id) => id.trim()).filter(Boolean)));
    if (!ids.length) return [];
    const snaps = await Promise.all(
        ids.map((customerId) =>
            getDocs(
                query(
                    collection(db, COLLECTIONS.CUSTOMER_RATE_ENTRIES),
                    where("customerId", "==", customerId)
                )
            )
        )
    );
    const all: BillingRateEntry[] = [];
    snaps.forEach((snap) => {
        snap.docs.forEach((docSnap) => {
            const d = docSnap.data();
            const customerId = String(d.customerId ?? "");
            all.push({
                id: docSnap.id,
                customerId,
                importId: String(d.importId ?? ""),
                hubId: normalizeCode(String(d.hubId ?? "")),
                destinationCode: normalizeDestinationCode(String(d.destinationCode ?? "")),
                vehicleClass: normalizeCode(String(d.vehicleClass ?? "4WJ")),
                rateThb: Number(d.rateThb ?? 0),
                effectiveFromMs: toMillis(d.effectiveFrom),
            });
        });
    });
    return all;
}

function normalizeCode(v: string | null | undefined): string {
    return (v ?? "").trim().toUpperCase();
}

export async function fetchFuelAdjustmentsForCustomers(
    db: Firestore,
    customerIds: string[]
): Promise<FuelRateAdjustment[]> {
    const ids = Array.from(new Set(customerIds.map((id) => id.trim()).filter(Boolean)));
    if (!ids.length) return [];
    const snaps = await Promise.all(
        ids.map((customerId) =>
            getDocs(
                query(
                    collection(db, COLLECTIONS.CUSTOMER_FUEL_RATE_ADJUSTMENTS),
                    where("customerId", "==", customerId)
                )
            )
        )
    );
    const all: FuelRateAdjustment[] = [];
    snaps.forEach((snap) => {
        snap.docs.forEach((docSnap) => {
            const d = docSnap.data();
            const customerId = String(d.customerId ?? "");
            all.push({
                id: docSnap.id,
                customerId,
                effectiveFromMs: toMillis(d.effectiveFrom),
                rateMultiplier: Number(d.rateMultiplier ?? 1),
                addThbPerTrip: Number(d.addThbPerTrip ?? 0),
            });
        });
    });
    return all;
}
