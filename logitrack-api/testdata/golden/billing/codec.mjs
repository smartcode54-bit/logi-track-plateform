// Value codec shared by export.mjs and the Vitest verifier
// (logitrack-web/lib/billingGolden.test.ts). JSON cannot carry every value the
// billing engine returns, so a few are tagged:
//   {"$num": "NaN" | "Infinity" | "-Infinity" | "-0"}   numbers JSON loses
//   {"$date": "2026-08-05T03:00:00.000Z"}                 a Date (toISOString)
//   {"$local": "2026-08-16T13:45:00"}                     a date picked in the host zone (args only)
//   {"$now": true}                                        legacy only: Date.now() at call time
// Undefined object fields are omitted, as JSON.stringify does.

export function enc(v) {
  if (v === undefined || v === null) return v ?? null;
  if (typeof v === "number") {
    if (Number.isNaN(v)) return { $num: "NaN" };
    if (v === Infinity) return { $num: "Infinity" };
    if (v === -Infinity) return { $num: "-Infinity" };
    if (Object.is(v, -0)) return { $num: "-0" };
    return v;
  }
  if (v instanceof Date) return { $date: v.toISOString() };
  if (Array.isArray(v)) return v.map((x) => (x === undefined ? null : enc(x)));
  if (typeof v === "object") {
    const o = {};
    for (const [k, x] of Object.entries(v)) if (x !== undefined) o[k] = enc(x);
    return o;
  }
  return v;
}

export function dec(v) {
  if (v === null || typeof v !== "object") return v;
  if (Array.isArray(v)) return v.map(dec);
  if ("$num" in v) return { NaN: NaN, Infinity: Infinity, "-Infinity": -Infinity, "-0": -0 }[v.$num];
  if ("$date" in v) return new Date(v.$date);
  if ("$local" in v) {
    const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})$/.exec(v.$local);
    return new Date(+m[1], +m[2] - 1, +m[3], +m[4], +m[5], +m[6], 0);
  }
  const o = {};
  for (const [k, x] of Object.entries(v)) o[k] = dec(x);
  return o;
}
