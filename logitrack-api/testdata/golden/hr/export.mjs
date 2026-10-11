// Exports the compensation golden vectors from the TypeScript engine (main spec §6.16, T44).
//
//   node testdata/golden/hr/export.mjs            (from logitrack-api/, Node >= 22.18)
//
// Node strips the types of the imported .ts file itself; no install is needed.
// Ported cases re-run the assertions of their Vitest `it` block
// (logitrack-web/lib/compensationCompute.test.ts) against the TypeScript output
// before storing it as `want`, so a vector can only hold what the legacy test
// accepts. characterisation.json pins the same functions on seeded random
// inputs. logitrack-web/lib/compensationGolden.test.ts re-checks every vector
// against the TypeScript module on each `pnpm test`; the Go tests
// (internal/hr/compute/golden_test.go) check them against Go.
import assert from "node:assert/strict";
import { readFileSync, writeFileSync } from "node:fs";
import { dec, enc } from "../billing/codec.mjs";

const web = (p) => new URL(`../../../../logitrack-web/${p}`, import.meta.url);
const cc = await import(web("lib/compensationCompute.ts"));

const COMMIT = "4f552099";
const out = (name) => new URL(name, import.meta.url);

// web:lib/compensationCompute.ts and fn:core/compensationCompute.ts are one engine (§6.1): they may differ in their
// header line 4 only.
{
  const a = readFileSync(web("lib/compensationCompute.ts"), "utf8").split("\n");
  const b = readFileSync(web("functions/src/core/compensationCompute.ts"), "utf8").split("\n");
  assert.equal(a.length, b.length, "the two compensationCompute.ts copies differ in length");
  const diff = a.flatMap((l, i) => (l === b[i] ? [] : [i + 1]));
  assert.deepEqual(diff, [4], `the two compensationCompute.ts copies differ in lines ${diff.join(", ")}`);
}

// ─── vector file builder ─────────────────────────────────────────────────────

function vectorFile(source, note) {
  const file = { source, sourceCommit: COMMIT, note, cases: [] };
  let current = null;
  return {
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
    write(name) {
      writeFileSync(out(name), JSON.stringify(file, null, 2) + "\n");
      return file;
    },
    writeCompact(name) {
      const head = JSON.stringify({ ...file, cases: undefined }).slice(0, -1);
      const cases = file.cases.map((c) => {
        const checks = c.checks.map((k) => "      " + JSON.stringify(k)).join(",\n");
        const meta = JSON.stringify({ ...c, checks: undefined }).slice(0, -1);
        return `    ${meta},"checks":[\n${checks}\n    ]}`;
      });
      writeFileSync(out(name), `${head},"cases":[\n${cases.join(",\n")}\n]}\n`);
      return file;
    },
  };
}

// ─── TypeScript entry points by vector name ──────────────────────────────────
// Kept identical to the Vitest verifier's table; the Go table maps the same
// names onto the Go API (internal/hr/compute/golden_test.go).
const holidayFn = (h) => (h && h.never ? () => false : cc.makeHolidayChecker(new Set(h ? h.keys : [])));
const CALL = {
  roundTHB: (n) => cc.roundTHB(n),
  assignRound: (d) => cc.assignRound(d),
  bangkokDateKey: (d) => cc.bangkokDateKey(d),
  isHoliday: (keys, d) => cc.makeHolidayChecker(new Set(keys))(d),
  computeBasePay: (trips, cfg, holiday) => cc.computeBasePay(trips, cfg, holidayFn(holiday)),
  computeFuelIncentive: (km, refuels, tiers, min) => cc.computeFuelIncentive(km, refuels, tiers, min),
  computeTripVolumeIncentive: (n, tiers) => cc.computeTripVolumeIncentive(n, tiers),
  computeSso: (driver, rule, asOf) => cc.computeSso(driver, rule, asOf),
  computeHelperPay: (n, rate) => cc.computeHelperPay(n, rate),
  helperWindowKey: (d) => cc.helperWindowKey(d),
  eligibleHelperDayKeys: (keys, delivered) => cc.eligibleHelperDayKeys(keys, delivered),
  ageYearsAt: (birth, asOf) => cc.ageYearsAt(birth, asOf),
  applyDeductions: (earnings, sso, penalties) => cc.applyDeductions(earnings, sso, penalties),
};

// ─── compensationCompute.test.ts (29 `it`) ────────────────────────────────────

const SSO_RULE = { ratePercent: 5, baseExistingThb: 15000, baseNewThb: 12000, existingHiredBeforeYear: 2026, maxAgeInclusive: 55 };
const ASOF = new Date("2026-06-30T12:00:00+07:00");
const FUEL_TIERS = [
  { min: 10, amountThb: 1000 },
  { min: 11, amountThb: 1100 },
  { min: 12, amountThb: 1200 },
  { min: 13, amountThb: 1400 },
  { min: 14, amountThb: 1800 },
];
const TRIP_TIERS = [
  { min: 50, amountThb: 1000 },
  { min: 60, amountThb: 1500 },
  { min: 70, amountThb: 2000 },
];
const d = (s) => new Date(s);
const bkk = (y, m1, day, h, min = 0) => new Date(Date.UTC(y, m1 - 1, day, h, min, 0) - 7 * 3600000);

const v = vectorFile(
  "logitrack-web/lib/compensationCompute.test.ts",
  "Ported from the Vitest file (one case per `it`); want = the TypeScript output after the test's assertions accepted it. Regenerate with export.mjs; never edit by hand.",
);

v.it("roundTHB > rounds half-up to whole baht", 42, () => {
  v.check("roundTHB", [949.5], (r) => assert.equal(r, 950));
  v.check("roundTHB", [949.4], (r) => assert.equal(r, 949));
  v.check("roundTHB", [950], (r) => assert.equal(r, 950));
});

v.it("assignRound > days 1-15 are R1, 16-end are R2 (Bangkok)", 50, () => {
  v.check("assignRound", [d("2026-06-15T23:00:00+07:00")], (r) => assert.equal(r, "R1"));
  v.check("assignRound", [d("2026-06-16T00:30:00+07:00")], (r) => assert.equal(r, "R2"));
  v.check("assignRound", [d("2026-06-01T08:00:00+07:00")], (r) => assert.equal(r, "R1"));
  v.check("assignRound", [d("2026-06-30T08:00:00+07:00")], (r) => assert.equal(r, "R2"));
});

const HOL = ["2026-06-16"];
v.it("makeHolidayChecker > treats Sunday as holiday", 60, () => {
  v.check("isHoliday", [HOL, d("2026-01-04T10:00:00+07:00")], (r) => assert.equal(r, true));
});
v.it("makeHolidayChecker > treats listed public holidays as holiday", 64, () => {
  v.check("isHoliday", [HOL, d("2026-06-16T10:00:00+07:00")], (r) => assert.equal(r, true));
});
v.it("makeHolidayChecker > treats a normal weekday as non-holiday", 67, () => {
  v.check("isHoliday", [HOL, d("2026-06-17T10:00:00+07:00")], (r) => assert.equal(r, false));
});

const BP_CFG = { weekdayRateThb: 300, holidayRateThb: 350, payStandby: false };
const BP_TRIPS = [
  { deliveredAt: d("2026-06-02T08:00:00+07:00"), isStandby: false, isMultiStop: false },
  { deliveredAt: d("2026-06-03T08:00:00+07:00"), isStandby: false, isMultiStop: false },
  { deliveredAt: d("2026-06-04T08:00:00+07:00"), isStandby: false, isMultiStop: false },
  { deliveredAt: d("2026-06-05T08:00:00+07:00"), isStandby: true, isMultiStop: false },
  { deliveredAt: d("2026-06-06T08:00:00+07:00"), isStandby: false, isMultiStop: true },
];
v.it("computeBasePay > excludes standby + multi-stop, splits weekday/holiday, sums via roundTHB", 83, () => {
  v.check("computeBasePay", [BP_TRIPS, BP_CFG, { keys: ["2026-06-04"] }], (r) => {
    assert.equal(r.weekdayTrips, 2);
    assert.equal(r.holidayTrips, 1);
    assert.equal(r.excludedTrips, 2);
    assert.equal(r.basePayThb, 2 * 300 + 1 * 350);
  });
});
v.it("computeBasePay > includes standby when payStandby is true", 92, () => {
  v.check("computeBasePay", [BP_TRIPS, { ...BP_CFG, payStandby: true }, { never: true }], (r) => {
    assert.equal(r.weekdayTrips, 4);
    assert.equal(r.holidayTrips, 0);
    assert.equal(r.excludedTrips, 1);
    assert.equal(r.basePayThb, 4 * 300);
  });
});

v.it("computeFuelIncentive > maps km/L to the highest reached tier when eligible", 103, () => {
  v.check("computeFuelIncentive", [10, 6, FUEL_TIERS, 5], (r) => assert.equal(r.amountThb, 1000));
  v.check("computeFuelIncentive", [13.9, 6, FUEL_TIERS, 5], (r) => assert.equal(r.amountThb, 1400));
  v.check("computeFuelIncentive", [14, 6, FUEL_TIERS, 5], (r) => assert.equal(r.amountThb, 1800));
  v.check("computeFuelIncentive", [20, 6, FUEL_TIERS, 5], (r) => assert.equal(r.amountThb, 1800));
});
v.it("computeFuelIncentive > returns 0 below the lowest tier", 109, () => {
  v.check("computeFuelIncentive", [9.9, 6, FUEL_TIERS, 5], (r) => assert.equal(r.amountThb, 0));
});
v.it("computeFuelIncentive > gates the whole incentive on > minRefuels", 112, () => {
  v.check("computeFuelIncentive", [14, 5, FUEL_TIERS, 5], (r) => assert.deepEqual(r, { amountThb: 0, eligible: false }));
  v.check("computeFuelIncentive", [14, 6, FUEL_TIERS, 5], (r) => assert.equal(r.eligible, true));
});
v.it("computeFuelIncentive > returns 0 when km/L is null", 116, () => {
  v.check("computeFuelIncentive", [null, 10, FUEL_TIERS, 5], (r) => assert.deepEqual(r, { amountThb: 0, eligible: false }));
});

v.it("computeTripVolumeIncentive > uses the highest reached tier only (not additive)", 122, () => {
  for (const [n, want] of [[49, 0], [50, 1000], [65, 1500], [70, 2000], [100, 2000]]) {
    v.check("computeTripVolumeIncentive", [n, TRIP_TIERS], (r) => assert.equal(r, want));
  }
});

v.it("computeSso > is 0 while in probation", 133, () => {
  v.check("computeSso", [{ hireDate: d("2024-01-01T00:00:00+07:00"), birthDate: null, probationPassed: false }, SSO_RULE, ASOF],
    (r) => assert.equal(r, 0));
});
v.it("computeSso > existing (hired before cutoff) uses 15000 base @5% = 750", 136, () => {
  v.check("computeSso", [{ hireDate: d("2025-05-01T00:00:00+07:00"), birthDate: d("1990-01-01T00:00:00+07:00"), probationPassed: true }, SSO_RULE, ASOF],
    (r) => assert.equal(r, 750));
});
v.it("computeSso > new (hired in/after cutoff) uses 12000 base @5% = 600", 139, () => {
  v.check("computeSso", [{ hireDate: d("2026-02-01T00:00:00+07:00"), birthDate: d("1990-01-01T00:00:00+07:00"), probationPassed: true }, SSO_RULE, ASOF],
    (r) => assert.equal(r, 600));
});
v.it("computeSso > is 0 when older than maxAgeInclusive", 142, () => {
  v.check("computeSso", [{ hireDate: d("2025-01-01T00:00:00+07:00"), birthDate: d("1969-01-01T00:00:00+07:00"), probationPassed: true }, SSO_RULE, ASOF],
    (r) => assert.equal(r, 0));
});
v.it("computeSso > missing hireDate is treated as existing", 145, () => {
  v.check("computeSso", [{ hireDate: null, birthDate: null, probationPassed: true }, SSO_RULE, ASOF], (r) => assert.equal(r, 750));
});

v.it("computeHelperPay > multiplies eligible helper days by the daily rate", 151, () => {
  v.check("computeHelperPay", [3, 400], (r) => assert.equal(r, 1200));
  v.check("computeHelperPay", [0, 400], (r) => assert.equal(r, 0));
  v.check("computeHelperPay", [-2, 400], (r) => assert.equal(r, 0));
});

v.it("helperWindowKey > keys the window 12:00 D → 11:59 D+1 to day D", 163, () => {
  v.check("helperWindowKey", [bkk(2026, 6, 15, 12, 0)], (r) => assert.equal(r, "2026-06-15"));
  v.check("helperWindowKey", [bkk(2026, 6, 15, 13, 0)], (r) => assert.equal(r, "2026-06-15"));
  v.check("helperWindowKey", [bkk(2026, 6, 16, 2, 0)], (r) => assert.equal(r, "2026-06-15"));
  v.check("helperWindowKey", [bkk(2026, 6, 16, 11, 59)], (r) => assert.equal(r, "2026-06-15"));
});
v.it("helperWindowKey > rolls to the next window at exactly 12:00", 169, () => {
  v.check("helperWindowKey", [bkk(2026, 6, 16, 12, 0)], (r) => assert.equal(r, "2026-06-16"));
});
v.it("helperWindowKey > an 11:00 check-in on the 16th belongs to the 15th (R1, not R2)", 172, () => {
  v.check("helperWindowKey", [bkk(2026, 6, 16, 11, 0)], (r) => assert.equal(r, "2026-06-15"));
});

v.it("eligibleHelperDayKeys > counts one helper-day per window key (dedupes multiple helper tasks same window)", 178, () => {
  v.check("eligibleHelperDayKeys", [["2026-06-02", "2026-06-02", "2026-06-03"], []],
    (r) => assert.deepEqual([...r].sort(), ["2026-06-02", "2026-06-03"]));
});
v.it("eligibleHelperDayKeys > excludes days the driver had a delivered trip (driving days pay as trips)", 182, () => {
  v.check("eligibleHelperDayKeys", [["2026-06-02", "2026-06-03"], ["2026-06-03"]], (r) => assert.deepEqual(r, ["2026-06-02"]));
});
v.it("eligibleHelperDayKeys > returns empty when every helper day is also a driving day", 186, () => {
  v.check("eligibleHelperDayKeys", [["2026-06-05"], ["2026-06-05"]], (r) => assert.deepEqual(r, []));
});
v.it("eligibleHelperDayKeys > ignores empty keys and handles no helper tasks", 189, () => {
  v.check("eligibleHelperDayKeys", [["", "2026-06-07"], []], (r) => assert.deepEqual(r, ["2026-06-07"]));
  v.check("eligibleHelperDayKeys", [[], ["2026-06-01"]], (r) => assert.deepEqual(r, []));
});
// The Vitest case passes a Set; a JSON vector carries its members (the function accepts any Iterable).
v.it("eligibleHelperDayKeys > accepts a Set for delivered keys", 193, () => {
  v.check("eligibleHelperDayKeys", [["2026-06-10", "2026-06-11"], ["2026-06-11"]], (r) => assert.deepEqual(r, ["2026-06-10"]));
});

v.it("applyDeductions > deducts SSO then penalty; net = earnings - sso - penalty", 200, () => {
  v.check("applyDeductions", [5000, 750, [{ id: "p1", remainingThb: 3000, installmentThb: 3000 }]], (r) => {
    assert.equal(r.ssoAppliedThb, 750);
    assert.equal(r.penaltiesAppliedThb, 3000);
    assert.equal(r.perPenalty[0].remainingAfterThb, 0);
    assert.equal(r.netThb, 1250);
  });
});
v.it("applyDeductions > never goes negative; unpaid penalty carries forward", 207, () => {
  v.check("applyDeductions", [1000, 750, [{ id: "p1", remainingThb: 3000, installmentThb: 3000 }]], (r) => {
    assert.equal(r.ssoAppliedThb, 750);
    assert.equal(r.penaltiesAppliedThb, 250);
    assert.equal(r.perPenalty[0].remainingAfterThb, 2750);
    assert.equal(r.netThb, 0);
  });
});
v.it("applyDeductions > honors a split installment smaller than the balance", 214, () => {
  v.check("applyDeductions", [5000, 0, [{ id: "p1", remainingThb: 3000, installmentThb: 1000 }]], (r) => {
    assert.equal(r.penaltiesAppliedThb, 1000);
    assert.equal(r.perPenalty[0].remainingAfterThb, 2000);
    assert.equal(r.netThb, 4000);
  });
});

const ported = v.write("compensationCompute.json");
assert.equal(ported.cases.length, 29, "compensationCompute.test.ts has 29 `it` blocks");

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
const rnd = mulberry32(0x4844); // "HD"
const int = (lo, hi) => lo + Math.floor(rnd() * (hi - lo + 1));
const pick = (xs) => xs[int(0, xs.length - 1)];
// An instant between 1950 and 2040, at a whole minute (Bangkok edges included by the curated lists).
const instant = () => new Date(Date.UTC(1950, 0, 1) + int(0, 90 * 365 * 1440) * 60000);
// Instants within a day of Bangkok noon and midnight, where the window and the date keys turn.
const edgeInstant = () => {
  const day = Date.UTC(2026, int(0, 11), int(1, 28));
  return new Date(day - 7 * 3600000 + pick([0, 12]) * 3600000 + int(-90, 90) * 60000 + pick([0, 1, 59]) * 1000);
};

const c = vectorFile(
  "logitrack-web/lib/compensationCompute.ts",
  "Characterisation vectors (§6.16, T44): seeded random and curated inputs, want = the TypeScript output. Regenerate with export.mjs; never edit by hand.",
);

c.it("Bangkok date keys, rounds and helper windows", 0, () => {
  for (let i = 0; i < 120; i++) {
    const t = i % 2 ? instant() : edgeInstant();
    c.check("bangkokDateKey", [t]);
    c.check("assignRound", [t]);
    c.check("helperWindowKey", [t]);
  }
}, "characterisation");

c.it("roundTHB on halves, negatives and signed zero", 0, () => {
  for (const x of [0, -0, 0.5, -0.5, 1.5, -1.5, 2.5, -2.5, 0.49999999999999994, -0.4, 949.5, 1e15 + 0.5, -949.5]) c.check("roundTHB", [x]);
  for (let i = 0; i < 60; i++) c.check("roundTHB", [int(-500000, 500000) / pick([1, 2, 4, 10, 100, 3])]);
}, "characterisation");

c.it("holidays: Sundays and listed dates (Bangkok)", 0, () => {
  const keys = ["2026-01-01", "2026-04-13", "2026-04-14", "2026-12-31"];
  for (let i = 0; i < 60; i++) c.check("isHoliday", [keys, i % 3 ? edgeInstant() : instant()]);
}, "characterisation");

c.it("computeBasePay on random trips and rates", 0, () => {
  for (let i = 0; i < 40; i++) {
    const trips = Array.from({ length: int(0, 12) }, () => ({
      deliveredAt: edgeInstant(), isStandby: rnd() < 0.2, isMultiStop: rnd() < 0.2,
    }));
    const cfg = { weekdayRateThb: int(0, 600), holidayRateThb: int(0, 900), payStandby: rnd() < 0.5 };
    c.check("computeBasePay", [trips, cfg, i % 4 ? { keys: ["2026-04-13", "2026-05-01", "2026-12-05"] } : { never: true }]);
  }
}, "characterisation");

c.it("incentive tiers: unsorted, duplicate thresholds, the refuel gate", 0, () => {
  const tiers = [{ min: 13, amountThb: 1400 }, { min: 10, amountThb: 1000 }, { min: 12, amountThb: 1200 }, { min: 12, amountThb: 1250 }, { min: 14, amountThb: 1800 }];
  for (let i = 0; i < 30; i++) {
    c.check("computeFuelIncentive", [rnd() < 0.15 ? null : int(80, 160) / 10, int(0, 9), tiers, int(0, 6)]);
    c.check("computeTripVolumeIncentive", [int(0, 90), [{ min: 70, amountThb: 2000 }, { min: 50, amountThb: 1000 }, { min: 60, amountThb: 1500 }, { min: 60, amountThb: 1600 }]]);
  }
  c.check("computeTripVolumeIncentive", [10, []]);
  c.check("computeFuelIncentive", [12, 9, [], 5]);
}, "characterisation");

c.it("ageYearsAt and computeSso around birthdays, the age cap and the hire-year cutoff", 0, () => {
  for (let i = 0; i < 60; i++) {
    const asOf = new Date(Date.UTC(2026, int(0, 11), int(1, 28)) + pick([-7, 17]) * 3600000);
    const asBkk = new Date(asOf.getTime() + 7 * 3600000);
    // Birthdays on, just before and just after the as-of day, 50-60 years back.
    const birth = new Date(Date.UTC(asBkk.getUTCFullYear() - int(50, 60), asBkk.getUTCMonth(), asBkk.getUTCDate() + int(-1, 1)) - 7 * 3600000 + pick([0, 23]) * 3600000);
    c.check("ageYearsAt", [birth, asOf]);
    const hire = rnd() < 0.2 ? null : new Date(Date.UTC(int(2023, 2027), int(0, 11), 1) - 7 * 3600000 + pick([0, -1]) * 60000);
    c.check("computeSso", [{ hireDate: hire, birthDate: rnd() < 0.2 ? null : birth, probationPassed: rnd() < 0.85 }, SSO_RULE, asOf]);
  }
  c.check("computeSso", [{ hireDate: null, birthDate: null, probationPassed: true }, { ...SSO_RULE, ratePercent: 4.5, baseExistingThb: 15001 }, ASOF]);
}, "characterisation");

c.it("applyDeductions with several penalties: order, shortfalls, carried balances", 0, () => {
  for (let i = 0; i < 40; i++) {
    const penalties = Array.from({ length: int(0, 4) }, (_, k) => {
      const remaining = int(0, 6000);
      return { id: `p${k}`, remainingThb: remaining, installmentThb: pick([remaining, Math.round(remaining / 3), int(0, 8000), -50]) };
    });
    c.check("applyDeductions", [pick([int(0, 12000), 0, -100]), pick([0, 600, 750, int(0, 900)]), penalties]);
  }
}, "characterisation");

c.it("helper days: dedupe, driving windows and empty keys", 0, () => {
  const pool = ["2026-06-01", "2026-06-02", "2026-06-03", "2026-06-04", ""];
  for (let i = 0; i < 30; i++) {
    c.check("eligibleHelperDayKeys", [Array.from({ length: int(0, 6) }, () => pick(pool)), Array.from({ length: int(0, 3) }, () => pick(pool))]);
    c.check("computeHelperPay", [int(-2, 16), pick([400, 0, -10, 350.5])]);
  }
}, "characterisation");

c.writeCompact("characterisation.json");
