# Spec: jobCategory carried on the trip (seed at creation, display falls back to the task, fail loud)

> **Status:** ✅ Done (code complete + static checks green; runtime QA of mobile seed + invoice display pending)
> **Owner:** Samart Kas
> **Created:** 2026-08-09
> **Approved:** 2026-08-09 (via `/spec-build`; open Qs resolved with documented defaults — version `3.0.1+1`, invoice surfaces-not-blocks the unverified marker)
> **Domain:** accounting/billing (display) + drivers (Driver Monitor) + tasks (assign write) + mobile (trip creation)
> **Related:** [ADR 0010](../adr/0010-job-category-carried-on-trip-independent-of-billing.md); extends [ADR 0002](../adr/0002-edit-job-category-on-delivered-trip.md); fail-loud posture [ADR 0003](../adr/0003-edit-forms-fail-loudly-on-legacy-docs.md)/[0004](../adr/0004-shared-oninvalid-handler-for-all-forms.md); mobile release sequencing [ADR 0007](../adr/0007-mobile-forced-update-pipeline.md)

---

## 1. Problem & Goal (ทำไมต้องทำ)

`jobCategory` (หลัก `PRIMARY` / เสริม `SUPPLEMENTARY`) is set on the **task** on every assignment, yet the Income table, the Billing Document (invoice), and the Edit Trip dialog all read it from **`trip_records`**. The only writer of `trip_records.jobCategory` is the billing snapshot, which is invoked best-effort from mobile (swallowed error, no retry, no Firestore trigger) and early-returns without writing on many normal conditions — most often **"No rate"**. When the trip field is absent the invoice **silently prints หลัก** and Income shows `—`, regardless of the เสริม the admin chose.

**Goal:** make `trip_records.jobCategory` reliable end-to-end — seed it at trip creation, have every reader fall back to the authoritative `tasks.jobCategory`, and **never silently guess หลัก** (show a loud "unverified" marker when it is genuinely unknown). Root cause + decision recorded in ADR 0010.

## 2. Scope

**In scope:**
- **Mobile:** write `jobCategory` onto the `trip_record` at creation, copied from the fulfilled task (default `PRIMARY` for self-created jobs with no task). `pubspec.yaml` version bump.
- **Web display fallback:** resolve `trip.jobCategory` → `task.jobCategory` → **loud "unverified" marker** on Billing Document (page badges + Excel + invoice PDF, via the shared billing data layer), Income (Missing Billing tab + main-table loud marker), and prefill in `EditTripDetailsDialog`.
- **Assign write fix:** `createOrUpdateTask` must **omit** `jobCategory` on update when the client didn't send it, instead of coercing to `PRIMARY`.
- **Backfill:** a new **admin callable** that copies `task.jobCategory` onto `trip_records` docs that lack it (default `PRIMARY` only when the task has none); retire the unsafe local script.
- i18n en + th for the new "unverified" marker.

**Out of scope (ทำทีหลัง / ไม่ทำ):**
- **No change to billing math** (`lib/billingCompute.ts` / `functions/src/core/billingCompute.ts`) — no rate/price behavior changes.
- **No change to the billing-snapshot early-returns or freeze guards** in `tripBillingOnDelivered.ts` (`:123-136`, `:368-385`, etc.). The seed + fallback make them non-load-bearing for display; hardening them is a separate concern.
- **No forced-update** to purge old mobile builds — the display fallback already makes old-client trips render correctly; forcing an update is a separate ops decision (ADR 0007).
- No change to `setTripJobCategory` (ADR 0002) or `EditBillingDialog` category derivation.

## 3. Requirements

**Functional**

- **R1.** Mobile writes `trip_records.jobCategory` at trip creation, copied from the fulfilled task's `jobCategory`. When the driver self-creates a job with no task, write `PRIMARY`. Covers single-delivery and multi-delivery (J&T) creation paths.
- **R2.** `createOrUpdateTask` (`functions/src/tasks.ts`) writes `jobCategory` **only when the request includes it** (normalized to `PRIMARY`/`SUPPLEMENTARY`). On **create** with none sent, default `PRIMARY`. On **update** with none sent, leave the stored value untouched (no coercion to `PRIMARY`).
- **R3.** The Billing Document data layer resolves each row's category as `trip.jobCategory ?? task.jobCategory` (task already batch-loaded), leaving it **`undefined`** when neither exists — **never** coerced to `PRIMARY`. This resolved value feeds the page badges, the Excel export, and the invoice PDF remark.
- **R4.** On the Billing Document page, Income, and any export, a row whose resolved category is `undefined` renders a **loud neutral "unverified / ตรวจสอบ" marker**, not a หลัก badge and not an empty cell. The หลัก/เสริม filter must treat `undefined` as its own visible bucket (not folded into หลัก).
- **R5.** The Income **Missing Billing** tab resolves category from its already-loaded `taskMap`; the **main (billed) table** renders the loud marker for any billed-but-uncategorised legacy row (instead of the silent `—`).
- **R6.** `EditTripDetailsDialog` prefills the หลัก/เสริม selector from `trip.jobCategory ?? task.jobCategory ?? "PRIMARY"` (the dialog already has the linked task / writes to it). Edit/save behavior via `setTripJobCategory` is unchanged (ADR 0002).
- **R7.** A new **admin-only** callable `backfillTripJobCategoryFromTask` scans `trip_records` missing `jobCategory`, copies the linked `task.jobCategory` (by `taskId`), defaults `PRIMARY` when the task is missing or has no value, writes in batches, is **idempotent** (skips docs already `PRIMARY`/`SUPPLEMENTARY`), and returns stats `{ totalProcessed, updated, defaultedPrimary, taskMissing, errors }`. Exposed from the existing Utilities/backfill page (admin-gated).
- **R8.** The unsafe local script `scripts/backfill-job-category.mjs` (blind `PRIMARY`, and unrunnable without local Firestore access) is removed or replaced by a pointer to R7.

**Non-functional** (perf / security / i18n / cost)

- **N1.** i18n complete in `en` and `th` for the new "unverified" marker (and any backfill UI strings).
- **N2.** Security: mobile trip-creation write must be permitted by `firestore.rules` for the owning driver including the new `jobCategory` field (verify no field allowlist blocks it). The backfill callable uses Admin SDK and enforces admin in code, mirroring `backfillTaskCustomerLinks`.
- **N3.** No new Firestore index (the backfill pages `trip_records` by document id and batch-reads tasks by id, like existing backfills).
- **N4.** **billing sync guard:** this spec adds **no billing math**, so `lib/billingCompute.ts` and `functions/src/core/billingCompute.ts` are untouched. If any billing computation change becomes necessary, it MUST be mirrored in both files.
- **N5.** Reliability: display correctness must not depend on the billing callable having run — the fallback to `task.jobCategory` guarantees the invoice never mislabels a เสริม trip as หลัก even for old-client / unbilled trips.

## 4. Design

**Data model (Firestore)**
- `trip_records.jobCategory: "PRIMARY" | "SUPPLEMENTARY"` — **now seeded at creation**, still refreshed at billing (`tripBillingOnDelivered.ts:322`/`:403`) and corrected by `setTripJobCategory`. It is a **denormalized cache** of `tasks.jobCategory` (the source of truth). No new field is added.
- `tasks.jobCategory` — unchanged semantics; the only change is the write guard in R2.
- Reconciliation rule of record (ADR 0010): task = source of truth; trip copy is (a) seeded at creation, (b) refreshed at billing, (c) corrected by `setTripJobCategory`; readers prefer trip then fall back to task.

**Cloud Functions / billing**
- `functions/src/tasks.ts` (`createOrUpdateTask`, ~`:134`): replace the unconditional
  `jobCategory: data.jobCategory === "SUPPLEMENTARY" ? "SUPPLEMENTARY" : "PRIMARY"` with a conditional:
  set `taskDoc.jobCategory` only when `data.jobCategory !== undefined` (normalized); in the **create** branch, default `writeDoc.jobCategory ??= "PRIMARY"`. The existing strip-undefined filter (`:184`) then leaves an omitted value untouched on update. **(R2)**
- New callable **`backfillTripJobCategoryFromTask`** — a new file `functions/src/backfillTripJobCategory.ts` (or fold into an existing backfill module), exported via `functions/src/index.ts`, `region: "asia-southeast1"`, admin-only (`request.auth.token.admin === true || request.auth.token["role"] === "admin"`). Mirrors `backfillTaskCustomerLinks.ts` / `backfillTripTruckData.ts`: page `trip_records` by document id, collect `taskId`s of docs lacking `jobCategory`, batch-read tasks (`in` chunks of 30), copy `task.jobCategory` (default `PRIMARY` when task missing/blank), `batch.update` in groups of ≤400, return stats. **(R7)**
- **No change** to billing math, the snapshot early-returns, or the freeze guards. **(N4, out-of-scope items)**

**Web (Next.js)**
- `features/accounting/api/billing.ts`:
  - Extend `TaskInfo` (`:812`) with `jobCategory?: "PRIMARY" | "SUPPLEMENTARY"` and project it when building `taskMap` (`:821-822`).
  - Where rows are built (single `:926`/`:958`, multi-stop `:913` area) change `data.jobCategory === "SUPPLEMENTARY" ? … : "PRIMARY"` to resolve `data.jobCategory ?? taskInfo?.jobCategory`, preserving `undefined` when neither exists (do **not** default `PRIMARY`). This single change feeds the Billing Document page, the Excel export, and the invoice PDF remark (`lib/billingDocument.ts:754`/`:778`, which reads the row's `jobCategory`). **(R3)**
- `app/app/accounting/billing-document/page.tsx`:
  - Badge (`:665-673`): three-way render — `SUPPLEMENTARY` → เสริม; `PRIMARY` → หลัก; `undefined` → loud "unverified" badge (amber/destructive outline). **(R4)**
  - Category filter (`:196-199`, counts `:236-242`): add an "unverified" bucket so `undefined` rows are countable and not silently filtered as หลัก; keep them visible by default. **(R4)**
- `app/app/accounting/income/page.tsx`:
  - Missing Billing tab: resolve category from the already-built `taskMap` (`:388-426`). **(R5)**
  - Main billed table (`:337-342`, badge `:1227-1237`): render the `else` branch as the loud "unverified" marker instead of `"—"`. **(R4, R5)**
- `features/drivers/components/EditTripDetailsDialog.tsx` (`:112`, `:261`): prefill `localJobCategory` from `trip.jobCategory ?? task.jobCategory ?? "PRIMARY"` (source the task the dialog already links to; fetch its `jobCategory` if not already on the row type). Editing/saving via `setTripJobCategory` unchanged. **(R6)**
- i18n keys (`context/locales/en|th/accounting.ts`, and `driverMonitor.ts` if the dialog needs it):
  - `accounting.billingDocument.badge.jobCategoryUnknown` — en "Unverified — check" / th "ยังไม่ระบุ — ตรวจสอบ"
  - `accounting.income.table.jobCategoryUnknown` — same text (or shared key)
  - `accounting.billingDocument.table.jobCategoryUnknownFilter` — label for the unverified filter bucket
- Utilities/backfill page (`app/app/utilities/backfill/page.tsx`): add a "Backfill trip job category" button wired to `backfillTripJobCategoryFromTask`, showing the returned stats (mirrors existing backfill buttons). **(R7)**

**Mobile (Flutter)**
- `lib/features/home/data/models/trip_record.dart`: add `final String? jobCategory;` (constructor param, `fromMap` `map['jobCategory'] as String?`, `toFirestore` `if (jobCategory != null) 'jobCategory': jobCategory`).
- `lib/features/loading_phase/data/repositories/loading_trip_repository.dart` (`submitLoadingPhaseRecord`): add `String? jobCategory` param, pass into `TripRecord(... jobCategory: jobCategory ...)`.
- `lib/features/loading_phase/presentation/pages/loading_phase_page.dart`: resolve `_jobCategory` from the active task (default `'PRIMARY'` when no task / field absent), pass it to **every** `submitLoadingPhaseRecord` call (`:1382`, `:1445`).
- Verify/seed the same on any other trip-creation path (multi-delivery / manual check-in) so `PRIMARY` is written explicitly when there is no task.
- Bump `pubspec.yaml` `3.0.0+1` → `3.0.1+1`. Ships in the next APK; per ADR 0007 this only reaches trips created by the new build — the web display fallback (R3–R6) covers older builds.

**Firestore Rules**
- Verify the driver trip-creation write (loading phase `set(merge:true)` on `trip_records`) permits the new `jobCategory` field (no field-level `hasOnly` should block it; if one exists, add `jobCategory` to the allowed set). No new collection, no new rule otherwise. **(N2)**

## 5. Affected files

- `logitrack-web/functions/src/tasks.ts` — conditional `jobCategory` write (R2).
- `logitrack-web/functions/src/backfillTripJobCategory.ts` — **new** admin callable (R7).
- `logitrack-web/functions/src/index.ts` — export the new callable.
- `logitrack-web/features/accounting/api/billing.ts` — `TaskInfo.jobCategory` + resolve trip→task (R3).
- `logitrack-web/app/app/accounting/billing-document/page.tsx` — unverified badge + filter bucket (R4).
- `logitrack-web/app/app/accounting/income/page.tsx` — Missing-Billing task resolve + loud main-table marker (R4, R5).
- `logitrack-web/features/drivers/components/EditTripDetailsDialog.tsx` — prefill fallback (R6).
- `logitrack-web/app/app/utilities/backfill/page.tsx` — backfill button (R7).
- `logitrack-web/context/locales/en/accounting.ts` + `th/accounting.ts` — "unverified" keys (N1).
- `logitrack-mobile/lib/features/home/data/models/trip_record.dart` — `jobCategory` field (R1).
- `logitrack-mobile/lib/features/loading_phase/data/repositories/loading_trip_repository.dart` — param (R1).
- `logitrack-mobile/lib/features/loading_phase/presentation/pages/loading_phase_page.dart` — resolve + pass (R1).
- `logitrack-mobile/pubspec.yaml` — version bump.
- `logitrack-web/scripts/backfill-job-category.mjs` — remove/replace (R8).
- `logitrack-web/firestore.rules` — verify only (N2).
- `shared-docs/.vibe-rules.md` — Change Log entry (project rule).

## 6. Task breakdown

- [x] **T1.** Mobile: added `jobCategory` to `TripRecord` (field + constructor + `fromMap` + `toFirestore`); added `jobCategory` param to `submitLoadingPhaseRecord`; resolved `_jobCategory` from the active task (mirroring `_truckType`) and pass `_jobCategory ?? 'PRIMARY'` at the sole creation call site (`loading_phase_page.dart` — multi-delivery uses the same call); bumped `pubspec.yaml` → `3.0.1+1`. Audited: `loading_trip_repository.dart:130` `.set(merge)` is the only trip-creation site; delivery/standby paths are updates.
- [x] **T2.** Functions: extracted `resolveJobCategoryWrite(sent, isCreate)` → `functions/src/core/jobCategoryWrite.ts` (pure, testable); `createOrUpdateTask` uses it — omit on update-without-value, default `PRIMARY` on create. Unit test `functions/src/core/jobCategoryWrite.test.ts` (7 tests, green).
- [x] **T3.** Functions: `backfillTripJobCategoryFromTask` admin callable (paginated, copy-from-task, default PRIMARY, idempotent, stats) + exported via `index.ts` + wired an amber card on the Utilities page. Removed the old `scripts/backfill-job-category.mjs`.
- [x] **T4.** Web data layer: `TaskInfo.jobCategory` + `resolveJobCategory(trip, task)` in `billing.ts` (single fix point — feeds Billing Document page, Excel, and the invoice PDF remark).
- [x] **T5.** Web display: unverified badge + filter bucket on Billing Document; Income main-table loud marker + Missing-Billing tab task-resolved `jobCategory` column (type + build + header + cell + Excel); `EditTripDetailsDialog` prefill fallback (piggybacked on the existing task fetch in `loadHelperContext`).
- [x] **T6.** i18n en + th: `accounting.billingDocument.badge.jobCategoryUnknown` + `accounting.income.missing.table.jobCategory` (Utilities backfill card uses hardcoded English, matching the existing cards).
- [x] **T7.** Verified `firestore.rules` — the `trip_records` create rule (`:290-293`) has no field allowlist, so the driver-written `jobCategory` is permitted. No rule change.
- [x] **T8.** Updated `.vibe-rules.md` Change Log (2026-08-09).

## 7. Acceptance criteria (ตรวจรับ)

> AC1–AC7 describe **runtime** behavior that needs an emulator/staging with seeded rate cards + tasks
> — not runnable here (no local Firestore access). Ticks marked *(code)* mean "implemented + statically
> verified," not "observed at runtime."

- [x] **AC1. (R1)** *(code)* Mobile writes `trip_records.jobCategory` from `_jobCategory ?? 'PRIMARY'` inside `submitLoadingPhaseRecord`, before the fire-and-forget `computeTripBillingSnapshot`. *(runtime QA pending)*
- [x] **AC2. (R2)** `resolveJobCategoryWrite(undefined, false) === undefined` (omit → untouched) and `(undefined, true) === "PRIMARY"` — covered by 7 green unit tests; `createOrUpdateTask` consumes it.
- [x] **AC3. (R3, R4)** *(code)* `resolveJobCategory(trip, task)` in `billing.ts` returns the task value when the trip's is absent, `undefined` when neither; the Billing Document badge renders เสริม / หลัก / loud "unverified", never a defaulted หลัก. *(runtime QA pending)*
- [x] **AC4. (R3)** *(code)* The invoice PDF remark (`billingDocument.ts:754/778`) and Excel read the row's resolved `jobCategory` from the shared data layer, so a task-เสริม trip prints เสริม. *(runtime QA pending)*
- [x] **AC5. (R5)** *(code)* Income main table renders the loud marker (not `—`) for an uncategorised row; the Missing-Billing tab resolves `d.jobCategory ?? task.jobCategory` into a new column + Excel. *(runtime QA pending)*
- [x] **AC6. (R7)** *(code)* `backfillTripJobCategoryFromTask` skips already-set docs (idempotent), copies from the task, defaults PRIMARY on missing/blank task, returns `{ totalProcessed, updated, copiedFromTask, defaultedPrimary, taskMissing, errors }`. *(runtime QA pending — run from Utilities/console on dev first)*
- [x] **AC7. (R6)** *(code)* `EditTripDetailsDialog` falls back to `task.jobCategory` (via `loadHelperContext`) when `trip.jobCategory` is absent. *(runtime QA pending)*
- [x] **AC8. (N1)** "Unverified" key added to both `en` and `th`; the Missing-Billing header key added to both; no raw key rendered.
- [x] **AC9.** Web `tsc --noEmit` ✅; functions `tsc --noEmit` ✅; ESLint 0 errors (pre-existing warnings only); Vitest **253** pass (246 + 7 new); `dart analyze` on touched mobile files adds 0 new issues (4 pre-existing, unrelated).

## 8. Risks & rollback

| Risk | Mitigation / rollback |
|------|----------------------|
| Old mobile builds keep creating trips without `jobCategory` | The web display fallback (R3–R6) resolves those from the task, so the invoice is still correct; backfill (R7) fixes stored data. Forced update not required. |
| Denormalization drift: admin edits `tasks.jobCategory` after the trip is seeded, before delivery | Task is source of truth; billing refresh (on success) and `setTripJobCategory` reconcile; readers prefer trip then fall back to task, so a stale trip copy is at worst momentarily out of date, never invisible (ADR 0010). |
| Backfill mislabels legacy เสริม as หลัก | Copy-from-task (not blind `PRIMARY`); default `PRIMARY` only when the task has no value; idempotent + dry-run/stats before apply. |
| Rules block the new field on driver create | T7 verifies before shipping mobile; add `jobCategory` to any field allowlist if present. |
| `createOrUpdateTask` change regresses assign flow | UI always sends `jobCategory` today, so behavior is unchanged for the UI path; the change only stops non-UI/omitting callers from resetting. Covered by T2 unit test. |
| Rollback | Web + functions changes are additive/behavioral — revert the resolve + badge + callable + tasks.ts guard. Mobile: revert the field + version bump (older reader ignores the extra field; the web fallback still works). No schema migration to undo. |

## 9. Open questions / follow-ups

- **Version bump:** shipped `3.0.1+1` (patch) per the documented default. Owner may re-tag to a minor.
- **Multi-delivery / manual check-in:** audited — `loading_trip_repository.dart:130` (`.set(merge)`) is the **only** trip-creation site; multi-delivery uses the same `submitLoadingPhaseRecord` call, and a self-created job with no task falls to `'PRIMARY'`. Delivery/standby repositories only *update* existing trips. No other seed wiring needed.
- **Invoice gating:** resolved to **surface, not block** — an unverified row still bills (never silently dropped) and shows the loud marker + its own filter bucket. Gating (block finalizing a bill with unverified rows) left as a follow-up.
- **Old script:** `scripts/backfill-job-category.mjs` **removed**; superseded by the `backfillTripJobCategoryFromTask` callable on the Utilities page.
- **Deploy note:** `backfillTripJobCategoryFromTask` is a new callable — needs `firebase deploy --only functions` before the Utilities button works in any live env. Mobile seed ships with the next APK (ADR 0007 sequencing); the web display fallback covers older builds meanwhile.
- **Runtime QA pending (AC1, AC3–AC7):** exercise on emulator/staging — a task-เสริม trip whose billing failed shows เสริม (not หลัก) on the invoice; a truly orphaned trip shows the loud marker; the backfill copies from tasks. Not runnable here (no local Firestore access).
- `git add -f` required when committing this spec + the ADR + the new `functions/src/core/jobCategoryWrite.ts` is tracked normally (`.md` gitignore only affects the spec/ADR).
