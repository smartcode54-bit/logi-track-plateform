# LINE Flex golden vectors

Language-neutral test vectors for the LINE Flex cards of `notify.line` (main spec §7.6, T49). They are exported from the TypeScript builders that are being replaced and checked against both implementations:

| Verifier | Command | Checks against |
|---|---|---|
| Go | `go test ./internal/notify/ -run TestLineFlexGolden` | `internal/notify/lineflex.go` |
| TypeScript | `pnpm exec vitest run lib/lineMessageGolden.test.ts` (in `logitrack-web`; part of `pnpm test`) | `lib/lineMessage.ts` (and, through the header check, `functions/src/core/lineMessage.ts`) |

| File | Source | Ported `it` | Added |
|---|---|---|---|
| `lineMessage.json` | `logitrack-web/lib/lineMessage.test.ts` | 14 of 15 | 9 characterisation groups: Bangkok calendar days and Buddhist-era years around midnight and centuries; `HH:mm` at midnight and the last minute; driver-name blanks, trimming (U+FEFF included) and partial names; driver codes (preferred keys before the others, blanks skipped, a padded customer code); cards with every field empty; JSON escaping (quotes, backslashes, `\b \f \n \r \t`, control characters, `<`, `&`, U+2028/U+2029); the 300-unit `altText` cut; the evidence button without a link or without photos; delay notes with every cause, unknown keys, trimming and order |

The `it` that is not ported is `coerceToDate` (Firestore `Timestamp`, `Date`, string and number coercion): Go reads typed `timestamptz` values, never a Firestore value. The Go test asserts the ported count, so a dropped case fails the build.

## Format

The billing format (`../billing/README.md`): one case per Vitest `it` (`name`, `line`) holding `checks: [{fn, args, want}]`; a case with `added` is new for the Go contract and is not counted as ported. Instants are `{"$date": "<toISOString>"}` (the codec is `../billing/codec.mjs`); any other date argument stands for "no valid date". Particular to this file:

- **A card's `want` is a string**: the exact `JSON.stringify` text of the message `{type, altText, contents}`. The Go builders write the same bytes (keys in the order of the TypeScript object literals, `JSON.stringify` escaping: non-ASCII, `<`, `&` and U+2028 are written as they are), so "byte-equal" is a string comparison.
- `resolveDriverCustomerCode(driver, code)`: Go reads one customer's ids for a driver from `driver_customer_codes` in key order (`ORDER BY id_type_key COLLATE "C"`, the order Firestore returns a map's fields); the exporter refuses a vector whose `customerDriverIds` keys are not in that order. The national ID card and the licence are never among the ids Go reads.
- **The one deliberate difference** (not in the vectors): `altText` is JavaScript's `slice(0, 300)` in UTF-16 code units, but a surrogate pair the cut would split is dropped whole (as the FCM payloads do, `internal/notify/pushes.go`): JavaScript keeps a lone high surrogate, which no UTF-8 payload can carry.

## Regenerating

The vectors are generated, never edited by hand:

```bash
node testdata/golden/line/export.mjs
```

Run from `logitrack-api/` with Node 22.18 or later: Node strips the types of the imported `.ts` file itself, no install is needed. Ported cases re-run their Vitest assertions on the TypeScript output before it is stored. Output is deterministic and independent of the host time zone. The exporter also fails when `lib/lineMessage.ts` and `functions/src/core/lineMessage.ts` differ after their header comment.
