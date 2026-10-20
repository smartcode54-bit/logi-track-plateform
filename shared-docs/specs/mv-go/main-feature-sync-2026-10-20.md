# Main feature sync — 2026-10-20

Status: Implemented; PR validation pending

## Source and scope

Owner requested features from main, particularly consolidated menus. Compare
origin/main `30c32c72` against mv-go `045e7414`; their merge base is `4f552099`.
The job-assignment feature commit `90af0d17` is already an ancestor of mv-go,
but the Go rewrite restored separate menu entries and a pre-dialog type menu.
Only one main commit is missing: the mobile Standby transaction ordering fix.

## Implementation

- Merge main on the dedicated mv-go-sync branch, preserving both changelog histories.
- Present First Mile and Line Haul through Job assignment in sidebar and Quick Jump.
  Keep the full route catalog, existing URLs, breadcrumbs and authorization unchanged.
- Open the existing Go task form directly with one Add job action. Select FM/LH
  within the same dialog; default to the current type filter. A type change starts
  a fresh form, matching main, so old route, billing party and assignment fields
  cannot carry into the other type. Editing keeps its type.
- Restore the unified board column chooser, remembered locally as main does.
  Keep the existing mobile summary independent of desktop column preferences.
- Show actual pickup date and plain destination on the unified board; keep linked
  trip and existing detail/evidence actions. Avoid per-row photo signing requests.
- Reuse the current Go import, duplicate, cancel, paging, SSE and scope behavior.

## Files and verification

Navigation catalog and tests; task board and its rendered tests; task form tests;
DESIGN.md and project changelog. The upstream merge also touches only the mobile
Standby repository and the changelog. No API/schema/permission/flag/dependency change.

Validate targeted navigation/task/form/import tests, the frozen route-authority
fixture, translations, TypeScript, lint, full web suite and production build.
Inspect English/Thai desktop/mobile light/dark using the existing synthetic API.
The inherited mobile fix needs Flutter/device validation if no SDK is available.

## Risks

Desktop column visibility is a browser preference, contains no record data, and
must recover from malformed/unknown saved values. Hidden legacy menu entries remain
allowed by URL; this changes presentation only. Main stays production, untouched.
Support entry remains OFF. No deployment or existing local stack changes.

## Validation results

- Targeted navigation, board, form, import and customer-picker suite: 71 passed.
- Full web suite: 3,087 tests in 195 files passed.
- Translation and frozen route-authority guards after the Thai wording change: 26 passed.
- TypeScript: passed. Scoped ESLint: 0 errors; existing dictionary default-export warning.
- Production build and bundle budget: passed, 77 routes, no baseline relaxation.
- Production-build browser matrix: 8 passed (EN/TH × light/dark × 360/1280).
  Menu and form captures were inspected, including desktop and mobile states.
  The initial Thai check exposed duplicate labels; distinguish kind (ชนิดงาน)
  from primary/supplementary category (ประเภทงาน) before the final passing matrix.
- Desktop preferences ignore malformed/unknown saved columns and retain at least
  one data column. Mobile summaries ignore these preferences. A duplicated
  multi-drop task switches type and posts the new route without old stops.
- No Flutter/Dart executable on PATH: inherited main mobile fix reviewed as-is;
  APK build and actual device/Firestore validation remain unperformed.

Local logs: /private/tmp/main-sync-{targeted-final,web-tests-final,browser-final,
locales-final,tsc-final,eslint-final,bundle}-2026-10-20.log.

Final form regression: duplicate a multi-drop task billed to old-party, switch
kind, and prove the previous customer is cleared. Pick a new route whose hub
links p1 and verify that the new task posts p1 and no old delivery stops. The
red reproducer retained old-party; the fix reuses the existing create initializer.

## CI repairs

Two full CI web runs exposed the existing password-form focus race; the second
also exposed keyboard tests firing before Quick Jump's passive listener. Unit
CI uses Node20; build/bundle use Node22. Match the failing job's runtime.

A deterministic password regression flushes disabled inputs before onInvalid
and makes zero-delay focus callbacks run before the final re-enable commit.
The original fails with focus on submit; the existing post-submit effect now
reads the first invalid field through getFieldState after isSubmitting clears.
It retains field order, server422 behavior and focus while correcting a field;
no validation, auth API, shared invalid handler or timeout change. All37 affected
password/Quick Jump tests pass on Node20. The new case adds one web test (3088).

The Quick Jump test mount waits for exactly one window keydown registration,
then every shortcut test starts. Preserve the existing no-duplicate listener
and same-callback cleanup assertions; no production Quick Jump edit.

CI repair files: AccountPasswordPanel.tsx and its test, Quick Jump's test only.
