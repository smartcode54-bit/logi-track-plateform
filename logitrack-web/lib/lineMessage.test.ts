import { describe, it, expect } from "vitest";
import {
    buildCheckinMessage,
    buildDeliveredMessage,
    coerceToDate,
    formatBangkokHm,
    formatBuddhistShortDate,
    resolveDriverNameTh,
    resolveDriverCustomerCode,
    buildStandbyMessage,
    buildDelayNote,
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
    it("returns a flex card with the check-in header and Thai name, no evidence button", () => {
        const msg = buildCheckinMessage(baseCtx);
        expect(msg.type).toBe("flex");
        expect(msg.altText).toContain("เช็คอิน");
        const json = JSON.stringify(msg.contents);
        expect(json).toContain("🚚 เช็คอินต้นทางแล้ว");
        expect(json).toContain("สมชาย ใจดี");
        expect(json).not.toContain("ดูรูปหลักฐาน");
        expect(json).not.toContain("จบงาน");
    });
});

describe("buildDeliveredMessage", () => {
    it("returns a flex card with full detail and all timestamps", () => {
        const msg = buildDeliveredMessage({
            ...baseCtx,
            parcels: "42",
            departHm: "09:00",
            arriveHm: "12:15",
            doneHm: "12:40",
            notes: "ส่งเรียบร้อย",
        });
        expect(msg.type).toBe("flex");
        expect(msg.altText).toContain("ส่งงานสำเร็จ");
        const json = JSON.stringify(msg.contents);
        expect(json).toContain("✅ ส่งงานสำเร็จ");
        expect(json).toContain("42");
        expect(json).toContain("12:40");
        expect(json).toContain("ส่งเรียบร้อย");
    });

    it("adds the evidence button only when a url and photo count are given", () => {
        const withPhotos = buildDeliveredMessage({ ...baseCtx, evidenceUrl: "https://x/e/tok", photoCount: 8 });
        const withJson = JSON.stringify(withPhotos);
        expect(withJson).toContain("ดูรูปหลักฐาน (8)");
        expect(withJson).toContain("https://x/e/tok");
        expect(JSON.stringify(buildDeliveredMessage(baseCtx))).not.toContain("ดูรูปหลักฐาน");
    });
});

describe("buildDelayNote", () => {
    it("names distinct causes in Thai and points to the evidence", () => {
        const note = buildDelayNote(["incident_cause_traffic", "incident_cause_traffic", "incident_cause_tire"]);
        expect(note).toContain("การจราจรติดขัดรุนแรง");
        expect(note).toContain("ยางแตก");
        expect(note).toContain("ดูรูปหลักฐาน");
        // deduped — the traffic cause appears once, not twice
        expect(note.match(/การจราจรติดขัดรุนแรง/g)?.length).toBe(1);
    });
    it("falls back to a generic note for empty / unknown input", () => {
        expect(buildDelayNote([])).toContain("มีเหตุทำให้จัดส่งล่าช้า");
        expect(buildDelayNote([null, "  "])).toContain("มีเหตุทำให้จัดส่งล่าช้า");
    });
});

describe("buildStandbyMessage", () => {
    const stdCtx = {
        dateLine: "05/09/69",
        startLabel: "ลาดพร้าว08",
        endLabel: "บัวโรย",
        driverNameTh: "สมชาย ใจดี",
        driverCode: "-",
        plate: "1กก-1234",
        phone: "0812345678",
        partner: "SPX",
        startedHm: "10:00",
        endedHm: "12:30",
        durationText: "150 นาที",
        notes: "งานหมด",
    };

    it("renders the standby header + timeline, no evidence button by default", () => {
        const msg = buildStandbyMessage(stdCtx);
        expect(msg.type).toBe("flex");
        expect(msg.altText).toContain("งานหมด รถ Standby");
        const json = JSON.stringify(msg.contents);
        expect(json).toContain("🅿️ งานหมด รถ Standby");
        expect(json).toContain("เริ่มจอด");
        expect(json).toContain("ระยะเวลา");
        expect(json).toContain("150 นาที");
        expect(json).not.toContain("ดูรูปหลักฐาน");
    });

    it("adds the evidence button when a url + count are given", () => {
        const msg = buildStandbyMessage({ ...stdCtx, evidenceUrl: "https://x/e/tok", photoCount: 2 });
        expect(JSON.stringify(msg)).toContain("ดูรูปหลักฐาน (2)");
        expect(JSON.stringify(msg)).toContain("https://x/e/tok");
    });
});
