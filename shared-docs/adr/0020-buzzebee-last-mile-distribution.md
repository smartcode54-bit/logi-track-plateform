# ADR 0020 — Buzzebee last-mile distribution domain (order as SSOT + conservation of goods)

- **Status:** Accepted (2026-08-25) — implementation pending
- **Deciders:** Samart Kas (product owner), Claude
- **Area:** whole platform — logitrack-web (`app/app/**`, `features/`, `functions/src/`, `firestore.rules`),
  logitrack-mobile (driver app), shared-docs (spec + glossary)

## Context

Buzzebee hands us **orders** (สินค้าที่ต้องกระจายส่งถึงลูกค้าปลายทาง) as an Excel/Google Sheet file, and we
run **last-mile B2C distribution** for them: allocate → confirm deliverable orders back to Buzzebee → assign a
driver → the driver receives goods, delivers each order, handles postpone/cancel, returns leftovers to the
warehouse, and closes the day; and Buzzebee tracks each order's status in a customer portal. This is a **new
line of business** structurally unlike the platform's existing hub-to-hub logistics.

The existing model terminates at a **task** (`tasks` collection) executed into a **trip_record**
(`validate/taskSchema.ts:57`, glossary [[Task]] / [[`trip_record`]]). There is **no order entity** — the closest
structure is the embedded multi-drop array `tasks.deliveryStops[]` (`validate/taskSchema.ts:40-55,112-114`) and
its trip-side twin `trip_records.deliveryStopsProgress[]` (glossary [[Delivery stop]]). That array is designed for
"one trip, N hub drops," not for a customer-facing, independently-tracked, SKU-itemised order with its own call
history and proof of delivery.

Facts established during exploration that constrain the design:

- **No area/zone model.** Destinations today are three fixed SOC codes (`validate/taskSchema.ts:3-9`) plus hub
  lat/lng; there is no subdistrict/zone concept and no order geocoding.
- **No cargo-volume dimension on trucks.** `truckSchema` has only `maxLoadWeight` in kg
  (`validate/truckSchema.ts:58`); nothing about cubic volume, pallets, or a size class beyond the free-text
  `type` (`:52`).
- **Google Maps is already wired server-side** via `GOOGLE_MAPS_API_KEY` (`functions/src/distances.ts:22`,
  Distance Matrix at `:116`), so geocoding an address can reuse the same key + `defineString` pattern.
- **Client-side maps today are Leaflet**, not Google — `react-leaflet`/`leaflet`/`leaflet-geosearch`
  (`package.json:65-75`) power `components/map/SourcesMap.tsx`, `LocationPicker.tsx`,
  `features/dashboard/components/DashboardVehicleMapClient.tsx`, and the incident/session-location maps. The owner
  wants distribution to render on **Google Maps** for the higher subdistrict-level detail.
- **No Firestore document triggers** are possible — the database region `asia-southeast3` supports none; every
  reactive write is an app-invoked callable (glossary [[Denormalization (in this codebase)]]).
- **Customer scoping already exists.** `firestore.rules` has `isCustomer()` (`firestore.rules:18-21`) gating on
  `role == 'customer'` + a non-empty `customerScopeId` claim, mirrored by `hooks/useCustomerScope.ts`.
- **Product decisions taken with the owner (2026-08-25):** allocate = **semi-automatic** (system proposes, admin
  confirms); Buzzebee exchange = **files/email both ways, no API**; counting = **driver enters quantities, a
  countable photo is the evidence** (not barcode/OCR); order data is **per-SKU line items**; the day cannot be
  closed until **every received unit has a disposition**; and allocation must let the admin **name how many trucks
  are available** and view the split **on a map at subdistrict (ตำบล) level**.

## Decision

1. **The distribution order is a first-class Firestore collection, `distribution_orders`, and is the single
   source of truth for per-order tracking.** One doc per Buzzebee order. It is **not** modelled as an embedded
   `deliveryStops[]` element — a customer portal that tracks each order, SKU-level reconciliation, a per-order
   call history, and an independent per-order status all require a queryable, individually-addressable document,
   which an array element inside a task cannot be. New collection names go in `lib/collections.ts` as
   `COLLECTIONS.*` constants, never string literals (project rule).

2. **Orders carry per-SKU line items.** `items[]` = `{ sku, description, expectedQty, deliveredQty?,
   unitVolumeM3?, unitWeightKg? }`. `expectedQty` per SKU (from the Buzzebee file) is the **reconciliation
   baseline**; `deliveredQty` is filled by the driver. This is the granularity chosen with the owner.

3. **Conservation of goods is a hard invariant.** For every SKU, across a driver's day:
   `received = delivered + left_at_drop + returned_to_warehouse + variance(with a recorded reason)`. Each unit
   must resolve to exactly one **disposition** (glossary [[Disposition]]); "shortage/damaged/refused" is a
   disposition **with a reason**, not an unaccounted gap. The **day cannot be closed** (`daily_dispatch.reconciled`
   stays false, the empty-container photo gate stays shut) until every received unit has a disposition. This is the
   answer to "how does the app know goods were received and delivered in full": the file supplies `expectedQty`,
   the driver enters actual counts at each checkpoint (receive / deliver / return), and the invariant is enforced
   at day-close — see glossary [[Conservation invariant]].

4. **Counting is manual entry backed by a mandatory countable photo, not barcode/OCR.** The driver types the
   quantity; a photo in which the items can be counted is attached as evidence via the existing upload pipeline.
   The photo proves *an* image is attached, not that the number is correct — same honesty caveat as the app-
   screenshot evidence in [ADR 0019](0019-app-screenshots-as-mandatory-evidence.md). Barcode-per-parcel and OCR
   auto-count were rejected (see Alternatives).

5. **Zone/area is keyed at subdistrict (ตำบล) granularity, and orders are geocoded at import.** Each order stores
   `subdistrict` / `district` / `province` / `postalCode`, a derived `zoneKey` (subdistrict-level), and `lat`/`lng`
   obtained by geocoding the recipient address at import time, **reusing the existing `GOOGLE_MAPS_API_KEY`** in a
   Cloud callable (Geocoding API alongside the Distance Matrix use at `functions/src/distances.ts:22,116`). A row
   that fails to geocode is flagged and must be corrected / pin-dropped before it can be allocated. An optional
   `distribution_zones` master may map subdistrict → zone label + a default truck; absent that, `zoneKey` derives
   directly from the address.

6. **Truck cargo capacity gains a volume dimension.** Add `cargoVolumeM3` to `truckSchema`
   (`validate/truckSchema.ts`) alongside the existing `maxLoadWeight`. Allocation fits Σ item volume **and** Σ item
   weight against each truck's two caps.

7. **Allocation is semi-automatic and driven by an admin-named truck count, with a subdistrict map view.** The
   admin selects the N real trucks available (each with its `cargoVolumeM3` / `maxLoadWeight`). The system
   partitions the day's orders into **N loads** by grouping spatially-contiguous subdistricts under each truck's
   capacity caps, and renders the split on a **map at ตำบล level**, orders coloured by truck/zone, with a per-truck
   Σvolume/Σweight-vs-cap meter that warns on overflow. The admin drag-adjusts orders across trucks/zones, can add
   or remove trucks to re-partition, and marks each order **accepted** or **rejected** (out of area / no capacity /
   bad address). This is a proposal-then-confirm tool, not an optimiser (glossary [[Allocation]]). **The allocation
   map renders on Google Maps JS** — a new client-side integration (existing maps are Leaflet, `package.json:65-75`)
   chosen for its subdistrict-level detail — using the platform's Google Maps project via a browser Maps JS key
   (HTTP-referrer-restricted, distinct from the server-side Distance Matrix key). The Leaflet `SourcesMap.tsx` is
   **not** reused here. Import pipeline patterns (SheetJS header mapping, code→id lookup) are reused as before.

8. **A driver's daily route reuses the task/trip machinery; orders link to it.** One dispatch = one `task` with a
   **new** `taskType` value `"DISTRIBUTION"` added to `TASK_TYPE_ENUM` (`validate/taskSchema.ts:30`) — additive,
   so existing FIRST_MILE/LINE_HAUL behaviour is untouched (see the API-versioning rule). Each order sets
   `assignedTaskId`; the mobile "work slip" UI renders the orders belonging to that task instead of reading
   `deliveryStops[]`. The `trip_record` still records trip-level evidence (check-in, goods receipt, empty
   container), but **per-order proof of delivery lives on the order document**, not the trip. This preserves reuse
   of assignment, `runOrder`, the driver picker, the "On run" badge, check-in/loading, `activeTruck`, and FCM
   `notifyTaskUpdate`.

9. **Buzzebee is an ordinary `customers` doc; the customer portal reuses customer scoping.** Buzzebee =
   `customers` doc with `code: "BUZZEBEE"`. Portal users get `role: 'customer'` + a `customerScopeId` claim and see
   only their own orders, enforced by `isCustomer()` (`firestore.rules:18-21`) and the existing
   `page-permission-guard` / `useCustomerScope`. Tracking is **per order**: the portal reads `distribution_orders`
   filtered by scope and renders a status timeline from the order's immutable `statusHistory[]` plus its POD photos.

10. **Buzzebee exchange is file/email both directions, no API.** Intake is the uploaded file; the confirm-back of
    accepted orders is an exported Excel plus an email (the email transport is [ADR 0021](0021-transactional-email-smtp-workspace.md)).

### Collections introduced (registered in `lib/collections.ts`)

| Constant / name | Purpose |
|---|---|
| `distribution_orders` | 1 doc per order — SSOT for tracking. `items[]` (per-SKU), recipient + subdistrict + `zoneKey` + `lat`/`lng`, Σvolume/Σweight, `status`, per-SKU dispositions, POD (photos + signature + `leftAtDrop`), `callHistory[]`, `assignedDriverId`/`assignedTaskId`, `dayKey`, immutable `statusHistory[]` |
| `order_import_batches` | 1 doc per uploaded file — audit + confirm-back state (totals, accepted/rejected, confirm-sent) |
| `return_manifests` | 1 doc per warehouse return — `items[]`+qty, warehouse-receiver signature, photo, email recipients, `submittedAt` |
| `daily_dispatch` | 1 doc per driver per day — received/delivered/left/returned totals, variance list, empty-container photo, `closedAt`, `reconciled` |

## Consequences

**Positive**
- One queryable document per order makes the customer portal, SKU reconciliation, and per-order call/POD history
  straightforward, and keeps the conservation invariant checkable at day-close.
- Reusing `tasks`/`trip_records`/`activeTruck`/assignment means the new line of business inherits check-in,
  driver queue (`runOrder`), the "On run" badge, and FCM without reinvention.
- Additive `taskType` and additive collections mean **zero change** to existing hub-to-hub billing and monitoring.

**Negative / risks**
- Four new collections need `firestore.rules` blocks (default-deny at `firestore.rules:546` blocks them until
  written), composite indexes, and RBAC capabilities.
- The N-truck partition is a **heuristic**, not an optimal solve — it can propose an awkward split the admin must
  fix by hand. Accepted: the owner chose semi-auto precisely to keep a human in the loop.
- **Manual counts trust the driver.** The photo is corroboration, not verification — the same reuse/accuracy gap
  documented in [ADR 0019](0019-app-screenshots-as-mandatory-evidence.md).
- Geocoding every order at import consumes Google Maps quota/cost and can fail on messy Thai addresses; hence the
  explicit "flag + pin-drop manually" path in decision 5.

**Follow-ups**
- **Billing for this line of business is specified separately in [ADR 0023](0023-buzzebee-distribution-billing.md)**
  (per-pack × zone × tier + per-trip minimum + fuel surcharge, from the approved quote QT-202608-001). Note that the
  [[Zone key]] defined here is **also** the billing zone in ADR 0023 — one shared, district-keyed taxonomy.
- Mobile needs two capabilities it does not have today — on-glass signature capture and `tel:` call launching with
  a call log — recorded in the spec, not here.
- Email transport is a separate greenfield decision: [ADR 0021](0021-transactional-email-smtp-workspace.md).
- Distribution introduces the platform's **first client-side Google Maps JS** usage and a browser Maps key; the
  owner has signalled a preference for Google's map detail over Leaflet, so **migrating the existing Leaflet maps
  app-wide to Google Maps** (`SourcesMap.tsx`, `LocationPicker.tsx`, dashboard/incident/session maps) is a
  candidate follow-up — tracked as an open question in the spec, not committed here.

## Alternatives considered

- **Model orders as `tasks.deliveryStops[]` (extend the multi-drop array).** Rejected: an array element cannot be
  independently queried for a customer-scoped portal, cannot cleanly hold per-SKU lines / a call history / its own
  status timeline, and the schema already caps and de-dups stops for a different purpose
  (`validate/taskSchema.ts:118-137`). Per-order tracking is a first-class-document requirement.
- **Barcode/QR scan per parcel for counting.** Most accurate, but requires Buzzebee to print a scannable code on
  every parcel and adds scanning hardware/flow — rejected for the MVP; the owner chose manual-entry + photo.
- **OCR auto-count from the photo** (the codebase has `TripOcrData`). Rejected: OCR is unreliable at counting
  arbitrary goods and would make the number look authoritative when it isn't.
- **Full automatic route optimisation / bin-packing.** Rejected in favour of semi-auto — the owner wants to name
  the truck count and adjust the split on a map, not receive an opaque optimal answer.
- **A brand-new dispatch entity instead of reusing `tasks`.** Rejected: it would duplicate check-in, `runOrder`,
  `activeTruck`, and FCM; an additive `taskType` reuses all of it.

## Related

- Spec (what to build, phased): `shared-docs/specs/buzzebee-distribution-last-mile.md`.
- Email transport: [ADR 0021](0021-transactional-email-smtp-workspace.md).
- Glossary: [[Distribution order]], [[Work slip]], [[Disposition]], [[Conservation invariant]], [[Zone key]],
  [[Day-close]], [[Return manifest]], [[Proof of delivery (signed-on-glass)]] in [../glossary.md](../glossary.md).
- Reuses / relates to: [ADR 0019](0019-app-screenshots-as-mandatory-evidence.md) (un-overlaid evidence photos),
  [ADR 0010](0010-job-category-carried-on-trip-independent-of-billing.md) (denormalisation caches, resolve-not-guess),
  [ADR 0008](0008-standby-billing-visibility-and-recompute-semantics.md) (a record carries its own `customerId`),
  [ADR 0016](0016-explicit-job-category-at-assign.md) (decide at assign time, don't derive silently).
