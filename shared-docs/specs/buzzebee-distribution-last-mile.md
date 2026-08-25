# Spec: Buzzebee Last-Mile Distribution

> **Status:** 🟡 Draft
> **Owner:** Samart Kas
> **Created:** 2026-08-25
> **Domain:** distribution (new) — touches tasks, drivers, trucks, customers, functions, mobile
> **Related:** [ADR 0020](../adr/0020-buzzebee-last-mile-distribution.md) (order SSOT + conservation),
> [ADR 0021](../adr/0021-transactional-email-smtp-workspace.md) (email),
> [ADR 0022](../adr/0022-phone-gps-fallback-for-trucks-without-device.md) (phone-GPS fallback), glossary terms
> [[Distribution order]] / [[Conservation invariant]] / [[Disposition]] / [[Work slip]] / [[Day-close]]

---

## 1. Problem & Goal

Buzzebee sends us orders as a file and we must run **last-mile B2C distribution** for them — allocate to trucks by
area under capacity, confirm the deliverable set back to Buzzebee, dispatch drivers, capture proof at each order,
return leftovers to the warehouse, close the day with every unit accounted for, and let Buzzebee track each order.
The platform has **no order entity, no area/zone model, no cargo-volume capacity, no signature capture, no call
log, and no email** today. Goal: deliver a complete, reconciled, trackable distribution workflow across web admin,
the mobile driver app, and a customer portal — built spec-first, in phases.

## 2. Scope

**In scope:**
- Import Buzzebee order file (Excel/CSV) → validate + geocode → `distribution_orders` (per-SKU line items).
- Semi-automatic allocation: admin names N trucks; system splits orders by subdistrict under volume+weight caps;
  subdistrict **map** view; admin adjusts; accept/reject per order.
- Confirm-back to Buzzebee: Excel export + email (no API).
- Assign a load to a driver via a `task` (`taskType: "DISTRIBUTION"`).
- Driver flow (mobile): goods receipt + day summary, per-order work slips, reorder outer→inner, call customer +
  call log, POD (countable photo + on-glass signature + per-SKU delivered qty + "left at drop" checkbox),
  postpone/cancel, warehouse return (list+qty, receiver signature, photo, submit → email), day-close (empty-
  container photo + conservation check).
- Customer portal: Buzzebee tracks **per order** (status timeline + POD), customer-scoped.
- Email transport (greenfield, per ADR 0021) + `cargoVolumeM3` on trucks.

**Out of scope (ทำทีหลัง / ไม่ทำ):**
- **Billing/pricing for this line of business** — different model from the hub-to-hub rate card; separate ADR + spec.
- Any **API integration** with Buzzebee (files/email chosen).
- Barcode/QR or OCR auto-counting; full automatic route optimisation.
- Real-time GPS trace of the delivery on the customer portal (status timeline only for now).

## 3. Requirements

**Functional**
- **R1. Order import.** Admin uploads `.xlsx/.xls/.csv`; flexible header mapping (bilingual, tolerant) reads
  recipient, address (with subdistrict), and **per-SKU line items** (sku, qty, optional dimensions/volume/weight);
  rows validate with a preview (invalid rows flagged); a valid import writes `distribution_orders` + one
  `order_import_batches` audit doc.
- **R2. Geocoding at import.** Each order's address is geocoded to `lat`/`lng` + normalised subdistrict/`zoneKey`;
  rows that fail geocoding are flagged and blocked from allocation until corrected/pin-dropped.
- **R3. Truck cargo capacity.** `trucks` gains `cargoVolumeM3` (kept alongside `maxLoadWeight`), editable in the
  truck form and read by allocation.
- **R4. Semi-auto allocation.** Admin selects N available trucks; system partitions orders into N loads by
  spatially-contiguous subdistricts under each truck's volume **and** weight caps; a **Google Maps view at ตำบล
  level** shows orders coloured by truck/zone with a per-truck capacity meter that warns on overflow; admin drag-moves orders,
  adds/removes trucks to re-partition, and marks each order **accepted/rejected** with a reason.
- **R5. Confirm-back.** Admin exports the accepted set as Excel and emails it to Buzzebee; the batch records
  confirm state (accepted/rejected counts, `confirmSentAt`).
- **R6. Assign to driver.** Each accepted load becomes a `task` (`taskType: "DISTRIBUTION"`) assigned to a
  driver + truck (reuse driver picker, `runOrder`, "On run" badge, `TruckPlateField`); its orders set
  `assignedTaskId`/`assignedDriverId`/`dayKey`.
- **R7. Goods receipt + day summary.** On receiving, the app shows Σ`expectedQty` for the day's assigned orders;
  the driver enters received counts (per SKU) + a countable photo; the app reconciles vs expected and flags any
  variance; the accepted totals seed `daily_dispatch`.
- **R8. Per-order work slips.** Mobile renders the task's orders as work slips; the driver can **reorder** them
  (outer→inner rule available), **call** the recipient (`tel:`) with the call recorded to the order's
  `callHistory[]`, and complete each order with a **disposition**: delivered / partial / left_at_drop / postponed /
  cancelled — capturing per-SKU `deliveredQty`, a countable photo, an **on-glass signature**, and the "left at drop
  (customer not home)" checkbox that flags Buzzebee.
- **R9. Warehouse return.** The app auto-computes required return per SKU = received − delivered − left_at_drop;
  the driver's return list must reconcile (mismatch requires a reason); capture the **warehouse receiver's on-glass
  signature** + a photo of the goods with the receiver; on submit, write `return_manifests` and **email** the
  manifest to the warehouse and Buzzebee.
- **R10. Day-close.** Closing requires **every received unit to have a disposition** (delivered + left + returned +
  reasoned-variance = received, per SKU) and an **empty-container photo**; on success set `daily_dispatch.reconciled`;
  otherwise block with a clear remaining-items list.
- **R11. Customer portal.** A Buzzebee (customer-scoped) user sees **per-order** tracking — status timeline from the
  order's immutable `statusHistory[]` + POD photos — filtered to their `customerScopeId` only.
- **R12. RBAC + navigation.** New capabilities gate the admin distribution pages and the portal; sidebar entries are
  permission-filtered; admin (`"*"`) gets all; the Buzzebee customer role gets the portal only.
- **R13. Phone-GPS fallback (ADR 0022).** When the driver's assigned truck has **no hardware GPS** (`GPSVehicleId`
  empty), the mobile app reports the phone's position to `vehicle_locations/{truckId}` with `source: "mobile"` while
  a route is active (gated by `activeTruck`, debounced ~2 min / ~1000 m — **mobile source only**); a truck with a
  Cartrack device is never phone-tracked and its hardware sync cadence is unchanged. The distribution/dashboard maps
  consume it transparently.

**Non-functional** (perf / security / i18n / cost)
- **N1. i18n complete in `en` and `th`** — a new `distribution` namespace (web) + keys in `en.json`/`th.json`
  (mobile); no hardcoded UI strings.
- **N2. Additive only** — new `taskType` value + new collections; **no change** to existing FIRST_MILE/LINE_HAUL
  tasks, billing, or Driver Monitor behaviour.
- **N3. Firestore security** — new collections start default-denied (`firestore.rules:546`); add per-role blocks;
  Cloud-Function-only fields (e.g. geocode results, email audit) are not client-writable where appropriate.
- **N4. Email security (ADR 0021)** — SMTP secret in Cloud env only; recipients server-controlled; mobile-invokable
  send restricted to the owning driver + configured recipient sets.
- **N5. API versioning** — new callable params optional/defaulted; existing callables unchanged (mobile updates slowly).
- **N6. Quality gate** — `tsc --noEmit` (web), `dart analyze` (mobile) clean; CI green.

## 4. Design

> Follows `.vibe-rules.md` + feature architecture (`features/<domain>/api` ↔ `components`). Data model detail and
> the reasoning live in [ADR 0020](../adr/0020-buzzebee-last-mile-distribution.md); this section is the build shape.

**Data model (Firestore)** — new collections in `lib/collections.ts` (`COLLECTIONS.*`, never literals):
- `distribution_orders` — SSOT per order: `buzzebeeOrderId`, `customerId` (Buzzebee), `importBatchId`,
  `recipient {name, phone, address, subdistrict, district, province, postalCode}`, `zoneKey`, `lat`/`lng`,
  `items[] {sku, description, expectedQty, deliveredQty?, unitVolumeM3?, unitWeightKg?}`, `totalVolumeM3`,
  `totalWeightKg`, `status`, per-SKU `dispositions`, `pod {photos[], signatureUrl, leftAtDrop, deliveredAt}`,
  `callHistory[] {calledAt, outcome, note}`, `assignedTaskId`, `assignedDriverId`, `dayKey`, immutable
  `statusHistory[]`.
- `order_import_batches` — per uploaded file: `fileName`, `uploadedBy/At`, `customerId`, totals, accepted/rejected,
  `confirmSentAt`, email audit.
- `return_manifests` — per return: `driverId`, `dayKey`, `truckId`, `items[]{sku,qty}`, `receiverSignatureUrl`,
  `photoUrl`, `emailRecipients[]`, `emailSentAt`, `submittedAt`.
- `daily_dispatch` — per driver/day: totals (received/delivered/left/returned), `varianceItems[]`,
  `emptyContainerPhotoUrl`, `closedAt`, `reconciled`.
- Optional `distribution_zones` master (subdistrict → zone label + default truck).
- `trucks` + `cargoVolumeM3`; `tasks` + `taskType` value `"DISTRIBUTION"` (additive to `TASK_TYPE_ENUM`,
  `validate/taskSchema.ts:30`); `customers` doc `code: "BUZZEBEE"`.

**Cloud Functions** (all `onCall`, region `asia-southeast1`, `enforceAppCheck:false` + manual auth; `functions/src/`):
- `sendTransactionalEmail` (`email.ts`, ADR 0021) — SMTP via Google Workspace; provider-swappable transport.
- `geocodeDistributionOrders` — batch geocode addresses at import, reuse `GOOGLE_MAPS_API_KEY`
  (`distances.ts:22`).
- `confirmOrdersToBuzzebee` — build Excel + call email; stamp `order_import_batches`.
- Reuse `createOrUpdateTask` (`tasks.ts`) for assignment; return-submit and day-close writes are direct client
  writes guarded by rules (mobile pattern), with the email side effect via `sendTransactionalEmail`.
- ⚠️ Billing: **not touched** in this spec (out of scope) — so **no** change to `lib/billingCompute.ts` /
  `functions/src/core/billingCompute.ts`. When distribution billing is specced later, the two-file sync rule applies.

**Web (Next.js)** — `app/app/distribution/**` + `features/distribution/{api,components,hooks}`:
- Import dialog (reuse SheetJS pattern from `TollExpenseImportDialog.tsx` header probing +
  `sources/pickup-import-dialog.tsx` code→id lookup).
- Allocation page: truck-count selector, auto-partition, **Google Maps JS map at ตำบล level** (new client
  integration — existing maps are Leaflet, `package.json:65-75`; NOT reusing `SourcesMap.tsx`), orders coloured by
  truck/zone, per-truck capacity meters, drag-adjust, accept/reject. Add a Google Maps React wrapper (e.g.
  `@vis.gl/react-google-maps`) + a browser Maps JS key (HTTP-referrer-restricted, `NEXT_PUBLIC_*`), separate from
  the server `GOOGLE_MAPS_API_KEY`.
- Confirm-back (export xlsx via `xlsx-js-style` + email), assign (reuse `TruckPlateField`, `taskService`).
- Customer portal page (customer-scoped tracking) — reuse `useCustomerScope`, `PagePermissionGuard`.
- i18n: `context/locales/{en,th}/distribution.ts` registered in `context/locales/index.ts`.

**Mobile (Flutter)** — `lib/features/distribution/` (adapt `delivery_phase_page_multi.dart` scaffold):
- Goods-receipt page (day total + per-SKU received + photo); work-slip list (reorder); per-order POD sheet
  (countable photo + on-glass signature + per-SKU qty + left_at_drop); call button (`tel:`) + call log;
  warehouse-return page (list+qty + receiver signature + photo + submit→email); day-close (empty-container + check).
- **New deps:** a `signature` capture (package or `CustomPainter`→PNG, fed into the existing
  `uploadTripPhoto`/no-overlay pipeline like `isScreenshotPhotoType`); `tel:` launch via `url_launcher` + a
  `<queries>` `tel:` entry in `android/app/src/main/AndroidManifest.xml`.
- **Phone-GPS reporting (ADR 0022):** a debounced service that, while `activeTruck` is set **and** the truck has no
  `GPSVehicleId`, writes the phone's position to `vehicle_locations/{truckId}` as `source: "mobile"` (reuse existing
  `geolocator: ^14.0.2`, `pubspec.yaml:45`; add a foreground-service / background-location capability + permissions);
  stops when the route ends.
- Reuse `photo_overlay_service.dart`, `trip_records_repository.dart` upload, `truck_picker_field.dart`,
  `driver_repository.dart` (`activeTruck`), `cloud_functions_service.dart`; **bump `pubspec.yaml`**.
- i18n keys added to **both** `assets/translations/en.json` and `th.json`.

**Firestore Rules** — add blocks for the 4 new collections above the catch-all deny (`firestore.rules:546`):
admin full access via `isWebAdmin()` (`:13`); Buzzebee read of its own `distribution_orders` via `isCustomer()`
(`:18-21`) scoped by `customerScopeId`; driver create/update of their own order POD / return / day-close; email &
geocode audit fields written server-side. **Extend `vehicle_locations` (`firestore.rules:302-305`, currently
admin-write)** so a driver may write `vehicle_locations/{truckId}` iff `activeTruck.truckId == truckId` and
`request.resource.data.source == 'mobile'` (ADR 0022). Add composite indexes (`firestore.indexes.json`) for by-day /
by-driver / by-customer / by-status queries.

## 5. Affected files

- **New (web):** `lib/collections.ts` (+4), `features/distribution/**`, `app/app/distribution/**`,
  `validate/distributionOrderSchema.ts`, `context/locales/{en,th}/distribution.ts` (+ `index.ts`),
  `functions/src/email.ts`, `functions/src/distribution.ts` (geocode + confirm-back), `functions/src/index.ts`
  (exports); web `package.json` gains a Google Maps JS React wrapper (e.g. `@vis.gl/react-google-maps`) + browser
  Maps JS key env (`NEXT_PUBLIC_*`).
- **Edit (web):** `validate/truckSchema.ts` (`cargoVolumeM3`) + truck form, `validate/taskSchema.ts`
  (`TASK_TYPE_ENUM` += `"DISTRIBUTION"`) + `shared-docs/schemas/taskSchema.ts` (keep in sync), `firestore.rules`,
  `firestore.indexes.json`, `lib/capabilities.ts` / `lib/roles.ts` / `components/app-sidebar.tsx` /
  `components/page-permission-guard.tsx` (RBAC + nav), `functions/package.json` (`nodemailer`).
- **New/edit (mobile):** `lib/features/distribution/**`, a phone-GPS reporting service (ADR 0022),
  `pubspec.yaml` (signature dep + foreground-service/background-location dep + version bump),
  `android/app/src/main/AndroidManifest.xml` (`tel:` queries + background-location permissions),
  `assets/translations/{en,th}.json`.
- **Docs:** this spec, ADR 0020/0021/0022, `shared-docs/glossary.md`, `.vibe-rules.md` Change Log.

## 6. Task breakdown

**Phase 0 — Foundation**
- [ ] T0.1 Add 4 collections to `lib/collections.ts`; `distributionOrderSchema.ts`; `cargoVolumeM3` on truck schema+form.
- [ ] T0.2 `TASK_TYPE_ENUM += "DISTRIBUTION"` (both `validate/` and `shared-docs/schemas/` copies).
- [ ] T0.3 Zone/subdistrict derivation + `geocodeDistributionOrders` callable (reuse `GOOGLE_MAPS_API_KEY`).
- [ ] T0.4 Buzzebee `customers` doc; RBAC capabilities + routes + sidebar; `firestore.rules` + indexes.
- [ ] T0.5 `distribution` i18n namespace (en+th) skeleton.

**Phase 1 — Admin**
- [ ] T1.1 Import dialog (parse + per-SKU + validate + preview + geocode + write orders/batch).
- [ ] T1.2 Allocation page: truck-count selector + auto-partition + map + capacity meters + drag-adjust + accept/reject.
- [ ] T1.3 Confirm-back: Excel export + `confirmOrdersToBuzzebee` (email) + batch state.
- [ ] T1.4 Assign load → `task` (DISTRIBUTION) + link orders.

**Phase 2 — Driver (mobile)**
- [ ] T2.1 Goods-receipt page + day summary + `daily_dispatch` seed.
- [ ] T2.2 Work-slip list + reorder (outer→inner) + call (`tel:`) + call log.
- [ ] T2.3 POD sheet: countable photo + on-glass signature + per-SKU qty + left_at_drop + dispositions.
- [ ] T2.4 Warehouse return: auto-required-qty + reconcile + receiver signature + photo + submit → email.
- [ ] T2.5 Day-close: conservation check + empty-container photo + `reconciled`.
- [ ] T2.6 Bump `pubspec.yaml`; add signature dep + `tel:` `<queries>`.
- [ ] T2.7 Phone-GPS fallback service (ADR 0022): report to `vehicle_locations` (`source:"mobile"`) while
  `activeTruck` set and truck has no `GPSVehicleId`; rules + foreground-service/permission.

**Phase 3 — Customer portal**
- [ ] T3.1 Customer-scoped per-order tracking page (timeline + POD), rules + guard.

**Cross-cutting**
- [ ] TX.1 Email transport (`email.ts`, ADR 0021) + `nodemailer` + Workspace SMTP secret.
- [ ] TX.2 i18n en + th complete for every new string (web + mobile).
- [ ] TX.3 Update `.vibe-rules.md` Change Log.

## 7. Acceptance criteria

- [ ] AC1. (R1/R2) Importing a Buzzebee file creates `distribution_orders` with per-SKU items and geocoded lat/lng;
  bad rows are flagged and excluded; a `order_import_batches` doc records the run.
- [ ] AC2. (R3/R4) With N trucks selected, orders auto-split into N loads within each truck's volume+weight caps;
  the ตำบล map shows colour-by-truck with an overflow warning; admin can move an order and it re-meters live.
- [ ] AC3. (R5/R6) Accepted set exports to Excel + emails Buzzebee; each load becomes a `DISTRIBUTION` task whose
  orders carry `assignedTaskId`.
- [ ] AC4. (R7/R8) Driver goods-receipt reconciles vs Σexpected; each order can be called (logged), reordered, and
  completed with a disposition capturing per-SKU delivered qty, countable photo, and on-glass signature.
- [ ] AC5. (R9) Return list auto-fills required qty; a receiver signature + photo are captured; submit writes
  `return_manifests` and sends the manifest email to warehouse + Buzzebee.
- [ ] AC6. (R10) Day-close is blocked until every SKU balances (received = delivered + left + returned + reasoned
  variance) and an empty-container photo is present; then `daily_dispatch.reconciled` is set.
- [ ] AC7. (R11) A Buzzebee-scoped login sees only its own orders with a status timeline + POD; other customers' and
  admin-only data are inaccessible.
- [ ] AC10. (R13) A driver on a truck with no `GPSVehicleId` appears on the map from phone GPS (`source:"mobile"`)
  while on a route and disappears/stops updating when the route ends; a truck with a Cartrack device is never
  phone-tracked.
- [ ] AC8. (R12/N2) Existing FIRST_MILE/LINE_HAUL tasks, billing, and Driver Monitor behave unchanged.
- [ ] AC9. (N1/N6) Every new string exists in en + th; `tsc --noEmit` and `dart analyze` pass; CI green.

## 8. Risks & rollback

| Risk | Mitigation / rollback |
|------|----------------------|
| Buzzebee file layout differs from assumptions (missing SKU/dimensions) | Tolerant header mapping + preview; unknown-volume falls back to weight-only capacity; **needs a sample file** (open Q) |
| Geocoding fails / costs on messy Thai addresses | Flag + manual pin-drop before allocate; batch + cache; monitor Maps quota |
| Manual counts are wrong despite the photo | Accepted (ADR 0020/0019); conservation check catches *imbalance*, not a wrong-but-balanced count |
| Mobile-invokable email abused as relay | Server-controlled recipients + owner-driver auth (ADR 0021 §3-4) |
| New collections/rules mis-scoped leak data | Default-deny baseline (`firestore.rules:546`); scope tests before enabling portal |
| Scope is large | Phased build (0→3); each phase independently shippable; feature is additive so rollback = hide nav + stop importing |
| Billing expectation creep | Explicitly out of scope; separate ADR before any pricing code |

## 9. Open questions / follow-ups

- **Sample Buzzebee order file** — exact columns: does it carry per-SKU dimensions/volume/weight, a usable
  subdistrict, and a stable order id? (drives R1/R2/R3 field mapping).
- Zone model: derive `zoneKey` straight from subdistrict, or maintain a `distribution_zones` master with default
  trucks? (start derived; add master if ops need it).
- "Left at drop" — does Buzzebee require pre-approval, or is the checkbox + photo notification sufficient?
- Signature: pick the `signature` package vs a hand-rolled `CustomPainter`→PNG.
- **Maps:** distribution uses Google Maps JS. The owner wants Google's detail "ทั้งหมด" — should the existing
  **Leaflet** maps (`SourcesMap.tsx`, `LocationPicker.tsx`, dashboard/incident/session-location) be **migrated
  app-wide to Google Maps** (and Leaflet deps dropped) as a follow-up, or only distribution for now?
- **Phone-GPS fallback (ADR 0022):** enable for **all** task types (any GPS-less truck with an active driver) or
  distribution-only first? And which foreground-service / background-location package + throttle (time vs distance)?
- **Billing for distribution** — pricing model (per order / drop / day) → separate ADR + spec (follow-up).
- Workspace SMTP daily sending limit vs expected manifest volume; SPF/DKIM/DMARC readiness of the sending domain.
