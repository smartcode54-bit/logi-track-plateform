/**
 * Hub–SOC distance: fetch hubs/SOCs from Firestore, call Google Distance Matrix API, save to hub_soc_distances.
 * Run from server only (API route) so GOOGLE_MAPS_API_KEY is not exposed.
 *
 * จับคู่ Hub↔SOC เฉพาะกลุ่มเดียวกัน (SPX vs SPK/J&T) ตาม source_id + linked customer code.
 */

import {
    collection,
    getDocs,
    writeBatch,
    doc,
    Timestamp,
    query,
    where,
    documentId,
} from "firebase/firestore";
import type { Firestore } from "firebase/firestore";
import { COLLECTIONS } from "@/lib/collections";
import { normalizeSocIdToKey } from "@/validate/taskSchema";
import { hubSocDistanceDocId } from "@/validate/hubSocDistanceSchema";
import { socHubDistanceDocId } from "@/validate/socHubDistanceSchema";
import { hubDistanceNetworkGroup, type HubDistanceNetworkGroup } from "@/validate/hubSchema";

const MAX_ELEMENTS_PER_REQUEST = 100;
const MAX_ORIGINS_PER_REQUEST = 25;
const MAX_DESTINATIONS_PER_REQUEST = 25;
const FIRESTORE_IN_CHUNK = 10;

export interface HubOrSocPoint {
    id: string;
    source_id: string;
    lat: number;
    lng: number;
    network: HubDistanceNetworkGroup;
}

function normalizeStationType(value: unknown): "HUB" | "SOC" {
    const v = String(value ?? "").toUpperCase();
    if (v === "SOC" || v === "RETURN_CENTER") return "SOC";
    return "HUB";
}

async function fetchCustomerCodesById(db: Firestore, ids: string[]): Promise<Map<string, string>> {
    const uniq = [...new Set(ids.filter((x) => typeof x === "string" && x.trim() !== ""))];
    const map = new Map<string, string>();
    for (let i = 0; i < uniq.length; i += FIRESTORE_IN_CHUNK) {
        const chunk = uniq.slice(i, i + FIRESTORE_IN_CHUNK);
        const q = query(collection(db, COLLECTIONS.CUSTOMERS), where(documentId(), "in", chunk));
        const snap = await getDocs(q);
        snap.docs.forEach((d) => {
            const code = d.data().code;
            if (typeof code === "string" && code.trim() !== "") map.set(d.id, code.trim());
        });
    }
    return map;
}

/** Read from Firestore: all HUBs and SOCs that have coordinates (ยกเว้น SOC ที่รหัสขึ้นต้น 0 = stand by) */
export async function getHubsAndSocs(db: Firestore): Promise<{ hubs: HubOrSocPoint[]; socs: HubOrSocPoint[] }> {
    const snapshot = await getDocs(collection(db, COLLECTIONS.HUBS));
    const linkedIds: string[] = [];
    snapshot.docs.forEach((d) => {
        const lid = d.data().linkedCustomerId;
        if (typeof lid === "string" && lid.trim() !== "") linkedIds.push(lid.trim());
    });
    const codeByCustomerId = await fetchCustomerCodesById(db, linkedIds);

    const hubs: HubOrSocPoint[] = [];
    const socs: HubOrSocPoint[] = [];
    const seenSocKeys = new Set<string>();
    for (const d of snapshot.docs) {
        const data = d.data();
        const lat = data.latitude ?? data.lat;
        const lng = data.longitude ?? data.lng;
        if (typeof lat !== "number" || typeof lng !== "number") continue;
        const source_id = (data.source_id ?? data.hubId ?? data.hubCode ?? "").toString();
        if (!source_id) continue;
        const lid = typeof data.linkedCustomerId === "string" ? data.linkedCustomerId.trim() : "";
        const customerCode = lid ? codeByCustomerId.get(lid) : undefined;
        const network = hubDistanceNetworkGroup(source_id, customerCode);
        const station_type = normalizeStationType(data.station_type);
        if (station_type === "HUB") {
            hubs.push({ id: d.id, source_id, lat, lng, network });
            continue;
        }
        if (station_type !== "SOC" || source_id.startsWith("0")) continue;
        const key = normalizeSocIdToKey(source_id);
        if (!key) continue;
        const dedup = `${network}_${key}`;
        if (seenSocKeys.has(dedup)) continue;
        seenSocKeys.add(dedup);
        socs.push({ id: d.id, source_id, lat, lng, network });
    }
    return { hubs, socs };
}

function parseElement(
    row: { elements: Array<{ status: string; distance?: { value: number }; duration?: { value: number } }> },
    i: number,
    j: number,
    hub: HubOrSocPoint,
    soc: HubOrSocPoint
): { hubId: string; socId: string; distanceMeters: number; durationSeconds: number } | null {
    const el = row.elements?.[j];
    if (!el || el.status !== "OK" || el.distance?.value == null || el.duration?.value == null) return null;
    const socId = normalizeSocIdToKey(soc.source_id);
    return {
        hubId: hub.source_id,
        socId,
        distanceMeters: el.distance.value,
        durationSeconds: el.duration.value,
    };
}

async function distanceMatrixRequest(
    apiKey: string,
    origins: HubOrSocPoint[],
    destinations: HubOrSocPoint[]
): Promise<{
    rows: Array<{ elements: Array<{ status: string; distance?: { value: number }; duration?: { value: number } }> }>;
}> {
    const originsStr = origins.map((p) => `${p.lat},${p.lng}`).join("|");
    const destStr = destinations.map((p) => `${p.lat},${p.lng}`).join("|");
    const params = new URLSearchParams({
        origins: originsStr,
        destinations: destStr,
        key: apiKey,
        mode: "driving",
    });
    const url = `https://maps.googleapis.com/maps/api/distancematrix/json?${params.toString()}`;
    const res = await fetch(url);
    if (!res.ok) throw new Error(`Distance Matrix API HTTP ${res.status}`);
    const json = await res.json();
    if (json.status !== "OK") {
        const msg = json.error_message || json.status || "Unknown API error";
        console.error("[Distance Matrix API] status:", json.status, "error_message:", json.error_message);
        throw new Error(`Distance Matrix API: ${msg}`);
    }
    return json;
}

export interface HubSocDistanceRow {
    hubId: string;
    socId: string;
    distanceMeters: number;
    durationSeconds: number;
    hubLat: number;
    hubLng: number;
    socLat: number;
    socLng: number;
    network: HubDistanceNetworkGroup;
}

interface SocToHubRow {
    socId: string;
    hubId: string;
    distanceMeters: number;
    durationSeconds: number;
    socLat: number;
    socLng: number;
    hubLat: number;
    hubLng: number;
    network: HubDistanceNetworkGroup;
}

async function computeRowsForNetwork(
    apiKey: string,
    hubs: HubOrSocPoint[],
    socs: HubOrSocPoint[],
    delayMs: number
): Promise<{ hubToSocRows: HubSocDistanceRow[]; socToHubRows: SocToHubRow[]; error?: string }> {
    const hubToSocRows: HubSocDistanceRow[] = [];
    const socToHubRows: SocToHubRow[] = [];

    if (hubs.length === 0 || socs.length === 0) {
        return { hubToSocRows, socToHubRows };
    }

    const batchSizeOriginsHub = Math.min(
        MAX_ORIGINS_PER_REQUEST,
        MAX_DESTINATIONS_PER_REQUEST >= socs.length ? Math.floor(MAX_ELEMENTS_PER_REQUEST / socs.length) : 0
    );
    if (batchSizeOriginsHub < 1 || socs.length > MAX_DESTINATIONS_PER_REQUEST) {
        return {
            hubToSocRows,
            socToHubRows,
            error: "Too many SOCs (max 25 per request) in one network group.",
        };
    }

    for (let start = 0; start < hubs.length; start += batchSizeOriginsHub) {
        const chunk = hubs.slice(start, start + batchSizeOriginsHub);
        const json = await distanceMatrixRequest(apiKey, chunk, socs);
        const rows = json.rows;
        if (!Array.isArray(rows)) continue;
        for (let i = 0; i < rows.length; i++) {
            const hub = chunk[i];
            if (!hub) continue;
            for (let j = 0; j < socs.length; j++) {
                const soc = socs[j];
                const parsed = parseElement(rows[i], i, j, hub, soc);
                if (parsed)
                    hubToSocRows.push({
                        ...parsed,
                        hubLat: hub.lat,
                        hubLng: hub.lng,
                        socLat: soc.lat,
                        socLng: soc.lng,
                        network: hub.network,
                    });
            }
        }
        if (start + batchSizeOriginsHub < hubs.length) await new Promise((r) => setTimeout(r, delayMs));
    }

    const batchSizeSocs = Math.min(
        MAX_ORIGINS_PER_REQUEST,
        Math.max(1, Math.floor(MAX_ELEMENTS_PER_REQUEST / Math.min(hubs.length, MAX_DESTINATIONS_PER_REQUEST)))
    );
    const batchSizeHubs = Math.min(MAX_DESTINATIONS_PER_REQUEST, hubs.length, Math.floor(MAX_ELEMENTS_PER_REQUEST / batchSizeSocs));

    for (let socStart = 0; socStart < socs.length; socStart += batchSizeSocs) {
        const socChunk = socs.slice(socStart, socStart + batchSizeSocs);
        for (let hubStart = 0; hubStart < hubs.length; hubStart += batchSizeHubs) {
            const hubChunk = hubs.slice(hubStart, hubStart + batchSizeHubs);
            const json = await distanceMatrixRequest(apiKey, socChunk, hubChunk);
            const rows = json.rows;
            if (!Array.isArray(rows)) continue;
            for (let i = 0; i < rows.length; i++) {
                const soc = socChunk[i];
                if (!soc) continue;
                for (let j = 0; j < hubChunk.length; j++) {
                    const hub = hubChunk[j];
                    const el = rows[i]?.elements?.[j];
                    if (!el || el.status !== "OK" || el.distance?.value == null || el.duration?.value == null) continue;
                    socToHubRows.push({
                        socId: normalizeSocIdToKey(soc.source_id),
                        hubId: hub.source_id,
                        distanceMeters: el.distance.value,
                        durationSeconds: el.duration.value,
                        socLat: soc.lat,
                        socLng: soc.lng,
                        hubLat: hub.lat,
                        hubLng: hub.lng,
                        network: soc.network,
                    });
                }
            }
            if (hubStart + batchSizeHubs < hubs.length) await new Promise((r) => setTimeout(r, delayMs));
        }
        if (socStart + batchSizeSocs < socs.length) await new Promise((r) => setTimeout(r, delayMs));
    }

    return { hubToSocRows, socToHubRows };
}

/** Run full flow: get hubs/SOCs, call API in batches, write to Firestore. Computes both Hub→SOC and SOC→Hub (ใช้ร่วมกัน collection เดิม). */
export async function computeAndSaveHubSocDistances(
    db: Firestore,
    apiKey: string,
    userId?: string | null
): Promise<{ written: number; hubsCount: number; socsCount: number; error?: string }> {
    const { hubs, socs } = await getHubsAndSocs(db);
    if (socs.length === 0) return { written: 0, hubsCount: hubs.length, socsCount: 0, error: "No SOCs with coordinates" };
    if (hubs.length === 0) return { written: 0, hubsCount: 0, socsCount: socs.length, error: "No Hubs with coordinates" };

    const delayMs = 200;
    const networks: HubDistanceNetworkGroup[] = ["SPX", "SPK"];
    const hubToSocRows: HubSocDistanceRow[] = [];
    const socToHubRows: SocToHubRow[] = [];
    let groupError: string | undefined;

    for (const net of networks) {
        const gh = hubs.filter((h) => h.network === net);
        const gs = socs.filter((s) => s.network === net);
        const { hubToSocRows: h2s, socToHubRows: s2h, error } = await computeRowsForNetwork(apiKey, gh, gs, delayMs);
        if (error) groupError = error;
        hubToSocRows.push(...h2s);
        socToHubRows.push(...s2h);
    }

    if (hubToSocRows.length === 0 && socToHubRows.length === 0) {
        return {
            written: 0,
            hubsCount: hubs.length,
            socsCount: socs.length,
            error:
                groupError ??
                "No Hub–SOC pairs computed (each network needs at least one Hub and one SOC with coordinates).",
        };
    }

    const now = Timestamp.now();
    const uid = userId ?? null;
    const BATCH_WRITE_LIMIT = 500;

    const hubSocCollRef = collection(db, COLLECTIONS.HUB_SOC_DISTANCES);
    const existingHubSoc = await getDocs(collection(db, COLLECTIONS.HUB_SOC_DISTANCES));
    const existingHubSocCreatedBy = new Map<string, string>();
    existingHubSoc.docs.forEach((d) => {
        const createdBy = d.data().createdBy;
        if (typeof createdBy === "string") existingHubSocCreatedBy.set(d.id, createdBy);
    });
    for (let i = 0; i < hubToSocRows.length; i += BATCH_WRITE_LIMIT) {
        const chunk = hubToSocRows.slice(i, i + BATCH_WRITE_LIMIT);
        const batch = writeBatch(db);
        for (const row of chunk) {
            const docId = hubSocDistanceDocId(row.hubId, row.socId);
            const distanceKm = row.distanceMeters / 1000;
            const durationMinutes = row.durationSeconds / 60;
            const isNew = !existingHubSocCreatedBy.has(docId);
            const createdByVal = existingHubSocCreatedBy.get(docId) ?? uid;
            const payload: Record<string, unknown> = {
                hubId: row.hubId,
                socId: row.socId,
                network: row.network,
                distanceMeters: row.distanceMeters,
                distanceKm: Math.round(distanceKm * 100) / 100,
                durationSeconds: row.durationSeconds,
                durationMinutes: Math.round(durationMinutes * 100) / 100,
                hubLat: row.hubLat,
                hubLng: row.hubLng,
                socLat: row.socLat,
                socLng: row.socLng,
                updatedAt: now,
            };
            if (createdByVal != null && createdByVal !== "") payload.createdBy = createdByVal;
            if (uid != null && uid !== "") payload.updatedBy = uid;
            if (isNew && now) payload.createdAt = now;
            batch.set(doc(hubSocCollRef, docId), payload);
        }
        await batch.commit();
    }

    const socHubCollRef = collection(db, COLLECTIONS.SOC_HUB_DISTANCES);
    const existingSocHub = await getDocs(collection(db, COLLECTIONS.SOC_HUB_DISTANCES));
    const existingSocHubCreatedBy = new Map<string, string>();
    existingSocHub.docs.forEach((d) => {
        const createdBy = d.data().createdBy;
        if (typeof createdBy === "string") existingSocHubCreatedBy.set(d.id, createdBy);
    });
    for (let i = 0; i < socToHubRows.length; i += BATCH_WRITE_LIMIT) {
        const chunk = socToHubRows.slice(i, i + BATCH_WRITE_LIMIT);
        const batch = writeBatch(db);
        for (const row of chunk) {
            const docId = socHubDistanceDocId(row.socId, row.hubId);
            const distanceKm = row.distanceMeters / 1000;
            const durationMinutes = row.durationSeconds / 60;
            const isNew = !existingSocHubCreatedBy.has(docId);
            const createdByVal = existingSocHubCreatedBy.get(docId) ?? uid;
            const payload: Record<string, unknown> = {
                socId: row.socId,
                hubId: row.hubId,
                network: row.network,
                distanceMeters: row.distanceMeters,
                distanceKm: Math.round(distanceKm * 100) / 100,
                durationSeconds: row.durationSeconds,
                durationMinutes: Math.round(durationMinutes * 100) / 100,
                socLat: row.socLat,
                socLng: row.socLng,
                hubLat: row.hubLat,
                hubLng: row.hubLng,
                updatedAt: now,
            };
            if (createdByVal != null && createdByVal !== "") payload.createdBy = createdByVal;
            if (uid != null && uid !== "") payload.updatedBy = uid;
            if (isNew && now) payload.createdAt = now;
            batch.set(doc(socHubCollRef, docId), payload);
        }
        await batch.commit();
    }

    const totalWritten = hubToSocRows.length + socToHubRows.length;
    return { written: totalWritten, hubsCount: hubs.length, socsCount: socs.length };
}
