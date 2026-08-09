/**
 * ADR 0009 — reporting and repair for rate rounds.
 *
 * Two defects were corrected going forward; neither is repaired retroactively, by explicit owner
 * decision (2026-08-04). This file provides:
 *
 *  - `billingImpactReport` — READ-ONLY. Names the customers and periods whose already-invoiced
 *    amounts were affected, so the business can decide about credit notes with numbers in hand.
 *  - `normalizeAnnouncementEffectiveFrom` — the one-off data repair that moves stored
 *    `effectiveFrom` from 07:00 ICT to Bangkok midnight of the same calendar date and backfills
 *    `effectiveFromDateStr`. It rewrites announcements only; it never touches a trip amount.
 */

import * as admin from "firebase-admin";
import { logger } from "firebase-functions";
import { onCall, HttpsError } from "firebase-functions/v2/https";
import { fuelBandFloor, bangkokDateStrFromMillis } from "./core/billingCompute";

const COL_RATE_ENTRIES = "customer_rate_entries";
const COL_FUEL_ADJ = "customer_fuel_rate_adjustments";
const COL_TRIP_RECORDS = "trip_records";

/** 07:00 ICT — where the old `Date.UTC(y, m, d)` helper actually placed a "date". */
const LEGACY_OFFSET_MS = 7 * 60 * 60 * 1000;

function bangkokMidnightMsForSameDay(ms: number): number {
    const dateStr = bangkokDateStrFromMillis(ms);
    return new Date(`${dateStr}T00:00:00+07:00`).getTime();
}

// ─── Read-only impact report (ADR 0009, spec R14) ────────────────────────────

interface ImpactReportRequest {
    fromDateStr?: string;
    toDateStr?: string;
    maxScan?: number;
}

interface BandImpactRow {
    adjustmentId: string;
    customerId: string;
    effectiveFromDateStr: string;
    referenceFuelPriceThb: number;
    storedAddThbPerTrip: number;
    correctedAddThbPerTrip: number;
    deltaThbPerTrip: number;
}

interface BoundaryImpactRow {
    tripId: string;
    customerId: string;
    deliveredAtIso: string;
    /** The round the trip was priced under vs the one Bangkok midnight would have selected. */
    pricedUnderDateStr: string;
    shouldBeDateStr: string;
}

interface ImpactReportResponse {
    ok: true;
    scannedAnnouncements: number;
    scannedTrips: number;
    /** Rounds whose reference price sat exactly on x.00 and were charged one band too high. */
    bandImpacts: BandImpactRow[];
    /** Trips delivered in the 00:00–06:59 ICT window of a switch day. */
    boundaryImpacts: BoundaryImpactRow[];
    periodsAffected: string[];
}

export const billingImpactReport = onCall<ImpactReportRequest, Promise<ImpactReportResponse>>(
    {
        region: "asia-southeast1",
        enforceAppCheck: false, // Admin-only, protected by the auth check below
        timeoutSeconds: 540,
    },
    async (request): Promise<ImpactReportResponse> => {
        if (request.auth?.token?.admin !== true) {
            throw new HttpsError("permission-denied", "Admin only");
        }
        const fromDateStr = request.data?.fromDateStr?.trim() ?? "";
        const toDateStr = request.data?.toDateStr?.trim() ?? "";
        if (!fromDateStr || !toDateStr) {
            throw new HttpsError("invalid-argument", "fromDateStr and toDateStr are required (yyyy-MM-dd)");
        }
        const start = new Date(`${fromDateStr}T00:00:00+07:00`);
        const end = new Date(`${toDateStr}T23:59:59.999+07:00`);
        if (Number.isNaN(start.getTime()) || Number.isNaN(end.getTime()) || start > end) {
            throw new HttpsError("invalid-argument", "Invalid fromDateStr / toDateStr");
        }
        const maxScan = Math.min(Math.max(1, request.data?.maxScan ?? 1000), 5000);

        // NOTE: this function holds no WriteBatch and calls no set/update/delete. That is the
        // guarantee, not a comment — the report must never be able to change what it reports on.
        const db = admin.firestore();

        // ── Defect 1: bands computed with Math.floor on a price ending in .00 ──
        const adjSnap = await db.collection(COL_FUEL_ADJ).get();
        const bandImpacts: BandImpactRow[] = [];
        const switchDays = new Set<string>();
        adjSnap.docs.forEach((d) => {
            const data = d.data();
            if (data.voided === true) return;
            const effMs = data.effectiveFrom?.toMillis?.() ?? 0;
            if (!effMs) return;
            switchDays.add(bangkokDateStrFromMillis(effMs));

            const price = data.referenceFuelPriceThbPerLitre;
            if (data.fuelBandEnabled !== true || typeof price !== "number") return;
            const baseline = Number(data.fuelBandBaselineFuelFloor ?? 41);
            const perBaht = Number(data.fuelBandThbPerBaht ?? 10);
            const stored = Number(data.addThbPerTrip ?? 0);
            const corrected = Math.round((fuelBandFloor(price) - baseline) * perBaht * 100) / 100;
            if (corrected === stored) return;
            bandImpacts.push({
                adjustmentId: d.id,
                customerId: String(data.customerId ?? ""),
                effectiveFromDateStr:
                    typeof data.effectiveFromDateStr === "string"
                        ? data.effectiveFromDateStr
                        : bangkokDateStrFromMillis(effMs),
                referenceFuelPriceThb: price,
                storedAddThbPerTrip: stored,
                correctedAddThbPerTrip: corrected,
                deltaThbPerTrip: Math.round((corrected - stored) * 100) / 100,
            });
        });

        const rateSnap = await db.collection(COL_RATE_ENTRIES).get();
        rateSnap.docs.forEach((d) => {
            const data = d.data();
            if (data.voided === true) return;
            const effMs = data.effectiveFrom?.toMillis?.() ?? 0;
            if (effMs) switchDays.add(bangkokDateStrFromMillis(effMs));
        });

        // ── Defect 2: trips delivered in the 7-hour window a switch day used to lose ──
        // A stored boundary sat at 07:00 ICT, so anything delivered 00:00–06:59 that day was
        // priced under the PREVIOUS round.
        const tripSnap = await db
            .collection(COL_TRIP_RECORDS)
            .where("status", "==", "delivered")
            .where("deliveredTimestamp", ">=", admin.firestore.Timestamp.fromDate(start))
            .where("deliveredTimestamp", "<=", admin.firestore.Timestamp.fromDate(end))
            .limit(maxScan)
            .get();

        const boundaryImpacts: BoundaryImpactRow[] = [];
        tripSnap.docs.forEach((d) => {
            const data = d.data();
            const ms = data.deliveredTimestamp?.toMillis?.() ?? 0;
            if (!ms) return;
            const dayStr = bangkokDateStrFromMillis(ms);
            if (!switchDays.has(dayStr)) return;
            // Bangkok-local hour of the delivery.
            const bkkHour = new Date(ms + LEGACY_OFFSET_MS).getUTCHours();
            if (bkkHour >= 7) return; // outside the window the 07:00 boundary created
            boundaryImpacts.push({
                tripId: d.id,
                customerId: String(data.billingCustomerId ?? ""),
                deliveredAtIso: new Date(ms).toISOString(),
                pricedUnderDateStr:
                    typeof data.billingRoundEffectiveFromDateStr === "string"
                        ? data.billingRoundEffectiveFromDateStr
                        : "(unknown — priced before ADR 0009)",
                shouldBeDateStr: dayStr,
            });
        });

        const periods = new Set<string>();
        bandImpacts.forEach((b) => periods.add(`${b.customerId}:${b.effectiveFromDateStr.slice(0, 7)}`));
        boundaryImpacts.forEach((b) => periods.add(`${b.customerId}:${b.deliveredAtIso.slice(0, 7)}`));

        logger.info("[billingImpactReport] done", {
            bandImpacts: bandImpacts.length,
            boundaryImpacts: boundaryImpacts.length,
        });

        return {
            ok: true,
            scannedAnnouncements: adjSnap.size + rateSnap.size,
            scannedTrips: tripSnap.size,
            bandImpacts,
            boundaryImpacts,
            periodsAffected: Array.from(periods).sort(),
        };
    }
);

// ─── One-off repair: effectiveFrom → Bangkok midnight (spec R2 / T10) ────────

interface NormalizeRequest {
    /** Must be sent explicitly — this rewrites announcement rows. */
    confirm?: boolean;
    dryRun?: boolean;
}

interface NormalizeResponse {
    ok: true;
    dryRun: boolean;
    scanned: number;
    updated: number;
    alreadyNormalized: number;
    samples: { id: string; collection: string; fromIso: string; toIso: string; dateStr: string }[];
}

/**
 * Move every stored `effectiveFrom` to Bangkok midnight of the day it already denotes, and write
 * `effectiveFromDateStr`.
 *
 * Runs through the Admin SDK, so the immutability rules in `firestore.rules` do not apply — which
 * is the point: this is the migration that makes those rules' data consistent, and it is the only
 * sanctioned writer of `effectiveFrom` after the fact.
 *
 * It does NOT recompute any trip. Amounts already frozen on `trip_records` are untouched; what
 * changes is which round a *future* recompute would select. Run `billingImpactReport` first, and
 * do not run this in prod before the ADR 0008 period lock is deployed.
 *
 * `dryRun` defaults to true so an accidental invocation reports instead of writing.
 */
export const normalizeAnnouncementEffectiveFrom = onCall<NormalizeRequest, Promise<NormalizeResponse>>(
    {
        region: "asia-southeast1",
        enforceAppCheck: false, // Admin-only, protected by the auth check below
        timeoutSeconds: 540,
    },
    async (request): Promise<NormalizeResponse> => {
        if (request.auth?.token?.admin !== true) {
            throw new HttpsError("permission-denied", "Admin only");
        }
        const dryRun = request.data?.dryRun !== false;
        if (!dryRun && request.data?.confirm !== true) {
            throw new HttpsError("failed-precondition", "Pass confirm: true to write");
        }

        const db = admin.firestore();
        let scanned = 0;
        let updated = 0;
        let alreadyNormalized = 0;
        const samples: NormalizeResponse["samples"] = [];

        for (const col of [COL_RATE_ENTRIES, COL_FUEL_ADJ]) {
            const snap = await db.collection(col).get();
            let batch = db.batch();
            let pending = 0;

            for (const d of snap.docs) {
                scanned += 1;
                const data = d.data();
                const ms: number = data.effectiveFrom?.toMillis?.() ?? 0;
                if (!ms) continue;

                const targetMs = bangkokMidnightMsForSameDay(ms);
                const dateStr = bangkokDateStrFromMillis(ms);
                const alreadyAtMidnight = targetMs === ms;
                const hasDateStr = typeof data.effectiveFromDateStr === "string";
                if (alreadyAtMidnight && hasDateStr) {
                    alreadyNormalized += 1;
                    continue;
                }

                if (samples.length < 20) {
                    samples.push({
                        id: d.id,
                        collection: col,
                        fromIso: new Date(ms).toISOString(),
                        toIso: new Date(targetMs).toISOString(),
                        dateStr,
                    });
                }
                updated += 1;

                if (!dryRun) {
                    batch.update(d.ref, {
                        effectiveFrom: admin.firestore.Timestamp.fromMillis(targetMs),
                        effectiveFromDateStr: dateStr,
                    });
                    pending += 1;
                    if (pending >= 400) {
                        await batch.commit();
                        batch = db.batch();
                        pending = 0;
                    }
                }
            }
            if (!dryRun && pending > 0) await batch.commit();
        }

        logger.info("[normalizeAnnouncementEffectiveFrom] done", { dryRun, scanned, updated });
        return { ok: true, dryRun, scanned, updated, alreadyNormalized, samples };
    }
);
