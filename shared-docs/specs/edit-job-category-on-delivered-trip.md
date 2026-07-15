# Spec: Edit job category (หลัก/เสริม) on a delivered trip

> **Status:** ✅ Done (code complete + static checks green; runtime QA of AC1–AC7 pending)
> **Owner:** Samart Kas
> **Created:** 2026-07-15
> **Approved:** 2026-07-15
> **Domain:** drivers (Driver Monitor) + accounting/billing (Cloud Functions)
> **Related:** [ADR 0002](../adr/0002-edit-job-category-on-delivered-trip.md); extends [ADR-0006](../../logitrack-web/_bmad-output/planning-artifacts/adr/ADR-0006-explicit-job-category-at-assign.md), realizes the manual-edit escape hatch of [ADR-0005](../../logitrack-web/_bmad-output/planning-artifacts/adr/ADR-0005-supplementary-trips.md)

---

## 1. Problem & Goal (ทำไมต้องทำ)

A trip's **`jobCategory`** (หลัก `PRIMARY` / เสริม `SUPPLEMENTARY`) decides which rate card bills it, whether fuel adjustment applies, and — for เสริม — freezes the price forever. Today it can only be set once at task-assign time; there is **no way to correct it on a trip after delivery**. The motivating case: a trip billed เสริม (frozen at a supplementary price) that should have been หลัก, with no UI to fix it.

Goal: let an admin change หลัก/เสริม on a **delivered** trip from the Driver Monitor, and have the billing price **re-derived** from the correct rate card, atomically.

## 2. Scope

**In scope:**
- A หลัก/เสริม selector in `EditTripDetailsDialog` (Driver Monitor), enabled **only for delivered trips**.
- A new **admin-only** Cloud Function callable that re-derives the price for the chosen category and, on success, writes `task.jobCategory` + the trip billing snapshot **atomically** (nothing written if no matching rate).
- Coverage of **both** single-destination and multi-delivery (J&T multi-stop) trips.
- i18n en + th for the new UI + error/success feedback.

**Out of scope (ทำทีหลัง / ไม่ทำ):**
- Editing category on **non-delivered** trips (category is still set via the assign dialog dropdown).
- Any change to `EditBillingDialog` (income) — its silent try-PRIMARY-then-SUPPLEMENTARY derivation is a known inconsistency, tracked as a follow-up, not fixed here.
- Changing the freeze guards themselves; bulk `forceRecompute` / fuel re-imports must still never move a frozen เสริม price.
- Mobile (Flutter) — no change.

## 3. Requirements

**Functional**
- **R1.** In `EditTripDetailsDialog`, show a หลัก/เสริม `Select` prefilled from `trip.jobCategory` (absent ⇒ default `PRIMARY`). It is **enabled only when `trip.status === "delivered"`**; otherwise hidden or disabled with a hint.
- **R2.** On Save, if the selected category **differs** from the trip's current category **and** the trip is delivered, invoke the new callable `setTripJobCategory({ tripId, jobCategory })`. If unchanged, do nothing (no recompute).
- **R3.** The callable is **admin-only**: reject non-admins with `permission-denied` using the project pattern `request.auth.token.admin === true || request.auth.token["role"] === "admin"`.
- **R4.** The callable re-derives the price for the **explicit target category only** (no PRIMARY↔SUPPLEMENTARY fallback): single trips via `computeTripBillingFromParts`, multi-delivery via `computeMultiDeliveryBilling`, mirroring the branching + `codeToName` destination retry already in `tryWriteBillingSnapshotFromTripData`.
- **R5.** **Atomic:** if no rate entry matches the target category, the callable throws `failed-precondition` (`No <CAT> rate: hub → dest (class)`) and writes **nothing** — the trip keeps its old category and old price.
- **R6.** On success the callable writes, together: `task.jobCategory = <target>` (the source of truth) **and** the trip's billing fields (`billingEstimateThb`, `billingBaseRateThb`, `billingRateImportId`, `billingLookupHubId`, `billingLookupDestination`, `billingFuelAdjustmentId`, `billingRateMultiplier`, `billingAddThbPerTrip`, `billingEffectiveFromDateStr`, `billingCustomerId`, `jobCategory`, `updatedAt`), plus the freeze marker: `billingManualOverride: true` when target is `SUPPLEMENTARY`, `billingManualOverride: false` when target is `PRIMARY` (explicitly cleared so the trip un-freezes).
- **R7.** The dialog surfaces the outcome: success ⇒ close + refresh (`onSuccess`); failure ⇒ a toast/inline error using the "No rate" message, and the trip is left unchanged.
- **R8.** When both route/stops **and** category changed in one Save, apply the task route/stops write **before** `setTripJobCategory`, so the re-derivation runs against the updated route; skip the redundant `computeTripBillingSnapshot` call for that Save (the category callable already recomputes).

**Non-functional** (perf / security / i18n / cost)
- **N1.** i18n complete in both `en` and `th` (label, both options, delivered-only hint, error + success text).
- **N2.** Security: mutation runs server-side via a callable using Admin SDK; the client never writes billing fields for this action. `EditTripDetailsDialog` must not offer the control to customer/partner-scoped viewers of Driver Monitor (gate on admin, consistent with R3).
- **N3.** No new Firestore index (queries by `customerId` on rate/fuel already exist and are reused).
- **N4.** `Select` inside `Dialog` (Radix) must use `className="z-[1005]"` + `position="popper"` so options are clickable above the dialog overlay (`.vibe-rules.md` Key patterns).

## 4. Design

**Data model (Firestore)**
- No new fields. Writes existing fields:
  - `tasks.jobCategory` — the authoritative value (already exists, ADR-0006).
  - `trip_records.jobCategory`, `trip_records.billingManualOverride`, and the `billing*` snapshot fields (all already exist).
- Invariant kept: **task is the source of truth; trip is its derived snapshot.** No trip-level override precedence is introduced.

**Cloud Functions / billing**
- New callable **`setTripJobCategory`** in `functions/src/tripBillingOnDelivered.ts` (auto-exported via `index.ts:27 export * from "./tripBillingOnDelivered"`), region `asia-southeast1`, `enforceAppCheck: false` (matches `computeTripBillingSnapshot`; web admin calls without App Check).
  - Request: `{ tripId: string; jobCategory: "PRIMARY" | "SUPPLEMENTARY" }`.
  - Steps: admin check (R3) → load trip; require `status === "delivered"` → load linked task (`data.taskId`) → if `task.jobCategory` already equals target, return `{ ok: true, skipped: true }` → resolve hub maps + `taskInput` + `customerId` + rate/fuel entries (reuse the existing resolution logic in `tryWriteBillingSnapshotFromTripData`) → **compute** for the explicit target category (single vs multi branch, R4) → if null, `throw new HttpsError("failed-precondition", ...)` (R5) → else write task + trip fields (R6). Prefer a Firestore **batch** (or `Promise.all` best-effort) for the two writes.
  - **Reuse over duplication:** extract the compute portion of `tryWriteBillingSnapshotFromTripData` into a shared helper (e.g. `computeBillingForExplicitCategory(...)`) that both the delivery/backfill path and `setTripJobCategory` call, so the billing math has one implementation. If a clean extraction is impractical, `setTripJobCategory` may re-implement the same two `compute*` calls — but must stay behaviorally identical (same `codeToName` retry, same field mapping).
- ⚠️ **billing sync:** this feature reuses `functions/src/core/billingCompute.ts` (`computeTripBillingFromParts`, `computeMultiDeliveryBilling`, `selectBillingRateEntry`) **unchanged**. No new billing math is added, so no edit to `lib/billingCompute.ts` is expected. **If any change to billing computation becomes necessary, it MUST be mirrored in both `lib/billingCompute.ts` and `functions/src/core/billingCompute.ts`.**
- `computeTripBillingSnapshot` and the freeze guards (`tripBillingOnDelivered.ts:120-129`) are **not modified** — `setTripJobCategory` is the only sanctioned path that moves a frozen price.

**Web (Next.js)**
- `features/drivers/components/EditTripDetailsDialog.tsx`:
  - Add `localJobCategory` state, prefilled from `trip.jobCategory ?? "PRIMARY"`; reset in the existing `useEffect` that resyncs on `trip` change.
  - Render the `Select` in the route/billing section, gated on `trip.status === "delivered"` and on the current user being admin (R1, N2, N4).
  - In `handleSave`, after the existing task route/stops sync, if category changed and delivered → `await httpsCallable(functions, "setTripJobCategory")({ tripId: trip.id, jobCategory: localJobCategory })`; on throw, set an error state / toast; skip the standalone `computeTripBillingSnapshot` call for this save (R8).
- Trip type: ensure the dialog's `trip` prop type carries `jobCategory` (the driver-monitor row type already includes it — see `income/page.tsx:89`; confirm the `TripRecord`/monitor type used here exposes it, add if missing).
- i18n keys (`context/locales/en|th/driverMonitor.ts`, flat-string style like existing `driverMonitor.editTrip.*`):
  - `driverMonitor.editTrip.jobCategoryLabel` — en "Job Category (Primary/Supplementary)" / th "ประเภทงาน (หลัก/เสริม)"
  - `driverMonitor.editTrip.jobCategoryPrimary` — en "Primary (หลัก)" / th "หลัก"
  - `driverMonitor.editTrip.jobCategorySupplementary` — en "Supplementary (เสริม)" / th "เสริม"
  - `driverMonitor.editTrip.jobCategoryDeliveredOnly` — en "Editable after the trip is delivered" / th "แก้ไขได้เมื่อส่งงานสำเร็จแล้ว"
  - `driverMonitor.editTrip.jobCategoryRecomputeHint` — en "Changing this re-computes the billing price from the matching rate card" / th "การเปลี่ยนค่านี้จะคำนวณราคาวางบิลใหม่จาก Rate Card ที่ตรงกัน"
  - `driverMonitor.editTrip.jobCategoryNoRate` — en "No {category} rate card for this route/vehicle/date — nothing changed" / th "ไม่พบ Rate Card ({category}) สำหรับเส้นทาง/ประเภทรถ/วันที่นี้ — ยังไม่มีการเปลี่ยนแปลง"
  - `driverMonitor.editTrip.jobCategorySaved` — en "Job category updated and price re-computed" / th "อัปเดตประเภทงานและคำนวณราคาใหม่แล้ว"

**Mobile (Flutter)** — none.

**Firestore Rules** — no change. Rules already permit web admin to update `tasks`/`trip_records`; the callable uses Admin SDK regardless (bypasses rules) and enforces admin in code (R3).

## 5. Affected files

- `logitrack-web/functions/src/tripBillingOnDelivered.ts` — new `setTripJobCategory` callable (+ optional extraction of a shared compute helper).
- `logitrack-web/features/drivers/components/EditTripDetailsDialog.tsx` — selector + save wiring.
- `logitrack-web/context/locales/en/driverMonitor.ts` — new keys.
- `logitrack-web/context/locales/th/driverMonitor.ts` — new keys.
- (verify) the trip/monitor row type feeding the dialog exposes `jobCategory`.
- `shared-docs/.vibe-rules.md` — Change Log entry (project rule).

## 6. Task breakdown

- [x] **T1.** Add `setTripJobCategory` callable (admin check, delivered check, compute-for-explicit-category, atomic task+trip write, throw on no-rate). Self-contained re-implementation reusing `core/billingCompute.ts` (clean extraction was impractical without modifying the delivery/backfill path, which the spec forbids — the spec's allowed fallback was taken).
- [x] **T2.** Wire the หลัก/เสริม `Select` into `EditTripDetailsDialog` (delivered-only + admin-only via `isAdmin`, `z-[1005]`/`position="popper"`), state prefill/reset in the on-open effect.
- [x] **T3.** Save flow: call the callable on category change after route/stops sync; success toast + refresh, error toast (localized no-rate vs raw message); added `categoryChanged` to the early-return guard so a category-only edit still fires; skip the redundant `computeTripBillingSnapshot` (route + stops) on that save (R8).
- [x] **T4.** Add i18n keys to en + th driverMonitor locales (`driverMonitor.editTrip.jobCategory*`).
- [x] **T5.** Update `.vibe-rules.md` Change Log (2026-07-15).

## 7. Acceptance criteria (ตรวจรับ)

> Verified statically below (AC9 + code inspection). **AC1–AC7 describe runtime behavior that needs
> manual/emulator QA against seeded rate cards — not exercisable in this build environment.** The code
> paths are implemented as specified; ticks marked *(code)* mean "implemented + statically verified,"
> not "observed at runtime."

- [x] **AC1. (R1)** *(code)* Selector renders only for `canEditCategory` (admin), `disabled` unless `trip.status === "delivered"`, prefilled from `trip.jobCategory ?? "PRIMARY"`, with delivered-only vs recompute hint.
- [x] **AC2. (R4,R6)** *(code)* Single/multi compute for target category → batch writes `task.jobCategory` + trip snapshot + `billingManualOverride: false` for PRIMARY. *(runtime QA pending)*
- [x] **AC3. (R5)** *(code)* `computeForCategory(target)` returns null ⇒ `throw noRateError()` before any write; UI shows localized `jobCategoryNoRate`. *(runtime QA pending)*
- [x] **AC4. (R4)** *(code)* SUPPLEMENTARY path sets `billingManualOverride: true`; the freeze guards in `computeTripBillingSnapshot`/backfill are untouched, so bulk force-recompute still skips it. *(runtime QA pending)*
- [x] **AC5. (R4 multi)** *(code)* `isMultiDelivery && deliveryStopsProgress.length >= 2` branch calls `computeMultiDeliveryBilling` with the explicit category + extra-stop fee, mirroring the delivery path write shape. *(runtime QA pending)*
- [x] **AC6. (R3,N2)** *(code)* Callable throws `permission-denied` unless `token.admin === true || token.role === "admin"`; selector gated on `isAdmin(customClaims)`. `permissions.test.ts` (26 tests) green.
- [x] **AC7. (R2)** *(code)* `categoryChanged` requires a real diff + delivered + admin; unchanged category never calls the callable and (alone) hits the early-return.
- [x] **AC8. (N1)** *(code)* 7 keys added to both `en` and `th` driverMonitor locales; no raw keys referenced in the component.
- [x] **AC9.** `tsc --noEmit` **web + functions** pass; `npm run build` (functions) EXIT 0; ESLint 0 errors (pre-existing warnings only); Vitest **110/110** pass.

## 8. Risks & rollback

| Risk | Mitigation / rollback |
|------|----------------------|
| Re-derive overwrites a legitimately hand-set price on the edited trip | By design (category is the input); callable only fires when category actually changes. Admin can re-set the price via `EditBillingDialog` afterward. |
| Task has >1 billable trip (not enforced) ⇒ writing `task.jobCategory` affects siblings on their next recompute | Normal flow is 1:1; delivered+frozen siblings are skipped by the freeze guard, cancelled siblings have no billing. Documented in ADR 0002; acceptable. |
| Freeze bypass abused | Bypass lives only in this admin-gated callable with a single explicit intent; the guards in `computeTripBillingSnapshot`/backfill are untouched. |
| Two-write non-atomicity (task written, trip write fails) | Compute-first guarantees no write on the common failure (no rate). Use a batch for the two writes; on partial failure, re-running the action is idempotent (recomputes to the same result). |
| Rollback | Feature is additive: revert the callable + dialog + locale changes. No schema/migration to undo; existing `jobCategory`/freeze semantics unchanged. |

## 9. Open questions / follow-ups

- **Runtime QA pending (AC1–AC7):** exercise against a Firebase emulator (or staging) with seeded PRIMARY + SUPPLEMENTARY rate cards — verify เสริม→หลัก un-freeze/re-price, the no-rate atomic no-op, หลัก→เสริม freeze survival under force-recompute, and a multi-stop trip. Not runnable in the build environment used here.
- Deploy note: `setTripJobCategory` is a new callable — needs `firebase deploy --only functions` before the UI works in any live env.
- Reconcile `EditBillingDialog` (income) silent category derivation with explicit task-level `jobCategory` — separate spec.
- Should the Driver Monitor row or income table show a subtle "manually re-categorized" marker for audit? Deferred.
- `git add -f` required when committing this spec (repo's broad `*.md` gitignore).
