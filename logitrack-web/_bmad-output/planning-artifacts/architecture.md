---
stepsCompleted: [1, 2, 3, 4, 5, 6, 7, 8]
lastStep: 8
status: 'complete'
completedAt: '2026-06-18'
inputDocuments:
  - _bmad-output/planning-artifacts/prds/prd-logitrack-web-2026-06-18/prd.md
  - _bmad-output/planning-artifacts/prds/prd-logitrack-web-2026-06-18/.decision-log.md
workflowType: 'architecture'
project_name: 'logitrack-web'
user_name: 'Smart.dev'
date: '2026-06-18'
feature: 'Driver Compensation (คิดค่าตอบแทนคนขับ)'
---

# Architecture Decision Document — Driver Compensation (คิดค่าตอบแทนคนขับ)

_This document builds collaboratively through step-by-step discovery. Sections are appended as we work through each architectural decision together._

## Project Context Analysis

### Requirements Overview
**Functional Requirements (~24):** config (FR1–4), per-trip base pay (FR5–7), fuel incentive (FR8–10), trip-volume (FR11–12), deductions incl. SSO + installments (FR13–15.1), payout gen/approve/adjust (FR16–19), mobile self-view (FR20–21). Architecturally → one server-side compute engine + effective-dated config + immutable payout snapshots + ledger posting.

**Non-Functional (N1–N6):** i18n EN+TH; auditability/immutability; money precision (whole-THB rounding, no negative net); RBAC + Firestore-rule scoping; batch performance (no timeout); shared compute logic synced lib ↔ functions/src/core.

**Scale & Complexity:** medium–high
- Primary domain: full-stack (Next.js web + Cloud Functions Gen2 + Flutter mobile)
- Complexity level: medium–high (payroll calc with cross-round installment state + dual surface)
- Estimated components: config store, compute engine (pure), payout run orchestrator (Cloud Function), approval/adjustment + transactions poster, web admin UI, mobile read view, Firestore rules

### Technical Constraints & Dependencies
- Firebase Firestore + Cloud Functions Gen2 (asia-southeast1); design for Firestore now but financial data is a Phase-1 SQL-migration candidate (keep compute portable).
- MUST reuse: trip_records, standby_records, fuel km/L logic, working_holiday_calendar, drivers, transactions ledger.
- Project rule: billing/compute logic duplicated across lib/*.ts and functions/src/core/*.ts must stay in sync.

### Cross-Cutting Concerns Identified
i18n (EN+TH) · RBAC + driver-scoped Firestore rules · audit trail & immutable snapshots · monetary precision & rounding · idempotent batch recompute · web/functions logic sync · future SQL portability.

## Starter Template Evaluation

### Primary Technology Domain
Full-stack brownfield feature inside the existing LogiTrack monorepo — **no new starter template applies.** The "starter" is the established stack and conventions.

### Decision: No starter — extend existing stack
**Rationale:** This feature is additive to a mature codebase; introducing a starter would conflict with existing patterns. We build within current foundations.

**Established stack & conventions (the de-facto starter):**
- **Web:** Next.js (App Router) + TypeScript, route group `app/app/**`, feature architecture `features/<domain>/api` + `components`.
- **Backend:** Firebase Cloud Functions Gen2 (Node 22, asia-southeast1), Firestore (rules SSOT `firestore.rules`).
- **Mobile:** Flutter (read-only consumer of payout data).
- **Shared compute rule:** pure logic duplicated across `lib/*.ts` ↔ `functions/src/core/*.ts` kept in sync (billing pattern).
- **i18n:** `context/locales/en|th`. **RBAC:** `lib/capabilities.ts` + `lib/roles.ts`. **Money docs:** `lib/billingDocument.ts` (xlsx-js-style).
- **Testing:** Vitest (web) + `tsc --noEmit` + ESLint in CI; `dart analyze` (mobile).

**New code will live under:** `features/driver-compensation/` (web) + a new functions module (e.g. `functions/src/driverCompensation.ts` + `functions/src/core/compensationCompute.ts` mirrored to `lib/compensationCompute.ts`).

**Note:** No init/scaffold story needed — first implementation story starts directly on data model + compute engine.

## Core Architectural Decisions

### Already decided by existing stack (not re-decided)
Next.js App Router + TS, Firebase Firestore + Functions Gen2, Flutter mobile, Firebase Auth + custom claims (RBAC), feature architecture, i18n EN/TH, transactions ledger, billing-document export.

### Data Architecture
- **D1 Collections (new):**
  - `driver_compensation_config` — one **effective-dated** doc per `effectiveFrom` holding ALL structures (base rates, fuel tiers, trip-volume tiers, SSO rule, penalty types). Atomic lookup like rate cards.
  - `driver_payouts` — one doc per **driver × period × round**, doc id `${driverId}_${period}_${round}`; line items, deductions, net, status, snapshot.
  - `driver_penalties` — **separate** collection: lifecycle independent of runs; supports installments spanning rounds (`amountThb`, `remainingThb`, `installmentsTotal/Paid`, `status`).
  - `drivers` additions: `employmentType`, `ssoBase`, `birthDate`, `hireDate`, `ssoEligible/probationPassed`.
- **D2 Validation:** Zod schema `validate/compensationSchema.ts`.

### Compute Engine
- **D3 Pure module, mirrored:** `lib/compensationCompute.ts` ↔ `functions/src/core/compensationCompute.ts` (synced). Pure fns: `computeBasePay`, `computeFuelIncentive`, `computeTripVolumeIncentive`, `computeSso`, `applyInstallmentDeductions`, `roundTHB`.
- **D4 Orchestration server-side:** callable `generateDriverPayoutRun(period, round)` — batch over drivers, high `timeoutSeconds`, pre-fetch via `Promise.all` (billing-backfill pattern).
- **D5 Idempotent:** recompute overwrites **draft** payouts only; **approved** immutable.
- **D6 Installment state machine:** penalty `pending → partially_deducted → cleared`; each run consumes one installment; net never < 0.

### Money & Ledger
- **D7 On approve →** `transactions` (expense), one entry per driver payout (net) linked by `payoutId`; corrections = linked reversing/adjustment entries; snapshot untouched.
- **D8 Rounding:** whole THB, half-up, line-item then net.

### Security / RBAC
- **D9 Capabilities:** `compensation_manage_config`, `compensation_run_payout`, `compensation_approve`, `compensation_view` (`lib/capabilities.ts` + `lib/roles.ts`).
- **D10 Firestore rules:** config & penalties → admin read/write. `driver_payouts` → write functions/admin only; read = admin OR (driver owns doc AND `status == published`).

### Frontend / Mobile
- **D11 Web:** `features/driver-compensation/` (api + components); pages `app/app/accounting/driver-compensation/` (config, run, review/approve, export).
- **D12 Mobile:** read-only own `driver_payouts` where `status == published`; no compute on device.

### Decision Impact / Sequence
1. Schema + config + drivers fields → 2. pure compute module (mirrored) → 3. payout-run Cloud Function → 4. review/approve + transactions posting → 5. export → 6. mobile self-view + rules.
**Dependencies:** compute ← config shape (D1/D2); ledger posting ← payout snapshot (D5/D7); mobile ← rules (D10).

## Implementation Patterns & Consistency Rules

> Goal: prevent divergent choices between agents. Aligned with existing LogiTrack conventions.

### Naming
- **Firestore collections:** `snake_case` plural-ish nouns — `driver_compensation_config`, `driver_payouts`, `driver_penalties` (matches `trip_records`, `standby_records`).
- **Document fields:** `camelCase` (matches existing docs). Money fields end in `Thb` (e.g. `basePayThb`, `fuelIncentiveThb`, `ssoDeductionThb`, `netThb`). Mirror existing `billingEstimateThb`/`deliveredTimestamp`.
- **Doc IDs:** payout = `${driverId}_${period}_${round}`; `period` = `YYYY-MM`; `round` ∈ `R1` | `R2`.
- **Code (TS):** functions `camelCase` (`computeBasePay`), React components `PascalCase` (`PayoutReviewTable.tsx`), files match export. Mobile Dart `lower_snake_case` files.
- **i18n keys:** namespace `driverCompensation.*` in both `en` and `th`.
- **Capabilities:** `compensation_<verb>` (snake_case, matches `security_view_*`).

### Compute (purity & sync)
- Compute fns are **pure** — NO Firestore reads/writes inside; all inputs (trips, fuel, holidays, config, penalties) passed as args → deterministic & unit-testable.
- A single `roundTHB(n)` helper (half-up, whole baht) used everywhere; never round ad-hoc.
- `lib/compensationCompute.ts` and `functions/src/core/compensationCompute.ts` are **byte-for-byte logically identical** (project sync rule). Change one → change both in the same commit.
- Recompute honors a `forceRecompute` flag but NEVER overwrites `status: approved|published` (mirror billing early-return guard).

### Status enums (fixed vocab)
- `driver_payouts.status`: `draft` → `approved` → `published`.
- `driver_penalties.status`: `pending` → `partially_deducted` → `cleared`.

### Money & time
- Currency stored as whole-baht `number`; net clamped `≥ 0` (overflow → installment balance, FR15.1).
- Time zone **Asia/Bangkok** for period/round boundaries and holiday (Sun + `working_holiday_calendar`) classification; trips assigned by `deliveredTimestamp`.

### Process
- **Errors:** callables throw `HttpsError(code, message)`; client surfaces via existing toast pattern; server logs via functions `logger`.
- **RBAC:** capability enforced **server-side** in each callable AND guarded client-side (page permission guard) — never client-only.
- **Ledger:** approval writes one `transactions` expense per payout (`relatedPayoutId`); corrections write linked reversing entries (`reverses: <txnId>`), never edit posted entries.

### Enforcement — all agents MUST
- Keep the two compute files in sync; route all money through `roundTHB`; never read Firestore inside compute fns; add EN+TH i18n for every string; update `firestore.rules` + `firestore.indexes.json` when adding queries.

## Project Structure & Boundaries

### Directory tree (feature-scoped, brownfield additions)
```
logitrack-web/
├── features/driver-compensation/
│   ├── api/
│   │   ├── config.ts          # read/write driver_compensation_config (effective-dated)
│   │   ├── payouts.ts         # read driver_payouts; call generate/approve callables
│   │   └── penalties.ts       # CRUD driver_penalties (+ installment state)
│   ├── components/
│   │   ├── CompensationConfigEditor.tsx
│   │   ├── PayoutRunPanel.tsx        # pick period+round, run generate
│   │   ├── PayoutReviewTable.tsx     # per-driver breakdown, override, approve
│   │   ├── PenaltyDialog.tsx         # add penalty + installments
│   │   └── PayoutExport.tsx          # Excel/PDF (reuse billingDocument style)
│   └── utils/                        # display helpers (no compute)
├── app/app/accounting/driver-compensation/
│   ├── page.tsx               # overview/config
│   ├── run/page.tsx           # generate + review/approve
│   └── config/page.tsx        # rate/tier/SSO/penalty-type config
├── lib/compensationCompute.ts        # PURE engine (mirror of functions/src/core)
├── validate/compensationSchema.ts    # Zod schemas
├── context/locales/en/driverCompensation.ts
├── context/locales/th/driverCompensation.ts
├── lib/capabilities.ts               # +4 capabilities (edit)
├── lib/roles.ts                      # grant to Admin/Accounting (edit)
├── firestore.rules                   # +rules (edit)
├── firestore.indexes.json            # +indexes (edit)
└── functions/src/
    ├── driverCompensation.ts         # callables: generateDriverPayoutRun, approveDriverPayoutRun, recomputeDriverPayout
    ├── core/compensationCompute.ts   # PURE engine (mirror of lib/)
    └── index.ts                      # export callables (edit)

logitrack-mobile/
└── lib/features/compensation/
    ├── data/repositories/compensation_repository.dart   # read own published payouts
    └── presentation/pages/my_compensation_page.dart     # read-only breakdown
```

### Boundaries & data flow
- **Compute (pure)** ← inputs only → no IO. **Orchestration (Cloud Function)** does all Firestore IO + batching + posting. **Web api layer** wraps Firestore reads + callable invokes. **Mobile** read-only via Firestore rules.
- **Flow:** config + trips/fuel/holidays/standby/penalties → `generateDriverPayoutRun` (compute) → `driver_payouts` (draft) → admin review/override/approve → `transactions` + `status: published` → mobile reads own published payout.

### FR → location mapping
| FR group | Lives in |
|----------|----------|
| Config (FR1–4) | `features/.../api/config.ts`, `config/page.tsx`, `validate/compensationSchema.ts` |
| Base/Fuel/Trip-volume (FR5–12) | `compensationCompute.ts` (both mirrors) |
| Deductions/SSO/installments (FR13–15.1) | compute + `api/penalties.ts` + `PenaltyDialog.tsx` |
| Payout gen/approve/adjust (FR16–19) | `functions/src/driverCompensation.ts`, `PayoutRunPanel/ReviewTable/Export` |
| Mobile self-view (FR20–21) | `logitrack-mobile/lib/features/compensation/` + `firestore.rules` |

## Architecture Validation Results

### Coherence Validation ✅
Decisions are mutually consistent: pure-compute + mirrored files match the established billing pattern; effective-dated config matches rate-card pattern; status/enum and naming align with existing collections; no contradictory choices.

### Requirements Coverage ✅
All FR groups map to concrete components (table above). NFRs covered: i18n (EN+TH keys), auditability (immutable snapshot + adjustment entries), money precision (`roundTHB`, net≥0), RBAC + Firestore rules, batch performance (high-timeout Cloud Function), web/functions sync rule.

### Implementation Readiness ✅ (with minor gaps)
Decisions, patterns, structure are specific enough for consistent agent implementation.

### Gap Analysis
- **Critical:** none blocking.
- **Minor (deferred OQs from PRD):** OQ2 (km/L multi-truck attribution), OQ5 (trip-volume period), OQ6 (slip format), OQ8 (probation flag source), **OQ9 (multi-stop paid vs excluded — most impactful; confirm before the base-pay story)**, OQ10 (mid-cycle hire/termination pro-rating).

### Architecture Completeness Checklist
**Requirements Analysis:** [x] context analyzed [x] scale assessed [x] constraints identified [x] cross-cutting mapped
**Architectural Decisions:** [x] critical decisions documented [x] stack specified [x] integration patterns defined [x] performance addressed
**Implementation Patterns:** [x] naming [x] structure [x] communication [x] process
**Project Structure:** [x] directory tree [x] boundaries [x] integration points [x] FR→structure mapping

### Architecture Readiness Assessment
**Overall Status:** READY WITH MINOR GAPS (6 non-blocking OQs deferred; confirm OQ9 before base-pay implementation)
**Confidence Level:** medium-high
**Key Strengths:** reuses proven billing/rate-card patterns; pure deterministic compute; clean audit/immutability + adjustment model; brownfield-aligned.
**Future Enhancement:** auto SLA penalty detection (PRD Phase 3); SQL portability when migration Phase-1 triggers.

### Implementation Handoff
**AI agents MUST:** follow decisions D1–D12 + patterns exactly; keep the two compute files in sync; EN+TH i18n; update rules/indexes with queries.
**First implementation priority:** schema + `driver_compensation_config` + `drivers` field additions + Zod schema (no scaffold needed).
