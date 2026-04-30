/**
 * 🛠️ Core Distance Logic (Pure Functions - No Firestore dependencies)
 * สำหรับคำนวณและปรับโครงสร้างตัวแปรระยะทาง Hub <-> SOC
 */

export const SOC_KEYS = ["SOCE", "SOCN", "SOCW"] as const;

export type HubDistanceNetworkGroup = "SPX" | "SPK";

/** Hub Code / source_id ลงท้าย SPX → กลุ่ม SPX (ตรงกับ validate/hubSchema) */
export function hubSourceIdHasSpxSuffix(sourceIdOrHubCode: string): boolean {
    const s = String(sourceIdOrHubCode ?? "").trim().toUpperCase();
    if (!s) return false;
    if (s === "SPX") return true;
    return s.endsWith("-SPX") || s.endsWith("_SPX") || s.endsWith(".SPX");
}

/** SPX = Shopee/เดิม, SPK = J&T (รหัสขึ้นต้น SPK หรือลูกค้า SPK/J&T) */
export function hubDistanceNetworkGroup(sourceId: string, linkedCustomerCode?: string | null): HubDistanceNetworkGroup {
    const id = String(sourceId ?? "").trim();
    const u = id.toUpperCase();
    if (u.startsWith("SPK")) return "SPK";

    const code = String(linkedCustomerCode ?? "").trim().toUpperCase();
    if (code === "SPK" || code === "J&T" || code === "JT") return "SPK";

    if (hubSourceIdHasSpxSuffix(id)) return "SPX";
    return "SPX";
}

export function hubSocDistanceDocId(originId: string, destinationId: string): string {
    return `${originId}_${destinationId}`;
}

export function socHubDistanceDocId(socId: string, hubId: string): string {
    return `${socId}_${hubId}`;
}

export function normalizeStationType(value: unknown): "HUB" | "SOC" {
    const v = String(value ?? "").toUpperCase();
    if (v === "SOC" || v === "RETURN_CENTER") return "SOC";
    return "HUB";
}

export function normalizeSocIdToKey(sourceId: string): string {
    const u = (sourceId ?? "").trim().toUpperCase();
    for (const key of SOC_KEYS) {
        const k = key.toUpperCase();
        if (u === k || u.startsWith(k + " ") || u.startsWith(k + "(")) return key;
    }
    return u;
}

export function parseElement(
    row: { elements?: Array<{ status: string; distance?: { value: number }; duration?: { value: number } }> } | undefined,
    i: number,
    j: number,
    hub: { source_id: string },
    soc: { source_id: string }
): { hubId: string; socId: string; distanceMeters: number; durationSeconds: number } | null {
    if (!row || !Array.isArray(row.elements)) return null;
    const el = row.elements[j];
    if (!el || el.status !== "OK" || el.distance?.value == null || el.duration?.value == null) return null;
    const socId = normalizeSocIdToKey(soc.source_id);
    return { hubId: hub.source_id, socId, distanceMeters: el.distance.value, durationSeconds: el.duration.value };
}
