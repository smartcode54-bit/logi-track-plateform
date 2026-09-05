/**
 * LINE notification message templates (SSOT) — the Thai text pushed into a customer's / partner's
 * LINE group from the Wanpenradchada Official Account when a driver checks in and when a job
 * completes. Mirrors the manual copy-paste pattern in `lib/tripLineShare.ts`
 * (see .vibe-rules "Driver Monitor — แชร์เที่ยวไป LINE").
 *
 * Dependency-free on purpose so it stays a pure, unit-testable module. The server callable
 * (`functions/src/lineNotify.ts`) resolves the raw values from Firestore and calls these builders.
 *
 * ⚠️ Sync rule (billing convention): a byte-identical mirror lives at
 * `functions/src/core/lineMessage.ts` — the callable cannot import from web `lib/`. Change both.
 */

const BANGKOK = "Asia/Bangkok";

/** Coerce a Firestore Timestamp / Date / string / number into a Date (or null). */
export function coerceToDate(val: unknown): Date | null {
    if (val == null) return null;
    if (val instanceof Date) return isNaN(val.getTime()) ? null : val;
    const maybe = val as { toDate?: () => Date };
    if (typeof maybe?.toDate === "function") {
        const d = maybe.toDate();
        return d instanceof Date && !isNaN(d.getTime()) ? d : null;
    }
    if (typeof val === "string" || typeof val === "number") {
        const d = new Date(val);
        return isNaN(d.getTime()) ? null : d;
    }
    return null;
}

/** dd/MM/yy where yy = last 2 digits of the Buddhist-era year (Bangkok calendar day). */
export function formatBuddhistShortDate(input: unknown): string {
    const d = coerceToDate(input) ?? new Date();
    const parts = new Intl.DateTimeFormat("en-CA", {
        timeZone: BANGKOK,
        year: "numeric",
        month: "2-digit",
        day: "2-digit",
    }).formatToParts(d);
    const get = (type: Intl.DateTimeFormatPartTypes) =>
        parts.find((p) => p.type === type)?.value ?? "";
    const y = parseInt(get("year"), 10);
    const beSuffix = ((y + 543) % 100).toString().padStart(2, "0");
    return `${get("day")}/${get("month")}/${beSuffix}`;
}

/** HH:mm in the Bangkok timezone, or "-" when the value is missing/invalid. */
export function formatBangkokHm(input: unknown): string {
    const d = coerceToDate(input);
    if (!d) return "-";
    return new Intl.DateTimeFormat("en-GB", {
        timeZone: BANGKOK,
        hour: "2-digit",
        minute: "2-digit",
        hour12: false,
    }).format(d);
}

function dash(v: string | number | undefined | null): string {
    const s = (v ?? "").toString().trim();
    return s.length > 0 ? s : "-";
}

/**
 * Driver display name honoring the Thai-name business rule:
 * fullNameTh → name → firstName+lastName → email → fallbackId. Mirrors `lib/driverName.ts`.
 * The manual share builder used only the Latin firstName+lastName — this is the corrected order.
 */
export function resolveDriverNameTh(
    d: Record<string, unknown> | null | undefined,
    fallbackId?: string
): string {
    if (!d) return dash(fallbackId);
    const fullNameTh = String((d.fullNameTh as string) ?? "").trim();
    if (fullNameTh) return fullNameTh;
    const name = String((d.name as string) ?? "").trim();
    if (name) return name;
    const combined = [d.firstName, d.lastName].filter(Boolean).join(" ").trim();
    if (combined) return combined;
    const email = String((d.email as string) ?? "").trim();
    if (email) return email;
    return dash(fallbackId);
}

/**
 * The driver code to show to THIS job's customer — the id used in that customer's own system,
 * looked up in `customerDriverIds` by the customer's `code` (e.g. Shopee "SPX" → App ID). Returns
 * "-" when the customer keeps no id for this driver (e.g. J&T, which needs none).
 *
 * NEVER falls back to the national ID card or license: these messages are pushed to an external
 * customer group, so personal identifiers must never leak.
 */
export function resolveDriverCustomerCode(
    d: Record<string, unknown> | null | undefined,
    customerCode: string | null | undefined
): string {
    const code = String(customerCode ?? "").trim();
    if (!d || !code) return "-";
    const nested = d.customerDriverIds as Record<string, Record<string, unknown>> | undefined;
    const forCustomer = nested?.[code];
    if (forCustomer && typeof forCustomer === "object") {
        // Prefer an app-id-like key, else the first non-empty value the customer stored.
        for (const k of ["appId", "appid", "appID", "workId", "driverId"]) {
            const v = String(forCustomer[k] ?? "").trim();
            if (v) return v;
        }
        for (const v of Object.values(forCustomer)) {
            const s = String(v).trim();
            if (s) return s;
        }
    }
    return "-";
}

/** Pre-resolved primitives the message builders format into Thai lines. */
export interface LineTripContext {
    /** dd/MM/yy Buddhist-era (already formatted). */
    dateLine: string;
    originLabel: string;
    destinationLabel: string;
    tripNo: string;
    /** Driver's Thai name (resolved via resolveDriverNameTh). */
    driverNameTh: string;
    driverCode: string;
    plate: string;
    phone: string;
    partner: string;
    truckType: string;
    /** Only known after loading; omit for check-in. */
    parcels?: string;
    checkInHm: string;
    departHm?: string;
    arriveHm?: string;
    doneHm?: string;
    notes?: string;
    /** Delivered only: link + photo count for the "view evidence photos" button (omit to hide it). */
    evidenceUrl?: string;
    photoCount?: number;
}

/** A LINE Flex message object (one push payload item). `contents` is a Flex bubble. */
export interface LineFlexMessage {
    type: "flex";
    altText: string;
    contents: Record<string, unknown>;
}

type FlexComponent = Record<string, unknown>;

const FLEX_COLOR = {
    delivered: "#1DB446",
    checkin: "#2E7BE4",
    standby: "#E8A33D",
    label: "#8C8C8C",
    value: "#111111",
    muted: "#BBBBBB",
    button: "#06C755",
    white: "#FFFFFF",
    separator: "#EEEEEE",
};

function kvRow(label: string, value: string): FlexComponent {
    const isDash = value === "-";
    return {
        type: "box",
        layout: "baseline",
        spacing: "sm",
        contents: [
            { type: "text", text: label, color: FLEX_COLOR.label, size: "sm", flex: 4 },
            { type: "text", text: value, color: isDash ? FLEX_COLOR.muted : FLEX_COLOR.value, size: "sm", flex: 6, wrap: true },
        ],
    };
}

function timeCell(label: string, value: string): FlexComponent {
    const isDash = value === "-";
    return {
        type: "box",
        layout: "vertical",
        flex: 1,
        contents: [
            { type: "text", text: label, color: FLEX_COLOR.label, size: "xs" },
            { type: "text", text: value, color: isDash ? FLEX_COLOR.muted : FLEX_COLOR.value, size: "sm" },
        ],
    };
}

function routeBlock(ctx: LineTripContext): FlexComponent {
    return {
        type: "box",
        layout: "vertical",
        spacing: "xs",
        contents: [
            {
                type: "text",
                text: `${dash(ctx.originLabel)}  →  ${dash(ctx.destinationLabel)}`,
                weight: "bold",
                size: "md",
                color: FLEX_COLOR.value,
                wrap: true,
            },
            { type: "text", text: `เลขทริป ${dash(ctx.tripNo)}`, size: "xs", color: FLEX_COLOR.label },
        ],
    };
}

function flexSeparator(): FlexComponent {
    return { type: "separator", margin: "md", color: FLEX_COLOR.separator };
}

function flexHeader(title: string, dateLine: string, bg: string): FlexComponent {
    return {
        type: "box",
        layout: "horizontal",
        backgroundColor: bg,
        paddingAll: "14px",
        contents: [
            { type: "text", text: title, color: FLEX_COLOR.white, weight: "bold", size: "md", flex: 1, wrap: true, gravity: "center" },
            { type: "text", text: dash(dateLine), color: FLEX_COLOR.white, size: "sm", align: "end", gravity: "center" },
        ],
    };
}

/** Check-in Flex card — the fields known at check-in; no evidence button. */
export function buildCheckinMessage(ctx: LineTripContext): LineFlexMessage {
    return {
        type: "flex",
        altText: `🚚 เช็คอิน • ${dash(ctx.originLabel)} → ${dash(ctx.destinationLabel)} • ${dash(ctx.tripNo)} • ${dash(ctx.driverNameTh)}`.slice(0, 300),
        contents: {
            type: "bubble",
            size: "mega",
            header: flexHeader("🚚 เช็คอินต้นทางแล้ว", ctx.dateLine, FLEX_COLOR.checkin),
            body: {
                type: "box",
                layout: "vertical",
                backgroundColor: FLEX_COLOR.white,
                paddingAll: "16px",
                spacing: "md",
                contents: [
                    routeBlock(ctx),
                    flexSeparator(),
                    {
                        type: "box", layout: "vertical", spacing: "sm", contents: [
                            kvRow("ชื่อ", dash(ctx.driverNameTh)),
                            kvRow("รหัส", dash(ctx.driverCode)),
                            kvRow("ทะเบียนรถ", dash(ctx.plate)),
                            kvRow("เบอร์โทร", dash(ctx.phone)),
                            kvRow("ช่องทาง", dash(ctx.partner)),
                            kvRow("ประเภทรถ", dash(ctx.truckType)),
                            kvRow("เช็คอิน", dash(ctx.checkInHm)),
                        ],
                    },
                ],
            },
        },
    };
}

/** Job-complete Flex card — full detail + optional "view evidence photos" button. */
export function buildDeliveredMessage(ctx: LineTripContext): LineFlexMessage {
    const bubble: Record<string, unknown> = {
        type: "bubble",
        size: "mega",
        header: flexHeader("✅ ส่งงานสำเร็จ", ctx.dateLine, FLEX_COLOR.delivered),
        body: {
            type: "box",
            layout: "vertical",
            backgroundColor: FLEX_COLOR.white,
            paddingAll: "16px",
            spacing: "md",
            contents: [
                routeBlock(ctx),
                flexSeparator(),
                {
                    type: "box", layout: "vertical", spacing: "sm", contents: [
                        kvRow("ชื่อ", dash(ctx.driverNameTh)),
                        kvRow("รหัส", dash(ctx.driverCode)),
                        kvRow("ทะเบียนรถ", dash(ctx.plate)),
                        kvRow("เบอร์โทร", dash(ctx.phone)),
                        kvRow("ช่องทาง", dash(ctx.partner)),
                        kvRow("ประเภทรถ", dash(ctx.truckType)),
                        kvRow("จำนวนสินค้า", dash(ctx.parcels)),
                    ],
                },
                flexSeparator(),
                {
                    type: "box", layout: "horizontal", spacing: "sm", contents: [
                        timeCell("เช็คอิน", dash(ctx.checkInHm)),
                        timeCell("ออกเดินทาง", dash(ctx.departHm)),
                    ],
                },
                {
                    type: "box", layout: "horizontal", spacing: "sm", contents: [
                        timeCell("ถึงปลายทาง", dash(ctx.arriveHm)),
                        timeCell("จบงาน", dash(ctx.doneHm)),
                    ],
                },
                flexSeparator(),
                kvRow("หมายเหตุ", dash(ctx.notes)),
            ],
        },
    };

    if (ctx.evidenceUrl && (ctx.photoCount ?? 0) > 0) {
        bubble.footer = {
            type: "box",
            layout: "vertical",
            backgroundColor: FLEX_COLOR.white,
            paddingAll: "12px",
            contents: [
                {
                    type: "button",
                    style: "primary",
                    color: FLEX_COLOR.button,
                    height: "sm",
                    action: { type: "uri", label: `📷 ดูรูปหลักฐาน (${ctx.photoCount})`, uri: ctx.evidenceUrl },
                },
            ],
        };
    }

    return {
        type: "flex",
        altText: `✅ ส่งงานสำเร็จ • ${dash(ctx.originLabel)} → ${dash(ctx.destinationLabel)} • ${dash(ctx.tripNo)} • ${dash(ctx.driverNameTh)}`.slice(0, 300),
        contents: bubble,
    };
}

/** Thai labels for the incident delay-cause keys (mirror mobile assets/translations/th.json). */
const INCIDENT_CAUSE_TH: Record<string, string> = {
    incident_cause_accident: "อุบัติเหตุเฉี่ยว/ชน",
    incident_cause_engine: "เครื่องยนต์ขัดข้อง/รถเสีย",
    incident_cause_tire: "ยางแตก/ยางระเบิด/ยางรั่ว",
    incident_cause_traffic: "การจราจรติดขัดรุนแรง",
    incident_cause_weather: "สภาพอากาศแปรปรวน/น้ำท่วม",
    incident_cause_checkpoint: "ด่านตรวจ/การตรวจสอบจากเจ้าหน้าที่",
};

/**
 * The delivered-card หมายเหตุ when a trip had ≥1 incident: names the distinct causes in Thai and
 * points to the evidence. Unknown keys pass through; empty input yields a generic delay note.
 */
export function buildDelayNote(causeKeys: Array<string | null | undefined>): string {
    const seen = new Set<string>();
    const labels: string[] = [];
    for (const raw of causeKeys) {
        const key = String(raw ?? "").trim();
        if (!key || seen.has(key)) continue;
        seen.add(key);
        labels.push(INCIDENT_CAUSE_TH[key] ?? key);
    }
    if (labels.length === 0) return "มีเหตุทำให้จัดส่งล่าช้า ตามหลักฐาน โปรดดูรูป";
    return `ล่าช้าจาก${labels.join(", ")} — ดูรูปหลักฐาน`;
}

/** Pre-resolved primitives for the standby ("งานหมด รถ Standby") Flex card. */
export interface LineStandbyContext {
    dateLine: string;
    startLabel: string;
    endLabel: string;
    driverNameTh: string;
    driverCode: string;
    plate: string;
    phone: string;
    partner: string;
    startedHm: string;
    endedHm: string;
    durationText: string;
    notes?: string;
    evidenceUrl?: string;
    photoCount?: number;
}

/** Standby Flex card — mirrors delivered, with an amber header and a เริ่มจอด/สิ้นสุด/ระยะเวลา timeline. */
export function buildStandbyMessage(ctx: LineStandbyContext): LineFlexMessage {
    const bubble: Record<string, unknown> = {
        type: "bubble",
        size: "mega",
        header: flexHeader("🅿️ งานหมด รถ Standby", ctx.dateLine, FLEX_COLOR.standby),
        body: {
            type: "box",
            layout: "vertical",
            backgroundColor: FLEX_COLOR.white,
            paddingAll: "16px",
            spacing: "md",
            contents: [
                {
                    type: "text",
                    text: `${dash(ctx.startLabel)}  →  ${dash(ctx.endLabel)}`,
                    weight: "bold",
                    size: "md",
                    color: FLEX_COLOR.value,
                    wrap: true,
                },
                flexSeparator(),
                {
                    type: "box", layout: "vertical", spacing: "sm", contents: [
                        kvRow("ชื่อ", dash(ctx.driverNameTh)),
                        kvRow("รหัส", dash(ctx.driverCode)),
                        kvRow("ทะเบียนรถ", dash(ctx.plate)),
                        kvRow("เบอร์โทร", dash(ctx.phone)),
                        kvRow("ช่องทาง", dash(ctx.partner)),
                    ],
                },
                flexSeparator(),
                {
                    type: "box", layout: "horizontal", spacing: "sm", contents: [
                        timeCell("เริ่มจอด", dash(ctx.startedHm)),
                        timeCell("สิ้นสุด", dash(ctx.endedHm)),
                        timeCell("ระยะเวลา", dash(ctx.durationText)),
                    ],
                },
                flexSeparator(),
                kvRow("หมายเหตุ", dash(ctx.notes)),
            ],
        },
    };

    if (ctx.evidenceUrl && (ctx.photoCount ?? 0) > 0) {
        bubble.footer = {
            type: "box",
            layout: "vertical",
            backgroundColor: FLEX_COLOR.white,
            paddingAll: "12px",
            contents: [
                {
                    type: "button",
                    style: "primary",
                    color: FLEX_COLOR.button,
                    height: "sm",
                    action: { type: "uri", label: `📷 ดูรูปหลักฐาน (${ctx.photoCount})`, uri: ctx.evidenceUrl },
                },
            ],
        };
    }

    return {
        type: "flex",
        altText: `🅿️ งานหมด รถ Standby • ${dash(ctx.startLabel)} → ${dash(ctx.endLabel)} • ${dash(ctx.driverNameTh)}`.slice(0, 300),
        contents: bubble,
    };
}
