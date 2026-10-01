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
    normalizeVehicleClass,
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
                vehicleClass: normalizeVehicleClass(String(d.vehicleClass ?? "4WJ")),
                rateThb: Number(d.rateThb ?? 0),
                effectiveFromMs: toMillis(d.effectiveFrom),
                // หลัก/เสริม is a rate-lookup dimension (ADR-0005). Dropping it made every เสริม card
                // look like a หลัก card, so the client priced เสริม trips with the fuel adjustment.
                jobCategory: d.jobCategory === "SUPPLEMENTARY" ? "SUPPLEMENTARY" : "PRIMARY",
                // Without this a voided announcement would still price the web preview, and the
                // preview would disagree with the server (ADR 0009 §1).
                voided: d.voided === true,
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
                referenceFuelPriceThb:
                    d.referenceFuelPriceThbPerLitre != null
                        ? Number(d.referenceFuelPriceThbPerLitre)
                        : undefined,
                voided: d.voided === true,
            });
        });
    });
    return all;
}
