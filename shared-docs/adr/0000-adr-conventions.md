# ADR 0000 — ADR & glossary conventions (one standard for decision records)

- **Status:** Accepted (2026-07-15)
- **Deciders:** Samart Kas (product owner), Claude
- **Area:** whole platform (logitrack-web, logitrack-mobile, Cloud Functions, shared-docs)

## Context

Architecture Decision Records (ADRs) were being produced by two different tools that wrote to two
different places, with two different naming schemes and two separate glossaries:

- **`shared-docs/adr/`** + **`shared-docs/glossary.md`** — written by the `/grill-with-docs` skill
  (a grilling/interview session that sharpens a decision and records it). Filenames like
  `0001-checkin-time-on-trip-records.md`. Platform-level scope (web + mobile + functions).
- **`logitrack-web/_bmad-output/planning-artifacts/adr/`** + its own `glossary.md` — produced as a
  by-product of the BMAD planning pipeline (PRD → architecture → epics → stories) for the
  driver-compensation module. Filenames like `ADR-0001-helper-pay-data-model.md`.

The two numbering namespaces **collide** (both start at `0001` for unrelated decisions), the two
glossaries can drift, and referring to "ADR-0001" is ambiguous. This ADR fixes the standard so every
future decision record has one home, one format, and one shared vocabulary — regardless of which
skill or workflow authored it.

## Decision

**`shared-docs/adr/` is the single canonical home for architecture decision records, and
`shared-docs/glossary.md` is the single canonical glossary.** ADRs are inherently cross-cutting
(most touch web + mobile + functions), so they live at the platform level, next to the spec workflow
that `CLAUDE.md` already blesses (`shared-docs/specs/`).

### File & numbering rules

1. **Location:** every new ADR goes in `shared-docs/adr/`.
2. **Filename:** `NNNN-kebab-case-title.md` — four-digit zero-padded number, **no** `ADR-` prefix
   (e.g. `0002-fuel-adjustment-effective-date.md`).
3. **Numbers are monotonic and permanent.** Take the next unused number. **Never reuse and never
   renumber** an existing ADR — other docs link to it by number.
4. **Never delete an ADR.** To reverse a decision, write a new ADR and set the old one's status to
   `Superseded by NNNN`.

### Required structure (every ADR)

A top `# ADR NNNN — <title>` heading, then a metadata block:

- **Status:** one of `Proposed` · `Accepted (YYYY-MM-DD)` · `Superseded by NNNN (YYYY-MM-DD)` ·
  `Deprecated (YYYY-MM-DD)`. Optionally add `— implementation pending` while code hasn't shipped.
- **Deciders:** who made the call.
- **Area:** which parts of the system it touches.

Then these sections, in order:

- **Context** — the problem, the facts established, the constraints. Ground claims in real code with
  `path:line` references so the next reader can verify, not guess.
- **Decision** — what we chose, as numbered points.
- **Consequences** — positive, negative/risks, and follow-ups.
- **Alternatives considered** — what was rejected and *why*.
- **Related** — links to the glossary, the spec, and any superseded/related ADRs.

### Glossary rules

1. **One glossary:** `shared-docs/glossary.md`. Add a term whenever a discussion turns on what a word
   precisely means. Ground each term in the data model / code.
2. Link related terms with `[[wiki-style]]` names. ADRs link into the glossary; the glossary links
   back to the ADR that defined a term.

### How this fits the workflow

`ADR (why)` → `spec in shared-docs/specs/ (what to build)` → `code`. An ADR records the *decision*;
the spec (`/spec-new`, `/spec-build`) records *what to build*; neither replaces the other. The
`/grill-with-docs` skill is the go-forward tool for producing ADRs, because it already writes here in
this format.

### Legacy BMAD ADRs

The six ADRs under `logitrack-web/_bmad-output/planning-artifacts/adr/` (`ADR-0001`..`ADR-0006`) are
**kept in place as historical, module-scoped records** for driver-compensation. They are a **separate
numbering namespace** — their `0001` is *not* this folder's `0001`. They are **not** renumbered or
moved (they cross-link each other by relative `./ADR-000N` paths, and BMAD epics/stories reference
them). New cross-cutting decisions go here, in `shared-docs/adr/`. See [README.md](README.md) for the
index that spans both.

## Consequences

- **Positive:** one place to look, one naming scheme, one glossary; "ADR NNNN" is unambiguous within
  the canonical set; `/grill-with-docs` output already conforms.
- **Negative / risks:** the legacy BMAD set still uses the `ADR-` prefix and its own numbers, so the
  index must spell out the two namespaces to avoid confusion. Folding the BMAD glossary terms into
  `shared-docs/glossary.md` is left as incremental cleanup, not a one-shot migration.
- **Follow-up:** the `/grill-with-docs` SKILL.md was updated to pin this path and structure so results
  stay consistent across runs.

## Alternatives considered

- **Physically migrate the six BMAD ADRs into `shared-docs/adr/` (renumber 0002–0007, fix links).**
  Rejected as default: breaks their internal `./ADR-000N` cross-links and the BMAD epics/stories that
  cite them, for little gain over an index that links out to them. Can be revisited if the BMAD
  output folder is ever retired.
- **Keep two parallel systems and just document both.** Rejected: leaves the numbering collision and
  glossary drift unaddressed.

## Related

- Index: [README.md](README.md) — canonical ADRs + legacy BMAD ADRs.
- Glossary: [../glossary.md](../glossary.md).
- Spec workflow: `shared-docs/specs/` (`/spec-new`, `/spec-build`), see `CLAUDE.md`.
- First canonical ADR: [0001-checkin-time-on-trip-records.md](0001-checkin-time-on-trip-records.md).
