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
import * as admin from "firebase-admin";
import { onCall, HttpsError } from "firebase-functions/v2/https";
import { logger } from "firebase-functions";
import {
    computeBasePay,
    computeFuelIncentive,
    computeTripVolumeIncentive,
    computeSso,
    applyDeductions,
    makeHolidayChecker,
    bangkokDateKey,
    type BasePayTrip,
    type IncentiveTier,
} from "./core/compensationCompute";

const db = () => admin.firestore();

type Round = "R1" | "R2";

interface GenerateRequest {
    period?: string; // "YYYY-MM"
    round?: Round;
}

/** Bangkok local midnight (start of `day`) as a UTC instant. */
function bkkMidnight(year: number, month1: number, day: number): Date {
    return new Date(Date.UTC(year, month1 - 1, day, 0, 0, 0) - 7 * 3600000);
}

function roundRange(period: string, round: Round) {
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

function tsToDate(v: unknown): Date | null {
    if (v instanceof admin.firestore.Timestamp) return v.toDate();
    if (v instanceof Date) return v;
    return null;
}

export const generateDriverPayoutRun = onCall(
    { enforceAppCheck: false }, // admin-only checked manually
    async (request) => {
        if (!request.auth) throw new HttpsError("unauthenticated", "User must be authenticated");
        if (request.auth.token.admin !== true) {
            throw new HttpsError("permission-denied", "Only admins can generate payouts");
        }
        const { period, round } = (request.data ?? {}) as GenerateRequest;
        if (!period || !/^\d{4}-\d{2}$/.test(period)) {
            throw new HttpsError("invalid-argument", "period must be 'YYYY-MM'");
        }
        if (round !== "R1" && round !== "R2") {
            throw new HttpsError("invalid-argument", "round must be 'R1' or 'R2'");
        }

        const firestore = db();
        const range = roundRange(period, round);

        // Active compensation config (latest effectiveFrom <= month end).
        const cfgSnap = await firestore
            .collection("driver_compensation_config")
            .where("effectiveFrom", "<=", admin.firestore.Timestamp.fromDate(range.monthEnd))
            .orderBy("effectiveFrom", "desc")
            .limit(1)
            .get();
        if (cfgSnap.empty) throw new HttpsError("failed-precondition", "No compensation config is in effect");
        const cfg = cfgSnap.docs[0].data();

        // Published holiday date keys.
        const holSnap = await firestore.collection("holidays").get();
        const holidayKeys = new Set<string>();
        for (const d of holSnap.docs) {
            const date = tsToDate(d.data().date);
            if (date) holidayKeys.add(bangkokDateKey(date));
        }
        const isHoliday = makeHolidayChecker(holidayKeys);

        const fuelTiers: IncentiveTier[] = (cfg.fuelIncentiveTiers ?? []).map(
            (t: { minKmPerLitre: number; amountThb: number }) => ({ min: t.minKmPerLitre, amountThb: t.amountThb }),
        );
        const tripTiers: IncentiveTier[] = (cfg.tripVolumeTiers ?? []).map(
            (t: { minTrips: number; amountThb: number }) => ({ min: t.minTrips, amountThb: t.amountThb }),
        );

        const driversSnap = await firestore.collection("drivers").get();

        let written = 0;
        let skippedApproved = 0;

        await Promise.all(
            driversSnap.docs.map(async (driverDoc) => {
                const driver = driverDoc.data();
                const authId: string | undefined = driver.authId;
                if (!authId) return; // can't match trips without auth id

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
                    .filter((t): t is BasePayTrip => t !== null);

                // Base pay only for trips delivered within THIS round window.
                const roundTrips = monthTrips.filter(
                    (t) => t.deliveredAt >= range.start && t.deliveredAt < range.end,
                );
                const base = computeBasePay(
                    roundTrips,
                    {
                        weekdayRateThb: cfg.weekdayRateThb,
                        holidayRateThb: cfg.holidayRateThb,
                        payStandby: cfg.payStandby === true,
                    },
                    isHoliday,
                );

                const lineItems: Array<{ type: "EARNING" | "DEDUCTION"; category: string; name: string; amount: number }> = [
                    { type: "EARNING", category: "TRIP_COMMISSION", name: "Trip pay", amount: base.basePayThb },
                ];

                // Incentives + deductions settle in R2 only (computed over the full month).
                if (round === "R2") {
                    const payableMonthTrips = monthTrips.filter(
                        (t) => !t.isMultiStop && (cfg.payStandby === true || !t.isStandby),
                    ).length;

                    // TODO(OQ2): source real monthly km/L + refuel count per driver from fuel records.
                    const fuel = computeFuelIncentive(null, 0, fuelTiers, cfg.fuelMinRefuelsPerMonth ?? 5);
                    const tripVolume = computeTripVolumeIncentive(payableMonthTrips, tripTiers);
                    const sso = computeSso(
                        {
                            hireDate: tsToDate(driver.hireDate),
                            birthDate: tsToDate(driver.birthDate),
                            probationPassed: driver.probationPassed === true,
                        },
                        {
                            ratePercent: cfg.sso?.ratePercent ?? 5,
                            baseExistingThb: cfg.sso?.baseExistingThb ?? 15000,
                            baseNewThb: cfg.sso?.baseNewThb ?? 12000,
                            existingHiredBeforeYear: cfg.sso?.existingHiredBeforeYear ?? 2026,
                            maxAgeInclusive: cfg.sso?.maxAgeInclusive ?? 55,
                        },
                        range.monthEnd,
                    );

                    if (fuel.amountThb > 0) lineItems.push({ type: "EARNING", category: "FUEL_INCENTIVE", name: "Fuel incentive", amount: fuel.amountThb });
                    if (tripVolume > 0) lineItems.push({ type: "EARNING", category: "TRIP_VOLUME_INCENTIVE", name: "Trip-volume incentive", amount: tripVolume });

                    const earnings = lineItems.filter((l) => l.type === "EARNING").reduce((s, l) => s + l.amount, 0);
                    // TODO(Story 3.1): load driver_penalties and schedule installments here.
                    const deductionResult = applyDeductions(earnings, sso, []);
                    if (deductionResult.ssoAppliedThb > 0) {
                        lineItems.push({ type: "DEDUCTION", category: "SOCIAL_SECURITY", name: "Social security", amount: deductionResult.ssoAppliedThb });
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

                await ref.set(
                    {
                        driverId: authId,
                        driverName: `${driver.firstName ?? ""} ${driver.lastName ?? ""}`.trim() || driver.fullNameTh || "",
                        payPeriod: period,
                        round,
                        periodStart: admin.firestore.Timestamp.fromDate(range.start),
                        periodEnd: admin.firestore.Timestamp.fromDate(range.end),
                        status: "DRAFT",
                        lineItems,
                        totalEarnings,
                        totalDeductions,
                        netPay,
                        currency: "THB",
                        updatedAt: admin.firestore.FieldValue.serverTimestamp(),
                        createdAt: existing.exists ? existing.data()?.createdAt ?? admin.firestore.FieldValue.serverTimestamp() : admin.firestore.FieldValue.serverTimestamp(),
                    },
                    { merge: false },
                );
                written++;
            }),
        );

        logger.info("[generateDriverPayoutRun] done", { period, round, written, skippedApproved });
        return { period, round, written, skippedApproved };
    },
);
