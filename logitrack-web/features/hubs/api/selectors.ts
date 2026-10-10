/**
 * Per-page shapes of the one `['hubs']` list (developer-spec.md §10.6; Appendix E §E.4: the four
 * legacy shapes of `taskService.ts:19-37`, `useDriverMonitor.ts:506-521`, `billing.ts:747-769` and
 * `income/page.tsx:297-326` become `select` functions over one DTO). Each selector reproduces what
 * its page built from the documents before, field for field, so moving a page onto the cache changes
 * where the data comes from, not what it shows.
 */
import {
    billingHubLabelFromFirestoreData,
    primaryHubLabelFromFirestoreData,
    type HubDisplayEntry,
} from "@/lib/hubDisplay";
import type { HubDTO } from "./hubs";

/**
 * The hub as the stored-document record the legacy mappers read: the Firestore document in P0
 * (`legacyDoc`), else the DTO under the stored field names (Go source, P1).
 */
export function hubRecord(hub: HubDTO): Record<string, unknown> {
    if (hub.legacyDoc) return hub.legacyDoc as Record<string, unknown>;
    const record: Record<string, unknown> = { source_id: hub.sourceId, createdByDriver: hub.createdByDriver };
    if (hub.sourceNameTh !== null) record.source_name_th = hub.sourceNameTh;
    if (hub.sourceNameEn !== null) record.source_name_en = hub.sourceNameEn;
    if (hub.latitude !== null) record.latitude = hub.latitude;
    if (hub.longitude !== null) record.longitude = hub.longitude;
    if (hub.stationType !== null) record.station_type = hub.stationType;
    if (hub.linkedCustomerId !== null) record.linkedCustomerId = hub.linkedCustomerId;
    if (hub.linkedCustomerName !== null) record.linkedCustomerName = hub.linkedCustomerName;
    if (hub.linkedCustomerKind !== null) record.customerLinkKind = hub.linkedCustomerKind;
    return record;
}

/** Primary display label: Thai, English, linked customer name, code (lib/hubDisplay.ts). */
export function selectHubLabel(hub: HubDTO): string {
    return primaryHubLabelFromFirestoreData(hubRecord(hub));
}

/** Billing label: English, Thai, code (invoices, lib/hubDisplay.ts). */
export function selectBillingHubLabel(hub: HubDTO): string {
    return billingHubLabelFromFirestoreData(hubRecord(hub));
}

/** `sourceId` -> hub (first one wins on a duplicate code). */
export function selectHubBySourceId(hubs: readonly HubDTO[]): Map<string, HubDTO> {
    const map = new Map<string, HubDTO>();
    for (const hub of hubs) if (hub.sourceId && !map.has(hub.sourceId)) map.set(hub.sourceId, hub);
    return map;
}

/**
 * The row of the task boards, task dialogs and import dialogs (was `taskService.fetchHubs` and the
 * inline reads of first-mile, line-haul and job-assign).
 */
export interface HubRow {
    "Hub Code": string | undefined;
    "Hub Name": string | undefined;
    "Hub Name Th": string | undefined;
    station_type: string;
    linkedCustomerId: string;
    customerLinkKind: string;
    /** J&T hubs keep their real name on the linked customer: a display fallback (ADR 0019 follow-up). */
    linkedCustomerName: string | undefined;
    lat: number | undefined;
    lng: number | undefined;
    source: "custom";
    id: string;
    [key: string]: unknown;
}

export function selectHubRows(hubs: readonly HubDTO[]): HubRow[] {
    return hubs.map((hub) => {
        const data = hubRecord(hub);
        return {
            "Hub Code": (data.source_id ?? data.hubId ?? data.hubCode) as string | undefined,
            "Hub Name": (data.source_name_en ?? data.hubName) as string | undefined,
            "Hub Name Th":
                ((data.source_name_th ?? data.hubTHName ?? data.hub_th_name ?? data.station_name_th ?? "") as string) ||
                undefined,
            station_type: (data.station_type ?? "") as string,
            linkedCustomerId: (data.linkedCustomerId ?? "") as string,
            customerLinkKind: (data.customerLinkKind ?? "") as string,
            linkedCustomerName: data.linkedCustomerName as string | undefined,
            lat: (data.latitude ?? data.lat) as number | undefined,
            lng: (data.longitude ?? data.lng) as number | undefined,
            source: "custom",
            id: hub.id,
        };
    });
}

/** The driver monitor's entry (was `useDriverMonitor.ts:505-521`). */
export interface HubDisplayEntryWithLink extends HubDisplayEntry {
    station_type: "SOC" | "HUB";
    linkedCustomerId?: string;
    customerLinkKind?: string;
}

export function selectHubDisplayEntries(hubs: readonly HubDTO[]): HubDisplayEntryWithLink[] {
    return hubs.map((hub) => {
        const data = hubRecord(hub);
        return {
            source_id: String(data.source_id ?? data.hubId ?? data.hubCode ?? ""),
            source_name_en: String(data.source_name_en ?? data.hubName ?? "") || undefined,
            source_name_th: String(data.source_name_th ?? data.hubTHName ?? data.hub_th_name ?? "").trim() || undefined,
            station_type: data.station_type === "SOC" ? "SOC" : "HUB",
            linkedCustomerId: data.linkedCustomerId as string | undefined,
            linkedCustomerName: data.linkedCustomerName as string | undefined,
            customerLinkKind: data.customerLinkKind as string | undefined,
        };
    });
}

/** The rate-card picker option: upper-cased code and its billing-side name (was `rate-card/page.tsx:420-427`). */
export interface HubOption {
    id: string;
    name?: string;
}

export function selectHubOptions(hubs: readonly HubDTO[]): HubOption[] {
    return hubs
        .map((hub) => {
            const data = hubRecord(hub);
            const id = String(data.hubId ?? data.source_id ?? "").trim().toUpperCase();
            const name = String(data.source_name_en ?? data.source_name_th ?? data.hubName ?? "").trim();
            return { id, name: name || undefined };
        })
        .filter((x) => x.id);
}
