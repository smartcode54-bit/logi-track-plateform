import { describe, it, expect } from "vitest";
import {
    BillingPeriodLocks,
    billingPeriodKey,
    bangkokYearMonth,
    type LockedPeriod,
} from "./billingPeriodLock";

/**
 * ADR 0008 §5 — a recompute may rewrite a price only while the period is still a draft. These tests
 * pin the two things that decide "which period is this row in", because getting either wrong either
 * silently rewrites an issued invoice or refuses a legitimate correction.
 */

const lock = (over: Partial<LockedPeriod> = {}): LockedPeriod => ({
    customerId: "cust_a",
    year: 2026,
    month: 7,
    invoiceNumber: "TTP-202607-001",
    status: "sent",
    ...over,
});

describe("billingPeriodKey", () => {
    it("zero-pads the month so month 7 and month 70 can never collide", () => {
        expect(billingPeriodKey("c", 2026, 7)).toBe("c__2026-07");
        expect(billingPeriodKey("c", 2026, 12)).toBe("c__2026-12");
    });

    it("keys are per customer — the same month for two customers is two periods", () => {
        expect(billingPeriodKey("a", 2026, 7)).not.toBe(billingPeriodKey("b", 2026, 7));
    });
});

describe("bangkokYearMonth", () => {
    it("uses the Bangkok calendar, not UTC", () => {
        // 2026-08-01T00:30 in Bangkok is still 2026-07-31T17:30 UTC. Billing periods are Thai
        // calendar months, so this instant belongs to August.
        const ms = Date.UTC(2026, 6, 31, 17, 30);
        expect(bangkokYearMonth(ms)).toEqual({ year: 2026, month: 8 });
    });

    it("keeps the last UTC-evening minutes of a month in the NEXT month", () => {
        // 2026-07-31T17:00Z === 2026-08-01T00:00+07:00 — the first instant of August in Bangkok.
        expect(bangkokYearMonth(Date.UTC(2026, 6, 31, 17, 0))).toEqual({ year: 2026, month: 8 });
        // One minute earlier is still July.
        expect(bangkokYearMonth(Date.UTC(2026, 6, 31, 16, 59))).toEqual({ year: 2026, month: 7 });
    });

    it("rolls the year over at the December/January boundary", () => {
        expect(bangkokYearMonth(Date.UTC(2026, 11, 31, 17, 0))).toEqual({ year: 2027, month: 1 });
    });
});

describe("BillingPeriodLocks.lockFor", () => {
    const locks = new BillingPeriodLocks([
        lock(),
        lock({ customerId: "cust_b", month: 6, invoiceNumber: "CJ-202606-002", status: "paid" }),
    ]);

    it("blocks a row whose customer and period match an issued invoice", () => {
        const july = Date.UTC(2026, 6, 15, 3, 0); // 2026-07-15 10:00 Bangkok
        expect(locks.lockFor("cust_a", july)?.invoiceNumber).toBe("TTP-202607-001");
    });

    it("does not block a different customer in the same month", () => {
        expect(locks.lockFor("cust_b", Date.UTC(2026, 6, 15, 3, 0))).toBeNull();
    });

    it("does not block the same customer in a different month", () => {
        expect(locks.lockFor("cust_a", Date.UTC(2026, 7, 15, 3, 0))).toBeNull();
    });

    it("treats paid the same as sent — both are documents the customer already has", () => {
        expect(locks.lockFor("cust_b", Date.UTC(2026, 5, 15, 3, 0))?.status).toBe("paid");
    });

    it("never blocks when the customer or the date is unknown, so a fixable row stays fixable", () => {
        expect(locks.lockFor("", Date.UTC(2026, 6, 15, 3, 0))).toBeNull();
        expect(locks.lockFor(undefined, Date.UTC(2026, 6, 15, 3, 0))).toBeNull();
        expect(locks.lockFor("cust_a", 0)).toBeNull();
    });

    it("trims the customer id, since denormalized ids arrive with stray whitespace", () => {
        expect(locks.lockFor("  cust_a  ", Date.UTC(2026, 6, 15, 3, 0))?.invoiceNumber).toBe(
            "TTP-202607-001"
        );
    });

    it("blocks a row that lands in July only after the Bangkok shift", () => {
        // 2026-06-30T18:00Z === 2026-07-01T01:00+07:00 → July, so the July invoice must block it.
        expect(locks.lockFor("cust_a", Date.UTC(2026, 5, 30, 18, 0))?.invoiceNumber).toBe(
            "TTP-202607-001"
        );
    });
});
