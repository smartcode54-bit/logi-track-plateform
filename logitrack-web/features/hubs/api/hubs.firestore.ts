/**
 * P0 source of `['hubs']`: the `hubs` collection read once and mapped to the `GET /v1/hubs` DTO
 * (developer-spec.md §10.6 "Domain adapter"). Deleted with the Firebase SDK (TW7); the Go source
 * takes over when the `masterdata` flag flips (T21, P1). Loaded only through `import()` from
 * `./hubs.ts` (T25), which also routes the hub writes, the import, the distance reads and the
 * Distance Matrix callable here while the flag is off.
 *
 * This is the only place the web lists or writes the hubs collection: the source guard of
 * `hubs.test.tsx` fails on any other.
 */
import { collection, doc, getDoc, getDocs, addDoc, updateDoc, query, where, deleteField, writeBatch } from "firebase/firestore";
import { httpsCallable } from "firebase/functions";
import { db, functions } from "@/firebase/client";
import { ApiError } from "@/lib/apiError";
import { COLLECTIONS } from "@/lib/collections";
import { normalizeSocIdToKey } from "@/validate/taskSchema";
import type { ComputeDistancesResult, DistanceQuery, DistanceRow, DistancesMeta, HubDTO, HubImportResult, HubImportRow, HubInput } from "./hubs";

const text = (v: unknown): string | null => {
    if (v === undefined || v === null) return null;
    const s = String(v).trim();
    return s === "" ? null : s;
};

const coordinate = (v: unknown): number | null => (typeof v === "number" && Number.isFinite(v) ? v : null);

/**
 * One stored hub as the DTO. The legacy field names resolve in the order every page used
 * (`source_id ?? hubId ?? hubCode`, `source_name_th ?? hubTHName ?? hub_th_name ?? station_name_th`,
 * `source_name_en ?? hubName ?? station_name_en`, `latitude ?? lat`), and the document itself rides
 * along as `legacyDoc` for the pages that key maps by several of those names.
 */
export function hubFromFirestore(id: string, data: Record<string, unknown>): HubDTO {
    return {
        id,
        sourceId: String(data.source_id ?? data.hubId ?? data.hubCode ?? "").trim(),
        sourceNameTh: text(data.source_name_th ?? data.hubTHName ?? data.hub_th_name ?? data.station_name_th),
        sourceNameEn: text(data.source_name_en ?? data.hubName ?? data.station_name_en),
        latitude: coordinate(data.latitude ?? data.lat),
        longitude: coordinate(data.longitude ?? data.lng),
        stationType: text(data.station_type),
        linkedCustomerId: text(data.linkedCustomerId),
        linkedCustomerName: text(data.linkedCustomerName),
        linkedCustomerKind: text(data.customerLinkKind),
        createdByDriver: data.createdByDriver === true,
        legacyDoc: data,
    };
}

export async function fetchHubsFromFirestore(): Promise<HubDTO[]> {
    const snapshot = await getDocs(collection(db, COLLECTIONS.HUBS));
    return snapshot.docs.map((d) => hubFromFirestore(d.id, d.data() as Record<string, unknown>));
}

// --- writes (T25) ------------------------------------------------------------------------------

/** A code already used by another hub: the error Go answers for the same case. */
function codeTaken(): ApiError {
    return new ApiError({ status: 409, code: "already_exists", message: "hub code taken", details: { field: "sourceId", reason: "taken" } });
}

/** The duplicate-code probe (one `where` query): the stale-cache guard until `UNIQUE(source_id)` answers on Go. */
async function assertCodeFree(sourceId: string, exceptId?: string): Promise<void> {
    const snap = await getDocs(query(collection(db, COLLECTIONS.HUBS), where("source_id", "==", sourceId)));
    if (snap.docs.some((d) => d.id !== exceptId)) throw codeTaken();
}

function hubFields(input: HubInput): Record<string, unknown> {
    const name = (input.linkedCustomerName ?? "").trim();
    return {
        source_id: input.sourceId,
        source_name_th: input.sourceNameTh,
        source_name_en: input.sourceNameEn ?? "",
        latitude: input.latitude,
        longitude: input.longitude,
        station_type: input.stationType,
        linkedCustomerId: input.linkedCustomerId ?? "",
        linkedCustomerName: name.length > 0 ? name : deleteField(),
        customerLinkKind: input.linkedCustomerKind ?? "customer",
        updatedAt: new Date(),
    };
}

export async function createHubFirestore(input: HubInput): Promise<string> {
    await assertCodeFree(input.sourceId);
    const { linkedCustomerName, ...fields } = hubFields(input);
    const ref = await addDoc(collection(db, COLLECTIONS.HUBS), {
        ...fields,
        ...(typeof linkedCustomerName === "string" ? { linkedCustomerName } : {}),
        createdAt: new Date(),
    });
    return ref.id;
}

export async function updateHubFirestore(id: string, input: HubInput): Promise<void> {
    await assertCodeFree(input.sourceId, id);
    await updateDoc(doc(db, COLLECTIONS.HUBS, id), hubFields(input));
}

export async function importHubsFirestore(rows: HubImportRow[]): Promise<HubImportResult> {
    let created = 0;
    let updated = 0;
    const batchSize = 500;
    for (let i = 0; i < rows.length; i += batchSize) {
        const batch = writeBatch(db);
        for (const row of rows.slice(i, i + batchSize)) {
            const isUpdate = Boolean(row.firestoreDocId);
            const ref = isUpdate ? doc(db, COLLECTIONS.HUBS, row.firestoreDocId!) : doc(collection(db, COLLECTIONS.HUBS));
            const payload: Record<string, unknown> = {
                source_id: row.sourceId,
                source_name_en: row.sourceNameEn ?? "",
                latitude: row.latitude ?? null,
                longitude: row.longitude ?? null,
                station_type: row.stationType ?? "HUB",
                updatedAt: new Date(),
            };
            if (row.hasThaiColumn) payload.source_name_th = row.sourceNameTh || null;
            if (row.linkedCustomerId) {
                payload.linkedCustomerId = row.linkedCustomerId;
                payload.customerLinkKind = row.linkedCustomerKind ?? "customer";
            }
            if (isUpdate) {
                batch.set(ref, payload, { merge: true });
                updated++;
            } else {
                batch.set(ref, { ...payload, createdAt: new Date() });
                created++;
            }
        }
        await batch.commit();
    }
    return { created, updated, unchanged: 0 };
}

// --- distances (T25) ---------------------------------------------------------------------------

export async function fetchDistancesMetaFirestore(): Promise<DistancesMeta> {
    const snap = await getDoc(doc(db, COLLECTIONS.METADATA, "distances_last_calculated"));
    const ts = snap.data()?.timestamp as { toDate?: () => Date } | undefined;
    return { lastCalculatedAt: ts?.toDate ? ts.toDate().toISOString() : null };
}

export async function fetchDistanceRowsFirestore(q: DistanceQuery): Promise<DistanceRow[]> {
    const coll = q.mode === "HUB_SOC" ? COLLECTIONS.HUB_SOC_DISTANCES : COLLECTIONS.SOC_HUB_DISTANCES;
    const field = q.isSoc ? "socId" : "hubId";
    const value = q.isSoc ? normalizeSocIdToKey(q.code) : q.code;
    const snap = await getDocs(query(collection(db, coll), where(field, "==", value)));
    return snap.docs.map((d) => {
        const data = d.data();
        return {
            socId: String(data.socId ?? ""),
            destId: String(data.hubId ?? ""),
            distanceKm: Number(data.distanceKm ?? 0),
            durationMinutes: Number(data.durationMinutes ?? 0),
        };
    });
}

export async function computeDistancesFirestore(): Promise<ComputeDistancesResult> {
    const call = httpsCallable<void, { ok: boolean; written: number; hubsCount: number; socsCount: number; calculatedAt?: string; error?: string }>(
        functions,
        "computeHubSocDistances"
    );
    const { data } = await call();
    if (!data.ok && data.error) throw new ApiError({ status: 500, code: "internal", message: data.error });
    return { kind: "done", written: data.written ?? 0, hubsCount: data.hubsCount ?? 0, socsCount: data.socsCount ?? 0, calculatedAt: data.calculatedAt };
}
