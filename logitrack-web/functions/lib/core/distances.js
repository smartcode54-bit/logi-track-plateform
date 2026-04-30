"use strict";
/**
 * 🛠️ Core Distance Logic (Pure Functions - No Firestore dependencies)
 * สำหรับคำนวณและปรับโครงสร้างตัวแปรระยะทาง Hub <-> SOC
 */
Object.defineProperty(exports, "__esModule", { value: true });
exports.SOC_KEYS = void 0;
exports.hubSourceIdHasSpxSuffix = hubSourceIdHasSpxSuffix;
exports.hubDistanceNetworkGroup = hubDistanceNetworkGroup;
exports.hubSocDistanceDocId = hubSocDistanceDocId;
exports.socHubDistanceDocId = socHubDistanceDocId;
exports.normalizeStationType = normalizeStationType;
exports.normalizeSocIdToKey = normalizeSocIdToKey;
exports.parseElement = parseElement;
exports.SOC_KEYS = ["SOCE", "SOCN", "SOCW"];
/** Hub Code / source_id ลงท้าย SPX → กลุ่ม SPX (ตรงกับ validate/hubSchema) */
function hubSourceIdHasSpxSuffix(sourceIdOrHubCode) {
    const s = String(sourceIdOrHubCode ?? "").trim().toUpperCase();
    if (!s)
        return false;
    if (s === "SPX")
        return true;
    return s.endsWith("-SPX") || s.endsWith("_SPX") || s.endsWith(".SPX");
}
/** SPX = Shopee/เดิม, SPK = J&T (รหัสขึ้นต้น SPK หรือลูกค้า SPK/J&T) */
function hubDistanceNetworkGroup(sourceId, linkedCustomerCode) {
    const id = String(sourceId ?? "").trim();
    const u = id.toUpperCase();
    if (u.startsWith("SPK"))
        return "SPK";
    const code = String(linkedCustomerCode ?? "").trim().toUpperCase();
    if (code === "SPK" || code === "J&T" || code === "JT")
        return "SPK";
    if (hubSourceIdHasSpxSuffix(id))
        return "SPX";
    return "SPX";
}
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
    return u;
}
function parseElement(row, i, j, hub, soc) {
    if (!row || !Array.isArray(row.elements))
        return null;
    const el = row.elements[j];
    if (!el || el.status !== "OK" || el.distance?.value == null || el.duration?.value == null)
        return null;
    const socId = normalizeSocIdToKey(soc.source_id);
    return { hubId: hub.source_id, socId, distanceMeters: el.distance.value, durationSeconds: el.duration.value };
}
//# sourceMappingURL=distances.js.map