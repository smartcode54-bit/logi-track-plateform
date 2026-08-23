import { SOC_DESTINATIONS, SOC_KEYS, normalizeSocIdToKey } from "@/validate/taskSchema";

/**
 * ป้ายหลักสำหรับจุด Hub/SOC: ไทย → อังกฤษ → ชื่อลูกค้าที่ผูก → รหัส
 * J&T hubs (เช่น SPK-GW) มักเก็บชื่อจริงไว้ที่ `linkedCustomerName` ไม่ใช่ source_name_* — จึง fallback
 * ไป linkedCustomerName ก่อนรหัส และข้ามค่าที่เท่ากับรหัส (กัน "SPK-GW" โผล่เป็นชื่อ)
 */
export function primaryHubLabelFromFirestoreData(data: Record<string, unknown>): string {
    const code = String(data.source_id ?? data.hubId ?? data.hubCode ?? "").trim();
    const en = String(data.source_name_en ?? data.hubName ?? data.station_name_en ?? "").trim();
    const th = String(
        data.source_name_th ?? data.hubTHName ?? data.hub_th_name ?? data.station_name_th ?? ""
    ).trim();
    const cust = String(data.linkedCustomerName ?? "").trim();
    return [th, en, cust].find((v) => v && v !== code) ?? code;
}

/**
 * ป้ายสำหรับ "การวางบิล": อังกฤษ → ไทย → รหัส
 * ชื่อภาษาอังกฤษของจุดรับ-ส่ง (source_name_en) คือชื่อที่ใช้บนเอกสารวางบิล/ใบแจ้งหนี้
 */
export function billingHubLabelFromFirestoreData(data: Record<string, unknown>): string {
    const code = String(data.source_id ?? data.hubId ?? data.hubCode ?? "").trim();
    const en = String(data.source_name_en ?? data.hubName ?? data.station_name_en ?? "").trim();
    const th = String(
        data.source_name_th ?? data.hubTHName ?? data.hub_th_name ?? data.station_name_th ?? ""
    ).trim();
    return en || th || code;
}

export type HubDisplayEntry = {
    source_id: string;
    source_name_en?: string;
    source_name_th?: string;
    linkedCustomerName?: string;
};

/** แผนที่รหัสจุด → ข้อความแสดง (รวม SOCE/SOCN/SOCW จาก SOC_DESTINATIONS เมื่อไม่มีใน hubs) */
export function buildHubCodeToDisplayMapFromEntries(entries: HubDisplayEntry[]): Record<string, string> {
    const map: Record<string, string> = {};
    for (const h of entries) {
        const id = String(h.source_id ?? "").trim();
        if (!id) continue;
        const en = String(h.source_name_en ?? "").trim();
        const th = String(h.source_name_th ?? "").trim();
        // linkedCustomerName fallback for J&T hubs whose real name lives on the customer, not source_name_*.
        const cust = String(h.linkedCustomerName ?? "").trim();
        map[id] = [th, en, cust].find((v) => v && v !== id) ?? id;
    }
    for (const key of SOC_KEYS) {
        if (!map[key]) {
            map[key] = SOC_DESTINATIONS[key as keyof typeof SOC_DESTINATIONS];
        }
    }
    return map;
}

/** จากแถว `{ 'Hub Code', 'Hub Name', 'Hub Name Th'? }` ที่ใช้ในหน้า first-mile / line-haul */
export function buildHubCodeToDisplayMapFromHubRows(hubs: Array<Record<string, unknown>>): Record<string, string> {
    const entries: HubDisplayEntry[] = hubs.map((h) => ({
        source_id: String(h["Hub Code"] ?? "").trim(),
        source_name_en: String(h["Hub Name"] ?? "").trim() || undefined,
        source_name_th: String(h["Hub Name Th"] ?? "").trim() || undefined,
        linkedCustomerName: String(h["linkedCustomerName"] ?? "").trim() || undefined,
    }));
    return buildHubCodeToDisplayMapFromEntries(entries);
}

/** แก้ชื่อที่แสดงจากรหัสดิบ (origin/destination บนงานหรือ trip) */
export function resolveHubOrSocDisplay(
    raw: string | null | undefined,
    codeToLabel: Record<string, string>
): string {
    if (raw == null || String(raw).trim() === "") return "-";
    const k = String(raw).trim();
    if (codeToLabel[k]) return codeToLabel[k];
    const norm = normalizeSocIdToKey(k);
    if (codeToLabel[norm]) return codeToLabel[norm];
    const friendly = SOC_DESTINATIONS[norm as keyof typeof SOC_DESTINATIONS];
    if (friendly) return friendly;
    return k;
}
