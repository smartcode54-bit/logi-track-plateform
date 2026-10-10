# Billing golden vectors

Language-neutral test vectors for the billing engine (main spec §6.16, T36). They are exported from the TypeScript engine that is being replaced and checked against both implementations:

| Verifier | Command | Checks against |
|---|---|---|
| Go | `go test ./internal/billing/... ./internal/platform/clock/` | `internal/billing/compute`, `internal/billing/documents`, `internal/platform/clock`, `internal/platform/jsmath` |
| TypeScript | `pnpm exec vitest run lib/billingGolden.test.ts` (in `logitrack-web`; part of `pnpm test`) | `lib/billingCompute.ts`, `lib/billingRates.ts`, `lib/billingDate.ts`, `lib/billingDocument.ts`, `lib/jobCategory.ts`, `functions/src/core/billingPeriodLock.ts` |

| File | Source | Ported `it` | Added |
|---|---|---|---|
| `billingCompute.json` | `logitrack-web/lib/billingCompute.test.ts` | 49 | R15, R16, R19, R20 cases |
| `billingRates.json` | `logitrack-web/lib/billingRates.test.ts` | 5 | |
| `billingDate.json` | `logitrack-web/lib/billingDate.test.ts` | 8 | V8 date-parsing parity |
| `billingPeriodLock.json` | `logitrack-web/functions/src/core/billingPeriodLock.test.ts` | 12 | |
| `billingDocument.json` | `logitrack-web/lib/billingDocument.test.ts` | 16 | route labels, `toFixed` rounding |
| `jobCategory.json` | `logitrack-web/lib/jobCategory.test.ts` | 9 | JavaScript `trim` / `toLowerCase` on import cells |
| `characterisation.json` | untested TypeScript paths and JavaScript number semantics (seeded random + curated) | 0 | 14 groups, 5234 checks |

The Go tests assert the ported counts, so a dropped case fails the build.

## Format

```json
{"source": "...", "sourceCommit": "4f552099", "note": "...", "cases": [
  {"name": "describe > it", "line": 25, "checks": [
    {"fn": "normalizeVehicleClass", "args": ["Pickup"], "want": "4W"},
    {"fn": "normalizeVehicleClass", "args": [""], "want": null, "legacy": "4WJ", "divergence": "R15"}
  ]},
  {"name": "...", "line": 0, "added": "R19", "checks": [
    {"fn": "computeTripBillingFromParts", "args": [{}, "..."], "want": null, "wantReason": "no_billing_date", "legacy": {"...": "..."}, "divergence": "R19"}
  ]}
]}
```

- `fn` names the TypeScript function (or a JavaScript expression such as `Math.round`); each verifier maps it onto its own API. An unmapped name fails.
- `want` is what Go must return. Where Go deliberately differs, `legacy` is what TypeScript returns, `divergence` names the resolution and `wantReason` the unpriced reason Go reports.
- Instants in `billingCompute` / `billingPeriodLock` / `characterisation` arguments are epoch milliseconds; `0` or `null` is absent, as the legacy `timestampLikeToMillis` returned `0`.
- Tagged values (`codec.mjs`): `{"$num": "NaN" | "Infinity" | "-Infinity" | "-0"}`, `{"$date": "<toISOString>"}`, `{"$local": "2026-08-16T13:45:00"}` (a date picked in the host zone; Go replays it in five zones), and `{"$now": true}` (legacy only: TypeScript returned `Date.now()`).
- Floats compare bit for bit (`-0` differs from `+0`, NaN equals NaN).

## Regenerating

The vectors are generated, never edited by hand (the `billingDocument.json` and `jobCategory.json` wants are literals in `export.mjs`, because `billingDocument.ts` needs the browser bundle and `jobCategory.ts` imports the `@/` path alias; the Vitest verifier checks them against the modules).

```bash
node testdata/golden/billing/export.mjs
```

Run from `logitrack-api/` with Node 22.18 or later: Node strips the types of the imported `.ts` files itself, no install is needed. Ported cases re-run their Vitest assertions on the TypeScript output before it is stored, so a vector only holds what the legacy test accepts. Output is deterministic (seeded) and independent of the host time zone. The exporter also fails when `lib/billingCompute.ts` and `functions/src/core/billingCompute.ts` differ in more than their header line.
