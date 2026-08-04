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

// Type-only: keeps this module importable from the web-root Vitest run, which has no firebase-admin.
import type * as admin from "firebase-admin";

const COL_BILLING_STATEMENTS = "billing_statements";

/** Statuses that mean "a document already went to the customer". */
const LOCKED_STATUSES = ["sent", "paid"] as const;

export interface LockedPeriod {
    customerId: string;
    year: number;
    month: number;
    invoiceNumber: string;
    status: string;
}

/** Map key for a (customer, period) pair. Month is 1-12, matching `billing_statements.period`. */
export function billingPeriodKey(customerId: string, year: number, month: number): string {
    return `${customerId}__${year}-${String(month).padStart(2, "0")}`;
}

/**
 * Bangkok-local (UTC+7) year/month for an instant. Billing periods are calendar months in Thailand,
 * and the Billing Document builds its range with local-time month boundaries, so the lock must use
 * the same calendar or a record could be checked against the wrong month near a boundary.
 */
export function bangkokYearMonth(ms: number): { year: number; month: number } {
    const bkk = new Date(ms + 7 * 60 * 60 * 1000);
    return { year: bkk.getUTCFullYear(), month: bkk.getUTCMonth() + 1 };
}

export class BillingPeriodLocks {
    private readonly byKey: Map<string, LockedPeriod>;

    constructor(locked: LockedPeriod[]) {
        this.byKey = new Map(locked.map((l) => [billingPeriodKey(l.customerId, l.year, l.month), l]));
    }

    /** The blocking invoice for this record, or null when the period is still open. */
    lockFor(customerId: string | undefined | null, billDateMs: number): LockedPeriod | null {
        const cid = (customerId ?? "").trim();
        if (!cid || !billDateMs) return null;
        const { year, month } = bangkokYearMonth(billDateMs);
        return this.byKey.get(billingPeriodKey(cid, year, month)) ?? null;
    }

    get size(): number {
        return this.byKey.size;
    }
}

/**
 * Load every issued statement once per callable invocation. Volume is one doc per customer per
 * month, so a full read is cheaper than per-record queries inside the recompute loop.
 */
export async function loadBillingPeriodLocks(
    db: admin.firestore.Firestore
): Promise<BillingPeriodLocks> {
    const snap = await db
        .collection(COL_BILLING_STATEMENTS)
        .where("status", "in", LOCKED_STATUSES as unknown as string[])
        .get();

    const locked: LockedPeriod[] = [];
    snap.forEach((doc) => {
        const d = doc.data() as Record<string, unknown>;
        const customerId = String(d.customerId ?? "").trim();
        const period = d.period as { month?: unknown; year?: unknown } | undefined;
        const year = Number(period?.year);
        const month = Number(period?.month);
        if (!customerId || !Number.isFinite(year) || !Number.isFinite(month)) return;
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
