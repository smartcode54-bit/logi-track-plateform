"use strict";
/**
 * Billing period lock — ADR 0008 §5.
 *
 * A recompute may rewrite a price only while the period is still a draft. Once a
 * `billing_statements` doc for (customerId, {month, year}) reaches "sent" or "paid", that document
 * is in the customer's hands and the system must not contradict it: the recompute reports the row as
 * blocked, with the invoice number, and leaves the stored price alone.
 *
 * Deliberately NOT in `core/billingCompute.ts` — that file is mirrored verbatim into
 * `lib/billingCompute.ts` for the client, and this reads Firestore (server only).
 *
 * Scope note: `billing_statements` stores totals, not line items, so the lock is *period* level, not
 * row level. A row added to a month after that month was invoiced is indistinguishable from one that
 * was on the invoice. `billedOnInvoiceNumber` per row is the ADR follow-up that makes this exact.
 */
Object.defineProperty(exports, "__esModule", { value: true });
exports.BillingPeriodLocks = void 0;
exports.billingPeriodKey = billingPeriodKey;
exports.bangkokYearMonth = bangkokYearMonth;
exports.loadBillingPeriodLocks = loadBillingPeriodLocks;
const COL_BILLING_STATEMENTS = "billing_statements";
/** Statuses that mean "a document already went to the customer". */
const LOCKED_STATUSES = ["sent", "paid"];
/** Map key for a (customer, period) pair. Month is 1-12, matching `billing_statements.period`. */
function billingPeriodKey(customerId, year, month) {
    return `${customerId}__${year}-${String(month).padStart(2, "0")}`;
}
/**
 * Bangkok-local (UTC+7) year/month for an instant. Billing periods are calendar months in Thailand,
 * and the Billing Document builds its range with local-time month boundaries, so the lock must use
 * the same calendar or a record could be checked against the wrong month near a boundary.
 */
function bangkokYearMonth(ms) {
    const bkk = new Date(ms + 7 * 60 * 60 * 1000);
    return { year: bkk.getUTCFullYear(), month: bkk.getUTCMonth() + 1 };
}
class BillingPeriodLocks {
    constructor(locked) {
        this.byKey = new Map(locked.map((l) => [billingPeriodKey(l.customerId, l.year, l.month), l]));
    }
    /** The blocking invoice for this record, or null when the period is still open. */
    lockFor(customerId, billDateMs) {
        const cid = (customerId ?? "").trim();
        if (!cid || !billDateMs)
            return null;
        const { year, month } = bangkokYearMonth(billDateMs);
        return this.byKey.get(billingPeriodKey(cid, year, month)) ?? null;
    }
    get size() {
        return this.byKey.size;
    }
}
exports.BillingPeriodLocks = BillingPeriodLocks;
/**
 * Load every issued statement once per callable invocation. Volume is one doc per customer per
 * month, so a full read is cheaper than per-record queries inside the recompute loop.
 */
async function loadBillingPeriodLocks(db) {
    const snap = await db
        .collection(COL_BILLING_STATEMENTS)
        .where("status", "in", LOCKED_STATUSES)
        .get();
    const locked = [];
    snap.forEach((doc) => {
        const d = doc.data();
        const customerId = String(d.customerId ?? "").trim();
        const period = d.period;
        const year = Number(period?.year);
        const month = Number(period?.month);
        if (!customerId || !Number.isFinite(year) || !Number.isFinite(month))
            return;
        locked.push({
            customerId,
            year,
            month,
            invoiceNumber: String(d.invoiceNumber ?? doc.id),
            status: String(d.status ?? ""),
        });
    });
    return new BillingPeriodLocks(locked);
}
//# sourceMappingURL=billingPeriodLock.js.map