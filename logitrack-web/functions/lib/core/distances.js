"use strict";
/**
 * 🛠️ Core Distance Logic (Pure Functions - No Firestore dependencies)
 * สำหรับคำนวณและปรับโครงสร้างตัวแปรระยะทาง Hub <-> SOC
 */
Object.defineProperty(exports, "__esModule", { value: true });
exports.SOC_KEYS = void 0;
exports.hubSocDistanceDocId = hubSocDistanceDocId;
exports.socHubDistanceDocId = socHubDistanceDocId;
exports.normalizeStationType = normalizeStationType;
exports.normalizeSocIdToKey = normalizeSocIdToKey;
exports.parseElement = parseElement;
exports.SOC_KEYS = ["SOCE", "SOCN", "SOCW"];
function hubSocDistanceDocId(originId, destinationId) {
    return `${originId}_${destinationId}`;
}
function socHubDistanceDocId(socId, hubId) {
    return `${socId}_${hubId}`;
}
function normalizeStationType(value) {
    const v = String(value ?? "").toUpperCase();
    if (v === "SOC" || v === "RETURN_CENTER")
        return "SOC";
    return "HUB";
}
function normalizeSocIdToKey(sourceId) {
    const u = (sourceId ?? "").trim().toUpperCase();
    for (const key of exports.SOC_KEYS) {
        const k = key.toUpperCase();
        if (u === k || u.startsWith(k + " ") || u.startsWith(k + "("))
            return key;
    }
    return sourceId;
}
function parseElement(row, i, j, hub, soc) {
    const el = row.elements?.[j];
    if (!el || el.status !== "OK" || el.distance?.value == null || el.duration?.value == null)
        return null;
    const socId = normalizeSocIdToKey(soc.source_id);
    return { hubId: hub.source_id, socId, distanceMeters: el.distance.value, durationSeconds: el.duration.value };
}
//# sourceMappingURL=distances.js.map