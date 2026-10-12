import {
    addDoc,
    collection,
    getDocs,
    limit,
    orderBy,
    query,
    serverTimestamp,
    Timestamp,
    where,
} from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import {
    compensationConfigSchema,
    type CompensationConfig,
} from "@/validate/compensationConfigSchema";

type RawDoc = Record<string, unknown> & { effectiveFrom?: unknown; createdAt?: unknown; updatedAt?: unknown };

function toDate(v: unknown): unknown {
    if (v instanceof Timestamp) return v.toDate();
    return v;
}

function parseDoc(id: string, data: RawDoc): CompensationConfig {
    return compensationConfigSchema.parse({
        ...data,
        id,
        effectiveFrom: toDate(data.effectiveFrom),
        createdAt: toDate(data.createdAt),
        updatedAt: toDate(data.updatedAt),
    });
}

/** All config versions, newest effective date first. */
export async function listCompensationConfigs(): Promise<CompensationConfig[]> {
    const ref = collection(db, COLLECTIONS.DRIVER_COMPENSATION_CONFIG);
    const snap = await getDocs(query(ref, orderBy("effectiveFrom", "desc")));
    return snap.docs.map((d) => parseDoc(d.id, d.data() as RawDoc));
}

/** The config in effect at `at` (latest doc whose effectiveFrom <= at), or null if none. */
export async function getActiveCompensationConfig(at: Date = new Date()): Promise<CompensationConfig | null> {
    const ref = collection(db, COLLECTIONS.DRIVER_COMPENSATION_CONFIG);
    const snap = await getDocs(
        query(
            ref,
            where("effectiveFrom", "<=", Timestamp.fromDate(at)),
            orderBy("effectiveFrom", "desc"),
            limit(1),
        ),
    );
    if (snap.empty) return null;
    const d = snap.docs[0];
    return parseDoc(d.id, d.data() as RawDoc);
}

/** Write a new effective-dated config version (history preserved). Returns the new doc id. */
export async function saveCompensationConfig(
    input: Omit<CompensationConfig, "id" | "createdAt" | "updatedAt">,
    createdBy?: string,
): Promise<string> {
    const parsed = compensationConfigSchema
        .omit({ id: true, createdAt: true, updatedAt: true })
        .parse(input);
    const ref = collection(db, COLLECTIONS.DRIVER_COMPENSATION_CONFIG);
    const docRef = await addDoc(ref, {
        ...parsed,
        effectiveFrom: Timestamp.fromDate(parsed.effectiveFrom),
        createdBy: createdBy ?? null,
        createdAt: serverTimestamp(),
        updatedAt: serverTimestamp(),
    });
    return docRef.id;
}
