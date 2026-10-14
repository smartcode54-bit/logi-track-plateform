# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

Several parties share one console, each in its own slice of the data (owner, 2026-10-14):

- **Operations staff of a transport company** (admin, operation staff, operators, dispatchers): plan and assign
  first-mile and line-haul work, watch drivers and trips through the day, fix trips, handle standby and
  incidents. They work at a desktop for long sessions.
- **Accounting and HR staff of that company**: income, rate cards, fuel adjustments, billing documents and
  statements to customers, vehicle expenses and maintenance, driver compensation and payroll. Desktop.
- **Customers** (shippers billed by the transport company, e.g. CJSF): log in to follow their own tasks, trips
  and evidence. Often on a phone.
- **Partners** (รถร่วม, subcontracted carriers with their own trucks and drivers): log in to see and manage their
  own fleet's work. Often on a phone.
- **Platform admins** (GORATECH): run tenants, users, roles, API keys, mobile releases and the security center.

Drivers use the separate LogiTrack mobile app (Flutter, `logitrack-mobile/`), not this console; the console
shows what they send.

## Product Purpose

LogiTrack Pro runs a Thai trucking operation from assignment to payout: a task is assigned, the driver checks
in, loads and delivers through the mobile app with photos, location and time at each step, the trip is priced
on the server from the customer's rate card, the customer is billed, and the driver is paid. Success is that
every trip can be answered for (who, which truck, when, what evidence, what price) without leaving the system.

It is sold as multi-tenant SaaS to other transport companies and 3PLs in Thailand (owner, 2026-10-14); the
first tenant is GORATECH's own fleet.

## Positioning

What a generic TMS cannot truthfully claim (owner, 2026-10-14):

- **One system end to end:** assignment → check-in and photos on the driver's phone → price from the rate card →
  billing to the customer → driver pay, with no re-keying between tools.
- **Evidence on every trip:** photos, GPS position and timestamps at check-in, loading and each delivery stop,
  usable as proof with the customer.
- **Built for Thai first-mile and line-haul:** marketplace hubs and SOCs (Shopee SPX, J&T SPK), main and extra
  jobs (หลัก/เสริม), multi-drop, standby, and how customers are actually billed.
- **Partners and customers see their own data:** carriers and shippers log in to their own scoped view.

## Operating Context

- Work is organised around **tasks** (assigned jobs) and **trip records** (executed trips, created at loading);
  first-mile runs from a hub to an SOC, line-haul between hubs. Vocabulary: `shared-docs/glossary.md`.
- Hubs and SOCs are keyed by codes (e.g. `SPK890174`, `SOCE`) with Thai and English names; operators search by
  code or Thai name.
- Prices are computed on the server only: rate cards per customer, route and vehicle class with effective
  dates, fuel adjustments (not on extra jobs), flat extra-stop fees for multi-drop, standby rates, and period
  locks once a statement is issued.
- Billing produces invoice and receipt PDFs and Excel detail sheets for customers; billing periods follow the
  delivery date or, for some customers, the plan date.
- Roles and scopes: platform admin; per-tenant admin, operation staff, operator, dispatcher; customer scope;
  partner (carrier tenant). One user may belong to several organisations.
- A web force-update and release page governs the driver app's versions.

## Capabilities and Constraints

- Next.js web console behind a BFF; the browser never calls the Go API directly. The Firebase → Go +
  PostgreSQL migration (`mv-go`) is in progress; `main` is live production until it finishes.
- Thai and English carry equal weight: every string exists in both, with equal key counts.
- Lists can be large (thousands of drivers, trucks, hubs, trips): pickers search on the server, long tables page
  or virtualise.
- Forms never fail silently: one shared invalid-form handler (ADR 0004); legacy records that do not meet the
  current schema fail loudly (ADR 0003).
- Undecided: pricing and packaging of the SaaS offer; public positioning copy beyond the current landing page.

## Brand Commitments

- Name: **LogiTrack Pro by GORATECH** (owner, 2026-10-14). LogiTrack Pro is the product, GORATECH the company.
  "Logi-Track", "Logistics Pro" and other variants in the code are to be aligned to it.
- Existing logo assets: `public/Logitrack-logo.jpg` (sidebar logo) and `app/icon.jpg` (favicon; the driver app's
  icon comes from the same artwork).
- Binding look constraints from the owner (issue #140, 2026-10-12): light, calm, easy to use and modern; it must
  not look AI-generated, so no decorative gradients, glassmorphism, glow, emoji icons, oversized rounded cards or
  random accent colours.

## Evidence on Hand

- Real operation: GORATECH's own fleet runs on it today, billing customers such as CJSF and TTP and working
  Shopee SPX and J&T hubs (production data; never copy real customer data into marketing).
- Origin story on the about page: built in-house from January 2026 instead of off-the-shelf tools like AppSheet.
- Absent, never to be fabricated: testimonials, customer logos approved for marketing, case studies, metrics,
  pricing, certifications, and any claim about other tenants.

## Product Principles

1. **One record from dispatch to payout.** A trip's task, evidence, price, statement and pay stay linked; nothing
   is typed twice.
2. **Evidence beside every number.** Wherever a time, a stop or a price can be disputed, its photos, position and
   timestamps are one step away.
3. **Speak the operation's language.** Hub and SOC codes, หลัก/เสริม, multi-drop and standby are first-class
   terms, equally clear in Thai and English.
4. **Each party sees exactly its slice.** Customers, partners and staff see only what their scope allows, and the
   scope is always evident.
5. **Money is decided by the server and explained.** The console never estimates a price; it shows where a price
   came from and why one is missing.

## Accessibility & Inclusion

- WCAG 2.2 AA contrast (text 4.5:1, control outlines 3:1), full keyboard use and visible focus (owner, issue #140).
- Layouts work from 360 px wide: customers and partners use phones; internal staff use desktops.
- Thai script legibility: Thai and Latin text set together without clipping or cramped line height.
