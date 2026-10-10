// Exports the billing golden vectors from the TypeScript engine (main spec §6.16).
//
//   node testdata/golden/billing/export.mjs            (from logitrack-api/, Node >= 22.18)
//
// Node strips the types of the imported .ts files itself; no install is needed.
// Ported cases re-run the assertions of their Vitest `it` block against the
// TypeScript output before storing it as `want`, so a vector can only hold what
// the legacy test accepts. Where the Go contract deliberately differs (R15, R16,
// R19, R20) the TypeScript output is kept as `legacy` and `want` is the Go value.
// characterisation.json pins untested TypeScript behaviour with seeded random
// inputs. logitrack-web/lib/billingGolden.test.ts re-checks every vector against
// the TypeScript modules on each `pnpm test`; the Go tests check them against Go.
import assert from "node:assert/strict";
import { readFileSync, writeFileSync } from "node:fs";
import { dec, enc } from "./codec.mjs";

const web = (p) => new URL(`../../../../logitrack-web/${p}`, import.meta.url);
const bc = await import(web("lib/billingCompute.ts"));
const bd = await import(web("lib/billingDate.ts"));
const pl = await import(web("functions/src/core/billingPeriodLock.ts"));

const COMMIT = "4f552099";
const out = (name) => new URL(name, import.meta.url);

// ─── vector file builder ─────────────────────────────────────────────────────

function vectorFile(source, note) {
  const file = { source, sourceCommit: COMMIT, note, cases: [] };
  let current = null;
  return {
    file,
    /** One Vitest `it` (or an `added` Go-contract case when `added` is set). */
    it(name, line, body, added) {
      current = { name, line, ...(added ? { added } : {}), checks: [] };
      body();
      assert.ok(current.checks.length > 0, `case without checks: ${name}`);
      file.cases.push(current);
      current = null;
    },
    /** A check whose want is the TypeScript output, after `expectFn` accepted it. */
    check(fn, args, expectFn) {
      const got = CALL[fn](...dec(enc(args)));
      if (expectFn) expectFn(got);
      current.checks.push({ fn, args: enc(args), want: enc(got) });
    },
    /** A check with a literal want (the TypeScript output must equal it). */
    literal(fn, args, want) {
      current.checks.push({ fn, args: enc(args), want: enc(want) });
    },
    /** A deliberate Go divergence: want is the Go contract, legacy the TypeScript output. */
    diverge(fn, args, goWant, divergence, { wantReason, legacyNow, expectLegacy } = {}) {
      const got = legacyNow ? undefined : CALL[fn](...dec(enc(args)));
      if (expectLegacy) expectLegacy(got);
      current.checks.push({
        fn,
        args: enc(args),
        want: enc(goWant),
        ...(wantReason ? { wantReason } : {}),
        legacy: legacyNow ? { $now: true } : enc(got),
        divergence,
      });
    },
    write(name, { compact = false } = {}) {
      const text = compact
        ? stringifyCompact(file)
        : JSON.stringify(file, null, 2) + "\n";
      writeFileSync(out(name), text);
      return file;
    },
  };
}

function stringifyCompact(file) {
  const head = JSON.stringify({ ...file, cases: undefined }).slice(0, -1);
  const cases = file.cases.map((c) => {
    const checks = c.checks.map((k) => "      " + JSON.stringify(k)).join(",\n");
    const meta = JSON.stringify({ ...c, checks: undefined }).slice(0, -1);
    return `    ${meta},"checks":[\n${checks}\n    ]}`;
  });
  return `${head},"cases":[\n${cases.join(",\n")}\n]}\n`;
}

const idOf = (x) => (x ? x.id : null);

// ─── TypeScript entry points by vector name ──────────────────────────────────
// Kept identical to the Vitest verifier's table; the Go table maps the same
// names onto the Go API (internal/billing/compute/golden_test.go).
const CALL = {
  normalizeVehicleClass: (v) => bc.normalizeVehicleClass(v),
  extractHubId: (v) => bc.extractHubId(v),
  normalizeDestinationCode: (v) => bc.normalizeDestinationCode(v),
  computeFinalRateThb: (b, m, a) => bc.computeFinalRateThb(b, m, a),
  fuelBandFloor: (p) => bc.fuelBandFloor(p),
  fuelBandRange: (p) => bc.fuelBandRange(p),
  computeFuelSurchargeThb: (p, b, k) => bc.computeFuelSurchargeThb(p, b, k),
  bangkokDateStrFromMillis: (ms) => bc.bangkokDateStrFromMillis(ms),
  isEffectiveOnOrBeforeBillingDate: (e, b) => bc.isEffectiveOnOrBeforeBillingDate(e, b),
  selectBillingRateEntry: (c, h, d, v, b, e, cat) => idOf(bc.selectBillingRateEntry(c, h, d, v, b, e, cat ?? undefined)),
  selectFuelAdjustmentForBillingDate: (c, b, a) => idOf(bc.selectFuelAdjustmentForBillingDate(c, b, a)),
  selectStandbyRateEntry: (c, b, r) => idOf(bc.selectStandbyRateEntry(c, b, r)),
  resolveBillingRoundProvenance: (ms, adj) => bc.resolveBillingRoundProvenance(ms, adj),
  computeTripBillingFromParts: (trip, task, e, f, cat) => bc.computeTripBillingFromParts(trip, task, e, f, cat ?? undefined),
  computeMultiDeliveryBilling: (trip, task, stops, vc, e, f, fee, cat) =>
    bc.computeMultiDeliveryBilling(trip, task, stops, vc, e, f, fee ?? undefined, cat ?? undefined),
  computeStandbyBilling: (ms, c, r) => bc.computeStandbyBilling(ms, c, r),
  getTripBillingDateMs: (trip) => bc.getTripBillingDateMs(trip),
  resolveTaskCustomerId: (task) => bc.resolveTaskCustomerId(task),
  snapshotCarriesFuel: (s) => bc.snapshotCarriesFuel(s),
  isFrozenBillingSnapshot: (s) => bc.isFrozenBillingSnapshot(s),
  // web:lib/billingRates.ts:67-86 (imports Firestore, so not loadable here): the
  // same composition over computeTripBillingFromParts; the Vitest verifier calls
  // the real computeTripBilling.
  computeTripBilling: (trip, task, rates, fuel) => {
    const parts = { deliveredTimestamp: trip.deliveredTimestamp, createdAt: trip.createdAt };
    const input = task
      ? {
          sourceHub: task.sourceHub,
          destination: task.destination,
          truckType: task.truckType,
          billingCustomerId: task.billingCustomerId,
          sourceHubLinkedCustomerId: task.sourceHubLinkedCustomerId,
          destinationLinkedCustomerId: task.destinationLinkedCustomerId,
        }
      : null;
    const explicit = task?.jobCategory;
    if (explicit === "SUPPLEMENTARY" || explicit === "PRIMARY") {
      return bc.computeTripBillingFromParts(parts, input, rates, fuel, explicit);
    }
    return (
      bc.computeTripBillingFromParts(parts, input, rates, fuel, "PRIMARY") ??
      bc.computeTripBillingFromParts(parts, input, rates, fuel, "SUPPLEMENTARY")
    );
  },
  // web:lib/billingDate.ts
  bangkokMidnightFromDateStr: (s) => bd.bangkokMidnightFromDateStr(s),
  bangkokDateStr: (d) => bd.bangkokDateStr(d),
  "bangkokDateStr(bangkokMidnightFromDateStr)": (s) => bd.bangkokDateStr(bd.bangkokMidnightFromDateStr(s)),
  msBeforeUtcMidnight: (s) => {
    const [y, m, d] = s.split("-").map(Number);
    return Date.UTC(y, m - 1, d) - bd.bangkokMidnightFromDateStr(s).getTime();
  },
  pickedDateToDateStr: (d) => bd.pickedDateToDateStr(d),
  bangkokMidnightFromPickedDate: (d) => bd.bangkokMidnightFromPickedDate(d),
  "bangkokDateStr(bangkokMidnightFromPickedDate)": (d) => bd.bangkokDateStr(bd.bangkokMidnightFromPickedDate(d)),
  // fn:core/billingPeriodLock.ts
  billingPeriodKey: (c, y, m) => pl.billingPeriodKey(c, y, m),
  bangkokYearMonth: (ms) => pl.bangkokYearMonth(ms),
  lockFor: (locks, c, ms) => new pl.BillingPeriodLocks(locks).lockFor(c ?? undefined, ms),
  // JavaScript number semantics the engine relies on.
  "Math.round": (x) => Math.round(x),
  round2: (x) => Math.round(x * 100) / 100,
  toFixed: (x, digits) => x.toFixed(digits),
  withholdingThb: (total, rate) => Math.round(total * rate * 100) / 100, // web:lib/billingDocument.ts:365
};

// ─── lib/billingCompute.test.ts ──────────────────────────────────────────────

const ms = (iso) => new Date(iso).getTime();

function exportBillingCompute() {
  const v = vectorFile(
    "logitrack-web/lib/billingCompute.test.ts",
    "49 Vitest cases ported; `added` cases are the Go contract for R15, R16, R19 and R20. Instants are epoch milliseconds; 0 or null is absent. Vitest's local-time literals (`new Date(\"2025-07-15T10:00:00\")`) are pinned to +07:00."
  );
  const eq = (want) => (got) => assert.deepStrictEqual(got, want);

  v.it("normalizeVehicleClass > folds the truck master's names onto the class a task carries", 25, () => {
    for (const [i, o] of [["Pickup", "4W"], ["4 Wheels Jumbo", "4WJ"], ["6 Wheels", "6WH"], ["10 Wheels", "10WH"], ["18 Wheels", "18WH"]])
      v.check("normalizeVehicleClass", [i], eq(o));
  });
  v.it("normalizeVehicleClass > folds the codes an earlier normalize pass produced", 33, () => {
    v.check("normalizeVehicleClass", ["6W"], eq("6WH"));
    v.check("normalizeVehicleClass", ["10W"], eq("10WH"));
  });
  v.it("normalizeVehicleClass > folds the pre-2026-07 names for 4W", 39, () => {
    v.check("normalizeVehicleClass", ["PICKUP"], eq("4W"));
    v.check("normalizeVehicleClass", ["4WH"], eq("4W"));
  });
  v.it("normalizeVehicleClass > leaves a current class untouched, so both sides of a lookup agree", 44, () => {
    for (const code of ["4W", "4WJ", "6WH", "10WH", "18WH", "VAN"]) v.check("normalizeVehicleClass", [code], eq(code));
  });
  v.it("normalizeVehicleClass > defaults a missing class to 4WJ", 50, () => {
    // R15: a blank class is no class in Go (FoldVehicleClass -> ("", false)).
    v.diverge("normalizeVehicleClass", [""], null, "R15", { expectLegacy: eq("4WJ") });
    v.diverge("normalizeVehicleClass", [null], null, "R15", { expectLegacy: eq("4WJ") });
  });

  v.it("computeFinalRateThb > rounds to 2 decimal places like billing snapshot", 57, () => {
    v.check("computeFinalRateThb", [100, 1.05, 0], eq(105));
    v.check("computeFinalRateThb", [100, 1.055, 12.345], eq(Math.round((100 * 1.055 + 12.345) * 100) / 100));
    v.check("computeFinalRateThb", [333.33, 1.01, 0.005], eq(Math.round((333.33 * 1.01 + 0.005) * 100) / 100));
  });

  v.it("fuelBandFloor (ADR 0009 §3 — the band is (n, n+1]) > keeps a price at the exact top of a band inside that band", 65, () => {
    v.check("fuelBandFloor", [42.0], eq(41));
    v.check("fuelBandFloor", [37.0], eq(36));
  });
  v.it("fuelBandFloor (ADR 0009 §3 — the band is (n, n+1]) > moves to the next band one satang later", 72, () => {
    v.check("fuelBandFloor", [42.01], eq(42));
    v.check("fuelBandFloor", [36.01], eq(36));
  });
  v.it("fuelBandFloor (ADR 0009 §3 — the band is (n, n+1]) > agrees with the old floor behaviour everywhere inside a band", 77, () => {
    v.check("fuelBandFloor", [42.5], eq(42));
    v.check("fuelBandFloor", [42.99], eq(42));
    v.check("fuelBandFloor", [36.5], eq(36));
  });
  v.it("fuelBandFloor (ADR 0009 §3 — the band is (n, n+1]) > is exact on values that are not representable in binary floating point", 83, () => {
    v.check("fuelBandFloor", [41.1], eq(41));
    v.check("fuelBandFloor", [41.7], eq(41));
    v.check("fuelBandFloor", [43.29], eq(43));
  });
  v.it("fuelBandFloor (ADR 0009 §3 — the band is (n, n+1]) > returns NaN rather than a plausible number for junk input", 90, () => {
    v.check("fuelBandFloor", [NaN], (g) => assert.ok(Number.isNaN(g)));
    v.check("fuelBandFloor", [Infinity], (g) => assert.ok(Number.isNaN(g)));
  });

  v.it("fuelBandRange > reports the inclusive bounds the contract is written in", 97, () => {
    v.check("fuelBandRange", [42.0], eq({ lowerThb: 41.01, upperThb: 42 }));
    v.check("fuelBandRange", [42.01], eq({ lowerThb: 42.01, upperThb: 43 }));
    v.check("fuelBandRange", [36.5], eq({ lowerThb: 36.01, upperThb: 37 }));
  });
  v.it("fuelBandRange > is null when there is no price to derive a band from", 103, () => {
    v.check("fuelBandRange", [NaN], eq(null));
  });

  v.it("computeFuelSurchargeThb > charges nothing at the baseline band, including its top edge", 110, () => {
    v.check("computeFuelSurchargeThb", [41.5, 41, 10], eq(0));
    v.check("computeFuelSurchargeThb", [42.0, 41, 10], eq(0));
  });
  v.it("computeFuelSurchargeThb > charges one step per band above the baseline", 115, () => {
    v.check("computeFuelSurchargeThb", [42.01, 41, 10], eq(10));
    v.check("computeFuelSurchargeThb", [43.0, 41, 10], eq(10));
    v.check("computeFuelSurchargeThb", [43.01, 41, 10], eq(20));
  });
  v.it("computeFuelSurchargeThb > discounts below the baseline — the adjustment is symmetric and never clamped", 121, () => {
    v.check("computeFuelSurchargeThb", [40.5, 41, 10], eq(-10));
    v.check("computeFuelSurchargeThb", [39.0, 41, 10], eq(-30));
    v.check("computeFuelSurchargeThb", [39.01, 41, 10], eq(-20));
  });

  v.it("bangkokDateStrFromMillis > reports the Bangkok calendar date, not the UTC one", 130, () => {
    v.check("bangkokDateStrFromMillis", [Date.UTC(2026, 7, 15, 17, 0, 0)], eq("2026-08-16"));
  });
  v.it("bangkokDateStrFromMillis > holds at both ends of a Bangkok day", 135, () => {
    v.check("bangkokDateStrFromMillis", [Date.UTC(2026, 7, 15, 17, 0, 0)], eq("2026-08-16"));
    v.check("bangkokDateStrFromMillis", [Date.UTC(2026, 7, 16, 16, 59, 59)], eq("2026-08-16"));
    v.check("bangkokDateStrFromMillis", [Date.UTC(2026, 7, 16, 17, 0, 0)], eq("2026-08-17"));
  });

  {
    const base = { customerId: "cust1", importId: "imp1", hubId: "HUB", destinationCode: "DEST", vehicleClass: "4WJ" };
    const jan = Date.UTC(2026, 0, 1);
    const feb = Date.UTC(2026, 1, 1);
    const billDate = Date.UTC(2026, 2, 1);
    v.it("voided announcements are never selected (ADR 0009 §1) > falls back to the previous round when the newest one is voided", 154, () => {
      const entries = [
        { ...base, id: "r1", rateThb: 1000, effectiveFromMs: jan },
        { ...base, id: "r2", rateThb: 1200, effectiveFromMs: feb, voided: true },
      ];
      v.check("selectBillingRateEntry", ["cust1", "HUB", "DEST", "4WJ", billDate, entries, null], eq("r1"));
    });
    v.it("voided announcements are never selected (ADR 0009 §1) > ignores a voided fuel adjustment", 162, () => {
      const adjustments = [
        { id: "a1", customerId: "cust1", effectiveFromMs: jan, rateMultiplier: 1, addThbPerTrip: 10 },
        { id: "a2", customerId: "cust1", effectiveFromMs: feb, rateMultiplier: 1, addThbPerTrip: 20, voided: true },
      ];
      v.check("selectFuelAdjustmentForBillingDate", ["cust1", billDate, adjustments], eq("a1"));
    });
  }

  {
    const legacyUtcMidnightAug1 = Date.UTC(2026, 7, 1, 0, 0, 0);
    const correctBkkMidnightAug1 = Date.UTC(2026, 6, 31, 17, 0, 0);
    const overnightAug1 = Date.UTC(2026, 6, 31, 17, 21, 0);
    const lateJul31 = Date.UTC(2026, 6, 31, 16, 0, 0);
    const d = "isEffectiveOnOrBeforeBillingDate (ADR 0009 §2 — announcements are declared by Bangkok DATE)";
    v.it(`${d} > puts an overnight switch-day trip in-round for both storage conventions`, 187, () => {
      v.check("isEffectiveOnOrBeforeBillingDate", [legacyUtcMidnightAug1, overnightAug1], eq(true));
      v.check("isEffectiveOnOrBeforeBillingDate", [correctBkkMidnightAug1, overnightAug1], eq(true));
    });
    v.it(`${d} > keeps the day before out of round for both storage conventions`, 192, () => {
      v.check("isEffectiveOnOrBeforeBillingDate", [legacyUtcMidnightAug1, lateJul31], eq(false));
      v.check("isEffectiveOnOrBeforeBillingDate", [correctBkkMidnightAug1, lateJul31], eq(false));
    });
  }

  {
    const jul8 = Date.UTC(2026, 6, 8, 0, 0, 0);
    const legacyAug1 = Date.UTC(2026, 7, 1, 0, 0, 0);
    const overnightAug1 = Date.UTC(2026, 6, 31, 17, 21, 0);
    const d = "overnight switch-day trips price under the correct round (legacy UTC-midnight effectiveFrom)";
    v.it(`${d} > selectFuelAdjustmentForBillingDate picks the Aug round, not the previous one`, 205, () => {
      const adjustments = [
        { id: "jul", customerId: "c", effectiveFromMs: jul8, rateMultiplier: 1, addThbPerTrip: -70 },
        { id: "aug", customerId: "c", effectiveFromMs: legacyAug1, rateMultiplier: 1, addThbPerTrip: -50 },
      ];
      v.check("selectFuelAdjustmentForBillingDate", ["c", overnightAug1, adjustments], eq("aug"));
    });
    v.it(`${d} > selectBillingRateEntry honors the Bangkok day for a legacy-stored rate card`, 213, () => {
      const base = { customerId: "c", importId: "i", hubId: "HUB", destinationCode: "DEST", vehicleClass: "4WJ" };
      const entries = [
        { ...base, id: "old", rateThb: 1000, effectiveFromMs: jul8 },
        { ...base, id: "aug", rateThb: 1200, effectiveFromMs: legacyAug1 },
      ];
      v.check("selectBillingRateEntry", ["c", "HUB", "DEST", "4WJ", overnightAug1, entries, null], eq("aug"));
    });
    v.it(`${d} > selectStandbyRateEntry honors the Bangkok day for a legacy-stored standby rate`, 222, () => {
      const rates = [
        { id: "old", customerId: "c", rateThb: 300, effectiveFromMs: jul8 },
        { id: "aug", customerId: "c", rateThb: 400, effectiveFromMs: legacyAug1 },
      ];
      v.check("selectStandbyRateEntry", ["c", overnightAug1, rates], eq("aug"));
    });
  }

  {
    const adj = (t, price) => ({ id: `a${t}`, customerId: "cust1", effectiveFromMs: t, rateMultiplier: 1, addThbPerTrip: 0, referenceFuelPriceThb: price });
    const d = "resolveBillingRoundProvenance (ADR 0009 §4)";
    v.it(`${d} > keeps two fuel rounds distinct even when the rate card was imported after both`, 241, () => {
      const rateEntryImportedLate = Date.UTC(2026, 6, 19, 17, 0, 0);
      v.check("resolveBillingRoundProvenance", [rateEntryImportedLate, adj(Date.UTC(2026, 5, 30, 17, 0, 0), 34.5)], (g) =>
        assert.equal(g.roundEffectiveFromDateStr, "2026-07-01"));
      v.check("resolveBillingRoundProvenance", [rateEntryImportedLate, adj(Date.UTC(2026, 6, 15, 17, 0, 0), 36.5)], (g) =>
        assert.equal(g.roundEffectiveFromDateStr, "2026-07-16"));
    });
    v.it(`${d} > carries the band derived from the announced price`, 258, () => {
      v.check("resolveBillingRoundProvenance", [0, adj(Date.UTC(2026, 6, 15, 17, 0, 0), 34.5)], (g) => {
        assert.equal(g.fuelBandLowerThb, 34.01);
        assert.equal(g.fuelBandUpperThb, 35);
        assert.equal(g.referenceFuelPriceThb, 34.5);
      });
    });
    v.it(`${d} > falls back to the rate entry only when no fuel adjustment applied`, 265, () => {
      v.check("resolveBillingRoundProvenance", [Date.UTC(2026, 6, 19, 17, 0, 0), null], (g) => {
        assert.equal(g.roundEffectiveFromDateStr, "2026-07-20");
        assert.equal(g.fuelBandLowerThb, undefined);
      });
    });
    v.it(`${d} > reports no band when the round was announced without a reference price`, 271, () => {
      v.check("resolveBillingRoundProvenance", [0, adj(Date.UTC(2026, 6, 15, 17, 0, 0))], (g) => {
        assert.equal(g.roundEffectiveFromDateStr, "2026-07-16");
        assert.equal(g.fuelBandLowerThb, undefined);
        assert.equal(g.referenceFuelPriceThb, undefined);
      });
    });
  }

  {
    const entries = [
      { id: "e1", customerId: "c1", importId: "imp1", hubId: "HUBA", destinationCode: "SOCE", vehicleClass: "4WJ", rateThb: 1000, effectiveFromMs: ms("2020-01-01") },
    ];
    const fuel = [{ id: "f1", customerId: "c1", effectiveFromMs: ms("2025-06-01"), rateMultiplier: 1.1, addThbPerTrip: 50 }];
    v.it("computeTripBillingFromParts > matches manual composition for bill date and fuel rule", 302, () => {
      v.check(
        "computeTripBillingFromParts",
        [{ deliveredTimestamp: ms("2025-07-15T10:00:00+07:00") }, { sourceHub: "HUBA - Name", destination: "SOCE", truckType: "4WJ", sourceHubLinkedCustomerId: "c1" }, entries, fuel, null],
        (g) => {
          assert.notEqual(g, null);
          assert.equal(g.finalRateThb, bc.computeFinalRateThb(1000, 1.1, 50));
          assert.equal(g.baseRateThb, 1000);
          assert.equal(g.customerId, "c1");
        }
      );
    });
    v.it("computeTripBillingFromParts > selectFuelAdjustmentForBillingDate picks latest rule on or before bill date", 321, () => {
      v.check("selectFuelAdjustmentForBillingDate", ["c1", ms("2025-07-01"), fuel], eq("f1"));
    });

    // R15 / R19 at trip level (spec §6.16): the same priced input with no class, or no date.
    const task = { sourceHub: "HUBA - Name", destination: "SOCE", sourceHubLinkedCustomerId: "c1" };
    v.it("computeTripBillingFromParts > R15: a trip whose task has no truck type is unpriced no_vehicle_class", 0, () => {
      for (const truckType of ["", null, "   "]) {
        v.diverge("computeTripBillingFromParts", [{ deliveredTimestamp: ms("2025-07-15T10:00:00+07:00") }, { ...task, truckType }, entries, fuel, null], null, "R15", {
          wantReason: "no_vehicle_class",
          expectLegacy: (g) => assert.equal(g.finalRateThb, bc.computeFinalRateThb(1000, 1.1, 50)),
        });
      }
    }, "R15");
    v.it("computeTripBillingFromParts > R19: a trip with no plan, delivery or creation instant is unpriced no_billing_date", 0, () => {
      // Legacy prices at Date.now(); no fuel and a 2020 card keep that output fixed.
      v.diverge("computeTripBillingFromParts", [{}, { ...task, truckType: "4WJ" }, entries, [], null], null, "R19", {
        wantReason: "no_billing_date",
        expectLegacy: (g) => assert.equal(g.finalRateThb, 1000),
      });
      v.diverge("computeTripBillingFromParts", [{ deliveredTimestamp: 0, createdAt: 0, billingDateMs: 0 }, { ...task, truckType: "4WJ" }, entries, [], null], null, "R19", {
        wantReason: "no_billing_date",
        expectLegacy: (g) => assert.equal(g.finalRateThb, 1000),
      });
    }, "R19");
  }

  {
    const entries = [
      { id: "primary1", customerId: "c1", importId: "imp-primary", hubId: "HUBA", destinationCode: "SOCE", vehicleClass: "4WJ", rateThb: 1000, effectiveFromMs: ms("2020-01-01"), jobCategory: "PRIMARY" },
      { id: "supp1", customerId: "c1", importId: "imp-supp", hubId: "HUBA", destinationCode: "WANGTHONGLANG12", vehicleClass: "4WJ", rateThb: 1250, effectiveFromMs: ms("2020-01-01"), jobCategory: "SUPPLEMENTARY" },
    ];
    const trip = { deliveredTimestamp: ms("2026-05-02T10:00:00+07:00") };
    const task = (destination) => ({ sourceHub: "HUBA - Name", destination, truckType: "4WJ", sourceHubLinkedCustomerId: "c1" });
    const d = "jobCategory dimension (ADR-0005 — supplementary trips)";
    v.it(`${d} > PRIMARY lookup (default) does NOT match a supplementary-only route`, 366, () => {
      v.check("computeTripBillingFromParts", [trip, task("WANGTHONGLANG12"), entries, [], null], eq(null));
    });
    v.it(`${d} > SUPPLEMENTARY lookup resolves the supplementary route at its agreed price`, 377, () => {
      v.check("computeTripBillingFromParts", [trip, task("WANGTHONGLANG12"), entries, [], "SUPPLEMENTARY"], (g) => assert.equal(g.finalRateThb, 1250));
    });
    v.it(`${d} > derivation: PRIMARY first, fall back to SUPPLEMENTARY`, 389, () => {
      v.check("computeTripBillingFromParts", [trip, task("SOCE"), entries, [], "PRIMARY"], (g) => assert.equal(g.finalRateThb, 1000));
      v.check("computeTripBillingFromParts", [trip, task("WANGTHONGLANG12"), entries, [], "PRIMARY"], eq(null));
      v.check("computeTripBillingFromParts", [trip, task("WANGTHONGLANG12"), entries, [], "SUPPLEMENTARY"], (g) => assert.equal(g.finalRateThb, 1250));
    });
    v.it(`${d} > a SUPPLEMENTARY lookup does not leak into PRIMARY pricing`, 403, () => {
      v.check("computeTripBillingFromParts", [trip, task("SOCE"), entries, [], "PRIMARY"], (g) => assert.equal(g.finalRateThb, 1000));
    });
    v.it(`${d} > SUPPLEMENTARY is a fixed rate — a fuel adjustment does NOT change it`, 415, () => {
      const fuel = [{ id: "f1", customerId: "c1", effectiveFromMs: ms("2020-01-01"), rateMultiplier: 1.1, addThbPerTrip: 50 }];
      v.check("computeTripBillingFromParts", [trip, task("WANGTHONGLANG12"), entries, fuel, "SUPPLEMENTARY"], (g) => {
        assert.equal(g.finalRateThb, 1250);
        assert.equal(g.rateMultiplier, 1);
        assert.equal(g.addThbPerTrip, 0);
        assert.equal(g.fuelAdjustmentId, undefined);
      });
      v.check("computeTripBillingFromParts", [trip, task("SOCE"), entries, fuel, "PRIMARY"], (g) =>
        assert.equal(g.finalRateThb, bc.computeFinalRateThb(1000, 1.1, 50)));
    });
  }

  {
    const d = "getTripBillingDateMs (ADR 0027 — plan-date override)";
    v.it(`${d} > prefers an explicit billingDateMs over deliveredTimestamp`, 450, () => {
      const planMs = Date.UTC(2026, 8, 30, 17, 0, 0);
      v.check("getTripBillingDateMs", [{ billingDateMs: planMs, deliveredTimestamp: Date.UTC(2026, 9, 1, 5, 0, 0) }], eq(planMs));
    });
    v.it(`${d} > falls back to deliveredTimestamp when no override (delivered-basis unchanged)`, 458, () => {
      const deliveredMs = Date.UTC(2026, 9, 1, 5, 0, 0);
      v.check("getTripBillingDateMs", [{ deliveredTimestamp: deliveredMs }], eq(deliveredMs));
    });
    v.it(`${d} > ignores a zero / non-finite override and uses deliveredTimestamp`, 463, () => {
      const deliveredMs = Date.UTC(2026, 9, 1, 5, 0, 0);
      v.check("getTripBillingDateMs", [{ billingDateMs: 0, deliveredTimestamp: deliveredMs }], eq(deliveredMs));
      v.check("getTripBillingDateMs", [{ billingDateMs: NaN, deliveredTimestamp: deliveredMs }], eq(deliveredMs));
    });
    v.it(`${d} > falls back to createdAt when neither override nor deliveredTimestamp exist`, 469, () => {
      const createdMs = Date.UTC(2026, 8, 15, 3, 0, 0);
      v.check("getTripBillingDateMs", [{ createdAt: createdMs }], eq(createdMs));
    });
    v.it(`${d} > R19: no instant at all is no billing date, never Date.now()`, 0, () => {
      v.diverge("getTripBillingDateMs", [{}], null, "R19", { legacyNow: true });
      v.diverge("getTripBillingDateMs", [{ billingDateMs: 0, deliveredTimestamp: 0, createdAt: 0 }], null, "R19", { legacyNow: true });
    }, "R19");
  }

  {
    const d = "resolveTaskCustomerId (ADR 0027 — explicit customer wins)";
    v.it(`${d} > prefers the explicit billingCustomerId over the hub-derived link`, 476, () => {
      v.check("resolveTaskCustomerId", [{ billingCustomerId: "CJSF_ID", sourceHubLinkedCustomerId: "SPX_ID", destinationLinkedCustomerId: "OTHER_ID" }], eq("CJSF_ID"));
    });
    v.it(`${d} > falls back to the hub link when no explicit customer is set`, 486, () => {
      v.check("resolveTaskCustomerId", [{ sourceHubLinkedCustomerId: "SPX_ID" }], eq("SPX_ID"));
      v.check("resolveTaskCustomerId", [{ destinationLinkedCustomerId: "DEST_ID" }], eq("DEST_ID"));
    });
    v.it(`${d} > ignores a blank billingCustomerId`, 495, () => {
      v.check("resolveTaskCustomerId", [{ billingCustomerId: "  ", sourceHubLinkedCustomerId: "SPX_ID" }], eq("SPX_ID"));
    });
  }

  {
    const cleanSupp = { jobCategory: "SUPPLEMENTARY", billingFuelAdjustmentId: null, billingRateMultiplier: 1, billingAddThbPerTrip: 0 };
    const d = "snapshotCarriesFuel / isFrozenBillingSnapshot (ADR-0005 frozen price)";
    v.it(`${d} > a snapshot with no fuel fields at all carries no fuel`, 510, () => {
      v.check("snapshotCarriesFuel", [{}], eq(false));
      v.check("snapshotCarriesFuel", [{ billingRateMultiplier: 1, billingAddThbPerTrip: 0 }], eq(false));
    });
    v.it(`${d} > any one of id / multiplier / per-trip add marks the snapshot as fuel-adjusted`, 515, () => {
      v.check("snapshotCarriesFuel", [{ billingFuelAdjustmentId: "adj1" }], eq(true));
      v.check("snapshotCarriesFuel", [{ billingRateMultiplier: 1.05 }], eq(true));
      v.check("snapshotCarriesFuel", [{ billingAddThbPerTrip: -40 }], eq(true));
      v.check("snapshotCarriesFuel", [{ billingFuelAdjustmentId: "  " }], eq(false));
    });
    v.it(`${d} > a clean เสริม price is frozen, with or without the override flag`, 523, () => {
      v.check("isFrozenBillingSnapshot", [cleanSupp], eq(true));
      v.check("isFrozenBillingSnapshot", [{ ...cleanSupp, billingManualOverride: true }], eq(true));
    });
    v.it(`${d} > a เสริม label carrying fuel is NOT frozen — it is the corrupted price a recompute must repair`, 528, () => {
      v.check("isFrozenBillingSnapshot", [{ ...cleanSupp, billingAddThbPerTrip: -40, billingFuelAdjustmentId: "adj1" }], eq(false));
      v.check("isFrozenBillingSnapshot", [{ ...cleanSupp, billingRateMultiplier: 1.1 }], eq(false));
    });
    v.it(`${d} > an explicit manual override stays frozen even when it carries fuel (an admin typed that price)`, 533, () => {
      v.check("isFrozenBillingSnapshot", [{ ...cleanSupp, billingAddThbPerTrip: -40, billingManualOverride: true }], eq(true));
      v.check("isFrozenBillingSnapshot", [{ jobCategory: "PRIMARY", billingManualOverride: true, billingRateMultiplier: 1.05 }], eq(true));
    });
    v.it(`${d} > an un-overridden หลัก snapshot is never frozen`, 538, () => {
      v.check("isFrozenBillingSnapshot", [{ jobCategory: "PRIMARY", billingRateMultiplier: 1.05 }], eq(false));
      v.check("isFrozenBillingSnapshot", [{}], eq(false));
    });
  }

  // R16: equal effective instants. Legacy keeps load order (the first loaded wins);
  // Go orders explicitly: legacy doc id ascending, then created_at, then id.
  {
    const t = Date.UTC(2026, 7, 15, 17, 0, 0);
    const later = Date.UTC(2026, 8, 1, 0, 0, 0);
    const bill = Date.UTC(2026, 8, 10, 3, 0, 0);
    const early = Date.UTC(2026, 6, 1, 3, 0, 0);
    const rate = (id, rateThb, order) => ({ id, customerId: "c", importId: `imp-${id}`, hubId: "HUB", destinationCode: "DEST", vehicleClass: "4WJ", rateThb, effectiveFromMs: t, ...order });
    v.it("R16 > equal-instant legacy rate cards: the lower legacy doc id wins, whatever the load order", 0, () => {
      const e = [rate("Zq9", 1300, { legacyDocId: "Zq9" }), rate("Ab1", 1200, { legacyDocId: "Ab1" })];
      v.diverge("selectBillingRateEntry", ["c", "HUB", "DEST", "4WJ", bill, e, null], "Ab1", "R16", { expectLegacy: eq("Zq9") });
      // Bytes, not a collation: "Z" (0x5A) sorts before "a" (0x61).
      const f = [rate("ab1", 1300, { legacyDocId: "ab1" }), rate("Zz9", 1200, { legacyDocId: "Zz9" })];
      v.diverge("selectBillingRateEntry", ["c", "HUB", "DEST", "4WJ", bill, f, null], "Zz9", "R16", { expectLegacy: eq("ab1") });
    }, "R16");
    v.it("R16 > equal-instant new rate cards: the earlier created_at wins, then the lower id", 0, () => {
      // created_at order and id order disagree (uuidv7() is taken at insert,
      // created_at = now() at transaction start), so created_at must decide.
      const e = [
        rate("0199-a", 1300, { createdAtMs: Date.UTC(2026, 8, 2) }),
        rate("0199-b", 1200, { createdAtMs: Date.UTC(2026, 8, 1) }),
      ];
      v.diverge("selectBillingRateEntry", ["c", "HUB", "DEST", "4WJ", bill, e, null], "0199-b", "R16", { expectLegacy: eq("0199-a") });
      const same = Date.UTC(2026, 8, 1);
      const f = [rate("0199-d", 1300, { createdAtMs: same }), rate("0199-c", 1200, { createdAtMs: same })];
      v.diverge("selectBillingRateEntry", ["c", "HUB", "DEST", "4WJ", bill, f, null], "0199-c", "R16", { expectLegacy: eq("0199-d") });
    }, "R16");
    v.it("R16 > equal-instant mixed rate cards: a migrated row wins over a row created in Go", 0, () => {
      const e = [rate("0199-new", 1300, { createdAtMs: Date.UTC(2020, 0, 1) }), rate("Leg1", 1200, { legacyDocId: "Leg1", createdAtMs: Date.UTC(2026, 8, 1) })];
      v.diverge("selectBillingRateEntry", ["c", "HUB", "DEST", "4WJ", bill, e, null], "Leg1", "R16", { expectLegacy: eq("0199-new") });
    }, "R16");
    v.it("R16 > the oldest-card fallback breaks equal instants the same way", 0, () => {
      const e = [rate("Zq9", 1300, { legacyDocId: "Zq9" }), rate("Ab1", 1200, { legacyDocId: "Ab1" }), { ...rate("New", 900, {}), effectiveFromMs: later }];
      v.diverge("selectBillingRateEntry", ["c", "HUB", "DEST", "4WJ", early, e, null], "Ab1", "R16", { expectLegacy: eq("Zq9") });
    }, "R16");
    v.it("R16 > fuel adjustments and standby rates use the same order", 0, () => {
      const adj = [
        { id: "Zq9", legacyDocId: "Zq9", customerId: "c", effectiveFromMs: t, rateMultiplier: 1, addThbPerTrip: -40 },
        { id: "Ab1", legacyDocId: "Ab1", customerId: "c", effectiveFromMs: t, rateMultiplier: 1, addThbPerTrip: -50 },
      ];
      v.diverge("selectFuelAdjustmentForBillingDate", ["c", bill, adj], "Ab1", "R16", { expectLegacy: eq("Zq9") });
      const sr = [
        { id: "Zq9", legacyDocId: "Zq9", customerId: "c", rateThb: 400, effectiveFromMs: t },
        { id: "Ab1", legacyDocId: "Ab1", customerId: "c", rateThb: 300, effectiveFromMs: t },
      ];
      v.diverge("selectStandbyRateEntry", ["c", bill, sr], "Ab1", "R16", { expectLegacy: eq("Zq9") });
      // Rows created in Go: the earlier created_at wins over the lower id.
      const newAdj = [
        { id: "0199-a", customerId: "c", effectiveFromMs: t, rateMultiplier: 1, addThbPerTrip: -40, createdAtMs: Date.UTC(2026, 8, 2) },
        { id: "0199-b", customerId: "c", effectiveFromMs: t, rateMultiplier: 1, addThbPerTrip: -50, createdAtMs: Date.UTC(2026, 8, 1) },
      ];
      v.diverge("selectFuelAdjustmentForBillingDate", ["c", bill, newAdj], "0199-b", "R16", { expectLegacy: eq("0199-a") });
      const newSr = [
        { id: "0199-a", customerId: "c", rateThb: 400, effectiveFromMs: t, createdAtMs: Date.UTC(2026, 8, 2) },
        { id: "0199-b", customerId: "c", rateThb: 300, effectiveFromMs: t, createdAtMs: Date.UTC(2026, 8, 1) },
      ];
      v.diverge("selectStandbyRateEntry", ["c", bill, newSr], "0199-b", "R16", { expectLegacy: eq("0199-a") });
    }, "R16");
    v.it("R20 > a voided standby rate is never selected", 0, () => {
      const sr = [
        { id: "old", customerId: "c", rateThb: 300, effectiveFromMs: Date.UTC(2026, 0, 1) },
        { id: "new", customerId: "c", rateThb: 400, effectiveFromMs: Date.UTC(2026, 6, 1), voided: true },
      ];
      v.diverge("selectStandbyRateEntry", ["c", bill, sr], "old", "R20", { expectLegacy: eq("new") });
    }, "R20");
  }

  v.it("normalizeVehicleClass > R15: whitespace is no class either", 0, () => {
    v.diverge("normalizeVehicleClass", ["   "], null, "R15", { expectLegacy: eq("4WJ") });
    v.diverge("normalizeVehicleClass", ["\u00a0\ufeff"], null, "R15", { expectLegacy: eq("4WJ") });
  }, "R15");

  v.it("computeMultiDeliveryBilling > R15: a multi-drop trip without a vehicle class is unpriced no_vehicle_class", 0, () => {
    const entries = [
      { id: "m1", customerId: "c1", importId: "imp-m", hubId: "HUBA", destinationCode: "SPK1", vehicleClass: "4WJ", rateThb: 1500, effectiveFromMs: ms("2020-01-01") },
    ];
    const task = { sourceHub: "HUBA", destination: "SPK1", sourceHubLinkedCustomerId: "c1" };
    const stops = [{ index: 1, destination: "SPK1" }, { index: 2, destination: "SPK2" }];
    for (const vc of ["", "  "]) {
      v.diverge("computeMultiDeliveryBilling", [{ deliveredTimestamp: ms("2026-08-01T10:00:00+07:00") }, task, stops, vc, entries, [], 300, null], null, "R15", {
        wantReason: "no_vehicle_class",
        expectLegacy: (g) => assert.equal(g.totalBillingThb, 1800),
      });
    }
  }, "R15");

  return v.write("billingCompute.json");
}

// ─── lib/billingRates.test.ts ────────────────────────────────────────────────

function exportBillingRates() {
  const v = vectorFile(
    "logitrack-web/lib/billingRates.test.ts",
    "5 Vitest cases ported. computeTripBilling is the web estimate; Go runs it as compute.PriceTrip with a delivered basis and no hub maps (the pure core of PriceTrip, §6.10)."
  );
  const DELIVERED = new Date("2026-09-10T05:00:00Z");
  const trip = { deliveredTimestamp: DELIVERED };
  const task = (over = {}) => ({ sourceHub: "SPK-GW", destination: "SPK890103", truckType: "4WJ", sourceHubLinkedCustomerId: "cjsf", ...over });
  const rate = (over) => ({
    id: "r", customerId: "cjsf", importId: "imp", hubId: "SPK-GW", destinationCode: "SPK890103", vehicleClass: "4WJ",
    rateThb: 1000, effectiveFromMs: ms("2026-09-01T00:00:00+07:00"), jobCategory: "PRIMARY", ...over,
  });
  const primaryCard = rate({ id: "p", rateThb: 1200, jobCategory: "PRIMARY" });
  const suppCard = rate({ id: "s", rateThb: 950, jobCategory: "SUPPLEMENTARY" });
  const fuel = [{ id: "f1", customerId: "cjsf", effectiveFromMs: ms("2026-09-01T00:00:00+07:00"), rateMultiplier: 1, addThbPerTrip: -40 }];
  const d = "computeTripBilling — หลัก/เสริม follows the task (ADR-0006)";

  v.it(`${d} > an explicit เสริม task prices from the เสริม card with NO fuel, even when a หลัก card exists`, 52, () => {
    v.check("computeTripBilling", [trip, task({ jobCategory: "SUPPLEMENTARY" }), [primaryCard, suppCard], fuel], (r) => {
      assert.equal(r.baseRateThb, 950);
      assert.equal(r.finalRateThb, 950);
      assert.equal(r.fuelAdjustmentId, undefined);
      assert.equal(r.addThbPerTrip, 0);
      assert.equal(r.rateMultiplier, 1);
    });
  });
  v.it(`${d} > an explicit หลัก task never falls back to the เสริม card`, 61, () => {
    v.check("computeTripBilling", [trip, task({ jobCategory: "PRIMARY" }), [suppCard], fuel], (r) => assert.equal(r, null));
  });
  v.it(`${d} > an explicit หลัก task prices from the หลัก card with fuel`, 65, () => {
    v.check("computeTripBilling", [trip, task({ jobCategory: "PRIMARY" }), [primaryCard, suppCard], fuel], (r) => {
      assert.equal(r.finalRateThb, 1160);
      assert.equal(r.fuelAdjustmentId, "f1");
    });
  });
  v.it(`${d} > a legacy task with no category tries หลัก first, then falls back to เสริม (no fuel)`, 71, () => {
    v.check("computeTripBilling", [trip, task(), [primaryCard, suppCard], fuel], (r) => assert.equal(r.finalRateThb, 1160));
    v.check("computeTripBilling", [trip, task(), [suppCard], fuel], (r) => {
      assert.equal(r.finalRateThb, 950);
      assert.equal(r.fuelAdjustmentId, undefined);
    });
  });
  v.it(`${d} > prices under the explicit billing customer chosen at assign (ADR 0027)`, 78, () => {
    const other = rate({ id: "o", customerId: "other", rateThb: 700 });
    v.check("computeTripBilling", [trip, task({ billingCustomerId: "other" }), [primaryCard, other], []], (r) => {
      assert.equal(r.customerId, "other");
      assert.equal(r.finalRateThb, 700);
    });
  });
  return v.write("billingRates.json");
}

// ─── lib/billingDate.test.ts ─────────────────────────────────────────────────

function exportBillingDate() {
  const v = vectorFile(
    "logitrack-web/lib/billingDate.test.ts",
    "8 Vitest cases ported to internal/platform/clock. A {\"$local\"} argument is a date picked in the host zone; Go replays it in several zones."
  );
  const eq = (want) => (got) => assert.deepStrictEqual(got, want);
  const iso = (want) => (got) => assert.equal(got?.toISOString(), want);
  const d1 = "bangkokMidnightFromDateStr";
  v.it(`${d1} > resolves a date to Bangkok midnight, i.e. 17:00Z the previous day`, 10, () => {
    v.check("bangkokMidnightFromDateStr", ["2026-08-16"], iso("2026-08-15T17:00:00.000Z"));
  });
  v.it(`${d1} > does not depend on the host timezone`, 14, () => {
    v.check("bangkokMidnightFromDateStr", ["2026-01-01"], iso("2025-12-31T17:00:00.000Z"));
  });
  v.it(`${d1} > is 7 hours earlier than the UTC-midnight form the old helper produced`, 20, () => {
    v.check("msBeforeUtcMidnight", ["2026-08-16"], eq(7 * 60 * 60 * 1000));
  });
  v.it(`${d1} > rejects anything that is not a yyyy-MM-dd date`, 26, () => {
    for (const s of ["", "16/08/2026", "2026-8-6"]) v.check("bangkokMidnightFromDateStr", [s], eq(null));
  });
  v.it("bangkokDateStr > reads an instant on the Bangkok calendar, not the UTC one", 34, () => {
    v.check("bangkokDateStr", [new Date("2026-08-15T17:00:00.000Z")], eq("2026-08-16"));
    v.check("bangkokDateStr", [new Date("2026-08-16T16:59:59.000Z")], eq("2026-08-16"));
    v.check("bangkokDateStr", [new Date("2026-08-16T17:00:00.000Z")], eq("2026-08-17"));
  });
  v.it("bangkokDateStr > round-trips with bangkokMidnightFromDateStr", 40, () => {
    for (const s of ["2026-01-01", "2026-08-16", "2026-12-31"]) v.check("bangkokDateStr(bangkokMidnightFromDateStr)", [s], eq(s));
  });
  v.it("bangkokMidnightFromPickedDate > keeps the calendar day the picker showed the admin", 48, () => {
    v.check("pickedDateToDateStr", [{ $local: "2026-08-16T00:00:00" }], eq("2026-08-16"));
    v.check("bangkokDateStr(bangkokMidnightFromPickedDate)", [{ $local: "2026-08-16T00:00:00" }], eq("2026-08-16"));
  });
  v.it("bangkokMidnightFromPickedDate > normalizes a picked date that carries a time component", 55, () => {
    v.check("bangkokMidnightFromPickedDate", [{ $local: "2026-08-16T13:45:00" }], iso("2026-08-15T17:00:00.000Z"));
  });
  v.it(`${d1} > accepts what V8 accepts: a day past the month end rolls over, month 00/13 and day 00/32 do not parse`, 0, () => {
    for (const s of ["2026-02-30", "2026-04-31", "2024-02-29", "2026-02-00", "2026-13-01", "2026-00-10", "2026-12-32", " 2026-01-01", "2026-01-01\n", "0000-01-01"])
      v.check("bangkokMidnightFromDateStr", [s]);
  }, "V8 parity");
  return v.write("billingDate.json");
}

// ─── functions/src/core/billingPeriodLock.test.ts ────────────────────────────

function exportBillingPeriodLock() {
  const v = vectorFile(
    "logitrack-web/functions/src/core/billingPeriodLock.test.ts",
    "12 Vitest cases ported to compute.PeriodKey / PeriodOf / PeriodLocks. lockFor returns the locking statement or null."
  );
  const eq = (want) => (got) => assert.deepStrictEqual(got, want);
  const lock = (over = {}) => ({ customerId: "cust_a", year: 2026, month: 7, invoiceNumber: "TTP-202607-001", status: "sent", ...over });
  const locks = [lock(), lock({ customerId: "cust_b", month: 6, invoiceNumber: "CJ-202606-002", status: "paid" })];
  v.it("billingPeriodKey > zero-pads the month so month 7 and month 70 can never collide", 25, () => {
    v.check("billingPeriodKey", ["c", 2026, 7], eq("c__2026-07"));
    v.check("billingPeriodKey", ["c", 2026, 12], eq("c__2026-12"));
  });
  v.it("billingPeriodKey > keys are per customer — the same month for two customers is two periods", 30, () => {
    v.check("billingPeriodKey", ["a", 2026, 7], eq("a__2026-07"));
    v.check("billingPeriodKey", ["b", 2026, 7], eq("b__2026-07"));
  });
  v.it("bangkokYearMonth > uses the Bangkok calendar, not UTC", 36, () => {
    v.check("bangkokYearMonth", [Date.UTC(2026, 6, 31, 17, 30)], eq({ year: 2026, month: 8 }));
  });
  v.it("bangkokYearMonth > keeps the last UTC-evening minutes of a month in the NEXT month", 43, () => {
    v.check("bangkokYearMonth", [Date.UTC(2026, 6, 31, 17, 0)], eq({ year: 2026, month: 8 }));
    v.check("bangkokYearMonth", [Date.UTC(2026, 6, 31, 16, 59)], eq({ year: 2026, month: 7 }));
  });
  v.it("bangkokYearMonth > rolls the year over at the December/January boundary", 50, () => {
    v.check("bangkokYearMonth", [Date.UTC(2026, 11, 31, 17, 0)], eq({ year: 2027, month: 1 }));
  });
  const d = "BillingPeriodLocks.lockFor";
  const july = Date.UTC(2026, 6, 15, 3, 0);
  v.it(`${d} > blocks a row whose customer and period match an issued invoice`, 61, () => {
    v.check("lockFor", [locks, "cust_a", july], (g) => assert.equal(g.invoiceNumber, "TTP-202607-001"));
  });
  v.it(`${d} > does not block a different customer in the same month`, 66, () => {
    v.check("lockFor", [locks, "cust_b", july], eq(null));
  });
  v.it(`${d} > does not block the same customer in a different month`, 70, () => {
    v.check("lockFor", [locks, "cust_a", Date.UTC(2026, 7, 15, 3, 0)], eq(null));
  });
  v.it(`${d} > treats paid the same as sent — both are documents the customer already has`, 74, () => {
    v.check("lockFor", [locks, "cust_b", Date.UTC(2026, 5, 15, 3, 0)], (g) => assert.equal(g.status, "paid"));
  });
  v.it(`${d} > never blocks when the customer or the date is unknown, so a fixable row stays fixable`, 78, () => {
    v.check("lockFor", [locks, "", july], eq(null));
    v.check("lockFor", [locks, null, july], eq(null));
    v.check("lockFor", [locks, "cust_a", 0], eq(null));
  });
  v.it(`${d} > trims the customer id, since denormalized ids arrive with stray whitespace`, 84, () => {
    v.check("lockFor", [locks, "  cust_a  ", july], (g) => assert.equal(g.invoiceNumber, "TTP-202607-001"));
  });
  v.it(`${d} > blocks a row that lands in July only after the Bangkok shift`, 90, () => {
    v.check("lockFor", [locks, "cust_a", Date.UTC(2026, 5, 30, 18, 0)], (g) => assert.equal(g.invoiceNumber, "TTP-202607-001"));
  });
  return v.write("billingPeriodLock.json");
}

// ─── lib/billingDocument.test.ts ─────────────────────────────────────────────
// billingDocument.ts imports jspdf and friends, so Node cannot load it here:
// these wants are literal and the Vitest verifier checks them against the module.

function exportBillingDocument() {
  const v = vectorFile(
    "logitrack-web/lib/billingDocument.test.ts",
    "16 Vitest cases ported to internal/billing/documents. Wants are literal (the module needs a browser bundle); lib/billingGolden.test.ts checks them against billingDocument.ts. groupToLineItems' second argument is the rows its rounds are collected from (null: no rounds argument)."
  );
  const D = (s) => new Date(s);
  const trip = (over = {}) => ({
    id: "t1", billingEstimateThb: 1200, vehicleClass: "4WJ", hubDisplayName: "SPK-GW", destinationDisplayName: "ลาดกระบัง",
    deliveredTimestamp: D("2026-08-05T03:00:00Z"), rowType: "trip", ...over,
  });
  const round = (label, date, first, last) => ({ label, effectiveFromDateStr: date, firstBillingDate: D(first), lastBillingDate: D(last) });
  const line = (over) => ({
    vehicleClass: "4WJ", route: "SPK-GW → ลาดกระบัง", count: 1, unitPrice: 1200, total: 1200,
    dates: [D("2026-08-05T03:00:00Z")], enumerateDays: false, roundLabel: "", ...over,
  });
  const T0 = "2026-08-05T03:00:00.000Z";

  v.it("collectBillingRounds > labels rounds by date order, oldest first", 31, () => {
    v.literal("collectBillingRounds", [[
      trip({ id: "a", billingRoundEffectiveFromDateStr: "2026-08-16" }),
      trip({ id: "b", billingRoundEffectiveFromDateStr: "2026-08-01" }),
      trip({ id: "c", billingRoundEffectiveFromDateStr: "2026-08-25" }),
    ]], [round("R1", "2026-08-01", T0, T0), round("R2", "2026-08-16", T0, T0), round("R3", "2026-08-25", T0, T0)]);
  });
  v.it("collectBillingRounds > ignores rows that carry no round, so legacy trips cannot invent one", 44, () => {
    v.literal("collectBillingRounds", [[trip(), trip()]], []);
  });
  v.it("collectBillingRounds > widens a round's span across its rows", 48, () => {
    v.literal("collectBillingRounds", [[
      trip({ id: "a", billingRoundEffectiveFromDateStr: "2026-08-01", deliveredTimestamp: D("2026-08-05T03:00:00Z") }),
      trip({ id: "b", billingRoundEffectiveFromDateStr: "2026-08-01", deliveredTimestamp: D("2026-08-12T03:00:00Z") }),
    ]], [round("R1", "2026-08-01", "2026-08-05T03:00:00Z", "2026-08-12T03:00:00Z")]);
  });
  v.it("collectBillingRounds > spans a plan-basis round on the plan date, not the delivery instant", 66, () => {
    v.literal("collectBillingRounds", [[
      trip({ id: "a", billingRoundEffectiveFromDateStr: "2026-08-01", deliveredTimestamp: D("2026-08-05T03:00:00Z"), billingDate: D("2026-08-04T17:00:00Z") }),
      trip({ id: "b", billingRoundEffectiveFromDateStr: "2026-08-01", deliveredTimestamp: D("2026-09-01T01:30:00Z"), billingDate: D("2026-08-30T17:00:00Z") }),
    ]], [round("R1", "2026-08-01", "2026-08-04T17:00:00Z", "2026-08-30T17:00:00Z")]);
  });

  v.it("billingAxisDate > uses the frozen billing date when the row has one", 88, () => {
    v.literal("billingAxisDate", [trip({ billingDate: D("2026-08-30T17:00:00Z") })], D("2026-08-30T17:00:00Z"));
  });
  v.it("billingAxisDate > falls back to the delivery instant for rows priced before ADR 0027 and for standby", 93, () => {
    v.literal("billingAxisDate", [trip()], D(T0));
    v.literal("billingAxisDate", [trip({ rowType: "standby" })], D(T0));
  });

  v.it("billingDateBasisOf > reads the basis stamped on the rows", 100, () => {
    v.literal("billingDateBasisOf", [[trip({ billingDateBasis: "plan" })]], "plan");
    v.literal("billingDateBasisOf", [[trip({ billingDateBasis: "delivered" })]], "delivered");
  });
  v.it("billingDateBasisOf > defaults to delivered when nothing is stamped, so no opt-out customer is relabelled", 105, () => {
    v.literal("billingDateBasisOf", [[trip(), trip()]], "delivered");
    v.literal("billingDateBasisOf", [[]], "delivered");
  });

  v.it("formatFuelBand > prints the contract's inclusive range", 112, () => {
    v.literal("formatFuelBand", [37.01, 38], "37.01–38.00");
  });
  v.it("formatFuelBand > prints a dash rather than half a range", 116, () => {
    v.literal("formatFuelBand", [null, 38], "-");
    v.literal("formatFuelBand", [37.01, null], "-");
  });

  v.it("groupToLineItems > keeps count × unitPrice = total on every line", 123, () => {
    v.literal("groupToLineItems", [[trip({ id: "a", billingEstimateThb: 1200 }), trip({ id: "b", billingEstimateThb: 1200 })], null],
      [line({ count: 2, total: 2400, dates: [D(T0), D(T0)] })]);
  });
  v.it("groupToLineItems > never merges two rounds into one line, even at an identical unit price", 133, () => {
    v.literal("groupToLineItems", [
      [trip({ id: "a", billingEstimateThb: 1200, billingRoundEffectiveFromDateStr: "2026-08-01" }), trip({ id: "b", billingEstimateThb: 1200, billingRoundEffectiveFromDateStr: "2026-08-16" })],
      [trip({ id: "a", billingRoundEffectiveFromDateStr: "2026-08-01" }), trip({ id: "b", billingRoundEffectiveFromDateStr: "2026-08-16" })],
    ], [line({ roundLabel: "R1" }), line({ roundLabel: "R2" })]);
  });
  v.it("groupToLineItems > splits a route priced differently in two rounds", 152, () => {
    const trips = [
      trip({ id: "a", billingEstimateThb: 1200, billingRoundEffectiveFromDateStr: "2026-08-01" }),
      trip({ id: "b", billingEstimateThb: 1210, billingRoundEffectiveFromDateStr: "2026-08-16" }),
    ];
    v.literal("groupToLineItems", [trips, trips], [line({ roundLabel: "R1" }), line({ unitPrice: 1210, total: 1210, roundLabel: "R2" })]);
  });
  v.it("groupToLineItems > labels a multidrop stop with its round like any other row", 162, () => {
    const trips = [trip({ id: "t1_s2", rowType: "multidrop_stop", billingEstimateThb: 300, billingRoundEffectiveFromDateStr: "2026-08-16" })];
    v.literal("groupToLineItems", [trips, trips], [line({ route: "ค่าโยก", unitPrice: 300, total: 300, roundLabel: "R1" })]);
  });
  v.it("groupToLineItems > dates a line on the billing axis, so a plan-basis invoice stays inside its month", 177, () => {
    v.literal("groupToLineItems", [[trip({ id: "a", deliveredTimestamp: D("2026-09-01T01:30:00Z"), billingDate: D("2026-08-30T17:00:00Z") })], null],
      [line({ dates: [D("2026-08-30T17:00:00Z")] })]);
  });
  v.it("groupToLineItems > leaves the round label empty when a row carries no round", 188, () => {
    v.literal("groupToLineItems", [[trip()], []], [line({})]);
  });

  v.it("groupToLineItems > origin code over lookup code over hub name; a destination name wins even when empty; standby lists its days", 0, () => {
    v.literal("groupToLineItems", [[
      trip({ id: "s1", rowType: "standby", originHubCode: "SPK-GW", billingLookupHubId: "X", destinationDisplayName: "", billingLookupDestination: "SPK1", billingEstimateThb: 500 }),
      trip({ id: "s2", rowType: "standby", originHubCode: "SPK-GW", destinationDisplayName: "", billingEstimateThb: 500, deliveredTimestamp: D("2026-08-09T03:00:00Z") }),
      trip({ id: "t2", hubDisplayName: "", billingLookupHubId: "HUBX", destinationDisplayName: undefined, billingLookupDestination: "SPK9", vehicleClass: undefined, billingEstimateThb: 0.1 + 0.2 }),
      trip({ id: "t3", hubDisplayName: "", billingLookupHubId: "HUBX", destinationDisplayName: undefined, billingLookupDestination: "SPK9", vehicleClass: undefined, billingEstimateThb: 0.30000000000000004 }),
    ], null], [
      line({ route: "SPK-GW →  (Stand by)", count: 2, unitPrice: 500, total: 1000, dates: [D(T0), D("2026-08-09T03:00:00Z")], enumerateDays: true }),
      line({ vehicleClass: "-", route: "HUBX → SPK9", count: 2, unitPrice: 0.1 + 0.2, total: 0.1 + 0.2 + 0.30000000000000004, dates: [D(T0), D(T0)] }),
    ]);
  }, "characterisation");
  v.it("formatFuelBand > uses JavaScript toFixed rounding, half up on the exact value", 0, () => {
    v.literal("formatFuelBand", [0.125, 2.675], "0.13–2.67");
    v.literal("formatFuelBand", [NaN, 38], "NaN–38.00");
  }, "characterisation");
  return v.write("billingDocument.json");
}

// ─── lib/jobCategory.test.ts ─────────────────────────────────────────────────
// jobCategory.ts imports "@/validate/taskSchema" (a path alias Node cannot
// resolve without the web toolchain): these wants are literal, copied from the
// Vitest assertions, and the Vitest verifier checks them against the module.

function exportJobCategory() {
  const v = vectorFile(
    "logitrack-web/lib/jobCategory.test.ts",
    "9 Vitest cases ported to billing/compute (JobCategoryFromCell, ResolveDisplayJobCategory), used by the task import (T31), the rate-card write (T37, §6.12) and the rows APIs. Wants are literal (the module imports a path alias); lib/billingGolden.test.ts checks them against jobCategory.ts. null is an absent cell or value (the Vitest undefined and null both encode as null)."
  );
  const P = "PRIMARY";
  const S = "SUPPLEMENTARY";
  const cell = "jobCategoryFromCell";
  const show = "resolveDisplayJobCategory";

  v.it("jobCategoryFromCell > reads the Thai words admins actually type", 5, () => {
    v.literal(cell, ["หลัก"], P);
    v.literal(cell, ["เสริม"], S);
    v.literal(cell, ["งานเสริม"], S);
  });
  v.it("jobCategoryFromCell > reads the English words and the enum itself", 11, () => {
    v.literal(cell, ["Primary"], P);
    v.literal(cell, ["supplementary"], S);
    v.literal(cell, ["SUPPLEMENTARY"], S);
  });
  v.it("jobCategoryFromCell > tolerates surrounding whitespace", 17, () => {
    v.literal(cell, ["  เสริม "], S);
  });
  v.it("jobCategoryFromCell > defaults a blank cell to PRIMARY", 21, () => {
    v.literal(cell, [""], P);
    v.literal(cell, [undefined], P);
    v.literal(cell, [null], P);
  });
  v.it("jobCategoryFromCell > returns undefined rather than guessing", 27, () => {
    v.literal(cell, ["เสิรม"], null);
    v.literal(cell, ["secondary"], null);
  });
  v.it("resolveDisplayJobCategory (ADR 0010 R2) > prefers the trip's own value over the task", 35, () => {
    v.literal(show, [S, P], S);
    v.literal(show, [P, S], P);
  });
  v.it("resolveDisplayJobCategory (ADR 0010 R2) > falls back to the task when the trip cache was never written", 41, () => {
    v.literal(show, [undefined, S], S);
    v.literal(show, [null, P], P);
  });
  v.it("resolveDisplayJobCategory (ADR 0010 R2) > returns undefined when neither side carries a value", 47, () => {
    v.literal(show, [undefined, undefined], null);
    v.literal(show, [undefined], null);
  });
  v.it("resolveDisplayJobCategory (ADR 0010 R2) > does not coerce near-miss spellings from either side", 53, () => {
    v.literal(show, ["primary"], null);
    v.literal(show, ["", "supplementary"], null);
    v.literal(show, ["เสริม"], null);
  });

  // String.prototype.trim / toLowerCase on spreadsheet-shaped cells.
  v.it("jobCategoryFromCell > JavaScript trim and toLowerCase on spreadsheet text", 0, () => {
    v.literal(cell, ["\ufeffเสริม\u00a0"], S); // BOM and NBSP are JS whitespace
    v.literal(cell, ["\u3000งานหลัก\u2003"], P);
    v.literal(cell, ["\t\n\u00a0"], P); // blank after trim
    v.literal(cell, ["\u0085หลัก"], null); // U+0085 is not JS whitespace
    v.literal(cell, ["\u200bเสริม"], null); // nor is U+200B
    v.literal(cell, ["SUPPLEMENT"], S);
    v.literal(cell, ["PRİMARY"], null); // İ lower-cases to "i\u0307", not "i"
    v.literal(cell, ["งาน เสริม"], null);
    v.literal(cell, ["2"], null);
  }, "characterisation");
  v.it("resolveDisplayJobCategory > exact enum values only, trip first", 0, () => {
    v.literal(show, [" PRIMARY", null], null);
    v.literal(show, ["SUPPLEMENTARY ", P], P);
    v.literal(show, ["", S], S);
    v.literal(show, ["bogus", "bogus"], null);
  }, "characterisation");
  return v.write("jobCategory.json");
}

// ─── characterisation: seeded random inputs, want = TypeScript output ────────

function mulberry32(seed) {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function exportCharacterisation() {
  const rnd = mulberry32(0x7360);
  const pick = (xs) => xs[Math.floor(rnd() * xs.length)];
  const int = (lo, hi) => lo + Math.floor(rnd() * (hi - lo + 1));
  const dp = (lo, hi, places) => Math.round((lo + rnd() * (hi - lo)) * 10 ** places) / 10 ** places;
  const v = vectorFile(
    "logitrack-web/lib/billingCompute.ts (untested paths), JavaScript number semantics",
    "Characterisation vectors (§6.16): seeded random and curated inputs, want = the TypeScript output. Regenerate with export.mjs; never edit by hand."
  );

  v.it("Math.round: ties toward +Infinity, signed zero", 0, () => {
    const xs = [0, -0, 0.5, -0.5, 1.5, -1.5, 2.5, -2.5, 0.49999999999999994, -0.49999999999999994, 0.4, -0.4, 4503599627370495.5, -4503599627370495.5,
      4503599627370497, 1e21, -1e21, Number.MIN_VALUE, -Number.MIN_VALUE, NaN, Infinity, -Infinity, 123456.5, -123456.5, 1199.9999999999998, 116000.00000000001];
    for (let i = 0; i < 160; i++) xs.push(int(-2000000, 2000000) / 2, dp(-5000, 5000, 3) * 100, (rnd() - 0.5) * 1e6);
    for (const x of xs) v.check("Math.round", [x]);
  }, "characterisation");

  v.it("round2: Math.round(x * 100) / 100", 0, () => {
    const xs = [1.005, 2.675, -2.675, 1.115, 0.125, -0.125, 0.005, -0.005, -0.004, 8.345, 1159.9999999999998, -39.995, 1e15 + 0.125];
    for (let i = 0; i < 300; i++) xs.push(dp(-3000, 30000, int(2, 5)), int(-500000, 500000) / 200);
    for (const x of xs) v.check("round2", [x]);
  }, "characterisation");

  v.it("toFixed: exact decimal value, half up on the magnitude", 0, () => {
    const xs = [0.125, 0.375, 1.005, 2.675, 8.345, -0.001, -0, 0, 1e21, -1.5e21, 999.995, 0.5, 2.5, -2.5, 1.45, 37.01, 38, 41.01, NaN, Infinity, 123456789.125];
    for (let i = 0; i < 160; i++) xs.push(dp(-10000, 10000, int(0, 6)), int(0, 100000) / 8);
    for (const x of xs) v.check("toFixed", [x, pick([0, 1, 2, 2, 2, 3])]);
  }, "characterisation");

  v.it("withholdingThb: Round2(total × rate)", 0, () => {
    for (let i = 0; i < 160; i++) v.check("withholdingThb", [dp(0, 2000000, 2), pick([0.01, 0.03, 0.015, 0.05, 0.02])]);
    for (const t of [0.5, 50, 150, 1250.5, 333.33, 1050, 99999.99]) v.check("withholdingThb", [t, 0.01]);
  }, "characterisation");

  v.it("computeFinalRateThb: Round2(base × mult + add) with no fused multiply-add", 0, () => {
    const mults = [1, 1.05, 0.95, 1.1, 1.055, 0.97, 1.01, 1.025, 1.15, 0.9];
    const adds = [0, -40, -50, -70, 10, 20, 25.5, 12.345, 0.005, -0.005];
    for (let i = 0; i < 900; i++) {
      const base = pick([int(100, 30000), dp(100, 30000, 2), dp(0, 5000, 2), int(1, 50) * 50]);
      const mult = rnd() < 0.6 ? pick(mults) : dp(0.8, 1.25, int(2, 6));
      const add = rnd() < 0.6 ? pick(adds) : dp(-200, 200, int(0, 3));
      v.check("computeFinalRateThb", [base, mult, add]);
    }
  }, "characterisation");

  v.it("fuelBandFloor / fuelBandRange / computeFuelSurchargeThb", 0, () => {
    const prices = [29.94, 30, 30.01, 31.99, 32, 35.5, 36.01, 41, 41.005, 41.004, 42, 42.005, -1.5, -0, 0, 0.001, 1e10, NaN, -Infinity];
    for (let i = 0; i < 200; i++) prices.push(dp(25, 50, 2), dp(25, 50, 3), dp(25, 50, 6));
    for (const p of prices) {
      v.check("fuelBandFloor", [p]);
      v.check("fuelBandRange", [p]);
    }
    for (let i = 0; i < 300; i++) v.check("computeFuelSurchargeThb", [dp(25, 50, int(2, 3)), pick([36, 38, 40, 41, 43]), pick([10, 5, 7.5, 12.34, 0.1])]);
    for (const k of [[42, NaN, 10], [42, 41, Infinity], [NaN, 41, 10]]) v.check("computeFuelSurchargeThb", k);
  }, "characterisation");

  const Y = (y, m, d, h = 0, mi = 0, s = 0) => Date.UTC(y, m - 1, d, h, mi, s);
  v.it("bangkokDateStrFromMillis / isEffectiveOnOrBeforeBillingDate", 0, () => {
    const fixed = [0, 1, -1, Y(2026, 8, 15, 16, 59, 59), Y(2026, 8, 15, 17), Y(2025, 12, 31, 17), Y(2025, 12, 31, 16, 59, 59), Y(2028, 2, 28, 17), Y(1999, 12, 31, 17)];
    for (const t of fixed) v.check("bangkokDateStrFromMillis", [t]);
    for (let i = 0; i < 120; i++) v.check("bangkokDateStrFromMillis", [int(Y(2020, 1, 1), Y(2030, 1, 1))]);
    for (let i = 0; i < 160; i++) {
      const day = Y(2026, int(1, 12), int(1, 28));
      const e = day + pick([-7, 0, 7, 17, 24]) * 3600000 + pick([0, 0, 59 * 60000]);
      v.check("isEffectiveOnOrBeforeBillingDate", [e, e + int(-36, 36) * 3600000 + int(-59, 59) * 60000]);
    }
  }, "characterisation");

  v.it("extractHubId / normalizeDestinationCode / resolveTaskCustomerId on spreadsheet-shaped text", 0, () => {
    const texts = ["HUBA - Name", " huba ", "SPK-GW", "J&T EXPRESS บางปู", "SPK890103 - ลาดกระบัง26", "SPK890103-ลาดกระบัง26", "spk890103 -ลาดกระบัง", "-SPK", "--", "SOCE", "soce-1",
      "SOCN 2", "SOCW-north", "SOCX-1", "ห้วยขวาง10", "\u00a0SPK1\u00a0", "\ufeffSPK2", "\u0085SPK3", "\u2003HUB - x", "HUB -x", "HUB - ", " - HUB", "", "   ", "a - b - c", "Straße", "ŝpk-1", "ǅ-x"];
    for (const t of texts) {
      v.check("extractHubId", [t]);
      v.check("normalizeDestinationCode", [t]);
    }
    for (const task of [
      { billingCustomerId: "\u00a0c1 " }, { billingCustomerId: "\ufeff", sourceHubLinkedCustomerId: "c2" }, { sourceHubLinkedCustomerId: "\u0085" },
      { billingCustomerId: "", sourceHubLinkedCustomerId: " ", destinationLinkedCustomerId: " c3" }, {},
    ]) v.check("resolveTaskCustomerId", [task]);
  }, "characterisation");

  v.it("normalizeVehicleClass on stored spellings", 0, () => {
    for (const s of ["pickup", " 6 wheels ", "4 Wheels", "4wj", "van", "2 Wheels", "18W", "trailer", "6 WHEELS JUMBO", "\u00a06W"]) v.check("normalizeVehicleClass", [s]);
  }, "characterisation");

  // Random rate tables: unique instants per scenario, so legacy load order never decides (R16).
  const customers = ["c1", "c2"];
  const hubs = ["HUBA", "SPK-GW", "HUB-B"];
  const dests = ["SOCE", "SPK890103", "DEST1", "WANG"];
  const classes = ["4WJ", "6WH", "4W", "10WH", "Pickup", "6 Wheels", "10W"];
  const instantPool = () => {
    const pool = new Set();
    while (pool.size < 16) {
      const day = Y(int(2025, 2026), int(1, 12), int(1, 28));
      pool.add(day + pick([-7 * 3600000, 0, 3600000 * int(1, 20) + int(0, 59) * 60000]));
    }
    return [...pool];
  };
  const rateTable = (pool, n) =>
    Array.from({ length: n }, (_, i) => ({
      id: `r${i}`, customerId: pick(customers), importId: `rc_${i}`, hubId: pick(hubs), destinationCode: pick(dests), vehicleClass: pick(classes),
      rateThb: pick([int(500, 3000), dp(500, 3000, 2), 0]), effectiveFromMs: pool.pop(),
      ...(rnd() < 0.3 ? { jobCategory: pick(["PRIMARY", "SUPPLEMENTARY"]) } : {}), ...(rnd() < 0.15 ? { voided: true } : {}),
    }));
  const fuelTable = (pool, n) =>
    Array.from({ length: n }, (_, i) => ({
      id: `f${i}`, customerId: pick(customers), effectiveFromMs: pool.pop(),
      rateMultiplier: pick([1, 1, 1.05, 0.97, dp(0.9, 1.1, 4)]), addThbPerTrip: pick([0, -40, -50, -70, 25.5, dp(-100, 100, 2)]),
      ...(rnd() < 0.6 ? { referenceFuelPriceThb: pick([dp(30, 45, 2), 42, 41.01]) } : {}), ...(rnd() < 0.1 ? { voided: true } : {}),
    }));
  const bill = () => Y(int(2024, 2027), int(1, 12), int(1, 28), int(0, 23), int(0, 59));
  const cat = () => pick([null, "PRIMARY", "SUPPLEMENTARY"]);

  v.it("selectBillingRateEntry / selectFuelAdjustmentForBillingDate / selectStandbyRateEntry", 0, () => {
    for (let i = 0; i < 90; i++) {
      const pool = instantPool();
      const e = rateTable(pool, int(1, 7));
      const pivot = pick(e);
      v.check("selectBillingRateEntry", [pick(customers), pivot.hubId, pivot.destinationCode, pick(classes), bill(), e, cat()]);
      v.check("selectBillingRateEntry", [pivot.customerId, pivot.hubId, pivot.destinationCode, pivot.vehicleClass, bill(), e, pivot.jobCategory ?? null]);
      v.check("selectFuelAdjustmentForBillingDate", [pick(customers), bill(), fuelTable(pool, int(0, 4))]);
      const sr = Array.from({ length: int(0, 3) }, (_, k) => ({ id: `s${k}`, customerId: pick(customers), rateThb: pick([300, 400, 450.5]), effectiveFromMs: pool.pop() }));
      v.check("selectStandbyRateEntry", [pick(customers), bill(), sr]);
      v.check("computeStandbyBilling", [bill(), pick(["c1", "c2", ""]), sr]);
    }
  }, "characterisation");

  v.it("resolveBillingRoundProvenance", 0, () => {
    for (let i = 0; i < 60; i++) {
      const pool = instantPool();
      const adj = rnd() < 0.25 ? null : fuelTable(pool, 1)[0];
      v.check("resolveBillingRoundProvenance", [pool.pop(), adj]);
    }
  }, "characterisation");

  const srcForms = (h) => pick([h, `${h} - ชื่อ`, ` ${h.toLowerCase()} `]);
  const destForms = (d) => pick([d, `${d} - ลาดกระบัง26`, `${d.toLowerCase()}`, d === "SOCE" ? "SOCE-1" : d]);
  const taskFor = (pivot) => ({
    sourceHub: srcForms(pivot.hubId),
    destination: destForms(pivot.destinationCode),
    truckType: pick([pivot.vehicleClass, pick(classes)]),
    ...(rnd() < 0.3 ? { billingCustomerId: pick(["", " ", pivot.customerId, "c2"]) } : {}),
    sourceHubLinkedCustomerId: pick([pivot.customerId, "", "c2"]),
    ...(rnd() < 0.3 ? { destinationLinkedCustomerId: pick(customers) } : {}),
  });
  const tripTimes = () => {
    const t = {};
    if (rnd() < 0.85) t.deliveredTimestamp = bill();
    if (rnd() < 0.5) t.createdAt = bill();
    if (rnd() < 0.25) t.billingDateMs = pick([bill(), 0]);
    if (t.deliveredTimestamp === undefined && t.createdAt === undefined && !t.billingDateMs) t.createdAt = bill();
    return t;
  };

  v.it("computeTripBillingFromParts", 0, () => {
    for (let i = 0; i < 140; i++) {
      const pool = instantPool();
      const e = rateTable(pool, int(1, 7));
      v.check("computeTripBillingFromParts", [tripTimes(), taskFor(pick(e)), e, fuelTable(pool, int(0, 4)), cat()]);
    }
  }, "characterisation");

  v.it("computeMultiDeliveryBilling: flat fee and legacy per-route modes", 0, () => {
    const card = (id, dest, rateThb, effectiveFromMs, over = {}) => ({ id, customerId: "c1", importId: `imp-${id}`, hubId: "HUBA", destinationCode: dest, vehicleClass: "4WJ", rateThb, effectiveFromMs, ...over });
    const cards = [card("a", "SPK1", 1500, Y(2026, 1, 1)), card("b", "SPK2", 1700, Y(2026, 1, 1)), card("c", "SPK3", 900, Y(2026, 1, 1)), card("z", "SPK4", 0, Y(2026, 1, 1))];
    const fuel = [{ id: "f", customerId: "c1", effectiveFromMs: Y(2026, 6, 1), rateMultiplier: 1.05, addThbPerTrip: -40, referenceFuelPriceThb: 36.5 }];
    const trip = { deliveredTimestamp: Y(2026, 8, 3, 4) };
    const task = (destination) => ({ sourceHub: "HUBA - Hub A", destination, sourceHubLinkedCustomerId: "c1" });
    const S = (...ds) => ds.map((destination, i) => ({ index: i + 1, destination }));
    const curated = [
      [task("SPK2"), S("SPK1", "SPK2", "SPK3"), 300], // planned among delivered, not first
      [task("SPK2 - ชื่อ"), S("spk1", "SPK2-x"), 300], // normalised match
      [task("SPK9"), S("SPK1", "SPK2"), 300], // planned not delivered, no card -> first stop with a card
      [task("SPK3"), S("SPK1", "SPK2"), 250.5], // planned not delivered but priced -> base, stop 0 excluded
      [task("NONE"), S("X1", "X2"), 300], // no card anywhere
      [task("SPK1"), S("SPK1", "SPK1", "SPK1"), 0], // zero fee
      [task("SPK1"), S("SPK1", "SPK2", "SPK3"), null], // legacy per-route
      [task("SPK1"), S("X1", "SPK2", "SPK3"), null], // legacy: first stop unmatched -> null
      [task("SPK1"), S("SPK1", "X2", "SPK3"), null], // legacy: unmatched extra stop is free
      [task("SPK4"), S("SPK4", "SPK1"), 300], // base of 0 -> null
      [task("SPK1"), S("SPK1"), 300], // fewer than two stops
      [task("SPK1"), S("SPK1", "SPK2"), -1], // negative fee -> legacy mode
    ];
    for (const [t, stops, fee] of curated) {
      for (const c of [null, "SUPPLEMENTARY"]) v.check("computeMultiDeliveryBilling", [trip, t, stops, "4WJ", cards, fuel, fee, c]);
    }
    for (let i = 0; i < 110; i++) {
      const pool = instantPool();
      const e = rateTable(pool, int(2, 8));
      const pivot = pick(e);
      const stops = S(...Array.from({ length: int(2, 4) }, () => destForms(pick(dests))));
      v.check("computeMultiDeliveryBilling", [tripTimes(), taskFor(pivot), stops, pick(classes), e, fuelTable(pool, int(0, 3)), pick([null, 300, 0, 450.25]), cat()]);
    }
  }, "characterisation");

  v.it("snapshotCarriesFuel / isFrozenBillingSnapshot on odd stored values", 0, () => {
    const snaps = [
      { billingRateMultiplier: NaN }, { billingAddThbPerTrip: NaN }, { billingRateMultiplier: Infinity }, { billingAddThbPerTrip: -0 },
      { billingRateMultiplier: 1.0000001 }, { billingFuelAdjustmentId: "\ufeff" }, { billingFuelAdjustmentId: "\u00a0x" },
      { jobCategory: "SUPPLEMENTARY" }, { jobCategory: "SUPPLEMENTARY", billingRateMultiplier: NaN }, { jobCategory: "supplementary" },
      { jobCategory: "SUPPLEMENTARY", billingManualOverride: false, billingAddThbPerTrip: 0.01 },
    ];
    for (const s of snaps) {
      v.check("snapshotCarriesFuel", [s]);
      v.check("isFrozenBillingSnapshot", [s]);
    }
  }, "characterisation");

  return v.write("characterisation.json", { compact: true });
}

// ─── main ────────────────────────────────────────────────────────────────────

const lcSrc = readFileSync(web("lib/billingCompute.ts"), "utf8").split("\n");
const fnSrc = readFileSync(web("functions/src/core/billingCompute.ts"), "utf8").split("\n");
lcSrc.splice(2, 1);
fnSrc.splice(2, 1);
assert.deepStrictEqual(fnSrc, lcSrc, "lib/billingCompute.ts and functions/src/core/billingCompute.ts must differ only in line 3");

const files = [exportBillingCompute(), exportBillingRates(), exportBillingDate(), exportBillingPeriodLock(), exportBillingDocument(), exportJobCategory(), exportCharacterisation()];
for (const f of files) {
  const ported = f.cases.filter((c) => !c.added).length;
  const checks = f.cases.reduce((n, c) => n + c.checks.length, 0);
  console.log(`${f.source}: ${ported} ported cases, ${f.cases.length - ported} added, ${checks} checks`);
}
