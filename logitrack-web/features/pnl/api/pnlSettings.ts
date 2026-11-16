"use client";

/**
 * P&L settings — append-only (ADR 0031, spec §4.2/§4.8). Rows are created or voided, never edited
 * or deleted; Firestore rules enforce the same (isVoidOnlyUpdate, delete: false).
 */
import { addDoc, collection, doc, getDocs, serverTimestamp, setDoc, updateDoc } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { stripUndefined } from "@/lib/firestoreWrite";
import {
    pnlFixedCostInputSchema,
    pnlRateInputSchema,
    pnlVoidInputSchema,
    type PnlFixedCostInput,
    type PnlFixedCostVersion,
    type PnlRateInput,
    type PnlRateRow,
} from "@/validate/pnlSettingsSchema";

export interface PnlActor {
    uid: string;
    name?: string;
}

function toDate(v: unknown): Date | null {
    if (!v) return null;
    if (v instanceof Date) return v;
    const maybe = v as { toDate?: () => Date };
    return typeof maybe.toDate === "function" ? maybe.toDate() : null;
}

function auditFields(d: Record<string, unknown>) {
    return {
        createdAt: toDate(d.createdAt),
        createdBy: d.createdBy as string | undefined,
        createdByName: d.createdByName as string | undefined,
        voided: d.voided === true,
        voidedAt: toDate(d.voidedAt),
        voidedBy: d.voidedBy as string | undefined,
        voidedReason: d.voidedReason as string | undefined,
    };
}

export async function listPnlRates(): Promise<PnlRateRow[]> {
    const snap = await getDocs(collection(db, COLLECTIONS.PNL_RATES));
    return snap.docs.map((s) => {
        const d = s.data();
        return {
            id: s.id,
            kind: d.kind,
            amountThb: Number(d.amountThb) || 0,
            effectiveFrom: String(d.effectiveFrom ?? ""),
            note: d.note as string | undefined,
            ...auditFields(d),
        } as PnlRateRow;
    });
}

export async function listPnlFixedCosts(): Promise<PnlFixedCostVersion[]> {
    const snap = await getDocs(collection(db, COLLECTIONS.PNL_FIXED_COSTS));
    return snap.docs.map((s) => {
        const d = s.data();
        return {
            id: s.id,
            itemId: String(d.itemId ?? s.id),
            name: String(d.name ?? ""),
            category: d.category,
            categoryLabel: d.categoryLabel as string | undefined,
            scope: d.scope,
            truckId: d.truckId as string | undefined,
            truckPlate: d.truckPlate as string | undefined,
            frequency: d.frequency,
            amountThb: Number(d.amountThb) || 0,
            effectiveFromMonth: String(d.effectiveFromMonth ?? ""),
            effectiveToMonth: d.effectiveToMonth as string | undefined,
            isStop: d.isStop === true,
            note: d.note as string | undefined,
            ...auditFields(d),
        } as PnlFixedCostVersion;
    });
}

const created = (actor: PnlActor) => ({
    voided: false,
    createdAt: serverTimestamp(),
    createdBy: actor.uid,
    createdByName: actor.name,
});

export async function addPnlRate(input: PnlRateInput, actor: PnlActor): Promise<string> {
    const parsed = pnlRateInputSchema.parse(input);
    const ref = await addDoc(
        collection(db, COLLECTIONS.PNL_RATES),
        stripUndefined({ ...parsed, note: parsed.note?.trim() || undefined, ...created(actor) }),
    );
    return ref.id;
}

/**
 * Append a fixed-cost version. Omit `itemId` to start a new item (the new doc id becomes the item
 * id); pass it to change the amount / stop the item from `effectiveFromMonth`.
 */
export async function addPnlFixedCostVersion(input: PnlFixedCostInput, actor: PnlActor, itemId?: string): Promise<string> {
    const parsed = pnlFixedCostInputSchema.parse(input);
    const ref = doc(collection(db, COLLECTIONS.PNL_FIXED_COSTS));
    await setDoc(
        ref,
        stripUndefined({
            ...parsed,
            categoryLabel: parsed.category === "custom" ? parsed.categoryLabel : undefined,
            truckId: parsed.scope === "truck" ? parsed.truckId : undefined,
            truckPlate: parsed.scope === "truck" ? parsed.truckPlate : undefined,
            isStop: parsed.isStop ? true : undefined,
            note: parsed.note?.trim() || undefined,
            itemId: itemId ?? ref.id,
            ...created(actor),
        }),
    );
    return ref.id;
}

/** Stop an item from `fromMonth`: a tombstone version carrying the item's last fields. */
export async function stopPnlFixedCostItem(latest: PnlFixedCostVersion, fromMonth: string, actor: PnlActor, note?: string): Promise<string> {
    return addPnlFixedCostVersion(
        {
            name: latest.name,
            category: latest.category,
            categoryLabel: latest.categoryLabel,
            scope: latest.scope,
            truckId: latest.truckId,
            truckPlate: latest.truckPlate,
            frequency: latest.frequency,
            amountThb: 0,
            effectiveFromMonth: fromMonth,
            isStop: true,
            note,
        },
        actor,
        latest.itemId,
    );
}

async function voidRow(collectionName: string, id: string, reason: string, actor: PnlActor): Promise<void> {
    const { reason: r } = pnlVoidInputSchema.parse({ reason });
    await updateDoc(doc(db, collectionName, id), {
        voided: true,
        voidedAt: serverTimestamp(),
        voidedBy: actor.uid,
        voidedReason: r,
        updatedAt: serverTimestamp(),
    });
}

export const voidPnlRate = (id: string, reason: string, actor: PnlActor) => voidRow(COLLECTIONS.PNL_RATES, id, reason, actor);
export const voidPnlFixedCostVersion = (id: string, reason: string, actor: PnlActor) =>
    voidRow(COLLECTIONS.PNL_FIXED_COSTS, id, reason, actor);
