"use strict";
/**
 * LINE notification message templates (SSOT mirror) — the Thai text pushed into a customer's /
 * partner's LINE group from the Wanpenradchada Official Account at check-in and job-complete.
 * Mirrors the manual copy-paste pattern in web `lib/tripLineShare.ts`.
 *
 * Dependency-free so it stays pure and unit-testable. The callable (`lineNotify.ts`) resolves the
 * raw values from Firestore and calls these builders.
 *
 * ⚠️ Sync rule (billing convention): a byte-identical copy lives at web `lib/lineMessage.ts`, which
 * carries the Vitest tests (functions cannot import from web `lib/`). Change both together.
 */
Object.defineProperty(exports, "__esModule", { value: true });
exports.coerceToDate = coerceToDate;
exports.formatBuddhistShortDate = formatBuddhistShortDate;
exports.formatBangkokHm = formatBangkokHm;
exports.resolveDriverNameTh = resolveDriverNameTh;
exports.resolveDriverCustomerCode = resolveDriverCustomerCode;
exports.buildCheckinMessage = buildCheckinMessage;
exports.buildDeliveredMessage = buildDeliveredMessage;
const BANGKOK = "Asia/Bangkok";
/** Coerce a Firestore Timestamp / Date / string / number into a Date (or null). */
function coerceToDate(val) {
    if (val == null)
        return null;
    if (val instanceof Date)
        return isNaN(val.getTime()) ? null : val;
    const maybe = val;
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
function formatBuddhistShortDate(input) {
    const d = coerceToDate(input) ?? new Date();
    const parts = new Intl.DateTimeFormat("en-CA", {
        timeZone: BANGKOK,
        year: "numeric",
        month: "2-digit",
        day: "2-digit",
    }).formatToParts(d);
    const get = (type) => parts.find((p) => p.type === type)?.value ?? "";
    const y = parseInt(get("year"), 10);
    const beSuffix = ((y + 543) % 100).toString().padStart(2, "0");
    return `${get("day")}/${get("month")}/${beSuffix}`;
}
/** HH:mm in the Bangkok timezone, or "-" when the value is missing/invalid. */
function formatBangkokHm(input) {
    const d = coerceToDate(input);
    if (!d)
        return "-";
    return new Intl.DateTimeFormat("en-GB", {
        timeZone: BANGKOK,
        hour: "2-digit",
        minute: "2-digit",
        hour12: false,
    }).format(d);
}
function dash(v) {
    const s = (v ?? "").toString().trim();
    return s.length > 0 ? s : "-";
}
/**
 * Driver display name honoring the Thai-name business rule:
 * fullNameTh → name → firstName+lastName → email → fallbackId. Mirrors web `lib/driverName.ts`.
 */
function resolveDriverNameTh(d, fallbackId) {
    if (!d)
        return dash(fallbackId);
    const fullNameTh = String(d.fullNameTh ?? "").trim();
    if (fullNameTh)
        return fullNameTh;
    const name = String(d.name ?? "").trim();
    if (name)
        return name;
    const combined = [d.firstName, d.lastName].filter(Boolean).join(" ").trim();
    if (combined)
        return combined;
    const email = String(d.email ?? "").trim();
    if (email)
        return email;
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
function resolveDriverCustomerCode(d, customerCode) {
    const code = String(customerCode ?? "").trim();
    if (!d || !code)
        return "-";
    const nested = d.customerDriverIds;
    const forCustomer = nested?.[code];
    if (forCustomer && typeof forCustomer === "object") {
        // Prefer an app-id-like key, else the first non-empty value the customer stored.
        for (const k of ["appId", "appid", "appID", "workId", "driverId"]) {
            const v = String(forCustomer[k] ?? "").trim();
            if (v)
                return v;
        }
        for (const v of Object.values(forCustomer)) {
            const s = String(v).trim();
            if (s)
                return s;
        }
    }
    return "-";
}
/** Short "checked in at origin" report — fields known at check-in (no depart/arrive/done yet). */
function buildCheckinMessage(ctx) {
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
function buildDeliveredMessage(ctx) {
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
//# sourceMappingURL=lineMessage.js.map