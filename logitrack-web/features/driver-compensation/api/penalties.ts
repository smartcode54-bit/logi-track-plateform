import {
    addDoc,
    collection,
    doc,
    getDocs,
    limit,
    orderBy,
    query,
    serverTimestamp,
    Timestamp,
    updateDoc,
    where,
} from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import {
    driverPenaltySchema,
    type DriverPenalty,
} from "@/validate/driverPenaltySchema";

type RawDoc = Record<string, unknown>;

function toDate(v: unknown): unknown {
    if (v instanceof Timestamp) return v.toDate();
    return v;
}

function parse(id: string, data: RawDoc): DriverPenalty {
    return driverPenaltySchema.parse({
        ...data,
        id,
        incurredAt: toDate(data.incurredAt),
        createdAt: toDate(data.createdAt),
        updatedAt: toDate(data.updatedAt),
    });
}

/** Most recent penalties (for the admin list). */
export async function listPenalties(max = 200): Promise<DriverPenalty[]> {
    const ref = collection(db, COLLECTIONS.DRIVER_PENALTIES);
    const snap = await getDocs(query(ref, orderBy("incurredAt", "desc"), limit(max)));
    return snap.docs.map((d) => parse(d.id, d.data() as RawDoc));
}

/** Open (deductible) penalties for one driver. */
export async function listOpenPenaltiesForDriver(driverId: string): Promise<DriverPenalty[]> {
    const ref = collection(db, COLLECTIONS.DRIVER_PENALTIES);
    const snap = await getDocs(
        query(
            ref,
            where("driverId", "==", driverId),
            where("status", "in", ["pending", "partially_deducted"]),
        ),
    );
    return snap.docs.map((d) => parse(d.id, d.data() as RawDoc));
}

export async function createPenalty(
    input: Omit<DriverPenalty, "id" | "remainingThb" | "installmentsPaid" | "status" | "createdAt" | "updatedAt">,
    createdBy?: string,
): Promise<string> {
    const parsed = driverPenaltySchema
        .omit({ id: true, createdAt: true, updatedAt: true })
        .parse({ ...input, remainingThb: input.totalThb, installmentsPaid: 0, status: "pending" });
    const ref = collection(db, COLLECTIONS.DRIVER_PENALTIES);
    const docRef = await addDoc(ref, {
        ...parsed,
        incurredAt: Timestamp.fromDate(parsed.incurredAt),
        createdBy: createdBy ?? null,
        createdAt: serverTimestamp(),
        updatedAt: serverTimestamp(),
    });
    return docRef.id;
}

export async function cancelPenalty(id: string): Promise<void> {
    await updateDoc(doc(db, COLLECTIONS.DRIVER_PENALTIES, id), {
        status: "cancelled",
        updatedAt: serverTimestamp(),
    });
}
