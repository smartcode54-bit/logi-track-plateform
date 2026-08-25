# ADR 0022 — Phone-GPS fallback for trucks without a hardware GPS device

- **Status:** Accepted (2026-08-25) — implementation pending
- **Deciders:** Samart Kas (product owner), Claude
- **Area:** logitrack-mobile (driver app location reporting), logitrack-web (`vehicle_locations`, dashboard/
  distribution maps, `firestore.rules`, Cloud Functions)

## Context

Live vehicle position on the platform comes **only from hardware GPS**: a scheduled Cartrack sync writes
`vehicle_locations` (`COLLECTIONS.VEHICLE_LOCATIONS`, `lib/collections.ts:94`) via the Admin SDK, and a truck is
linked to a device through `trucks.GPSVehicleId` (`validate/truckSchema.ts:40`). Trucks **without** a device
(`GPSVehicleId` empty — common for subcontractor vehicles) are simply invisible on the map. The Buzzebee
distribution feature ([ADR 0020](0020-buzzebee-last-mile-distribution.md)) needs live position for the allocation/
monitor map, so the owner asked: **when a truck has no GPS, use the driver's phone GPS instead.**

Facts that shape the design:

- **The phone can already read GPS.** `geolocator: ^14.0.2` is a dependency (`pubspec.yaml:45`) and is used for
  **point-in-time** capture at check-in/delivery/expense (`check_in_page.dart`, `delivery_phase_page*.dart`, …).
  What is missing is **periodic reporting** of position to Firestore while a route is running.
- **`vehicle_locations` is admin-write today** (`firestore.rules:302-305`: `read` = any auth, `write` =
  `isWebAdmin()`), populated by the Cartrack scheduled sweep. No client writes it.
- **The driver's current truck is already known on the driver doc.** `drivers/{id}.activeTruck =
  {truckId, truckPlate, taskId}` is written at check-in and cleared at job end (glossary [[activeTruck]],
  `check_in_page.dart:1062-1071`) — precisely because Firestore rules cannot query tasks.
- **No Firestore document triggers** (region `asia-southeast3`); reactive server work is callables/sweeps
  (glossary [[Denormalization (in this codebase)]]).
- Continuous phone tracking is a **privacy + battery** concern, so it must be bounded, not always-on.

## Decision

1. **Hardware GPS is authoritative; the phone is a fallback only for a truck with no device.** The phone reports
   position **only when the assigned truck's `GPSVehicleId` is empty**. A truck with a Cartrack device is never
   phone-tracked; the two sources are disjoint by truck, so they cannot fight over the same document.

2. **Location provenance is explicit via a `source` field.** Each `vehicle_locations/{truckId}` doc carries
   `source: "cartrack" | "mobile"`, plus `lat`/`lng`, `accuracy`, `heading?`, `speed?`, `driverId` (when mobile),
   `updatedAt`. Consumers that show "where is the truck" read the doc as-is; a consumer that must distinguish a
   device fix from a phone fix reads `source`. The Cartrack sweep only ever writes `source: "cartrack"`.

3. **The phone reports only while the driver is on an active route, and debounced.** Reporting is gated by
   `drivers/{id}.activeTruck` being set (i.e. a job is in progress) — the same lifecycle that already scopes truck
   responsibility — and throttled to **every ~2 min or ~1000 m moved**, mirroring the debounce of the existing
   mobile heartbeat. When the route ends / `activeTruck` is cleared, reporting stops. This bounds the privacy and
   battery cost to working time on a GPS-less truck. **This ~2 min / ~1000 m cadence applies to the
   `source: "mobile"` path only** — the Cartrack **hardware** path keeps its existing sync cadence unchanged (this
   ADR does not alter device reporting).

4. **The phone writes via a lightweight guarded client write, keyed by `truckId`.** Doc id is the `truckId` (so the
   map keys on [[Truck identity]], not the driver). `firestore.rules` is extended so a **driver may write
   `vehicle_locations/{truckId}` only when `drivers/{their id}.activeTruck.truckId == truckId` and
   `request.resource.data.source == 'mobile'`**; admin/Cartrack (Admin SDK) remains allowed and writes
   `source: 'cartrack'`. Direct write is chosen over a per-tick callable to avoid a callable invocation every ~2 min
   per active driver. (If abuse or cross-writes become a concern, a `reportVehicleLocation` callable behind the
   same rule is the fallback — API-versioning safe.)

5. **This is a platform capability, first consumed by distribution.** The mechanism (phone fallback for any
   GPS-less truck with an active driver) is general and directly benefits the existing dashboard vehicle map
   (`DashboardVehicleMapClient`). The Buzzebee spec adopts it in its mobile phase; whether to enable it for **all**
   task types at once, or distribution-only first, is a rollout choice recorded as an open question in the spec.

## Consequences

**Positive**
- GPS-less trucks (many subcontractor vehicles) appear on the map with no new hardware.
- `source`-tagged `vehicle_locations` means the dashboard and distribution maps consume phone fixes transparently.
- Bounding reporting to `activeTruck` reuses an existing lifecycle and keeps tracking to working time only.

**Negative / risks**
- **Battery + reliability.** Reliable periodic/background updates on Android may need a foreground-service
  (persistent notification) and the right location permission tier; iOS needs background-location modes. This is
  net-new mobile plumbing on top of `geolocator`.
- **Privacy.** The app tracks the driver's phone while on a route — must be disclosed, scoped to `activeTruck`, and
  never run off-shift.
- **Trust.** A direct client write means a driver *could* spoof a position; acceptable for a fleet-ops app, and the
  rule still binds the write to their own active truck. Escalate to a callable if needed (decision 4).
- **Accuracy differs** from a wired device (phone in a bag/tunnel); `accuracy` is stored so consumers can down-weight.

**Follow-ups**
- Confirm the foreground-service / background-location package and the exact throttle (distance vs time).
- Decide whether the customer portal shows live position at all (currently scoped to a status timeline, not a live
  map — [ADR 0020](0020-buzzebee-last-mile-distribution.md) §9 / spec out-of-scope).

## Alternatives considered

- **Per-tick callable (`reportVehicleLocation`) for every update.** Rejected as the default: a callable invocation
  every ~2 min per active driver is needless cost/latency for a single small write; kept as the escalation path if
  the guarded direct write proves abusable.
- **A separate `mobile_vehicle_locations` collection.** Rejected: it would force every map consumer to merge two
  collections; one collection with a `source` discriminator keyed by `truckId` lets existing readers work unchanged.
- **Always-on phone tracking (ignore `activeTruck`).** Rejected on privacy/battery grounds — tracking off-shift is
  neither needed nor acceptable.
- **Let the phone override even when a device exists.** Rejected: a wired device is more reliable; phone is strictly
  a fallback for absent hardware.

## Related

- Consumer: [ADR 0020](0020-buzzebee-last-mile-distribution.md) (distribution live map); spec
  `shared-docs/specs/buzzebee-distribution-last-mile.md`.
- Reuses: glossary [[activeTruck]] / [[Truck identity]]; `vehicle_locations` (`lib/collections.ts:94`,
  `firestore.rules:302-305`); `geolocator` (`pubspec.yaml:45`).
- Glossary term: [[Vehicle location source]] in [../glossary.md](../glossary.md).
