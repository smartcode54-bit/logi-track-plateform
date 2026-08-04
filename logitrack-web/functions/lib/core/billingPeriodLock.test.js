"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
const vitest_1 = require("vitest");
const billingPeriodLock_1 = require("./billingPeriodLock");
/**
 * ADR 0008 §5 — a recompute may rewrite a price only while the period is still a draft. These tests
 * pin the two things that decide "which period is this row in", because getting either wrong either
 * silently rewrites an issued invoice or refuses a legitimate correction.
 */
const lock = (over = {}) => ({
    customerId: "cust_a",
    year: 2026,
    month: 7,
    invoiceNumber: "TTP-202607-001",
    status: "sent",
    ...over,
});
(0, vitest_1.describe)("billingPeriodKey", () => {
    (0, vitest_1.it)("zero-pads the month so month 7 and month 70 can never collide", () => {
        (0, vitest_1.expect)((0, billingPeriodLock_1.billingPeriodKey)("c", 2026, 7)).toBe("c__2026-07");
        (0, vitest_1.expect)((0, billingPeriodLock_1.billingPeriodKey)("c", 2026, 12)).toBe("c__2026-12");
    });
    (0, vitest_1.it)("keys are per customer — the same month for two customers is two periods", () => {
        (0, vitest_1.expect)((0, billingPeriodLock_1.billingPeriodKey)("a", 2026, 7)).not.toBe((0, billingPeriodLock_1.billingPeriodKey)("b", 2026, 7));
    });
});
(0, vitest_1.describe)("bangkokYearMonth", () => {
    (0, vitest_1.it)("uses the Bangkok calendar, not UTC", () => {
        // 2026-08-01T00:30 in Bangkok is still 2026-07-31T17:30 UTC. Billing periods are Thai
        // calendar months, so this instant belongs to August.
        const ms = Date.UTC(2026, 6, 31, 17, 30);
        (0, vitest_1.expect)((0, billingPeriodLock_1.bangkokYearMonth)(ms)).toEqual({ year: 2026, month: 8 });
    });
    (0, vitest_1.it)("keeps the last UTC-evening minutes of a month in the NEXT month", () => {
        // 2026-07-31T17:00Z === 2026-08-01T00:00+07:00 — the first instant of August in Bangkok.
        (0, vitest_1.expect)((0, billingPeriodLock_1.bangkokYearMonth)(Date.UTC(2026, 6, 31, 17, 0))).toEqual({ year: 2026, month: 8 });
        // One minute earlier is still July.
        (0, vitest_1.expect)((0, billingPeriodLock_1.bangkokYearMonth)(Date.UTC(2026, 6, 31, 16, 59))).toEqual({ year: 2026, month: 7 });
    });
    (0, vitest_1.it)("rolls the year over at the December/January boundary", () => {
        (0, vitest_1.expect)((0, billingPeriodLock_1.bangkokYearMonth)(Date.UTC(2026, 11, 31, 17, 0))).toEqual({ year: 2027, month: 1 });
    });
});
(0, vitest_1.describe)("BillingPeriodLocks.lockFor", () => {
    const locks = new billingPeriodLock_1.BillingPeriodLocks([
        lock(),
        lock({ customerId: "cust_b", month: 6, invoiceNumber: "CJ-202606-002", status: "paid" }),
    ]);
    (0, vitest_1.it)("blocks a row whose customer and period match an issued invoice", () => {
        const july = Date.UTC(2026, 6, 15, 3, 0); // 2026-07-15 10:00 Bangkok
        (0, vitest_1.expect)(locks.lockFor("cust_a", july)?.invoiceNumber).toBe("TTP-202607-001");
    });
    (0, vitest_1.it)("does not block a different customer in the same month", () => {
        (0, vitest_1.expect)(locks.lockFor("cust_b", Date.UTC(2026, 6, 15, 3, 0))).toBeNull();
    });
    (0, vitest_1.it)("does not block the same customer in a different month", () => {
        (0, vitest_1.expect)(locks.lockFor("cust_a", Date.UTC(2026, 7, 15, 3, 0))).toBeNull();
    });
    (0, vitest_1.it)("treats paid the same as sent — both are documents the customer already has", () => {
        (0, vitest_1.expect)(locks.lockFor("cust_b", Date.UTC(2026, 5, 15, 3, 0))?.status).toBe("paid");
    });
    (0, vitest_1.it)("never blocks when the customer or the date is unknown, so a fixable row stays fixable", () => {
        (0, vitest_1.expect)(locks.lockFor("", Date.UTC(2026, 6, 15, 3, 0))).toBeNull();
        (0, vitest_1.expect)(locks.lockFor(undefined, Date.UTC(2026, 6, 15, 3, 0))).toBeNull();
        (0, vitest_1.expect)(locks.lockFor("cust_a", 0)).toBeNull();
    });
    (0, vitest_1.it)("trims the customer id, since denormalized ids arrive with stray whitespace", () => {
        (0, vitest_1.expect)(locks.lockFor("  cust_a  ", Date.UTC(2026, 6, 15, 3, 0))?.invoiceNumber).toBe("TTP-202607-001");
    });
    (0, vitest_1.it)("blocks a row that lands in July only after the Bangkok shift", () => {
        // 2026-06-30T18:00Z === 2026-07-01T01:00+07:00 → July, so the July invoice must block it.
        (0, vitest_1.expect)(locks.lockFor("cust_a", Date.UTC(2026, 5, 30, 18, 0))?.invoiceNumber).toBe("TTP-202607-001");
    });
});
//# sourceMappingURL=billingPeriodLock.test.js.map