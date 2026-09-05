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
}

/** Short "checked in at origin" report — fields known at check-in (no depart/arrive/done yet). */
export function buildCheckinMessage(ctx: LineTripContext): string {
    return [
        "🚚 เช็คอินต้นทางแล้ว",
        `วันที่ ${dash(ctx.dateLine)}`,
        `ต้นทาง : ${dash(ctx.originLabel)}`,
        `ปลายทาง : ${dash(ctx.destinationLabel)}`,
        `เลขทริป : ${dash(ctx.tripNo)}`,
        `ชื่อ : ${dash(ctx.driverNameTh)}`,
        `รหัส : ${dash(ctx.driverCode)}`,
        `ทะเบียนรถ : ${dash(ctx.plate)}`,
        `เบอร์โทร : ${dash(ctx.phone)}`,
        `ช่องทาง : ${dash(ctx.partner)}`,
        `ประเภทรถ : ${dash(ctx.truckType)}`,
        `เช็คอิน : ${dash(ctx.checkInHm)}`,
    ].join("\n");
}

/** Full job-complete report — the existing 16-line pattern with a status header. */
export function buildDeliveredMessage(ctx: LineTripContext): string {
    return [
        "✅ ส่งงานสำเร็จ",
        `วันที่ ${dash(ctx.dateLine)}`,
        `ต้นทาง : ${dash(ctx.originLabel)}`,
        `ปลายทาง : ${dash(ctx.destinationLabel)}`,
        `เลขทริป : ${dash(ctx.tripNo)}`,
        `ชื่อ : ${dash(ctx.driverNameTh)}`,
        `รหัส : ${dash(ctx.driverCode)}`,
        `ทะเบียนรถ : ${dash(ctx.plate)}`,
        `เบอร์โทร : ${dash(ctx.phone)}`,
        `ช่องทาง : ${dash(ctx.partner)}`,
        `ประเภทรถ : ${dash(ctx.truckType)}`,
        `จำนวนสินค้า : ${dash(ctx.parcels)}`,
        `เช็คอิน : ${dash(ctx.checkInHm)}`,
        `ออกเดินทาง : ${dash(ctx.departHm)}`,
        `ถึงปลายทาง : ${dash(ctx.arriveHm)}`,
        `จบงาน : ${dash(ctx.doneHm)}`,
        `หมายเหตุ : ${dash(ctx.notes)}`,
    ].join("\n");
}
