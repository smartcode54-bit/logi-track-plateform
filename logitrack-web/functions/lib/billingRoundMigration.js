"use strict";
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
var __createBinding = (this && this.__createBinding) || (Object.create ? (function(o, m, k, k2) {
    if (k2 === undefined) k2 = k;
    var desc = Object.getOwnPropertyDescriptor(m, k);
    if (!desc || ("get" in desc ? !m.__esModule : desc.writable || desc.configurable)) {
      desc = { enumerable: true, get: function() { return m[k]; } };
    }
    Object.defineProperty(o, k2, desc);
}) : (function(o, m, k, k2) {
    if (k2 === undefined) k2 = k;
    o[k2] = m[k];
}));
var __setModuleDefault = (this && this.__setModuleDefault) || (Object.create ? (function(o, v) {
    Object.defineProperty(o, "default", { enumerable: true, value: v });
}) : function(o, v) {
    o["default"] = v;
});
var __importStar = (this && this.__importStar) || (function () {
    var ownKeys = function(o) {
        ownKeys = Object.getOwnPropertyNames || function (o) {
            var ar = [];
            for (var k in o) if (Object.prototype.hasOwnProperty.call(o, k)) ar[ar.length] = k;
            return ar;
        };
        return ownKeys(o);
    };
    return function (mod) {
        if (mod && mod.__esModule) return mod;
        var result = {};
        if (mod != null) for (var k = ownKeys(mod), i = 0; i < k.length; i++) if (k[i] !== "default") __createBinding(result, mod, k[i]);
        __setModuleDefault(result, mod);
        return result;
    };
})();
Object.defineProperty(exports, "__esModule", { value: true });
exports.normalizeAnnouncementEffectiveFrom = exports.billingImpactReport = void 0;
const admin = __importStar(require("firebase-admin"));
const firebase_functions_1 = require("firebase-functions");
const https_1 = require("firebase-functions/v2/https");
const billingCompute_1 = require("./core/billingCompute");
const COL_RATE_ENTRIES = "customer_rate_entries";
const COL_FUEL_ADJ = "customer_fuel_rate_adjustments";
const COL_TRIP_RECORDS = "trip_records";
/** 07:00 ICT — where the old `Date.UTC(y, m, d)` helper actually placed a "date". */
const LEGACY_OFFSET_MS = 7 * 60 * 60 * 1000;
function bangkokMidnightMsForSameDay(ms) {
    const dateStr = (0, billingCompute_1.bangkokDateStrFromMillis)(ms);
    return new Date(`${dateStr}T00:00:00+07:00`).getTime();
}
exports.billingImpactReport = (0, https_1.onCall)({
    region: "asia-southeast1",
    enforceAppCheck: false, // Admin-only, protected by the auth check below
    timeoutSeconds: 540,
}, async (request) => {
    if (request.auth?.token?.admin !== true) {
        throw new https_1.HttpsError("permission-denied", "Admin only");
    }
    const fromDateStr = request.data?.fromDateStr?.trim() ?? "";
    const toDateStr = request.data?.toDateStr?.trim() ?? "";
    if (!fromDateStr || !toDateStr) {
        throw new https_1.HttpsError("invalid-argument", "fromDateStr and toDateStr are required (yyyy-MM-dd)");
    }
    const start = new Date(`${fromDateStr}T00:00:00+07:00`);
    const end = new Date(`${toDateStr}T23:59:59.999+07:00`);
    if (Number.isNaN(start.getTime()) || Number.isNaN(end.getTime()) || start > end) {
        throw new https_1.HttpsError("invalid-argument", "Invalid fromDateStr / toDateStr");
    }
    const maxScan = Math.min(Math.max(1, request.data?.maxScan ?? 1000), 5000);
    // NOTE: this function holds no WriteBatch and calls no set/update/delete. That is the
    // guarantee, not a comment — the report must never be able to change what it reports on.
    const db = admin.firestore();
    // ── Defect 1: bands computed with Math.floor on a price ending in .00 ──
    const adjSnap = await db.collection(COL_FUEL_ADJ).get();
    const bandImpacts = [];
    const switchDays = new Set();
    adjSnap.docs.forEach((d) => {
        const data = d.data();
        if (data.voided === true)
            return;
        const effMs = data.effectiveFrom?.toMillis?.() ?? 0;
        if (!effMs)
            return;
        switchDays.add((0, billingCompute_1.bangkokDateStrFromMillis)(effMs));
        const price = data.referenceFuelPriceThbPerLitre;
        if (data.fuelBandEnabled !== true || typeof price !== "number")
            return;
        const baseline = Number(data.fuelBandBaselineFuelFloor ?? 41);
        const perBaht = Number(data.fuelBandThbPerBaht ?? 10);
        const stored = Number(data.addThbPerTrip ?? 0);
        const corrected = Math.round(((0, billingCompute_1.fuelBandFloor)(price) - baseline) * perBaht * 100) / 100;
        if (corrected === stored)
            return;
        bandImpacts.push({
            adjustmentId: d.id,
            customerId: String(data.customerId ?? ""),
            effectiveFromDateStr: typeof data.effectiveFromDateStr === "string"
                ? data.effectiveFromDateStr
                : (0, billingCompute_1.bangkokDateStrFromMillis)(effMs),
            referenceFuelPriceThb: price,
            storedAddThbPerTrip: stored,
            correctedAddThbPerTrip: corrected,
            deltaThbPerTrip: Math.round((corrected - stored) * 100) / 100,
        });
    });
    const rateSnap = await db.collection(COL_RATE_ENTRIES).get();
    rateSnap.docs.forEach((d) => {
        const data = d.data();
        if (data.voided === true)
            return;
        const effMs = data.effectiveFrom?.toMillis?.() ?? 0;
        if (effMs)
            switchDays.add((0, billingCompute_1.bangkokDateStrFromMillis)(effMs));
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
    const boundaryImpacts = [];
    tripSnap.docs.forEach((d) => {
        const data = d.data();
        const ms = data.deliveredTimestamp?.toMillis?.() ?? 0;
        if (!ms)
            return;
        const dayStr = (0, billingCompute_1.bangkokDateStrFromMillis)(ms);
        if (!switchDays.has(dayStr))
            return;
        // Bangkok-local hour of the delivery.
        const bkkHour = new Date(ms + LEGACY_OFFSET_MS).getUTCHours();
        if (bkkHour >= 7)
            return; // outside the window the 07:00 boundary created
        boundaryImpacts.push({
            tripId: d.id,
            customerId: String(data.billingCustomerId ?? ""),
            deliveredAtIso: new Date(ms).toISOString(),
            pricedUnderDateStr: typeof data.billingRoundEffectiveFromDateStr === "string"
                ? data.billingRoundEffectiveFromDateStr
                : "(unknown — priced before ADR 0009)",
            shouldBeDateStr: dayStr,
        });
    });
    const periods = new Set();
    bandImpacts.forEach((b) => periods.add(`${b.customerId}:${b.effectiveFromDateStr.slice(0, 7)}`));
    boundaryImpacts.forEach((b) => periods.add(`${b.customerId}:${b.deliveredAtIso.slice(0, 7)}`));
    firebase_functions_1.logger.info("[billingImpactReport] done", {
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
});
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
exports.normalizeAnnouncementEffectiveFrom = (0, https_1.onCall)({
    region: "asia-southeast1",
    enforceAppCheck: false, // Admin-only, protected by the auth check below
    timeoutSeconds: 540,
}, async (request) => {
    if (request.auth?.token?.admin !== true) {
        throw new https_1.HttpsError("permission-denied", "Admin only");
    }
    const dryRun = request.data?.dryRun !== false;
    if (!dryRun && request.data?.confirm !== true) {
        throw new https_1.HttpsError("failed-precondition", "Pass confirm: true to write");
    }
    const db = admin.firestore();
    let scanned = 0;
    let updated = 0;
    let alreadyNormalized = 0;
    const samples = [];
    for (const col of [COL_RATE_ENTRIES, COL_FUEL_ADJ]) {
        const snap = await db.collection(col).get();
        let batch = db.batch();
        let pending = 0;
        for (const d of snap.docs) {
            scanned += 1;
            const data = d.data();
            const ms = data.effectiveFrom?.toMillis?.() ?? 0;
            if (!ms)
                continue;
            const targetMs = bangkokMidnightMsForSameDay(ms);
            const dateStr = (0, billingCompute_1.bangkokDateStrFromMillis)(ms);
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
        if (!dryRun && pending > 0)
            await batch.commit();
    }
    firebase_functions_1.logger.info("[normalizeAnnouncementEffectiveFrom] done", { dryRun, scanned, updated });
    return { ok: true, dryRun, scanned, updated, alreadyNormalized, samples };
});
//# sourceMappingURL=billingRoundMigration.js.map