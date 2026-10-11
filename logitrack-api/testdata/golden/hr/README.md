# Compensation golden vectors

Language-neutral test vectors for the driver compensation engine (main spec §6.15, §6.16, T44). They are exported from the TypeScript engine that is being replaced and checked against both implementations:

| Verifier | Command | Checks against |
|---|---|---|
| Go | `go test ./internal/hr/compute/` | `internal/hr/compute` |
| TypeScript | `pnpm exec vitest run lib/compensationGolden.test.ts` (in `logitrack-web`; part of `pnpm test`) | `lib/compensationCompute.ts` (and, through the line-4 check, `functions/src/core/compensationCompute.ts`) |

| File | Source | Ported `it` | Added |
|---|---|---|---|
| `compensationCompute.json` | `logitrack-web/lib/compensationCompute.test.ts` | 29 | |
| `characterisation.json` | `logitrack-web/lib/compensationCompute.ts` on seeded random and curated inputs | 0 | 8 groups: Bangkok date keys, rounds and helper windows near noon and midnight; `roundTHB` halves and signed zero; holidays; `computeBasePay`; unsorted and duplicate incentive tiers and the refuel gate; `ageYearsAt` and `computeSso` around birthdays, the age cap and the hire-year cutoff; `applyDeductions` with several penalties (order, shortfalls, carried balances); helper days |

The Go test asserts the ported count, so a dropped case fails the build. The assembly of a run (`compute.Compute`, the per-driver body of `fn:driverCompensation.ts`, which imports `firebase-admin` and cannot be loaded here) is pinned by Go table tests (`internal/hr/compute/payslip_test.go`) and the integration tests of `internal/hr`.

## Format

The billing format (`../billing/README.md`): one case per Vitest `it` (`name`, `line`) holding `checks: [{fn, args, want}]`; a case with `added` is new for the Go contract and is not counted as ported. Instants are `{"$date": "<toISOString>"}` (the codec is `../billing/codec.mjs`). Three argument shapes are particular to these files:

- `isHoliday(keys, date)` is `makeHolidayChecker(new Set(keys))(date)`.
- The third argument of `computeBasePay` is the holiday predicate: `{"keys": [...]}` for `makeHolidayChecker`, `{"never": true}` for `() => false`.
- `eligibleHelperDayKeys` receives the delivered keys as an array (the Vitest "accepts a Set" case passes a `Set`; the function takes any iterable).

## Regenerating

The vectors are generated, never edited by hand:

```bash
node testdata/golden/hr/export.mjs
```

Run from `logitrack-api/` with Node 22.18 or later: Node strips the types of the imported `.ts` file itself, no install is needed. Ported cases re-run their Vitest assertions on the TypeScript output before it is stored. Output is deterministic (seeded) and independent of the host time zone. The exporter also fails when `lib/compensationCompute.ts` and `functions/src/core/compensationCompute.ts` differ in more than their header line 4.
