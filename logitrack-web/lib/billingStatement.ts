/**
 * Billing Statement helpers
 *
 * Manages the billing_statements collection (invoice registry) and
 * billing_counters collection (sequential invoice number generator).
 *
 * Invoice number format: {CUSTOMER_CODE}-{YYYYMM}-{SEQ:03d}
 * e.g.  TTP-202606-001
 */

import {
    collection,
    doc,
    runTransaction,
    addDoc,
    getDocs,
    updateDoc,
    query,
    where,
    orderBy,
    serverTimestamp,
    Timestamp,
    type DocumentData,
} from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";

// ─── Types ────────────────────────────────────────────────────────────────────

export type BillingStatementStatus = "draft" | "sent" | "paid" | "cancelled";

export interface BillingStatement {
    id?: string;
    /** e.g. "TTP-202606-001" */
    invoiceNumber: string;
    customerId: string;
    /** Denormalized for display without joins */
    customerName: string;
    customerCode: string;
    period: { month: number; year: number };
    totalAmount: number;
    withholdingTax: number;
    netAmount: number;
    /** Total row count (trips + standby + multi-drop stops) */
    tripCount: number;
    // ── Breakdown by record type (0 when not applicable / legacy docs) ──────
    tripOnlyCount: number;
    tripSubtotal: number;
    standbyCount: number;
    standbySubtotal: number;
    multiDropCount: number;
    multiDropSubtotal: number;
    status: BillingStatementStatus;
    generatedAt: Date | Timestamp;
    sentAt?: Date | Timestamp;
    paidAt?: Date | Timestamp;
    /** generatedAt + paymentTermsDays (ms) — undefined when paymentTermsDays not set */
    dueDate?: Date | Timestamp;
    note?: string;
    /** UID of admin who generated the statement */
    generatedBy?: string;
}

// ─── Counter / sequential invoice number ──────────────────────────────────────

/**
 * Atomically increment the per-customer-per-period sequence counter
 * and return the formatted invoice number.
 *
 * Counter doc ID: `{customerId}_{YYYYMM}`
 */
export async function generateNextInvoiceNumber(
    customerId: string,
    customerCode: string,
    period: { month: number; year: number }
): Promise<string> {
    const mm = String(period.month).padStart(2, "0");
    const periodKey = `${period.year}${mm}`;
    const counterId = `${customerId}_${periodKey}`;
    const counterRef = doc(db, COLLECTIONS.BILLING_COUNTERS, counterId);

    let nextSeq = 1;
    await runTransaction(db, async (tx) => {
        const snap = await tx.get(counterRef);
        if (snap.exists()) {
            nextSeq = (snap.data().lastSeq as number) + 1;
        }
        tx.set(counterRef, {
            lastSeq: nextSeq,
            customerCode,
            updatedAt: serverTimestamp(),
        });
    });

    const seq = String(nextSeq).padStart(3, "0");
    return `${customerCode}-${periodKey}-${seq}`;
}

// ─── Save ──────────────────────────────────────────────────────────────────────

export interface SaveBillingStatementInput {
    customerId: string;
    customerName: string;
    customerCode: string;
    period: { month: number; year: number };
    totalAmount: number;
    withholdingTax: number;
    netAmount: number;
    /** Total rows (trips + standby + multi-drop stops) */
    tripCount: number;
    // Breakdown
    tripOnlyCount: number;
    tripSubtotal: number;
    standbyCount: number;
    standbySubtotal: number;
    multiDropCount: number;
    multiDropSubtotal: number;
    paymentTermsDays?: number;
    note?: string;
    generatedBy?: string;
}

/**
 * Generate the next invoice number and write a new billing statement doc.
 * Returns the generated invoiceNumber so the caller can display it.
 */
export async function saveBillingStatement(
    input: SaveBillingStatementInput
): Promise<string> {
    const invoiceNumber = await generateNextInvoiceNumber(
        input.customerId,
        input.customerCode,
        input.period
    );

    const now = Timestamp.now();
    const dueDate =
        input.paymentTermsDays != null
            ? Timestamp.fromMillis(now.toMillis() + input.paymentTermsDays * 24 * 60 * 60 * 1000)
            : undefined;

    const payload: Omit<BillingStatement, "id"> = {
        invoiceNumber,
        customerId: input.customerId,
        customerName: input.customerName,
        customerCode: input.customerCode,
        period: input.period,
        totalAmount: input.totalAmount,
        withholdingTax: input.withholdingTax,
        netAmount: input.netAmount,
        tripCount: input.tripCount,
        tripOnlyCount: input.tripOnlyCount,
        tripSubtotal: input.tripSubtotal,
        standbyCount: input.standbyCount,
        standbySubtotal: input.standbySubtotal,
        multiDropCount: input.multiDropCount,
        multiDropSubtotal: input.multiDropSubtotal,
        status: "draft",
        generatedAt: now,
        ...(dueDate ? { dueDate } : {}),
        ...(input.note ? { note: input.note } : {}),
        ...(input.generatedBy ? { generatedBy: input.generatedBy } : {}),
    };

    await addDoc(collection(db, COLLECTIONS.BILLING_STATEMENTS), payload);
    return invoiceNumber;
}

// ─── Fetch ─────────────────────────────────────────────────────────────────────

export interface GetBillingStatementsFilter {
    customerId?: string;
    status?: BillingStatementStatus | "all";
    /** If provided, filter by period.year + period.month */
    period?: { month: number; year: number };
}

function toDate(val: unknown): Date | undefined {
    if (!val) return undefined;
    if (val instanceof Date) return val;
    if (typeof (val as { toDate?: () => Date }).toDate === "function") {
        return (val as { toDate: () => Date }).toDate();
    }
    return undefined;
}

function docToStatement(id: string, data: DocumentData): BillingStatement {
    return {
        id,
        invoiceNumber: data.invoiceNumber ?? "",
        customerId: data.customerId ?? "",
        customerName: data.customerName ?? "",
        customerCode: data.customerCode ?? "",
        period: data.period ?? { month: 0, year: 0 },
        totalAmount: data.totalAmount ?? 0,
        withholdingTax: data.withholdingTax ?? 0,
        netAmount: data.netAmount ?? 0,
        tripCount: data.tripCount ?? 0,
        // Breakdown — default 0 for legacy docs that predate this field
        tripOnlyCount: data.tripOnlyCount ?? 0,
        tripSubtotal: data.tripSubtotal ?? 0,
        standbyCount: data.standbyCount ?? 0,
        standbySubtotal: data.standbySubtotal ?? 0,
        multiDropCount: data.multiDropCount ?? 0,
        multiDropSubtotal: data.multiDropSubtotal ?? 0,
        status: data.status ?? "draft",
        generatedAt: toDate(data.generatedAt) ?? new Date(0),
        sentAt: toDate(data.sentAt),
        paidAt: toDate(data.paidAt),
        dueDate: toDate(data.dueDate),
        note: data.note,
        generatedBy: data.generatedBy,
    };
}

export async function getBillingStatements(
    filter: GetBillingStatementsFilter = {}
): Promise<BillingStatement[]> {
    const constraints = [];

    if (filter.customerId) {
        constraints.push(where("customerId", "==", filter.customerId));
    }
    if (filter.status && filter.status !== "all") {
        constraints.push(where("status", "==", filter.status));
    }
    if (filter.period) {
        // Firestore can't filter on nested fields directly with where() for map fields
        // We filter by year + month in post-processing if needed.
        // But we can filter on customerId + status + then post-filter period.
    }

    constraints.push(orderBy("generatedAt", "desc"));

    const snap = await getDocs(
        query(collection(db, COLLECTIONS.BILLING_STATEMENTS), ...constraints)
    );

    let results = snap.docs.map((d) => docToStatement(d.id, d.data()));

    // Post-filter by period (Firestore doesn't index nested map equality well)
    if (filter.period) {
        results = results.filter(
            (s) =>
                s.period.year === filter.period!.year &&
                s.period.month === filter.period!.month
        );
    }

    return results;
}

// ─── Update status ─────────────────────────────────────────────────────────────

export async function updateBillingStatementStatus(
    id: string,
    status: BillingStatementStatus
): Promise<void> {
    const ref = doc(db, COLLECTIONS.BILLING_STATEMENTS, id);
    const update: Record<string, unknown> = { status };

    if (status === "sent") update.sentAt = serverTimestamp();
    if (status === "paid") update.paidAt = serverTimestamp();

    await updateDoc(ref, update);
}
