"use strict";
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
exports.approveDriverPayout = exports.generateDriverPayoutRun = void 0;
/**
 * Driver compensation — payout run orchestrator (Cloud Function).
 *
 * generateDriverPayoutRun(period, round) batches over drivers, fetches their
 * delivered trips for the round, runs the PURE compute engine
 * (./core/compensationCompute, unit-tested), and writes draft `payroll` docs
 * (reusing the existing payroll collection + payrollSchema, extended with
 * payPeriod/round). Idempotent: overwrites only DRAFT payouts.
 *
 * ⚠️ Scope of this story (E2 S2.5): base pay + trip-volume incentive + SSO are
 * fully wired. Fuel incentive and penalties are STUBBED (amount 0) pending:
 *   - fuel km/L attribution logic (OQ2) — TODO: source per-driver monthly km/L
 *   - driver_penalties collection (Epic 3 / Story 3.1) — TODO: load + schedule
 * All monetary math is delegated to the tested pure engine.
 */
const admin = __importStar(require("firebase-admin"));
const https_1 = require("firebase-functions/v2/https");
const firebase_functions_1 = require("firebase-functions");
const compensationCompute_1 = require("./core/compensationCompute");
const db = () => admin.firestore();
/** Bangkok local midnight (start of `day`) as a UTC instant. */
function bkkMidnight(year, month1, day) {
    return new Date(Date.UTC(year, month1 - 1, day, 0, 0, 0) - 7 * 3600000);
}
function roundRange(period, round) {
    const [y, m] = period.split("-").map(Number);
    const monthStart = bkkMidnight(y, m, 1);
    const monthEnd = bkkMidnight(m === 12 ? y + 1 : y, m === 12 ? 1 : m + 1, 1);
    const r2Start = bkkMidnight(y, m, 16);
    return {
        start: round === "R1" ? monthStart : r2Start,
        end: round === "R1" ? r2Start : monthEnd,
        monthStart,
        monthEnd,
    };
}
function tsToDate(v) {
    if (v instanceof admin.firestore.Timestamp)
        return v.toDate();
    if (v instanceof Date)
        return v;
    return null;
}
exports.generateDriverPayoutRun = (0, https_1.onCall)({ enforceAppCheck: false }, // admin-only checked manually
async (request) => {
    if (!request.auth)
        throw new https_1.HttpsError("unauthenticated", "User must be authenticated");
    if (request.auth.token.admin !== true) {
        throw new https_1.HttpsError("permission-denied", "Only admins can generate payouts");
    }
    const { period, round } = (request.data ?? {});
    if (!period || !/^\d{4}-\d{2}$/.test(period)) {
        throw new https_1.HttpsError("invalid-argument", "period must be 'YYYY-MM'");
    }
    if (round !== "R1" && round !== "R2") {
        throw new https_1.HttpsError("invalid-argument", "round must be 'R1' or 'R2'");
    }
    const firestore = db();
    const range = roundRange(period, round);
    // Active compensation config (latest effectiveFrom <= month end).
    const cfgCol = firestore.collection("driver_compensation_config");
    let cfgSnap = await cfgCol
        .where("effectiveFrom", "<=", admin.firestore.Timestamp.fromDate(range.monthEnd))
        .orderBy("effectiveFrom", "desc")
        .limit(1)
        .get();
    if (cfgSnap.empty) {
        // Fallback: earliest config (e.g. generating a period before the first effective date).
        cfgSnap = await cfgCol.orderBy("effectiveFrom", "asc").limit(1).get();
    }
    if (cfgSnap.empty) {
        throw new https_1.HttpsError("failed-precondition", "No compensation config exists — create one in Compensation Config first");
    }
    const cfg = cfgSnap.docs[0].data();
    // Published holiday date keys.
    const holSnap = await firestore.collection("holidays").get();
    const holidayKeys = new Set();
    for (const d of holSnap.docs) {
        const date = tsToDate(d.data().date);
        if (date)
            holidayKeys.add((0, compensationCompute_1.bangkokDateKey)(date));
    }
    const isHoliday = (0, compensationCompute_1.makeHolidayChecker)(holidayKeys);
    const fuelTiers = (cfg.fuelIncentiveTiers ?? []).map((t) => ({ min: t.minKmPerLitre, amountThb: t.amountThb }));
    const tripTiers = (cfg.tripVolumeTiers ?? []).map((t) => ({ min: t.minTrips, amountThb: t.amountThb }));
    const driversSnap = await firestore.collection("drivers").get();
    let written = 0;
    let skippedApproved = 0;
    await Promise.all(driversSnap.docs.map(async (driverDoc) => {
        const driver = driverDoc.data();
        const authId = driver.authId;
        if (!authId)
            return; // can't match trips without auth id
        // Delivered trips for the FULL month (for trip-volume count) keyed by driver auth id.
        const tripsSnap = await firestore
            .collection("trip_records")
            .where("driverId", "==", authId)
            .where("status", "==", "delivered")
            .where("deliveredTimestamp", ">=", admin.firestore.Timestamp.fromDate(range.monthStart))
            .where("deliveredTimestamp", "<", admin.firestore.Timestamp.fromDate(range.monthEnd))
            .get();
        const monthTrips = tripsSnap.docs
            .map((t) => {
            const data = t.data();
            const deliveredAt = tsToDate(data.deliveredTimestamp);
            return deliveredAt
                ? { deliveredAt, isMultiStop: data.isMultiDelivery === true, isStandby: false }
                : null;
        })
            .filter((t) => t !== null);
        // Base pay only for trips delivered within THIS round window.
        const roundTrips = monthTrips.filter((t) => t.deliveredAt >= range.start && t.deliveredAt < range.end);
        const base = (0, compensationCompute_1.computeBasePay)(roundTrips, {
            weekdayRateThb: cfg.weekdayRateThb,
            holidayRateThb: cfg.holidayRateThb,
            payStandby: cfg.payStandby === true,
        }, isHoliday);
        const lineItems = [
            { type: "EARNING", category: "TRIP_COMMISSION", name: "Trip pay", amount: base.basePayThb },
        ];
        const penaltyApplications = [];
        // Helper / training-day pay for this round window. Helper days are
        // tasks where this driver was selected as a helper by the main driver
        // (tasks.helperDriverIds, set at check-in), dated by the task's date.
        // Paid only on days with NO own delivered trip (driving days pay as trips).
        const deliveredDateKeys = new Set(monthTrips.map((t) => (0, compensationCompute_1.bangkokDateKey)(t.deliveredAt)));
        const helperSnap = await firestore
            .collection("tasks")
            .where("helperDriverIds", "array-contains", authId)
            .where("date", ">=", admin.firestore.Timestamp.fromDate(range.start))
            .where("date", "<", admin.firestore.Timestamp.fromDate(range.end))
            .get();
        const helperDayKeys = helperSnap.docs
            .map((h) => {
            const date = tsToDate(h.data().date);
            return date ? (0, compensationCompute_1.bangkokDateKey)(date) : "";
        })
            .filter(Boolean);
        const eligibleHelperDays = (0, compensationCompute_1.eligibleHelperDayKeys)(helperDayKeys, deliveredDateKeys);
        const helperPay = (0, compensationCompute_1.computeHelperPay)(eligibleHelperDays.length, cfg.helperDayRateThb ?? 400);
        if (helperPay > 0) {
            lineItems.push({ type: "EARNING", category: "HELPER_PAY", name: "Helper/training pay", amount: helperPay });
        }
        // Incentives + deductions settle in R2 only (computed over the full month).
        if (round === "R2") {
            const payableMonthTrips = monthTrips.filter((t) => !t.isMultiStop && (cfg.payStandby === true || !t.isStandby)).length;
            // TODO(OQ2): source real monthly km/L + refuel count per driver from fuel records.
            const fuel = (0, compensationCompute_1.computeFuelIncentive)(null, 0, fuelTiers, cfg.fuelMinRefuelsPerMonth ?? 5);
            const tripVolume = (0, compensationCompute_1.computeTripVolumeIncentive)(payableMonthTrips, tripTiers);
            const sso = (0, compensationCompute_1.computeSso)({
                hireDate: tsToDate(driver.hireDate),
                birthDate: tsToDate(driver.birthDate),
                probationPassed: driver.probationPassed === true,
            }, {
                ratePercent: cfg.sso?.ratePercent ?? 5,
                baseExistingThb: cfg.sso?.baseExistingThb ?? 15000,
                baseNewThb: cfg.sso?.baseNewThb ?? 12000,
                existingHiredBeforeYear: cfg.sso?.existingHiredBeforeYear ?? 2026,
                maxAgeInclusive: cfg.sso?.maxAgeInclusive ?? 55,
            }, range.monthEnd);
            if (fuel.amountThb > 0)
                lineItems.push({ type: "EARNING", category: "FUEL_INCENTIVE", name: "Fuel incentive", amount: fuel.amountThb });
            if (tripVolume > 0)
                lineItems.push({ type: "EARNING", category: "TRIP_VOLUME_INCENTIVE", name: "Trip-volume incentive", amount: tripVolume });
            const earnings = lineItems.filter((l) => l.type === "EARNING").reduce((s, l) => s + l.amount, 0);
            // Open penalties for this driver → scheduled installments (balances are
            // committed at approve time, not here; draft only computes).
            const penSnap = await firestore
                .collection("driver_penalties")
                .where("driverId", "==", authId)
                .where("status", "in", ["pending", "partially_deducted"])
                .get();
            const penaltyDocs = penSnap.docs.map((p) => ({ id: p.id, data: p.data() }));
            const scheduled = penaltyDocs.map((p) => {
                const total = Number(p.data.totalThb ?? 0);
                const remaining = Number(p.data.remainingThb ?? 0);
                const installmentsTotal = Number(p.data.installmentsTotal ?? 1);
                const per = installmentsTotal > 1 ? Math.round(total / installmentsTotal) : remaining;
                return { id: p.id, remainingThb: remaining, installmentThb: Math.min(Math.max(0, per), Math.max(0, remaining)) };
            });
            const deductionResult = (0, compensationCompute_1.applyDeductions)(earnings, sso, scheduled);
            if (deductionResult.ssoAppliedThb > 0) {
                lineItems.push({ type: "DEDUCTION", category: "SOCIAL_SECURITY", name: "Social security", amount: deductionResult.ssoAppliedThb });
            }
            for (const pp of deductionResult.perPenalty) {
                if (pp.appliedThb > 0) {
                    const src = penaltyDocs.find((d) => d.id === pp.id);
                    lineItems.push({
                        type: "DEDUCTION",
                        category: "PENALTY",
                        name: src?.data.typeName || src?.data.typeCode || "Penalty",
                        amount: pp.appliedThb,
                    });
                    penaltyApplications.push({ penaltyId: pp.id, appliedThb: pp.appliedThb });
                }
            }
        }
        const totalEarnings = lineItems.filter((l) => l.type === "EARNING").reduce((s, l) => s + l.amount, 0);
        const totalDeductions = lineItems.filter((l) => l.type === "DEDUCTION").reduce((s, l) => s + l.amount, 0);
        const netPay = Math.max(0, totalEarnings - totalDeductions);
        const docId = `${authId}_${period}_${round}`;
        const ref = firestore.collection("payroll").doc(docId);
        const existing = await ref.get();
        if (existing.exists && existing.data()?.status !== "DRAFT") {
            skippedApproved++;
            return; // never overwrite a non-draft payout
        }
        await ref.set({
            driverId: authId,
            driverName: `${driver.firstName ?? ""} ${driver.lastName ?? ""}`.trim() || driver.fullNameTh || "",
            payPeriod: period,
            round,
            periodStart: admin.firestore.Timestamp.fromDate(range.start),
            periodEnd: admin.firestore.Timestamp.fromDate(range.end),
            status: "DRAFT",
            lineItems,
            penaltyApplications,
            totalEarnings,
            totalDeductions,
            netPay,
            currency: "THB",
            updatedAt: admin.firestore.FieldValue.serverTimestamp(),
            createdAt: existing.exists ? existing.data()?.createdAt ?? admin.firestore.FieldValue.serverTimestamp() : admin.firestore.FieldValue.serverTimestamp(),
        }, { merge: false });
        written++;
    }));
    firebase_functions_1.logger.info("[generateDriverPayoutRun] done", { period, round, written, skippedApproved });
    return { period, round, written, skippedApproved };
});
/**
 * Approve a draft payout (FR18): atomically lock it APPROVED, post one expense
 * to the `transactions` ledger, and commit penalty installment balances. Runs
 * in a Firestore transaction so the ledger + penalty balances never diverge.
 */
exports.approveDriverPayout = (0, https_1.onCall)({ enforceAppCheck: false }, async (request) => {
    if (!request.auth)
        throw new https_1.HttpsError("unauthenticated", "User must be authenticated");
    if (request.auth.token.admin !== true) {
        throw new https_1.HttpsError("permission-denied", "Only admins can approve payouts");
    }
    const payoutId = request.data?.payoutId;
    if (!payoutId)
        throw new https_1.HttpsError("invalid-argument", "payoutId is required");
    const firestore = db();
    const approverUid = request.auth.uid;
    await firestore.runTransaction(async (tx) => {
        const payRef = firestore.collection("payroll").doc(payoutId);
        const paySnap = await tx.get(payRef);
        if (!paySnap.exists)
            throw new https_1.HttpsError("not-found", "Payout not found");
        const pay = paySnap.data();
        if (pay.status === "APPROVED" || pay.status === "PAID") {
            throw new https_1.HttpsError("failed-precondition", "Payout already approved/paid");
        }
        const applications = pay.penaltyApplications ?? [];
        // Read all penalty docs first (transaction: reads before writes).
        const penRefs = applications.map((a) => firestore.collection("driver_penalties").doc(a.penaltyId));
        const penSnaps = await Promise.all(penRefs.map((r) => tx.get(r)));
        // Commit penalty balances.
        penSnaps.forEach((snap, i) => {
            if (!snap.exists)
                return;
            const data = snap.data();
            const applied = applications[i].appliedThb;
            const remainingAfter = Math.max(0, Number(data.remainingThb ?? 0) - applied);
            const installmentsPaid = Number(data.installmentsPaid ?? 0) + 1;
            tx.update(penRefs[i], {
                remainingThb: remainingAfter,
                installmentsPaid,
                status: remainingAfter <= 0 ? "cleared" : "partially_deducted",
                updatedAt: admin.firestore.FieldValue.serverTimestamp(),
            });
        });
        // Post one expense entry to the ledger.
        const txnRef = firestore.collection("transactions").doc();
        tx.set(txnRef, {
            type: "driver_payout",
            subType: "Driver Compensation",
            amount: Number(pay.netPay ?? 0),
            date: new Date().toISOString().split("T")[0],
            driverId: pay.driverId ?? null,
            driverName: pay.driverName ?? null,
            payoutId,
            payPeriod: pay.payPeriod ?? null,
            round: pay.round ?? null,
            performedBy: approverUid,
            createdAt: admin.firestore.FieldValue.serverTimestamp(),
        });
        // Lock the payout.
        tx.update(payRef, {
            status: "APPROVED",
            approvedBy: approverUid,
            approvedAt: admin.firestore.FieldValue.serverTimestamp(),
            ledgerTransactionId: txnRef.id,
            updatedAt: admin.firestore.FieldValue.serverTimestamp(),
        });
    });
    firebase_functions_1.logger.info("[approveDriverPayout] approved", { payoutId, approverUid });
    return { payoutId, status: "APPROVED" };
});
//# sourceMappingURL=driverCompensation.js.map