import {
    collection,
    getDocs,
    query,
    where,
    type Firestore,
    type Timestamp,
} from "firebase/firestore";
import { COLLECTIONS } from "@/lib/collections";
import type { Task } from "@/validate/taskSchema";
import type { TripRecord } from "@/validate/tripRecordSchema";

export interface BillingRateEntry {
    id: string;
    customerId: string;
    importId: string;
    hubId: string;
    destinationCode: string;
    vehicleClass: string;
    rateThb: number;
    effectiveFromMs: number;
}

export interface FuelRateAdjustment {
    id: string;
    customerId: string;
    effectiveFromMs: number;
    rateMultiplier: number;
    addThbPerTrip: number;
}

export interface TripBillingComputed {
    customerId: string;
    baseRateThb: number;
    finalRateThb: number;
    rateImportId: string;
    lookupHubId: string;
    lookupDestination: string;
    fuelAdjustmentId?: string;
    rateMultiplier: number;
    addThbPerTrip: number;
    effectiveFromDateStr?: string;
}

function toMillis(val: unknown): number {
    if (!val) return 0;
    if (typeof val === "number" && Number.isFinite(val)) return val;
    if (val instanceof Date) return val.getTime();
    if (typeof (val as { toDate?: () => Date }).toDate === "function") {
        return (val as Timestamp).toDate().getTime();
    }
    return 0;
}

function normalizeCode(v: string | null | undefined): string {
    return (v ?? "").trim().toUpperCase();
}

export function extractHubId(sourceHub: string | null | undefined): string {
    const raw = (sourceHub ?? "").trim();
    if (!raw) return "";
    const code = raw.split(" - ")[0]?.trim() ?? raw;
    return normalizeCode(code);
}

export function normalizeDestinationCode(destination: string | null | undefined): string {
    const u = normalizeCode(destination);
    if (!u) return "";
    if (u.startsWith("SOCE")) return "SOCE";
    if (u.startsWith("SOCN")) return "SOCN";
    if (u.startsWith("SOCW")) return "SOCW";
    return u;
}

export function getTripBillingDateMs(trip: TripRecord): number {
    const delivered = toMillis(trip.deliveredTimestamp);
    if (delivered > 0) return delivered;
    const created = toMillis(trip.createdAt);
    if (created > 0) return created;
    return Date.now();
}

export function resolveTaskCustomerId(task: Task | null | undefined): string {
    return (
        task?.sourceHubLinkedCustomerId?.trim() ||
        task?.destinationLinkedCustomerId?.trim() ||
        ""
    );
}

export function computeTripBilling(
    trip: TripRecord,
    task: Task | null | undefined,
    rateEntries: BillingRateEntry[],
    fuelAdjustments: FuelRateAdjustment[]
): TripBillingComputed | null {
    if (!task) return null;
    const customerId = resolveTaskCustomerId(task);
    if (!customerId) return null;

    const hubId = extractHubId(task.sourceHub);
    const destination = normalizeDestinationCode(task.destination);
    const vehicleClass = normalizeCode(task.truckType || "4WJ");
    const billDateMs = getTripBillingDateMs(trip);

    const matchedRate = rateEntries
        .filter((entry) =>
            entry.customerId === customerId &&
            entry.hubId === hubId &&
            entry.destinationCode === destination &&
            entry.vehicleClass === vehicleClass &&
            entry.effectiveFromMs <= billDateMs
        )
        .sort((a, b) => b.effectiveFromMs - a.effectiveFromMs)[0];
    if (!matchedRate) return null;

    const matchedAdjustment = fuelAdjustments
        .filter((adj) => adj.customerId === customerId && adj.effectiveFromMs <= billDateMs)
        .sort((a, b) => b.effectiveFromMs - a.effectiveFromMs)[0];

    const multiplier = matchedAdjustment?.rateMultiplier ?? 1;
    const addThbPerTrip = matchedAdjustment?.addThbPerTrip ?? 0;
    const finalRate = Math.round((matchedRate.rateThb * multiplier + addThbPerTrip) * 100) / 100;

    return {
        customerId,
        baseRateThb: matchedRate.rateThb,
        finalRateThb: finalRate,
        rateImportId: matchedRate.importId,
        lookupHubId: hubId,
        lookupDestination: destination,
        fuelAdjustmentId: matchedAdjustment?.id,
        rateMultiplier: multiplier,
        addThbPerTrip,
        effectiveFromDateStr: matchedAdjustment
            ? new Date(matchedAdjustment.effectiveFromMs).toISOString().slice(0, 10)
            : undefined,
    };
}

export async function fetchRateEntriesForCustomers(
    db: Firestore,
    customerIds: string[]
): Promise<BillingRateEntry[]> {
    const ids = Array.from(new Set(customerIds.map((id) => id.trim()).filter(Boolean)));
    if (!ids.length) return [];
    const all: BillingRateEntry[] = [];
    for (const customerId of ids) {
        const snap = await getDocs(
            query(
                collection(db, COLLECTIONS.CUSTOMER_RATE_ENTRIES),
                where("customerId", "==", customerId)
            )
        );
        snap.docs.forEach((docSnap) => {
            const d = docSnap.data();
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
    }
    return all;
}

export async function fetchFuelAdjustmentsForCustomers(
    db: Firestore,
    customerIds: string[]
): Promise<FuelRateAdjustment[]> {
    const ids = Array.from(new Set(customerIds.map((id) => id.trim()).filter(Boolean)));
    if (!ids.length) return [];
    const all: FuelRateAdjustment[] = [];
    for (const customerId of ids) {
        const snap = await getDocs(
            query(
                collection(db, COLLECTIONS.CUSTOMER_FUEL_RATE_ADJUSTMENTS),
                where("customerId", "==", customerId)
            )
        );
        snap.docs.forEach((docSnap) => {
            const d = docSnap.data();
            all.push({
                id: docSnap.id,
                customerId,
                effectiveFromMs: toMillis(d.effectiveFrom),
                rateMultiplier: Number(d.rateMultiplier ?? 1),
                addThbPerTrip: Number(d.addThbPerTrip ?? 0),
            });
        });
    }
    return all;
}
