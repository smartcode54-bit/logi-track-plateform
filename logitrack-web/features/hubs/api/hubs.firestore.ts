/**
 * P0 source of `['hubs']`: the `hubs` collection read once and mapped to the `GET /v1/hubs` DTO
 * (developer-spec.md §10.6 "Domain adapter"). Deleted with the Firebase SDK (TW7); the Go source
 * takes over when the `masterdata` flag flips (T21, P1).
 *
 * This is the only place the web lists the hubs collection: `hubs.session.test.ts` fails on any other
 * page-level read.
 */
import { collection, getDocs } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import type { HubDTO } from "./hubs";

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
