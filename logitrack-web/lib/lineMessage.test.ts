import { describe, it, expect } from "vitest";
import {
    buildCheckinMessage,
    buildDeliveredMessage,
    coerceToDate,
    formatBangkokHm,
    formatBuddhistShortDate,
    resolveDriverNameTh,
    resolveDriverCustomerCode,
    type LineTripContext,
} from "./lineMessage";

// A Firestore Timestamp-like object (has toDate()).
const ts = (iso: string) => ({ toDate: () => new Date(iso) });

const baseCtx: LineTripContext = {
    dateLine: "05/09/69",
    originLabel: "ประเวศ18",
    destinationLabel: "SOCE (บัวโรย)",
    tripNo: "TRIP-123",
    driverNameTh: "สมชาย ใจดี",
    driverCode: "12345",
    plate: "1กก-1234",
    phone: "0812345678",
    partner: "SPX",
    truckType: "6 Wheels",
    checkInHm: "08:30",
};

describe("date/time helpers (Bangkok)", () => {
    it("formatBuddhistShortDate → dd/MM/yy with Buddhist-era year, from a Timestamp-like", () => {
        // 2026-09-05 UTC is still 2026-09-05 in Bangkok (UTC+7). BE year 2569 → "69".
        expect(formatBuddhistShortDate(ts("2026-09-05T04:00:00Z"))).toBe("05/09/69");
    });

    it("formatBangkokHm → HH:mm in Asia/Bangkok, '-' when missing/invalid", () => {
        expect(formatBangkokHm(ts("2026-09-05T01:30:00Z"))).toBe("08:30"); // +7h
        expect(formatBangkokHm(null)).toBe("-");
        expect(formatBangkokHm(undefined)).toBe("-");
        expect(formatBangkokHm("not a date")).toBe("-");
    });

    it("coerceToDate handles Date, Timestamp-like, string, number, and nullish", () => {
        expect(coerceToDate(new Date("2026-01-01"))!.getTime()).toBe(new Date("2026-01-01").getTime());
        expect(coerceToDate(ts("2026-01-01T00:00:00Z"))!.getTime()).toBe(new Date("2026-01-01T00:00:00Z").getTime());
        expect(coerceToDate(null)).toBeNull();
        expect(coerceToDate("")).toBeNull();
    });
});

describe("resolveDriverNameTh", () => {
    it("prefers fullNameTh (the Thai-name rule the manual builder violated)", () => {
        expect(resolveDriverNameTh({ fullNameTh: "สมชาย ใจดี", firstName: "Somchai", lastName: "Jaidee" }))
            .toBe("สมชาย ใจดี");
    });

    it("falls back name → firstName+lastName → email → id", () => {
        expect(resolveDriverNameTh({ name: "Legacy" })).toBe("Legacy");
        expect(resolveDriverNameTh({ firstName: "Anan", lastName: "Kumar" })).toBe("Anan Kumar");
        expect(resolveDriverNameTh({ email: "a@b.com" })).toBe("a@b.com");
        expect(resolveDriverNameTh({}, "uid_9")).toBe("uid_9");
        expect(resolveDriverNameTh(null, "uid_9")).toBe("uid_9");
        expect(resolveDriverNameTh(null)).toBe("-");
    });
});

describe("resolveDriverCustomerCode", () => {
    it("returns the customer's own driver id (e.g. Shopee App ID) by the customer code", () => {
        const d = { customerDriverIds: { SPX: { appId: "889900" } } };
        expect(resolveDriverCustomerCode(d, "SPX")).toBe("889900");
    });
    it("returns '-' when that customer keeps no id for this driver (e.g. J&T)", () => {
        expect(resolveDriverCustomerCode({ customerDriverIds: { SPX: { appId: "1" } } }, "JNT")).toBe("-");
        expect(resolveDriverCustomerCode({}, "SPX")).toBe("-");
        expect(resolveDriverCustomerCode(null, "SPX")).toBe("-");
        expect(resolveDriverCustomerCode({ customerDriverIds: { SPX: { appId: "1" } } }, "")).toBe("-");
    });
    it("never leaks the national ID card or license (PII), even when present", () => {
        const d = { idCard: "1234567890123", truckLicenseId: "AB123456", customerDriverIds: {} };
        expect(resolveDriverCustomerCode(d, "SPX")).toBe("-");
    });
});

describe("buildCheckinMessage", () => {
    it("renders the short header + fields known at check-in, no depart/arrive/done lines", () => {
        const msg = buildCheckinMessage(baseCtx);
        expect(msg.startsWith("🚚 เช็คอินต้นทางแล้ว")).toBe(true);
        expect(msg).toContain("ชื่อ : สมชาย ใจดี");
        expect(msg).toContain("เช็คอิน : 08:30");
        expect(msg).not.toContain("ออกเดินทาง");
        expect(msg).not.toContain("จบงาน");
    });
});

describe("buildDeliveredMessage", () => {
    it("renders the full pattern with the Thai driver name and all timestamps", () => {
        const msg = buildDeliveredMessage({
            ...baseCtx,
            parcels: "42",
            departHm: "09:00",
            arriveHm: "12:15",
            doneHm: "12:40",
            notes: "ส่งเรียบร้อย",
        });
        expect(msg.startsWith("✅ ส่งงานสำเร็จ")).toBe(true);
        expect(msg).toContain("จำนวนสินค้า : 42");
        expect(msg).toContain("ออกเดินทาง : 09:00");
        expect(msg).toContain("ถึงปลายทาง : 12:15");
        expect(msg).toContain("จบงาน : 12:40");
        expect(msg).toContain("หมายเหตุ : ส่งเรียบร้อย");
    });

    it("replaces missing optional fields with '-'", () => {
        const msg = buildDeliveredMessage(baseCtx);
        expect(msg).toContain("จำนวนสินค้า : -");
        expect(msg).toContain("ออกเดินทาง : -");
        expect(msg).toContain("จบงาน : -");
        expect(msg).toContain("หมายเหตุ : -");
    });
});
