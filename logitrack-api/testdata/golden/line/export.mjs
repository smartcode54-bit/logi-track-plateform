// Exports the LINE Flex golden vectors from the TypeScript builders (main spec §7.6, T49).
//
//   node testdata/golden/line/export.mjs            (from logitrack-api/, Node >= 22.18)
//
// Node strips the types of the imported .ts file itself; no install is needed.
// Ported cases re-run the assertions of their Vitest `it` block
// (logitrack-web/lib/lineMessage.test.ts) against the TypeScript output before
// storing it as `want`; a card's want is the exact JSON.stringify of the
// message, so the Go builders (internal/notify/lineflex.go) are checked byte for
// byte. characterisation cases pin the same functions on edge inputs (Bangkok
// midnight, Buddhist-era years, empty and whitespace fields, JSON escaping,
// the 300-unit altText cut). logitrack-web/lib/lineMessageGolden.test.ts
// re-checks every vector against the TypeScript module on each `pnpm test`; the
// Go tests (internal/notify/lineflex_golden_test.go) check them against Go.
import assert from "node:assert/strict";
import { readFileSync, writeFileSync } from "node:fs";
import { dec, enc } from "../billing/codec.mjs";

const web = (p) => new URL(`../../../../logitrack-web/${p}`, import.meta.url);
const lm = await import(web("lib/lineMessage.ts"));

const COMMIT = "4f552099";
const out = (name) => new URL(name, import.meta.url);

// web:lib/lineMessage.ts and fn:core/lineMessage.ts are one module (the "byte-identical mirror" of their header): they
// may differ in the header comment only, so the code after it must be equal.
{
  const body = (p) => {
    const src = readFileSync(web(p), "utf8");
    const end = src.indexOf("*/");
    assert.ok(end > 0, `${p}: no header comment`);
    return src.slice(end);
  };
  assert.equal(body("lib/lineMessage.ts"), body("functions/src/core/lineMessage.ts"),
    "lib/lineMessage.ts and functions/src/core/lineMessage.ts differ after their header comment");
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
    /** A check whose want is the TypeScript output (a card as its JSON text), after `expectFn` accepted it. */
    check(fn, args, expectFn) {
      const got = CALL[fn](...dec(enc(args)));
      if (expectFn) expectFn(got);
      const want = CARDS.has(fn) ? JSON.stringify(got) : got;
      current.checks.push({ fn, args: enc(args), want: enc(want) });
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
// names onto the Go API (internal/notify/lineflex_golden_test.go). A date
// argument is {"$date"}; any other value stands for "no valid date".
const CARDS = new Set(["buildCheckinMessage", "buildDeliveredMessage", "buildStandbyMessage"]);
const CALL = {
  formatBuddhistShortDate: (d) => lm.formatBuddhistShortDate(d),
  formatBangkokHm: (d) => lm.formatBangkokHm(d),
  resolveDriverNameTh: (d, fallback) => lm.resolveDriverNameTh(d, fallback ?? undefined),
  resolveDriverCustomerCode: (d, code) => {
    // Go reads a customer's ids in key order (driver_customer_codes ORDER BY id_type_key COLLATE "C", the order
    // Firestore returns a map's fields): a vector lists them that way.
    for (const ids of Object.values(d?.customerDriverIds ?? {})) {
      const keys = Object.keys(ids ?? {});
      assert.deepEqual(keys, [...keys].sort(), "customerDriverIds keys must be in byte order");
    }
    return lm.resolveDriverCustomerCode(d, code);
  },
  buildCheckinMessage: (ctx) => lm.buildCheckinMessage(ctx),
  buildDeliveredMessage: (ctx) => lm.buildDeliveredMessage(ctx),
  buildStandbyMessage: (ctx) => lm.buildStandbyMessage(ctx),
  buildDelayNote: (keys) => lm.buildDelayNote(keys),
};

// ─── lineMessage.test.ts (15 `it`; 14 ported) ─────────────────────────────────

const d = (s) => new Date(s);

const baseCtx = {
  dateLine: "05/09/69",
  originLabel: "ประเวศ18",
  destinationLabel: "SOCE (บัวโรย)",
  tripNo: "TRIP-123",
  driverNameTh: "สมชาย ใจดี",
  driverCode: "12345",
  plate: "1กก-1234",
  phone: "0812345678",
  partner: "SPX",
  truckType: "6 Wheels",
  checkInHm: "08:30",
};

const stdCtx = {
  dateLine: "05/09/69",
  startLabel: "ลาดพร้าว08",
  endLabel: "บัวโรย",
  driverNameTh: "สมชาย ใจดี",
  driverCode: "-",
  plate: "1กก-1234",
  phone: "0812345678",
  partner: "SPX",
  startedHm: "10:00",
  endedHm: "12:30",
  durationText: "150 นาที",
  notes: "งานหมด",
};

const v = vectorFile(
  "logitrack-web/lib/lineMessage.test.ts",
  "Ported from the Vitest file (one case per `it`; coerceToDate is not ported: Go reads typed timestamptz values, never Firestore Timestamps); want = the TypeScript output after the test's assertions accepted it, a card as the exact JSON.stringify text. Regenerate with export.mjs; never edit by hand.",
);

v.it("date/time helpers (Bangkok) > formatBuddhistShortDate → dd/MM/yy with Buddhist-era year, from a Timestamp-like", 33, () => {
  v.check("formatBuddhistShortDate", [d("2026-09-05T04:00:00Z")], (r) => assert.equal(r, "05/09/69"));
});
v.it("date/time helpers (Bangkok) > formatBangkokHm → HH:mm in Asia/Bangkok, '-' when missing/invalid", 38, () => {
  v.check("formatBangkokHm", [d("2026-09-05T01:30:00Z")], (r) => assert.equal(r, "08:30"));
  v.check("formatBangkokHm", [null], (r) => assert.equal(r, "-"));
  v.check("formatBangkokHm", ["not a date"], (r) => assert.equal(r, "-"));
});
v.it("resolveDriverNameTh > prefers fullNameTh (the Thai-name rule the manual builder violated)", 54, () => {
  v.check("resolveDriverNameTh", [{ fullNameTh: "สมชาย ใจดี", firstName: "Somchai", lastName: "Jaidee" }],
    (r) => assert.equal(r, "สมชาย ใจดี"));
});
v.it("resolveDriverNameTh > falls back name → firstName+lastName → email → id", 59, () => {
  v.check("resolveDriverNameTh", [{ name: "Legacy" }], (r) => assert.equal(r, "Legacy"));
  v.check("resolveDriverNameTh", [{ firstName: "Anan", lastName: "Kumar" }], (r) => assert.equal(r, "Anan Kumar"));
  v.check("resolveDriverNameTh", [{ email: "a@b.com" }], (r) => assert.equal(r, "a@b.com"));
  v.check("resolveDriverNameTh", [{}, "uid_9"], (r) => assert.equal(r, "uid_9"));
  v.check("resolveDriverNameTh", [null, "uid_9"], (r) => assert.equal(r, "uid_9"));
  v.check("resolveDriverNameTh", [null], (r) => assert.equal(r, "-"));
});
v.it("resolveDriverCustomerCode > returns the customer's own driver id (e.g. Shopee App ID) by the customer code", 70, () => {
  v.check("resolveDriverCustomerCode", [{ customerDriverIds: { SPX: { appId: "889900" } } }, "SPX"], (r) => assert.equal(r, "889900"));
});
v.it("resolveDriverCustomerCode > returns '-' when that customer keeps no id for this driver (e.g. J&T)", 74, () => {
  v.check("resolveDriverCustomerCode", [{ customerDriverIds: { SPX: { appId: "1" } } }, "JNT"], (r) => assert.equal(r, "-"));
  v.check("resolveDriverCustomerCode", [{}, "SPX"], (r) => assert.equal(r, "-"));
  v.check("resolveDriverCustomerCode", [null, "SPX"], (r) => assert.equal(r, "-"));
  v.check("resolveDriverCustomerCode", [{ customerDriverIds: { SPX: { appId: "1" } } }, ""], (r) => assert.equal(r, "-"));
});
v.it("resolveDriverCustomerCode > never leaks the national ID card or license (PII), even when present", 80, () => {
  v.check("resolveDriverCustomerCode", [{ idCard: "1234567890123", truckLicenseId: "AB123456", customerDriverIds: {} }, "SPX"],
    (r) => assert.equal(r, "-"));
});
v.it("buildCheckinMessage > returns a flex card with the check-in header and Thai name, no evidence button", 87, () => {
  v.check("buildCheckinMessage", [baseCtx], (msg) => {
    assert.equal(msg.type, "flex");
    assert.ok(msg.altText.includes("เช็คอิน"));
    const json = JSON.stringify(msg.contents);
    assert.ok(json.includes("🚚 เช็คอินต้นทางแล้ว"));
    assert.ok(json.includes("สมชาย ใจดี"));
    assert.ok(!json.includes("ดูรูปหลักฐาน"));
    assert.ok(!json.includes("จบงาน"));
  });
});
v.it("buildDeliveredMessage > returns a flex card with full detail and all timestamps", 100, () => {
  v.check("buildDeliveredMessage", [{ ...baseCtx, parcels: "42", departHm: "09:00", arriveHm: "12:15", doneHm: "12:40", notes: "ส่งเรียบร้อย" }], (msg) => {
    assert.equal(msg.type, "flex");
    assert.ok(msg.altText.includes("ส่งงานสำเร็จ"));
    const json = JSON.stringify(msg.contents);
    for (const s of ["✅ ส่งงานสำเร็จ", "42", "12:40", "ส่งเรียบร้อย"]) assert.ok(json.includes(s));
  });
});
v.it("buildDeliveredMessage > adds the evidence button only when a url and photo count are given", 118, () => {
  v.check("buildDeliveredMessage", [{ ...baseCtx, evidenceUrl: "https://x/e/tok", photoCount: 8 }], (msg) => {
    const json = JSON.stringify(msg);
    assert.ok(json.includes("ดูรูปหลักฐาน (8)"));
    assert.ok(json.includes("https://x/e/tok"));
  });
  v.check("buildDeliveredMessage", [baseCtx], (msg) => assert.ok(!JSON.stringify(msg).includes("ดูรูปหลักฐาน")));
});
v.it("buildDelayNote > names distinct causes in Thai and points to the evidence", 128, () => {
  v.check("buildDelayNote", [["incident_cause_traffic", "incident_cause_traffic", "incident_cause_tire"]], (note) => {
    assert.ok(note.includes("การจราจรติดขัดรุนแรง"));
    assert.ok(note.includes("ยางแตก"));
    assert.ok(note.includes("ดูรูปหลักฐาน"));
    assert.equal(note.match(/การจราจรติดขัดรุนแรง/g)?.length, 1);
  });
});
v.it("buildDelayNote > falls back to a generic note for empty / unknown input", 136, () => {
  v.check("buildDelayNote", [[]], (r) => assert.ok(r.includes("มีเหตุทำให้จัดส่งล่าช้า")));
  v.check("buildDelayNote", [[null, "  "]], (r) => assert.ok(r.includes("มีเหตุทำให้จัดส่งล่าช้า")));
});
v.it("buildStandbyMessage > renders the standby header + timeline, no evidence button by default", 158, () => {
  v.check("buildStandbyMessage", [stdCtx], (msg) => {
    assert.equal(msg.type, "flex");
    assert.ok(msg.altText.includes("งานหมด รถ Standby"));
    const json = JSON.stringify(msg.contents);
    for (const s of ["🅿\ufe0f งานหมด รถ Standby", "เริ่มจอด", "ระยะเวลา", "150 นาที"]) assert.ok(json.includes(s));
    assert.ok(!json.includes("ดูรูปหลักฐาน"));
  });
});
v.it("buildStandbyMessage > adds the evidence button when a url + count are given", 170, () => {
  v.check("buildStandbyMessage", [{ ...stdCtx, evidenceUrl: "https://x/e/tok", photoCount: 2 }], (msg) => {
    assert.ok(JSON.stringify(msg).includes("ดูรูปหลักฐาน (2)"));
    assert.ok(JSON.stringify(msg).includes("https://x/e/tok"));
  });
});

// ─── characterisation ────────────────────────────────────────────────────────

const C = "characterisation";
v.it("Bangkok calendar day and Buddhist-era year around midnight and centuries", 0, () => {
  for (const s of ["2026-09-04T16:59:59Z", "2026-09-04T17:00:00Z", "2026-12-31T17:00:00Z", "1999-12-31T16:59:00Z",
    "2000-01-01T00:00:00+07:00", "2043-06-15T12:00:00Z", "2457-01-01T00:00:00+07:00"]) {
    v.check("formatBuddhistShortDate", [d(s)]);
  }
}, C);
v.it("HH:mm at Bangkok midnight, noon and the last minute", 0, () => {
  for (const s of ["2026-09-04T17:00:00Z", "2026-09-04T16:59:59Z", "2026-09-05T05:00:30Z", "2026-09-05T16:59:00Z"]) {
    v.check("formatBangkokHm", [d(s)]);
  }
}, C);
v.it("driver names: blanks, trimming and partial names", 0, () => {
  v.check("resolveDriverNameTh", [{ fullNameTh: "   ", firstName: "Anan", lastName: "" }]);
  v.check("resolveDriverNameTh", [{ fullNameTh: "  สมหญิง  " }]);
  v.check("resolveDriverNameTh", [{ firstName: "", lastName: "Kumar" }]);
  v.check("resolveDriverNameTh", [{ firstName: " ", lastName: " ", email: " x@y.z " }]);
  v.check("resolveDriverNameTh", [{ firstName: "\ufeffBom", email: "" }, "fallback"]);
  v.check("resolveDriverNameTh", [{ email: "  " }, "  "]);
}, C);
v.it("driver codes: preferred keys before the others, blanks skipped", 0, () => {
  v.check("resolveDriverCustomerCode", [{ customerDriverIds: { SPX: { appId: " ", driverId: "D-1", workId: "W-1" } } }, "SPX"]);
  v.check("resolveDriverCustomerCode", [{ customerDriverIds: { SPX: { appid: "a-lower", workId: "W-1" } } }, "SPX"]);
  v.check("resolveDriverCustomerCode", [{ customerDriverIds: { SPX: { badge: " ", employeeNo: "E-9", zone: "Z" } } }, "SPX"]);
  v.check("resolveDriverCustomerCode", [{ customerDriverIds: { SPX: { other: "  " } } }, "SPX"]);
  v.check("resolveDriverCustomerCode", [{ customerDriverIds: { CJSF: { workId: " 77 " } } }, " CJSF "]);
}, C);
v.it("cards with every field empty or blank", 0, () => {
  const blank = { dateLine: "", originLabel: "", destinationLabel: " ", tripNo: "", driverNameTh: "", driverCode: "",
    plate: "", phone: "", partner: "", truckType: "", checkInHm: "" };
  v.check("buildCheckinMessage", [blank]);
  v.check("buildDeliveredMessage", [blank]);
  v.check("buildStandbyMessage", [{ dateLine: "", startLabel: "", endLabel: "", driverNameTh: "", driverCode: "", plate: "",
    phone: "", partner: "", startedHm: "", endedHm: "", durationText: "" }]);
}, C);
v.it("JSON escaping of quotes, backslashes, control characters and line separators", 0, () => {
  const odd = 'A "quoted" \\ back\nline\ttab\bbs\fff\rcr\u0001ctl <b>&amp; \u2028\u2029 end';
  v.check("buildCheckinMessage", [{ ...baseCtx, originLabel: odd, driverNameTh: odd, plate: odd }]);
  v.check("buildDeliveredMessage", [{ ...baseCtx, notes: odd, evidenceUrl: "https://x/e/a\"b", photoCount: 1 }]);
  v.check("buildStandbyMessage", [{ ...stdCtx, notes: odd, startLabel: odd }]);
}, C);
v.it("altText is cut at 300 UTF-16 units", 0, () => {
  const long = "ท".repeat(290);
  v.check("buildCheckinMessage", [{ ...baseCtx, originLabel: long }]);
  v.check("buildDeliveredMessage", [{ ...baseCtx, driverNameTh: long, destinationLabel: long }]);
  v.check("buildStandbyMessage", [{ ...stdCtx, startLabel: "a".repeat(281) }]);
}, C);
v.it("evidence button needs both a link and photos", 0, () => {
  v.check("buildDeliveredMessage", [{ ...baseCtx, evidenceUrl: "https://x/e/tok", photoCount: 0 }]);
  v.check("buildDeliveredMessage", [{ ...baseCtx, evidenceUrl: "", photoCount: 3 }]);
  v.check("buildStandbyMessage", [{ ...stdCtx, notes: "", evidenceUrl: "https://x/e/tok", photoCount: 12 }]);
}, C);
v.it("delay notes: every cause, unknown keys, trimming and order", 0, () => {
  v.check("buildDelayNote", [["incident_cause_accident", "incident_cause_engine", "incident_cause_tire", "incident_cause_traffic",
    "incident_cause_weather", "incident_cause_checkpoint"]]);
  v.check("buildDelayNote", [[" incident_cause_weather ", "custom_cause", "incident_cause_weather", null]]);
  v.check("buildDelayNote", [["something_else"]]);
}, C);

const file = v.writeCompact("lineMessage.json");
const ported = file.cases.filter((c) => !c.added).length;
assert.equal(ported, 14, "ported `it` count");
console.log(`lineMessage.json: ${file.cases.length} cases (${ported} ported), ${file.cases.reduce((n, c) => n + c.checks.length, 0)} checks`);
