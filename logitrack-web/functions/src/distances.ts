/**
 * Cloud Function: compute Hub–SOC and SOC–Hub distances via Google Distance Matrix API,
 * write to hub_soc_distances and soc_hub_distances. Admin only.
 * Set GOOGLE_MAPS_API_KEY in functions/.env or when prompted on first deploy.
 */

import * as admin from "firebase-admin";
import { onCall, HttpsError } from "firebase-functions/v2/https";
import { defineString } from "firebase-functions/params";

const googleMapsApiKey = defineString("GOOGLE_MAPS_API_KEY", {
    description: "Google Maps API key for Distance Matrix API (used by computeHubSocDistances)",
});

const COLLECTIONS = {
    HUBS: "hubs",
    HUB_SOC_DISTANCES: "hub_soc_distances",
    SOC_HUB_DISTANCES: "soc_hub_distances",
    METADATA: "metadata",
} as const;

const SOC_KEYS = ["SOCE", "SOCN", "SOCW"] as const;

function hubSocDistanceDocId(originId: string, destinationId: string): string {
    return `${originId}_${destinationId}`;
}

function socHubDistanceDocId(socId: string, hubId: string): string {
    return `${socId}_${hubId}`;
}

interface HubOrSocPoint {
    id: string;
    source_id: string;
    lat: number;
    lng: number;
}

function normalizeStationType(value: unknown): "HUB" | "SOC" {
    const v = String(value ?? "").toUpperCase();
    if (v === "SOC" || v === "RETURN_CENTER") return "SOC";
    return "HUB";
}

function normalizeSocIdToKey(sourceId: string): string {
    const u = (sourceId ?? "").trim().toUpperCase();
    for (const key of SOC_KEYS) {
        const k = key.toUpperCase();
        if (u === k || u.startsWith(k + " ") || u.startsWith(k + "(")) return key;
    }
    return sourceId;
}

const MAX_ELEMENTS_PER_REQUEST = 100;
const MAX_ORIGINS_PER_REQUEST = 25;
const MAX_DESTINATIONS_PER_REQUEST = 25;

async function getHubsAndSocs(db: admin.firestore.Firestore): Promise<{ hubs: HubOrSocPoint[]; socs: HubOrSocPoint[] }> {
    const snapshot = await db.collection(COLLECTIONS.HUBS).get();
    const hubs: HubOrSocPoint[] = [];
    const socs: HubOrSocPoint[] = [];
    const seenKeys = new Set<string>();
    for (const d of snapshot.docs) {
        const data = d.data();
        const lat = data.latitude ?? data.lat;
        const lng = data.longitude ?? data.lng;
        if (typeof lat !== "number" || typeof lng !== "number") continue;
        const source_id = (data.source_id ?? data.hubId ?? data.hubCode ?? "").toString();
        if (!source_id) continue;
        const station_type = normalizeStationType(data.station_type);
        if (station_type === "HUB") {
            hubs.push({ id: d.id, source_id, lat, lng });
            continue;
        }
        if (station_type !== "SOC" || source_id.startsWith("0")) continue;
        const key = normalizeSocIdToKey(source_id);
        if (!SOC_KEYS.includes(key as (typeof SOC_KEYS)[number])) continue;
        if (seenKeys.has(key)) continue;
        seenKeys.add(key);
        socs.push({ id: d.id, source_id, lat, lng });
    }
    return { hubs, socs };
}

async function distanceMatrixRequest(
    apiKey: string,
    origins: HubOrSocPoint[],
    destinations: HubOrSocPoint[]
): Promise<{ rows: Array<{ elements: Array<{ status: string; distance?: { value: number }; duration?: { value: number } }> }> }> {
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
        throw new Error(`Distance Matrix API: ${msg}`);
    }
    return json;
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
    return { hubId: hub.source_id, socId, distanceMeters: el.distance.value, durationSeconds: el.duration.value };
}

export const computeHubSocDistances = onCall(
    { region: "asia-southeast1" },
    async (
        request
    ): Promise<{ ok: boolean; written: number; hubsCount: number; socsCount: number; calculatedAt?: string; error?: string }> => {
        if (request.auth?.token?.admin !== true) {
            throw new HttpsError("permission-denied", "Admin only");
        }
        const apiKey = googleMapsApiKey.value();
        if (!apiKey) {
            throw new HttpsError("failed-precondition", "GOOGLE_MAPS_API_KEY is not set. Add it to functions/.env or set it when deploying (firebase deploy --only functions).");
        }
        const userId = request.auth?.uid ?? null;
        const db = admin.firestore();

        const { hubs, socs } = await getHubsAndSocs(db);
        if (socs.length === 0) {
            return { ok: false, written: 0, hubsCount: hubs.length, socsCount: 0, error: "No SOCs with coordinates" };
        }
        if (hubs.length === 0) {
            return { ok: false, written: 0, hubsCount: 0, socsCount: socs.length, error: "No Hubs with coordinates" };
        }

        const hubToSocRows: Array<{ hubId: string; socId: string; distanceMeters: number; durationSeconds: number; hubLat: number; hubLng: number; socLat: number; socLng: number }> = [];
        const socToHubRows: Array<{ socId: string; hubId: string; distanceMeters: number; durationSeconds: number; socLat: number; socLng: number; hubLat: number; hubLng: number }> = [];
        const delayMs = 200;

        const batchSizeOriginsHub = Math.min(
            MAX_ORIGINS_PER_REQUEST,
            MAX_DESTINATIONS_PER_REQUEST >= socs.length ? Math.floor(MAX_ELEMENTS_PER_REQUEST / socs.length) : 0
        );
        if (batchSizeOriginsHub < 1 || socs.length > MAX_DESTINATIONS_PER_REQUEST) {
            return { ok: false, written: 0, hubsCount: hubs.length, socsCount: socs.length, error: "Too many SOCs (max 25 per request)." };
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
                    if (parsed) hubToSocRows.push({ ...parsed, hubLat: hub.lat, hubLng: hub.lng, socLat: soc.lat, socLng: soc.lng });
                }
            }
            if (start + batchSizeOriginsHub < hubs.length) await new Promise((r) => setTimeout(r, delayMs));
        }

        const batchSizeSocs = Math.min(MAX_ORIGINS_PER_REQUEST, Math.max(1, Math.floor(MAX_ELEMENTS_PER_REQUEST / Math.min(hubs.length, MAX_DESTINATIONS_PER_REQUEST))));
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
                        });
                    }
                }
                if (hubStart + batchSizeHubs < hubs.length) await new Promise((r) => setTimeout(r, delayMs));
            }
            if (socStart + batchSizeSocs < socs.length) await new Promise((r) => setTimeout(r, delayMs));
        }

        const now = admin.firestore.Timestamp.now();
        const BATCH_WRITE_LIMIT = 500;

        const hubSocSnap = await db.collection(COLLECTIONS.HUB_SOC_DISTANCES).get();
        const existingHubSocCreatedBy = new Map<string, string>();
        hubSocSnap.docs.forEach((d) => {
            const createdBy = d.data().createdBy;
            if (typeof createdBy === "string") existingHubSocCreatedBy.set(d.id, createdBy);
        });

        for (let i = 0; i < hubToSocRows.length; i += BATCH_WRITE_LIMIT) {
            const chunk = hubToSocRows.slice(i, i + BATCH_WRITE_LIMIT);
            const batch = db.batch();
            for (const row of chunk) {
                const docId = hubSocDistanceDocId(row.hubId, row.socId);
                const distanceKm = row.distanceMeters / 1000;
                const durationMinutes = row.durationSeconds / 60;
                const isNew = !existingHubSocCreatedBy.has(docId);
                const createdByVal = existingHubSocCreatedBy.get(docId) ?? userId;
                const payload: Record<string, unknown> = {
                    hubId: row.hubId,
                    socId: row.socId,
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
                if (userId != null && userId !== "") payload.updatedBy = userId;
                if (isNew) payload.createdAt = now;
                batch.set(db.collection(COLLECTIONS.HUB_SOC_DISTANCES).doc(docId), payload);
            }
            await batch.commit();
        }

        const socHubSnap = await db.collection(COLLECTIONS.SOC_HUB_DISTANCES).get();
        const existingSocHubCreatedBy = new Map<string, string>();
        socHubSnap.docs.forEach((d) => {
            const createdBy = d.data().createdBy;
            if (typeof createdBy === "string") existingSocHubCreatedBy.set(d.id, createdBy);
        });

        for (let i = 0; i < socToHubRows.length; i += BATCH_WRITE_LIMIT) {
            const chunk = socToHubRows.slice(i, i + BATCH_WRITE_LIMIT);
            const batch = db.batch();
            for (const row of chunk) {
                const docId = socHubDistanceDocId(row.socId, row.hubId);
                const distanceKm = row.distanceMeters / 1000;
                const durationMinutes = row.durationSeconds / 60;
                const isNew = !existingSocHubCreatedBy.has(docId);
                const createdByVal = existingSocHubCreatedBy.get(docId) ?? userId;
                const payload: Record<string, unknown> = {
                    socId: row.socId,
                    hubId: row.hubId,
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
                if (userId != null && userId !== "") payload.updatedBy = userId;
                if (isNew) payload.createdAt = now;
                batch.set(db.collection(COLLECTIONS.SOC_HUB_DISTANCES).doc(docId), payload);
            }
            await batch.commit();
        }

        const totalWritten = hubToSocRows.length + socToHubRows.length;

        // Save last calculated timestamp for UI display
        await db
            .collection(COLLECTIONS.METADATA)
            .doc("distances_last_calculated")
            .set({ timestamp: now });

        const calculatedAtIso = now.toDate().toISOString();
        return {
            ok: true,
            written: totalWritten,
            hubsCount: hubs.length,
            socsCount: socs.length,
            calculatedAt: calculatedAtIso,
        };
    }
);
