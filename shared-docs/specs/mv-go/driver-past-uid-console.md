# Driver past Auth UIDs in the Go-backed console — 2026-11-04

Status: Implemented (the ETL heal); the insert-time half is PR #234

Ports main's driver auth-UID history (`drivers.authIdHistory`, `.vibe-rules.md` "🪪 Driver identity", 2026-11-01)
into the console mv-go serves from Go. The web half of that feature was dropped by the merge `a53bdd02`, because
it edited the legacy Firestore pages the Go rewrite had already replaced.

## Where past-UID resolution belongs: the ETL, not the browser

The Go schema already carries a stable driver id, so the console never sees an Auth UID:

| | 3.x web (Firestore) | mv-go console (Go) |
|---|---|---|
| a history row names its driver by | `driverId`: the Auth UID current when it was written | `driver_id uuid REFERENCES drivers(id)`, with the raw value kept in `legacy_driver_ref` and how it matched in `driver_ref_match` |
| the driver's name | mapped in the browser from the `drivers` listing | resolved in SQL by the join |
| the driver filter | compares `row.driverId` against the picker's id in the browser | `?driverId=<PostgreSQL id>`, in SQL |
| the date range | sliced in the browser after a capped read | `?from&to`, in SQL |

`trip_records`, `standby_records`, `incident_reports` and `vehicle_expenses` all follow that shape, and so do
`tasks`, `chats`, `transactions`, `driver_penalties`, `payroll_runs` and `leave_requests`. Every Go DTO carries
`driverId` plus a server-resolved `driverName` (`features/accounting/api/{billing,expenses,income}.go.ts`,
`features/drivers/api/monitor.go.ts`, `features/driver-compensation/api/{payroll,penalties}.go.ts`), and the picker
(`features/accounting/api/pickers.go.ts`) hands out PostgreSQL ids.

So the console has nothing to fan out: a past uid has to become a `driver_id` once, where `legacy_driver_ref` is
read. That is the ETL, in two places:

1. **While a row loads** — `driverByRef` (`internal/etl/lookups.go`) tries `legacy_doc_id`, then
   `legacy_auth_uid`, then a past uid of the driver document's `authIdHistory`. This is what a cut-over full load
   needs, because drivers load before the collections that name them. **Implemented in open PR #234**
   (`mv-go-sync-2026-10-27`), found while writing this; not duplicated here.
2. **For a row already stored** — `afterDrivers` (`internal/etl/map_master.go`, registered on the `drivers`
   collection). Added here. A row an earlier load stored unresolved keeps `driver_id` NULL, and no later load of
   *its own* document revisits it, so (1) alone never reaches it: the old uid becomes known only when the 3.x side
   records it (`buildDriverRelinkUpdate`, the `backfillDriverAuthHistory` callable), which changes the **driver's**
   document, not the trip's. The hook points those rows at the driver on the next load that applies the driver
   document.

Both are needed and neither replaces the other: (1) covers rows arriving with a past uid, (2) rows already stored.

### What `afterDrivers` does and does not touch

- Only `driverHistoryTables` = `trip_records`, `standby_records`, `incident_reports`, `vehicle_expenses` — the four
  collections `.vibe-rules.md` names, and the four the console reads as a driver's own history. `transactions` is
  an append-only ledger; `payroll_runs`, `driver_penalties`, `chats` and `leave_requests` are not read as history
  and `chats` carries the one-open-chat-per-driver index, so none of them is re-pointed.
- Only rows whose `tenant_id` is the driver's own. A row stamped elsewhere — the quarantine tenant included — is
  re-homed (`etl quarantine resolve --action=rehome`, the T28 routes), never re-pointed here, and a quarantined
  driver links nothing (Appendix C §C.1.8). A `vehicle_expenses` row with neither driver nor truck can only exist
  in the quarantine tenant (its CHECK), so it needs that re-home first.
- A uid another driver holds, as its `legacy_auth_uid` or as its `legacy_doc_id`, is skipped: only the server
  writes `authIdHistory`, but taking such an entry would hand one driver the other's history, the same way
  `authIdHistory` grants read access over a uid's rows (`.vibe-rules.md`). Those rows resolve to their own driver
  anyway, so nothing is lost.
- `legacy_driver_ref` is never rewritten, and neither is a Firestore `driverId`: the ban in `.vibe-rules.md` holds.
  Only `driver_id` and `driver_ref_match` ('auth_uid') are set, on rows where `driver_id IS NULL`, so the hook is
  idempotent. It bumps `updated_at` through `trg_set_updated_at`, as every ETL write does.
- A driver document with no `authIdHistory` costs no statement, which is almost every driver.
- The shadow projection does not write a trip's `driverId` back to Firestore (`ProjectedCollections` carries only
  the billing fields of trips and standby records), so a heal cannot rewrite a historical Firestore `driverId`. It
  does move `updated_at`, so a healed row can be re-projected with its unchanged billing fields once.

## The web side: nothing to change, nothing removed

`driverIdentityKeys` and `driverFilterKeys` (`logitrack-web/lib/driverName.ts`) and the i18n keys
`drivers.authHeal.scansExpenses` / `scannedExpenses` / `expenseCount` came in with `a53bdd02` and have no caller
on mv-go. They are **kept, deliberately**:

- `driverIdentityKeys` is the normative definition the other two implementations mirror —
  `logitrack-mobile/lib/core/utils/driver_identity.dart` says so, and `functions/src/core/driverAuthLink.ts` points
  at it. Both still run: the 3.x mobile app and the Cloud Functions are live.
- `driverFilterKeys` and the `authHeal` keys belong to pages `main` still has. Deleting them here would conflict
  with every remaining main → mv-go sync for no gain in the Go console, which cannot use them.

There is no Go-backed home for the `authHeal` repair tool and none is added: its Go equivalent is the
`driver_unresolved` finding (`etl status`, `etl quarantine list --reason=driver_unresolved`) plus this heal, both
operator-side, and the console has no ETL-findings page to hang it on.

## Not done

- `functions/src/driverCompensation.ts` still counts a driver's trips by `driverId == authId` alone, so a payroll
  run computed on the 3.x path misses trips written under a past uid. `main` has the same gap; mv-go replaces that
  path with Go payroll, so it is left alone here.
- The shadow projection writes `drivers.authId` after a Go re-link but does not append the old uid to
  `authIdHistory` (also recorded in `main-feature-sync-2026-10-27.md`). Until it does, a re-link done *in Go* leaves
  the 3.x web and mobile showing raw uids, and leaves this heal with nothing to match.
- Rows whose driver is in the quarantine tenant, or whose own tenant differs from their driver's, stay unresolved
  until they are re-homed.

## Validation

- `TestPastAuthUIDHealsTheDriverHistoryRowsAlreadyStored` (`internal/etl/engine_integration_test.go`, PostgreSQL):
  a trip and an incident stored under the replaced uid are pointed at the driver with `driver_ref_match` 'auth_uid';
  a row naming a uid no driver claims, a quarantined row and a row of the driver whose own current uid the history
  names are left alone; a second load changes nothing. With the hook unregistered the three positive assertions
  fail.
