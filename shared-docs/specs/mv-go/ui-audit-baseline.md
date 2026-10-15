# UI audit baseline (UI1, issue #141)

The scored starting point of the UI passes UI3-UI7 (epic #140). Each pass re-measures its page group with the same
method and puts the before (this file) and after in its pull request.

| | |
|---|---|
| **Measured on** | `dd8da7df` (branch `mv-go-UI1-design-tokens`: mv-go `f852e39a` + the UI1 tokens, layering scale and `components/ui` baseline). Counts for `f852e39a` itself are given beside it. |
| **Date** | 2026-10-14 |
| **Scope** | `logitrack-web`: 73 pages (`app/**/page.tsx`), 293 `.tsx`/`.css` files under `app/`, `components/`, `features/` (tests excluded) |
| **Method** | The `impeccable` audit (five dimensions, 0-4 each, P0-P3 findings), run as a code-based audit: PRODUCT.md does not exist yet (hitl, the owner runs `/impeccable teach`), so `impeccable` allows only narrow evaluation and no critique. Evidence: (1) `logitrack-web/scripts/ui-audit-counts.mjs` (heuristic counts per page group, below); (2) the impeccable detector over the source (`impeccable detect app components features hooks`) and over the rendered public pages; (3) a production build (`next build`, standalone) against a stand-in Go upstream (JWKS, an admin `GET /v1/me`, a customers page, `{data: []}` elsewhere) with Playwright screenshots at 1280 and 360 px, light and dark; (4) reading the flagged files. |
| **Re-measure** | `cd logitrack-web && node scripts/ui-audit-counts.mjs . --json out.json` and `impeccable detect --json <files of the group>`. |

## Audit health scores

0-4 per dimension (impeccable bands: 18-20 excellent, 14-17 good, 10-13 acceptable, 6-9 poor, 0-5 critical).

| Group (pass) | Pages | A11y | Perf | Theming | Responsive | Integrity | **Total** | Band |
|---|---|---|---|---|---|---|---|---|
| Shared primitives `components/ui`, shared components (UI1) | - | 3 | 3 | 3 | 3 | 4 | **16/20** | Good |
| App shell, sign-in, navigation (UI3) | 7 | 2 | 3 | 2 | 2 | 2 | **11/20** | Acceptable |
| Master data (UI4) | 23 | 2 | 2 | 1 | 2 | 1 | **8/20** | Poor |
| Operations (UI5) | 6 | 3 | 3 | 2 | 2 | 3 | **13/20** | Acceptable |
| Accounting, billing, maintenance, utilities (UI6) | 12 | 2 | 3 | 2 | 2 | 3 | **12/20** | Acceptable |
| HR, comms, security center, settings, dashboard, public pages (UI7) | 25 | 2 | 3 | 1 | 2 | 1 | **9/20** | Poor |

Page groups follow the issue scopes: UI3 = `app/layout`, `app/app/{layout,page,unauthorized}`, `app/(auth)/**`,
`app/my-account`, the sidebars, header, tenant and language switchers, `features/{auth,platform}`; UI4 = customers,
subcontractors, companies, company profile, sources, trucks/*, renewals, truck assignment, drivers (not the monitor),
`components/map`; UI5 = driver monitor, first mile, line haul, job assign, standby, incidents, the import dialogs;
UI6 = `app/app/{accounting,maintenance,utilities}`, `features/{accounting,maintenance}`, `components/accounting`;
UI7 = dashboard, payroll, leave, holidays, chat, security center, users, waitlist, `app/{page,join-network,about,support}`,
`components/landing`. The exact patterns are in `scripts/ui-audit-counts.mjs`.

### Implementation integrity verdict

**Fail for the pages, pass for the primitives.** The primitives now express one system (tokens, one accent, one
layering scale, guard tests), but the pages do not use it yet: 1,330 palette classes (`bg-blue-600`,
`text-slate-500` ...) and 52 hex values stand in for tokens, the shell carries controls that do nothing, and the
public pages keep the AI-template tells (icon tiles above headings, an uppercase kicker above the hero title, glass
and glow). Detector: 1 source finding (`side-tab`, `features/holidays/components/HolidaysPage.tsx:409`, `border-l-4`)
and 19 on the rendered `/` and `/join-network` (below).

## Counts per page group

Heuristic counts (each is a candidate to verify in context). `base` = mv-go `f852e39a`, `UI1` = `dd8da7df`.

| Metric | Shared | UI3 | UI4 | UI5 | UI6 | UI7 | **Total UI1** | Total base |
|---|---|---|---|---|---|---|---|---|
| Files / pages | 45 / 0 | 28 / 7 | 74 / 23 | 23 / 6 | 41 / 12 | 82 / 25 | 293 / 73 | 293 / 73 |
| Gradients (`bg-gradient-*`, `*-gradient()`) | 0 | 0 | 0 | 0 | 0 | 3 | **3** | 3 |
| `backdrop-blur` (glassmorphism) | 0 | 1 | 3 | 0 | 0 | 1 | **5** | 8 |
| Glow (`blur-xl+`, coloured shadows) | 0 | 0 | 3 | 0 | 0 | 0 | **3** | 6 |
| Inline `style={{}}` (all; shared ones are data-driven) | 8 | 0 | 1 | 1 | 1 | 3 | **14** | 21 |
| Oversized shadows (`shadow-xl`, `shadow-2xl`) | 0 | 1 | 0 | 0 | 0 | 4 | **5** | 8 |
| Oversized radius (`rounded-2xl`, `rounded-3xl`) | 0 | 0 | 0 | 0 | 0 | 7 | **7** | 9 |
| Arbitrary `z-[...]` | 0 | 0 | 0 | 0 | 0 | 0 | **0** | 5 |
| Palette colour classes instead of tokens | 4 | 27 | 644 | 126 | 152 | 377 | **1,330** | 1,341 |
| Hex colours | 0 | 1 | 0 | 2 | 0 | 49 | **52** | 58 |
| Raw `<button>` / `<input>` / `<textarea>` / `<select>` / `<table>` | 3/2/1/3/3 | 9/2/0/0/0 | 17/0/1/0/1 | 0/0/0/0/0 | 3/0/0/0/0 | 17/1/0/0/0 | **49/5/2/3/4** | same |
| Icon-only buttons without a name | 8 | 0 | 22 | 0 | 5 | 2 | **37** | 37 |
| `<img>` without `alt` | 0 | 0 | 0 | 1 | 0 | 0 | **1** | 1 |
| `Input` without `id` / `aria-label` (label not associated) | 1 | 0 | 24 | 4 | 46 | 9 | **84** | 84 |
| Contrast risks (`text-slate/gray-300/400`, `text-*/NN`) | 1 | 2 | 7 | 0 | 0 | 9 | **19** | 19 |
| Emoji in UI (incl. flags) | 0 | 11 | 3 | 0 | 1 | 19 | **34** | 34 |
| Fixed widths over 360 px | 0 | 0 | 1 | 2 | 1 | 0 | **4** | 4 |
| Hand-written `dark:` variants | 31 | 5 | 105 | 28 | 10 | 73 | **252** | 252 |
| `motion-reduce` / reduced-motion handling | 0 | 0 | 0 | 0 | 0 | 0 | **0** | 0 |
| Hover scale on cards and tiles | 0 | 0 | 9 | 0 | 0 | 3 | **12** | 13 |

### UI4 after measurement

Measured 2026-10-15 on mv-go `7d04c8a7` with `cd logitrack-web && node scripts/ui-audit-counts.mjs .`. This source-counter
report uses the shared ratchet metric names in `scripts/ui-audit-metrics.mjs`; it is separate from the UI1 audit scores
above.

| Group | Files / pages | `paletteColors` | `hexOrRgb` | `tinyText` | `uppercaseClass` | `sideStripe` | `blurGlow` | `invalidHslVar` |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| UI1 shared | 47 / 0 | 2 | 0 | 0 | 0 | 0 | 0 | 0 |
| UI3 shell, sign-in, navigation | 36 / 8 | 0 | 0 | 2 | 0 | 0 | 0 | 0 |
| UI4 master data | 76 / 23 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| UI5 operations | 23 / 6 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| UI6 accounting, billing, maintenance, utilities | 42 / 12 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| UI7 HR, comms, security, settings, dashboard, public | 84 / 24 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| **Total** | **308 / 73** | **2** | **0** | **2** | **0** | **0** | **0** | **0** |

In the shared code, the three native `<select>`s are inside `components/ui/datetime-picker.tsx` (allowed there by
the UI2 guard), the raw `<table>` is `VirtualTable`, and every remaining inline style is computed from data (virtual
row offsets, image pan and zoom, progress value).

### Detector on the rendered public pages (production build, `dd8da7df`)

`/`: low contrast 2.6:1 (`#90a1b9` = slate-400 on white, 3 places) and 2.4:1 on `#f1f5f9`; 4.3:1 (`#62748e` on
`#f1f5f9`, advisory); 11 px body text; three 48 px icon tiles stacked above `h3`s; an uppercase kicker above the
`h1`; Geist as 100 % of the text (accepted: the owner's pairing, §10.11). `/join-network`: three icon tiles above
`h3`s, 2.6:1 slate-400 text, 4.3:1 `#dbeafe` on `#155dfc`, `h2` followed by `h4`. (`/login` could not be scanned: the
detector's page navigated away under it.)

## Executive summary

- **Audit health:** shared 16, UI3 11, UI4 8, UI5 13, UI6 12, UI7 9 (of 20).
- **Findings:** P0 0, P1 9, P2 10, P3 4.
- **Top issues:** (1) visible labels not tied to their inputs (84 candidates, verified in UI6's expense dialog);
  (2) `<html lang>` is always `en`; (3) 37 icon-only buttons without a name; (4) no language switch in the app
  header below 640 px; (5) 1,330 palette classes and 52 hex values bypass the tokens, so the accent, status colours
  and dark mode drift page by page.
- **Next steps:** each pass fixes its P0/P1 first (`/impeccable harden`, `/impeccable adapt`), then moves its pages
  onto the tokens (`/impeccable quieter`, `/impeccable distill`, `/impeccable layout`, `/impeccable typeset`), and
  ends with `/impeccable polish`. The passes need PRODUCT.md first.

## Findings by severity

### P1 Major

- **[P1] Labels not associated with inputs.** *Location:* 84 `Input`s without `id`/`aria-label` outside a
  `FormControl`; e.g. `features/accounting/components/go/AddExpenseDialog.tsx:148-193` (`<Label>` with no `htmlFor`,
  `<Input>` with no `id`), `features/maintenance/components/maintenance/MaintenanceForm.tsx` (9),
  `features/accounting/components/go/rateCard/*` (17), `features/trucks/components/renew/RenewalForm.tsx` (7).
  *Category:* Accessibility. *Impact:* screen readers announce "edit text" with no name; clicking the label does not
  focus the field. *WCAG:* 1.3.1, 4.1.2. *Fix:* `htmlFor`/`id` pairs or `FormField` + `FormControl`. *Command:*
  `/impeccable harden`. *Pass:* UI6 (46), UI4 (24), UI7 (9), UI5 (4).
- **[P1] `<html lang>` never follows the language.** *Location:* `app/layout.tsx` (`lang="en"`), `context/language.tsx`
  (no `document.documentElement.lang` update). *Impact:* Thai text is read with English pronunciation rules; Thai
  hyphenation and font selection by the browser are off. *WCAG:* 3.1.1. *Fix:* set `lang` on toggle and from the
  stored language before paint. *Command:* `/impeccable harden`. *Pass:* UI3.
- **[P1] Icon-only buttons without an accessible name.** *Location:* `components/ui/file-viewer.tsx` (7: reset, zoom
  in/out, download, close, previous, next), `components/accounting/ImagePreviewGallery.tsx` (5), customers new/edit
  back and remove buttons, trucks lists and uploader (22 in UI4), chat and holiday dialogs (2), the shell's
  notification bell (`app/app/layout.tsx`). *WCAG:* 4.1.2, 2.4.4. *Fix:* `aria-label` from en/th keys. *Command:*
  `/impeccable harden`. *Pass:* UI4, UI6 (the two viewers are shared; take them in whichever pass lands first), UI3, UI7.
- **[P1] No language switch on phones in the app.** *Location:* `app/app/layout.tsx` (`hidden sm:flex` on the EN/TH
  toggle; the sidebar has none). *Impact:* below 640 px a user cannot change the language of `/app`. *Fix:* move the
  toggle into the user menu or the sidebar footer. *Command:* `/impeccable adapt`. *Pass:* UI3.
- **[P1] Shell controls that do nothing.** *Location:* `app/app/layout.tsx`: the dashboard header's "Search data..."
  and the security overview's "Search security logs..." inputs (no handler, no label), "Generate Report" (no
  `onClick`, hard-coded `bg-blue-600`), the notification bell with a permanent red dot. *Category:* Implementation
  integrity. *Impact:* users try them and nothing happens; the red dot claims unread notifications that do not exist.
  *Fix:* remove until the features exist. *Command:* `/impeccable distill`. *Pass:* UI3.
- **[P1] Low-contrast text on the public pages.** *Location:* `components/landing/Hero.tsx` (4), `Footer.tsx` (2),
  `app/join-network/page.tsx` (2): `text-slate-400` on white (2.6:1) and on slate-100 (2.4:1); 11 px body text.
  *WCAG:* 1.4.3. *Fix:* `text-muted-foreground` (5.5:1), 12 px minimum. *Command:* `/impeccable typeset`. *Pass:* UI7.
- **[P1] Status and accent colours bypass the tokens.** *Location:* 1,330 palette classes; hotspots
  `features/subcontractors/components/SubcontractorDetail.tsx` (167), `features/trucks/components/TruckPreview.tsx`
  (71), `NewSubcontractorWizard.tsx` (67), the operations `opsShared.tsx` (41), landing (hex `#1c2127`, `#283039`,
  `#3b4754`), `components/navigation.tsx` (`bg-[#020817]`), the shell header (`border-b-zinc-700/50`, orange avatar
  fallback). *Category:* Theming. *Impact:* the one-accent rule cannot hold, dark mode is hand-written per page (252
  `dark:` variants) and drifts; primary buttons are three different blues. *Fix:* tokens (`bg-primary`,
  `text-muted-foreground`, `bg-success`, `bg-warning`, `bg-destructive`). *Command:* `/impeccable quieter`.
  *Pass:* every pass, largest in UI4 and UI7.
- **[P1] Glassmorphism, glow and oversized surfaces in pages.** *Location:* `backdrop-blur` in the three wizard footers
  (`NewDriverForm`, `NewSubcontractorWizard`, `NewTruckWizard`), `features/auth/components/LoginForm.tsx`
  (`bg-background/95 backdrop-blur-sm shadow-xl`), `app/join-network/page.tsx`; glow in `truck-assignment`,
  `SubcontractorDetail`, `SubcontractorsListDashboard`; `shadow-xl/2xl` (5) and `rounded-2xl/3xl` (7) on the
  join and about pages; a decorative gradient overlay in `app/about/page.tsx`. *Category:* Implementation integrity
  (owner anti-references). *Command:* `/impeccable quieter`. *Pass:* UI3, UI4, UI7.
- **[P1] AI-template patterns on the public pages.** *Location:* `components/landing/Features.tsx` and
  `app/join-network/page.tsx` (48 px icon tiles stacked above every `h3`, `hover:scale-110`),
  `components/landing/Hero.tsx` (uppercase tracked kicker above the `h1`), emoji flags in the country pickers
  (`components/landing/WaitlistModal.tsx`, `features/auth/components/WaitlistForm.tsx`, 11 each), emoji in the
  dashboard widgets and map popups. *Command:* `/impeccable distill`. *Pass:* UI7, UI3.

### P2 Minor

- **[P2] Focus ring below 3:1 on borderless buttons.** `components/ui/button.tsx` uses `ring-ring/50` (Route Blue at
  50 %, about 2.2:1 on white); inputs also change their border to the ring colour, buttons without a border do not.
  *WCAG:* 1.4.11. *Fix:* full-strength ring or an offset outline. *Command:* `/impeccable polish`. *Pass:* UI3 (shared).
- **[P2] No reduced-motion handling.** Dialogs zoom and fade, sheets slide for 300-500 ms, no `motion-reduce`
  variant anywhere. *Fix:* shorten or drop the slide under `prefers-reduced-motion`, keep the fade. *Command:*
  `/impeccable animate`. *Pass:* UI3 (shared).
- **[P2] No skip link** to the main content past the sidebar. *WCAG:* 2.4.1. *Command:* `/impeccable harden`. *Pass:* UI3.
- **[P2] Touch targets under 44 px.** Icon buttons are 36 px (73 uses), `size="sm"` 32 px (155); WCAG 2.5.8 (24 px)
  is met, 2.5.5 is not. *Fix:* 44 px hit areas on touch layouts. *Command:* `/impeccable adapt`. *Pass:* all.
- **[P2] Wizard footers assume an open sidebar.** `fixed ... md:left-64` in `NewTruckWizard.tsx:177` and
  `NewSubcontractorWizard.tsx:622`; the sidebar opens collapsed (3 rem). *Command:* `/impeccable layout`. *Pass:* UI4.
- **[P2] Fixed widths wider than a phone.** `app/app/sources/pickup-import-dialog.tsx`,
  `features/incident-reports/components/go/IncidentReportsPage.tsx`, `features/standby/components/go/StandbyRecordsPage.tsx`,
  `features/maintenance/components/maintenance/MaintenanceForm.tsx` (`w-[NNNpx]` over 360). *Command:*
  `/impeccable adapt`. *Pass:* UI4, UI5, UI6.
- **[P2] Page titles larger and heavier than the scale.** 26 pages use `text-3xl font-bold tracking-tight`, 12
  `text-2xl font-bold`; DESIGN.md sets 24 px semibold. *Command:* `/impeccable typeset`. *Pass:* all.
- **[P2] Table headers in three styles.** Uppercase tracked `text-xs` headers (43 of 325 `TableHead`s) next to the
  primitive's sentence-case header; Thai has no case. *Command:* `/impeccable typeset`. *Pass:* UI4-UI7.
- **[P2] Broken empty donut.** `features/security-center/components/SecurityOverviewPage.tsx:91` uses
  `hsl(var(--muted))`, but the tokens are oklch, so the empty state paints nothing. *Command:* `/impeccable polish`.
  *Pass:* UI7.
- **[P2] Largest initial JS on master-data forms.** `/app/drivers/new` 331 KB gzip, `/app/sources` 306 KB,
  `/app/settings/company-profile` 304 KB, customer and subcontractor forms about 299 KB (the lightest page is
  211 KB). *Command:* `/impeccable optimize`. *Pass:* UI4.

### P3 Polish

- **[P3] Side-tab accent border** (`border-l-4`) on the holiday list (`HolidaysPage.tsx:409`, detector). *Pass:* UI7.
- **[P3] Heading level skipped** on `/join-network` (`h2` then `h4`). *Pass:* UI7.
- **[P3] Native `<select>` for hours and minutes** inside `components/ui/datetime-picker.tsx` (allowed in
  `components/ui`, but not the shared picker). *Pass:* UI6 (its only caller is the maintenance form).
- **[P3] Hard-coded English strings in the shell** ("Home", "Switch to light mode", "Real-Time Operations",
  "Loading / กำลังโหลด..."). *Pass:* UI3.

## Patterns and systemic issues

- Tokens exist but pages do not use them: 1,330 palette classes, 52 hex values, 252 hand-written `dark:` variants.
  The fix is mechanical per page and is the bulk of UI4 and UI7.
- Labels are visible but not programmatic in hand-built forms (84 candidates); react-hook-form pages using
  `FormField`/`FormControl` are fine.
- Icon-only buttons are named in the shared primitives (dialog close, sidebar trigger) but not in page code.
- The public pages (landing, join, about) carry the template look the owner rejected; the app pages are calmer.

## Positive findings

- One searchable select for every pick-from-a-list control (UI2) and now one layering scale: a select, menu, popover
  or tooltip opens above any Dialog or Sheet with no override (`components/ui/layering.test.tsx`), and no arbitrary
  z-index is left in the source (`lib/layering.test.ts`).
- Radix primitives everywhere for dialogs, menus, tabs, checkboxes and switches: keyboard, focus trap and ARIA come
  for free.
- Long lists are virtualised (`VirtualTable`, the SearchSelect's virtual list); heavy libraries (xlsx, Leaflet,
  FullCalendar) load lazily; the bundle gate holds every route within 5 % of its budget.
- i18n parity is enforced (en/th key counts), and every form uses the shared `onInvalid` handler (ADR 0004).
- Light and dark come from one token set through next-themes; the shared components have no gradient, blur, glow,
  oversized shadow or static inline style left.

## Recommended actions

1. **[P1] `/impeccable harden`**: label association (UI6, UI4 first), `<html lang>`, icon-button names (UI3 shell and
   the two image viewers, UI4).
2. **[P1] `/impeccable adapt`**: the app's language switch below 640 px (UI3).
3. **[P1] `/impeccable distill`**: remove the dead shell controls (UI3) and the template patterns of the public pages (UI7).
4. **[P1] `/impeccable quieter`**: palette classes and hex values to tokens; glass, glow, oversized shadows and radii
   out (UI4, UI7, then UI5, UI6, UI3).
5. **[P1] `/impeccable typeset`**: public-page contrast and the 11 px text (UI7); then page titles and table headers
   on the DESIGN.md scale (all passes).
6. **[P2] `/impeccable layout`**, **`/impeccable animate`**, **`/impeccable optimize`**: wizard footers, reduced motion,
   the heaviest master-data routes.
7. **`/impeccable polish`** last in every pass.

The passes start with `/impeccable critique`, which needs PRODUCT.md: the owner writes it through `/impeccable teach`
(#141, hitl) before UI3.
