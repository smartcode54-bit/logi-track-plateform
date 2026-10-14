---
name: LogiTrack Web
description: The operations console of LogiTrack: dispatch, fleet, billing and people, in Thai and English.
colors:
  route-blue: "oklch(0.52 0.15 258)"
  route-blue-night: "oklch(0.72 0.12 252)"
  on-route-blue: "oklch(0.985 0 0)"
  on-route-blue-night: "oklch(0.141 0.005 285.823)"
  paper: "oklch(1 0 0)"
  ink: "oklch(0.141 0.005 285.823)"
  ink-soft: "oklch(0.21 0.006 285.885)"
  mist: "oklch(0.967 0.001 286.375)"
  graphite: "oklch(0.52 0.016 286)"
  hairline: "oklch(0.92 0.004 286.32)"
  field-edge: "oklch(0.871 0.006 286.286)"
  control-edge: "oklch(0.62 0.01 286)"
  sidebar-paper: "oklch(0.985 0 0)"
  night: "oklch(0.141 0.005 285.823)"
  night-surface: "oklch(0.21 0.006 285.885)"
  night-mist: "oklch(0.274 0.006 286.033)"
  night-graphite: "oklch(0.705 0.015 286.067)"
  night-hairline: "oklch(1 0 0 / 10%)"
  night-field-edge: "oklch(1 0 0 / 15%)"
  night-control-edge: "oklch(0.55 0.01 286)"
  signal-red: "oklch(0.577 0.245 27.325)"
  signal-red-night: "oklch(0.704 0.191 22.216)"
  delivered-green: "oklch(0.52 0.12 155)"
  delivered-green-night: "oklch(0.74 0.15 155)"
  caution-amber: "oklch(0.53 0.13 55)"
  caution-amber-night: "oklch(0.8 0.14 75)"
typography:
  display:
    fontFamily: "Geist, Sarabun, ui-sans-serif, system-ui, sans-serif"
    fontSize: "clamp(2.25rem, 5vw, 3.75rem)"
    fontWeight: 700
    lineHeight: 1.1
    letterSpacing: "-0.025em"
  headline:
    fontFamily: "Geist, Sarabun, ui-sans-serif, system-ui, sans-serif"
    fontSize: "1.5rem"
    fontWeight: 600
    lineHeight: 1.333
    letterSpacing: "-0.025em"
  title:
    fontFamily: "Geist, Sarabun, ui-sans-serif, system-ui, sans-serif"
    fontSize: "1rem"
    fontWeight: 600
    lineHeight: 1.5
  body:
    fontFamily: "Geist, Sarabun, ui-sans-serif, system-ui, sans-serif"
    fontSize: "0.875rem"
    fontWeight: 400
    lineHeight: 1.43
  label:
    fontFamily: "Geist, Sarabun, ui-sans-serif, system-ui, sans-serif"
    fontSize: "0.875rem"
    fontWeight: 500
    lineHeight: 1
  caption:
    fontFamily: "Geist, Sarabun, ui-sans-serif, system-ui, sans-serif"
    fontSize: "0.75rem"
    fontWeight: 400
    lineHeight: 1.333
  mono:
    fontFamily: "Geist Mono, ui-monospace, SFMono-Regular, Menlo, monospace"
    fontSize: "0.875rem"
    fontWeight: 400
    lineHeight: 1.43
rounded:
  sm: "4px"
  md: "6px"
  lg: "8px"
  xl: "12px"
  full: "9999px"
spacing:
  unit: "4px"
  inline: "8px"
  gutter: "16px"
  stack: "16px"
  section: "24px"
components:
  button-primary:
    backgroundColor: "{colors.route-blue}"
    textColor: "{colors.on-route-blue}"
    rounded: "{rounded.md}"
    padding: "8px 16px"
    height: "36px"
  button-outline:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    rounded: "{rounded.md}"
    padding: "8px 16px"
    height: "36px"
  button-ghost:
    textColor: "{colors.ink}"
    rounded: "{rounded.md}"
    padding: "8px 16px"
    height: "36px"
  button-destructive:
    backgroundColor: "{colors.signal-red}"
    textColor: "{colors.on-route-blue}"
    rounded: "{rounded.md}"
    padding: "8px 16px"
    height: "36px"
  input:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    rounded: "{rounded.md}"
    padding: "4px 12px"
    height: "36px"
  select-trigger:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    rounded: "{rounded.md}"
    padding: "8px 12px"
    height: "36px"
  card:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    rounded: "{rounded.xl}"
    padding: "24px"
  dialog:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    rounded: "{rounded.lg}"
    padding: "24px"
  popover:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    rounded: "{rounded.md}"
    padding: "16px"
  menu:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    rounded: "{rounded.md}"
    padding: "4px"
  badge:
    backgroundColor: "{colors.route-blue}"
    textColor: "{colors.on-route-blue}"
    rounded: "{rounded.md}"
    padding: "2px 8px"
  table-row:
    textColor: "{colors.ink}"
    height: "40px"
    padding: "8px"
---

<!--
Derived from code (UI1, issue #141): the tokens in app/globals.css and the primitives in components/ui, measured on
the mv-go-UI1-design-tokens branch. The descriptive language (north star, colour names) is provisional: it was not
confirmed with the owner, because PRODUCT.md (`/impeccable teach`) is still to come from them (hitl). The frontmatter
and app/globals.css must agree; change both in the same commit.
-->

# Design System: LogiTrack Web

## Overview

**Creative North Star: "The Quiet Dispatch Desk"**

LogiTrack is worked in, not looked at. Dispatchers, accountants and fleet staff keep it open for a whole shift, read
plates, hub codes, Thai names and baht amounts at a glance, and act on them. The system is light, calm and
modern: white paper and zinc greys carry the page, one restrained blue marks what can be acted on, and status
colours appear only where a state needs naming. Density is moderate: a table row is 40 px, controls are 36 px,
and the page breathes in 16 and 24 px steps.

It must not look AI-generated. That rules out the usual tells: decorative gradients, glassmorphism and blur, glow,
emoji used as icons, oversized rounded cards, coloured shadows and accent colours picked per page. Depth comes from
hairline borders and a few quiet shadows; hierarchy comes from type weight and spacing, not colour.

Thai and English sit side by side in the same rows and labels, so the type pairing is Geist for Latin with Sarabun
for every Thai glyph, and every layout is checked in both languages and from 360 px up.

**Key Characteristics:**
- Zinc neutrals with one accent (Route Blue), used for actions, links, selection and focus only.
- Flat surfaces separated by hairlines; four shadow levels, none larger than `lg`.
- Geist + Sarabun for text, Geist Mono for codes, plates and IDs; tabular figures for money and counts.
- One layering scale, so every popover, select and menu opens above any dialog or sheet without overrides.
- Light and dark from the same tokens (next-themes, `class` on `<html>`, stored under `theme`, system by default).

## Colors

A zinc greyscale with a single, slightly muted blue; status colours are reserved for state.

### Primary
- **Route Blue** (`route-blue`, dark: `route-blue-night`): the only accent. Primary buttons, links, the selected
  option, the active nav item, checked checkboxes and switches, focus rings (`--ring`) and the selection highlight.
  White text on it is 5.35:1; on dark it turns light (8:1 against night) and its text turns ink. Token: `--primary`.

### Neutral
- **Paper** (`paper`): page, card and popover background in light mode (`--background`, `--card`, `--popover`).
- **Ink** (`ink`): body text and headings (`--foreground`). **Ink Soft** (`ink-soft`) is the text on mist surfaces.
- **Mist** (`mist`): hover and selected surfaces, muted panels, secondary buttons, table row hover
  (`--muted`, `--secondary`, `--accent`). In shadcn naming `--accent` is this neutral hover surface, not a brand colour.
- **Graphite** (`graphite`): secondary text, helper text, table meta (`--muted-foreground`); 5.5:1 on paper and
  5.0:1 on mist, so it stays AA on both.
- **Hairline** (`hairline`): borders and dividers (`--border`). **Field Edge** (`field-edge`) is one step darker,
  for input and select outlines (`--input`), so fields read as fields against card borders. **Control Edge**
  (`control-edge`) is darker again, the outline of a checkbox and the track of an unchecked switch (`--control-edge`):
  a control with no text needs 3:1 against paper and mist (WCAG 1.4.11), which Field Edge does not reach.
- **Night**, **Night Surface**, **Night Mist**, **Night Graphite**, **Night Hairline**, **Night Field Edge**, **Night Control Edge**: the dark
  theme's counterparts (`.dark` in app/globals.css).

### Status
- **Signal Red** (`signal-red`): errors, destructive actions, invalid fields (`--destructive`).
- **Delivered Green** (`delivered-green`): success and completed states (`--success`).
- **Caution Amber** (`caution-amber`): warnings, expiring documents, pending states (`--warning`).
  Each has a `-foreground` token for text on a filled chip. On dark, Signal Red Night is too light for white text
  (2.8:1), so a filled destructive button or badge uses it at 60% (`dark:bg-destructive/60`, 5.7:1), as the shared
  primitives do.

### Named Rules
**The One Voice Rule.** Route Blue is the only accent. A page never introduces its own blue, violet or teal for
emphasis; `bg-blue-600`-style palette classes are drift to be replaced by `bg-primary`.

**The State-Only Rule.** Red, green and amber name a state (failed, delivered, expiring). They are never decoration,
section colour or a way to make a card "pop".

## Typography

**Body Font:** Geist (Latin) with Sarabun for Thai, then `ui-sans-serif, system-ui` (`--font-sans`).
**Label/Mono Font:** Geist Mono (`--font-mono`), for trip IDs, plates, hub codes, API keys.

**Character:** A neutral, precise grotesk carrying a friendly, readable Thai face; the pairing reads as one voice in
mixed-language rows.

### Hierarchy
- **Display** (700, clamp 36-60 px, 1.1): public landing and join pages only. Never inside `/app`.
- **Headline** (600, 24 px / 32 px, tracking -0.025em): the page title, one per page (`text-2xl font-semibold
  tracking-tight`). Incumbent pages use `text-3xl font-bold` (26 pages); the UI3-UI7 passes bring them to this level.
- **Title** (600, 16 px / 24 px): card, section and dialog titles (`text-base font-semibold`; dialogs use 18 px).
- **Body** (400, 14 px / 20 px): all app text, table cells and form values (`text-sm`). Inputs use 16 px below
  `md` so mobile browsers do not zoom. Multi-line Thai paragraphs use `leading-relaxed` and stay under 75ch.
- **Label** (500, 14 px): form labels and menu items (`Label`). Helper and meta text is the 12 px caption in graphite.
- **Mono** (400, 14 px): codes and identifiers; money and counts stay in the sans with `tabular-nums`.

### Named Rules
**The Two Weights Rule.** Text uses 400, 500 and 600; 700 is for the public display face only. No `font-black`
inside the app.

**The No Shouting Rule.** No uppercase-and-tracked kickers above headings and no uppercase table headers; Thai has no
case, so the pattern makes the two languages look different.

## Layout

The app shell is a collapsible icon sidebar (16 rem open, 3 rem as icons, an 18 rem sheet on mobile) beside a sticky
64 px header (one height whatever the sidebar does) with the sidebar trigger, the breadcrumb, the tenant switcher and
the user menu; the language and theme live in the user menu, so they are there at every width. Content sits in a
single column with a 16 px gutter (`gutter`), sections 24 px apart (`section`), fields and cards 16 px apart
(`stack`), inline controls 8 px apart. All spacing is on Tailwind's 4 px step; the named steps are tokens
(`p-gutter`, `gap-section`, `space-y-stack`).

Breakpoints are Tailwind's (`sm` 640, `md` 768, `lg` 1024, `xl` 1280). Every page works from 360 px: filters wrap,
tables scroll horizontally inside their card (`overflow-x-auto`), dialogs keep a 16 px margin
(`w-[calc(100%-2rem)]`) and footers stack their buttons. Fixed widths above 360 px are not allowed on mobile.

## Elevation & Depth

Flat by default. Surfaces are separated by hairline borders and the mist tone; shadows are quiet and zinc-tinted, and
only four levels exist. `shadow-xl` and `shadow-2xl` resolve to the `lg` level, so nothing floats higher.

### Shadow Vocabulary
- **xs** (`0 1px 2px 0 oklch(0.141 0.005 285.823 / 0.05)`): inputs, outline buttons, checkboxes.
- **sm** (`0 1px 3px 0 / 0.08, 0 1px 2px -1px / 0.08`): cards, the active tab.
- **md** (`0 4px 6px -1px / 0.08, 0 2px 4px -2px / 0.06`): popovers, menus, select panels.
- **lg** (`0 10px 15px -3px / 0.08, 0 4px 6px -4px / 0.06`): dialogs and sheets.

### Layering scale
One z-index scale, defined as tokens in app/globals.css and used as `z-*` utilities:

| Token | Value | What sits there |
|---|---|---|
| `z-base` | 0 | page content |
| `z-raised` | 10 | lifts inside a component: sticky table headers, controls over a map, a corner badge |
| `z-sticky` | 20 | the sticky app header, sticky page and form bars, the fixed sidebar |
| `z-modal` | 50 | Dialog and Sheet (backdrop and panel share it; a dialog opened from another stacks by DOM order), full-screen image viewers |
| `z-popover` | 60 | Popover, SearchSelect / Select, DropdownMenu, date pickers |
| `z-toast` | 70 | toasts (sonner) |
| `z-tooltip` | 80 | tooltips |

Floating content portals to `<body>` on a layer above `modal`, so a select, menu, date picker or tooltip opened
inside a Dialog or Sheet works with no per-dialog `z-[...]` or `position` prop. Each Leaflet map is its own stacking
context (`.leaflet-container { isolation: isolate }`), so its internal 400-1000 values stay inside the map.
`lib/layering.test.ts` fails on any arbitrary or bare z-index above 50.

### Named Rules
**The Flat-By-Default Rule.** A surface earns a shadow only by floating (popover, dialog). Cards get `shadow-sm` at
most; nothing gets a coloured or glowing shadow.

## Shapes

Gently rounded, never bubbly: radii come off one `--radius` of 8 px. Fields, buttons, badges, menus and popovers use
6 px (`rounded-md`); dialogs 8 px (`rounded-lg`); cards and panels 12 px (`rounded-xl`), the maximum for a container.
Fully round shapes are for avatars, status dots, switches and progress bars only. Badges are rounded rectangles, not
pills. Borders are 1 px hairlines; no side-tab accent borders (`border-l-4` stripes).

## Components

### Buttons
- **Shape:** gently rounded (6 px), 36 px tall (`sm` 32, `lg` 40, icon 36 square).
- **Primary:** Route Blue with white text (dark: light blue with ink text). One primary action per view.
- **Outline / Secondary / Ghost:** outline for the second action, ghost for toolbars and row actions, secondary for
  low-emphasis fills. **Destructive** is Signal Red and asks for confirmation in a dialog.
- **Hover / Focus:** hover drops the fill to 90 % opacity; focus shows a 3 px ring in the ring colour plus the ring border. No scale
  or lift on hover.
- **Icon-only buttons** carry an `aria-label` (or `sr-only` text), in both languages.

### Badges and status
- **Style:** 6 px radius, 12 px medium text, `px-2 py-0.5`; `secondary` (mist) for neutral facts, `outline` for
  categories, status colours only for states (delivered, failed, expiring).

### Cards / Containers
- **Corner Style:** 12 px (`rounded-xl`). **Background:** paper (night surface in dark). **Shadow:** `sm`.
- **Border:** hairline. **Internal Padding:** 24 px (`px-6`, `py-6`), 16 px for dense dashboard tiles.

### Inputs / Fields (form style)
- **Style:** 36 px, Field Edge 1 px outline, 6 px radius, paper background (`dark:bg-input/30`), `shadow-xs`.
- **Labels:** always visible above the field (`Label`, 14 px medium), 8 px gap; a required field adds a `*` in
  Signal Red to its label. Placeholders show a format, never stand in for a label.
- **Layout:** one column below `md`, two columns for long forms at `md` and up; 16 px between fields, 24 px between
  sections; actions at the end, primary on the right, stacked full width on mobile.
- **Errors:** field-level, under the field (`FormMessage`, 14 px Signal Red) with `aria-invalid`; every form uses the
  shared `onInvalid` handler (ADR 0004), which focuses the first invalid field. Disabled is 50 % opacity.
- **Focus:** the outline turns Route Blue with a 3 px ring at 50 %.

### Pickers (the select rule)
**Every pick-from-a-list control uses the shared SearchSelect** (`components/ui/search-select.tsx`, its compound
front end `components/ui/select.tsx`, the server-searched pickers in `components/entity-pickers.tsx`, and
`FormSearchSelect` for react-hook-form). **A fixed list of 5 options or fewer (e.g. a status filter) may use a
segmented control or radio group instead.** No native `<select>`, no Radix Select, no hand-rolled result lists
(`lib/selectPrimitives.test.ts`, ESLint).

### Tables
- **Primitive:** `components/ui/table.tsx`; long or paged lists use `components/virtual-table.tsx` (TanStack
  Table + Virtual) inside a card.
- **Density:** 40 px header and rows (`h-10`, `p-2`), 14 px text, horizontal dividers only, no zebra stripes.
- **Header:** the primitive's 14 px medium ink, sentence case; sticky on scrolling lists (`sticky top-0 z-raised
  bg-card`).
- **Alignment:** text left; numbers, money and dates right-aligned with `tabular-nums`; codes and plates in mono.
- **Rows:** hover mist, selected mist; row actions in a ghost icon button opening a DropdownMenu at the row's end.
- **States:** loading shows skeleton rows; empty shows one graphite line and the next action; errors show inline with
  a retry. Below `md` the table scrolls horizontally within its card.

### Dialogs and Sheets
- 8 px radius, `shadow-lg`, 24 px padding, 50 % black backdrop, 16 px margin on small screens (`max-w-lg` unless the
  caller widens it). Title 18 px semibold; footer actions right-aligned, stacked on mobile. Sheets slide from an edge
  (the mobile sidebar is one).

### Navigation
- The sidebar uses the sidebar tokens (paper-tinted, hairline border); icons are lucide at 16 px, one distinct icon
  per entry and per group. The header is paper with a hairline bottom border, sticky at `z-sticky`, 64 px at every
  width; it is the page's banner, beside `<main>` (which holds the page, `#main-content`), and the menu is a
  `<nav>` named "Main menu" / "เมนูหลัก" (the Security Center's: "Security center menu").
- **You are here.** The current page's entry is Route Blue text (`sidebar-primary`, its icon too) in medium weight on
  the mist fill, with `aria-current="page"`; sub-entries the same; a closed group that holds the page, and a rail
  group, show it the same way and carry `aria-current="true"` (the current section, not only a colour). No side
  stripe. A detail page keeps its entry current. When the sidebar or the phone sheet opens, the current entry is
  scrolled into view (nearest edge).
- **The order is the work loop** (owner, UI3B): the principal's home first (the dashboard, or for customers,
  partners, dispatchers and operators the driver monitor, with the dashboard inside Operations), then Operations
  (job assignment, first mile, line haul, driver monitor, incidents, standby, chat, pickup and delivery points),
  Billing (rate cards, trips to bill, billing documents, statements, the Shopee report), Costs (fuel, fuel price
  history, other expenses, expense audit), Fleet and partners (trucks, truck assignment, drivers, renewals,
  maintenance, customers, partners), Payroll and HR; then System (security center, waitlist, billing utilities,
  companies, company profile) and Support. Only the group of the current page starts open. Labels are sentence
  case and say "and", never "&"; the four billing pages read four different names in both languages. The
  subcontracted carriers are "Partners" / "รถร่วม" wherever the console names them (menu, page, forms, broadcasts,
  company types), as in the scope line's "Partner admin" / "ผู้ดูแลรถร่วม".
- **One name per page.** The menu label, the crumb, the page's H1 and the tab title are one i18n key ("Trips to
  bill · LogiTrack Pro"; a detail page "<its name> · <entry> · LogiTrack Pro").
- **The sidebar keeps its state.** Open or icons, as the user left it (`sidebar_state` cookie, a year); without a
  choice it starts open from `lg` and as icons from `md`. The trigger names what it controls (`aria-controls`,
  `aria-expanded`), its label is translated and its tooltip adds only the shortcut ("Shortcut: ⌘B" on Apple
  platforms, "Shortcut: Ctrl+B" elsewhere; the other modifier does nothing, and neither does the shortcut while the
  user types in a field or an editor).
- **The icon rail** labels every icon with a tooltip, which stays hoverable (it is the only visible label) but never
  takes a click (`pointer-events-none`, and its positioning wrapper through `[data-sidebar-tooltip]`); the sidebar's
  tooltips and the trigger's are one group (`TooltipInGroup` under the sidebar's provider), so a pointer crossing the
  trigger on its way onto the logo's tooltip does not swap one tooltip for the other. A group
  opens its pages as a menu to the right (`DropdownMenu`, `side="right"`, on the popover layer, keyboard operable).
- **On a phone** the sidebar is a sheet with a visible title and a labelled 44 px close button; every entry in it
  is 44 px tall (`max-md:h-11`, desktop density unchanged), as are the user menu's items; when the sheet closes,
  focus returns to the menu button. A page has one `<main>` (the shell's): a page component never adds another.
- **One definition** (`components/app-nav.ts`): the sidebars and the breadcrumb read the same entries and i18n keys,
  so a crumb is the menu's label in the active language, never a URL slug. A detail page keeps its entry as a link
  back to the list and ends with its own name (a plate, a driver's name) or a generic New / Edit / Details. A crumb
  the principal cannot open is text, not a link. The current page shows at every width, Home from `md`, the group and
  entry from `lg`; the trail truncates rather than run under the header's controls. On a phone the sidebar sheet
  closes when a link is tapped.
- **The scope line** names the slice of the data the principal works in, as `slice · role` ("WRT · Tenant admin",
  "CJSF · Customer view", "Platform admin"): in the sidebar header (in the collapsed rail, the logo's tooltip with
  the title and the scope line on two lines), in the user menu and its trigger (and the trigger's accessible name),
  in the phone header (below `sm`, in place of the crumb that repeats the H1), and on the 403 page. One source:
  `principalRoleLabel` / `principalScope` over `['me']` (`components/scope-line.tsx` renders it); customer scopes
  name the slice only for a customer view (no tenant, platform role or dispatcher grant). When space runs out the
  slice is shortened, never the role, and the line height is normal so Thai marks are not clipped.
- **The user menu** holds the name, email and scope line, the language (in its own words, "ไทย" / "English", no
  flags) and theme (radio items) and the one sign-out ("Sign out" / "ออกจากระบบ"), which lands on `/login`. The
  header carries only controls that work: no placeholder search, bell or report button.
- A skip link ("Skip to main content") is the first stop of the keyboard and lands below the sticky header; header
  targets are 44 px on phones. The page area takes `min-w-0`, so a wide page scrolls without pushing the header out.

### Toasts
- sonner on `z-toast`, in the popover colours and the app font, bottom-right, 5 s; typed toasts (success, error,
  warning, info) use sonner's rich colours. One toast per event; errors name what to do next.

## Do's and Don'ts

### Do:
- **Do** take colours from tokens (`bg-primary`, `text-muted-foreground`, `border-border`, `bg-success`), never from
  palette classes or hex values.
- **Do** use the layering tokens (`z-raised`, `z-sticky`, `z-modal`, `z-popover`) and nothing above 50 by hand.
- **Do** use lucide icons at 16 px (20 px in the header); every icon-only control has an accessible name in en and th.
- **Do** check every page in Thai and English, light and dark, at 360 px and at 1280 px.
- **Do** keep text AA: 4.5:1 for body text, 3:1 for large text and control outlines that carry meaning.

### Don't:
- **Don't** use decorative gradients (`bg-gradient-*`, `bg-linear-*`, `*-gradient()`).
- **Don't** use glassmorphism: no `backdrop-blur`, no translucent panels over content.
- **Don't** use glow: no `blur-xl/2xl/3xl` halos, no coloured shadows (`shadow-primary/20`), no `shadow-xl`/`shadow-2xl`.
- **Don't** use emoji as icons or as flags in UI chrome.
- **Don't** use oversized rounded cards (`rounded-2xl`, `rounded-3xl`) or pill badges.
- **Don't** pick accent colours per page (`bg-blue-600`, `text-violet-500`, `bg-[#137fec]`): there is one accent.
- **Don't** scale or lift cards on hover, stack icon tiles above headings, or put uppercase kickers above titles.
- **Don't** use static inline `style={{...}}` for layout or colour; only values computed from data (a width, a
  transform, a chart coordinate) may be inline.
