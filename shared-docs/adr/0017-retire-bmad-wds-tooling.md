# ADR 0017 — Retire the BMAD/WDS tooling vendored into the repo

- **Status:** Accepted (2026-08-09)
- **Deciders:** Samart Kas (product owner), Claude
- **Area:** repo hygiene / developer tooling (logitrack-web), decision-record conventions (shared-docs)

## Context

Two AI-assisted planning toolkits had been installed **inside the web app** and committed to the repo:

- **BMAD** — a PRD → architecture → epics → stories pipeline, plus a large agent/skill library.
- **WDS** — a UX/agentic-development skill set that ships with BMAD.

They lived under `logitrack-web/.claude/skills/` (`bmad-*` + `wds-*`), `logitrack-web/_bmad/` (the
framework), and `logitrack-web/.agents/` (a WDS skill copy for another agent runtime). Facts
established while grilling this decision (2026-08-09):

- **Footprint.** ~261 files were git-tracked, but the broad `*.md` `.gitignore` rule meant most of the
  install was untracked-on-disk: the actual on-disk count was **~2,141 files** (`.claude/skills` 1,517
  + `.agents` 580 + `_bmad` 44). Only force-added files showed in `git ls-files`.
- **Nothing depends on it.** `git grep` for `_bmad` / skill paths across `app/`, `lib/`, `features/`,
  `functions/src/`, `components/` returned zero hits. No `package.json`, `*.config.*`, `tsconfig`, or
  CI workflow wires into it. It is not imported by the Next.js build or the Cloud Functions build.
- **It had already broken CI once.** On 2026-07-23 ESLint failed on 14 errors, *none in app code* —
  all from this vendored tooling — which skipped the Deploy job and stopped dev auto-deploy from
  2026-07-17. The fix (see `CLAUDE.md` §45) was to add `_bmad/**`, `.claude/**`, `.agents/**` to the
  ESLint `globalIgnores`. Vendored code that isn't app code but carries its own toolchain is a
  standing CI liability.
- **It writes decision records that collide with the canonical set.** The BMAD pipeline produced ADRs
  and a glossary under `logitrack-web/_bmad-output/planning-artifacts/`, in a **separate numbering
  namespace** (`ADR-0001`..`ADR-0006`) that collides with the canonical `shared-docs/adr/` set.
  [ADR 0000](0000-adr-conventions.md) unified the *format* and *home* but deliberately **kept the BMAD
  ADRs in place**, explicitly noting the migration "can be revisited if the BMAD output folder is ever
  retired." This ADR is that revisit.
- **The `_bmad-output` artifacts document a live feature.** PRD, architecture, epics, story-3.6,
  decision-log, and the six ADRs record the **driver-compensation** module, which is merged to `main`
  and partly still in progress (S3.4/S3.5, fuel-incentive stub, E4 mobile). Their design rationale and
  remaining backlog are worth keeping.
- **The skills are also installed globally.** `~/.claude/skills/bmad-*` exists independently of the
  repo copy, so removing the vendored files does not remove BMAD from the machine's skill menu.

`/grill-with-docs` (which writes to `shared-docs/adr/` + `shared-docs/glossary.md`) is the tool
`CLAUDE.md` and [ADR 0000](0000-adr-conventions.md) already bless for producing decision records. The
BMAD ADR/glossary output is therefore **redundant** with it.

## Decision

1. **Remove the BMAD/WDS method tooling from the repo** — delete `logitrack-web/.claude/skills/`,
   `logitrack-web/_bmad/`, and `logitrack-web/.agents/` (tracked files via `git rm`, untracked
   remnants via `rm`). **Keep** `logitrack-web/.claude/settings.json` and `settings.local.json` — real
   local config, not BMAD.
2. **Migrate the six BMAD ADRs into the canonical set**, preserving content, cross-links, and the
   supersede chain: `ADR-0001..0006` → [0011](0011-helper-pay-data-model.md),
   [0012](0012-helper-day-window.md), [0013](0013-payroll-lineitem-breakdown.md),
   [0014](0014-cash-advance.md), [0015](0015-supplementary-trips.md),
   [0016](0016-explicit-job-category-at-assign.md). Each carries a provenance note; original decision
   dates are preserved. This **reverses the "kept in place" sub-decision** of
   [ADR 0000](0000-adr-conventions.md) (its core decision — one canonical home — stands and is
   reinforced).
3. **Fold the BMAD glossary into the canonical glossary** (`shared-docs/glossary.md`), under a *Driver
   compensation* section, with links repointed to 0010–0016.
4. **Relocate the non-redundant planning artifacts** (not something `/grill-with-docs` produces) to a
   neutral home `shared-docs/driver-compensation/`: `prd.md`, `architecture.md`, `epics.md`,
   `decision-log.md`, `review-quality.md`, `stories/story-3.6-helper-training-pay.md`. Then delete
   `logitrack-web/_bmad-output/` entirely.
5. **Scope is the repo only.** The machine-global `~/.claude/skills/bmad-*` and `wds-*` are left
   untouched — this decision does not remove BMAD from the developer's machine, only from the project.
6. **`/grill-with-docs` remains the go-forward tool** for ADRs and glossary entries, per
   [ADR 0000](0000-adr-conventions.md).
7. **Drop the now-dead ESLint ignores** (`_bmad/**`, `.agents/**`, `.claude/**`) from
   `logitrack-web/eslint.config.mjs` — the directories they guarded are gone.

## Consequences

- **Positive:**
  - ~2,141 vendored files leave the working tree; the repo can no longer be broken by tooling that
    isn't app code.
  - One ADR home and one glossary — the numbering collision that [ADR 0000](0000-adr-conventions.md)
    had to document away is now actually gone. "ADR NNNN" is unambiguous.
  - The driver-compensation design record and backlog survive, in a home that doesn't read as "BMAD
    method output."
- **Negative / risks:**
  - BMAD/WDS skills **still appear in the skill menu** from the global install, and the WDS `sync`
    skill can re-vendor `~/.claude/commands/` if invoked. Removing them from the machine is a separate,
    opt-in action (decision 5).
  - Nothing prevents a future BMAD run from recreating `logitrack-web/_bmad-output/`. A `.gitignore`
    entry for `**/_bmad-output/` and `**/_bmad/` could guard against accidental re-commit; **not added
    here** to avoid masking an intentional future use — flagged as a follow-up.
  - These docs are `*.md`, caught by the broad `.gitignore` rule; the new files must be committed with
    `git add -f`.
- **Follow-ups:**
  - Update `shared-docs/adr/README.md`, [ADR 0000](0000-adr-conventions.md), and `CLAUDE.md` to reflect
    the migration (done alongside this ADR).
  - The user runs their own commit/deploy; the staged `git rm`/`git mv` plus the new files are prepared,
    not committed.

## Alternatives considered

- **Keep the tooling, just `git untrack` it.** Rejected: it would still sit on disk (loading as
  `logitrack-web:bmad-*` skills) and still risk being re-added; it doesn't achieve "remove from the
  project."
- **Delete the `_bmad-output` artifacts too.** Rejected by the owner: they document shipped code and a
  live backlog; losing the rationale for driver-compensation billing/pay decisions is a real cost.
- **Leave the BMAD ADRs in place (the original [ADR 0000](0000-adr-conventions.md) stance).** Rejected
  now that the folder is being retired — the precondition [ADR 0000](0000-adr-conventions.md) named
  ("if the BMAD output folder is ever retired") is met, and keeping a second ADR namespace alive after
  its folder is gone makes no sense.
- **Remove the global skills too (machine-wide).** Deferred to the owner (decision 5) — it affects
  every project on the machine, not just this repo.

## Related

- Supersedes the "Legacy BMAD ADRs / kept in place" sub-decision of
  [ADR 0000](0000-adr-conventions.md) (core conventions unchanged).
- Migrated ADRs: [0011](0011-helper-pay-data-model.md)–[0016](0016-explicit-job-category-at-assign.md).
- Relocated artifacts: `shared-docs/driver-compensation/` (PRD, architecture, epics, stories,
  decision-log, review-quality).
- Glossary: [../glossary.md](../glossary.md) (*Driver compensation* section folded in).
- Index: [README.md](README.md).
