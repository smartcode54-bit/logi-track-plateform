# Appendix A — Data model and PostgreSQL 18 DDL

Part of the mv-go migration documentation. The summary of this appendix (principles, tenancy classification, table list per domain) is [main spec §3](../../../developer-spec.md); this appendix carries the full, runnable DDL. Siblings: [Appendix B — API catalog](./B-api-catalog.md), [Appendix C — Auth and RBAC](./C-auth-rbac.md), [Appendix D — Seed and mock data](./D-seed-and-mock-data.md), [Appendix E — Web fetch audit](./E-web-fetch-audit.md). Decision record: [ADR 0029](../../adr/0029-migrate-firebase-stack-to-go-postgres.md).

## How to read this appendix

- **A.1** restates the design principles. **A.2** gives one `sql` block per goose file (`logitrack-api/migrations/NNNN_name.sql`, Up and Down) plus **Notes** (business rules, legacy Firestore source, index → live query): A.2.0–A.2.4 = files 0001–0005; A.2.5–A.2.8 (0006–0009), the ETL mapping (§A.3) and decisions D1–D8 (§A.4) follow. **A.2.R** / **A.2.S** list every rename or move against the earlier drafts.
- Each block is the complete file except the policy layer of [Appendix C §C.3](./C-auth-rbac.md). Appendix C §C.3.0 is the coverage table (R67): this appendix runs `ENABLE` + `FORCE ROW LEVEL SECURITY` on exactly the 70 tables it marks "yes" (all 47 tables of 0002–0005 among them) and on none of the 11 exempt tables or schema `etl`. **Each table's `CREATE POLICY` statements and generator calls (Appendix C §C.3.5) ship in the same migration file as the table, after its `CREATE TABLE`**, so no migration leaves an RLS table deny-all. Appendix C also adds, to the file it names, its generators and `trg_freeze_tenant_id()` (0001), `app_status_entity_visible()`, `t_users_self_columns` and `t_file_objects_commit_columns` (0002), the four scope helpers, `t_driver_self_columns`, `t_driver_link_membership` and three `scope_*` views (0003), four `scope_*` views (0004) and `app_driver_truck_ids()` (0006); each Up section below ends with a comment naming that block at its position, and the Down sections drop those objects (`IF EXISTS`; views and cross-table policies before the tables they read, functions after the tables that use them).
- Printed here, one copy per migration file: the GUC readers (incl. `app_subtenant_ids()` … `app_quarantine_tenant_id()`, A.2.0) and the allocators `next_task_seq()` / `next_invoice_seq()` (A.2.3, A.2.4), which Appendix C §C.3.3 reprints to explain them, and the two objects Appendix C §C.3.6 / §C.3.7 describe without a body, `trg_tenant_admin_columns()` (A.2.1) and `driver_directory()` (A.2.2). 0009 hands the allocators, the scope helpers, `driver_directory()` and `trg_driver_link_membership()` to `logitrack_rls_definer` (R66); Downs drop them through `app_drop_definer_function()` (0001).
- Function bodies and `DO` blocks are wrapped in `-- +goose StatementBegin` / `StatementEnd` (goose otherwise splits on `;`). sqlc reads only the Up sections.
- Citations are repo-relative `path:line` from the fact-base reports or files read in the repo; "UNVERIFIED:" marks an unconfirmed claim with the reason; owner decisions point to main spec §19.2 ("question N"). Files 0001–0005 plus the Appendix C additions were run on `postgres:18-alpine` (18.4): Up, Down, Up, and Down again after the A.2.8 hand-over.
- The whole chain lives in `logitrack-api/migrations/` (0001 from T03, 0002–0010 from T04): each file is the block below with its Appendix C block inserted at the marked position (function bodies and `DO` blocks wrapped for goose). Tests on `postgres:18-alpine`: `migrations/schema_integration_test.go` (tables = the `CREATE TABLE`s of this appendix, ids, generated columns, `tenant_source`, vocabularies, immutability, allocators, the D5 guard), `internal/platform/db/rls_catalog_test.go` (Appendix C §C.3.0 and §C.3.2 read from that document), and the R31 round trip.

### Decisions applied

Plan §7 (R1–R35) and the cross-document resolutions (R36–R89, binding where they differ) that shape files 0001–0005:

| # | Applied here as | Where |
|---|---|---|
| R1 | One object registry `file_objects` (`status`, `purpose`, `uploaded_by`, `tenant_id`, `expires_at`), in 0002 so every `*_file_id` FK resolves | A.2.1 |
| R2, R82, R83 | `sessions` (+ `active_tenant_id`, `install_id`, one live session per install), `refresh_tokens` (family, reuse detection, 30 s grace of R37), `password_reset_tokens`, `api_keys` (+ `scope integration\|script\|cf_shim\|release_publisher`) | A.2.1 |
| R6, R86 | `tenants` = carriers (subcontractor profile folded in); `billing_parties` single FK target for billed/linked parties; `memberships`, `user_platform_roles`, `role_capability_overrides`; `user_scopes.kind` `customer\|dispatcher` only | A.2.1, A.2.2 |
| R7, R56 | One `own_fleet` row (seed/ETL data, uuid from `OWN_FLEET_TENANT_ID`); the quarantine row inserted by 0002 with the fixed id `00000000-0000-7000-8000-00000000000f` (= `app_quarantine_tenant_id()`); `tenants.contractor_tenant_id uuid NULL REFERENCES tenants(id)` | A.2.1 |
| R10 | Task numbers global per `(task_type, plan_date)` | A.2.3 |
| R11, R58 | Inline `tenant_source text NOT NULL CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine'))` on every tenant-stamped table; unresolvable rows load into the quarantine tenant | A.1.2 |
| R12, R13, R28 | Per-row `legacy_driver_ref` + `driver_ref_match`; nullable `drivers.legacy_doc_id`; scalar `helper_driver_id` (check-in ownership); dispatcher-created tasks stamped `form` | A.1.1, A.1.2 |
| R14 | `statement_documents`, `billing_statement_lines`, `hub_soc_distances(direction)`, `trip_no_history`; truck current assignment derived | A.2.2–A.2.4 |
| R15, R62 | `tasks.truck_type` NULL legal; unpriced reasons `no_customer\|no_rate\|no_vehicle_class\|no_billing_date` (trips), `no_customer\|no_rate\|no_ended_at` (standby) | A.2.3, A.2.4 |
| R16–R20, R53 | Rate-entry tie-break; `last_event_id`; money writes read PostgreSQL only; WHT `NUMERIC(5,4)` snapshot; `NUMERIC(14,2)`; standby rates soft-deleted; `photo_type_known` STORED | A.1.5, A.2.3, A.2.4 |
| R30, R47 | Evidence tokens non-expiring; `evidence_token_revoked_at` on `trip_records` and `standby_records` (revoke routes in Appendix B); no separate link table | A.2.3 |
| R31 | Every file has a Down | A.2 |
| R34, R57 | Every id `uuid DEFAULT uuidv7()`, `status_history` and `trip_no_history` included; the only identity column is `outbox_events.id` (0009), stored by `last_event_id bigint` without FK; generated columns `STORED` | A.1.1, A.1.10 |
| R55 | `users.legacy_auth_uid` / `drivers.legacy_auth_uid`; scrypt pair (bytea), `status`, `auth_version`, `must_change_password`, `password_changed_at`; no platform-admin flag, revocation epoch or force-logout column | A.2.1 |
| R59, R88 | Baseline 0001–0009 in P0; `0010_d5_unique_constraints.sql` after the quarantine sign-off | below |
| R60, R61 | Contractor reach (`app.subtenant_ids`, `app_tenant_in_reach()`) and steward rule (`app.steward`); rate tables, snapshots, `billing_counters` (`tenant_id` + `tenant_source`) and statements carry the billing carrier's tenant; standby billing inline, hidden by a service projection | A.1.2, A.2.4 |
| R63 | `client_op_id uuid` + partial `UNIQUE (driver_id, client_op_id) WHERE client_op_id IS NOT NULL` on `tasks`, `standby_records`, `incident_reports` (`vehicle_expenses`, `leave_requests`: 0006 / 0008) | A.2.3 |
| R65, R73 | Statuses lower_snake, domain codes keep their case; catalog 81 keys (77 + 4 platform) in Go | A.1.7 |
| R66, R87 | Roles from `deploy/postgres-init/00-roles.sql`, only asserted by 0001; 0009 single grant site; `cmd/seed` writes as `logitrack_etl` (`ETL_DATABASE_URL`), `seed --verify` as `logitrack_app` (`DATABASE_URL`) | A.2, A.2.0 |
| R67 | `ENABLE` + `FORCE ROW LEVEL SECURITY` on exactly the Appendix C §C.3.0 "yes" tables; counters reachable only through the allocators | A.1.2 |
| R68 | Trip `created_at` = `COALESCE(createdAt, std, deliveredTimestamp, Firestore create time)` with `created_at_derived` | A.2.3 |

### Migration files

| File | Content | Section |
|---|---|---|
| `0001_preamble.sql` | `citext`, schema `etl`, role assertion (no role creation, no grants), Bangkok-day helpers, all RLS GUC readers, generic trigger functions, Down-only helper `app_drop_definer_function()` (+ Appendix C policy generators) | A.2.0 |
| `0002_identity.sql` | `tenants` (+ `contractor_tenant_id`, quarantine row), `users`, `file_objects`, `tenant_files`, `auth_identities`, `sessions`, `refresh_tokens`, `password_reset_tokens`, `api_keys`, `memberships`, `user_platform_roles`, `role_capability_overrides`, `status_history`, trigger `t_tenant_admin_columns` | A.2.1 |
| `0003_master.sql` | `customers`, `customer_driver_id_types`, `billing_parties`, `companies`, `user_scopes`, `hubs`, `hub_name_aliases`, `hub_soc_distances`, `drivers`, `driver_customer_codes`, `trucks`, `truck_files`, `truck_assignments`, `driver_directory()` | A.2.2 |
| `0004_operations.sql` | `task_number_counters` + `next_task_seq()`, `tasks`, `task_delivery_stops`, `trip_records`, `trip_no_history`, `trip_delivery_stops`, `trip_photos`, `standby_records`, `standby_photos`, `incident_reports`, `vehicle_locations`, tenant-consistency constraint triggers | A.2.3 |
| `0005_billing.sql` | rate entries, fuel adjustments, service fees, standby rates, trip billing snapshots + stop breakdown, billing counters + `next_invoice_seq()`, statements, statement lines, `statement_documents` | A.2.4 |
| `0006_finance_hr.sql` | vehicle expenses, maintenance, ledger transactions, compensation config, penalties, payroll, driver advances | A.2.5 |
| `0007_comms.sql` | device tokens, chats, messages, read state, broadcasts, recipients, reads, notification log | A.2.6 |
| `0008_platform.sql` | settings, mobile app releases, security events, mobile installations, holidays, leave requests, waitlist, partner interest, fuel snapshots | A.2.7 |
| `0009_infra.sql` | `outbox_events`, `consumer_inbox`, `idempotency_keys`, `jobs`, `etl.source_docs`, `etl.quarantine`, `etl.watermarks`, `etl.reconciliation_runs`; single grant site (`logitrack_readonly` included); hand-over of the SECURITY DEFINER functions to `logitrack_rls_definer` | A.2.8 |

Phase placement (R59, R88): `0001`–`0009` are applied together in **P0** (issue T04); phases P1–P7 change which side *writes* a table, not whether it exists, so phase schema tasks become data-layer tasks. `0010_d5_unique_constraints.sql` (`-- +goose NO TRANSACTION`, `CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS`: `chats_one_open_per_driver`, `vehicle_expenses_fuel_taxinv`, `customer_service_fees_one_per_type`) is authored by T04 and applied in production as a step of the P1 full-load runbook (T24): `goose up-to 9` → ETL → owner's quarantine sign-off → `goose up` (§A.4 D5). Dev, CI and seeded databases apply it immediately; later changes are 0011+.

## A.1 Design principles

### A.1.1 Keys and identity

- Every table with a surrogate key uses `id uuid PRIMARY KEY DEFAULT uuidv7()`. `uuidv7()` is native in PostgreSQL 18 (no extension), so ids are time-ordered at the source: btree inserts stay append-mostly, keyset pagination on `(created_at, id)` has a monotonic tie-break, and R16's "new rows by `created_at ASC, id ASC`" rule holds without Go generating ids. Go never supplies an id for a new row. The append-only logs `status_history` and `trip_no_history` use `uuidv7()` like every other table (R57); the only `bigint GENERATED ALWAYS AS IDENTITY` column is `outbox_events.id` (0009, the relay order), whose values `trip_billing_snapshots.last_event_id` stores without a foreign key.
- Every migrated table carries `legacy_doc_id text` with a partial unique index `WHERE legacy_doc_id IS NOT NULL`. Natural keys that were Firestore doc ids become ordinary UNIQUE columns: the driver-typed trip number (`trip_records.trip_no`), `billing_counters/{customerId}_{YYYYMM}` (composite PK), `hub_soc_distances/{hub}_{soc}` (`UNIQUE (hub_id, soc_key, direction)`), `vehicle_locations/{GPSVehicleId}` (PK `truck_id`), payroll and fuel snapshot keys (0006/0008).
- One driver identity: `drivers.id` is the FK everywhere a legacy `driverId` appeared; `drivers.user_id` (UNIQUE) replaces `drivers.authId` / legacy `authUid`. Legacy `driverId` meant the Auth uid in `trip_records`, `standby_records`, `chats`, `payroll`, `driver_penalties`, `incidentReport`, `vehicle_expenses` and the drivers doc id in `tasks`, `truckAssignment`, `mobile_installations`, `leave_requests` (schemas report IDENTITY rule; `logitrack-web/functions/src/driverCompensation.ts:199-204` queries `tasks.driverId == authId`, a live bug). ETL resolves each value to `drivers.id` and keeps the raw value in `legacy_driver_ref` + `driver_ref_match` (`doc_id|auth_uid|name|none`, R12); `drivers.legacy_doc_id` is nullable (Go-created drivers have none).
- Trip identity: surrogate `trip_records.id` + mutable `trip_no`. `renameTripRecord` (copy + delete, `logitrack-web/functions/src/renameTripRecord.ts:51`) becomes `UPDATE ... SET trip_no` plus a `trip_no_history` row; incidents FK the uuid. The doc-id charset rule (`logitrack-web/functions/src/core/tripDocId.ts:10-18`) is a CHECK on `trip_no`.
- `tasks.task_no` (`FM-ddMMyyyy-NNN`) is not unique in legacy data (web pads to 3 digits, mobile and `getNextTaskId` do not). It is indexed, not unique. New numbers come from `next_task_seq(task_type, plan_date)` inside the insert transaction: a SECURITY DEFINER allocator over `task_number_counters (task_type, plan_date)`, the only write path to that table, **global across tenants** so the dispatcher's cross-tenant list never shows two identical numbers (R10, R66).

### A.1.2 Tenancy

ADR 0026 is cited throughout as "ADR 0026 (text not on disk; semantics from branch glossary + `tenantResolve.ts`)" (R32): neither the ADR file nor its spec exists on `origin/feat/multi-tenant-carrier-isolation`; the rules come from that branch's glossary, `functions/src/core/tenantResolve.ts` and `functions/src/tenantLookups.ts`. ADR 0029 restates them.

- `tenants` = carrier organisations: every legacy `subcontractors` doc becomes `kind='carrier'` (profile folded in, no `subcontractors` table), plus exactly one `kind='own_fleet'` (Wanpen-Ratchada) and one `kind='quarantine'`, each enforced by a partial unique index. The own-fleet tenant is data, not a constant (the branch moved it to `settings/tenancy.ownFleetTenantId`, `functions/src/tenantLookups.ts:16-21` on that branch, because the hardcoded `DEFAULT_CUSTOMER_ID`, `logitrack-web/functions/src/backfillCustomerLinks.ts:6`, was the mistake; R7): `cmd/seed` / `cmd/etl` insert it with the uuid from `OWN_FLEET_TENANT_ID`. The quarantine tenant is structural: 0002 inserts it with the fixed id `00000000-0000-7000-8000-00000000000f` that `app_quarantine_tenant_id()` returns (R56).
- Contractor reach (R60, owner confirmation: main spec §19.2 question 13): `tenants.contractor_tenant_id` names the tenant a carrier works for (one level, carriers only); ETL sets it to the own fleet for every tenant built from a legacy `subcontractors` doc. `db.WithPrincipal` loads the active tenant's sub-tenants into `app.subtenant_ids` and staff policies test `app_tenant_in_reach(tenant_id)`, so own-fleet staff keep today's view of the trips their contractors run while a carrier working directly for a dispatcher stays isolated. Global master writes, PUBLIC holidays and platform-wide broadcasts also need `app.steward` (own-fleet staff or platform_admin).
- Every tenant-stamped table has `tenant_id uuid NOT NULL REFERENCES tenants(id)` and the inline `tenant_source` CHECK with all seven values (R11, R58). The value a row receives follows the resolver chain of `tenantResolve.ts`, enforced in Go: tasks ← driver (`form` without a driver, R13); trips ← task, driver; standby ← task, trip, driver; incidents ← trip, driver; drivers ← `self` (subcontractorId) or own fleet; trucks ← `self` (ownershipType) or own fleet; truck assignments, maintenance, expenses without a driver, vehicle locations ← `truck`; rate tables, statements, counters, compensation config ← `form` (the billing carrier, own fleet today). `driver` is an approximation (R24 seed fixture). Unresolvable rows load, never drop, into the quarantine tenant with `tenant_source='quarantine'`; `tenancy.orphan-scan` counts them and `POST /v1/tenants/quarantine/rows/{table}/{id}/rehome` moves them.
- `tenant_id` is frozen at insert (`t_freeze_tenant_id`, attached by the tenant-table generator of Appendix C §C.3.5). The only moves — the audited task reassignment (R13, `PATCH /v1/tasks/{id}` with `tenantId`), the driver move (Appendix C §C.1.4) and quarantine re-homing — each run in their own transaction with `app.tenant_move=on`; deferred constraint triggers in 0004 keep trips, standby records and incidents on their parent's tenant.
- Row-level security is the enforcement layer; **Appendix C §C.3.0 is the coverage table** (R67). Every request runs in a transaction opened by `db.WithPrincipal`, which sets `app.user_id`, `app.tenant_id`, `app.role`, `app.driver_id`, `app.customer_ids`, `app.subtenant_ids`, `app.dispatcher`, `app.steward` and `app.bypass_tenant` (transaction-local `set_config`); workers and scheduler use `db.WithSystem(ctx, tenantID)`, which sets `app.bypass_tenant` (R12); `cmd/etl` and the `cmd/seed` writes log in as `logitrack_etl` (BYPASSRLS). Dispatcher is an orthogonal axis (`user_scopes(kind='dispatcher')`), not a column.

70 of the 81 tables in `public` get `ENABLE` + `FORCE ROW LEVEL SECURITY`; the 11 exempt tables and the 4 tables of schema `etl` get none and are reached only through their service and the explicit grants of 0009:

| Family (Appendix C §C.3.0) | Tables (migration) |
|---|---|
| tenant, operational (staff reach = own tenant + sub-tenants; quarantine rows hidden; driver / scope branches per Appendix C) — 14 | `tenants` (keyed on `id`), `tenant_files` (0002); `companies`, `drivers`, `trucks`, `truck_assignments` (0003); `tasks`, `trip_records`, `standby_records`, `incident_reports`, `vehicle_locations` (0004); `chats` (0007); `mobile_installations`, `leave_requests` (0008) |
| tenant, carrier-internal (never dispatcher or scope principals) — 13 | `customer_rate_entries`, `customer_fuel_rate_adjustments`, `customer_service_fees`, `standby_rate_entries`, `trip_billing_snapshots`, `billing_statements` (0005); `vehicle_expenses`, `maintenance_records`, `transactions`, `driver_compensation_configs`, `driver_penalties`, `payroll_runs`, `driver_advances` (0006) |
| parent-scoped (no `tenant_id`; read when the parent is visible, write when it is in reach) — 21 | `status_history` (polymorphic) (0002); `driver_customer_codes`, `truck_files` (0003); `task_delivery_stops`, `trip_no_history`, `trip_delivery_stops`, `trip_photos`, `standby_photos` (0004); `trip_billing_stop_breakdown`, `billing_statement_lines`, `statement_documents` (0005); `maintenance_files`, `penalty_types`, `payroll_line_items`, `payroll_penalty_applications` (0006); `device_tokens` (parent `users`), `chat_messages`, `chat_read_state`, `broadcast_recipients`, `broadcast_reads` (0007); `leave_request_attachments` (0008) |
| nullable-tenant (NULL = platform-wide row) — 4 | `file_objects` (0002); `broadcasts` (0007); `security_events`, `holidays` (0008) |
| global-read-steward-write (R60) — 6 | `customers`, `customer_driver_id_types`, `billing_parties`, `hubs`, `hub_name_aliases`, `hub_soc_distances` (0003) |
| platform-only (written under `WithSystem` or by a SECURITY DEFINER allocator; principals see at most self / reach rows) — 12 | `users`, `auth_identities`, `user_platform_roles`, `sessions`, `refresh_tokens`, `password_reset_tokens`, `api_keys`, `memberships`, `role_capability_overrides` (0002); `user_scopes` (0003); `task_number_counters` (0004); `billing_counters` (0005) |
| exempt-service-layer, no RLS — 11 + `etl` | `notification_deliveries` (0007); `settings`, `mobile_app_releases`, `waitlist`, `partner_interest`, `fuel_daily_snapshots`, `fuel_monthly_snapshots` (0008); `outbox_events` (nullable `tenant_id`), `consumer_inbox`, `idempotency_keys`, `jobs`, `etl.*` (0009) |

Billing tables carry the **billing carrier's** tenant, not the tenant that ran the trip (R61: the rate-card owner, the own fleet today). Scope principals and evidence viewers never read `file_objects` directly: the storage service resolves a key under `WithSystem` only after the referencing row was read under the principal.

### A.1.3 Billing party

`tasks.billingCustomerId` and every `*LinkedCustomerId` may point at a `customers` doc **or** a `subcontractors` doc (schemas report, Task relations; ADR 0028). Instead of two nullable FKs on about ten tables, `billing_parties(id, kind customer|tenant, customer_id|tenant_id)` is the single FK target for every `billing_*` / `*_linked_party_id` reference and the single home of `billing_date_basis`; the legacy resolution order customers → subcontractors → `'delivered'` (`logitrack-web/functions/src/tripBillingOnDelivered.ts:45-64`) collapses into one column at ETL. `user_scopes` (customer and dispatcher scopes) and `app_customer_ids()` hold `billing_parties.id` values (R6, R12).

### A.1.4 Time

- All instants are `timestamptz`. Every Bangkok-calendar fact gets an explicit `date` column computed by the IMMUTABLE function `bkk_date(timestamptz)` = `((ts AT TIME ZONE 'UTC') + interval '7 hours')::date`, which is `bangkokDateStrFromMillis` (`logitrack-web/lib/billingCompute.ts:168-178`). Thailand has a fixed +07:00 offset with no DST, so IMMUTABLE is honest and the function can back STORED generated columns and indexes (`tasks.plan_date`, `trip_records.billing_axis_date`, `standby_records.billing_axis_date`).
- Legacy date-only strings (`registrationDate`, `taxExpiryDate`, insurance dates, maintenance dates, `transactions.date`) become `date`; `sealTime` (`dd-MM-yyyy HH:mm:ss`) is parsed as Bangkok local time.
- Announcement rows store both `effective_from_date date` (the selection key: `isEffectiveOnOrBeforeBillingDate` compares Bangkok dates, `logitrack-web/lib/billingCompute.ts:191-193`) and `effective_from_at timestamptz` (the legacy instant, used only for the within-day tie sort). Legacy UTC-midnight rows (07:00 ICT, before 2026-08-09) need no rewrite because both conventions give the same Bangkok day.
- `created_at` / `updated_at` default to server `now()`. Where the legacy value was device time and a business axis (`trip_records.createdAt` = Depart, the Driver Monitor axis), ETL keeps it in `created_at`; from cut-over the API writes device time into `std`, `check_in_at`, `delivered_at`.

### A.1.5 Money

- THB amounts are `NUMERIC(14,2)` everywhere (R20); rate multipliers `NUMERIC(10,6)`; fuel prices `NUMERIC(8,2)`; company withholding percentage `NUMERIC(5,2)`; statement withholding fraction `NUMERIC(5,4)` (R18).
- Parity-critical arithmetic (`computeFinalRateThb`, fuel band floor and surcharge) stays in the Go `billing/compute` package in float64 with the JS-rounding helper (`Math.round` ties toward +∞) and the FMA-blocking cast; the database stores results only. Integer satang in the DB would force a ×100 boundary on every reader for no parity gain.
- Legacy stored amounts are either already rounded to 2 dp or unrounded float sums (multi-drop `totalBillingThb`, statement `grandTotal`, `netAmount`, payroll totals). ETL rounds the sums half-up in float64 before the cast; parity tests allow 0.005 THB only for legacy multi-drop totals and `netAmount`, everything else is exact (R20). Legacy statements are loaded as stored, never recomputed.
- Whole-baht fields (compensation, penalties; 0006) keep `NUMERIC(14,2)` with a `= trunc(x)` CHECK so Go's `roundTHB` stays the only rounding site.

### A.1.6 JSONB vs child tables

Child tables for anything queried, joined, counted, uniquely constrained or appended: photos, delivery stops and stop progress, multi-drop breakdown, status history, statement lines, rendered documents, payroll lines, penalty applications, read state, device tokens, hub name aliases, driver customer codes, file links. JSONB only for write-once, provider-shaped blobs read whole: `trip_records.ocr_data`, fuel snapshot items, `security_events.details`, compensation tiers, `settings.value`, `etl.source_docs.raw`. Firestore maps keyed by uid (`lastReadByAdmin`, `fcmTokens`, `customerDriverIds`) always become child tables.

### A.1.7 Enum strategy

PostgreSQL `ENUM` types are not used: they cannot drop or rename values, and legacy data drifts (Title-case task status vs lowercase trip status; maintenance `'PM Booking'|'Scheduled'|'In-Progress'|'in_progress'`; trucks `'Active'|'Available'`). Vocabularies are `text` + `CHECK`, statuses canonical lower_snake (D2, closed in ADR 0029, R30, R65); the v1 mobile shim and the Firestore compat projection translate to legacy literals through one mapping table (main spec §13.8). Unmappable values load as NULL with an `etl.quarantine` entry, never coerced. Domain codes keep their case (`PRIMARY|SUPPLEMENTARY`, `HUB|SOC`, `SPX|SPK`, vehicle classes). Lookup tables exist only where a value carries attributes (`penalty_types`, 0006); roles and capabilities are code (81 keys (77 + 4 platform), R5, R73). Trip photo types are free text with a STORED `photo_type_known` flag because `stop_{i}_*` is dynamic and ADR 0018 makes `photos[]` the whole evidence set (R19).

### A.1.8 Immutability

Enforced twice: (a) `REVOKE UPDATE, DELETE` (or `DELETE`) from `logitrack_app` and `logitrack_etl` at the 0009 grant site, (b) a raising BEFORE row trigger next to the table, so the owner fails loudly too.

| Rule | Tables | Mechanism |
|---|---|---|
| Append-only | `status_history`, `trip_no_history` (0002/0004); `billing_statement_lines` (0005; delete only by cascade from a draft statement); `transactions`, `fuel_daily_snapshots`, `security_events` (0006/0008) | `trg_forbid_mutation` / statement-line guard + REVOKE |
| Void-only (announcement rows, ADR 0009 §1; `firestore.rules:142-154`) | `customer_rate_entries`, `customer_fuel_rate_adjustments` | `trg_announcement_void_only` allows only `voided` false→true plus `voided_at`, `voided_by`, `voided_reason`, `updated_at` (the rule's whitelist includes `updatedAt`, `firestore.rules:144`); DELETE forbidden |
| Soft delete only | `standby_rate_entries` (R20) | `voided_at`; DELETE forbidden because `standby_records.billing_rate_entry_id` FKs it |
| Draft-only delete | `billing_statements` | `trg_billing_statement_draft_only_delete` |
| Frozen after draft | `payroll_line_items` (0006) | trigger in 0006 |

### A.1.9 Cache-backed tables

PostgreSQL is the source of truth. Redis (prefix `lt:{APP_ENV}:`, R26; canonical key table in [Appendix B](./B-api-catalog.md)) holds read-through copies invalidated by a post-commit `DEL` plus the relayed domain event; TTL is only a safety net. **Money writes never read Redis** (R17, R53): period locks and the four rate tables are read inside the pricing transaction with `SELECT ... FOR SHARE`, and hub codes come from `hubs` / `hub_name_aliases` in the same transaction; the hub, rate-card, customer and period-lock keys below are UI read paths only.

| Source tables | Key (after the prefix) | Invalidated by |
|---|---|---|
| `hubs`, `hub_name_aliases` | `cache:hubs:all`; `cache:hubs:n2c` (alias → code) and `cache:hubs:c2n` (code → `name_th`), two keys, never merged (`shared-docs/.vibe-rules.md` Confirmed Patterns, rule against merging `nameToCode` and `codeToName`, as of commit 4f552099) | `hubs.changed` |
| `customer_rate_entries`, `customer_fuel_rate_adjustments`, `customer_service_fees`, `standby_rate_entries` | `cache:ratecard:{billingPartyId}` | `ratecard.changed` |
| `customers`, `billing_parties` | `cache:customer:{id}` | customer write, `customers.changed` |
| `billing_statements` (sent/paid) | `cache:period_locks:{billingPartyId}` | `statement.status_changed` |
| `settings`, `mobile_app_releases` | `cache:settings:{key}` | `settings.changed` (SSE `mobile_settings.changed`) |
| `vehicle_locations` | `cache:vehicle_locations:{tenantId}` | each `cartrack.sync` run |
| `tenants` (own fleet row) | `cache:tenant:own_fleet` | `tenant.created`, `tenant.updated` |
| `tenants.contractor_tenant_id` | sub-tenant list of a tenant (Appendix C §C.3.4) | `tenant.created`, `tenant.updated` |
| `role_capability_overrides` + Go catalog | `rbac:caps:{tenantId\|platform}:{role}:{ver}`, versioned by `rbac:ver` | `INCR rbac:ver` on `PUT /v1/roles/matrix` |
| `refresh_tokens` | `auth:rt:{sha256(token)}` | rotation, logout, revoke |
| `sessions` (revoked) | `auth:sess:revoked:{sid}` | revoke |
| `users.auth_version` | `auth:user:ver:{userId}` | role, scope, platform role, driver link, disable, password |
| `users.legacy_auth_uid` | `auth:fbuid:{firebaseUid}` | revocation post-commit hook |
| `idempotency_keys` (0009) | `idem:http:{userId}:{key}` | Redis TTL 24 h; the PostgreSQL row is durable until `IDEMPOTENCY_TTL` (default 168h, R53) |

### A.1.10 PostgreSQL 18 specifics

- Image `postgres:18-alpine`. PGDATA is `/var/lib/postgresql/18/docker`, so the compose volume mounts `/var/lib/postgresql` (not `/var/lib/postgresql/data`); main spec §15.
- `uuidv7()` is native; no `pgcrypto` / `uuid-ossp`. The only extension is `citext`.
- PostgreSQL 18 makes `GENERATED ALWAYS AS (...)` **VIRTUAL** by default. Every generated column here is written `GENERATED ALWAYS AS (...) STORED` explicitly, because each one is indexed, used in RLS or used as a billing axis, and virtual columns cannot be indexed.
- `UNIQUE NULLS NOT DISTINCT` (PostgreSQL 15+) replaces a `COALESCE(..., sentinel)` expression index for the key with a nullable part (`role_capability_overrides`, NULL tenant = platform-wide override).
- `FORCE ROW LEVEL SECURITY` makes the owner `logitrack_migrator` obey the policies too; `BYPASSRLS` / `NOBYPASSRLS` are per login role and never inherited, so every process logs in as its own role (R66). 0001 checks with `pg_has_role(..., 'SET')` (PostgreSQL 16+) that the migrator may hand functions to `logitrack_rls_definer`; since that membership is `WITH INHERIT FALSE`, Down sections drop handed-over functions through `app_drop_definer_function()` (a migration-only, transaction-local `SET ROLE`).
- Not used in the parity round ("optional later" in ADR 0029, R34): `WITHOUT OVERLAPS` keys, `RETURNING OLD/NEW`. pgx v5.11, sqlc v1.31, goose v3.28 support PostgreSQL 18.

## A.2 DDL

Conventions not repeated per table: mutable tables carry `created_at` / `updated_at timestamptz NOT NULL DEFAULT now()`, the latter maintained by `trg_set_updated_at`; migrated tables carry `legacy_doc_id text` with a partial unique index; money is `NUMERIC(14,2)`; every RLS table is followed by `ALTER TABLE ... ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;` (idempotent next to the Appendix C generator calls, which run the same two statements), and its `CREATE POLICY` statements ship in the same file (each Up section ends with a comment marking that block). `tenants` is enabled after the quarantine insert, because a forced table without policies denies the owner too.

Roles and privileges (R66, R87). `deploy/postgres-init/00-roles.sql` (Appendix C §C.3.2; superuser, once per cluster) creates `logitrack_migrator` (LOGIN, owns every object), `logitrack_app` (LOGIN, NOBYPASSRLS), `logitrack_etl` (LOGIN, BYPASSRLS), `logitrack_readonly` (LOGIN) and `logitrack_rls_definer` (NOLOGIN, BYPASSRLS, owns the SECURITY DEFINER helpers); no migration creates or alters a role. Grants are issued once, in 0009 (A.2.8). Each process logs in as its own role and never uses `SET ROLE`: `api`, `worker`, `scheduler`, `seed --verify` → `logitrack_app` (`DATABASE_URL`); goose and the seed's TRUNCATE / schema checks → `logitrack_migrator` (`MIGRATE_DATABASE_URL`); `cmd/etl` and `cmd/seed` writes → `logitrack_etl` (`ETL_DATABASE_URL`); reporting → `logitrack_readonly` (operator-held). Go data migrations run through `db.WithSystem`.

### A.2.0 Preamble (0001_preamble.sql)

```sql
-- 0001_preamble.sql
-- +goose Up
-- Roles come from deploy/postgres-init/00-roles.sql (R66); no migration creates a role or holds a credential.
-- Assert the login and the five roles first, so a wrong URL or a missing role fails before any object exists.
-- +goose StatementBegin
DO $$
DECLARE
  r record;
BEGIN
  IF current_user <> 'logitrack_migrator' THEN
    RAISE EXCEPTION 'migrations run as logitrack_migrator (MIGRATE_DATABASE_URL), not %', current_user
      USING ERRCODE = 'insufficient_privilege';
  END IF;
  FOR r IN SELECT * FROM (VALUES
      ('logitrack_migrator',    true,  false),
      ('logitrack_app',         true,  false),
      ('logitrack_etl',         true,  true),
      ('logitrack_readonly',    true,  false),
      ('logitrack_rls_definer', false, true)) AS v(role_name, can_login, bypass_rls)
  LOOP
    IF NOT EXISTS (SELECT 1 FROM pg_roles
                    WHERE rolname = r.role_name AND rolcanlogin = r.can_login
                      AND rolbypassrls = r.bypass_rls AND NOT rolsuper) THEN
      RAISE EXCEPTION 'role % is missing or has wrong attributes (want LOGIN=%, BYPASSRLS=%, NOSUPERUSER); run deploy/postgres-init/00-roles.sql',
        r.role_name, r.can_login, r.bypass_rls
        USING ERRCODE = 'undefined_object';
    END IF;
  END LOOP;
  -- 0009 hands the SECURITY DEFINER functions to logitrack_rls_definer (ALTER FUNCTION ... OWNER TO needs SET).
  IF NOT pg_has_role('logitrack_migrator', 'logitrack_rls_definer', 'SET') THEN
    RAISE EXCEPTION 'logitrack_migrator must be granted logitrack_rls_definer WITH INHERIT FALSE, SET TRUE (00-roles.sql)'
      USING ERRCODE = 'insufficient_privilege';
  END IF;
END
$$;
-- +goose StatementEnd

CREATE EXTENSION IF NOT EXISTS citext;   -- case-insensitive emails and business codes
CREATE SCHEMA IF NOT EXISTS etl;         -- ETL bookkeeping; tables in 0009

-- No GRANT/REVOKE in 0001-0008: 0009_infra is the single grant site (A.2.8).

-- Bangkok calendar day of an instant (== bangkokDateStrFromMillis, logitrack-web/lib/billingCompute.ts:168-178).
-- Fixed UTC+7, no DST: IMMUTABLE is correct, so it can back STORED generated columns and indexes.
CREATE FUNCTION bkk_date(ts timestamptz) RETURNS date
  LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
  RETURN ((ts AT TIME ZONE 'UTC') + interval '7 hours')::date;

-- 00:00 Asia/Bangkok of a calendar day, as an instant.
CREATE FUNCTION bkk_midnight(d date) RETURNS timestamptz
  LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
  RETURN (d::timestamp - interval '7 hours') AT TIME ZONE 'UTC';

-- RLS context helpers (GUC readers; Appendix C §C.3.3). db.WithPrincipal / db.WithSystem set the GUCs once per
-- transaction with set_config(name, value, true). An unset GUC yields NULL / '' / false, so a forgotten
-- WithPrincipal sees zero rows instead of all rows.
CREATE FUNCTION app_user_id() RETURNS uuid
  LANGUAGE sql STABLE PARALLEL SAFE
  RETURN nullif(current_setting('app.user_id', true), '')::uuid;

CREATE FUNCTION app_tenant_id() RETURNS uuid
  LANGUAGE sql STABLE PARALLEL SAFE
  RETURN nullif(current_setting('app.tenant_id', true), '')::uuid;

CREATE FUNCTION app_role() RETURNS text
  LANGUAGE sql STABLE PARALLEL SAFE
  RETURN coalesce(current_setting('app.role', true), '');

CREATE FUNCTION app_driver_id() RETURNS uuid
  LANGUAGE sql STABLE PARALLEL SAFE
  RETURN nullif(current_setting('app.driver_id', true), '')::uuid;

-- Comma-separated billing_parties.id values (customer and dispatcher scopes, R12).
CREATE FUNCTION app_customer_ids() RETURNS uuid[]
  LANGUAGE sql STABLE PARALLEL SAFE
  RETURN coalesce(string_to_array(nullif(current_setting('app.customer_ids', true), ''), ',')::uuid[], '{}'::uuid[]);

CREATE FUNCTION app_is_dispatcher() RETURNS boolean
  LANGUAGE sql STABLE PARALLEL SAFE
  RETURN coalesce(current_setting('app.dispatcher', true), '') = 'on';

CREATE FUNCTION app_bypass() RETURNS boolean
  LANGUAGE sql STABLE PARALLEL SAFE
  RETURN coalesce(current_setting('app.bypass_tenant', true), '') = 'on';

CREATE FUNCTION app_is_staff() RETURNS boolean
  LANGUAGE sql STABLE PARALLEL SAFE
  RETURN app_role() IN ('tenant_admin','manager','operation_staff','operator','user');

-- Contractor reach (R60): carrier tenants whose contractor_tenant_id is the active tenant (comma-separated).
CREATE FUNCTION app_subtenant_ids() RETURNS uuid[]
  LANGUAGE sql STABLE PARALLEL SAFE
  RETURN coalesce(string_to_array(nullif(current_setting('app.subtenant_ids', true), ''), ',')::uuid[], '{}'::uuid[]);

-- Steward (R60): staff role in the own-fleet tenant, or platform_admin.
CREATE FUNCTION app_is_steward() RETURNS boolean
  LANGUAGE sql STABLE PARALLEL SAFE
  RETURN coalesce(current_setting('app.steward', true), '') = 'on';

-- Set only by the three audited tenant-move paths (R13 task reassignment, driver move, quarantine re-home).
CREATE FUNCTION app_tenant_move_allowed() RETURNS boolean
  LANGUAGE sql STABLE PARALLEL SAFE
  RETURN coalesce(current_setting('app.tenant_move', true), '') = 'on';

-- Set only by cmd/etl.
CREATE FUNCTION app_etl_load() RETURNS boolean
  LANGUAGE sql STABLE PARALLEL SAFE
  RETURN coalesce(current_setting('app.etl_load', true), '') = 'on';

CREATE FUNCTION app_is_scope() RETURNS boolean
  LANGUAGE sql STABLE PARALLEL SAFE
  RETURN app_role() = 'customer' OR app_is_dispatcher();

CREATE FUNCTION app_is_authenticated() RETURNS boolean
  LANGUAGE sql STABLE PARALLEL SAFE
  RETURN app_user_id() IS NOT NULL OR app_bypass();

CREATE FUNCTION app_tenant_in_reach(t uuid) RETURNS boolean
  LANGUAGE sql STABLE PARALLEL SAFE
  RETURN t = app_tenant_id() OR t = ANY (app_subtenant_ids());

-- The quarantine tenant is structural: 0002 inserts its row with this id (R56).
CREATE FUNCTION app_quarantine_tenant_id() RETURNS uuid
  LANGUAGE sql IMMUTABLE PARALLEL SAFE
  RETURN '00000000-0000-7000-8000-00000000000f'::uuid;

-- +goose StatementBegin
CREATE FUNCTION trg_set_updated_at() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  NEW.updated_at := now();
  RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION trg_forbid_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION '% is append-only (% rejected)', TG_TABLE_NAME, TG_OP
    USING ERRCODE = 'integrity_constraint_violation';
END
$$;
-- +goose StatementEnd

-- Announcement rows (ADR 0009 §1; firestore.rules:142-154): the only legal UPDATE sets voided
-- false -> true plus the void columns; updated_at is whitelisted like firestore.rules:144.
-- +goose StatementBegin
CREATE FUNCTION trg_announcement_void_only() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  o jsonb;
  n jsonb;
BEGIN
  IF OLD.voided THEN
    RAISE EXCEPTION '% row % is already voided', TG_TABLE_NAME, OLD.id
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  IF NEW.voided IS DISTINCT FROM true OR NEW.voided_at IS NULL OR NEW.voided_by IS NULL THEN
    RAISE EXCEPTION '% rows are immutable; insert a new row or void this one', TG_TABLE_NAME
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  o := to_jsonb(OLD) - 'voided' - 'voided_at' - 'voided_by' - 'voided_reason' - 'updated_at';
  n := to_jsonb(NEW) - 'voided' - 'voided_at' - 'voided_by' - 'voided_reason' - 'updated_at';
  IF o IS DISTINCT FROM n THEN
    RAISE EXCEPTION '% void may not change other columns', TG_TABLE_NAME
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

-- Wake-up for the outbox relay (outbox_events is created in 0009).
-- +goose StatementBegin
CREATE FUNCTION trg_outbox_notify() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM pg_notify('outbox_new', NEW.id::text);
  RETURN NEW;
END
$$;
-- +goose StatementEnd

-- Down-only helper (R66). After 0009 hands a SECURITY DEFINER function to logitrack_rls_definer, the migrator
-- (member WITH INHERIT FALSE, SET TRUE) is not its owner; this drops it as the owner (transaction-local SET ROLE,
-- reset before returning). Migrator-owned functions are dropped directly, missing ones ignored.
-- +goose StatementBegin
CREATE PROCEDURE app_drop_definer_function(sig text) LANGUAGE plpgsql AS $$
DECLARE
  fn       regprocedure := to_regprocedure(sig);
  fn_owner regrole;
BEGIN
  IF fn IS NULL THEN
    RETURN;
  END IF;
  SELECT proowner::regrole INTO fn_owner FROM pg_proc WHERE oid = fn;
  IF fn_owner = 'logitrack_rls_definer'::regrole THEN
    EXECUTE 'SET LOCAL ROLE logitrack_rls_definer';
    EXECUTE format('DROP FUNCTION %s', fn);
    EXECUTE 'RESET ROLE';
  ELSE
    EXECUTE format('DROP FUNCTION %s', fn);
  END IF;
END
$$;
-- +goose StatementEnd

-- Appendix C block (same file): §C.3.6 trg_freeze_tenant_id(); §C.3.5 generators app_rls_tenant_table,
-- app_rls_child_table, app_rls_global_table, app_rls_platform_table, app_rls_driver_rows.

-- +goose Down
-- Appendix C additions first (IF EXISTS keeps this Down runnable with or without them).
DROP PROCEDURE IF EXISTS app_rls_driver_rows(regclass, boolean, boolean);
DROP PROCEDURE IF EXISTS app_rls_platform_table(regclass);
DROP PROCEDURE IF EXISTS app_rls_global_table(regclass);
DROP PROCEDURE IF EXISTS app_rls_child_table(regclass, regclass, name, name, boolean, boolean);
DROP PROCEDURE IF EXISTS app_rls_tenant_table(regclass);
DROP FUNCTION IF EXISTS trg_freeze_tenant_id();
DROP PROCEDURE app_drop_definer_function(text);
DROP FUNCTION trg_outbox_notify();
DROP FUNCTION trg_announcement_void_only();
DROP FUNCTION trg_forbid_mutation();
DROP FUNCTION trg_set_updated_at();
-- SQL-standard bodies record dependencies: drop the composed helpers before the readers they call.
DROP FUNCTION app_quarantine_tenant_id();
DROP FUNCTION app_tenant_in_reach(uuid);
DROP FUNCTION app_is_authenticated();
DROP FUNCTION app_is_scope();
DROP FUNCTION app_etl_load();
DROP FUNCTION app_tenant_move_allowed();
DROP FUNCTION app_is_steward();
DROP FUNCTION app_subtenant_ids();
DROP FUNCTION app_is_staff();
DROP FUNCTION app_bypass();
DROP FUNCTION app_is_dispatcher();
DROP FUNCTION app_customer_ids();
DROP FUNCTION app_driver_id();
DROP FUNCTION app_role();
DROP FUNCTION app_tenant_id();
DROP FUNCTION app_user_id();
DROP FUNCTION bkk_midnight(date);
DROP FUNCTION bkk_date(timestamptz);
DROP SCHEMA etl;
-- Not reverted on purpose: the citext extension (may serve other schemas; re-Up uses IF NOT EXISTS)
-- and the five roles (created by deploy/postgres-init/00-roles.sql, cluster-wide, never by a migration).
```

**Notes (0001)**

- Authored with T03 as `logitrack-api/migrations/0001_preamble.sql`, the Appendix C block inline (each generator and trigger body in its own `StatementBegin` / `StatementEnd`). goose applies the file in one transaction, so a refused login or a missing role rolls back every object of 0001 (no `citext`, no `etl`); the assertion is the first statement so that its error is the first one reported, and `cmd/migrate` checks the login before goose runs as well. goose itself commits `goose_db_version` before 0001 runs, owned by whichever login ran it: after a run outside `cmd/migrate` with the wrong login, drop that table (it holds no applied version) before `migrate up`.
- Roles (R66, A.2): the assertion also rejects a superuser login (it would silently bypass RLS) and checks that `logitrack_migrator` may `SET ROLE logitrack_rls_definer` (0009 hand-over, `app_drop_definer_function()`). `logitrack_readonly` sees RLS tables only after an operator-logged `app.bypass_tenant`. Every privilege comes from 0009.
- `app.bypass_tenant` is an ordinary GUC; its protection is that only `db.WithSystem` and the read-only `X-Act-On-Tenant: *` path set it and that `logitrack_app` connections run sqlc-generated statements only. Appendix C §C.3 owns the policy semantics.
- `app_subtenant_ids()` / `app_tenant_in_reach()` implement contractor reach and `app_is_steward()` the steward rule (R60); `app_tenant_move_allowed()` and `app_etl_load()` are read by `trg_freeze_tenant_id` (Appendix C §C.3.6); `app_is_scope()` marks customer-scope and dispatcher principals; `app_quarantine_tenant_id()` is IMMUTABLE and equals the id of the row 0002 inserts. `app_customer_ids()` holds `billing_parties.id` values (JWT `cs`, from `user_scopes.billing_party_id`), never `customers.id` (R12).
- The helpers are SQL-standard-body functions (`RETURN ...`), so the planner can inline them into policy predicates; `PARALLEL SAFE` because custom GUCs are copied to parallel workers (dashboard aggregates stay parallel-eligible).
- `bkk_date` / `bkk_midnight` replace the Bangkok-day helpers of `logitrack-web/lib/billingCompute.ts` and `logitrack-web/functions/src/core/billingCompute.ts` at the storage layer; the Go `clock` package keeps the same arithmetic. `trg_outbox_notify` lives here so every later file depends only on earlier objects.
- `app_drop_definer_function(sig)` serves only the Down sections of 0003–0005, so `up → down → up` (R31) round-trips whether or not 0009 ran; any other caller fails, because only the owner or a role allowed to `SET ROLE logitrack_rls_definer` can complete the `DROP`.
- goose's bookkeeping table `goose_db_version` (created by goose as `logitrack_migrator`) is not one of the 81 tables of Appendix C §C.3.0, carries no RLS and is excluded by name from the C.3.8 catalog test.

### A.2.1 Identity and tenancy (0002_identity.sql)

Creation order resolves the circular references without deferred FKs: `tenants` (self-reference `contractor_tenant_id`; quarantine row inserted, then RLS enabled) → `users` (photo FK added later) → `file_objects` (FKs `users`, `tenants`) → `ALTER TABLE users ADD ... photo FK` → every table that references `file_objects` or `users`. `user_scopes` is **not** in this file: it references `billing_parties`, so it is created in 0003 after `billing_parties` (A.2.2), with its FKs inline instead of a later `ALTER`. All 13 tables of this file carry RLS (Appendix C §C.3.0); their policies follow at the end of the Up section.

```sql
-- 0002_identity.sql
-- +goose Up

-- Carrier organisations (ADR 0026 semantics: text not on disk; semantics from branch glossary + tenantResolve.ts).
-- One row per legacy subcontractors doc (kind='carrier'), one own_fleet row, one quarantine row (R6/R7/R11).
CREATE TABLE tenants (
  id                uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id     text,                                  -- subcontractors/{id} (= legacy partnerScopeId value)
  kind              text NOT NULL CHECK (kind IN ('own_fleet','carrier','quarantine')),
  code              citext,                                -- short label (e.g. WRT); UNVERIFIED: subcontractors.code is read by
                                                           -- logitrack-web/functions/src/lineNotify.ts:141 but has no writer
  name_th           text NOT NULL,                         -- subcontractors.name
  name_en           text,
  -- carrier profile folded from subcontractors (no separate subcontractors table, R6)
  legal_type        text CHECK (legal_type IN ('individual','company')),   -- subcontractors.type
  id_card_number    text,                                  -- 13 digits when individual (PII)
  tax_id            text,                                  -- 13 digits when company
  contact_person    text,
  phone             text,
  email             citext,
  website           text,
  address           text,
  designation       text,
  fleet_size        int  NOT NULL DEFAULT 0 CHECK (fleet_size >= 0),
  dispatch_center   text,
  service_regions   text[] NOT NULL DEFAULT '{}',
  vehicle_types     text[] NOT NULL DEFAULT '{}',
  line_group_id     text,                                  -- LINE group id 'C...' (LINE push target)
  contractor_tenant_id uuid REFERENCES tenants(id),        -- contractor reach (R56/R60): the tenant this carrier works for;
                                                           -- own fleet for every legacy subcontractors doc; one level only
  status            text NOT NULL DEFAULT 'active' CHECK (status IN ('active','pending','suspended')),
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  CHECK (kind <> 'carrier' OR legal_type IS NOT NULL),
  CONSTRAINT tenants_contractor_carrier_only CHECK (contractor_tenant_id IS NULL OR kind = 'carrier'),
  CONSTRAINT tenants_contractor_not_self     CHECK (contractor_tenant_id IS DISTINCT FROM id)
);
CREATE UNIQUE INDEX tenants_one_own_fleet  ON tenants (kind) WHERE kind = 'own_fleet';
CREATE UNIQUE INDEX tenants_one_quarantine ON tenants (kind) WHERE kind = 'quarantine';
CREATE UNIQUE INDEX tenants_legacy         ON tenants (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE UNIQUE INDEX tenants_code           ON tenants (code) WHERE code IS NOT NULL;
CREATE INDEX tenants_kind_status           ON tenants (kind, status, name_th);
CREATE INDEX tenants_contractor            ON tenants (contractor_tenant_id) WHERE contractor_tenant_id IS NOT NULL;

-- Structural quarantine tenant (R56, Appendix C §C.1.8), id == app_quarantine_tenant_id(). Inserted before RLS is
-- enabled: a forced table without policies denies the owner too. The own-fleet row is seed/ETL data (R7).
INSERT INTO tenants (id, kind, name_th, name_en, status)
VALUES ('00000000-0000-7000-8000-00000000000f', 'quarantine', 'กักกันข้อมูล', 'Quarantine', 'active');
ALTER TABLE tenants ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (keyed on id)

CREATE TABLE users (
  id                     uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_auth_uid        text,                             -- Firebase Auth uid == users/{uid}; ETL join key and bridge key (R8, R55); kept forever
  email                  citext,                           -- UNVERIFIED: that every Firebase Auth account has an email (export not inspected);
                                                           -- nullable so such an account still loads (it cannot use password login until set)
  email_verified         boolean NOT NULL DEFAULT false,
  display_name           text,
  photo_file_id          uuid,                             -- FK users_photo_file_fk added after file_objects
  password_hash          text,                             -- Argon2id PHC string; NULL until first-login rehash, or Google-only
  password_changed_at    timestamptz,
  must_change_password   boolean NOT NULL DEFAULT false,   -- temporary passwords (R29) and ETL weak-password flag
  legacy_scrypt_hash     bytea,                            -- base64-decoded passwordHash from auth:export; NULLed after rehash
  legacy_scrypt_salt     bytea,                            -- purged 180 days after cut-over (R30)
  status                 text NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled','reset_required','deleted')),
  disabled_at            timestamptz,
  deleted_at             timestamptz,
  auth_version           int  NOT NULL DEFAULT 1 CHECK (auth_version >= 1),   -- JWT `ver`; bumped on role/scope/disable/password/driver-link change
  last_login_at          timestamptz,
  last_login_lat         double precision,
  last_login_lng         double precision,
  last_login_geo_source  text CHECK (last_login_geo_source IN ('gps','ip')),
  last_login_accuracy_m  double precision,
  legacy_auth_created_at timestamptz,                      -- Auth export createdAt
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now(),
  CHECK ((legacy_scrypt_hash IS NULL) = (legacy_scrypt_salt IS NULL)),
  CHECK (status <> 'disabled' OR disabled_at IS NOT NULL),
  CHECK (status <> 'deleted'  OR deleted_at  IS NOT NULL)
);
CREATE UNIQUE INDEX users_email      ON users (email) WHERE email IS NOT NULL AND status <> 'deleted';
CREATE UNIQUE INDEX users_legacy_uid ON users (legacy_auth_uid) WHERE legacy_auth_uid IS NOT NULL;
CREATE INDEX users_last_login        ON users (last_login_at DESC NULLS LAST, id DESC);
ALTER TABLE users ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (platform-only)

-- Registry of every MinIO/S3 object; every *_file_id column points here (R1). Moved from 0009 so the
-- FKs in 0002-0005 resolve. API bodies carry `key`; the service resolves key -> id at commit.
CREATE TABLE file_objects (
  id             uuid PRIMARY KEY DEFAULT uuidv7(),
  bucket         text NOT NULL,                            -- the S3_BUCKET (private) or S3_PUBLIC_BUCKET (app_releases/ only) value
  object_key     text NOT NULL,                            -- legacy Firebase path kept verbatim; new keys use entity uuids
  tenant_id      uuid REFERENCES tenants(id),              -- owning tenant; NULL = platform object (APK, unattributed legacy object)
  purpose        text NOT NULL CHECK (purpose ~ '^[a-z][a-z0-9_]*$'),   -- vocabulary in the notes below
  owner_kind     text CHECK (owner_kind IN ('trip','task','standby','incident','chat','driver','truck','company','customer',
                                            'tenant','maintenance','expense','leave','release','user','statement','report','penalty')),
  owner_id       uuid,                                     -- entity row, linked at commit; NULL while pending
  content_type   text,
  size_bytes     bigint CHECK (size_bytes >= 0),
  sha256         text CHECK (sha256 ~ '^[0-9a-f]{64}$'),
  visibility     text NOT NULL DEFAULT 'private' CHECK (visibility IN ('private','public')),
  status         text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','committed','missing_at_source')),
  legacy_url     text,                                     -- Firebase download URL with the token stripped (ETL only)
  uploaded_by    uuid REFERENCES users(id),
  expires_at     timestamptz,                              -- pending uploads only: storage.gc removes object + row after this
  committed_at   timestamptz,
  created_at     timestamptz NOT NULL DEFAULT now(),
  deleted_at     timestamptz,
  UNIQUE (bucket, object_key),
  CHECK (status <> 'pending'   OR expires_at   IS NOT NULL),
  CHECK (status <> 'committed' OR committed_at IS NOT NULL),
  CHECK ((owner_kind IS NULL) = (owner_id IS NULL)),
  CHECK (visibility = 'private' OR purpose = 'apk')
);
CREATE INDEX file_objects_owner   ON file_objects (owner_kind, owner_id) WHERE owner_id IS NOT NULL;
CREATE INDEX file_objects_gc      ON file_objects (expires_at) WHERE status = 'pending';
CREATE INDEX file_objects_missing ON file_objects (created_at) WHERE status = 'missing_at_source';
CREATE INDEX file_objects_tenant  ON file_objects (tenant_id, created_at DESC) WHERE tenant_id IS NOT NULL;
ALTER TABLE file_objects ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (nullable-tenant)

ALTER TABLE users ADD CONSTRAINT users_photo_file_fk
  FOREIGN KEY (photo_file_id) REFERENCES file_objects(id) ON DELETE SET NULL;

CREATE TABLE tenant_files (                                -- subcontractors.documents[]
  tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  file_id     uuid NOT NULL REFERENCES file_objects(id),
  kind        text NOT NULL CHECK (kind IN ('id_card','company_doc','other')),
  position    int  NOT NULL DEFAULT 0 CHECK (position >= 0),
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, file_id)
);
ALTER TABLE tenant_files ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (tenant)

CREATE TABLE auth_identities (                             -- external identity links: Google OIDC `sub`; legacy Firebase uid (ETL, §A.3.1)
  id               uuid PRIMARY KEY DEFAULT uuidv7(),
  user_id          uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  provider         text NOT NULL CHECK (provider IN ('google','firebase_legacy')),   -- widen when another IdP is added
  provider_subject text NOT NULL,                          -- Google `sub` (providerUserInfo[google.com].rawId); Firebase uid for firebase_legacy
  email_at_link    citext,
  linked_at        timestamptz NOT NULL DEFAULT now(),
  last_used_at     timestamptz,
  UNIQUE (provider, provider_subject),
  UNIQUE (user_id, provider)
);
ALTER TABLE auth_identities ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (platform-only)

CREATE TABLE sessions (                                    -- one per login on a device/browser (R2); JWT `sid`
  id                  uuid PRIMARY KEY DEFAULT uuidv7(),
  user_id             uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  platform            text NOT NULL CHECK (platform IN ('web','android','ios','script')),
  amr                 text NOT NULL CHECK (amr IN ('pwd','google')),
  active_tenant_id    uuid REFERENCES tenants(id),         -- JWT `tid`; written at login and by POST /v1/auth/tenant, re-issued on refresh (R83)
  install_id          text,                                -- mobile install id (API field installId, R83); NULL on web
  device_label        text,
  app_version         text,
  ip                  inet,
  user_agent          text,
  absolute_expires_at timestamptz NOT NULL,                -- web: created_at + 30 d; mobile: + REFRESH_TOKEN_TTL_MOBILE (90 d) (Appendix C §C.4.4)
  created_at          timestamptz NOT NULL DEFAULT now(),
  last_seen_at        timestamptz NOT NULL DEFAULT now(),
  revoked_at          timestamptz,
  revoked_by          uuid REFERENCES users(id),
  revoked_reason      text CHECK (revoked_reason IN ('logout','logout_all','admin_revoke','disabled','password_changed',
                                                     'password_reset','refresh_reuse','device_relogin','expired')),   -- Appendix C §C.4.4
  CHECK ((revoked_at IS NULL) = (revoked_reason IS NULL))
);
CREATE INDEX sessions_user_active ON sessions (user_id, last_seen_at DESC) WHERE revoked_at IS NULL;
CREATE INDEX sessions_expiry      ON sessions (absolute_expires_at);
-- One live session per (user, install): a re-login on the same device revokes the older one first (device_relogin, R83).
CREATE UNIQUE INDEX sessions_user_install_live ON sessions (user_id, install_id) WHERE install_id IS NOT NULL AND revoked_at IS NULL;
ALTER TABLE sessions ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (platform-only)

CREATE TABLE refresh_tokens (                              -- rotation chain; presenting a rotated token revokes the family (R2)
  id              uuid PRIMARY KEY DEFAULT uuidv7(),
  session_id      uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  family_id       uuid NOT NULL,
  token_hash      bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),   -- sha256(token); the token is never stored
  issued_at       timestamptz NOT NULL DEFAULT now(),
  expires_at      timestamptz NOT NULL,                    -- web: sliding 7 d, capped by sessions.absolute_expires_at
  rotated_at      timestamptz,
  replaced_by     uuid REFERENCES refresh_tokens(id),
  revoked_at      timestamptz,
  revoked_reason  text,
  CHECK ((rotated_at IS NULL) = (replaced_by IS NULL))
);
CREATE INDEX refresh_tokens_family  ON refresh_tokens (family_id);
CREATE INDEX refresh_tokens_session ON refresh_tokens (session_id);
CREATE INDEX refresh_tokens_expiry  ON refresh_tokens (expires_at);
ALTER TABLE refresh_tokens ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (platform-only)

CREATE TABLE password_reset_tokens (                       -- reset and invite links; passwords are never emailed (R29)
  id            uuid PRIMARY KEY DEFAULT uuidv7(),
  user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  purpose       text NOT NULL DEFAULT 'reset' CHECK (purpose IN ('reset','invite')),
  token_hash    bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
  expires_at    timestamptz NOT NULL,                      -- now() + PASSWORD_RESET_TTL
  used_at       timestamptz,
  requested_ip  inet,
  requested_by  uuid REFERENCES users(id),                 -- admin who sent an invite; NULL for self-service forgot-password
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX password_reset_tokens_user   ON password_reset_tokens (user_id, created_at DESC);
CREATE INDEX password_reset_tokens_expiry ON password_reset_tokens (expires_at) WHERE used_at IS NULL;
ALTER TABLE password_reset_tokens ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (platform-only)

CREATE TABLE api_keys (                                    -- machine principals: scripts, release CLI, callable shims (R25, R45)
  id            uuid PRIMARY KEY DEFAULT uuidv7(),
  name          text NOT NULL,
  scope         text NOT NULL CHECK (scope IN ('integration','script','cf_shim','release_publisher')),   -- R82
  key_prefix    text NOT NULL UNIQUE,                      -- first 8 characters, shown in the UI
  key_hash      bytea NOT NULL CHECK (octet_length(key_hash) = 32),   -- sha256(secret || API_KEY_PEPPER)
  tenant_id     uuid REFERENCES tenants(id),               -- NULL = platform-level key (only these may hold global-class and mobile:* keys; checked in Go)
  capabilities  text[] NOT NULL CHECK (cardinality(capabilities) > 0),
  rate_per_min  int  NOT NULL DEFAULT 600 CHECK (rate_per_min > 0),
  created_by    uuid NOT NULL REFERENCES users(id),
  expires_at    timestamptz,
  last_used_at  timestamptz,
  revoked_at    timestamptz,
  revoked_by    uuid REFERENCES users(id),
  created_at    timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT api_keys_cf_shim_platform CHECK (scope <> 'cf_shim' OR tenant_id IS NULL)   -- shim keys are platform keys (R45)
);
CREATE INDEX api_keys_tenant ON api_keys (tenant_id, created_at DESC) WHERE revoked_at IS NULL;
CREATE INDEX api_keys_scope  ON api_keys (scope) WHERE revoked_at IS NULL;
ALTER TABLE api_keys ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (platform-only)

CREATE TABLE memberships (                                 -- tenant role per (user, tenant) (R6)
  user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  role        text NOT NULL CONSTRAINT memberships_tenant_role_check
                CHECK (role IN ('tenant_admin','manager','operation_staff','operator','user','driver')),
  status      text NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended')),
  created_by  uuid REFERENCES users(id),
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, tenant_id)
);
CREATE INDEX memberships_by_tenant_role ON memberships (tenant_id, role);
ALTER TABLE memberships ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (platform-only)

CREATE TABLE user_platform_roles (                         -- platform axis, separate from tenant roles (ADR 0026 semantics)
  user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role        text NOT NULL CHECK (role IN ('platform_admin','support')),
  granted_by  uuid REFERENCES users(id),                   -- NULL = bootstrap by cmd/seed from PLATFORM_ADMIN_EMAILS
  granted_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, role)
);
ALTER TABLE user_platform_roles ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (platform-only)

-- Per-tenant capability overrides; replaces permissions_config/{role}. The catalog (81 keys = 77 tenant/scope keys
-- incl. mobile:create_hub + 4 platform keys, colon form) is Go code (R5, R73); the API validates `capability` against it.
CREATE TABLE role_capability_overrides (
  id          uuid PRIMARY KEY DEFAULT uuidv7(),
  tenant_id   uuid REFERENCES tenants(id),                 -- NULL = platform-wide default override
  role        text NOT NULL CHECK (role IN ('tenant_admin','manager','operation_staff','operator','user','driver')),
  capability  text NOT NULL CHECK (capability ~ '^[a-z]+:[a-z_]+$'),
  allowed     boolean NOT NULL,
  updated_by  uuid NOT NULL REFERENCES users(id),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT rco_not_grantable           CHECK (capability !~ '^(platform|dispatch):'),
  CONSTRAINT rco_assign_role_platform    CHECK (tenant_id IS NULL OR capability <> 'users:assign_role'),
  CONSTRAINT rco_key UNIQUE NULLS NOT DISTINCT (tenant_id, role, capability)
);
ALTER TABLE role_capability_overrides ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (platform-only)

-- statusHistory[] of drivers, trucks, subcontractors (tenants), truck assignments and users. Append-only.
CREATE TABLE status_history (
  id                  uuid PRIMARY KEY DEFAULT uuidv7(),    -- R57: uuidv7 like every table (time-ordered)
  entity_type         text NOT NULL CHECK (entity_type IN ('driver','truck','tenant','truck_assignment','user')),
  entity_id           uuid NOT NULL,
  status              text NOT NULL,                       -- free text: legacy truck entries carry values such as 'Tax Renewed'
  previous_status     text,
  changed_at          timestamptz NOT NULL,
  changed_by_user_id  uuid REFERENCES users(id),
  changed_by_label    text,                                -- trucks shape: displayName/email string; 'system'
  reason              text,
  legacy_raw          jsonb
);
CREATE INDEX status_history_entity ON status_history (entity_type, entity_id, changed_at);
CREATE TRIGGER status_history_immutable BEFORE UPDATE OR DELETE ON status_history
  FOR EACH ROW EXECUTE FUNCTION trg_forbid_mutation();
ALTER TABLE status_history ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (parent-scoped, polymorphic)

CREATE TRIGGER tenants_set_updated_at     BEFORE UPDATE ON tenants     FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER users_set_updated_at       BEFORE UPDATE ON users       FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER memberships_set_updated_at BEFORE UPDATE ON memberships FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER rco_set_updated_at         BEFORE UPDATE ON role_capability_overrides FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();

-- Column guard on tenants (Appendix C §C.3.6 describes it; body printed here): without bypass (a tenant_admin
-- through p_update_own) only profile columns change; these five are platform-only (platform:manage_tenants, WithSystem).
-- +goose StatementBegin
CREATE FUNCTION trg_tenant_admin_columns() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NOT app_bypass() AND (NEW.kind, NEW.status, NEW.code, NEW.legacy_doc_id, NEW.contractor_tenant_id)
       IS DISTINCT FROM (OLD.kind, OLD.status, OLD.code, OLD.legacy_doc_id, OLD.contractor_tenant_id) THEN
    RAISE EXCEPTION 'tenants: kind, status, code, legacy_doc_id and contractor_tenant_id are platform-only'
      USING ERRCODE = 'insufficient_privilege';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER t_tenant_admin_columns BEFORE UPDATE ON tenants FOR EACH ROW EXECUTE FUNCTION trg_tenant_admin_columns();

-- Appendix C block (same file): §C.3.5 "0002_identity" (generator DO block, policies of the 13 tables above,
-- app_status_entity_visible(text, uuid, boolean) + p_entity), then §C.3.6 trg_users_self_columns() + t_users_self_columns
-- and trg_file_objects_commit_columns() + t_file_objects_commit_columns.

-- +goose Down
-- Appendix C objects (IF EXISTS: runnable with or without them).
DROP POLICY IF EXISTS p_staff_read ON users;               -- reads memberships; would block its DROP
DROP TABLE status_history;
DROP TABLE role_capability_overrides;
DROP TABLE user_platform_roles;
DROP TABLE memberships;
DROP TABLE api_keys;
DROP TABLE password_reset_tokens;
DROP TABLE refresh_tokens;
DROP TABLE sessions;
DROP TABLE auth_identities;
DROP TABLE tenant_files;
ALTER TABLE users DROP CONSTRAINT users_photo_file_fk;
DROP TABLE file_objects;
DROP TABLE users;
DROP TABLE tenants;                                        -- removes the quarantine row with it
DROP FUNCTION trg_tenant_admin_columns();
DROP FUNCTION IF EXISTS app_status_entity_visible(text, uuid, boolean);
DROP FUNCTION IF EXISTS trg_users_self_columns();
DROP FUNCTION IF EXISTS trg_file_objects_commit_columns();
```

**Notes (0002)**

- Business rules encoded.
  - One own-fleet and one quarantine tenant per database (`tenants_one_own_fleet`, `tenants_one_quarantine`). The quarantine row is structural: this migration inserts it with the fixed id that `app_quarantine_tenant_id()` returns (R56) and seed/ETL never do. The own-fleet row is environment data from `cmd/seed` / `cmd/etl` (uuid from `OWN_FLEET_TENANT_ID`, R7; no request path reads it). A carrier tenant must carry its legal type (`subcontractors.type`).
  - Contractor reach (R56, R60): `contractor_tenant_id` exists only on carriers and never points at itself; ETL sets it to the own fleet for every legacy `subcontractors` doc. Reach is one level (`db.WithPrincipal` loads only direct sub-tenants into `app.subtenant_ids`, Appendix C §C.3.4). `kind`, `status`, `code`, `legacy_doc_id` and `contractor_tenant_id` change only through iam under `WithSystem` (`platform:manage_tenants`); trigger `t_tenant_admin_columns` rejects them on the tenant admin's own-row update (`p_update_own`, Appendix C §C.3.5).
  - Sessions (R83): `active_tenant_id` is re-issued as `tid` on refresh; `sessions_user_install_live` allows one live session per `(user, install_id)`, so a re-login on the same device first revokes the older one (`device_relogin`). `revoked_reason` follows Appendix C §C.4.4; a claims change (role, scope, driver link) bumps `users.auth_version` without revoking sessions (R50).
  - API keys (R82): `scope` is `integration`, `script`, `cf_shim` (the five callable shims, platform key, R45) or `release_publisher` (`cmd/release` on the private network, R43); capabilities are a non-empty catalog subset validated in Go.
  - Roles are rows, not a claims blob: a role change is one `memberships` row, with no "set claims" primitive (fixes claim overwrite without merge, `logitrack-web/functions/src/triggers.ts:121-125, 236-240`, and the stale `driverId` after a role change, `logitrack-web/functions/src/users.ts:78`). Legacy `partner` → `tenant_admin` of its tenant, legacy `admin` → own-fleet `tenant_admin`; only `PLATFORM_ADMIN_EMAILS` get `user_platform_roles(platform_admin)` (R6). `platform:*` and `dispatch:*` are not grantable through overrides; `users:assign_role` only platform-wide (Appendix C §C.2.5).
  - A user holds an Argon2id `password_hash`, a legacy scrypt pair awaiting the first-login rehash (all-or-nothing, NULLed by that login), or neither (Google-only).
  - Refresh rotation sets `rotated_at` and `replaced_by` together; presenting a rotated token is reuse → revoke family + session (`refresh_reuse`) and bump `auth_version` (R2), except inside the 30 s grace for concurrent tabs or a retried mobile request (R37, Appendix C §C.4.4).
  - Public objects exist only for APKs (`visibility='public'` requires `purpose='apk'`); the rest is private (presigned GET). Pending uploads always carry `expires_at` for `storage.gc`.
- `file_objects.purpose` vocabulary (format CHECK only; the presign API in Appendix B owns the list): `trip_photo`, `checkin_photo`, `checkin_app_screenshot`, `standby_photo`, `incident_photo`, `chat_image`, `leave_evidence`, `maintenance_file`, `expense_receipt`, `expense_odometer`, `company_logo`, `company_stamp`, `company_signature`, `customer_logo`, `driver_profile`, `driver_id_card`, `driver_license`, `truck_photo`, `truck_document`, `truck_receipt`, `insurance_document`, `tenant_document`, `user_photo`, `penalty_evidence`, `statement_document`, `report`, `apk`. Server-only cache objects (`cache/staticmaps/`) are not registered.
- Legacy Firestore source: `subcontractors` → `tenants` (`kind='carrier'`, `contractor_tenant_id` = own fleet) + `tenant_files`; Firebase Auth export + `users/{uid}` → `users`, `auth_identities`, `memberships`, `user_platform_roles`; `permissions_config/{role}` → `role_capability_overrides` (only keys that exist in the Go catalog; underscore matrix rows are mapped per Appendix C §C.2.6, the rest go to `etl.quarantine` with `unknown_capability_key`); every Firebase Storage URL → `file_objects` (key = URL path after `/o/`, token stripped); `drivers.statusHistory[]`, `trucks.statusHistory[]` (different shape) → `status_history`. `users.forceLogoutAt` has no column: forced logout is session revocation + `auth_version` bump + SSE `session.revoked`.
- Indexes → live query.
  - `users_last_login` serves the Security Center user list and active-session view sorted by last login (`logitrack-web/app/app/security-center/users/page.tsx:370`, `SessionManagementActiveUsers.tsx:147`) as a keyset page instead of the silent 50-row cap.
  - `users_legacy_uid` serves the Firebase ID-token verifier (uid → user, Redis `auth:fbuid:` on top): the five callable shims from P2 in every bridge mode (R45), `POST /v1/auth/exchange` and Firebase bearers while `AUTH_FIREBASE_BRIDGE_MODE` includes `mobile` (Appendix C §C.6.2), and every ETL driver/user join.
  - `sessions_user_active`: `GET /v1/me/sessions`, `GET /v1/users/{id}/sessions`, revoke-all; the `*_expiry` indexes: the `auth.token-cleanup` job (R2).
  - `refresh_tokens.token_hash` UNIQUE is the refresh lookup on a Redis `auth:rt:` miss; `refresh_tokens_family` serves family revocation on reuse.
  - `memberships_by_tenant_role` serves `GET /v1/tenants/{id}/members` and users-by-role counts (`logitrack-web/features/dashboard/components/DashboardStats.tsx:112-116`, `logitrack-web/lib/fetchSecurityOverviewStats.ts:32-38`).
  - `tenants_contractor`: the sub-tenant lookup of `db.WithPrincipal` (Redis-cached, Appendix C §C.3.4); `sessions_user_install_live`: the device re-login check; `api_keys_scope`: the API-keys page filter.
  - `file_objects_gc`: `storage.gc`; `file_objects_missing`: the ETL `file_missing_at_source` report; `file_objects_owner`: "all files of entity X" (evidence gallery, ZIP export).

### A.2.2 Master data (0003_master.sql)

```sql
-- 0003_master.sql
-- +goose Up

CREATE TABLE customers (                                   -- customers collection (global master)
  id                 uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id      text,
  code               citext NOT NULL,                      -- 'SPX', 'TTP', 'CJSF'; key of invoice numbers and driver customer codes
  name               text NOT NULL,
  description        text,
  logo_file_id       uuid REFERENCES file_objects(id),
  address            text,
  tax_id             text,
  branch_type        text CHECK (branch_type IN ('hq','branch')),   -- legacy 'สำนักงานใหญ่' -> hq, 'สาขา' -> branch
  branch_number      text,
  contact_name       text,
  contact_phone      text,
  billing_email      citext,
  payment_terms_days int  CHECK (payment_terms_days >= 0),          -- billing_statements.due_date
  invoice_note       text,
  line_group_id      text,
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX customers_code   ON customers (code);
CREATE UNIQUE INDEX customers_legacy ON customers (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
ALTER TABLE customers ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (global-read-steward-write)

CREATE TABLE customer_driver_id_types (                    -- customers.driverIdTypes[]
  customer_id uuid NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
  key         text NOT NULL,                               -- 'appId', 'workId', ...
  label       text NOT NULL,
  position    int  NOT NULL DEFAULT 0,
  PRIMARY KEY (customer_id, key)
);
ALTER TABLE customer_driver_id_types ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (global-read-steward-write)

-- Single FK target for everything billed to / linked with "a customer OR a carrier tenant" (A.1.3, R6).
CREATE TABLE billing_parties (
  id                 uuid PRIMARY KEY DEFAULT uuidv7(),
  kind               text NOT NULL CHECK (kind IN ('customer','tenant')),
  customer_id        uuid REFERENCES customers(id),
  tenant_id          uuid REFERENCES tenants(id),
  billing_date_basis text NOT NULL DEFAULT 'delivered' CHECK (billing_date_basis IN ('delivered','plan')),   -- ADR 0027
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  CHECK ((kind = 'customer' AND customer_id IS NOT NULL AND tenant_id IS NULL)
      OR (kind = 'tenant'   AND tenant_id   IS NOT NULL AND customer_id IS NULL))
);
CREATE UNIQUE INDEX billing_parties_customer ON billing_parties (customer_id) WHERE customer_id IS NOT NULL;
CREATE UNIQUE INDEX billing_parties_tenant   ON billing_parties (tenant_id)   WHERE tenant_id   IS NOT NULL;
-- billing_parties.tenant_id (kind 'tenant') is a reference to a billed carrier, not a tenancy stamp.
ALTER TABLE billing_parties ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (global-read-steward-write)

CREATE TABLE companies (                                   -- invoice issuer profiles (companies collection)
  id                   uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id        text,
  tenant_id            uuid NOT NULL REFERENCES tenants(id),
  tenant_source        text NOT NULL CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  name_th              text NOT NULL,
  name_en              text,
  short_name           text,                               -- e.g. WRT
  tax_id               text NOT NULL,
  branch_type          text NOT NULL CHECK (branch_type IN ('headquarters','branch')),
  branch_number        text,                               -- 5 digits when branch
  address              text NOT NULL,
  phone                text,
  email                citext,
  logo_file_id         uuid REFERENCES file_objects(id),
  stamp_file_id        uuid REFERENCES file_objects(id),
  signature_file_id    uuid REFERENCES file_objects(id),
  signatory_name       text,
  bank_name            text,
  account_number       text,
  account_name         text,
  withholding_tax_rate numeric(5,2) NOT NULL CHECK (withholding_tax_rate BETWEEN 0 AND 100),   -- percent; no default (main spec §19.2 question 6)
  company_type         text NOT NULL CHECK (company_type IN ('owner','subcontractor')),
  is_active            boolean NOT NULL DEFAULT true,
  max_trucks           int  CHECK (max_trucks > 0),
  max_drivers          int  CHECK (max_drivers > 0),
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX companies_legacy    ON companies (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
-- UNVERIFIED business rule (indexes report: "one owner company" is implied by /app/companies, not stated anywhere).
CREATE UNIQUE INDEX companies_owner_one ON companies (company_type) WHERE company_type = 'owner';
CREATE INDEX companies_type_name        ON companies (company_type, name_th);
CREATE INDEX companies_tenant           ON companies (tenant_id);
ALTER TABLE companies ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (tenant)

-- Customer scope and dispatcher axis of a user (R86: these two kinds only; the legacy companyId claim is never
-- written, so there is no company scope). Placed here (not in 0002) because it references billing_parties;
-- FKs are inline (R6: user_scopes references billing_parties.id).
CREATE TABLE user_scopes (
  id                uuid PRIMARY KEY DEFAULT uuidv7(),
  user_id           uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind              text NOT NULL CHECK (kind IN ('customer','dispatcher')),
  billing_party_id  uuid NOT NULL REFERENCES billing_parties(id),   -- value carried in JWT `cs`
  created_by        uuid REFERENCES users(id),
  created_at        timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT user_scopes_key UNIQUE (user_id, kind, billing_party_id)
);
CREATE INDEX user_scopes_party ON user_scopes (billing_party_id);
ALTER TABLE user_scopes ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (platform-only)

CREATE TABLE hubs (                                        -- hubs collection (pickup points, SOCs); global master
  id                 uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id      text,
  source_id          citext NOT NULL,                      -- business code (ALANG-A, SOCN, SPK890103 ...); the FK used everywhere
  name_th            text NOT NULL,                        -- source_name_th (required since 2026-06-13)
  name_en            text,                                 -- source_name_en (billing label)
  latitude           double precision,
  longitude          double precision,
  station_type       text NOT NULL DEFAULT 'HUB' CHECK (station_type IN ('HUB','SOC')),
  network            text NOT NULL CHECK (network IN ('SPX','SPK')),   -- hubDistanceNetworkGroup, computed at save
  linked_party_id    uuid REFERENCES billing_parties(id),  -- linkedCustomerId + customerLinkKind
  created_by_driver  boolean NOT NULL DEFAULT false,       -- mobile-created hubs (capability mobile:create_hub)
  created_by         uuid REFERENCES users(id),
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX hubs_source_id ON hubs (source_id);
CREATE UNIQUE INDEX hubs_legacy    ON hubs (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX hubs_station_type     ON hubs (station_type, network);
CREATE INDEX hubs_linked_party     ON hubs (linked_party_id) WHERE linked_party_id IS NOT NULL;
ALTER TABLE hubs ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (global-read-steward-write + driver hub insert)

-- nameToCode as data (buildHubMaps, logitrack-web/functions/src/tripBillingOnDelivered.ts:124-141).
-- Direction name -> code ONLY; codeToName is hubs.name_th. The two maps are never merged
-- (shared-docs/.vibe-rules.md Confirmed Patterns, as of commit 4f552099).
CREATE TABLE hub_name_aliases (
  alias      citext PRIMARY KEY,                           -- first writer wins (== `!nameToCode.has(alias)`)
  hub_id     uuid NOT NULL REFERENCES hubs(id) ON DELETE CASCADE,
  source     text NOT NULL CHECK (source IN ('name_th','name_en','legacy_hubName','manual')),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX hub_name_aliases_hub ON hub_name_aliases (hub_id);
ALTER TABLE hub_name_aliases ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (global-read-steward-write + driver alias insert)

CREATE TABLE hub_soc_distances (                           -- hub_soc_distances + soc_hub_distances in one table (R14)
  id              uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id   text,                                    -- `${hub}_${soc}` or `${soc}_${hub}`
  hub_id          uuid NOT NULL REFERENCES hubs(id),
  soc_key         text NOT NULL,                           -- normalizeSocIdToKey (SOCE/SOCN/SOCW or upper source_id)
  direction       text NOT NULL CHECK (direction IN ('hub_to_soc','soc_to_hub')),
  network         text CHECK (network IN ('SPX','SPK')),
  distance_m      int  NOT NULL CHECK (distance_m >= 0),
  distance_km     numeric(9,2) NOT NULL,
  duration_s      int  NOT NULL CHECK (duration_s >= 0),
  duration_min    numeric(9,2) NOT NULL,
  hub_lat         double precision NOT NULL,
  hub_lng         double precision NOT NULL,
  soc_lat         double precision NOT NULL,
  soc_lng         double precision NOT NULL,
  created_by      uuid REFERENCES users(id),
  updated_by      uuid REFERENCES users(id),
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (hub_id, soc_key, direction)
);
CREATE UNIQUE INDEX hub_soc_distances_legacy ON hub_soc_distances (legacy_doc_id, direction) WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX hub_soc_distances_soc           ON hub_soc_distances (soc_key, direction);
ALTER TABLE hub_soc_distances ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (global-read-steward-write)

CREATE TABLE drivers (
  id                     uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id          text,                             -- nullable: drivers created in Go have none (R12)
  legacy_auth_uid        text,                             -- drivers.authId / legacy authUid
  tenant_id              uuid NOT NULL REFERENCES tenants(id),
  tenant_source          text NOT NULL CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  user_id                uuid REFERENCES users(id),        -- login link; at most one driver per user
  first_name             text NOT NULL,
  last_name              text NOT NULL,
  full_name_th           text,                             -- whitespace-only legacy values load as NULL
  mobile                 text NOT NULL,
  email                  citext,
  profile_file_id        uuid REFERENCES file_objects(id),
  birth_date             date,
  id_card                text,                             -- 13 digits (PII; readable with drivers:view_pii only)
  id_card_expired_date   date,
  id_card_file_id        uuid REFERENCES file_objects(id),
  truck_license_id       text,
  license_type           text CHECK (license_type IN ('บ.1','บ.2','บ.3','บ.4','ท.1','ท.2','ท.3','ท.4')),
  license_expired_date   date,
  license_file_id        uuid REFERENCES file_objects(id),
  employment_type        text NOT NULL DEFAULT 'full_time' CHECK (employment_type IN ('full_time','subcontractor','part_time')),
  contract_years         numeric(5,2),
  employment_start       date,
  employment_end         date,
  hire_date              date,                             -- SSO base derivation (payroll)
  probation_passed       boolean NOT NULL DEFAULT false,
  status                 text NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive','on_duty')),
  assign_to_project      text,
  current_assignment_id  uuid,                             -- home truck (FK after truck_assignments)
  active_truck_id        uuid,                             -- activeTruck{}: truck responsible right now; set at check-in, cleared at trip end
  active_truck_plate     text,
  active_task_id         uuid,                             -- FK added in 0004 (tasks)
  active_started_at      timestamptz,
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX drivers_user       ON drivers (user_id)         WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX drivers_legacy     ON drivers (legacy_doc_id)   WHERE legacy_doc_id IS NOT NULL;
CREATE UNIQUE INDEX drivers_legacy_uid ON drivers (legacy_auth_uid) WHERE legacy_auth_uid IS NOT NULL;
CREATE INDEX drivers_tenant_status     ON drivers (tenant_id, status);
CREATE INDEX drivers_tenant_created    ON drivers (tenant_id, created_at DESC, id DESC);
CREATE INDEX drivers_active_truck      ON drivers (active_truck_id) WHERE active_truck_id IS NOT NULL;
ALTER TABLE drivers ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (tenant + driver self + scope read)

CREATE TABLE driver_customer_codes (                       -- drivers.customerDriverIds{customerCode:{idTypeKey:value}}
  driver_id    uuid NOT NULL REFERENCES drivers(id) ON DELETE CASCADE,
  customer_id  uuid NOT NULL REFERENCES customers(id),
  id_type_key  text NOT NULL,
  value        text NOT NULL,
  PRIMARY KEY (driver_id, customer_id, id_type_key)
);
ALTER TABLE driver_customer_codes ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (parent-scoped via drivers)

CREATE TABLE trucks (
  id                       uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id            text,
  tenant_id                uuid NOT NULL REFERENCES tenants(id),
  tenant_source            text NOT NULL CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  ownership_type           text NOT NULL DEFAULT 'own' CHECK (ownership_type IN ('own','subcontractor')),
  license_plate            text NOT NULL,
  province                 text NOT NULL,
  vin                      text,
  engine_number            text,
  gps_vehicle_id           text,                           -- Cartrack vehicle_id (GPSVehicleId)
  status                   text NOT NULL CHECK (status IN ('active','inactive','maintenance','insurance_claim','sold')),
  legacy_status            text,                           -- raw value when outside the enum ('Available', 'Active')
  brand                    text NOT NULL,
  model                    text NOT NULL,
  year                     smallint NOT NULL,
  color                    text NOT NULL,
  type_raw                 text NOT NULL,                  -- master full word, e.g. '6 Wheels'
  vehicle_class            text CHECK (vehicle_class IN ('4W','4WJ','6WH','10WH','18WH','VAN')),   -- logitrack-web/lib/truckType.ts map; NULL = unknown, never guessed
  seats                    smallint,
  fuel_type                text,
  engine_capacity          numeric(10,2),
  fuel_capacity            numeric(10,2),
  max_load_weight_kg       numeric(12,2),
  registration_date        date,
  buying_date              date,
  notes                    text,
  tax_expiry_date          date,
  tax_expense_thb          numeric(14,2),
  tax_responsible          text NOT NULL DEFAULT 'Operation Admin',
  tax_renewal_status       text CHECK (tax_renewal_status IN ('pending','in_progress','completed')),
  maintenance_responsible  text NOT NULL DEFAULT 'Driver',
  last_service_date        date,
  next_service_date        date,
  next_service_mileage     int,
  pm_interval_km           int  CHECK (pm_interval_km BETWEEN 1 AND 500000),
  current_mileage          int,
  last_alert_mileage       int,
  active_maintenance_id    uuid,                           -- FK trucks_active_maintenance_fk added in 0006 (maintenance_records)
  insurance_policy_id      text,
  insurance_policy_number  text,
  insurance_company        text,
  insurance_type           text CHECK (insurance_type IN ('1','2','2+','3','3+')),
  insurance_start_date     date,
  insurance_expiry_date    date,
  insurance_premium_thb    numeric(14,2),
  insurance_notes          text,
  insurance_renewal_status text CHECK (insurance_renewal_status IN ('pending','in_progress','completed')),
  payment_method           text CHECK (payment_method IN ('cash','transfer','company_credit')),
  created_by               uuid REFERENCES users(id),
  updated_by               uuid REFERENCES users(id),
  created_at               timestamptz NOT NULL DEFAULT now(),
  updated_at               timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX trucks_plate_per_tenant ON trucks (tenant_id, license_plate);   -- D4 (question 15): per tenant
CREATE UNIQUE INDEX trucks_legacy           ON trucks (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE UNIQUE INDEX trucks_gps              ON trucks (gps_vehicle_id) WHERE gps_vehicle_id IS NOT NULL AND gps_vehicle_id <> '';
CREATE INDEX trucks_tenant_status           ON trucks (tenant_id, ownership_type, status);
CREATE INDEX trucks_tenant_created          ON trucks (tenant_id, created_at DESC, id DESC);
CREATE INDEX trucks_updated                 ON trucks (updated_at DESC);
CREATE INDEX trucks_renewals                ON trucks (tax_expiry_date, insurance_expiry_date);
ALTER TABLE trucks ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (tenant + driver read + scope read)

CREATE TABLE truck_files (                                 -- image*, documentTax/Register, receipts, insuranceDocuments[], legacy images[]
  truck_id  uuid NOT NULL REFERENCES trucks(id) ON DELETE CASCADE,
  file_id   uuid NOT NULL REFERENCES file_objects(id),
  kind      text NOT NULL CHECK (kind IN ('image_front_right','image_front_left','image_back_right','image_back_left',
              'document_tax','document_register','tax_receipt','insurance_receipt','insurance_document','legacy_image','other')),
  position  int  NOT NULL DEFAULT 0,
  PRIMARY KEY (truck_id, file_id)
);
ALTER TABLE truck_files ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (parent-scoped via trucks)

CREATE TABLE truck_assignments (                           -- truckAssignment collection; trucks.currentAssignments[] is derived from status='active' (R14)
  id             uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id  text,
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  tenant_source  text NOT NULL CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  truck_id       uuid NOT NULL REFERENCES trucks(id),
  driver_id      uuid NOT NULL REFERENCES drivers(id),     -- legacy value is the drivers doc id
  status         text NOT NULL CHECK (status IN ('active','revoked','cancelled')),
  admin_user_id  uuid REFERENCES users(id),
  admin_label    text NOT NULL DEFAULT 'System',           -- legacy adminName
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  revoked_at     timestamptz
);
CREATE UNIQUE INDEX truck_assignments_legacy ON truck_assignments (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX truck_assignments_active  ON truck_assignments (tenant_id, created_at DESC, id DESC) WHERE status = 'active';
CREATE INDEX truck_assignments_tenant  ON truck_assignments (tenant_id, created_at DESC, id DESC);
CREATE INDEX truck_assignments_truck   ON truck_assignments (truck_id, created_at DESC);
CREATE INDEX truck_assignments_driver  ON truck_assignments (driver_id, created_at DESC);
ALTER TABLE truck_assignments ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (tenant + driver read)

ALTER TABLE drivers ADD CONSTRAINT drivers_current_assignment_fk
  FOREIGN KEY (current_assignment_id) REFERENCES truck_assignments(id) ON DELETE SET NULL;
ALTER TABLE drivers ADD CONSTRAINT drivers_active_truck_fk
  FOREIGN KEY (active_truck_id) REFERENCES trucks(id) ON DELETE SET NULL;

CREATE TRIGGER customers_set_updated_at         BEFORE UPDATE ON customers         FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER billing_parties_set_updated_at   BEFORE UPDATE ON billing_parties   FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER companies_set_updated_at         BEFORE UPDATE ON companies         FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER hubs_set_updated_at              BEFORE UPDATE ON hubs              FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER hub_soc_distances_set_updated_at BEFORE UPDATE ON hub_soc_distances FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER drivers_set_updated_at           BEFORE UPDATE ON drivers           FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER trucks_set_updated_at            BEFORE UPDATE ON trucks            FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER truck_assignments_set_updated_at BEFORE UPDATE ON truck_assignments FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();

-- Driver peer directory (Appendix C §C.3.7 describes it; body printed here): helper picker of a driver principal
-- (GET /v1/mobile/drivers). Drivers never read other drivers' rows; this returns four columns of the non-inactive
-- drivers of the caller's active tenant only. SECURITY DEFINER, handed to logitrack_rls_definer by 0009.
-- +goose StatementBegin
CREATE FUNCTION driver_directory() RETURNS TABLE (id uuid, full_name_th text, first_name text, last_name text)
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
  SELECT d.id, d.full_name_th, d.first_name, d.last_name
    FROM drivers d
   WHERE d.tenant_id = app_tenant_id() AND d.tenant_id <> app_quarantine_tenant_id() AND d.status <> 'inactive'
   ORDER BY d.full_name_th NULLS LAST, d.first_name, d.id
$$;
-- +goose StatementEnd

-- Appendix C block (same file): §C.3.3 app_task_stops_in_scope, app_task_in_scope, app_trip_in_scope,
-- app_recent_work_in_scope; §C.3.5 "0003_master" (DO block + policies of the 13 tables above); §C.3.6
-- trg_driver_self_columns() + t_driver_self_columns, trg_driver_link_membership() + t_driver_link_membership;
-- §C.3.7 scope_drivers, scope_trucks, scope_tenants.

-- +goose Down
-- Appendix C objects first where they block a table drop (IF EXISTS: runnable with or without them).
DROP VIEW IF EXISTS scope_tenants;                         -- reads tenants (0002)
DROP VIEW IF EXISTS scope_trucks;
DROP VIEW IF EXISTS scope_drivers;
CALL app_drop_definer_function('driver_directory()');
ALTER TABLE drivers DROP CONSTRAINT drivers_active_truck_fk;
ALTER TABLE drivers DROP CONSTRAINT drivers_current_assignment_fk;
DROP TABLE truck_assignments;
DROP TABLE truck_files;
DROP TABLE trucks;
DROP TABLE driver_customer_codes;
DROP TABLE drivers;
DROP TABLE hub_soc_distances;
DROP TABLE hub_name_aliases;
DROP TABLE hubs;
DROP TABLE user_scopes;
DROP TABLE companies;
DROP TABLE billing_parties;
DROP TABLE customer_driver_id_types;
DROP TABLE customers;
-- Functions after the tables whose policies and triggers use them.
DROP FUNCTION IF EXISTS trg_driver_self_columns();
CALL app_drop_definer_function('trg_driver_link_membership()');
CALL app_drop_definer_function('app_recent_work_in_scope(uuid,uuid)');
CALL app_drop_definer_function('app_trip_in_scope(uuid)');
CALL app_drop_definer_function('app_task_in_scope(uuid)');
CALL app_drop_definer_function('app_task_stops_in_scope(uuid)');
```

**Notes (0003)**

- Business rules encoded.
  - A billing party is exactly one of a customer or a carrier tenant, and each customer/tenant has at most one billing party; `billing_date_basis` (`delivered` default, `plan` for plan-basis customers such as CJSF, ADR 0027) lives only here.
  - Hub codes are unique and case-insensitive (`citext`); `network` is stored, not recomputed per read (rule `logitrack-web/validate/hubSchema.ts:36-49`: SPK prefix or linked customer code in SPK/J&T/JT → `SPK`, else `SPX`). `hub_name_aliases` materialises only name → code; code → name is `hubs.name_th`. Hub-distance pairs are unique per `(hub, soc_key, direction)`.
  - Truck plates are unique per tenant (D4: a broker carrier's plate may appear under two carriers); `trucks.id` is identity, the plate is a display string. `vehicle_class` is derived from `type_raw` through the `logitrack-web/lib/truckType.ts` map; unknown stays NULL (the MANDATORY "never guess a class" rule, R15). UNVERIFIED: whether legacy data contains duplicate plates inside one tenant (the web create path is check-then-add, `logitrack-web/app/app/trucks/new/action.client.ts:22,56`); if the ETL dry-run finds any, `trucks_plate_per_tenant` moves to `0010_d5_unique_constraints.sql` (§A.4 D5).
  - One user per driver and one driver per user (`drivers_user`). The membership invariant (a linked user holds `memberships(user, drivers.tenant_id, 'driver')`) is written by `PUT /v1/users/{id}/driver-link` in one transaction and checked at COMMIT by the deferred constraint trigger `t_driver_link_membership` (Appendix C §C.3.6), which is skipped while `app.etl_load` is on because ETL loads drivers and memberships in separate batches; `seed --verify` (Appendix D) asserts it after every load.
  - `drivers.active_*` is live state only (a trip keeps its own `truck_license_plate_snapshot`): set by `POST /v1/mobile/tasks/{id}/check-in`, cleared by delivery or standby completion.
  - `companies.withholding_tax_rate` is a percentage with no default: the legacy Zod default 3 contradicts the 1% the statements store (main spec §19.2 question 6); `billing_statements.withholding_tax_rate` snapshots `companies.withholding_tax_rate / 100` at statement creation (R18).
  - A user scope is a customer scope or the dispatcher axis over one `billing_parties.id` (R86); the legacy `companyId` claim is read but never written (`logitrack-web/firestore.rules:532`, `logitrack-web/hooks/useCompanyScope.ts:13-21`), so no company scope exists.
  - Drivers may insert the hubs and aliases they create (`mobile:create_hub`); other master writes are steward-only (R60).
- Legacy Firestore source: `customers` → `customers`, `customer_driver_id_types`, `billing_parties(kind='customer')`; `subcontractors.billingDateBasis` and every partner-kind link → `billing_parties(kind='tenant')`; `companies` (`companyType='owner'` → own-fleet tenant, `'subcontractor'` → tenant matched by tax id or name, else the quarantine tenant); custom claims `customerScopeId` → `user_scopes(kind='customer')`; `hubs` (incl. legacy `hubId`/`hubName`/`lat`/`lng` shape) → `hubs` + `hub_name_aliases`; `hub_soc_distances` and `soc_hub_distances` → `hub_soc_distances(direction)`; `metadata/distances_last_calculated` → `settings` (0008); `drivers` → `drivers`, `driver_customer_codes`; `trucks` → `trucks`, `truck_files`; `truckAssignment` → `truck_assignments`. `trucks.currentAssignments[]`, `drivers.currentTruckId`, `drivers.truckId` and denormalised plate/model strings stay only in `etl.source_docs`.
- Indexes → live query.
  - `customers_code` serves the customer list ordered by code (`logitrack-web/features/customers/api/customers.ts:34`) and invoice-number generation.
  - `companies_type_name` is the composite the live `/app/companies` query needs but never declared (`logitrack-web/features/companies/api/companies.ts:52-58`).
  - `hubs_source_id` serves every code resolution (`GET /v1/hubs`, `GET /v1/hubs/maps`, `logitrack-web/app/app/first-mile/hub-dialog.tsx:145-151`, mobile `logitrack-mobile/lib/features/home/data/repositories/hubs_repository.dart:220-223`); `hub_soc_distances_soc` and the `(hub_id, soc_key, direction)` key serve `HubDistancePanel.tsx:30` and the mobile STA lookup (`hub_soc_distances_repository.dart:50-80`).
  - `drivers_user` is the most frequent legacy lookup ("driver where authId == uid": `logitrack-web/functions/src/auth.ts:97-100`, `logitrack-web/functions/src/multiDeliveryTrips.ts:32-34`, mobile `driver_repository.dart:10-13`); in Go it resolves the `drv` claim at login only. `drivers_tenant_status` serves `GET /v1/drivers?status`, dashboard driver counts (`DashboardStats.tsx:112-126`) and `driver_directory()` (`GET /v1/mobile/drivers`, R33); `drivers_tenant_created` the keyset driver list.
  - `trucks_gps` serves the Cartrack sync every 3 minutes (`logitrack-web/functions/src/cartrack.ts:111-114`); `trucks_tenant_status` the compliance card and mobile truck picker (`ComplianceSummary.tsx:33`, mobile `trucks_repository.dart:53-54`); `trucks_renewals` `GET /v1/trucks/renewals?due=`; `trucks_plate_per_tenant` the duplicate check in `POST /v1/trucks` (409 `already_exists`).
  - `truck_assignments_*` serve `/app/truck-assignment` (today sorted in memory to avoid an index, `logitrack-web/app/app/truck-assignment/actions.client.ts:217-291`) and the driver's assignment history (mobile `driver_profile_repository.dart:34-37`).

### A.2.3 Operations (0004_operations.sql)

```sql
-- 0004_operations.sql
-- +goose Up

-- Task numbers FM|LH-ddMMyyyy-NNN, global across tenants (R10). Replaces count()+1
-- (logitrack-web/functions/src/triggers.ts:413-417, check_in_page.dart:2017-2022).
-- Reachable only through next_task_seq() (R67): no grant to logitrack_app.
CREATE TABLE task_number_counters (
  task_type  text NOT NULL CHECK (task_type IN ('first_mile','line_haul')),
  plan_date  date NOT NULL,                                -- Bangkok day of tasks.plan_at
  last_seq   int  NOT NULL DEFAULT 0 CHECK (last_seq >= 0),
  PRIMARY KEY (task_type, plan_date)
);
ALTER TABLE task_number_counters ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (platform-only)

-- Allocator (Appendix C §C.3.3, same definition), called inside the task insert transaction; 0009 hands it to
-- logitrack_rls_definer (BYPASSRLS, R66).
-- +goose StatementBegin
CREATE FUNCTION next_task_seq(p_task_type text, p_plan_date date) RETURNS int
  LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
  INSERT INTO task_number_counters AS c (task_type, plan_date, last_seq) VALUES (p_task_type, p_plan_date, 1)
  ON CONFLICT (task_type, plan_date) DO UPDATE SET last_seq = c.last_seq + 1
  RETURNING last_seq
$$;
-- +goose StatementEnd

CREATE TABLE tasks (
  id                          uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id               text,                        -- Firestore auto id (the human number is task_no)
  tenant_id                   uuid NOT NULL REFERENCES tenants(id),
  tenant_source               text NOT NULL CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  task_no                     text,                        -- FM-ddMMyyyy-NNN; legacy values not unique (web padded, mobile not)
  task_type                   text NOT NULL CHECK (task_type IN ('first_mile','line_haul')),
  job_category                text CHECK (job_category IN ('PRIMARY','SUPPLEMENTARY')),   -- NULL = legacy/unknown (ADR 0010), never defaulted
  status                      text NOT NULL CHECK (status IN ('pending','assigned','checked_in','in_transit','completed','cancelled')),
  plan_at                     timestamptz NOT NULL,        -- tasks.date: exact plan instant; plan-basis billing axis (ADR 0027)
  plan_date                   date GENERATED ALWAYS AS (bkk_date(plan_at)) STORED,
  plan_time                   text CHECK (plan_time ~ '^[0-9]{2}:[0-9]{2}$'),
  legacy_date_str             text,                        -- dateStr, ambiguous ddMMyyyy / YYYYMMDD; kept raw, never interpreted
  actual_pickup_at            timestamptz,                 -- ADR 0028 (operations only)
  source_hub_raw              text NOT NULL,               -- verbatim legacy value ('CODE', 'CODE - Name', 'soce')
  source_hub_id               uuid REFERENCES hubs(id),
  source_linked_party_id      uuid REFERENCES billing_parties(id),
  destination_raw             text NOT NULL,
  destination_hub_id          uuid REFERENCES hubs(id),
  destination_soc_key         text,
  destination_linked_party_id uuid REFERENCES billing_parties(id),
  billing_party_id            uuid REFERENCES billing_parties(id),   -- explicit billing customer (ADR 0027/0028)
  truck_id                    uuid REFERENCES trucks(id),
  truck_type                  text CHECK (truck_type IN ('4W','4WJ','6WH','10WH','18WH','VAN')),   -- NULL legal: unknown class -> unpriced no_vehicle_class (R15)
  legacy_truck_type           text,                        -- raw value outside the enum (PICKUP, 4WH, '6 Wheels')
  license_plate_snapshot      text,
  driver_id                   uuid REFERENCES drivers(id),
  legacy_driver_ref           text,                        -- raw legacy driverId (doc id, or auth uid on old rows)
  driver_ref_match            text CHECK (driver_ref_match IN ('doc_id','auth_uid','name','none')),
  helper_driver_id            uuid REFERENCES drivers(id), -- helperDriverIds[0]; ADR 0011 caps helpers at 1
  run_order                   int  CHECK (run_order >= 1), -- per-driver queue position (max + 1 inside the insert tx)
  check_in_at                 timestamptz,
  check_in_photo_file_id      uuid REFERENCES file_objects(id),
  check_in_app_screenshot_file_id uuid REFERENCES file_objects(id),   -- ADR 0019; copied into trip photos as checkin_app
  check_in_lat                double precision,
  check_in_lng                double precision,
  is_multi_delivery           boolean NOT NULL DEFAULT false,
  line_checkin_notified_at    timestamptz,                 -- LINE idempotency flag (notify.line)
  cancelled_at                timestamptz,
  cancel_reason               text,
  client_op_id                uuid,                        -- driver-created task (manual check-in): offline op id (R63)
  created_by                  uuid REFERENCES users(id),
  created_at                  timestamptz NOT NULL DEFAULT now(),
  updated_at                  timestamptz NOT NULL DEFAULT now(),
  CHECK (helper_driver_id IS NULL OR helper_driver_id IS DISTINCT FROM driver_id)
);
CREATE UNIQUE INDEX tasks_legacy          ON tasks (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE UNIQUE INDEX tasks_client_op       ON tasks (driver_id, client_op_id) WHERE client_op_id IS NOT NULL;
CREATE INDEX tasks_task_no                ON tasks (task_no) WHERE task_no IS NOT NULL;
CREATE INDEX tasks_tenant_type_plan       ON tasks (tenant_id, task_type, plan_date DESC, id DESC);
CREATE INDEX tasks_tenant_type_created    ON tasks (tenant_id, task_type, created_at DESC, id DESC);
CREATE INDEX tasks_plan_date              ON tasks (plan_date, task_type);
CREATE INDEX tasks_created                ON tasks (created_at DESC, id DESC);
CREATE INDEX tasks_driver_status          ON tasks (driver_id, status);
CREATE INDEX tasks_driver_run             ON tasks (driver_id, run_order DESC);
CREATE INDEX tasks_driver_plan            ON tasks (driver_id, plan_at);
CREATE INDEX tasks_helper_plan            ON tasks (helper_driver_id, plan_at) WHERE helper_driver_id IS NOT NULL;
CREATE INDEX tasks_active_status          ON tasks (status) WHERE status IN ('checked_in','in_transit');
CREATE INDEX tasks_billing_party          ON tasks (billing_party_id) WHERE billing_party_id IS NOT NULL;
CREATE INDEX tasks_source_party           ON tasks (source_linked_party_id) WHERE source_linked_party_id IS NOT NULL;
CREATE INDEX tasks_destination_party      ON tasks (destination_linked_party_id) WHERE destination_linked_party_id IS NOT NULL;
CREATE INDEX tasks_truck                  ON tasks (truck_id) WHERE truck_id IS NOT NULL;
ALTER TABLE tasks ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (tenant + driver + scope read)

ALTER TABLE drivers ADD CONSTRAINT drivers_active_task_fk
  FOREIGN KEY (active_task_id) REFERENCES tasks(id) ON DELETE SET NULL;

CREATE TABLE task_delivery_stops (                         -- tasks.deliveryStops[] (multi-drop plan)
  id                          uuid PRIMARY KEY DEFAULT uuidv7(),
  task_id                     uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  stop_index                  int  NOT NULL CHECK (stop_index >= 1),
  destination_raw             text NOT NULL,
  destination_hub_id          uuid REFERENCES hubs(id),
  destination_linked_party_id uuid REFERENCES billing_parties(id),
  status                      text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','delivered','failed')),
  delivered_at                timestamptz,
  delivered_lat               double precision,
  delivered_lng               double precision,
  estimated_rate_thb          numeric(14,2),
  drop_fee_thb                numeric(14,2),
  sequence                    int,                         -- CF-only legacy field
  added_at                    timestamptz,
  source_id                   text,
  is_custom                   boolean NOT NULL DEFAULT false,
  UNIQUE (task_id, stop_index),
  UNIQUE (task_id, destination_raw)                        -- taskSchema refine: unique destinations; 409 duplicate_destination
);
CREATE INDEX task_delivery_stops_party ON task_delivery_stops (destination_linked_party_id) WHERE destination_linked_party_id IS NOT NULL;
ALTER TABLE task_delivery_stops ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (parent-scoped via tasks)

CREATE TABLE trip_records (
  id                           uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id                text,                       -- original Firestore doc id (may differ from trip_no after a rename)
  tenant_id                    uuid NOT NULL REFERENCES tenants(id),
  tenant_source                text NOT NULL CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  trip_no                      text NOT NULL CHECK (trip_no <> '' AND trip_no !~ '/' AND trip_no NOT IN ('.','..')
                                                    AND trip_no !~ '^__.*__$' AND octet_length(trip_no) <= 1500),   -- core/tripDocId.ts:10-18
  status                       text NOT NULL CHECK (status IN ('in_transit','incident','delivered','standby','cancelled')),
  job_type                     text NOT NULL CHECK (job_type IN ('first_mile','line_haul')),
  job_category                 text CHECK (job_category IN ('PRIMARY','SUPPLEMENTARY')),   -- copied from the task (ADR 0010); NULL allowed
  task_id                      uuid REFERENCES tasks(id),
  legacy_task_ref              text,
  task_ref_match               text CHECK (task_ref_match IN ('doc_id','task_no','none')),
  driver_id                    uuid REFERENCES drivers(id),
  legacy_driver_ref            text,                       -- raw legacy driverId (auth uid from mobile)
  driver_ref_match             text CHECK (driver_ref_match IN ('doc_id','auth_uid','none')),
  origin_raw                   text,
  origin_hub_id                uuid REFERENCES hubs(id),   -- resolved through hub_name_aliases only; raw never overwritten
  destination_raw              text,
  destination_hub_id           uuid REFERENCES hubs(id),
  destination_soc_key          text,
  seal_code                    text,
  partner_code                 text,                       -- root partnerCode, else ocrData.partnerCode
  ocr_data                     jsonb,                      -- whole ocrData incl. keys missing from the web Zod schema
  truck_id                     uuid REFERENCES trucks(id),
  truck_license_plate_snapshot text,                       -- never rebuilt from drivers.active_* (lib/truckPlate.ts rule)
  truck_type_raw               text,
  vehicle_class                text CHECK (vehicle_class IN ('4W','4WJ','6WH','10WH','18WH','VAN')),
  distance_km                  numeric(9,2),
  parcel_count                 int,
  seal_time                    timestamptz,                -- 'dd-MM-yyyy HH:mm:ss' parsed as Asia/Bangkok
  total_weight_kg              numeric(12,2),
  loading_lat                  double precision,
  loading_lng                  double precision,
  std                          timestamptz,
  sta                          timestamptz,                -- seal_time + duration_minutes
  ata                          timestamptz,
  duration_minutes             numeric(9,2),
  delivered_at                 timestamptz,                -- deliveredTimestamp: delivered-basis billing axis
  delivered_lat                double precision,
  delivered_lng                double precision,
  delivered_via                text CHECK (delivered_via IN ('mobile','admin_web')),
  is_multi_delivery            boolean NOT NULL DEFAULT false,
  billing_party_id             uuid REFERENCES billing_parties(id),   -- stamped even when unpriced (tripBillingOnDelivered.ts:83-102)
  billing_date                 timestamptz,                -- ADR 0027 plan/delivered axis instant
  billing_axis_date            date GENERATED ALWAYS AS (COALESCE(bkk_date(billing_date), bkk_date(delivered_at))) STORED,
  needs_admin_review           boolean NOT NULL DEFAULT false,
  review_status                text CHECK (review_status IN ('pending_review')),
  review_reason                text,
  resubmitted_at               timestamptz,
  line_delivered_notified_at   timestamptz,                -- LINE idempotency flag
  evidence_token               text,                       -- public gallery key /evidence/{token}; non-expiring (R30)
  evidence_token_revoked_at    timestamptz,                -- POST /v1/trips/{id}/evidence/revoke (R47); next forced LINE send mints a new token
  created_at                   timestamptz NOT NULL DEFAULT now(),   -- legacy: device time at loading (Depart axis); ETL coalesces (R19)
  updated_at                   timestamptz NOT NULL DEFAULT now(),
  CHECK (evidence_token_revoked_at IS NULL OR evidence_token IS NOT NULL)
);
CREATE UNIQUE INDEX trip_records_trip_no        ON trip_records (trip_no);
CREATE UNIQUE INDEX trip_records_legacy         ON trip_records (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE UNIQUE INDEX trip_records_evidence       ON trip_records (evidence_token) WHERE evidence_token IS NOT NULL;
CREATE INDEX trip_records_delivered_driver      ON trip_records (driver_id, delivered_at) WHERE status = 'delivered';
CREATE INDEX trip_records_delivered             ON trip_records (delivered_at) WHERE status = 'delivered';
CREATE INDEX trip_records_party_status_del      ON trip_records (billing_party_id, status, delivered_at);
CREATE INDEX trip_records_party_billdate        ON trip_records (billing_party_id, billing_date) WHERE status = 'delivered';
CREATE INDEX trip_records_party_axis            ON trip_records (billing_party_id, billing_axis_date) WHERE status = 'delivered';
CREATE INDEX trip_records_pending_driver        ON trip_records (driver_id, updated_at DESC) WHERE status = 'in_transit';
CREATE INDEX trip_records_tenant_created        ON trip_records (tenant_id, created_at DESC, id DESC);
CREATE INDEX trip_records_created               ON trip_records (created_at DESC, id DESC);
CREATE INDEX trip_records_created_delivered     ON trip_records (created_at DESC, id DESC) WHERE status = 'delivered';
CREATE INDEX trip_records_driver_created        ON trip_records (driver_id, created_at DESC, id DESC);
CREATE INDEX trip_records_task                  ON trip_records (task_id) WHERE task_id IS NOT NULL;
CREATE INDEX trip_records_seal                  ON trip_records (seal_code) WHERE seal_code IS NOT NULL;
CREATE INDEX trip_records_truck_created         ON trip_records (truck_id, created_at DESC) WHERE truck_id IS NOT NULL;
ALTER TABLE trip_records ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (tenant + driver + scope read)

CREATE TABLE trip_no_history (                             -- renameTripRecord provenance (renamedFromTripId/At/By); append-only
  id           uuid PRIMARY KEY DEFAULT uuidv7(),          -- R57
  trip_id      uuid NOT NULL REFERENCES trip_records(id),
  old_trip_no  text NOT NULL,
  new_trip_no  text NOT NULL,
  renamed_by   uuid REFERENCES users(id),
  renamed_at   timestamptz NOT NULL DEFAULT now(),
  CHECK (old_trip_no <> new_trip_no)
);
CREATE INDEX trip_no_history_trip   ON trip_no_history (trip_id, renamed_at);
CREATE INDEX trip_no_history_old_no ON trip_no_history (old_trip_no);
CREATE TRIGGER trip_no_history_immutable BEFORE UPDATE OR DELETE ON trip_no_history
  FOR EACH ROW EXECUTE FUNCTION trg_forbid_mutation();
ALTER TABLE trip_no_history ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (parent-scoped via trip_records, staff read)

CREATE TABLE trip_delivery_stops (                         -- trip_records.deliveryStopsProgress[] (actual progress)
  id                 uuid PRIMARY KEY DEFAULT uuidv7(),
  trip_id            uuid NOT NULL REFERENCES trip_records(id) ON DELETE CASCADE,
  stop_index         int  NOT NULL CHECK (stop_index >= 1),
  destination_raw    text NOT NULL,
  destination_hub_id uuid REFERENCES hubs(id),
  source_id          text,
  status             text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','delivered','failed')),
  delivered_at       timestamptz,
  delivered_lat      double precision,
  delivered_lng      double precision,
  completed_seq      int,                                  -- completion order (legacy array order), not plan order
  UNIQUE (trip_id, stop_index)
);
ALTER TABLE trip_delivery_stops ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (parent-scoped via trip_records)

CREATE TABLE trip_photos (                                 -- flat photos[] (ADR 0018: the whole evidence set) + per-stop link
  id               uuid PRIMARY KEY DEFAULT uuidv7(),
  trip_id          uuid NOT NULL REFERENCES trip_records(id) ON DELETE CASCADE,
  stop_id          uuid REFERENCES trip_delivery_stops(id) ON DELETE SET NULL,
  photo_type       text NOT NULL CHECK (photo_type <> ''),   -- free text: unknown legacy types still load (R19)
  photo_type_known boolean GENERATED ALWAYS AS (
                     photo_type IN ('pre_close','closing','seal','runsheet','runsheet_extra_1','runsheet_extra_2','runsheet_extra_3',
                                    'pre_open','opening','empty_container','runsheet_received','checkin_app','truck_release','arrived')
                     OR photo_type ~ '^stop_[0-9]+_(arrived|pre_open|opening|empty_container|runsheet_received)$') STORED,
  file_id          uuid NOT NULL REFERENCES file_objects(id),
  geocode_lat      double precision,
  geocode_lng      double precision,
  geocode_address  text,
  geocoded_at      timestamptz,
  position         int  NOT NULL DEFAULT 0,                -- insertion/replace order
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (trip_id, photo_type)                             -- merge-by-type, last wins => UPSERT
);
CREATE INDEX trip_photos_unknown_type ON trip_photos (trip_id) WHERE NOT photo_type_known;
ALTER TABLE trip_photos ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (parent-scoped via trip_records)

CREATE TABLE standby_records (
  id                          uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id               text,
  tenant_id                   uuid NOT NULL REFERENCES tenants(id),
  tenant_source               text NOT NULL CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  driver_id                   uuid REFERENCES drivers(id),
  legacy_driver_ref           text,
  driver_ref_match            text CHECK (driver_ref_match IN ('doc_id','auth_uid','none')),
  task_id                     uuid REFERENCES tasks(id),
  trip_id                     uuid REFERENCES trip_records(id),
  customer_party_id           uuid REFERENCES billing_parties(id),   -- standby.customerId (pricing input)
  customer_resolved           boolean NOT NULL DEFAULT false,
  customer_resolved_from      text CHECK (customer_resolved_from IN ('task','origin_hub','manual')),
  job_category                text CHECK (job_category IN ('PRIMARY','SUPPLEMENTARY')),   -- label only
  start_location_raw          text,
  end_location_raw            text,
  started_at                  timestamptz,
  ended_at                    timestamptz,                 -- billing axis; NULL => unpriced no_ended_at (never derived)
  billing_axis_date           date GENERATED ALWAYS AS (bkk_date(COALESCE(ended_at, started_at, created_at))) STORED,
  duration_minutes            int,
  note                        text,
  status                      text NOT NULL DEFAULT 'completed' CHECK (status IN ('completed')),
  lat                         double precision,
  lng                         double precision,
  truck_id                    uuid REFERENCES trucks(id),
  truck_license_plate_snapshot text,
  backfilled_by_admin         boolean NOT NULL DEFAULT false,
  migrated_from_trip_no       text,                        -- migrate-standby-trips.js provenance
  -- inline billing snapshot (small; written only by the billing service)
  billing_estimate_thb        numeric(14,2),
  billing_party_id            uuid REFERENCES billing_parties(id),
  billing_rate_source         text CHECK (billing_rate_source IN ('standby_rate','service_fee')),
  billing_rate_entry_id       uuid,                        -- FK standby_records_rate_fk added in 0005
  billing_effective_from_date date,
  billing_unpriced_reason     text CHECK (billing_unpriced_reason IN ('no_customer','no_rate','no_ended_at')),
  billing_computed_at         timestamptz,
  line_notified_at            timestamptz,
  evidence_token              text,                        -- non-expiring (R30)
  evidence_token_revoked_at   timestamptz,                 -- POST /v1/standby/{id}/evidence/revoke (R47)
  client_op_id                uuid,                        -- offline op id (R63)
  created_at                  timestamptz NOT NULL DEFAULT now(),
  updated_at                  timestamptz NOT NULL DEFAULT now(),
  CHECK (billing_unpriced_reason IS NULL OR billing_estimate_thb IS NULL),
  CHECK (billing_rate_source IS DISTINCT FROM 'service_fee' OR billing_rate_entry_id IS NULL),
  CHECK (evidence_token_revoked_at IS NULL OR evidence_token IS NOT NULL)
);
CREATE UNIQUE INDEX standby_records_legacy   ON standby_records (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE UNIQUE INDEX standby_records_evidence ON standby_records (evidence_token) WHERE evidence_token IS NOT NULL;
CREATE UNIQUE INDEX standby_records_client_op ON standby_records (driver_id, client_op_id) WHERE client_op_id IS NOT NULL;
CREATE INDEX standby_records_ended           ON standby_records (ended_at) WHERE status = 'completed';
CREATE INDEX standby_records_created         ON standby_records (created_at DESC, id DESC);
CREATE INDEX standby_records_tenant_created  ON standby_records (tenant_id, created_at DESC, id DESC);
CREATE INDEX standby_records_driver          ON standby_records (driver_id, created_at DESC, id DESC);
CREATE INDEX standby_records_party_axis      ON standby_records (billing_party_id, billing_axis_date);
CREATE INDEX standby_records_customer_axis   ON standby_records (customer_party_id, billing_axis_date);
CREATE INDEX standby_records_task            ON standby_records (task_id) WHERE task_id IS NOT NULL;
CREATE INDEX standby_records_trip            ON standby_records (trip_id) WHERE trip_id IS NOT NULL;
ALTER TABLE standby_records ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (tenant + driver + scope read)

CREATE TABLE standby_photos (
  id           uuid PRIMARY KEY DEFAULT uuidv7(),
  standby_id   uuid NOT NULL REFERENCES standby_records(id) ON DELETE CASCADE,
  photo_type   text NOT NULL CHECK (photo_type IN ('customer_worksheet','site_photo')),
  file_id      uuid NOT NULL REFERENCES file_objects(id),
  overlay_text text,
  created_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (standby_id, photo_type)
);
ALTER TABLE standby_photos ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (parent-scoped via standby_records)

CREATE TABLE incident_reports (                            -- collection `incidentReport`
  id                           uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id                text,
  tenant_id                    uuid NOT NULL REFERENCES tenants(id),
  tenant_source                text NOT NULL CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  driver_id                    uuid REFERENCES drivers(id),
  legacy_driver_ref            text,
  driver_ref_match             text CHECK (driver_ref_match IN ('doc_id','auth_uid','none')),
  trip_id                      uuid REFERENCES trip_records(id),
  legacy_trip_ref              text,                       -- kept when the trip could not be resolved
  delay_cause                  text,                       -- i18n key incident_cause_*
  description                  text,
  lat                          double precision,
  lng                          double precision,
  truck_id                     uuid REFERENCES trucks(id),
  truck_license_plate_snapshot text,                       -- mobile truckLicensePlate or admin truckPlate
  map_photo_file_id            uuid REFERENCES file_objects(id),
  situation1_photo_file_id     uuid REFERENCES file_objects(id),
  situation2_photo_file_id     uuid REFERENCES file_objects(id),
  reported_by_kind             text NOT NULL DEFAULT 'driver' CHECK (reported_by_kind IN ('driver','admin')),
  reported_by_user_id          uuid REFERENCES users(id),
  client_op_id                 uuid,                       -- offline op id (R63)
  created_at                   timestamptz NOT NULL DEFAULT now(),
  updated_at                   timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX incident_reports_legacy ON incident_reports (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE UNIQUE INDEX incident_reports_client_op ON incident_reports (driver_id, client_op_id) WHERE client_op_id IS NOT NULL;
CREATE INDEX incident_reports_trip           ON incident_reports (trip_id) WHERE trip_id IS NOT NULL;
CREATE INDEX incident_reports_driver         ON incident_reports (driver_id, created_at DESC);
CREATE INDEX incident_reports_tenant_created ON incident_reports (tenant_id, created_at DESC, id DESC);
CREATE INDEX incident_reports_created        ON incident_reports (created_at DESC, id DESC);
ALTER TABLE incident_reports ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (tenant + driver + scope read)

CREATE TABLE vehicle_locations (                           -- latest Cartrack position per truck (sync every 3 min); latest only
  truck_id             uuid PRIMARY KEY REFERENCES trucks(id) ON DELETE CASCADE,
  tenant_id            uuid NOT NULL REFERENCES tenants(id),
  tenant_source        text NOT NULL CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  gps_vehicle_id       text NOT NULL,
  license_plate        text,
  lat                  double precision NOT NULL,
  lng                  double precision NOT NULL,
  speed_kmh            numeric(6,1),
  heading              numeric(5,1),
  engine_on            boolean,
  position_description text,
  driver_name_raw      text,
  last_gps_at          timestamptz,
  odometer_km          numeric(12,1),
  fuel_level           numeric(8,2),
  fuel_percentage      numeric(5,2),
  updated_at           timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX vehicle_locations_tenant ON vehicle_locations (tenant_id, updated_at DESC);
ALTER TABLE vehicle_locations ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (tenant + dispatcher read)

-- updated_at maintenance
CREATE TRIGGER tasks_set_updated_at             BEFORE UPDATE ON tasks             FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER trip_records_set_updated_at      BEFORE UPDATE ON trip_records      FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER trip_photos_set_updated_at       BEFORE UPDATE ON trip_photos       FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER standby_records_set_updated_at   BEFORE UPDATE ON standby_records   FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER incident_reports_set_updated_at  BEFORE UPDATE ON incident_reports  FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER vehicle_locations_set_updated_at BEFORE UPDATE ON vehicle_locations FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();

-- Tenant consistency (Appendix C §C.3.6): a child keeps its parent's tenant, following the resolver order of
-- tenantResolve.ts (trip <- task; standby <- task, else trip; incident <- trip). Deferred to COMMIT so a
-- re-home or an R13 cross-tenant reassignment can move parent and children in one transaction.
-- Functions re-read the current row because a deferred event's NEW may be stale by commit time.
-- +goose StatementBegin
CREATE FUNCTION assert_tenant_matches(child_table text, child_id uuid, child_tenant uuid,
                                      parent_table text, parent_id uuid, parent_tenant uuid) RETURNS void
LANGUAGE plpgsql AS $$
BEGIN
  IF parent_tenant IS NULL THEN
    RAISE EXCEPTION '% % references % % which is not visible to this session', child_table, child_id, parent_table, parent_id
      USING ERRCODE = 'insufficient_privilege';
  ELSIF parent_tenant <> child_tenant THEN
    RAISE EXCEPTION '% % has tenant % but its parent % % has tenant %',
      child_table, child_id, child_tenant, parent_table, parent_id, parent_tenant
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION trg_trip_tenant_check() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE c_tenant uuid; c_task uuid; p_tenant uuid;
BEGIN
  SELECT tenant_id, task_id INTO c_tenant, c_task FROM trip_records WHERE id = NEW.id;
  IF NOT FOUND OR c_task IS NULL THEN RETURN NULL; END IF;
  SELECT tenant_id INTO p_tenant FROM tasks WHERE id = c_task;
  PERFORM assert_tenant_matches('trip_records', NEW.id, c_tenant, 'tasks', c_task, p_tenant);
  RETURN NULL;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION trg_standby_tenant_check() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE c_tenant uuid; c_task uuid; c_trip uuid; p_tenant uuid;
BEGIN
  SELECT tenant_id, task_id, trip_id INTO c_tenant, c_task, c_trip FROM standby_records WHERE id = NEW.id;
  IF NOT FOUND THEN RETURN NULL; END IF;
  IF c_task IS NOT NULL THEN
    SELECT tenant_id INTO p_tenant FROM tasks WHERE id = c_task;
    PERFORM assert_tenant_matches('standby_records', NEW.id, c_tenant, 'tasks', c_task, p_tenant);
  ELSIF c_trip IS NOT NULL THEN
    SELECT tenant_id INTO p_tenant FROM trip_records WHERE id = c_trip;
    PERFORM assert_tenant_matches('standby_records', NEW.id, c_tenant, 'trip_records', c_trip, p_tenant);
  END IF;
  RETURN NULL;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION trg_incident_tenant_check() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE c_tenant uuid; c_trip uuid; p_tenant uuid;
BEGIN
  SELECT tenant_id, trip_id INTO c_tenant, c_trip FROM incident_reports WHERE id = NEW.id;
  IF NOT FOUND OR c_trip IS NULL THEN RETURN NULL; END IF;
  SELECT tenant_id INTO p_tenant FROM trip_records WHERE id = c_trip;
  PERFORM assert_tenant_matches('incident_reports', NEW.id, c_tenant, 'trip_records', c_trip, p_tenant);
  RETURN NULL;
END
$$;
-- +goose StatementEnd

-- A parent that changes tenant must take its children along in the same transaction.
-- +goose StatementBegin
CREATE FUNCTION trg_task_tenant_move_check() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE cur uuid; child uuid;
BEGIN
  SELECT tenant_id INTO cur FROM tasks WHERE id = NEW.id;
  IF NOT FOUND THEN RETURN NULL; END IF;
  SELECT id INTO child FROM trip_records WHERE task_id = NEW.id AND tenant_id <> cur LIMIT 1;
  IF FOUND THEN
    RAISE EXCEPTION 'task % is on tenant % but trip_records % is not', NEW.id, cur, child
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  SELECT id INTO child FROM standby_records WHERE task_id = NEW.id AND tenant_id <> cur LIMIT 1;
  IF FOUND THEN
    RAISE EXCEPTION 'task % is on tenant % but standby_records % is not', NEW.id, cur, child
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  RETURN NULL;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION trg_trip_tenant_move_check() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE cur uuid; child uuid;
BEGIN
  SELECT tenant_id INTO cur FROM trip_records WHERE id = NEW.id;
  IF NOT FOUND THEN RETURN NULL; END IF;
  SELECT id INTO child FROM incident_reports WHERE trip_id = NEW.id AND tenant_id <> cur LIMIT 1;
  IF FOUND THEN
    RAISE EXCEPTION 'trip % is on tenant % but incident_reports % is not', NEW.id, cur, child
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  SELECT id INTO child FROM standby_records WHERE trip_id = NEW.id AND task_id IS NULL AND tenant_id <> cur LIMIT 1;
  IF FOUND THEN
    RAISE EXCEPTION 'trip % is on tenant % but standby_records % is not', NEW.id, cur, child
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER trip_records_tenant_check AFTER INSERT OR UPDATE OF tenant_id, task_id ON trip_records
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION trg_trip_tenant_check();
CREATE CONSTRAINT TRIGGER standby_records_tenant_check AFTER INSERT OR UPDATE OF tenant_id, task_id, trip_id ON standby_records
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION trg_standby_tenant_check();
CREATE CONSTRAINT TRIGGER incident_reports_tenant_check AFTER INSERT OR UPDATE OF tenant_id, trip_id ON incident_reports
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION trg_incident_tenant_check();
CREATE CONSTRAINT TRIGGER tasks_tenant_move_check AFTER UPDATE OF tenant_id ON tasks
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION trg_task_tenant_move_check();
CREATE CONSTRAINT TRIGGER trip_records_tenant_move_check AFTER UPDATE OF tenant_id ON trip_records
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION trg_trip_tenant_move_check();

-- Appendix C block (same file): §C.3.5 "0004_operations" (DO block + policies of the 11 tables above); §C.3.7
-- scope_tasks, scope_trips, scope_standby, scope_incidents.

-- +goose Down
-- Appendix C views first: they read the tables below (IF EXISTS: runnable with or without them).
DROP VIEW IF EXISTS scope_incidents;
DROP VIEW IF EXISTS scope_standby;
DROP VIEW IF EXISTS scope_trips;
DROP VIEW IF EXISTS scope_tasks;
ALTER TABLE drivers DROP CONSTRAINT drivers_active_task_fk;
DROP TABLE vehicle_locations;
DROP TABLE incident_reports;
DROP TABLE standby_photos;
DROP TABLE standby_records;
DROP TABLE trip_photos;
DROP TABLE trip_delivery_stops;
DROP TABLE trip_no_history;
DROP TABLE trip_records;
DROP TABLE task_delivery_stops;
DROP TABLE tasks;
CALL app_drop_definer_function('next_task_seq(text,date)');
DROP TABLE task_number_counters;
DROP FUNCTION trg_trip_tenant_move_check();
DROP FUNCTION trg_task_tenant_move_check();
DROP FUNCTION trg_incident_tenant_check();
DROP FUNCTION trg_standby_tenant_check();
DROP FUNCTION trg_trip_tenant_check();
DROP FUNCTION assert_tenant_matches(text, uuid, uuid, text, uuid, uuid);
```

**Notes (0004)**

- Business rules encoded.
  - Statuses are canonical lower_snake (D2): task `Pending|Assigned|Checked in|In-Transit|Completed|Cancelled` → `pending|assigned|checked_in|in_transit|completed|cancelled`; trip `loading`/`departure` (stale shared-docs values, no live writer) are not in the CHECK. The compat projection and the v1 shim translate back (main spec §13.8).
  - `plan_date` and both `billing_axis_date` columns are `STORED` generated columns over `bkk_date` (R34); `trip_records.billing_axis_date` = Bangkok day of `billing_date`, else of `delivered_at` — the axis `fetchBillingTripRows` uses. Trips with neither stay out of every billing period; the billing port never falls back to `Date.now()` (R19, `no_billing_date`).
  - `tasks.truck_type` NULL is legal (R15): pricing returns `no_vehicle_class` instead of the legacy blank → `4WJ` default (`logitrack-web/lib/billingCompute.ts:147-148`); rate-card rows keep their stored folded class (0005).
  - One helper per task (ADR 0011), never the driver (ETL loads such a legacy helper as NULL with a field-quarantine entry). Check-in ownership is `driver_id = drv OR helper_driver_id = drv` (R28); the dead phone/plate fallback (`logitrack-web/functions/src/multiDeliveryTrips.ts:13-46`, compares `driverPhone` with a non-existent `phone` field) is not reproduced.
  - Task numbers come from `next_task_seq()` inside the task insert transaction, global per `(task_type, plan_date)` (R10); the counter is written only by that allocator (R66, R67) and ETL seeds it past the highest legacy suffix (§A.3.5). `task_no` stays non-unique because legacy numbers collide.
  - Durable offline idempotency (R63): driver-created `tasks`, `standby_records` and `incident_reports` store the request's `clientOpId` as `client_op_id`, unique per driver, so a replay after the Redis / `idempotency_keys` window cannot create a second row; admin-created rows leave it NULL.
  - `trip_no` is unique and obeys the Firestore doc-id rule (projectable during coexistence); renames append to `trip_no_history` and never move objects.
  - Photos merge by type (`UNIQUE (trip_id, photo_type)`, last wins); unknown types load with `photo_type_known = false` (R19). Stop destinations are unique per task (409 `duplicate_destination`); stop progress is unique per `(trip, stop_index)`, so `POST /v1/mobile/trips/{id}/stops/{index}/deliver` is idempotent per index.
  - Standby pricing columns stay inline (R61) and the standby repository returns `billing_*` only to the billing carrier's principals (Appendix C §C.3.4). `service_fee` prices carry no rate-entry FK; an unpriced standby carries exactly one reason (`no_customer|no_rate|no_ended_at`, R62) and no estimate; `ended_at` is never derived (legacy `endedAt = trip.updatedAt` loads as stored).
  - Evidence tokens are non-expiring and revocable (R30, R47): `/evidence/{token}` checks `evidence_token_revoked_at IS NULL`; the revoke routes set it and the next forced LINE send mints a new token.
  - Tenant consistency: constraint triggers enforce at commit that a trip is on its task's tenant, a standby record on its task's (else trip's) tenant and an incident on its trip's tenant; a parent changes tenant only if its children move in the same transaction (re-home, R13). A writer referencing a parent it cannot see under RLS gets `insufficient_privilege`. Rows without a parent link are not checked; `tenant_source='driver'` drift is reported by `tenancy.orphan-scan`.
  - `created_at` on trips, standby records and incidents is `NOT NULL DEFAULT now()`; ETL supplies the legacy value and, for trips, `COALESCE(createdAt, std, deliveredTimestamp, Firestore create time)` with the field-quarantine entry `created_at_derived` (R19, R68), so a billable trip is never dropped.
- Legacy Firestore source: `tasks` (+ `deliveryStops[]`) → `tasks`, `task_delivery_stops`; `trip_records` (+ `photos[]`, `deliveryStopsProgress[]`, `renamedFrom*`) → `trip_records`, `trip_photos`, `trip_delivery_stops`, `trip_no_history`; the `billing*` fields of a trip go to `trip_billing_snapshots` (0005), except `billingCustomerId` / `billingDate` which stay on `trip_records` as `billing_party_id` / `billing_date`; `standby_records` (+ `photos[]`) → `standby_records`, `standby_photos`; `incidentReport` → `incident_reports`; `vehicle_locations/{GPSVehicleId}` → `vehicle_locations` keyed by truck. The `checkin` collection (rules only, no reader or writer) gets no table.
- Indexes → live query.
  - `tasks_tenant_type_created` / `tasks_tenant_type_plan`: the First Mile, Line Haul and Job Assign boards without / with a date (I15 / I16: `logitrack-web/app/app/first-mile/page.tsx:171-182`, `line-haul/page.tsx:171-182`, `job-assign/page.tsx:179-186`) as `GET /v1/tasks` + `['tasks',{date,type}]`; `tasks_plan_date` serves the dispatcher's cross-tenant day list and day counts (`logitrack-web/features/tasks/services/taskService.ts:77-83`); `tasks_created` the unfiltered job-assign list.
  - `tasks_driver_status`: the driver check-in / loading gate (I17, mobile `task_repository.dart:166-183`, `loading_phase_page.dart:189-194`) and `GET /v1/mobile/tasks`; `tasks_driver_run`: `run_order = max + 1` (undeclared composite behind `logitrack-web/functions/src/tasks.ts:123-128`); `tasks_driver_plan` + `tasks_helper_plan`: payroll own-task and helper-day pay (`logitrack-web/functions/src/driverCompensation.ts:199-216`, I3); `tasks_active_status`: active-driver badges (`taskService.ts:50-55`).
  - `tasks_billing_party`, `tasks_source_party`, `tasks_destination_party`, `task_delivery_stops_party`: the customer/dispatcher scope predicates (`useDriverMonitor.ts:217` and `first-mile/page.tsx:262-266` definitions, now enforced by RLS instead of the browser).
  - `trip_records_tenant_created` (+ `trip_records_created` for dispatcher/platform principals): `GET /v1/trips/monitor` keyset pages of 200 (W8), replacing the unbounded listener and the export loop (`logitrack-web/features/drivers/hooks/useDriverMonitor.ts:327-343, 443-467`).
  - `trip_records_delivered_driver` (I1, payroll `driverCompensation.ts:136-141`); `trip_records_delivered` (I21: all-customer billing rows, Shopee report, backfill, the 15-minute safety-net `tripBillingOnDelivered.ts:1240-1243`); `trip_records_party_status_del` (I22, `logitrack-web/features/accounting/api/billing.ts:816-823, 1122-1129`); `trip_records_party_billdate` (I23, plan-basis); `trip_records_party_axis`: `GET /v1/billing/rows?customerId&year&month` and statement assembly in one index range per period.
  - `trip_records_created_delivered`: the Income page (I20, `income/page.tsx:336-342`) as an infinite query without the 500-row silent cap; `trip_records_pending_driver`: resume pending trip (I18, mobile `trip_records_repository.dart:195-201`); `trip_records_driver_created`: `GET /v1/mobile/trips` history (replaces the in-memory take-300, `trip_records_repository.dart:85-97`); `trip_records_seal`: duplicate-seal check (409 `duplicate_seal`, `trip_records_repository.dart:59-62`); `trip_records_evidence` / `standby_records_evidence`: `/evidence/{token}` (`logitrack-web/functions/src/tripEvidence.ts:135,148`); `trip_records_truck_created`: the plate filter of Driver Monitor / Billing Document (ADR 0005).
  - `trip_no_history_old_no`: incident and evidence lookups by a pre-rename trip number.
  - `standby_records_ended` (I34: billing rows, diagnostics, the 15-minute standby safety-net `logitrack-web/functions/src/standbyBilling.ts:283-286`); `standby_records_tenant_created` / `standby_records_created` (I33 + the standby page listener, `billing.ts:1476-1482`, `income/page.tsx:401-407`); `standby_records_driver` (mobile history `trip_history_page.dart:84-87`); `standby_records_party_axis` / `standby_records_customer_axis`: `GET /v1/billing/standby-diagnostics` and billing rows per period.
  - `incident_reports_trip` (`tripEvidence.ts:139`, `lineNotify.ts:411`, Edit Trip dialog); `incident_reports_tenant_created` (`incident-reports/page.tsx:101-104`, dashboard counts).
  - `vehicle_locations_tenant`: `GET /v1/vehicle-locations` for the live map (`fleet:view_live_map`) and the 3-minute realtime batch.

### A.2.4 Billing (0005_billing.sql)

```sql
-- 0005_billing.sql
-- +goose Up

-- Route rate cards: announcement rows, IMMUTABLE, void only (ADR 0009 §1; firestore.rules:142-154).
CREATE TABLE customer_rate_entries (
  id                   uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id        text,
  tenant_id            uuid NOT NULL REFERENCES tenants(id),   -- billing carrier (own fleet today)
  tenant_source        text NOT NULL DEFAULT 'form' CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  billing_party_id     uuid NOT NULL REFERENCES billing_parties(id),
  import_id            text NOT NULL,                        -- 'rc_{ms}' | 'manual_{ms}'
  hub_code             text NOT NULL CHECK (hub_code = upper(hub_code)),   -- extractHubId result
  raw_hub_name         text,
  destination_code     text NOT NULL,                        -- normalizeDestinationCode result as stored (legacy SPK dash collapse kept)
  vehicle_class        text NOT NULL,                        -- normalizeVehicleClass result as stored (legacy '4WJ' defaults kept)
  rate_thb             numeric(14,2) NOT NULL CHECK (rate_thb >= 0),
  distance_km          numeric(9,2),
  job_category         text NOT NULL DEFAULT 'PRIMARY' CHECK (job_category IN ('PRIMARY','SUPPLEMENTARY')),
  effective_from_date  date NOT NULL,                        -- selection key (Bangkok day)
  effective_from_at    timestamptz NOT NULL,                 -- legacy instant; within-day tie sort only
  imported_at          timestamptz,
  created_by           uuid REFERENCES users(id),
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  voided               boolean NOT NULL DEFAULT false,
  voided_at            timestamptz,
  voided_by            uuid REFERENCES users(id),            -- required for new voids by the trigger; may be NULL on legacy voided rows
  voided_reason        text,
  CHECK (NOT voided OR voided_at IS NOT NULL)
);
CREATE UNIQUE INDEX customer_rate_entries_legacy ON customer_rate_entries (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX customer_rate_entries_lookup ON customer_rate_entries
  (billing_party_id, hub_code, destination_code, vehicle_class, job_category, effective_from_date DESC, effective_from_at DESC)
  WHERE NOT voided;
CREATE INDEX customer_rate_entries_party  ON customer_rate_entries (billing_party_id, effective_from_at DESC);
CREATE INDEX customer_rate_entries_import ON customer_rate_entries (billing_party_id, import_id);
CREATE TRIGGER customer_rate_entries_set_updated_at BEFORE UPDATE ON customer_rate_entries
  FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER customer_rate_entries_void_only BEFORE UPDATE ON customer_rate_entries
  FOR EACH ROW EXECUTE FUNCTION trg_announcement_void_only();
CREATE TRIGGER customer_rate_entries_no_delete BEFORE DELETE ON customer_rate_entries
  FOR EACH ROW EXECUTE FUNCTION trg_forbid_mutation();
ALTER TABLE customer_rate_entries ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (tenant, billing carrier)

CREATE TABLE customer_fuel_rate_adjustments (              -- announcement rows; customer-level, no fallback; skipped for SUPPLEMENTARY
  id                       uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id            text,
  tenant_id                uuid NOT NULL REFERENCES tenants(id),
  tenant_source            text NOT NULL DEFAULT 'form' CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  billing_party_id         uuid NOT NULL REFERENCES billing_parties(id),
  effective_from_date      date NOT NULL,
  effective_from_at        timestamptz NOT NULL,
  rate_multiplier          numeric(10,6) NOT NULL CHECK (rate_multiplier > 0),
  add_thb_per_trip         numeric(14,2) NOT NULL DEFAULT 0, -- signed: discounts allowed
  reference_fuel_price_thb numeric(8,2),                     -- referenceFuelPriceThbPerLitre
  announcement_note        text NOT NULL DEFAULT '',
  fuel_band_enabled        boolean NOT NULL DEFAULT false,
  fuel_band_baseline_floor int,
  fuel_band_thb_per_baht   numeric(10,2),
  created_by               uuid REFERENCES users(id),
  created_at               timestamptz NOT NULL DEFAULT now(),
  updated_at               timestamptz NOT NULL DEFAULT now(),
  voided                   boolean NOT NULL DEFAULT false,
  voided_at                timestamptz,
  voided_by                uuid REFERENCES users(id),
  voided_reason            text,
  CHECK (NOT voided OR voided_at IS NOT NULL),
  CHECK (NOT fuel_band_enabled OR (fuel_band_baseline_floor IS NOT NULL AND fuel_band_thb_per_baht IS NOT NULL))
);
CREATE UNIQUE INDEX customer_fuel_adj_legacy ON customer_fuel_rate_adjustments (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX customer_fuel_adj_lookup ON customer_fuel_rate_adjustments
  (billing_party_id, effective_from_date DESC, effective_from_at DESC) WHERE NOT voided;
CREATE INDEX customer_fuel_adj_party  ON customer_fuel_rate_adjustments (billing_party_id, effective_from_at DESC);
CREATE TRIGGER customer_fuel_adj_set_updated_at BEFORE UPDATE ON customer_fuel_rate_adjustments
  FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER customer_fuel_adj_void_only BEFORE UPDATE ON customer_fuel_rate_adjustments
  FOR EACH ROW EXECUTE FUNCTION trg_announcement_void_only();
CREATE TRIGGER customer_fuel_adj_no_delete BEFORE DELETE ON customer_fuel_rate_adjustments
  FOR EACH ROW EXECUTE FUNCTION trg_forbid_mutation();
ALTER TABLE customer_fuel_rate_adjustments ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (tenant, billing carrier)

CREATE TABLE customer_service_fees (                       -- extra stop, waiting time, standby fallback, ... (mutable, deletable: firestore.rules:172-179)
  id               uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id    text,
  tenant_id        uuid NOT NULL REFERENCES tenants(id),
  tenant_source    text NOT NULL DEFAULT 'form' CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  billing_party_id uuid NOT NULL REFERENCES billing_parties(id),
  fee_type         text NOT NULL CHECK (fee_type IN ('extra_stop','waiting_time','special_handling','service_charge','standby','custom')),
  custom_type_name text,
  amount_thb       numeric(14,2) NOT NULL CHECK (amount_thb >= 0),
  unit             text NOT NULL CHECK (unit IN ('per_trip','per_stop')),
  note             text,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  CHECK (fee_type <> 'custom' OR custom_type_name IS NOT NULL)
);
CREATE UNIQUE INDEX customer_service_fees_legacy ON customer_service_fees (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
-- Non-unique until 0010_d5_unique_constraints.sql (§A.4 D5) adds
--   CREATE UNIQUE INDEX customer_service_fees_one_per_type ON customer_service_fees (billing_party_id, fee_type) WHERE fee_type <> 'custom';
CREATE INDEX customer_service_fees_party_type ON customer_service_fees (billing_party_id, fee_type);
CREATE INDEX customer_service_fees_party      ON customer_service_fees (billing_party_id, created_at DESC);
CREATE TRIGGER customer_service_fees_set_updated_at BEFORE UPDATE ON customer_service_fees
  FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
ALTER TABLE customer_service_fees ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (tenant, billing carrier)

CREATE TABLE standby_rate_entries (                        -- fixed rate per standby event; mutable, soft delete only (R20)
  id                  uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id       text,
  tenant_id           uuid NOT NULL REFERENCES tenants(id),
  tenant_source       text NOT NULL DEFAULT 'form' CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  billing_party_id    uuid NOT NULL REFERENCES billing_parties(id),
  rate_thb            numeric(14,2) NOT NULL CHECK (rate_thb >= 0),
  effective_from_date date NOT NULL,
  effective_from_at   timestamptz NOT NULL,                  -- legacy: browser-local midnight
  note                text,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  voided_at           timestamptz,
  voided_by           uuid REFERENCES users(id),
  voided_reason       text,
  CHECK ((voided_at IS NULL) = (voided_by IS NULL))
);
CREATE UNIQUE INDEX standby_rate_entries_legacy ON standby_rate_entries (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX standby_rate_entries_lookup ON standby_rate_entries
  (billing_party_id, effective_from_date DESC, effective_from_at DESC) WHERE voided_at IS NULL;
CREATE TRIGGER standby_rate_entries_set_updated_at BEFORE UPDATE ON standby_rate_entries
  FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER standby_rate_entries_no_delete BEFORE DELETE ON standby_rate_entries
  FOR EACH ROW EXECUTE FUNCTION trg_forbid_mutation();
ALTER TABLE standby_rate_entries ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (tenant, billing carrier)

ALTER TABLE standby_records ADD CONSTRAINT standby_records_rate_fk
  FOREIGN KEY (billing_rate_entry_id) REFERENCES standby_rate_entries(id);

-- 1:1 price snapshot of a trip. Separate from trip_records because it is carrier-internal (dispatcher and
-- the executing carrier never read it), written only by the billing service, deleted as a whole on reset
-- (reset-multidrop-billing.js semantics) and gated by period lock + freeze. The axis columns
-- (billing_party_id, billing_date) stay on trip_records.
CREATE TABLE trip_billing_snapshots (
  trip_id                   uuid PRIMARY KEY REFERENCES trip_records(id) ON DELETE CASCADE,
  tenant_id                 uuid NOT NULL REFERENCES tenants(id),   -- billing carrier that priced the trip (may differ from trip_records.tenant_id)
  tenant_source             text NOT NULL DEFAULT 'form' CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  estimate_thb              numeric(14,2),                 -- NULL = stamped but unpriced
  unpriced_reason           text CHECK (unpriced_reason IN ('no_customer','no_rate','no_vehicle_class','no_billing_date')),
  base_rate_thb             numeric(14,2),                 -- single: raw card price; multi-drop: fuel-adjusted base stop
  stop_charge_thb           numeric(14,2),                 -- multi-drop surcharge (stops 2+ flat fee, or legacy per-route)
  rate_entry_id             uuid REFERENCES customer_rate_entries(id),
  rate_import_id            text,                          -- legacy billingRateImportId / 'manual'
  lookup_hub_code           text,
  lookup_destination_code   text,
  fuel_adjustment_id        uuid REFERENCES customer_fuel_rate_adjustments(id),
  rate_multiplier           numeric(10,6) NOT NULL DEFAULT 1,
  add_thb_per_trip          numeric(14,2) NOT NULL DEFAULT 0,
  fuel_effective_from_date  date,                          -- billingEffectiveFromDateStr
  round_effective_from_date date,                          -- billingRoundEffectiveFromDateStr (ADR 0009)
  fuel_band_lower_thb       numeric(8,2),
  fuel_band_upper_thb       numeric(8,2),
  reference_fuel_price_thb  numeric(8,2),
  is_multi_delivery         boolean NOT NULL DEFAULT false,
  manual_override           boolean NOT NULL DEFAULT false, -- EditBillingDialog / PUT /v1/trips/{id}/billing/manual
  job_category_at_pricing   text CHECK (job_category_at_pricing IN ('PRIMARY','SUPPLEMENTARY')),
  computed_by               text NOT NULL CHECK (computed_by IN ('mobile_delivery','scheduler','admin_force','set_job_category','manual_edit','etl')),
  compute_version           int  NOT NULL DEFAULT 1,       -- 0 = loaded by ETL from the legacy snapshot
  last_event_id             bigint,                        -- outbox_events.id high-water mark; older events are dropped (R17)
  computed_at               timestamptz NOT NULL DEFAULT now(),
  updated_at                timestamptz NOT NULL DEFAULT now(),
  CHECK (unpriced_reason IS NULL OR estimate_thb IS NULL)
);
CREATE INDEX trip_billing_snapshots_rate     ON trip_billing_snapshots (rate_entry_id) WHERE rate_entry_id IS NOT NULL;
CREATE INDEX trip_billing_snapshots_fuel     ON trip_billing_snapshots (fuel_adjustment_id) WHERE fuel_adjustment_id IS NOT NULL;
CREATE INDEX trip_billing_snapshots_unpriced ON trip_billing_snapshots (tenant_id, unpriced_reason) WHERE estimate_thb IS NULL;
CREATE INDEX trip_billing_snapshots_manual   ON trip_billing_snapshots (tenant_id) WHERE manual_override;
CREATE TRIGGER trip_billing_snapshots_set_updated_at BEFORE UPDATE ON trip_billing_snapshots
  FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
ALTER TABLE trip_billing_snapshots ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (tenant, billing carrier)

CREATE TABLE trip_billing_stop_breakdown (                 -- billingMultiDeliveryBreakdown[]
  trip_id          uuid NOT NULL REFERENCES trip_billing_snapshots(trip_id) ON DELETE CASCADE,
  stop_index       int  NOT NULL CHECK (stop_index >= 1),
  destination_code text NOT NULL,
  base_rate_thb    numeric(14,2) NOT NULL,
  final_rate_thb   numeric(14,2) NOT NULL,
  PRIMARY KEY (trip_id, stop_index)
);
ALTER TABLE trip_billing_stop_breakdown ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (parent-scoped via trip_billing_snapshots)

CREATE TABLE billing_counters (                            -- billing_counters/{customerId}_{YYYYMM}; written only by next_invoice_seq()
  billing_party_id uuid NOT NULL REFERENCES billing_parties(id),
  period_year      int  NOT NULL CHECK (period_year BETWEEN 2000 AND 2999),
  period_month     int  NOT NULL CHECK (period_month BETWEEN 1 AND 12),
  tenant_id        uuid NOT NULL REFERENCES tenants(id),   -- billing carrier
  tenant_source    text NOT NULL DEFAULT 'form' CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  last_seq         int  NOT NULL DEFAULT 0 CHECK (last_seq >= 0),
  customer_code    text NOT NULL,                          -- prefix of the invoice number
  updated_at       timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (billing_party_id, period_year, period_month)
);
CREATE TRIGGER billing_counters_set_updated_at BEFORE UPDATE ON billing_counters
  FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
ALTER TABLE billing_counters ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (platform-only)

-- Allocator (Appendix C §C.3.3, same definition), called inside the statement insert transaction (no gap). The
-- counter carries the billing carrier (R61): another carrier's counter returns NULL and the transaction fails.
-- +goose StatementBegin
CREATE FUNCTION next_invoice_seq(p_tenant uuid, p_party uuid, p_year int, p_month int, p_code text) RETURNS int
  LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
  INSERT INTO billing_counters AS c (billing_party_id, period_year, period_month, tenant_id, tenant_source,
                                     last_seq, customer_code)
  VALUES (p_party, p_year, p_month, p_tenant, 'form', 1, p_code)
  ON CONFLICT (billing_party_id, period_year, period_month)
    DO UPDATE SET last_seq = c.last_seq + 1 WHERE c.tenant_id = p_tenant
  RETURNING last_seq
$$;
-- +goose StatementEnd

CREATE TABLE billing_statements (
  id                     uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id          text,
  tenant_id              uuid NOT NULL REFERENCES tenants(id),   -- billing carrier
  tenant_source          text NOT NULL DEFAULT 'form' CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  billing_party_id       uuid NOT NULL REFERENCES billing_parties(id),
  issuer_company_id      uuid REFERENCES companies(id),    -- invoice issuer profile used for the documents
  invoice_number         text NOT NULL,                    -- {CODE}-{YYYYMM}-{SEQ:03}; legacy invoiceNumber ?? doc id
  customer_name_snapshot text,
  customer_code_snapshot text,
  period_year            int  NOT NULL CHECK (period_year BETWEEN 2000 AND 2999),
  period_month           int  NOT NULL CHECK (period_month BETWEEN 1 AND 12),
  total_amount           numeric(14,2) NOT NULL,
  withholding_tax_rate   numeric(5,4) NOT NULL CHECK (withholding_tax_rate BETWEEN 0 AND 1),   -- fraction; snapshot (R18); legacy 0.0100
  withholding_tax        numeric(14,2) NOT NULL,
  net_amount             numeric(14,2) NOT NULL,
  trip_count             int  NOT NULL DEFAULT 0,
  trip_only_count        int  NOT NULL DEFAULT 0,
  trip_subtotal          numeric(14,2) NOT NULL DEFAULT 0,
  standby_count          int  NOT NULL DEFAULT 0,
  standby_subtotal       numeric(14,2) NOT NULL DEFAULT 0,
  multi_drop_count       int  NOT NULL DEFAULT 0,
  multi_drop_subtotal    numeric(14,2) NOT NULL DEFAULT 0,
  status                 text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','sent','paid','cancelled')),
  generated_at           timestamptz NOT NULL,
  sent_at                timestamptz,
  paid_at                timestamptz,
  cancelled_at           timestamptz,
  due_date               timestamptz,                      -- generated_at + customers.payment_terms_days
  note                   text,
  generated_by           uuid REFERENCES users(id),
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX billing_statements_invoice_no ON billing_statements (invoice_number);
CREATE UNIQUE INDEX billing_statements_legacy     ON billing_statements (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX billing_statements_party_gen    ON billing_statements (billing_party_id, generated_at DESC);
CREATE INDEX billing_statements_status_gen   ON billing_statements (status, generated_at DESC);
CREATE INDEX billing_statements_party_status ON billing_statements (billing_party_id, status, generated_at DESC);
CREATE INDEX billing_statements_period_lock  ON billing_statements (billing_party_id, period_year, period_month)
  WHERE status IN ('sent','paid');
CREATE INDEX billing_statements_tenant_gen   ON billing_statements (tenant_id, generated_at DESC, id DESC);
CREATE TRIGGER billing_statements_set_updated_at BEFORE UPDATE ON billing_statements
  FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
ALTER TABLE billing_statements ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (tenant, billing carrier)

-- +goose StatementBegin
CREATE FUNCTION trg_billing_statement_draft_only_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status <> 'draft' THEN
    RAISE EXCEPTION 'billing statement % is %, only a draft may be deleted', OLD.invoice_number, OLD.status
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  RETURN OLD;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER billing_statements_draft_delete BEFORE DELETE ON billing_statements
  FOR EACH ROW EXECUTE FUNCTION trg_billing_statement_draft_only_delete();

-- Which rows an invoice was built from (ADR 0008 follow-up "billedOnInvoiceNumber"). Empty for legacy statements.
CREATE TABLE billing_statement_lines (
  id            uuid PRIMARY KEY DEFAULT uuidv7(),
  statement_id  uuid NOT NULL REFERENCES billing_statements(id) ON DELETE CASCADE,
  row_type      text NOT NULL CHECK (row_type IN ('trip','multidrop_stop','standby')),
  trip_id       uuid REFERENCES trip_records(id),
  stop_index    int  CHECK (stop_index >= 1),
  standby_id    uuid REFERENCES standby_records(id),
  amount_thb    numeric(14,2) NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  CHECK ((row_type = 'standby') = (standby_id IS NOT NULL)),
  CHECK ((row_type = 'standby') = (trip_id IS NULL)),
  CHECK ((row_type = 'multidrop_stop') = (stop_index IS NOT NULL))
);
CREATE UNIQUE INDEX billing_statement_lines_row ON billing_statement_lines
  (statement_id, row_type, COALESCE(trip_id, standby_id), COALESCE(stop_index, 0));
CREATE INDEX billing_statement_lines_trip    ON billing_statement_lines (trip_id)    WHERE trip_id IS NOT NULL;
CREATE INDEX billing_statement_lines_standby ON billing_statement_lines (standby_id) WHERE standby_id IS NOT NULL;

-- UPDATE always rejected; DELETE only while the parent statement is a draft (or already gone, i.e. the
-- ON DELETE CASCADE of a draft statement).
-- +goose StatementBegin
CREATE FUNCTION trg_statement_lines_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'UPDATE' THEN
    RAISE EXCEPTION 'billing_statement_lines are immutable' USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  IF EXISTS (SELECT 1 FROM billing_statements s WHERE s.id = OLD.statement_id AND s.status <> 'draft') THEN
    RAISE EXCEPTION 'lines of statement % are frozen (statement is not a draft)', OLD.statement_id
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  RETURN OLD;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER billing_statement_lines_guard BEFORE UPDATE OR DELETE ON billing_statement_lines
  FOR EACH ROW EXECUTE FUNCTION trg_statement_lines_guard();
ALTER TABLE billing_statement_lines ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (parent-scoped via billing_statements)

-- Rendered documents per statement (R14; queue documents.render). Keys:
-- documents/statements/{statement_id}/{invoice_summary.pdf|invoice_detail.xlsx|receipt.pdf|bundle.zip}
CREATE TABLE statement_documents (
  id            uuid PRIMARY KEY DEFAULT uuidv7(),
  statement_id  uuid NOT NULL REFERENCES billing_statements(id) ON DELETE CASCADE,
  kind          text NOT NULL CHECK (kind IN ('invoice_summary_pdf','invoice_detail_xlsx','receipt_pdf','bundle_zip')),
  status        text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','rendering','ready','failed')),
  file_id       uuid REFERENCES file_objects(id),
  job_id        uuid,                                      -- jobs.id (0009) of the render job; no FK, jobs are pruned after 30 days
  error         text,
  rendered_at   timestamptz,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (statement_id, kind),
  CHECK (status <> 'ready' OR (file_id IS NOT NULL AND rendered_at IS NOT NULL))
);
CREATE INDEX statement_documents_pending ON statement_documents (created_at) WHERE status IN ('pending','rendering','failed');
CREATE TRIGGER statement_documents_set_updated_at BEFORE UPDATE ON statement_documents
  FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
ALTER TABLE statement_documents ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (parent-scoped via billing_statements)

-- Appendix C block (same file): §C.3.5 "0005_billing" generator DO block for the 10 tables above.

-- +goose Down
DROP TABLE statement_documents;
DROP TABLE billing_statement_lines;
DROP TABLE billing_statements;
CALL app_drop_definer_function('next_invoice_seq(uuid,uuid,integer,integer,text)');
DROP TABLE billing_counters;
DROP TABLE trip_billing_stop_breakdown;
DROP TABLE trip_billing_snapshots;
ALTER TABLE standby_records DROP CONSTRAINT standby_records_rate_fk;
DROP TABLE standby_rate_entries;
DROP TABLE customer_service_fees;
DROP TABLE customer_fuel_rate_adjustments;
DROP TABLE customer_rate_entries;
DROP FUNCTION trg_statement_lines_guard();
DROP FUNCTION trg_billing_statement_draft_only_delete();
```

**Notes (0005)**

- Business rules encoded.
  - Announcement rows (`customer_rate_entries`, `customer_fuel_rate_adjustments`) are never edited or deleted: a price is corrected by a newer row and retired by voiding (`trg_announcement_void_only`, which requires `voided_by` on every new void). `trg_set_updated_at` fires first (trigger names sort `..._set_updated_at` < `..._void_only`) and `updated_at` is excluded from the comparison, as in the `firestore.rules:144` whitelist.
  - Selection (`selectBillingRateEntry`, `logitrack-web/lib/billingCompute.ts:274-303`): exact match on `(party, hub_code, destination_code, vehicle_class, job_category)` among non-voided rows with `effective_from_date <= bkk_date(billing axis)`, newest first, fallback to the oldest. Equal `effective_from_at` ties break by `legacy_doc_id ASC` (legacy rows) and `created_at ASC, id ASC` (new rows; R16, `uuidv7()` ids are creation-ordered); the Go selector receives the slice in that order. Rate cards keep their stored folded `vehicle_class` (incl. `4WJ` defaults); only the trip-level class may be NULL (R15).
  - Fuel adjustment is customer-level, no fallback, skipped for `SUPPLEMENTARY`; `computeFinalRateThb = round2(base * multiplier + add)` runs in Go float64 with JS rounding; only inputs and results are stored.
  - Money writes (pricing, `forceRecompute`, job-category change, manual price, delivered-at or plan-date change) read period locks (`billing_statements` `sent`/`paid` for the party and Bangkok month, `billing_statements_period_lock`) and the four rate tables **in the pricing transaction** with `FOR SHARE`, never from Redis (R17). The `billing.compute` consumer takes the trip `FOR UPDATE`, records the message in `consumer_inbox` (0009) and drops events whose outbox id is not above `last_event_id` (a `bigint` without FK, outbox rows are pruned, R57).
  - A snapshot is priced (`estimate_thb`) or unpriced with exactly one reason (`no_customer|no_rate|no_vehicle_class|no_billing_date`, R15, R19, R62). `isFrozenBillingSnapshot` has one Go definition over `manual_override`, `job_category_at_pricing` and the fuel columns (frozen = repriced only when forced with `accounting:recompute_force`, and then only `billing_date` is re-stamped).
  - Snapshots, counters and statements carry the billing carrier's tenant (R61): the carrier that ran a trip sees the trip, never the customer price, so there is no tenant-consistency trigger between `trip_records` and `trip_billing_snapshots`.
  - Standby rates are soft-deleted (`voided_at`) because a priced standby keeps its `billing_rate_entry_id` (R20); hard delete is rejected by trigger and grant.
  - Invoice numbers come from `next_invoice_seq()` inside the statement insert transaction, after Go has checked `accounting:billing_document` and that the statement's `tenant_id` is the principal's tenant, so a failed statement leaves no gap (legacy: counter incremented before `addDoc`, `logitrack-web/lib/billingStatement.ts:84-94, 166`). Numbers are globally unique; a (party, month) counter belongs to one billing carrier and the allocator refuses another carrier's counter.
  - `withholding_tax_rate` is a fraction snapshotted from the issuing company (`companies.withholding_tax_rate / 100`); PDF and Excel render from the statement, so a re-download equals the original (R18). Legacy statements load with 0.0100 and their stored totals, never recomputed.
  - Only drafts can be deleted; statement lines are immutable and disappear only through a draft's cascade. Documents re-render in place (`UNIQUE (statement_id, kind)`); the receipt PDF renders on `statement.status_changed(paid)` only.
- Legacy Firestore source: `customer_rate_entries`, `customer_fuel_rate_adjustments`, `customer_service_fees`, `standby_rate_entries`, `billing_counters`, `billing_statements` map one-to-one (`customerId` → `billing_party_id`, customers first, then subcontractors). The trip `billing*` fields (`billingEstimateThb`, `billingBaseRateThb`, `billingStopChargeThb`, `billingRateImportId`, `billingLookupHubId`, `billingLookupDestination`, `billingFuelAdjustmentId`, `billingRateMultiplier`, `billingAddThbPerTrip`, `billingEffectiveFromDateStr`, `billingRoundEffectiveFromDateStr`, `billingFuelBand*`, `billingReferenceFuelPriceThb`, `billingIsMultiDelivery`, `billingManualOverride`, `billingMultiDeliveryBreakdown[]`) → `trip_billing_snapshots` + `trip_billing_stop_breakdown` with `computed_by='etl'`, `compute_version=0`. A legacy standby `billingRateEntryId == 'service_fee'` → `billing_rate_source='service_fee'`, no FK. `billing_statement_lines` and `statement_documents` are new from cut-over (legacy PDFs/XLSX/ZIP were generated in the browser and never stored).
- Indexes → live query.
  - `customer_rate_entries_lookup` / `customer_fuel_adj_lookup` / `standby_rate_entries_lookup`: the pricing transaction (port of `logitrack-web/functions/src/tripBillingOnDelivered.ts:369-370, 761-762` and `logitrack-web/functions/src/standbyBilling.ts:246-247`); `*_party` indexes: the rate-card page per customer (`logitrack-web/features/accounting/api/billing.ts:211-217, 299-307, 628-634`) as `['rateCard','entries',cust]` / `['rateCard','fuelAdj',cust]` / `['rateCard','standby']`; `customer_rate_entries_import`: void-by-import and the `billingRateImportId` → `rate_entry_id` resolution.
  - `customer_service_fees_party_type`: extra-stop and standby-fallback fee lookup in pricing (`tripBillingOnDelivered.ts:421-423, 823`); `customer_service_fees_party`: the fee list (I35, `billing.ts:529-535`).
  - `billing_statements_party_gen` / `_status_gen` / `_party_status` (I5 / I6 / I7, `logitrack-web/lib/billingStatement.ts:222-236`), with `period_year` / `period_month` columns replacing the in-memory period filter (`:239-246`); `billing_statements_period_lock`: `loadBillingPeriodLocks` (`logitrack-web/functions/src/core/billingPeriodLock.ts:75-78`) inside the pricing transaction; `billing_statements_tenant_gen`: the Billing Result list keyset.
  - `trip_billing_snapshots_unpriced` / `_manual`: the Billing Document problem banners ("missing price", "price set manually — review") and the Income "missing billing" tab; `billing_statement_lines_trip` / `_standby`: "which invoice billed this trip/standby" (period-lock message `blockedInvoiceNumber`).
  - `statement_documents_pending`: retry sweep of the `documents.render` consumer.

### A.2.R Renames and moves applied (part 1)

Against the earlier data-model draft (DM, files 0001–0005), the auth draft (AU), the runtime draft (RT) and earlier drafts of this appendix. "Decision" is the plan §7 / resolution entry or the reason.

| Draft object | This appendix | Decision |
|---|---|---|
| `id ... DEFAULT gen_random_uuid()` (DM, AU); Go-side UUIDv7 (DM §1.1) | `DEFAULT uuidv7()` (native PG18); Go never supplies ids | R34, R16 |
| `status_history.id`, `trip_no_history.id` `bigint` identity (DM) | `uuid DEFAULT uuidv7()`; `outbox_events.id` (0009) is the only identity column | R57 |
| implicit `GENERATED ... STORED` | explicit on `tasks.plan_date`, both `billing_axis_date`, `trip_photos.photo_type_known` | R34 |
| roles created in DM 0001; objects owned by whatever login ran goose | no migration creates a role: `deploy/postgres-init/00-roles.sql` (five roles); 0001 asserts them and refuses any login but `logitrack_migrator` | R66 |
| REVOKEs next to tables (DM 0005/0006/0008), undone by the later blanket GRANT | no GRANT/REVOKE in 0001–0008; 0009 is the single grant site | R66 (see A.2.S) |
| RLS helpers in AU §3.2 | 0001: the eight AU readers plus `app_subtenant_ids`, `app_is_steward`, `app_tenant_move_allowed`, `app_etl_load`, `app_is_scope`, `app_is_authenticated`, `app_tenant_in_reach`, `app_quarantine_tenant_id` | R12, R56, R60 |
| — | `app_drop_definer_function()` (0001), Down-only | R31, R66 |
| `t_tenant_admin_columns`, `driver_directory()` described in Appendix C without a body | printed in 0002 / 0003 (the other Appendix C objects stay in Appendix C, named per Up section) | R33, R67 |
| `trg_outbox_notify` | kept in 0001 (used by 0009) | file ordering |
| RLS on some tenant tables only; 0002 identity tables and 0003 masters without RLS | `ENABLE` + `FORCE` on all 47 tables of 0002–0005 (Appendix C §C.3.0 "yes" rows); policies in the table's own file | R67 |
| `tenants.kind IN ('own_fleet','carrier')`; AU enum `own_fleet\|subcontractor\|quarantine` | `kind IN ('own_fleet','carrier','quarantine')` + one-row partial unique indexes | R7, R11 |
| quarantine row created by `cmd/seed` / `cmd/etl` | inserted by 0002 with the fixed id `00000000-0000-7000-8000-00000000000f`; own fleet stays seed/ETL data | R56 |
| — | `tenants.contractor_tenant_id` (carriers only, not self) + `tenants_contractor` | R56, R60 |
| `tenants.name` (DM); AU `legacy_subcontractor_id`; AU `subcontractors` table | `name_th` + `name_en`, `legacy_doc_id`, carrier-only `legal_type` CHECK; profile folded into `tenants` | R6 |
| AU `users.legacy_firebase_uid` | `users.legacy_auth_uid` (same name as `drivers.legacy_auth_uid`) | R55 |
| DM `users.legacy_password jsonb`, `disabled`, `force_logout_at`, `revocation_epoch`, `is_platform_admin` | `legacy_scrypt_hash`/`legacy_scrypt_salt bytea`, `status` + `disabled_at`/`deleted_at`, `auth_version`, `must_change_password`, `password_changed_at`; platform admin in `user_platform_roles` | R2, R6, R29, R55 |
| AU `users.google_sub`, `users.photo_url`; DM provider `password` | `auth_identities(provider IN ('google','firebase_legacy'))`; `photo_file_id`; the hash lives on `users` | name map |
| `auth_sessions` (DM) | `sessions`, `refresh_tokens`, `password_reset_tokens` (`purpose reset\|invite`), `api_keys` (`rate_per_min`, `revoked_by`) | R2, R14, R25, R29 |
| a device-id column on `sessions` (AU); active tenant only in the token | `install_id` + `sessions_user_install_live`; `active_tenant_id`; `revoked_reason` CHECK (Appendix C §C.4.4) | R83, R50 |
| API-key scope derived from capabilities (AU) | `api_keys.scope` CHECK; `cf_shim` keys are platform keys | R82, R45 |
| `roles`, `capabilities`, `role_capabilities` (DM) | Go catalog + `role_capability_overrides` (`UNIQUE NULLS NOT DISTINCT`, non-grantable CHECKs) | R5, R6 |
| `tenant_memberships(role_id)` incl. `partner` (DM) | `memberships` (`memberships_tenant_role_check`, `status`); `partner` → `tenant_admin` | R6 |
| `file_objects` in 0009 with `source_status` (DM); RT `storage_objects`; DP `media_objects` | `file_objects` in 0002 (`status`, `purpose`, `tenant_id`, `uploaded_by`, `expires_at`, `committed_at`); no other registry | R1 |
| `user_scopes(customer_id → customers)` in 0002 (DM); AU `subcontractor_id`, `company_id`; reserved `company` kind | 0003 after `billing_parties`; `kind IN ('customer','dispatcher')`, `billing_party_id NOT NULL`, `UNIQUE (user_id, kind, billing_party_id)` | R6, R86 |
| counters incremented by service SQL (DM) | SECURITY DEFINER `next_task_seq()` (0004), `next_invoice_seq()` (0005), owned by `logitrack_rls_definer` after 0009; counters not granted to `logitrack_app` | R10, R61, R66, R67 |
| offline replays deduplicated only by `Idempotency-Key` (RT) | `client_op_id` + partial unique on `tasks`, `standby_records`, `incident_reports` (others in A.2.S) | R63 |
| AU `legacy_driver_refs` table; AU `drivers.legacy_doc_id NOT NULL` | per-row `legacy_driver_ref` + `driver_ref_match`; nullable `drivers.legacy_doc_id` | R12 |
| AU `task_helpers` junction | scalar `tasks.helper_driver_id` | R12, R28 |
| per-table `tenant_source` subsets incl. `etl_creator` (DM) | the full R11 set on every tenant-stamped table, incl. `truck_assignments`, `vehicle_locations`, `companies`, `trip_billing_snapshots`, `billing_counters` | R11, R58 |
| `companies.withholding_tax_rate ... DEFAULT 3` (DM) | no default | R18, §19.2 question 6 |
| `trucks.current_assignments` (RT); `drivers.active_task_id` without FK (DM) | derived from `truck_assignments`; `drivers_active_task_fk` in 0004 | R14, file ordering |
| `tasks` | `cancelled_at`, `cancel_reason`; keyset indexes `(…, created_at DESC, id DESC)`; party indexes | RT §2.6, R12 |
| `trip_records.created_at NOT NULL` without default; RT `trips.renamed_*` | `DEFAULT now()` (ETL coalesces); `trip_no_history` | R19, R14 |
| AU evidence-link table with 90-day expiry and token rotation | `evidence_token` + `evidence_token_revoked_at` on `trip_records` and `standby_records`; revoke routes | R30, R47 |
| `trip_photos.photo_type` regex CHECK (DM) | free text + `photo_type_known`; `updated_at` | R19 |
| `standby_records` | `billing_unpriced_reason`, `evidence_token_revoked_at`, `client_op_id` | R19, R47, R62, R63 |
| AU §3.7 link consistency (prose) | deferred constraint triggers in 0004 | R12, R13 |
| `customer_service_fees_one_per_type` UNIQUE in 0005 (DM) | non-unique `customer_service_fees_party_type`; UNIQUE in `0010_d5_unique_constraints.sql` | D5, R59 |
| announcement `CHECK (voided_at AND voided_by NOT NULL)` (DM) | `CHECK (NOT voided OR voided_at IS NOT NULL)`; trigger requires `voided_by` on new voids | R19 |
| `standby_rate_entries` hard delete, FK `ON DELETE SET NULL` (DM) | `voided_at`/`voided_by`/`voided_reason`, DELETE forbidden, plain FK | R20 |
| `trip_billing_snapshots`; `billing_counters` without tenant (DM) | `tenant_source`, `unpriced_reason`, `last_event_id bigint` (no FK), billing-carrier `tenant_id`; counters get `tenant_id` + `tenant_source` | R15, R17, R57, R61, R62 |
| `billing_statements.withholding_tax_rate numeric(6,4)` (DM) | `NUMERIC(5,4)` with `0..1` CHECK; `issuer_company_id`, `cancelled_at` | R18 |
| `billing_statement_lines` PK with expressions (invalid); RT `billing_statement_rows` | surrogate `id` + unique expression index; draft-cascade-aware guard; new `statement_documents` | R14 |
| RT `hub_soc_distances` + `soc_hub_distances` | one `hub_soc_distances` with `direction` | R14 |

### A.2.5 Finance and HR (0006_finance_hr.sql)

Tables: `vehicle_expenses`, `maintenance_records`, `maintenance_files`, `transactions`, `driver_compensation_configs`, `penalty_types`, `driver_penalties`, `payroll_runs`, `payroll_line_items`, `payroll_penalty_applications`, `driver_advances`. Every table except `penalty_types`/`maintenance_files`/`payroll_line_items`/`payroll_penalty_applications` carries `tenant_id`; all are carrier-internal (no scope principal reads them, [Appendix C](./C-auth-rbac.md) §C.3.7). All eleven are RLS tables in the [Appendix C](./C-auth-rbac.md) §C.3.0 coverage table.

```sql
-- 0006_finance_hr.sql
-- Depends on: 0001 (bkk_date, trg_set_updated_at, trg_forbid_mutation, app_* GUC readers, app_rls_* generators),
--             0002 (tenants, users, file_objects), 0003 (drivers, trucks, truck_assignments; trucks.active_maintenance_id).
-- Ids: uuid DEFAULT uuidv7() (PostgreSQL 18 native, R57). Money: NUMERIC(14,2) (R20).
-- tenant_source: inline CHECK, vocabulary task|trip|driver|truck|self|form|quarantine (R11, R58).
-- No GRANT/REVOKE here: 0009_infra is the single grant site (R66). RLS: all eleven tables (Appendix C §C.3.0).

-- +goose Up

CREATE TABLE vehicle_expenses (                              -- fuel + other expenses (mobile, admin, toll import)
  id                           uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id                text,
  tenant_id                    uuid NOT NULL REFERENCES tenants(id),
  tenant_source                text NOT NULL
                               CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  driver_id                    uuid REFERENCES drivers(id),
  legacy_driver_ref            text,                           -- raw legacy driverId (auth uid or doc id), R12
  driver_ref_match             text CHECK (driver_ref_match IN ('doc_id','auth_uid','none')),
  truck_id                     uuid REFERENCES trucks(id),
  truck_license_plate_snapshot text,                           -- display string, never normalised
  expense_type                 text NOT NULL CHECK (expense_type IN ('fuel','other')),
  category                     text CHECK (category IN ('tire_repair','maintenance','toll','parking','other')),
  expense_at                   timestamptz NOT NULL,           -- legacy `date` (Timestamp)
  expense_date                 date GENERATED ALWAYS AS (bkk_date(expense_at)) STORED,
  amount_thb                   numeric(14,2) NOT NULL,
  status                       text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
  volume_liters                numeric(10,3),
  price_per_liter              numeric(14,2),
  odometer_km                  int CHECK (odometer_km >= 0),
  station_tax_id               text,                           -- legacy alias gasStation
  tax_inv_id                   text,
  refill_lat                   double precision,
  refill_lng                   double precision,
  receipt_file_id              uuid REFERENCES file_objects(id),
  odometer_file_id             uuid REFERENCES file_objects(id),
  note                         text,
  description                  text,
  admin_note                   text,
  distance_km                  numeric(9,2),
  toll_import_sequence         int,
  toll_location                text,
  toll_lane                    text,
  toll_source_type             text,
  client_op_id                 uuid,                           -- offline outbox op id, body `clientOpId` (R63)
  created_by_user_id           uuid REFERENCES users(id),
  created_at                   timestamptz NOT NULL DEFAULT now(),   -- ETL supplies the legacy value
  updated_at                   timestamptz NOT NULL DEFAULT now(),
  CHECK (driver_id IS NOT NULL OR truck_id IS NOT NULL OR tenant_source = 'quarantine')
);
CREATE UNIQUE INDEX vehicle_expenses_legacy      ON vehicle_expenses (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE UNIQUE INDEX vehicle_expenses_client_op   ON vehicle_expenses (driver_id, client_op_id)
  WHERE client_op_id IS NOT NULL;                             -- durable offline replay key (R63)
CREATE INDEX vehicle_expenses_driver_date        ON vehicle_expenses (driver_id, expense_at DESC);
CREATE INDEX vehicle_expenses_type_date          ON vehicle_expenses (tenant_id, expense_type, expense_at DESC, id DESC);
CREATE INDEX vehicle_expenses_status_date        ON vehicle_expenses (tenant_id, status, expense_at DESC, id DESC);
CREATE INDEX vehicle_expenses_pending            ON vehicle_expenses (tenant_id) WHERE status = 'pending';
CREATE INDEX vehicle_expenses_truck_date         ON vehicle_expenses (truck_id, expense_at DESC);
CREATE INDEX vehicle_expenses_fuel_taxinv_lookup ON vehicle_expenses (driver_id, tax_inv_id)
  WHERE expense_type = 'fuel' AND tax_inv_id IS NOT NULL;     -- UNIQUE version is created by 0010 (D5, R31, R59)
CREATE TRIGGER vehicle_expenses_updated_at BEFORE UPDATE ON vehicle_expenses
  FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();

CREATE TABLE maintenance_records (                           -- collection `maintenance`
  id                           uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id                text,
  tenant_id                    uuid NOT NULL REFERENCES tenants(id),
  tenant_source                text NOT NULL DEFAULT 'truck'
                               CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  truck_id                     uuid REFERENCES trucks(id),
  truck_license_plate_snapshot text,
  truck_brand_snapshot         text,
  maintenance_type             text NOT NULL CHECK (maintenance_type IN ('PM','CM')),   -- domain codes, kept upper
  service_type                 text NOT NULL,
  start_date                   date NOT NULL,
  end_date                     date,
  status                       text NOT NULL
                               CHECK (status IN ('pm_booking','scheduled','in_progress','completed','cancelled')),
  appointment_time             text CHECK (appointment_time ~ '^[0-9]{2}:[0-9]{2}$'),
  pickup_appointment_at        timestamptz,
  cost_labor_thb               numeric(14,2),
  cost_parts_thb               numeric(14,2),
  total_cost_thb               numeric(14,2),
  invoice_amount_thb           numeric(14,2),                  -- driver receipt total (maintenanceDisplayCost fallback)
  provider                     text,
  provider_lat                 double precision,
  provider_lng                 double precision,
  payment_method               text CHECK (payment_method IN ('cash','credit_card','billing','transfer','insurance_claim')),
  current_mileage              int,
  next_service_mileage         int,
  driver_submitted             boolean NOT NULL DEFAULT false,
  check_in_at                  timestamptz,
  check_out_at                 timestamptz,
  notes                        text,
  created_by                   uuid REFERENCES users(id),
  updated_by                   uuid REFERENCES users(id),
  created_at                   timestamptz NOT NULL DEFAULT now(),
  updated_at                   timestamptz NOT NULL DEFAULT now(),
  CHECK (truck_id IS NOT NULL OR tenant_source = 'quarantine')
);
CREATE UNIQUE INDEX maintenance_records_legacy ON maintenance_records (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX maintenance_truck_created         ON maintenance_records (truck_id, created_at DESC);
CREATE INDEX maintenance_truck_status          ON maintenance_records (truck_id, status);
CREATE INDEX maintenance_tenant_created        ON maintenance_records (tenant_id, created_at DESC, id DESC);
CREATE INDEX maintenance_open                  ON maintenance_records (tenant_id, status)
  WHERE status IN ('pm_booking','scheduled','in_progress');
CREATE TRIGGER maintenance_records_updated_at BEFORE UPDATE ON maintenance_records
  FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
ALTER TABLE trucks ADD CONSTRAINT trucks_active_maintenance_fk
  FOREIGN KEY (active_maintenance_id) REFERENCES maintenance_records(id) ON DELETE SET NULL;

CREATE TABLE maintenance_files (                             -- images[], receipts[], invoiceUrl
  maintenance_id uuid NOT NULL REFERENCES maintenance_records(id) ON DELETE CASCADE,
  file_id        uuid NOT NULL REFERENCES file_objects(id),
  kind           text NOT NULL CHECK (kind IN ('image','receipt','invoice')),
  position       int  NOT NULL DEFAULT 0,
  PRIMARY KEY (maintenance_id, file_id)
);

CREATE TABLE transactions (                                  -- append-only ledger; two legacy shapes (renewal, payout)
  id                   uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id        text,
  tenant_id            uuid NOT NULL REFERENCES tenants(id),
  tenant_source        text NOT NULL
                       CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  tx_type              text NOT NULL CHECK (tx_type IN ('tax','insurance','driver_payout')),
  sub_type             text,
  amount_thb           numeric(14,2) NOT NULL,
  payment_method       text,
  tx_date              date NOT NULL,
  tx_date_source       text NOT NULL DEFAULT 'bangkok' CHECK (tx_date_source IN ('bangkok','legacy_utc')),
  truck_id             uuid REFERENCES trucks(id),
  driver_id            uuid REFERENCES drivers(id),
  legacy_driver_ref    text,
  driver_ref_match     text CHECK (driver_ref_match IN ('doc_id','auth_uid','none')),
  payroll_run_id       uuid,                                   -- FK added after payroll_runs (deferrable, circular)
  pay_period           text CHECK (pay_period ~ '^[0-9]{4}-[0-9]{2}$'),
  pay_round            text CHECK (pay_round IN ('R1','R2')),
  receipt_file_id      uuid REFERENCES file_objects(id),
  performed_by_user_id uuid REFERENCES users(id),
  performed_by_label   text,                                   -- legacy displayName/email string
  notes                text,
  created_at           timestamptz NOT NULL DEFAULT now(),
  CHECK (tx_type <> 'driver_payout' OR (pay_period IS NOT NULL AND pay_round IS NOT NULL))
);
CREATE UNIQUE INDEX transactions_legacy ON transactions (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX transactions_tenant_date   ON transactions (tenant_id, tx_date DESC, id DESC);
CREATE INDEX transactions_truck         ON transactions (truck_id, tx_date DESC) WHERE truck_id IS NOT NULL;
CREATE INDEX transactions_payroll       ON transactions (payroll_run_id) WHERE payroll_run_id IS NOT NULL;
CREATE TRIGGER transactions_immutable BEFORE UPDATE OR DELETE ON transactions
  FOR EACH ROW EXECUTE FUNCTION trg_forbid_mutation();

CREATE TABLE driver_compensation_configs (                   -- append-only versions; latest effective_from <= date wins
  id                             uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id                  text,
  tenant_id                      uuid NOT NULL REFERENCES tenants(id),
  tenant_source                  text NOT NULL DEFAULT 'form'
                                 CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  effective_from_at              timestamptz NOT NULL,
  effective_from_date            date GENERATED ALWAYS AS (bkk_date(effective_from_at)) STORED,
  weekday_rate_thb               numeric(14,2) NOT NULL DEFAULT 300
                                 CHECK (weekday_rate_thb >= 0 AND weekday_rate_thb = trunc(weekday_rate_thb)),
  holiday_rate_thb               numeric(14,2) NOT NULL DEFAULT 350
                                 CHECK (holiday_rate_thb >= 0 AND holiday_rate_thb = trunc(holiday_rate_thb)),
  pay_standby                    boolean NOT NULL DEFAULT false,
  helper_day_rate_thb            numeric(14,2) NOT NULL DEFAULT 400 CHECK (helper_day_rate_thb >= 0),
  fuel_incentive_tiers           jsonb NOT NULL DEFAULT '[]',   -- [{minKmPerLitre, amountThb}]
  fuel_min_refuels_per_month     int NOT NULL DEFAULT 5,
  trip_volume_tiers              jsonb NOT NULL DEFAULT '[]',   -- [{minTrips, amountThb}]
  sso_rate_percent               numeric(5,2) NOT NULL DEFAULT 5,
  sso_base_existing_thb          numeric(14,2) NOT NULL DEFAULT 15000,
  sso_base_new_thb               numeric(14,2) NOT NULL DEFAULT 12000,
  sso_existing_hired_before_year int NOT NULL DEFAULT 2026,
  sso_max_age_inclusive          int NOT NULL DEFAULT 55,
  sso_probation_months           int NOT NULL DEFAULT 3,
  created_by                     uuid REFERENCES users(id),
  created_at                     timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX driver_compensation_configs_legacy ON driver_compensation_configs (legacy_doc_id)
  WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX driver_compensation_configs_eff ON driver_compensation_configs (tenant_id, effective_from_at DESC, id DESC);

CREATE TABLE penalty_types (                                 -- config.penaltyTypes[] per config version
  config_id          uuid NOT NULL REFERENCES driver_compensation_configs(id) ON DELETE CASCADE,
  code               text NOT NULL,
  name_en            text NOT NULL,
  name_th            text NOT NULL,
  default_amount_thb numeric(14,2) NOT NULL DEFAULT 0 CHECK (default_amount_thb >= 0),
  PRIMARY KEY (config_id, code)
);

CREATE TABLE driver_penalties (
  id                 uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id      text,
  tenant_id          uuid NOT NULL REFERENCES tenants(id),
  tenant_source      text NOT NULL DEFAULT 'driver'
                     CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  driver_id          uuid REFERENCES drivers(id),
  legacy_driver_ref  text,
  driver_ref_match   text CHECK (driver_ref_match IN ('doc_id','auth_uid','none')),
  type_code          text NOT NULL,
  type_name_snapshot text,
  total_thb          numeric(14,2) NOT NULL CHECK (total_thb >= 0 AND total_thb = trunc(total_thb)),
  remaining_thb      numeric(14,2) NOT NULL CHECK (remaining_thb >= 0 AND remaining_thb <= total_thb),
  installments_total int NOT NULL DEFAULT 1 CHECK (installments_total > 0),
  installments_paid  int NOT NULL DEFAULT 0 CHECK (installments_paid >= 0 AND installments_paid <= installments_total),
  reason             text,
  evidence_file_id   uuid REFERENCES file_objects(id),
  status             text NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending','partially_deducted','cleared','cancelled')),
  incurred_at        timestamptz NOT NULL,
  created_by         uuid REFERENCES users(id),
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  CHECK (driver_id IS NOT NULL OR tenant_source = 'quarantine')
);
CREATE UNIQUE INDEX driver_penalties_legacy ON driver_penalties (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX driver_penalties_open          ON driver_penalties (driver_id) WHERE status IN ('pending','partially_deducted');
CREATE INDEX driver_penalties_incurred      ON driver_penalties (tenant_id, incurred_at DESC, id DESC);
CREATE TRIGGER driver_penalties_updated_at BEFORE UPDATE ON driver_penalties
  FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();

CREATE TABLE payroll_runs (                                  -- legacy payroll/{authUid}_{YYYY-MM}_{R1|R2}; keyed by driver_id
  id                    uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id         text,
  tenant_id             uuid NOT NULL REFERENCES tenants(id),
  tenant_source         text NOT NULL DEFAULT 'driver'
                        CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  driver_id             uuid REFERENCES drivers(id),
  legacy_driver_ref     text,
  driver_ref_match      text CHECK (driver_ref_match IN ('doc_id','auth_uid','none')),
  pay_period            text NOT NULL CHECK (pay_period ~ '^[0-9]{4}-[0-9]{2}$'),
  pay_round             text NOT NULL CHECK (pay_round IN ('R1','R2')),    -- R1 = days 1-15, R2 = 16-end (Bangkok)
  period_start          timestamptz NOT NULL,                  -- Bangkok midnight bounds
  period_end            timestamptz NOT NULL,
  status                text NOT NULL DEFAULT 'draft'
                        CHECK (status IN ('draft','pending_approval','approved','paid','cancelled')),
  total_earnings_thb    numeric(14,2) NOT NULL DEFAULT 0,
  total_deductions_thb  numeric(14,2) NOT NULL DEFAULT 0,
  net_pay_thb           numeric(14,2) NOT NULL DEFAULT 0,
  currency              text NOT NULL DEFAULT 'THB' CHECK (currency = 'THB'),
  payment_date          timestamptz,
  payment_method        text,
  remarks               text,
  approved_by           uuid REFERENCES users(id),
  approved_at           timestamptz,
  ledger_transaction_id uuid REFERENCES transactions(id),
  created_at            timestamptz NOT NULL DEFAULT now(),
  updated_at            timestamptz NOT NULL DEFAULT now(),
  CHECK (period_end > period_start),
  CHECK (driver_id IS NOT NULL OR tenant_source = 'quarantine')
);
CREATE UNIQUE INDEX payroll_runs_legacy        ON payroll_runs (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE UNIQUE INDEX payroll_runs_driver_period ON payroll_runs (driver_id, pay_period, pay_round);
CREATE INDEX payroll_runs_period_end           ON payroll_runs (tenant_id, period_end DESC, id DESC);
CREATE INDEX payroll_runs_period_status        ON payroll_runs (tenant_id, pay_period, pay_round, status);
CREATE TRIGGER payroll_runs_updated_at BEFORE UPDATE ON payroll_runs
  FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
ALTER TABLE transactions ADD CONSTRAINT transactions_payroll_fk
  FOREIGN KEY (payroll_run_id) REFERENCES payroll_runs(id) DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE payroll_line_items (                            -- frozen once the run leaves draft (ADR 0013)
  id             uuid PRIMARY KEY DEFAULT uuidv7(),
  payroll_run_id uuid NOT NULL REFERENCES payroll_runs(id) ON DELETE CASCADE,
  line_no        int  NOT NULL CHECK (line_no >= 0),
  item_type      text NOT NULL CHECK (item_type IN ('earning','deduction')),
  category       text NOT NULL,      -- ADR 0013 code as written by the engine: TRIP_COMMISSION, HELPER_PAY, FUEL_INCENTIVE,
                                     -- TRIP_VOLUME_INCENTIVE, SOCIAL_SECURITY, PENALTY, CASH_ADVANCE, BASE_SALARY, TAX
  name           text NOT NULL,
  amount_thb     numeric(14,2) NOT NULL,
  description    text,
  reference_id   text,
  quantity       numeric(12,3),
  unit_rate_thb  numeric(14,2),
  meta           jsonb,
  UNIQUE (payroll_run_id, line_no)
);
-- +goose StatementBegin
CREATE FUNCTION trg_payroll_lines_frozen() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE s text;
BEGIN
  SELECT status INTO s FROM payroll_runs WHERE id = COALESCE(NEW.payroll_run_id, OLD.payroll_run_id);
  -- s IS NULL when the parent row is being deleted (FK cascade): allowed.
  IF s IS NOT NULL AND s <> 'draft' THEN
    RAISE EXCEPTION 'payroll run % is %, line items are frozen', COALESCE(NEW.payroll_run_id, OLD.payroll_run_id), s
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  RETURN COALESCE(NEW, OLD);
END $$;
-- +goose StatementEnd
CREATE TRIGGER payroll_line_items_frozen BEFORE INSERT OR UPDATE OR DELETE ON payroll_line_items
  FOR EACH ROW EXECUTE FUNCTION trg_payroll_lines_frozen();

CREATE TABLE payroll_penalty_applications (                  -- penaltyApplications[]; committed to driver_penalties on approve
  payroll_run_id uuid NOT NULL REFERENCES payroll_runs(id) ON DELETE CASCADE,
  penalty_id     uuid NOT NULL REFERENCES driver_penalties(id),
  applied_thb    numeric(14,2) NOT NULL CHECK (applied_thb >= 0),
  PRIMARY KEY (payroll_run_id, penalty_id)
);
CREATE INDEX payroll_penalty_applications_penalty ON payroll_penalty_applications (penalty_id);

CREATE TABLE driver_advances (                               -- ADR 0014 cash advance; table shipped, UI deferred (R30)
  id             uuid PRIMARY KEY DEFAULT uuidv7(),
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  tenant_source  text NOT NULL DEFAULT 'driver'
                 CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  driver_id      uuid NOT NULL REFERENCES drivers(id),
  amount_thb     numeric(14,2) NOT NULL CHECK (amount_thb > 0),
  withdrawn_at   timestamptz NOT NULL,
  deduct_period  text NOT NULL CHECK (deduct_period ~ '^[0-9]{4}-[0-9]{2}$'),
  deduct_round   text NOT NULL CHECK (deduct_round IN ('R1','R2')),
  status         text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','deducted','cancelled')),
  reason         text,
  payroll_run_id uuid REFERENCES payroll_runs(id),
  created_by     uuid REFERENCES users(id),
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  CHECK (status <> 'deducted' OR payroll_run_id IS NOT NULL)
);
CREATE INDEX driver_advances_open ON driver_advances (driver_id, deduct_period, deduct_round) WHERE status = 'pending';
CREATE INDEX driver_advances_tenant ON driver_advances (tenant_id, withdrawn_at DESC, id DESC);
CREATE TRIGGER driver_advances_updated_at BEFORE UPDATE ON driver_advances
  FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();

-- Row level security: Appendix C §C.3.0 marks all eleven tables "yes" (family in the trailing comment).
ALTER TABLE vehicle_expenses             ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- tenant
ALTER TABLE maintenance_records          ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- tenant
ALTER TABLE maintenance_files            ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- parent-scoped: maintenance_records
ALTER TABLE transactions                 ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- tenant
ALTER TABLE driver_compensation_configs  ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- tenant
ALTER TABLE penalty_types                ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- parent-scoped: driver_compensation_configs
ALTER TABLE driver_penalties             ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- tenant
ALTER TABLE payroll_runs                 ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- tenant
ALTER TABLE payroll_line_items           ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- parent-scoped: payroll_runs
ALTER TABLE payroll_penalty_applications ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- parent-scoped: payroll_runs
ALTER TABLE driver_advances              ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- tenant
-- Appended here, in this file: the Appendix C §C.3.5 "0006_finance_hr" block (generator CALLs, p_driver_update_pending,
-- app_driver_truck_ids(), the maintenance driver policies). ENABLE/FORCE above are idempotent with the generators,
-- so no table of this file is ever deny-all between migrations.

-- +goose Down
DROP TABLE driver_advances;
DROP TABLE payroll_penalty_applications;
DROP TABLE payroll_line_items;
DROP FUNCTION trg_payroll_lines_frozen();
ALTER TABLE transactions DROP CONSTRAINT transactions_payroll_fk;
DROP TABLE payroll_runs;
DROP TABLE driver_penalties;
DROP TABLE penalty_types;
DROP TABLE driver_compensation_configs;
DROP TABLE transactions;
DROP TABLE maintenance_files;
ALTER TABLE trucks DROP CONSTRAINT trucks_active_maintenance_fk;
DROP TABLE maintenance_records;                               -- drops the policies that call app_driver_truck_ids()
DROP FUNCTION IF EXISTS app_driver_truck_ids();               -- created by the appended Appendix C block
DROP TABLE vehicle_expenses;
```

**Notes (0006)**

- **Payroll is keyed by `driver_id`.** Legacy payroll docs are keyed by the auth uid (`{authId}_{YYYY-MM}_{R1|R2}`, `driverCompensation.ts:294`) and the generator selects own tasks with `tasks.driverId == authId` (`functions/src/driverCompensation.ts:199-204`, read in the repo) although `tasks.driverId` holds the drivers doc id (schemas report line 1). In PostgreSQL both sides are `drivers.id`: own tasks by `tasks.driver_id`, helper days by `tasks.helper_driver_id` — the documented P4 parity exception (main spec §6).
- **Ledger date.** New `driver_payout` rows use the Bangkok date (`tx_date_source='bangkok'`), fixing billing divergence #11 (`functions/src/driverCompensation.ts:384`, UTC); legacy payouts load with `'legacy_utc'` and the date as written.
- **Circular FK.** `payroll_runs.ledger_transaction_id → transactions` and `transactions.payroll_run_id → payroll_runs` (deferred): the approve transaction inserts the append-only ledger row first and then sets `ledger_transaction_id`; `cmd/etl` loads both shapes in one transaction.
- **Line items frozen** (ADR 0013): `trg_payroll_lines_frozen` rejects writes unless the run is `draft`, the only state the `payroll.run` job overwrites. `category` keeps the ADR 0013 upper-case codes; status vocabularies are lower_snake (R30, R65).
- **FK nullability.** `driver_id` (`driver_penalties`, `payroll_runs`), driver-or-truck (`vehicle_expenses`) and `maintenance_records.truck_id` are required through `CHECK (… OR tenant_source = 'quarantine')`: only ETL rows parked in the quarantine tenant (R11) may lack them, so legacy rows load (R19) while API writes still need the FK.
- **Durable offline idempotency (R63).** `vehicle_expenses.client_op_id` is the body `clientOpId` of `POST /v1/mobile/expenses` (Appendix B §B.2.21); `vehicle_expenses_client_op` turns a replay after the `IDEMPOTENCY_TTL` window (168h, R53) into a conflict answered with the existing row. Staff and legacy rows leave it NULL. From P7a until P7b the service still writes Firestore (class C write-back, main spec §13.8): it checks `(driver_id, client_op_id)` in PostgreSQL first and stores `clientOpId` on the Firestore document so the mirror fills the column.
- **Fuel tax-invoice uniqueness** `(driver_id, tax_inv_id)` is a D5 constraint: 0006 creates the lookup index, `0010` the UNIQUE index after the quarantine sign-off (§A.4 D5, R59).
- **RLS (Appendix C §C.3.0, §C.3.5).** Drivers read their own expenses and payroll runs (lines through the parent), insert expenses and edit them while `pending`, and read/update `maintenance_records` of the truck they hold now (`drivers.active_truck_id`) or of an active assignment, replacing `logitrack-web/firestore.rules:381-418`. Penalties, configs, `transactions` and `driver_advances` are staff-only.
- **Money.** THB amounts are `NUMERIC(14,2)` (R20), `price_per_liter` included (receipts print two decimals; OCR-derived values are rounded half-up at load, `fuel_receipt_ocr_service.dart:108-130`). Whole-baht HR fields keep a `= trunc(x)` CHECK so Go's `roundTHB` is the only rounding site.
- **`driver_advances`** has no legacy rows (ADR 0014 never shipped on Firestore); it gives the `CASH_ADVANCE` deduction a home, with endpoints and UI deferred (R30).
- **Indexes.** `GET /v1/expenses?type&status&cursor` (W8/W9) uses `vehicle_expenses_type_date` / `_status_date` with keyset `(expense_at, id)`, replacing the all-types scan of `features/accounting/api/expenses.ts:90-101` (Appendix E).
- **Who writes.** `vehicle_expenses` and `maintenance_records` stay Firestore-owned (class C) until P7b: the mirror fills them and staff writes from P3 go through Go's write-back. Renewal `transactions` become PostgreSQL-written at P1 with the trucks; payroll, penalties, configs and payout `transactions` at P4 (main spec §13.11).

### A.2.6 Comms (0007_comms.sql)

Tables: `device_tokens`, `chats`, `chat_messages`, `chat_read_state`, `broadcasts`, `broadcast_recipients`, `broadcast_reads`, `notification_deliveries`. `chat_messages`, `chat_read_state`, `broadcast_recipients`, `broadcast_reads` and `device_tokens` have no `tenant_id` and are scoped through their parent (R12); `broadcasts.tenant_id` is nullable (NULL = platform-wide, steward write, R12, R60). Appendix C §C.3.0: the first seven are RLS tables; `notification_deliveries` is exempt (service layer only).

```sql
-- 0007_comms.sql
-- Depends on: 0001 (trg_set_updated_at, app_* GUC readers, app_rls_* generators), 0002 (tenants, users, file_objects),
--             0003 (drivers). No GRANT/REVOKE here (0009 is the single grant site, R66).

-- +goose Up

CREATE TABLE device_tokens (                                 -- one FCM token per (user, install); merges drivers.fcmToken + users.fcmTokens
  user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  install_id    text NOT NULL,                               -- body installId; legacy rows 'legacy:' + first 16 hex of sha256(token) (§A.3.1)
  token         text NOT NULL,
  driver_id     uuid REFERENCES drivers(id) ON DELETE SET NULL,   -- set when the user is a linked driver (R4)
  platform      text NOT NULL DEFAULT 'other' CHECK (platform IN ('ios','android','web','other')),
  app_flavor    text CHECK (app_flavor IN ('dev','prod')),
  app_version   text,
  legacy_source text CHECK (legacy_source IN ('drivers.fcmToken','users.fcmTokens')),
  created_at    timestamptz NOT NULL DEFAULT now(),
  last_seen_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, install_id)
);
CREATE UNIQUE INDEX device_tokens_token ON device_tokens (token);
CREATE INDEX device_tokens_driver       ON device_tokens (driver_id) WHERE driver_id IS NOT NULL;

CREATE TABLE chats (                                         -- one open conversation per driver (index in 0010, D5)
  id                      uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id           text,
  tenant_id               uuid NOT NULL REFERENCES tenants(id),
  tenant_source           text NOT NULL DEFAULT 'driver'
                          CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  driver_id               uuid REFERENCES drivers(id),
  legacy_driver_ref       text,                              -- legacy chats.driverId = auth uid
  driver_ref_match        text CHECK (driver_ref_match IN ('doc_id','auth_uid','none')),
  status                  text NOT NULL DEFAULT 'open' CHECK (status IN ('open','in_progress','closed')),
  assigned_admin_user_id  uuid REFERENCES users(id),
  assigned_at             timestamptz,
  closed_at               timestamptz,
  priority                text NOT NULL DEFAULT 'normal' CHECK (priority IN ('normal','urgent')),
  last_message_preview    text,                              -- '[Image]' for image-only messages
  last_message_at         timestamptz,
  last_message_by_user_id uuid REFERENCES users(id),
  last_message_type       text CHECK (last_message_type IN ('normal','broadcast')),
  created_at              timestamptz NOT NULL DEFAULT now(),
  updated_at              timestamptz NOT NULL DEFAULT now(),
  CHECK (driver_id IS NOT NULL OR tenant_source = 'quarantine')
);
CREATE UNIQUE INDEX chats_legacy ON chats (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX chats_my     ON chats (assigned_admin_user_id, last_message_at DESC, id DESC) WHERE status <> 'closed';
CREATE INDEX chats_queue  ON chats (tenant_id, last_message_at DESC, id DESC)
  WHERE assigned_admin_user_id IS NULL AND status <> 'closed';
CREATE INDEX chats_driver ON chats (driver_id, last_message_at DESC);
CREATE INDEX chats_tenant ON chats (tenant_id, last_message_at DESC, id DESC);
CREATE INDEX chats_urgent ON chats (tenant_id) WHERE priority = 'urgent' AND status <> 'closed';
CREATE TRIGGER chats_updated_at BEFORE UPDATE ON chats FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();

CREATE TABLE chat_messages (                                 -- chats/{id}/messages
  id                uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id     text,                                    -- unique per parent chat (subcollection doc id)
  chat_id           uuid NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
  sender_user_id    uuid REFERENCES users(id),
  sender_role       text NOT NULL CHECK (sender_role IN ('admin','driver')),
  text              text,
  image_file_id     uuid REFERENCES file_objects(id),
  message_type      text NOT NULL DEFAULT 'normal' CHECK (message_type IN ('normal','broadcast')),
  client_message_id uuid,                                    -- clientMessageId of POST /v1/chats/{id}/messages and
                                                             -- POST /v1/mobile/chat/messages; offline replay dedupe (R63)
  created_at        timestamptz NOT NULL DEFAULT now(),
  CHECK (text IS NOT NULL OR image_file_id IS NOT NULL)
);
CREATE UNIQUE INDEX chat_messages_legacy    ON chat_messages (chat_id, legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE UNIQUE INDEX chat_messages_client_id ON chat_messages (chat_id, client_message_id)
  WHERE client_message_id IS NOT NULL;                       -- unique per chat (R63)
CREATE INDEX chat_messages_chat_created     ON chat_messages (chat_id, created_at, id);   -- keyset both directions

CREATE TABLE chat_read_state (                               -- lastReadByAdmin{uid: ts} + lastReadByDriver
  chat_id      uuid NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
  user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  last_read_at timestamptz NOT NULL,
  PRIMARY KEY (chat_id, user_id)
);

CREATE TABLE broadcasts (
  id                 uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id      text,
  tenant_id          uuid REFERENCES tenants(id),             -- NULL = platform-wide (R12)
  created_by_user_id uuid REFERENCES users(id),
  created_by_name    text,
  title              text,                                    -- missing on legacy docs
  message_text       text NOT NULL,
  recipient_group    text NOT NULL DEFAULT 'all_driver',
  recipient_count    int  NOT NULL DEFAULT 0 CHECK (recipient_count >= 0),
  sent_at            timestamptz NOT NULL DEFAULT now(),
  voided_at          timestamptz,                             -- DELETE /v1/broadcasts/{id} is a soft delete (R14)
  voided_by          uuid REFERENCES users(id),
  created_at         timestamptz NOT NULL DEFAULT now(),
  CHECK (voided_by IS NULL OR voided_at IS NOT NULL)
);
CREATE UNIQUE INDEX broadcasts_legacy ON broadcasts (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX broadcasts_sent          ON broadcasts (sent_at DESC, id DESC) WHERE voided_at IS NULL;
CREATE INDEX broadcasts_tenant_sent   ON broadcasts (tenant_id, sent_at DESC) WHERE voided_at IS NULL;

CREATE TABLE broadcast_recipients (                          -- persisted from cut-over; legacy never stored recipients
  broadcast_id uuid NOT NULL REFERENCES broadcasts(id) ON DELETE CASCADE,
  user_id      uuid NOT NULL REFERENCES users(id),
  PRIMARY KEY (broadcast_id, user_id)
);
CREATE INDEX broadcast_recipients_user ON broadcast_recipients (user_id);

CREATE TABLE broadcast_reads (                               -- readDriverAuthIds[]; readCount is derived (COUNT)
  broadcast_id uuid NOT NULL REFERENCES broadcasts(id) ON DELETE CASCADE,
  user_id      uuid NOT NULL REFERENCES users(id),
  read_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (broadcast_id, user_id)
);
CREATE INDEX broadcast_reads_user ON broadcast_reads (user_id, read_at DESC);

CREATE TABLE notification_deliveries (                       -- FCM push log; exempt from RLS (Appendix C §C.3.0)
  id              uuid PRIMARY KEY DEFAULT uuidv7(),
  outbox_event_id bigint,                                    -- outbox_events.id that caused the push (no FK: outbox is pruned)
  user_id         uuid REFERENCES users(id) ON DELETE SET NULL,
  install_id      text,                                      -- device_tokens key at send time (no FK: tokens are deleted on UNREGISTERED)
  kind            text NOT NULL CHECK (kind IN ('task_assigned','task_unassigned','task_cancelled','tasks_changed',
                                                'maintenance_scheduled','chat','broadcast','leave_decided',
                                                'session_revoked')),   -- session_revoked: silent push (R50)
  payload         jsonb NOT NULL,                            -- FCM data map as sent (data.type contract unchanged)
  status          text NOT NULL DEFAULT 'queued'
                  CHECK (status IN ('queued','sent','failed','token_invalid','deduplicated')),
  error           text,
  created_at      timestamptz NOT NULL DEFAULT now(),
  sent_at         timestamptz
);
CREATE INDEX notification_deliveries_user    ON notification_deliveries (user_id, created_at DESC);
CREATE INDEX notification_deliveries_created ON notification_deliveries USING brin (created_at);

-- Row level security: Appendix C §C.3.0 marks these seven tables "yes"; notification_deliveries is exempt
-- (no ENABLE, explicit grants in 0009).
ALTER TABLE device_tokens        ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- parent-scoped: users (self)
ALTER TABLE chats                ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- tenant
ALTER TABLE chat_messages        ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- parent-scoped: chats
ALTER TABLE chat_read_state      ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- parent-scoped: chats
ALTER TABLE broadcasts           ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- nullable-tenant
ALTER TABLE broadcast_recipients ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- parent-scoped: broadcasts
ALTER TABLE broadcast_reads      ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- parent-scoped: broadcasts
-- Appended here, in this file: the Appendix C §C.3.5 "0007_comms" block (generator CALLs and the device-token, chat
-- and broadcast policies).

-- +goose Down
DROP TABLE notification_deliveries;
DROP TABLE broadcast_reads;
DROP TABLE broadcast_recipients;
DROP TABLE broadcasts;
DROP TABLE chat_read_state;
DROP TABLE chat_messages;
DROP TABLE chats;
DROP TABLE device_tokens;
```

**Notes (0007)**

- **`device_tokens` (R4).** PK `(user_id, install_id)`, `token` UNIQUE, `driver_id` nullable. `PUT /v1/me/devices` (web) and `PUT /v1/mobile/me/devices` (APK) upsert `{installId, token, platform, appFlavor}` on the PK (Appendix B §B.2.3, §B.2.21); a token already held by another `(user_id, install_id)` (shared phone) is deleted in the same transaction, so the UNIQUE index never fails. `POST /v1/auth/logout` deletes the install's row and `notify.fcm` deletes it on FCM `UNREGISTERED` (Appendix B §B.5.3). Today two stores exist: `drivers/{id}.fcmToken` for task pushes (`driver_repository.dart:73-83`, read by `triggers.ts:279-293`) and `users/{uid}.fcmTokens` with the fixed key `app` for chat and broadcast (`fcm_service.dart:28-50`, read by `chat.ts:13-27`; integrations report "FCM"). ETL rule: §A.3.1.
- **Chats.** Legacy `chats.driverId` is the auth uid; PostgreSQL references `drivers.id`, and the recipient user is always `drivers.user_id`, so a re-link (`PUT /v1/users/{id}/driver-link`) leaves no stale user on the chat. "One open chat per driver" is `chats_one_open_per_driver ON chats (driver_id) WHERE status <> 'closed'` (R14), created by `0010` after the duplicate report is signed off (§A.4 D5, R59); `POST /v1/chats/{id}/reopen` answers `409 already_exists` while another non-closed chat exists.
- **Chat messages** are keyset-paginated on `(created_at, id)` (W9, 50 per page), replacing the unbounded listeners (`room/page.tsx:89-91`, `chat_repository.dart:87-94`). `client_message_id` (body `clientMessageId`) is unique per chat (R63): the durable replay key of the offline outbox beyond `IDEMPOTENCY_TTL` (R53), and the id the web uses to match its optimistic message with the SSE `message.created` echo on `chat:{chatId}`.
- **Read state** is a row per `(chat, user)`, written only by `POST /v1/chats/{id}/read` (fixes the write inside the snapshot callback, `chat/room/page.tsx:108-113`, Appendix E).
- **Broadcasts** are soft-deleted (`voided_at`, `DELETE /v1/broadcasts/{id}`, R14); recipients are persisted at send time, so `GET /v1/mobile/broadcasts` is a join; `broadcast_reads` replaces `readDriverAuthIds[]` + `readCount` (`COUNT(*)`). Platform-wide rows (`tenant_id` NULL) are written by stewards only (R60).
- **`notification_deliveries`** is the push log written by `notify.fcm` under `db.WithSystem` (R12) and read by platform status pages; exempt from RLS (Appendix C §C.3.0), `logitrack_app` holds `SELECT, INSERT, UPDATE` (0009). `tasks_changed` is the silent push bound to `task.updated|checked_in|plan_date_changed`, deduplicated per driver for 30 s with `idem:push:tasks_changed:{driverId}` (R21; a suppressed push logs `deduplicated`); `session_revoked` is the silent push bound to `user.sessions_revoked`, sent only for session-ending reasons to the `device_tokens` rows whose `install_id` equals `sessions.install_id` of a revoked session of that user (R50, R83, R84).

### A.2.7 Platform (0008_platform.sql)

Tables: `settings`, `mobile_app_releases`, `security_events`, `mobile_installations`, `holidays`, `leave_requests`, `leave_request_attachments`, `waitlist`, `partner_interest`, `fuel_daily_snapshots`, `fuel_monthly_snapshots`; function `yyyy_mm(date)`. Appendix C §C.3.0: `security_events`, `mobile_installations`, `holidays`, `leave_requests` and `leave_request_attachments` are RLS tables; the other six are exempt (service layer only).

```sql
-- 0008_platform.sql
-- Depends on: 0001 (trg_set_updated_at, trg_forbid_mutation, app_* GUC readers, app_rls_* generators),
--             0002 (tenants, users, file_objects), 0003 (drivers). No GRANT/REVOKE here (R66).
-- RLS (Appendix C §C.3.0): security_events, mobile_installations, holidays, leave_requests, leave_request_attachments.
-- Exempt (service layer only, grants in 0009): settings, mobile_app_releases, waitlist, partner_interest,
-- fuel_daily_snapshots, fuel_monthly_snapshots.

-- +goose Up

-- 'YYYY-MM' of a date. to_char() is STABLE, so it cannot back a generated column; this is integer arithmetic only.
CREATE FUNCTION yyyy_mm(d date) RETURNS text
  LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
  RETURN lpad(extract(year FROM d)::int::text, 4, '0') || '-' || lpad(extract(month FROM d)::int::text, 2, '0');

CREATE TABLE settings (                                      -- key/value platform settings (R7: no platform_settings table)
  key        text PRIMARY KEY CHECK (key ~ '^[a-z][a-z0-9_]*$'),   -- 'mobile_app', 'distances_last_calculated'
  value      jsonb NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now(),
  updated_by uuid REFERENCES users(id)
);
CREATE TRIGGER settings_updated_at BEFORE UPDATE ON settings FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();

CREATE TABLE mobile_app_releases (                           -- script-owned half of settings/mobile_app (ADR 0007), one row per release
  id             uuid PRIMARY KEY DEFAULT uuidv7(),
  flavor         text NOT NULL CHECK (flavor IN ('dev','prod')),
  version        text NOT NULL CHECK (version ~ '^[0-9]+\.[0-9]+\.[0-9]+$'),   -- strict semver, no label (pub_semver gate)
  build_number   text NOT NULL CHECK (build_number ~ '^[0-9]+$'),
  apk_file_id    uuid NOT NULL REFERENCES file_objects(id),  -- object in the public bucket under app_releases/
  apk_size_bytes bigint NOT NULL CHECK (apk_size_bytes > 0),
  apk_sha256     text NOT NULL CHECK (apk_sha256 ~ '^[0-9a-f]{64}$'),
  release_notes  text,
  released_at    timestamptz NOT NULL,
  released_by    text NOT NULL,                              -- api_keys name (scope release_publisher, R82) or user email
  created_at     timestamptz NOT NULL DEFAULT now(),
  UNIQUE (flavor, version)
);
CREATE INDEX mobile_app_releases_latest ON mobile_app_releases (flavor, released_at DESC);

CREATE TABLE security_events (                               -- append-only audit log
  id               uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id    text,
  created_at       timestamptz NOT NULL DEFAULT now(),
  event_type       text NOT NULL CHECK (event_type ~ '^[a-z][a-z0-9_]*$'),
  severity         text NOT NULL DEFAULT 'info' CHECK (severity IN ('info','warning','critical')),
  summary          text NOT NULL,
  details          jsonb NOT NULL DEFAULT '{}',
  actor_user_id    uuid REFERENCES users(id),
  actor_email      text,
  actor_legacy_uid text,                                     -- legacy actorUid that no users row matched
  target_user_id   uuid REFERENCES users(id),
  tenant_id        uuid REFERENCES tenants(id),              -- NULL = platform-level event (R12)
  request_id       text
);
CREATE UNIQUE INDEX security_events_legacy ON security_events (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX security_events_feed          ON security_events (created_at DESC, id DESC);
CREATE INDEX security_events_type_created  ON security_events (event_type, created_at DESC);
CREATE INDEX security_events_tenant        ON security_events (tenant_id, created_at DESC) WHERE tenant_id IS NOT NULL;
CREATE INDEX security_events_actor         ON security_events (actor_user_id, created_at DESC) WHERE actor_user_id IS NOT NULL;
CREATE INDEX security_events_created_brin  ON security_events USING brin (created_at);
CREATE TRIGGER security_events_immutable BEFORE UPDATE OR DELETE ON security_events
  FOR EACH ROW EXECUTE FUNCTION trg_forbid_mutation();

CREATE TABLE mobile_installations (                          -- drivers/{id}/mobile_installations/{installId} flattened
  driver_id     uuid NOT NULL REFERENCES drivers(id),
  install_id    text NOT NULL,
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  tenant_source text NOT NULL DEFAULT 'driver'
                CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  platform      text NOT NULL CHECK (platform IN ('ios','android','other')),
  app_version   text,
  build_number  text,
  flavor        text NOT NULL DEFAULT 'dev' CHECK (flavor IN ('dev','prod')),
  first_seen_at timestamptz NOT NULL DEFAULT now(),
  last_seen_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (driver_id, install_id)                        -- R33: a guessed install id cannot overwrite another driver's row
);
CREATE INDEX mobile_installations_seen        ON mobile_installations (last_seen_at DESC, install_id);
CREATE INDEX mobile_installations_tenant_seen ON mobile_installations (tenant_id, last_seen_at DESC);
CREATE INDEX mobile_installations_install     ON mobile_installations (install_id);

CREATE TABLE holidays (
  id             uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id  text,
  tenant_id      uuid REFERENCES tenants(id),                -- NULL = public holiday for every tenant (R12)
  holiday_date   date NOT NULL,
  holiday_type   text NOT NULL DEFAULT 'public' CHECK (holiday_type IN ('public','company','other')),
  status         text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','published','archived')),
  name           text NOT NULL,
  name_en        text,
  name_th        text,
  description    text,
  description_en text,
  description_th text,
  is_recurring   boolean NOT NULL DEFAULT false,
  created_by     uuid REFERENCES users(id),
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  CHECK ((holiday_type = 'public') = (tenant_id IS NULL)),
  UNIQUE NULLS NOT DISTINCT (tenant_id, holiday_date, holiday_type)   -- upsert key date|type (holidays.ts:98-113)
);
CREATE UNIQUE INDEX holidays_legacy ON holidays (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX holidays_date          ON holidays (holiday_date);
CREATE TRIGGER holidays_updated_at BEFORE UPDATE ON holidays FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();

CREATE TABLE leave_requests (
  id                uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id     text,
  tenant_id         uuid NOT NULL REFERENCES tenants(id),
  tenant_source     text NOT NULL DEFAULT 'driver'
                    CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  driver_id         uuid REFERENCES drivers(id),
  legacy_driver_ref text,
  driver_ref_match  text CHECK (driver_ref_match IN ('doc_id','auth_uid','none')),
  leave_type        text NOT NULL DEFAULT 'sick' CHECK (leave_type IN ('sick','business')),
  status            text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected','cancelled')),
  start_date        date NOT NULL,
  end_date          date NOT NULL,
  reason            text NOT NULL DEFAULT '',
  approver_user_id  uuid REFERENCES users(id),
  decided_at        timestamptz,                             -- legacy approvedAt (also set on rejected)
  rejection_reason  text,
  client_op_id      uuid,                                    -- offline outbox op id, body `clientOpId` (R63)
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  CHECK (end_date >= start_date),
  CHECK (driver_id IS NOT NULL OR tenant_source = 'quarantine')
);
CREATE UNIQUE INDEX leave_requests_legacy    ON leave_requests (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE UNIQUE INDEX leave_requests_client_op ON leave_requests (driver_id, client_op_id)
  WHERE client_op_id IS NOT NULL;                            -- durable offline replay key (R63)
CREATE INDEX leave_requests_driver        ON leave_requests (driver_id, created_at DESC);
CREATE INDEX leave_requests_tenant        ON leave_requests (tenant_id, status, created_at DESC, id DESC);
CREATE TRIGGER leave_requests_updated_at BEFORE UPDATE ON leave_requests FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();

CREATE TABLE leave_request_attachments (
  leave_request_id uuid NOT NULL REFERENCES leave_requests(id) ON DELETE CASCADE,
  file_id          uuid NOT NULL REFERENCES file_objects(id),
  position         int NOT NULL DEFAULT 0,
  PRIMARY KEY (leave_request_id, file_id)
);

CREATE TABLE waitlist (                                      -- anonymous landing form (exempt, R44, R77)
  id            uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id text,
  email         citext NOT NULL,
  name          text NOT NULL,
  country_code  text NOT NULL DEFAULT '+66',
  phone         text,
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX waitlist_legacy ON waitlist (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX waitlist_created       ON waitlist (created_at DESC, id DESC);
CREATE INDEX waitlist_email         ON waitlist (email);

CREATE TABLE partner_interest (                              -- collection 'partner-interest' (exempt, R44, R77)
  id            uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id text,
  payload       jsonb NOT NULL,                              -- UNVERIFIED: field set not in COLLECTIONS or any Zod schema
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX partner_interest_legacy ON partner_interest (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX partner_interest_created       ON partner_interest (created_at DESC, id DESC);

CREATE TABLE fuel_daily_snapshots (                          -- create-only observation (ADR 0009 §5); insert-only
  day_key     date PRIMARY KEY,
  month_key   text GENERATED ALWAYS AS (yyyy_mm(day_key)) STORED,
  captured_at timestamptz NOT NULL,
  fetched_at  timestamptz,
  source      text NOT NULL,
  locale      text NOT NULL DEFAULT 'th' CHECK (locale IN ('th','en')),
  status      text NOT NULL DEFAULT 'ok' CHECK (status = 'ok'),
  items       jsonb NOT NULL                                 -- [{nameTh, nameEn, price, unit}]
);
CREATE INDEX fuel_daily_snapshots_month ON fuel_daily_snapshots (month_key, day_key DESC);
CREATE TRIGGER fuel_daily_snapshots_immutable BEFORE UPDATE OR DELETE ON fuel_daily_snapshots
  FOR EACH ROW EXECUTE FUNCTION trg_forbid_mutation();

CREATE TABLE fuel_monthly_snapshots (                        -- upserted only on a successful fetch; not a billing input
  month_key     text PRIMARY KEY CHECK (month_key ~ '^[0-9]{4}-[0-9]{2}$'),
  captured_at   timestamptz NOT NULL,
  fetched_at    timestamptz,
  source        text NOT NULL,
  locale        text NOT NULL DEFAULT 'th' CHECK (locale IN ('th','en')),
  status        text NOT NULL CHECK (status IN ('ok','error')),   -- 'error' only on legacy rows
  error_message text,
  items         jsonb NOT NULL DEFAULT '[]',
  updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE TRIGGER fuel_monthly_snapshots_updated_at BEFORE UPDATE ON fuel_monthly_snapshots
  FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();

-- Row level security: Appendix C §C.3.0 marks these five tables "yes"; the other six tables of this file are exempt
-- (no ENABLE; exact grants in 0009).
ALTER TABLE security_events           ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- nullable-tenant
ALTER TABLE mobile_installations      ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- tenant
ALTER TABLE holidays                  ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- nullable-tenant
ALTER TABLE leave_requests            ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- tenant
ALTER TABLE leave_request_attachments ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- parent-scoped: leave_requests
-- Appended here, in this file: the Appendix C §C.3.5 "0008_platform" block (generator CALLs and the security-event,
-- holiday and leave-request policies).

-- +goose Down
DROP TABLE fuel_monthly_snapshots;
DROP TABLE fuel_daily_snapshots;
DROP TABLE partner_interest;
DROP TABLE waitlist;
DROP TABLE leave_request_attachments;
DROP TABLE leave_requests;
DROP TABLE holidays;
DROP TABLE mobile_installations;
DROP TABLE security_events;
DROP TABLE mobile_app_releases;
DROP TABLE settings;
DROP FUNCTION yyyy_mm(date);
```

**Notes (0008)**

- **`settings` vs `mobile_app_releases`.** `settings/mobile_app` has two writers today (ADR 0007, CLAUDE.md #46): only the web writes `minAllowedVersion*` (`logitrack-web/features/mobile-release/api/mobileAppSettings.ts:64-96`); the publish script writes the release fields (`latestVersion`, `latestBuildNumber`, `apkSizeBytes`, `apkSha256`, `flavor`, `releasedAt`, `releasedBy`, `releaseNotes`) and never the floor (`logitrack-web/scripts/publish-mobile-release.mjs:251-264`); both set `apkDownloadUrl` (`mobileAppSettings.ts:69`, `publish-mobile-release.mjs:253`). In PostgreSQL each release is an immutable `mobile_app_releases` row whose APK is a public `file_objects` row (`purpose='apk'`, bucket `S3_PUBLIC_BUCKET`, key `app_releases/{flavor}/logitrack-{flavor}-v{version}.apk`, Appendix B §B.2.18). `settings('mobile_app')` keeps the floor half, written only by `PUT /v1/app-releases/floor` (user principals only; refused while the flavor has no published APK URL — a service rule, not a constraint), and the release half (latest version, build, download URL), written only by `POST /v1/app-releases` from the release CLI on the private network (R43). Drivers read both through the anonymous, rate-limited `GET /v1/mobile/settings` (R42). Keys in use: `mobile_app`, `distances_last_calculated`. The own fleet is a `tenants` row, never a settings key (R7).
- **`security_events` is append-only** (trigger + 0009 REVOKE). Only `internal/security` writes it, under `db.WithSystem`: privilege-changing events in the same transaction as the change (e.g. `refresh_token_reuse`, `user_role_changed`, `user_password_temporary_issued`); `platform_cross_tenant_access` in its own transaction, committed before the cross-tenant handler starts, because the read-only `X-Act-On-Tenant: *` transaction cannot insert (Appendix C §C.3.9); informational ones (`login_failed`, `login_lockout`, `password_reset_requested`, `tenant_orphans_detected`) through the `security.audit` consumer, which binds `security.event` only (no `user.*` / `auth.*` keys, R22). Event names and placement are owned by Appendix C §C.4.13 (R85); Appendix B §B.5.7 copies that split. `tenant_id` is the target tenant or NULL. Legacy types (`user_role_changed`, `user_created`, `user_disabled`, `user_enabled`, `user_sessions_revoked`, `role_matrix_saved`, `trip_record_renamed`) satisfy the snake_case CHECK.
- **`mobile_installations`** PK `(driver_id, install_id)` (R33). `POST /v1/mobile/heartbeat` derives `driver_id` and `tenant_id` from the token, so the legacy `partnerId` is not stored and the auth-uid-vs-doc-id no-op of `mobile_client_heartbeat_service.dart:34-85` disappears.
- **Holidays.** `holiday_type='public'` ⇔ `tenant_id IS NULL` (CHECK; PUBLIC rows written by stewards, R60); `UNIQUE NULLS NOT DISTINCT (tenant_id, holiday_date, holiday_type)` is the `saveHoliday` upsert key (`functions/src/holidays.ts:98-113`). `holiday_date` is a Bangkok date. Holiday and leave statuses are lower_snake (R30, R65); the legacy upper-case literals come only from the coexistence translation table (R25).
- **Leave requests.** Legacy `driverId` is inferred to be the drivers doc id (rules check `drivers/{driverId}.authId == uid`, `firestore.rules:439-443`; UNVERIFIED for every row, schemas report "UNCERTAIN"), so ETL tries the doc id first, then the auth uid (§A.3.10). `client_op_id` (body `clientOpId` of `POST /v1/mobile/leave-requests`) with `leave_requests_client_op` is the durable offline replay key (R63); staff and legacy rows leave it NULL.
- **Fuel snapshots** are written by the `bangchak.snapshot` job in Go from P3 (R70, Appendix B §B.5.6). Daily rows are insert-only (trigger + REVOKE); the monthly row is upserted only after a successful fetch, fixing the error overwrite of `functions/src/core/persistFuelMonthlySnapshot.ts:88-96` (read in the repo). `month_key` is STORED because PostgreSQL 18 defaults generated columns to VIRTUAL (R34) and `to_char` is not IMMUTABLE.
- **Waitlist / partner interest.** The marketing pages post to the unauthenticated BFF handlers `POST /api/forms/waitlist` and `POST /api/forms/partner-interest`, which call Go `POST /v1/waitlist` and `POST /v1/partner-interest` on the **internal** listener (`RATE_LIMIT_PUBLIC_FORMS`, honeypot; R44, R77, Appendix B §B.2.19), written under `db.WithSystem`. Reads need `waitlist:view` plus the steward rule (R60), checked by the service because both tables are exempt from RLS.

### A.2.8 Infra (0009_infra.sql)

Tables: `outbox_events`, `consumer_inbox`, `idempotency_keys`, `jobs` (schema `public`) and the ETL bookkeeping tables `etl.source_docs`, `etl.quarantine`, `etl.watermarks`, `etl.reconciliation_runs`. All eight are exempt from RLS ([Appendix C](./C-auth-rbac.md) §C.3.0): the service named in the coverage table is their only access path. This file is also the **single grant site** of the baseline (R66): every `GRANT` / `REVOKE` for the tables and views of 0002–0009, the `logitrack_readonly` grants, and the ownership hand-over of the SECURITY DEFINER functions to `logitrack_rls_definer`. It creates no role: the five roles come from `deploy/postgres-init/00-roles.sql` (Appendix C §C.3.2), and `0001` only asserts them. `file_objects` is not created here: it lives in `0002_identity.sql` (R1) so that the `*_file_id` foreign keys of 0002–0008 resolve.

```sql
-- 0009_infra.sql
-- Depends on: 0001 (schema etl, trg_outbox_notify, role assertion), 0002-0008 (every object granted below).
-- Roles come from the superuser script deploy/postgres-init/00-roles.sql (R66); no migration creates or alters a role.
-- Single grant site (R66): 0001-0008 contain no GRANT/REVOKE; a later migration (0011+) grants its own new tables.
-- Ids: uuid DEFAULT uuidv7(); the only identity column of the schema is outbox_events.id (R57).

-- +goose Up

CREATE TABLE outbox_events (                                 -- transactional outbox -> RabbitMQ + Redis realtime (relay in `scheduler`)
  id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,   -- relay order; AMQP message_id = id::text (R57)
  event_id        uuid NOT NULL DEFAULT uuidv7(),            -- stable envelope id exposed to consumers and SSE payloads
  exchange        text NOT NULL DEFAULT 'lt.events' CHECK (exchange IN ('lt.events','lt.jobs')),   -- relay targets only
  routing_key     text NOT NULL,                             -- 'trip.delivered', 'job.billing.backfill-trips', ...
  aggregate_type  text NOT NULL,                             -- 'trip','task','standby','chat','statement','hub','settings',...
  aggregate_id    text NOT NULL,                             -- text: uuid, settings key, fuel day_key, job id (R14)
  event_type      text NOT NULL,
  tenant_id       uuid REFERENCES tenants(id),               -- NULL = platform-level event (R12)
  payload         jsonb NOT NULL,
  headers         jsonb NOT NULL DEFAULT '{}',               -- traceparent, actor, idempotency key
  realtime_topics text[] NOT NULL DEFAULT '{}',              -- SSE topics the relay XADDs / PUBLISHes (Appendix B §B.4)
  created_at      timestamptz NOT NULL DEFAULT now(),
  published_at    timestamptz,
  attempts        int NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  last_error      text
);
CREATE UNIQUE INDEX outbox_events_event_id  ON outbox_events (event_id);
CREATE INDEX outbox_events_unpublished      ON outbox_events (id) WHERE published_at IS NULL;
CREATE INDEX outbox_events_aggregate        ON outbox_events (aggregate_type, aggregate_id, id);
CREATE INDEX outbox_events_published        ON outbox_events (published_at) WHERE published_at IS NOT NULL;   -- outbox.prune
CREATE TRIGGER outbox_events_notify AFTER INSERT ON outbox_events
  FOR EACH ROW EXECUTE FUNCTION trg_outbox_notify();

CREATE TABLE consumer_inbox (                                -- idempotent consumers: insert in the same tx as the side effect (R14, R17)
  consumer     text NOT NULL,                                -- queue name, e.g. 'billing.compute'
  message_id   text NOT NULL,                                -- AMQP message_id = outbox_events.id::text (events and jobs alike)
  processed_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (consumer, message_id)
);
CREATE INDEX consumer_inbox_processed ON consumer_inbox (processed_at);

CREATE TABLE idempotency_keys (                              -- durable copy of Redis lt:{APP_ENV}:idem:http:* (R53)
  scope         text NOT NULL,                               -- principal user id: key scope (user_id, key), Appendix B §B.1.5
  key           text NOT NULL,                               -- Idempotency-Key header (uuid)
  request_hash  text NOT NULL,                               -- sha256(method + path + body)
  status        text NOT NULL CHECK (status IN ('in_progress','completed')),
  response_code int,
  response_body jsonb,
  created_at    timestamptz NOT NULL DEFAULT now(),
  expires_at    timestamptz NOT NULL,                        -- created_at + IDEMPOTENCY_TTL (default 168h)
  PRIMARY KEY (scope, key),
  CHECK (status <> 'completed' OR response_code IS NOT NULL),
  CHECK (expires_at > created_at)
);
CREATE INDEX idempotency_keys_expires ON idempotency_keys (expires_at);   -- idempotency.prune

CREATE TABLE jobs (                                          -- long-running admin and scheduler jobs (R14, R64)
  id            uuid PRIMARY KEY DEFAULT uuidv7(),
  type          text NOT NULL CHECK (type ~ '^[a-z]+(\.[a-z0-9-]+)+$'),   -- 'billing.backfill-trips', 'payroll.run', ...
  status        text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','succeeded','failed')),
  owner_user_id uuid REFERENCES users(id),                   -- NULL = enqueued by the scheduler
  tenant_id     uuid REFERENCES tenants(id),                 -- NULL = platform-wide job
  params        jsonb NOT NULL DEFAULT '{}',                 -- input parameters as accepted at enqueue (R64)
  progress      jsonb NOT NULL DEFAULT '{}',                 -- {"done":n,"total":m}
  result        jsonb,                                       -- job-specific summary
  error         text,
  created_at    timestamptz NOT NULL DEFAULT now(),
  started_at    timestamptz,
  finished_at   timestamptz,
  CHECK (jsonb_typeof(params) = 'object'),
  CHECK (finished_at IS NULL OR status IN ('succeeded','failed')),
  CHECK (status <> 'running' OR started_at IS NOT NULL)
);
CREATE INDEX jobs_owner_created ON jobs (owner_user_id, created_at DESC, id DESC);
CREATE INDEX jobs_type_created  ON jobs (type, created_at DESC, id DESC);
CREATE INDEX jobs_tenant_created ON jobs (tenant_id, created_at DESC, id DESC) WHERE tenant_id IS NOT NULL;
CREATE INDEX jobs_active        ON jobs (type) WHERE status IN ('queued','running');

-- ETL bookkeeping (schema etl from 0001; owned by logitrack_migrator like every object; DML for logitrack_etl,
-- SELECT for logitrack_readonly, nothing for logitrack_app)
CREATE TABLE etl.source_docs (                               -- every dumped Firestore doc verbatim; also the legacy-id -> row map
  collection         text NOT NULL,
  doc_path           text NOT NULL,                          -- full path incl. parents (drivers/{id}/mobile_installations/{iid})
  doc_id             text NOT NULL,
  raw                jsonb NOT NULL,                         -- type-tagged NDJSON fields ({"$ts":..}, {"$geo":..}, {"$ref":..})
  source_create_time timestamptz,                            -- Firestore createTime (trip created_at fallback, R68)
  source_update_time timestamptz NOT NULL,                   -- Firestore updateTime; loads apply only when newer
  exported_at        timestamptz NOT NULL,
  imported_at        timestamptz,
  target_table       text,
  target_id          text,                                   -- uuid, or composite key 'driver_id/install_id' for composite-PK tables
  status             text NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending','loaded','quarantined','rejected','dropped')),
  PRIMARY KEY (collection, doc_path)
);
CREATE INDEX etl_source_docs_status ON etl.source_docs (status, collection);
CREATE INDEX etl_source_docs_target ON etl.source_docs (target_table, target_id);

CREATE TABLE etl.quarantine (                                -- field- and row-level findings (§A.3.0)
  id           uuid PRIMARY KEY DEFAULT uuidv7(),
  collection   text NOT NULL,
  doc_path     text NOT NULL,
  field        text,                                         -- NULL = whole-row finding
  reason_code  text NOT NULL CHECK (reason_code IN (         -- the canonical list of §A.3.0 (R68); synonyms never written
                 'tenant_unresolved','driver_unresolved','user_unresolved','truck_unresolved','task_unresolved',
                 'task_ambiguous','trip_unresolved','party_unresolved','customer_unresolved','hub_unresolved',
                 'tenant_mismatch','helper_overflow','helper_tenant_mismatch','missing_required',
                 'bad_timestamp','bad_number','negative_money','status_out_of_vocab','vehicle_class_out_of_enum',
                 'photo_type_unknown','file_missing_at_source','url_unparseable',
                 'duplicate_natural_key','duplicate_service_fee','duplicate_hub_source_id',
                 'created_at_derived','missing_delivered_at','missing_billing_date','missing_ended_at',
                 'billing_date_locked','trip_no_mismatch','multidrop_priced_as_single','link_blanket_default',
                 'legacy_standby_trip','unknown_capability_key','unknown_collection')),
  detail       text,
  raw_value    jsonb,
  created_at   timestamptz NOT NULL DEFAULT now(),
  resolved_at  timestamptz,
  resolved_by  text,
  resolution   text CHECK (resolution IN ('retried','accepted','rehomed','skipped','fixed_at_source')),
  CHECK ((resolved_at IS NULL) = (resolution IS NULL))
);
CREATE INDEX etl_quarantine_open ON etl.quarantine (reason_code, collection) WHERE resolved_at IS NULL;
CREATE INDEX etl_quarantine_doc  ON etl.quarantine (collection, doc_path);

CREATE TABLE etl.watermarks (                                -- FS->PG mirror / etl.sync position per collection
  collection       text PRIMARY KEY,
  last_update_time timestamptz NOT NULL,
  last_doc_path    text,                                     -- tie-break inside one updateTime (collection-group safe)
  updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE etl.reconciliation_runs (                       -- `etl reconcile` results (counts + money sums)
  id          uuid PRIMARY KEY DEFAULT uuidv7(),
  period      text CHECK (period ~ '^[0-9]{4}-[0-9]{2}$'),
  started_at  timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz,
  outcome     text CHECK (outcome IN ('match','mismatch','error')),
  report      jsonb NOT NULL DEFAULT '{}'
);

-- ===== Grants: the single grant site of the baseline (R66; exact privileges of Appendix C §C.3.2) =====

-- Guard (R67): 70 tables with ENABLE + FORCE and exactly the 11 exempt tables without RLS (Appendix C §C.3.0).
-- goose's own goose_db_version (public, no RLS, §A.2.0 notes) is not an Appendix A table and is skipped by name.
-- +goose StatementBegin
DO $$
DECLARE
  exempt text[] := ARRAY['notification_deliveries','settings','mobile_app_releases','waitlist','partner_interest',
                         'fuel_daily_snapshots','fuel_monthly_snapshots','outbox_events','consumer_inbox',
                         'idempotency_keys','jobs'];
  n_rls  int;
  bad    text;
BEGIN
  SELECT count(*) INTO n_rls
    FROM pg_class c JOIN pg_namespace s ON s.oid = c.relnamespace
   WHERE s.nspname = 'public' AND c.relkind = 'r' AND c.relrowsecurity AND c.relforcerowsecurity;
  SELECT string_agg(c.relname::text, ', ' ORDER BY c.relname) INTO bad
    FROM pg_class c JOIN pg_namespace s ON s.oid = c.relnamespace
   WHERE s.nspname = 'public' AND c.relkind = 'r' AND c.relname <> 'goose_db_version'
     AND (c.relrowsecurity AND c.relforcerowsecurity) = (c.relname::text = ANY (exempt));
  IF n_rls <> 70 OR bad IS NOT NULL THEN
    RAISE EXCEPTION 'RLS layout differs from Appendix C §C.3.0: % tables with ENABLE + FORCE (want 70), mismatched: %',
      n_rls, coalesce(bad, 'none') USING ERRCODE = 'invalid_object_definition';
  END IF;
END
$$;
-- +goose StatementEnd

-- logitrack_app (api, worker, scheduler): DML on the RLS tables (policies decide the rows) except the two counters,
-- which only the SECURITY DEFINER allocators touch (R10, R67).
GRANT USAGE ON SCHEMA public TO logitrack_app;
-- +goose StatementBegin
DO $$
DECLARE t regclass;
BEGIN
  FOR t IN SELECT c.oid::regclass
             FROM pg_class c JOIN pg_namespace s ON s.oid = c.relnamespace
            WHERE s.nspname = 'public' AND c.relkind = 'r' AND c.relrowsecurity AND c.relforcerowsecurity
              AND c.relname::text NOT IN ('task_number_counters','billing_counters')
            ORDER BY c.relname
  LOOP
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %s TO logitrack_app', t);
  END LOOP;
END
$$;
-- +goose StatementEnd
-- Exempt tables: exactly the privileges listed in Appendix C §C.3.2, nothing else.
GRANT SELECT, INSERT, UPDATE, DELETE ON outbox_events, idempotency_keys, jobs       TO logitrack_app;   -- jobs: jobs.prune (T10)
GRANT SELECT, INSERT, DELETE         ON consumer_inbox, waitlist                    TO logitrack_app;
GRANT SELECT, INSERT, UPDATE         ON notification_deliveries, settings,
                                        fuel_monthly_snapshots                      TO logitrack_app;
GRANT SELECT, INSERT                 ON mobile_app_releases, partner_interest,
                                        fuel_daily_snapshots                        TO logitrack_app;
-- Projection views for dispatcher and customer-scope principals (security_invoker, Appendix C §C.3.7).
GRANT SELECT ON scope_tenants, scope_drivers, scope_trucks, scope_tasks, scope_trips, scope_standby, scope_incidents
  TO logitrack_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO logitrack_app;     -- the outbox_events identity (R57)
-- /startupz compares the applied goose version with the version the binary needs (Appendix B), so the API login
-- reads goose's bookkeeping table; goose creates it before 0001 and it is absent only outside goose.
-- +goose StatementBegin
DO $$ BEGIN
  IF to_regclass('public.goose_db_version') IS NOT NULL THEN
    GRANT SELECT ON public.goose_db_version TO logitrack_app;
  END IF;
END $$;
-- +goose StatementEnd

-- logitrack_etl (cmd/etl and cmd/seed writes, R87; BYPASSRLS): DML on both schemas; no TRUNCATE (skips row triggers).
GRANT USAGE ON SCHEMA public, etl TO logitrack_etl;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO logitrack_etl;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA etl    TO logitrack_etl;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public, etl TO logitrack_etl;
-- The blanket grant also reached goose's bookkeeping table: the ETL login must never rewrite migration state.
-- +goose StatementBegin
DO $$ BEGIN
  IF to_regclass('public.goose_db_version') IS NOT NULL THEN
    REVOKE INSERT, UPDATE, DELETE ON public.goose_db_version FROM logitrack_etl;
  END IF;
END $$;
-- +goose StatementEnd

-- logitrack_readonly (reporting tools, operator-held): SELECT on both schemas; RLS still applies (Appendix C §C.3.2).
GRANT USAGE ON SCHEMA public, etl TO logitrack_readonly;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO logitrack_readonly;
GRANT SELECT ON ALL TABLES IN SCHEMA etl    TO logitrack_readonly;

-- Append-only and void-only tables (§A.1.8): revoked after the grants above, from both writing roles.
REVOKE UPDATE, DELETE ON status_history, trip_no_history, billing_statement_lines, transactions,
                         security_events, fuel_daily_snapshots
  FROM logitrack_app, logitrack_etl;
REVOKE DELETE ON customer_rate_entries, customer_fuel_rate_adjustments, standby_rate_entries
  FROM logitrack_app, logitrack_etl;

-- SECURITY DEFINER functions (Appendix C §C.3.3, §C.3.6, §C.3.7; allocators R10, R61): ACL first, then ownership,
-- because logitrack_migrator holds logitrack_rls_definer WITH INHERIT FALSE and cannot change the ACL of a function
-- it no longer owns (Notes 0009). Functions the definer already owns are skipped (re-run after Down).
GRANT CREATE ON SCHEMA public TO logitrack_rls_definer;      -- ALTER ... OWNER TO: the new owner needs CREATE here
-- +goose StatementBegin
DO $$
DECLARE
  f       regprocedure;
  fname   name;
  f_owner oid;
BEGIN
  FOREACH f IN ARRAY ARRAY['app_task_stops_in_scope(uuid)', 'app_task_in_scope(uuid)', 'app_trip_in_scope(uuid)',
                           'app_recent_work_in_scope(uuid,uuid)', 'driver_directory()',
                           'trg_driver_link_membership()', 'next_task_seq(text,date)',
                           'next_invoice_seq(uuid,uuid,int,int,text)']::regprocedure[]
  LOOP
    SELECT p.proname, p.proowner INTO fname, f_owner FROM pg_proc p WHERE p.oid = f;
    CONTINUE WHEN f_owner = 'logitrack_rls_definer'::regrole::oid;
    EXECUTE format('REVOKE ALL ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO logitrack_app', f);
    IF starts_with(fname::text, 'app_') THEN   -- policy helpers: every role that evaluates policies (Notes 0009)
      EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO logitrack_readonly, pg_database_owner', f);
    ELSIF starts_with(fname::text, 'next_') THEN
      EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO logitrack_etl', f);   -- cmd/seed numbers tasks and invoices (R87)
    END IF;
    EXECUTE format('ALTER FUNCTION %s OWNER TO logitrack_rls_definer', f);
  END LOOP;
END
$$;
-- +goose StatementEnd
REVOKE CREATE ON SCHEMA public FROM logitrack_rls_definer;
-- What the definer functions read and write (logitrack_rls_definer is BYPASSRLS, so no policy applies to it).
GRANT SELECT ON tasks, task_delivery_stops, trip_records, drivers, memberships TO logitrack_rls_definer;
GRANT SELECT, INSERT, UPDATE ON task_number_counters, billing_counters TO logitrack_rls_definer;

-- +goose Down
-- Function ownership and ACLs stay with logitrack_rls_definer (the migrator cannot take ownership back under INHERIT
-- FALSE); the Downs of 0003-0005 drop those functions as their owner through app_drop_definer_function() (0001).
REVOKE ALL ON ALL TABLES    IN SCHEMA etl    FROM logitrack_etl, logitrack_readonly;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA etl    FROM logitrack_etl;
REVOKE ALL ON ALL TABLES    IN SCHEMA public FROM logitrack_app, logitrack_etl, logitrack_readonly, logitrack_rls_definer;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA public FROM logitrack_app, logitrack_etl;
REVOKE USAGE ON SCHEMA etl    FROM logitrack_etl, logitrack_readonly;
REVOKE USAGE ON SCHEMA public FROM logitrack_app, logitrack_etl, logitrack_readonly;
DROP TABLE etl.reconciliation_runs;
DROP TABLE etl.watermarks;
DROP TABLE etl.quarantine;
DROP TABLE etl.source_docs;
DROP TABLE jobs;
DROP TABLE idempotency_keys;
DROP TABLE consumer_inbox;
DROP TABLE outbox_events;
```

**Notes (0009)**

- **Outbox (R14, R57).** `aggregate_id` is `text` (settings keys, fuel `day_key`, job ids fit). The relay publishes only to `lt.events` (topic, `{aggregate}.{event}`) and `lt.jobs` (direct, `job.{type}`); `lt.retry`, `lt.requeue` and `lt.dlx` belong to the worker and broker retry ladder (R22, R54, Appendix B §B.5.1, §B.5.4). The relay is leader-only in `scheduler`: `LISTEN outbox_new` (trigger from 0001) plus a 200 ms tick, `SELECT … WHERE published_at IS NULL ORDER BY id LIMIT 500 FOR UPDATE SKIP LOCKED`, publish with confirms (AMQP `message_id` = `id`), then for non-empty `realtime_topics` one `INCR rtlog:seq`, `XADD rtlog:{topic}` with id `{seq}-0` and `PUBLISH rt:{topic}` (prefix `lt:{APP_ENV}:`; R52, Appendix B §B.4.4), then `published_at`. `outbox.prune` deletes rows older than 7 days (Appendix B §B.5.6). `id` is the only identity column on purpose (R57): it is the relay order and the per-trip high-water mark that `trip_billing_snapshots.last_event_id` stores without a foreign key.
- **`consumer_inbox` (R14, R17).** Inserted in the same transaction as the consumer's side effect; a redelivery hits the primary key and is acked. `billing.compute` also locks the trip row and drops messages whose `message_id` ≤ `trip_billing_snapshots.last_event_id` (Appendix B §B.5.3). Pruned after 30 days by the 04:00 housekeeping of `scheduler`, past the last retry delay (2 h) and the DLQ replay window.
- **`idempotency_keys` (R53).** Redis keeps a 24 h hot copy (`idem:http:{userId}:{key}`); this row lives until `expires_at = created_at + IDEMPOTENCY_TTL` (default 168h) and `idempotency.prune` deletes it afterwards. Scope `(user_id, key)` and fingerprint `sha256(method + path + body)` per Appendix B §B.1.5. Older replays are caught by the per-table `client_op_id` / `client_message_id` keys (R63). Usage (T09, `internal/platform/httpx/idempotency`, queries in `internal/platform/db/queries/idempotency.sql`): a request first inserts an `in_progress` row (the durable claim; a conflicting row is taken over only when it expired or is an `in_progress` row older than the 30 s lock, i.e. its holder died; the route runs under a 25 s deadline so a live holder never reaches that age, Appendix B §B.1.5), then sets `completed`, `response_code` and `response_body` for a `2xx` answer or deletes the claim for anything else; both statements match the claim's `created_at`, so a holder that was taken over cannot overwrite the new claim. `response_body` is `{"contentType", "body"}` with the body as verbatim text (`"bodyBase64"` instead for a non-UTF-8 body or one containing a NUL byte, since `jsonb` rejects `\u0000`): storing the JSON itself in `jsonb` would reorder its keys and break byte-identical replays. `idempotency.prune` deletes in batches, skips rows a claim is taking over (`FOR UPDATE SKIP LOCKED`) and rechecks `expires_at` on the row it deletes, so a key re-claimed during the prune keeps its claim.
- **`jobs` (R14, R64).** R14 columns plus `params jsonb NOT NULL DEFAULT '{}'`; `GET /v1/jobs/{id}` returns `{id, type, status, params, progress{done,total}, result?, error?, createdAt, startedAt, finishedAt}` (Appendix B §B.2.20). Job endpoints insert the row and an `lt.jobs` outbox row in one transaction and answer `202 {jobId}`; scheduled runs get `owner_user_id IS NULL`; `jobs.prune` deletes rows older than 30 days in the 04:00 housekeeping (Appendix B §B.5.6). Duplicate runs are stopped by the advisory lock plus Redis `lock:job:{type}:{scope}`. `tenant_id` is NULL for platform-wide jobs. Access is checked by the jobs service (exempt table): owner, staff of the job's tenant holding the job's capability, or platform (Appendix C §C.3.0).
- **Exempt tables and ETL bookkeeping (R67, R68).** No table of this file has RLS; workers use `db.WithSystem` (R12). `etl.source_docs` is the verbatim archive and the legacy-id map; loads apply only when `source_update_time` is newer, and `source_create_time` feeds the trip `created_at` fallback. `etl.quarantine.reason_code` is constrained to the canonical list of §A.3.0. `etl.watermarks` serves mirror restarts; `etl.reconciliation_runs` persists `etl reconcile` (main spec §13). Status `quarantined` = loaded into the quarantine tenant; `rejected` = not loaded, forbidden for rows with billing evidence (R19).
- **Grant section (R66, R67).** Runs after every table, view and function of 0002–0009 exists: (1) a guard that the RLS layout equals the Appendix C §C.3.0 coverage table, so a forgotten `ENABLE` / `FORCE` fails the migration (goose's `goose_db_version` is skipped by name, §A.2.0 notes; checked on `postgres:18-alpine`); (2) `logitrack_app`: DML on the RLS tables except the two counters, the exact Appendix C §C.3.2 privileges on the exempt tables, `SELECT` on the seven `scope_*` views and on goose's `goose_db_version` (the `/startupz` version gate, Appendix B; added in T04), nothing on `etl`; (3) `logitrack_etl`: DML on `public` and `etl` except writes to `goose_db_version`, and no `TRUNCATE`: Appendix C §C.3.2's "all table privileges" is narrowed here because `TRUNCATE` fires no row trigger and would empty the append-only tables; (4) `logitrack_readonly`: `SELECT` on both schemas; (5) the §A.1.8 REVOKEs, after the grants they restrict; (6) the SECURITY DEFINER hand-over. Role attributes live in `00-roles.sql`; each process logs in with its own URL (`DATABASE_URL`, `MIGRATE_DATABASE_URL`, `ETL_DATABASE_URL`) and never uses `SET ROLE` at run time.
- **SECURITY DEFINER hand-over.** ACL changes come before `ALTER FUNCTION … OWNER TO logitrack_rls_definer`: the migrator holds the definer role `WITH INHERIT FALSE`, so after the transfer a `REVOKE … FROM PUBLIC` only warns and revokes nothing, leaving the allocators callable by every login (verified on `postgres:18-alpine`). The transfer rewrites the grantor of earlier entries to the new owner but folds an entry naming the old owner into the owner's own, so the database owner `logitrack_migrator` keeps `EXECUTE` on the policy helpers through `pg_database_owner`; it needs it because `FORCE` RLS evaluates the policies for the owner too (a later `FOREIGN KEY` on `trucks` fails in its validation scan without it). `logitrack_readonly` gets the policy helpers, `logitrack_etl` the allocators (`cmd/seed`, R87). The definer reads `tasks`, `task_delivery_stops`, `trip_records`, `drivers`, `memberships` and writes the two counters, nothing else.
- **Migration policy (R31, R59).** Every file has a `Down`. `0001`–`0009` are applied together in P0 (issue T04), so no request runs before these grants exist; `0010` is `NO TRANSACTION` (§A.4 D5). CI runs `up → down → up` on an empty `postgres:18-alpine` database.

### A.2.S Renames and moves applied (part 2)

Against the earlier drafts for 0006–0009, the ETL mapping and D1–D8: data model (DM), auth (AU), runtime (RT), delivery plan (DP) and earlier drafts of this appendix ("A-draft"). Part 1 (0001–0005) is §A.2.R.

| Draft object | This appendix | Why |
|---|---|---|
| DM 0001 `CREATE ROLE … NOLOGIN` + `SET ROLE`; 0009 header "roles from 0001" | five roles from `deploy/postgres-init/00-roles.sql`, asserted by `0001`; one login and URL per process, no run-time `SET ROLE` | R66 |
| DM 0009 blanket `GRANT … ON ALL TABLES` to `logitrack_app`, `GRANT ALL ON SCHEMA etl`; REVOKEs in 0006/0008 before it | single grant site with the Appendix C §C.3.2 privileges (app: RLS tables minus counters, listed exempt-table privileges, `scope_*` views; etl: DML on `public` + `etl`; readonly: `SELECT`), REVOKEs after the grants, `standby_rate_entries` loses DELETE | R12, R20, R66, R67 |
| SECURITY DEFINER helpers owned by the migration login | ACL first, then ownership to `logitrack_rls_definer` + its table grants | R10, R66, R87 |
| RLS statements per table in AU only | `ENABLE` + `FORCE` on exactly the §C.3.0 "yes" tables (11 of 0006, 7 of 0007, 5 of 0008, none of 0009), policy block in the same file, layout guard in 0009 | R67 |
| `file_objects` in 0009 (DM); RT `storage_objects`; DP `media_objects` | `file_objects` in `0002_identity` with the R1 columns | R1 |
| `job_runs` ledger (`bigint`, `UNIQUE(job_name, scheduled_for)`); A-draft params under `progress.params` | `jobs(…, params, progress {done,total}, …)`; advisory lock + Redis `lock:job:*` | R14, R64 |
| no inbox; `outbox_events.aggregate_id uuid`, no routing columns | `consumer_inbox`; `aggregate_id text`, `exchange` (`lt.events`\|`lt.jobs`), `routing_key`, `realtime_topics`, `event_id uuidv7()` | R14, R17, R54 |
| per-topic realtime sequence (RT) | `INCR rtlog:seq`, `XADD rtlog:{topic}` id `{seq}-0`, `PUBLISH rt:{topic}` | R52 |
| `idempotency_keys` as long as the Redis key | Redis 24 h + row until `IDEMPOTENCY_TTL` (168h) | R53 |
| no durable offline key on expenses and leave; A-draft `client_message_id` unique per (chat, sender) | `client_op_id` + partial `UNIQUE (driver_id, client_op_id)` on `vehicle_expenses`, `leave_requests`; `UNIQUE (chat_id, client_message_id)` | R63 |
| `device_tokens(id, token UNIQUE, install_id NULL, invalidated_at)`; `mobile_installations(install_id PK)` | PK `(user_id, install_id)`, invalid tokens deleted; PK `(driver_id, install_id)` | R4, R33 |
| `notification_deliveries.device_token_id`, `bigint` id | `user_id` + `install_id`, `outbox_event_id`, `uuid` id; `kind` adds `tasks_changed`, `session_revoked` | R4, R21, R50, R57, R84 |
| `chats.driver_user_id NOT NULL`; `chats_one_open_per_driver` in 0007; fuel tax-invoice UNIQUE in 0006 | dropped (derive via `drivers.user_id`); both unique indexes in `0010` | Notes 0007; D5, R31, R59 |
| `broadcasts` without void | `voided_at`, `voided_by` | R14 |
| per-table `tenant_source` subsets incl. `etl_creator`; `legacy_driver_ref` without match column; FK `NOT NULL` | one inline CHECK vocabulary; `legacy_driver_ref` + `driver_ref_match`; `CHECK (fk IS NOT NULL OR tenant_source = 'quarantine')` | R11, R12, R19, R58 |
| upper-case statuses (`DRAFT`, `PENDING`, `PUBLIC`, `SICK`, `EARNING`) | lower_snake; domain codes keep their case | R30 (D2), R65 |
| `gen_random_uuid()`; `bigint` identity on `security_events`, `notification_deliveries`, `etl.quarantine` | `uuidv7()`; identity only on `outbox_events.id` | R34, R57 |
| `price_per_liter numeric(8,3)`; `month_key` via STABLE `to_char`; immediate `transactions_payroll_fk` | `numeric(14,2)`; STORED `yyyy_mm(day_key)`; `DEFERRABLE INITIALLY DEFERRED` | R20, R34 |
| `etl.source_docs.target_id uuid`, no `rejected`; DP `etl_id_map`, `etl_quarantine(…, reason_detail, raw)`, free-text reason codes | `target_id text`, `source_create_time`, `rejected`; id map folded into `etl.source_docs`; `etl.quarantine(field, detail, raw_value)` with the §A.3.0 CHECK list | R11, R19, R68 |
| trip without `createdAt`, `std`, `deliveredTimestamp` rejected; fixture code `missing_created_at` | `COALESCE(…, Firestore create time)` + `created_at_derived` | R68 |
| quarantine tenant created by `cmd/etl`; own-fleet id under an ETL-prefixed variable name | fixed-id row from `0002`; own fleet from `OWN_FLEET_TENANT_ID` (`cmd/seed` / `cmd/etl` only, main spec §16) | R7, R56, R87 |
| no ETL rule for `truck_assignments` | tenant from the truck (`truck`); unresolved truck or driver → `rejected` | R11, R58 |
| AU `users.legacy_firebase_uid`; DM `legacy_password jsonb`, `force_logout_at`, `revocation_epoch` | `users.legacy_auth_uid`; `legacy_scrypt_hash` / `_salt` bytea, `status`; `forceLogoutAt` not loaded | R55 |
| ETL `is_platform_admin`, `tenant_memberships`, `role_capabilities`, `auth_sessions` | `memberships`, `user_platform_roles` (`PLATFORM_ADMIN_EMAILS` only), `role_capability_overrides`; sessions start empty | R2, R5, R6 |
| A-draft platform-wide overrides for every matrix row | own-fleet overrides for `manager`, `operation_staff`, `operator`, `driver` where the value differs (§C.2.6) | R5 |
| A-draft FCM install id `legacy:users.fcmTokens:<key>` | `'legacy:'` + 16 hex of `sha256(token)` (§C.5.5) | R4 |
| DM: null the losers' fuel `tax_inv_id` | load unchanged + `duplicate_natural_key`, fix through the API, then `0010` | D5, R59 |
| A-draft purposes `check_in_photo`, `leave_attachment`, `maintenance_image`, `truck_image`, `company_asset`, `apk_release` | the §A.2.1 vocabulary (`checkin_photo`, `leave_evidence`, `maintenance_file`, `truck_photo`, `company_logo`, `apk`, …) | R1 |
| RT floor writer `PUT /v1/mobile/settings`; RT public-listener form routes | `PUT /v1/app-releases/floor`, `POST /v1/app-releases` (internal); BFF `/api/forms/*` → internal `POST /v1/waitlist`, `POST /v1/partner-interest` | R43, R44, R77 |
| — | `holidays` CHECK `public ⇔ tenant_id IS NULL` | R12 |
| D5 indexes spread over 0005–0007 | one `0010_d5_unique_constraints.sql`, authored in T04, applied in the T24 runbook after sign-off | R59, R88 |

## A.3 ETL mapping (Firestore -> PostgreSQL)

Tool: `cmd/etl` (`dump` → NDJSON per collection, `load`, `mirror`, `project`, `media-copy`, `rewrite-urls`, `reconcile`, `export-back`, `quarantine list|resolve`, `repair <name>`, `status`, plus the auth steps `auth-import`, `auth-weak-scan`, `auth-tail` of [Appendix C](./C-auth-rbac.md) §C.5); the process, runbook and coexistence sync are [main spec](../../../developer-spec.md) §13. It connects as `logitrack_etl` through `ETL_DATABASE_URL` (BYPASSRLS, R66); `cmd/seed` writes through the same role and load path (R87). Source is a live Admin SDK read, not the `gcloud firestore export` format (that export is kept only as the immutable archive). Default rule for fields not listed below: camelCase → snake_case, same meaning, cast per §A.3.0. Only quirks and non-obvious moves are listed. Citations are to the fact-base reports (schemas, legacy-quirks "Qn", billing, integrations, auth) unless a repo path is given.

### A.3.0 Global rules, quarantine tenant and load order

**Timestamp casting** (schemas report line 7): Firestore `Timestamp{seconds,nanos}`, JS `Date`, epoch-ms number and ISO string → `timestamptz`; Firebase Auth metadata string (RFC 1123) → `timestamptz`; `yyyy-MM-dd` → `date` (or `bkk_midnight(date)` when the target is `timestamptz`); `dd-MM-yyyy HH:mm:ss` (`sealTime` only) → parsed as Asia/Bangkok; anything else → NULL + field finding `bad_timestamp`. Bangkok calendar facts always go through `bkk_date()` (fixed +07:00, `billingCompute.ts:168-178`).

**Money** (R20): legacy JS floats → `NUMERIC(14,2)` via `floor(x*100 + 0.5)/100` evaluated in float64 (reproduces `Math.round` half-up, including negatives `-2.5 → -2`); `NaN`/`Infinity` → NULL + `bad_number`; a negative value where the column requires `>= 0` (expense amount, fee, penalty, standby amount) → NULL + `negative_money`, or `rejected` when the column is NOT NULL (abort when the row carries billing evidence, R19). Stored rounded values (final rate, surcharge, WHT, band bounds) are unchanged by this; unrounded float sums (multi-drop `totalBillingThb`, statement `grandTotal`, `netAmount`, payroll totals) lose sub-satang noise. Reconciliation tolerance is 0.005 THB only for the legacy multi-drop total and `netAmount`; every other amount must match exactly (R20).

**Load outcomes per document** (R11, R19). `etl.source_docs.status` records which one applied:

| Outcome | When | Effect |
|---|---|---|
| `loaded` | tenant and every NOT NULL column resolve | row in its target table; field-level problems → column NULL + `etl.quarantine(field=<name>)` |
| `quarantined` | the tenant chain runs out (`resolveTenant` returns nothing), or a required driver/truck reference cannot be resolved | row loaded with `tenant_id` = the quarantine tenant, `tenant_source='quarantine'`, the unresolved FK NULL (allowed only by the `… OR tenant_source='quarantine'` CHECKs), raw reference kept in `legacy_*_ref`; invisible to every tenant; re-homed by a platform admin via `POST /v1/tenants/quarantine/rows/{table}/{id}/rehome` (Appendix B §B.2.4) |
| `rejected` | a NOT NULL business column has no source value and no lossless derivation (task without `date`, hub without `source_id` or Thai name: `missing_required`), a status outside the vocabulary with no lossless mapping (`status_out_of_vocab`), a CHECK the row cannot satisfy (`bad_number`, `negative_money`), or an unresolved reference in a key column that has no quarantine exception (`mobile_installations`, `truck_assignments`, `vehicle_locations`) | not loaded; row-level `etl.quarantine(field IS NULL)`; reloadable with `quarantine resolve --action=retry` after a fix at source. **Guard (R19):** if the document carries any billing evidence (`billingEstimateThb` number, any `billing*` field, `deliveredTimestamp` on a delivered trip, standby `endedAt`), rejection aborts the run with exit 2 instead of skipping. A trip is never rejected for a missing `createdAt` (R68) |
| `dropped` | collection intentionally not migrated (`checkin`, Firestore named DB `trucks`) or not in the mapping (`unknown_collection`) | kept only in `etl.source_docs` |

Invariant checked by `etl reconcile`: per collection, `firestore_count = loaded + quarantined + rejected + dropped`.

**Reason codes** (R68). The canonical merged list: the data-model draft's codes, the R68 additions (`task_ambiguous`, `party_unresolved`, `url_unparseable`, `negative_money`, `created_at_derived`, `missing_delivered_at`), `helper_tenant_mismatch` of Appendix C §C.3.6, and the codes the tables below need to name every finding (`user_unresolved`, `truck_unresolved`, `tenant_mismatch`, `helper_overflow`, `missing_required`). The CHECK on `etl.quarantine.reason_code` (0009) enforces exactly this set. Level: **row** = row outcome, **field** = column NULL (raw kept where a `legacy_*` / `*_raw` column exists), **info** = loaded unchanged.

| Code | Level | Raised when |
|---|---|---|
| `tenant_unresolved` | row → quarantine tenant | the tenant chain below runs out |
| `driver_unresolved` | field; row → quarantine tenant where the FK is required | the driver rule below matches nothing (also: installation under a deleted driver or truck assignment → `rejected`; FCM token of a driver without user → skipped) |
| `user_unresolved` | field | a Firebase uid (`authId`, `voidedBy`, `approverId`, `senderId`, broadcast reader, `performedBy`, `actorUid`) matches no `users.legacy_auth_uid` |
| `truck_unresolved` | field; `rejected` for `vehicle_locations` and `truck_assignments` | `truckId` / `GPSVehicleId` matches no truck |
| `task_unresolved`, `task_ambiguous` | field (`legacy_task_ref` kept) | `taskId` matches no task / a business `taskId` shared by several tasks (Q4) |
| `trip_unresolved` | field (`legacy_trip_ref` kept) | incident or standby `tripId` matches no trip, rename history included |
| `party_unresolved` | field; `rejected` for rate rows | a `billingCustomerId`, `*LinkedCustomerId`, rate or standby `customerId` matches no customers or subcontractors doc |
| `customer_unresolved` | field | a customer **code** (`customerDriverIds` key) matches no `customers.code` |
| `hub_unresolved` | info | hub or destination text resolves to no hub, SOC or alias |
| `tenant_mismatch` | field | an installation's `partnerId` disagrees with the driver's tenant |
| `helper_overflow` | field (`[0]` kept) | `helperDriverIds` longer than the ADR 0011 cap of 1 |
| `helper_tenant_mismatch` | info | helper outside the task's tenant and its `contractor_tenant_id` link |
| `missing_required` | `rejected` | a NOT NULL business column has no source value (task `date`, hub `source_id` / `name_th`) |
| `bad_timestamp`, `bad_number` | field; `rejected` when a CHECK cannot hold | unparseable time; non-numeric, `NaN`, `Infinity`, penalty `remainingThb > totalThb` |
| `negative_money` | field; `rejected` when NOT NULL | negative amount where the column requires `>= 0` |
| `status_out_of_vocab` | `rejected` at load; in the mirror the field is skipped (main spec §13.8) | legacy literal with no lossless mapping (§A.1.7) |
| `vehicle_class_out_of_enum` | field | folded class outside the enum; never guessed (R15) |
| `photo_type_unknown` | info (`photo_type_known=false`) | trip photo type outside the enum and the `stop_{n}_*` pattern |
| `file_missing_at_source` | info (`file_objects.status='missing_at_source'`) | `media-copy` found no object |
| `url_unparseable` | field | a URL that is not a Firebase Storage download URL |
| `duplicate_natural_key` | info (customer code: second row `rejected`; repeated stop destination: stop not loaded) | customer code, fuel `(driverId, taxInvId)`, several non-closed chats per driver, a destination twice in one task |
| `duplicate_service_fee` | info | several fees per (party, `feeType`) (D5) |
| `duplicate_hub_source_id` | loser `rejected`, mapped to the winner | several hub docs per `source_id` (Q8) |
| `created_at_derived` | info | trip `createdAt` missing, taken from `std`, `deliveredTimestamp` or the Firestore create time (R68) |
| `missing_delivered_at`, `missing_billing_date`, `missing_ended_at` | info (standby unpriced `no_ended_at`, R62) | delivered trip without `deliveredTimestamp` (Q13); plan-basis trip without `billingDate` (D7); standby without `endedAt` |
| `billing_date_locked` | info | a restamp would cross a sent/paid period (D7) |
| `trip_no_mismatch` | info | `spxTripId` differs from the doc id without rename provenance, or fails the charset CHECK (then `trip_no` = doc id) |
| `multidrop_priced_as_single`, `link_blanket_default`, `legacy_standby_trip` | info | multi-drop priced as single; blanket `DEFAULT_CUSTOMER_ID` link (Q9); unmigrated standby trip (Q5) |
| `unknown_capability_key`, `unknown_collection` | info; `dropped` | key without catalog equivalent (Appendix C §C.2.6); collection not in this mapping |

Draft synonyms are never written: `tenant_orphan` → `tenant_unresolved` (it survives only as a RabbitMQ permanent-error class, Appendix B §B.5.4); `invalid_timestamp` → `bad_timestamp`; `enum_unknown` → `status_out_of_vocab`; `missing_billing_axis` → `missing_ended_at`; `missing_created_at` → `created_at_derived`; `trip_no_invalid` → `trip_no_mismatch`; `plate_regex_fail` → no finding (plates are display strings, Q19).

**Own-fleet and quarantine tenants** (R7, R11, R56). The quarantine tenant exists before any load: `0002_identity` inserts it with the fixed id `00000000-0000-7000-8000-00000000000f`, the value of `app_quarantine_tenant_id()`. `cmd/etl` (or `cmd/seed`) creates the `tenants` row `kind='own_fleet'` with the uuid given by `OWN_FLEET_TENANT_ID` (built from the `companies` doc with `companyType='owner'`, or from `settings/tenancy.ownFleetTenantId` where that doc exists — `functions/src/tenantLookups.ts:16-21` on `origin/feat/multi-tenant-carrier-isolation`, read by the critic); the partial unique indexes of 0002 allow exactly one row of each kind. `cmd/etl` refuses to start when `OWN_FLEET_TENANT_ID` is unset (replaces the silent `undefined` own-fleet id, `tenantLookups.ts:26-45` on the same branch, DM). The variable is read only by `cmd/seed` and `cmd/etl`, never by `api` or `worker`. Every carrier tenant built from a legacy `subcontractors` doc gets `contractor_tenant_id` = the own fleet (R60, §A.3.2).

**Driver reference resolution** (R12, R28). Every `driverId`-like value: `drivers.legacy_doc_id = v` → `driver_ref_match='doc_id'`; else `drivers.legacy_auth_uid = v` (`authId`, or legacy `authUid`, billing.ts:891) → `'auth_uid'`; for `tasks` only, else `matchDriverOptionId` on `driverName` (Thai name, then Latin "first last"; `driverName.ts:44-61`) → `'name'`; else `'none'` + `driver_unresolved` (and, where the FK is required, the row goes to the quarantine tenant). The raw value is always kept in `legacy_driver_ref`. No phone/plate ownership fallback is ported (R28; the phone branch is dead, `multiDeliveryTrips.ts:46` reads `phone` while the field is `mobile`).

**Tenant derivation** (`tenantResolve.ts` on the tenancy branch, ordering per DM; R11). Run strictly in load order so parent links are stamped before fallbacks:

| Collection | Chain → `tenant_source` | Chain exhausted |
|---|---|---|
| drivers | `subcontractorId` → that carrier tenant (`self`); none → own fleet (`self`) | `subcontractorId` pointing at a missing subcontractors doc → quarantine tenant + `tenant_unresolved` |
| trucks | `ownershipType='subcontractor'` → `subcontractorId` (`self`); `'own'` → own fleet (`self`) | subcontractor without id → quarantine tenant |
| tasks | driver's tenant (`driver`); rows created later by a dispatcher or staff form: the active tenant (`form`, R13) | no resolvable driver (pending/unassigned/cancelled pool) → quarantine tenant (D6, §A.4); bulk re-home after owner confirmation |
| trip_records | task (`task`) → driver (`driver`) | quarantine tenant |
| standby_records | task (`task`) → trip (`trip`) → driver (`driver`) | quarantine tenant |
| incidentReport | trip (`trip`) → driver (`driver`) | quarantine tenant |
| vehicle_expenses | driver (`driver`) → truck (`truck`) | quarantine tenant |
| driver_penalties, payroll, leave_requests, chats, mobile_installations | driver (`driver`) | quarantine tenant (mobile_installations: `rejected`, see §A.3.4) |
| maintenance, vehicle_locations, transactions (renewal shape) | truck (`truck`) | quarantine tenant |
| truck_assignments | truck (`truck`), as main spec §3.3 classifies it | none: `truck_id` and `driver_id` are NOT NULL without a quarantine exception, so an unresolved truck or driver → `rejected` + `truck_unresolved` / `driver_unresolved` (assignment history carries no billing evidence) |
| rate entries, fuel adjustments, service fees, standby rates, statements, counters, compensation config, transactions (payout shape) | billing carrier = the rate-card owner, the own fleet today (`form`, R61) | n/a |
| companies | `companyType='owner'` → own fleet (`self`); `'subcontractor'` → carrier tenant matched by `taxId`, then name | quarantine tenant (relationship UNVERIFIED, auth report §8) |
| holidays | `type='PUBLIC'` → `tenant_id NULL`; `COMPANY`/`OTHER` → own fleet | n/a |
| broadcasts, security_events | `tenant_id NULL` (legacy rows are platform-level) | n/a |

**Load order** (main spec §13.9; rate tables early because snapshot FKs point at them). The P0 `auth-import` (Appendix C §C.5.1) needs carrier tenants, customers, `billing_parties` and drivers for memberships, scopes and driver links, so it loads those collections first; the P1 full load (R71) re-applies them idempotently (`ON CONFLICT (legacy_doc_id)`, newer `_updateTime` only) and continues. Full order: `file_objects` manifest → tenants (subcontractors, + `tenant_files`) → customers → `billing_parties` → companies → users (+ identities, memberships, scopes; PG-owned since P0, R49) and `role_capability_overrides` (step `auth-rbac`, report signed off before the P6 Security Center cut-over, Appendix C §C.2.6) → rate entries, fuel adjustments, service fees, standby rates, fuel snapshots → hubs + `hub_name_aliases` → `hub_soc_distances` → trucks (+ status history, files, renewal transactions) → drivers (+ status history, customer codes, user links, `device_tokens`) → `truck_assignments` (then `drivers.current_assignment_id`) → `mobile_installations` → tasks (+ delivery stops, counters) → trip_records (+ stops, photos, snapshots, breakdown, `trip_no_history`) → standby_records (+ photos) → incident_reports → billing_statements + counters → vehicle_expenses → maintenance (+ files) → compensation configs, penalties, payroll (+ lines, applications), payout transactions → chats (+ messages, read state), broadcasts (+ reads) → leave_requests (+ attachments) → holidays → security_events, vehicle_locations, waitlist, partner_interest, settings, mobile_app_releases → `media-copy` + `rewrite-urls` (parallel from the tasks step) → `reconcile` → owner sign-off of the quarantine report → `goose up` applies `0010` (R59, R88).

**Id minting for rows born in PostgreSQL** (R25; coexistence rules in main spec §13.8). The PG→FS projection (class B) and the class C write-back need a Firestore doc id:

| Case | Rule |
|---|---|
| Row inserted in PG (class A/B) and projected | `id` from `uuidv7()`; the same transaction sets `legacy_doc_id = id::text` before the outbox event; the projection upserts doc `{legacy_doc_id}` |
| Write-back of a Firestore-owned doc (class C, P2–P7b) | Go creates the doc with a fresh uuidv7 string as id; the mirror loads it with `id` = `legacy_doc_id` = that string, so identity round-trips without a lookup |
| User created in Go while `AUTH_FIREBASE_BRIDGE_MODE` includes `mobile` | Firebase uid = `users.id::text`, also stored in `users.legacy_auth_uid` |
| Legacy doc (Firestore auto-id or driver-typed trip id) | `id` = new `uuidv7()`; `legacy_doc_id` = original doc id |
| Field values in the projection | status via the translation table (canonical lower_snake → legacy literal, e.g. `checked_in` → `'Checked in'`, D2); `dateStr` written as Bangkok `ddMMyyyy`; `driverId` = `drivers.legacy_doc_id` on tasks and the driver's `legacy_auth_uid` on collections that store the auth uid (trip_records, standby_records, vehicle_expenses, chats, payroll, …; schemas report line 1); `helperDriverIds` = `[legacy_auth_uid]` |

**File URL → object key** (R1; integrations report "Storage: URLs not paths"). A stored download URL `https://firebasestorage.googleapis.com/v0/b/{bucket}/o/{encodedPath}?alt=media&token=…` becomes `object_key = urldecode(encodedPath)`; the token is discarded. One `file_objects` row per key: `bucket` = the `S3_BUCKET` value (the `S3_PUBLIC_BUCKET` value with `visibility='public'`, `purpose='apk'` for `app_releases/`), `legacy_url` = URL without token, `status='committed'` (+ `committed_at`) when `media-copy` copied the object GCS → MinIO under the **identical key**, else `'missing_at_source'` + `file_missing_at_source` (the referencing row still loads), `purpose` per referencing column (§A.3.7), `owner_kind` / `owner_id` and `tenant_id` from the referencing row, `uploaded_by` and `expires_at` NULL. Keys are never rebuilt from `trip_no`: renamed trips keep their objects under the old folder (`functions/src/renameTripRecord.ts:40-45`, read in the repo). Non-Firebase URLs (Google profile photos) → `url_unparseable`. URL-format readers (`looksLikeImageUrl`, `tripEvidence`, ZIP downloader, mobile gallery) change in the API layer, not in data. Presigned-only vs a public-read prefix for trip and incident photos is owner question 5 of main spec §19.2 (R75); the ETL registers every non-APK object as `private` either way.

**Bangkok effective dates for announcement rows** (rate entries, fuel adjustments, standby rates, compensation configs). `effective_from_date := effectiveFromDateStr ?? bkk_date(effectiveFrom)`; `effective_from_at := effectiveFrom` (the stored instant, untouched). Rows written before 2026-08-09 sit at UTC midnight (07:00 ICT) and later rows at Bangkok midnight (Q14; `billing.ts:122-127` per billing report); `bkk_date` maps both to the same Bangkok day, which is exactly what `isEffectiveOnOrBeforeBillingDate` compares (`billingCompute.ts:191-193`). This applies the never-run `normalizeAnnouncementEffectiveFrom` (`billingRoundMigration.ts:219`) as a load transform without rewriting any instant. `standby_rate_entries.effectiveFrom` is browser-local midnight (`rate-card/page.tsx:1146` → `billing.ts:664`, billing report); `bkk_date` gives the intended day only if the admin browser ran in ICT — UNVERIFIED, every standby rate is listed in the reconciliation report for review. Same-day ties (R16): selectors receive rows ordered `effective_from_date DESC, effective_from_at DESC`, then legacy rows by `legacy_doc_id ASC` (reproduces the stable sort over Firestore doc-id order, billing report §2c, UNCERTAIN order) and new rows by `created_at ASC, id ASC` (uuidv7 is time-ordered).

### A.3.1 users / Firebase Auth export

Step `auth-import` of P0 (issue T19); the claim mapping, special accounts, weak-password scan, re-import and tail are owned by [Appendix C](./C-auth-rbac.md) §C.5 and §C.2.6, and this table only names the target columns. Users are PG-owned from P0 (R49); afterwards only `lastLogin*` and `fcmTokens` are mirrored until P7b.

| Source | Target | Transform |
|---|---|---|
| Auth export `localId` | `users.legacy_auth_uid` (R55) | also `auth_identities(provider='firebase_legacy', provider_subject=uid)` |
| `email`, `emailVerified`, `displayName` | `users.email` (`citext`), `email_verified`, `display_name` | a duplicate email across uids (case-insensitive) → second user not imported; an account without email → not imported; both listed in `migration_users_report.csv` (Appendix C §C.5.6) |
| `photoUrl` | not loaded | external Google photo URLs are not our objects; kept in `etl.source_docs.raw` |
| `passwordHash`, `salt` (base64) | `users.legacy_scrypt_hash`, `users.legacy_scrypt_salt` (bytea, decoded once with `DecodeB64`) | verify-then-rehash to Argon2id on first login, which NULLs the pair (Appendix C §C.5.3–§C.5.4). Parameters `FIREBASE_SCRYPT_*` come from Console → Authentication → Users → "Password hash parameters", not from `auth:export` (R23). `auth-weak-scan` sets `must_change_password` for weak default driver passwords (R29; candidates never written anywhere); `auth-import --refresh-legacy` refreshes pairs with `password_hash IS NULL` before P7a; `auth-tail` NULLs the rest 180 days after the P7b gate (R30, §C.5.7–§C.5.8) |
| `providerUserInfo[]` `google.com` `rawId` | `auth_identities(provider='google', provider_subject=rawId)` | Google users need no password migration |
| `disabled: true` (export is authoritative) | `users.status='disabled'`, `disabled_at` = export time | |
| `createdAt` / `metadata.creationTime` | `users.legacy_auth_created_at` | |
| `customAttributes` (claims; authoritative over the users-doc mirror fields) | `memberships`, `user_platform_roles`, `user_scopes`, `drivers.user_id` | Appendix C §C.5.5: `admin` → own-fleet `tenant_admin`, no platform role; `PLATFORM_ADMIN_EMAILS` → also `user_platform_roles(platform_admin)` (`cmd/seed bootstrap-platform-admins`); `manager`/`operation_staff`/`operator`/`user` → own-fleet membership; `driver` / `driverId` → driver by `legacy_doc_id`, else `legacy_auth_uid`, then `drivers.user_id` + `(drivers.tenant_id, 'driver')` membership; `partner` + `partnerScopeId` → `tenant_admin` of that carrier tenant; `customer` + `customerScopeId` → `user_scopes(kind='customer')` (R86); `companyId` ignored; no role → no membership unless `--default-member-domain` (CLI flag) grants own-fleet `user`; unresolved scopes and stale driver claims → report only |
| users doc `lastLogin` (ISO **or** Auth metadata string) + export `lastSignedInAt` | `users.last_login_at` | latest parsed value; the field is `lastLogin`, not `lastLoginAt` |
| `lastLoginLat/Lng/GeoSource/LocationAccuracyM`, legacy nested `lastLoginLocation` | `users.last_login_*` | nested shape read as fallback |
| `forceLogoutAt`, users-doc `role` / scope mirrors / `providerData`; Firebase refresh tokens | not loaded | claims are authoritative, force logout is session-based; `sessions` start empty, so web users sign in again (R2) while mobile uses the Firebase bridge until P7a (R8) |
| users doc `fcmTokens` (map `{app: token}` or legacy array) + drivers `fcmToken` | `device_tokens(user_id, install_id, token, driver_id, legacy_source)` | `install_id = 'legacy:'` + first 16 hex of `sha256(token)` (Appendix C §C.5.5) until the device re-registers (`PUT /v1/mobile/me/devices`, `PUT /v1/me/devices`); deduplicated by token, keeping the `users.fcmTokens` row with `driver_id` filled; a driver token without a linked user → skipped + `driver_unresolved` |
| `permissions_config/{role}.capabilities` | `role_capability_overrides` (`tenant_id` = own fleet) | Appendix C §C.2.6 (step `auth-rbac`): key translation to colon keys; overrides only from the `manager`, `operation_staff`, `operator`, `driver` docs where the saved value differs from the catalog default; unknown keys → `unknown_capability_key`; `migration_rbac_report.csv` signed off by the owner |

### A.3.2 subcontractors, companies, customers, billing parties

| Source | Target | Transform |
|---|---|---|
| `subcontractors/{id}` | `tenants(kind='carrier')` | `legacy_doc_id = id`; `name` → `name_th`; `type` → `legal_type`; `documents[]` → `tenant_files` via the file manifest; `code` → `tenants.code` (UNVERIFIED field, read by `lineNotify.ts:141`); `contractor_tenant_id` = the own fleet, because legacy subcontractors work for the own fleet (R60; Appendix C §C.3.4; owner confirmation: question 13) |
| `subcontractors.billingDateBasis` | `billing_parties.billing_date_basis` (`kind='tenant'`) | a `billing_parties(kind='tenant')` row is created for every tenant referenced by `billingCustomerId`, a partner-kind `*LinkedCustomerId`, or a rate-table `customerId`; absent basis → `'delivered'` |
| `companies/{id}` | `companies` | `branchType` kept; `withholdingTaxRate` loaded as stored (schema default 3; effective stored value UNVERIFIED, billing report divergence #6). Statements do not read it (R18, §A.3.9) |
| customers `name` **or** `customerName`; `code` **or** `customerCode` (Q10) | `customers.name`, `customers.code` | code upper-cased; duplicate codes → second row `rejected` + `duplicate_natural_key` (no billing fields live on customers) |
| customers `branchType` `'สำนักงานใหญ่'`/`'สาขา'` | `customers.branch_type` `hq`/`branch` | |
| customers `billingDateBasis` (Q10) | `billing_parties.billing_date_basis` (`kind='customer'`) | one `billing_parties` row per customer; absent → `'delivered'` (`tripBillingOnDelivered.ts:45-47`) |
| customers `logoUrl` (`''` when none) | `customers.logo_file_id` | `''` → NULL; URL → §A.3.0 rule |
| customers `driverIdTypes[]` | `customer_driver_id_types` | |
| every `billingCustomerId` / `*LinkedCustomerId` / rate or standby `customerId` | `*_party_id` → `billing_parties.id` | lookup customers first, then subcontractors (ADR 0028; `tripBillingOnDelivered.ts:45-64`); unresolved → NULL + `party_unresolved` |

### A.3.3 hubs, aliases, distances

| Source | Target | Transform |
|---|---|---|
| `source_id` ← `hubId` ← `hubCode` (`sources/page.tsx:76-100`, Q8) | `hubs.source_id` | upper, trimmed; none → `rejected` + `missing_required`; `unknown_<n>` ids from `import-hubs.ts` kept and flagged `hub_unresolved` |
| `source_name_th` ← `hubTHName` ← `hub_th_name` ← `station_name_th`; TH empty and EN contains Thai → TH := EN, EN := NULL (`hub-dialog.tsx:95-104`) | `hubs.name_th` (NOT NULL) | still empty → `rejected` + `missing_required` |
| `source_name_en` ← `hubName` ← `station_name_en` | `hubs.name_en` | |
| `latitude/longitude` ← `lat/lng` | `hubs.latitude/longitude` | |
| `station_type` FM_HUB/LH_HUB → HUB, RETURN_CENTER → SOC | `hubs.station_type` | |
| `linkedCustomerId` + `customerLinkKind` | `hubs.linked_party_id` | `linkedCustomerName` denorm dropped; unresolved → `party_unresolved` |
| `createdByDriver` | `hubs.created_by_driver` | driver-created hubs (`mobile:create_hub`) keep the flag the driver insert policy checks (Appendix C §C.3.5) |
| network rule (`hubSchema.ts:36-49`) | `hubs.network` | `source_id` starts with SPK → SPK; linked customer code ∈ {SPK, J&T, JT} → SPK; else SPX |
| duplicate docs per `source_id` (prod, Q8) | one `hubs` row | keep the doc with the most non-empty fields (`reimport-hubs-dev.js:45-79`); losers `rejected` + `duplicate_hub_source_id`, their doc ids mapped to the winner in `etl.source_docs.target_id` so legacy references still resolve |
| nameToCode (`source_name_th`, `source_name_en`, legacy `hubName`; skip blank or equal-to-code; first writer wins in doc-id order, as `buildHubMaps` iterates) | `hub_name_aliases` | **name → code only**; `codeToName` is `hubs.name_th` and is never materialised as a second map (`shared-docs/.vibe-rules.md` Confirmed Patterns, rule against merging `nameToCode` and `codeToName`, as of commit 4f552099; `tripBillingOnDelivered.ts:124-141`) |
| `hub_soc_distances/{hub}_{soc}`, `soc_hub_distances/{soc}_{hub}` | `hub_soc_distances(direction)` | `hubId` is a `source_id` → `hubs.id`; `socId` normalised to SOCE/SOCN/SOCW or upper raw |
| `metadata/distances_last_calculated` | `settings('distances_last_calculated')` | |

### A.3.4 drivers, trucks, assignments, status history, installations

| Source | Target | Transform |
|---|---|---|
| drivers doc id | `drivers.legacy_doc_id` | nullable for Go-created drivers (R12) |
| drivers `authId` (legacy alias `authUid`, billing.ts:891) | `drivers.legacy_auth_uid` (R55), `drivers.user_id` | user found by `users.legacy_auth_uid`; none → `user_id NULL` + `user_unresolved`; a linked driver also gets its `memberships(tenant, 'driver')` row in the same transaction (the deferred `t_driver_link_membership` check, Appendix C §C.3.6) |
| drivers `statusHistory[]` `{status, changedAt Timestamp, changedBy uid\|'system', changedByName, reason, previousStatus}` | `status_history(entity_type='driver')` | uid → `changed_by_user_id`; `'system'`/name → `changed_by_label` (Q18) |
| trucks `statusHistory[]` `{status, date ISO string, changedBy displayName/email, notes}` (Q18) | `status_history(entity_type='truck')` | `date` → `changed_at`; `changedBy` → `changed_by_label`; `notes` → `reason`; free-text statuses (`'Tax Renewed'`) kept verbatim |
| drivers `status` `'Active'/'Inactive'/'On-Duty'` | `drivers.status` `active/inactive/on_duty` | |
| drivers `currentAssignment{assignmentId,…}` | `drivers.current_assignment_id` | by `truckAssignment` doc id; `truckPlate/truckModel` denorm dropped |
| drivers `activeTruck{truckId, truckPlate, taskId, startedAt}` | `drivers.active_*` | top-level legacy `truckId`/`currentTruckId` kept only in `etl.source_docs` |
| drivers `customerDriverIds{code:{key:value}}` | `driver_customer_codes` | code → `customers.code`; unknown → entry dropped + `customer_unresolved` |
| drivers `fullNameTh` / `firstName` / `lastName` | as named | whitespace-only Thai name → NULL (`driverName.ts:8-22`) |
| trucks `truckStatus` incl. `'Active'`, `'Available'` | `trucks.status` | `'Active'` → `active`; `'Available'` → `active` with `legacy_status='Available'` (UNVERIFIED semantics); unknown → `status_out_of_vocab` + `rejected` |
| trucks `type` (full word) | `trucks.type_raw` + `trucks.vehicle_class` | `taskTruckTypeFromTruckDoc` map (Pickup→4W, '4 Wheels'→4WJ, '4 Wheels Jumbo'→4WJ, '6 Wheels'→6WH, '10 Wheels'→10WH, '18 Wheels'→18WH, Van→VAN; Q11); unknown → NULL + `vehicle_class_out_of_enum`, never guessed |
| trucks `subcontractorId` + `ownershipType` | `trucks.tenant_id`, `ownership_type` | §A.3.0 tenant table |
| trucks image/document fields, `insuranceDocuments[]`, legacy `images[]` | `truck_files(kind)` | URL rule §A.3.0 |
| trucks `currentAssignments[]` / `currentAssignment:null` | not loaded | derived from `truck_assignments.status='active'` |
| trucks `activeMaintenanceId`, `lastAlertMileage` | `trucks.active_maintenance_id`, `last_alert_mileage` | FK set after maintenance loads |
| `truckAssignment/{id}` (`driverId` = doc id) | `truck_assignments` | `adminName` → `admin_label`; `tenant_id` from the truck (`tenant_source='truck'`, §A.3.0); unresolved truck or driver → `rejected` |
| `drivers/{id}/mobile_installations/{installId}` (collection-group dump) | `mobile_installations(driver_id, install_id)` | `driver_id` from the parent path (doc id); `tenant_id` from the driver; legacy `partnerId` (`''` = own fleet) not stored — a value that disagrees with the driver's tenant → `tenant_mismatch`. A subcollection under a deleted driver doc cannot satisfy the composite PK → `rejected` + `driver_unresolved` (telemetry only, no billing evidence) |
| `vehicle_locations/{GPSVehicleId}` | `vehicle_locations(truck_id)` | by `trucks.gps_vehicle_id`; latest position only; no matching truck → `rejected` + `truck_unresolved` (refreshed by `cartrack.sync` every 3 min from P6, R70) |

### A.3.5 tasks

| Source | Target | Transform |
|---|---|---|
| doc id vs `taskId` | `tasks.legacy_doc_id` / `tasks.task_no` | both kept; `task_no` is indexed, not unique (web pads to 3 digits, mobile and `getNextTaskId` do not; schemas report DOC-ID RULES) |
| max legacy `task_no` suffix per (type, date encoded in the number) | `task_number_counters(task_type, plan_date, last_seq)` | seeded so the first Go-minted number for a day continues after the highest legacy suffix; numbering is global per `(task_type, plan_date)` across tenants (R10) |
| `date` (Timestamp/Date) | `tasks.plan_at` (+ generated `plan_date`) | exact instant kept (it is the `billing_date` for plan-basis parties); missing → `rejected` + `missing_required` unless the task is referenced by a priced trip, in which case the run aborts (R19 guard) |
| `dateStr` (`ddMMyyyy` **or** `YYYYMMDD`, Q21) | `tasks.legacy_date_str` | not interpreted; `plan_date` comes from `date` |
| `status` `'Pending'`, `'Assigned'`, `'Checked in'`, `'In-Transit'`, `'Completed'`, `'Cancelled'` | `tasks.status` `pending`, `assigned`, `checked_in`, `in_transit`, `completed`, `cancelled` | canonical lower_snake (R30, R65, D2); the reverse mapping is the projection's translation table (R25, main spec §13.8) |
| `taskType` FIRST_MILE/LINE_HAUL | `task_type` `first_mile`/`line_haul` | |
| `jobCategory` absent (legacy) | `job_category NULL` | never defaulted to PRIMARY (ADR 0010; Q12) |
| `sourceHub` (`'CODE'`, `'CODE - Name'`, `'SOCE (บัวโรย)'`, `'soce'`; Q6) | `source_hub_raw` + `source_hub_id` | raw kept verbatim; id via `extractHubId` (split on `' - '`, upper) → `hubs.source_id`, else `hub_name_aliases`; unresolved → NULL + `hub_unresolved` (informational) |
| `destination` (same formats) | `destination_raw` + `destination_hub_id` / `destination_soc_key` | SOC prefix → `destination_soc_key`; otherwise the **full** raw code is tried first (`SPK-GW` matches its own hub), then the code before the first `-`. The collapsed key that legacy pricing used (`normalizeDestinationCode`, `SPK-GW` → `SPK`, billing divergence #1) survives only in `trip_billing_snapshots.lookup_destination_code` and `customer_rate_entries.destination_code` so historical prices stay reproducible |
| `sourceHubLinked*` + `sourceHubCustomerLinkKind`; `destinationLinked*` | `source_linked_party_id`, `destination_linked_party_id` | id + kind → `billing_parties`; Name/Code denorms dropped. Links equal to the hard-coded `DEFAULT_CUSTOMER_ID` (`backfillCustomerLinks.ts:6`) while the hub links elsewhere (blanket backfill, Q9) → loaded as stored + `link_blanket_default` for review |
| `billingCustomerId/Name/Code` | `billing_party_id` | customers first, then subcontractors (ADR 0028) |
| `truckType` incl. legacy `PICKUP`, `4WH`, `'6 Wheels'` (Q11) | `truck_type` + `legacy_truck_type` | fold map only (`PICKUP`,`4WH` → `4W`; `'6 Wheels'` → `6WH`; …); **blank → NULL**, never `4WJ` (R15: pricing returns unpriced reason `no_vehicle_class`); non-enum result → NULL + `vehicle_class_out_of_enum` |
| `truckId`, `licensePlate` | `truck_id`, `license_plate_snapshot` | plate kept as a display string (Q19) |
| `driverId` (doc id; legacy auth uid, `firestore.rules:194`) | `driver_id`, `legacy_driver_ref`, `driver_ref_match` | §A.3.0 rule including the name match; driverless tasks → quarantine tenant (D6) |
| `helperDriverIds[]` (auth uids, cap 1, ADR 0011) | `helper_driver_id` | `[0]` resolved by auth uid (`driver_unresolved` when none); length > 1 → `helper_overflow`; a helper outside the task's tenant and its `contractor_tenant_id` link → `helper_tenant_mismatch` (loaded as stored) |
| `checkInPhotoUrl`, `checkInAppScreenshotUrl` | `check_in_photo_file_id`, `check_in_app_screenshot_file_id` | URL rule §A.3.0 |
| `deliveryStops[]` (+ CF-only `sequence`, `addedAt`, `sourceId`, `isCustom`) | `task_delivery_stops` | `destinationLinked*` → party; duplicate destination within one task → `duplicate_natural_key` (the UNIQUE `(task_id, destination_raw)` would reject; the duplicate stop is kept in `etl.source_docs`) |
| `lineCheckinNotifiedAt` | `line_checkin_notified_at` | keeps `notify.line` idempotent across the cut-over |
| (no legacy field) | `client_op_id` | NULL for every legacy task; set only by `POST /v1/mobile/tasks/manual` from P7a (R63) |

### A.3.6 trip_records

| Source | Target | Transform |
|---|---|---|
| doc id (driver-typed) vs `spxTripId` (Q3) | `legacy_doc_id` / `trip_no` | `trip_no := spxTripId ?? docId`; both present and different → `trip_no = spxTripId`, `legacy_doc_id = docId`, `trip_no_history(old=docId, new=spxTripId)` when `renamedFromTripId` is set, otherwise `trip_no_mismatch` (informational). `trip_no` collision across docs → the second doc keeps `trip_no = legacy_doc_id` if free, else the run stops for manual resolution (both rows may be billable, R19). `trip_no` must pass the `tripDocId.ts:10-18` charset CHECK, which every Firestore doc id already satisfies |
| `taskId` = tasks doc id **or** business `taskId` (Q4) | `task_id`, `legacy_task_ref`, `task_ref_match` | doc id first, then `task_no` only when exactly one task has it; ambiguous → `task_ambiguous`, unresolved → `task_unresolved`; tenant then falls back to the driver |
| `status` incl. shared-docs `'loading'`/`'departure'` and the Dart default | `status` | `'loading'`/`'departure'` (no live writer, count expected 0) → `rejected` + `status_out_of_vocab`, guarded by R19; `'standby'` kept (legacy standby trips, §A.3.8) |
| `jobType` | `job_type` lower | |
| `jobCategory` absent | `job_category NULL` | |
| `driverId` (auth uid from mobile; legacy doc id) | `driver_id`, `legacy_driver_ref`, `driver_ref_match` | §A.3.0 |
| `origin` / `destination` free text (`SPK890146`, `ประเวศ18`, `ALANG-A - วังทองหลาง`; Q7) | `origin_raw` / `destination_raw` + resolved ids | `placeFilter.ts:70-89` order: known code passthrough → SOC spelling → `hub_name_aliases` → unresolved (NULL, raw kept); never code → name |
| `partnerCode` root vs `ocrData.partnerCode` (Q20) | `partner_code` | root first, then OCR; `ocrData` kept whole in `ocr_data` |
| `distance` (string km), `totalWeight` (string) | `distance_km`, `total_weight_kg` | numeric parse; non-numeric → NULL + `bad_number` |
| `sealTime` `'dd-MM-yyyy HH:mm:ss'` | `seal_time` | Bangkok local |
| `truckType` free string | `truck_type_raw` + `vehicle_class` | fold map; else NULL |
| `truckLicensePlate` | `truck_license_plate_snapshot` | never rebuilt from `activeTruck` (`truckPlate.ts:46-57`) |
| `deliveredTimestamp` | `delivered_at` | missing on some delivered trips (Q13) → NULL + `missing_delivered_at`; the row still loads and stays out of delivered-axis invoices, as today; never derived |
| `billingCustomerId`, `billingDate` | `trip_records.billing_party_id`, `billing_date` | stamped on unpriced trips too; unresolved party → `party_unresolved`; NULL `billingDate` on a plan-basis party → loaded NULL + `missing_billing_date` (D7, §A.4) |
| `billingEstimateThb` … `billingReferenceFuelPriceThb`, `billingManualOverride`, billing `jobCategory` (Q15) | `trip_billing_snapshots` | row created iff `billingEstimateThb` is a number **or** any `billing*` field is present ("stamped but unpriced"); `billingRateImportId` → `rate_import_id` and resolved to `rate_entry_id` by (`import_id`, party, hub, destination, class, category) when unique; `billingFuelAdjustmentId` → `fuel_adjustment_id` via legacy doc id; `*DateStr` → `date`; explicit JS `null`s → SQL NULL; `computed_by='etl'`, `compute_version=0`, `last_event_id` NULL. Legacy prices are never recomputed by the ETL (R15: priced rows untouched). Multi-drop trips priced as single (`isMultiDelivery` true, `billingIsMultiDelivery` not true) → loaded as-is + `multidrop_priced_as_single` |
| `billingMultiDeliveryBreakdown[]` | `trip_billing_stop_breakdown` | |
| `deliveryStopsProgress[]` (completion order, Q16) | `trip_delivery_stops` | `completed_seq` = array position; `photos[]` inside a stop → `trip_photos.stop_id` |
| `photos[]` `{url, type, geocoding{lat, lng, address, timestamp Date\|ISO}}` (Q17) | `trip_photos` | R19: `photo_type` stored verbatim; `photo_type_known = (type ∈ TRIP_PHOTO_TYPE_ENUM or matches stop_{n}_(arrived\|pre_open\|opening\|empty_container\|runsheet_received))`; unknown types load with `photo_type_known=false` + `photo_type_unknown` (ADR 0018: `photos[]` is the whole evidence set). Duplicate types → last wins (merge-by-type). URL rule §A.3.0; objects stay under the old trip folder after a rename |
| `renamedFromTripId/At/By` | `trip_no_history` | |
| `evidenceToken`, `lineDeliveredNotifiedAt`, `deliveredVia`, review fields | as named | evidence tokens load unchanged and stay non-expiring but revocable (`evidence_token_revoked_at` NULL; `POST /v1/trips/{id}/evidence/revoke`; R30, R47; `EVIDENCE_TOKEN_TTL_DAYS` default `0`) so LINE cards already sent keep opening |
| `std`, `sta`, `ata` (`ata` written null at create) | as named | |
| `createdAt` (device time, Driver Monitor "Depart" axis) | `created_at` | R19, R68: `created_at := COALESCE(createdAt, std, deliveredTimestamp, Firestore create time)` (`etl.source_docs.source_create_time`); a derived value adds `created_at_derived`; never rejected for this field |

### A.3.7 Files: URL → key (summary)

The rule is §A.3.0 "File URL → object key". Per-collection URL fields and their `file_objects.purpose` (vocabulary of §A.2.1): trips `photos[].url`, also inside `deliveryStopsProgress[]` (`trip_photo`); tasks `checkInPhotoUrl` / `checkInAppScreenshotUrl` (`checkin_photo`, `checkin_app_screenshot`); standby `photos[].url` (`standby_photo`); incidents map and situation images (`incident_photo`); chat `imageUrl` (`chat_image`); leave `attachments[]` (`leave_evidence`); maintenance `images[]` / `receipts[]` / `invoiceUrl` (`maintenance_file`, with `maintenance_files.kind` `image` / `receipt` / `invoice`); expenses receipt and odometer (`expense_receipt`, `expense_odometer`); penalties evidence (`penalty_evidence`); drivers profile, ID card and licence (`driver_profile`, `driver_id_card`, `driver_license` — private, presigned only, closing today's public read of ID-card images); trucks images, tax / registration documents, receipts, `insuranceDocuments[]` and legacy `images[]` (`truck_photo`, `truck_document`, `truck_receipt`, `insurance_document`); subcontractors `documents[]` (`tenant_document`); customers `logoUrl` (`customer_logo`); companies logo, stamp and signature (`company_logo`, `company_stamp`, `company_signature`); the APK of `settings/mobile_app` (`apk`, public bucket, the only `visibility='public'` purpose). Users' `photoUrl` is not registered (§A.3.1). `media-copy --verify` compares size and sha256; the reconciliation report lists referenced keys vs objects (HEAD); `rewrite-urls` reports any Firebase URL left in a `text` / `jsonb` column (main spec §13.6).

### A.3.8 standby_records, incidents, legacy standby trips

| Source | Target | Transform |
|---|---|---|
| standby `driverId` (auth uid; admin backfill writes `authId \|\| docId`) | `driver_id`, `legacy_driver_ref`, `driver_ref_match` | §A.3.0; `client_op_id` NULL for legacy rows (R63) |
| `customerId`, `customerResolved`, `customerResolvedFrom` | `customer_party_id`, `customer_resolved`, `customer_resolved_from` | loaded as stored (unresolved id → `party_unresolved`). Go keeps the legacy resolution that ignores `task.billingCustomerId` for standby (`standbyBilling.ts:92-94`, billing divergence #9) for parity (R19) |
| `startedAt` / `endedAt`; migrated rows have `endedAt = trip.updatedAt` (ADR 0008) | `started_at`, `ended_at` | `migratedFromTripId/SpxTripId` → `migrated_from_trip_no`; missing `endedAt` → NULL + `missing_ended_at` (unpriced reason `no_ended_at`), never derived |
| `photos[] {url, type}` | `standby_photos` | URL rule |
| billing fields incl. `billingRateEntryId == 'service_fee'` | `billing_rate_source='service_fee'`, `billing_rate_entry_id NULL`; otherwise `'standby_rate'` + FK by legacy doc id | amounts per §A.3.0 money rule |
| legacy `trip_records.status=='standby'` not migrated by `migrate-standby-trips.js` (Q5) | `trip_records(status='standby')` + `legacy_standby_trip` | ETL does not synthesise `standby_records` (start/end would be approximations); the owner runs the existing Firestore migration before the dump or accepts these as unbilled |
| incidentReport `driverId` (auth uid; admin modal `context.driverId`) | `driver_id` … | §A.3.0 |
| incidentReport `tripId` (may be a pre-rename id) | `trip_id` | `trip_records.legacy_doc_id`, then `trip_no`, then `trip_no_history.old_trip_no`; else `legacy_trip_ref` + `trip_unresolved` (tenant from the driver) |
| `truckLicensePlate` (mobile) vs `truckPlate` (admin) | `truck_license_plate_snapshot` | either key |
| `reportedBy:'admin'`, `reportedByUid` | `reported_by_kind`, `reported_by_user_id` | |

### A.3.9 Billing tables

| Source | Target | Transform |
|---|---|---|
| `customer_rate_entries.customerId` | `billing_party_id` | customers, then subcontractors; unresolved → `rejected` + `party_unresolved` (`billing_party_id` is NOT NULL and a rate row without a party can never be selected; trip snapshots priced against it keep their stored amounts and `rate_import_id`, with `rate_entry_id` NULL). Same rule for fuel adjustments and standby rates |
| `hubId` (upper), `destinationCode` (already normalised; SPK collision baked in), `vehicleClass` | `hub_code`, `destination_code`, `vehicle_class` | `vehicle_class := normalizeVehicleClass(stored)` — the normalisation both writers already apply at match time (`billingCompute.ts:283,290`); stored `'4WJ'` defaults stay as stored (R15 applies to trips, not cards) |
| `effectiveFrom` + `effectiveFromDateStr` | `effective_from_date`, `effective_from_at` | Bangkok rule in §A.3.0 |
| `jobCategory` absent | `'PRIMARY'` | the one place a default is correct: the selector already coerces it (`billingCompute.ts:15,291`) |
| `voided`, `voidedAt`, `voidedBy`, `voidedReason` | same | `voidedBy` uid → user. The void-only trigger also whitelists `updated_at`, as `firestore.rules:144` whitelists `updatedAt` (read in the repo) |
| same-day ties | `effective_from_at` + `legacy_doc_id` | R16 ordering, §A.3.0 |
| `customer_fuel_rate_adjustments` | same pattern | `referenceFuelPriceThbPerLitre` → `reference_fuel_price_thb`; customer-level only, no fallback (billing report) |
| `standby_rate_entries` | `standby_rate_entries` | browser-local `effectiveFrom` → `bkk_date` (UNVERIFIED, §A.3.0); `voided_at` NULL (no legacy void concept; soft delete from cut-over, R20) |
| `customer_service_fees`, duplicates per (customer, feeType) (Q25) | all rows loaded | D5 (§A.4): duplicates flagged `duplicate_service_fee`; until the owner confirms winners the Go selector reproduces legacy winners — `extra_stop` first by `legacy_doc_id ASC` (`.find`, `recompute-trip-billing.js:93`), `standby` last (`buildStandbyServiceFeeMap`, `standbyBilling.ts:49-59`); the UNIQUE index comes with 0010 |
| `billing_statements.period{month, year}` | `period_year`, `period_month` | |
| WHT computed with a hard-coded 0.01 | `withholding_tax_rate = 0.0100` | R18: legacy totals loaded as stored (half-up to 2 dp), never recomputed; new statements snapshot `companies.withholding_tax_rate` at creation; documents render from the statement |
| `invoiceNumber ?? doc.id` (`functions/src/core/billingPeriodLock.ts:92`, read in the repo) | `invoice_number` | |
| `status` draft/sent/paid/cancelled | `status` | sent/paid rows are the period locks read in-transaction by pricing (R17) |
| `billing_counters/{customerId}_{YYYYMM}` `{lastSeq, customerCode}` | `billing_counters` | doc id split on the last `_` |
| statement row linkage | `billing_statement_lines` | empty for legacy statements (never persisted); populated from cut-over |

### A.3.10 Finance, HR, comms, platform

| Source | Target | Transform |
|---|---|---|
| `vehicle_expenses.driverId` (auth uid; web toll import `authUid ?? docId`) | `driver_id` … | §A.3.0; no driver → truck tenant; `client_op_id` NULL for legacy rows (R63); negative `amount` → `negative_money` |
| `date`; `refillLocation 'lat,lng'`; legacy `gasStation` | `expense_at`; `refill_lat/lng`; `station_tax_id` | |
| `status` PENDING/APPROVED/REJECTED | lower | |
| duplicate `(driverId, taxInvId)` fuel rows | all loaded unchanged | D5: flagged `duplicate_natural_key`; the UNIQUE index is created by 0010 after owner sign-off (DM's "null the losers' `tax_inv_id`" is not applied) |
| maintenance `status` `'PM Booking'`, `'Scheduled'`, `'In-Progress'`, `'in_progress'`, `'completed'`, `'cancelled'` | `pm_booking`, `scheduled`, `in_progress`, `completed`, `cancelled` | `startDate`/`endDate` strings → `date`; `images[]`, `receipts[]`, `invoiceUrl` → `maintenance_files(kind)`; `invoiceAmount` → `invoice_amount_thb` |
| transactions shape (a) renewal `{truckId, type 'tax'\|'insurance', subType, amount, paymentMethod, date 'yyyy-MM-dd', receiptUrl, performedBy display/email, notes}` | `transactions(tx_type, …, performed_by_label, tx_date_source='bangkok')` | tenant from the truck |
| transactions shape (b) payout `{type 'driver_payout', date UTC yyyy-MM-dd, driverId auth uid, payoutId, payPeriod, round, performedBy uid}` | `transactions(tx_type='driver_payout', tx_date_source='legacy_utc', payroll_run_id, pay_period, pay_round, performed_by_user_id)` | date kept as written (`driverCompensation.ts:384`, divergence #11); loaded in the same transaction as payroll (deferred FK) |
| `payroll/{authId}_{YYYY-MM}_{R1\|R2}` | `payroll_runs` | doc id parsed for `pay_period`/`pay_round` and cross-checked with fields; `driverId` (auth uid) → `driver_id` (keying by `driver_id` fixes `driverCompensation.ts:199-204`); `status` DRAFT/PENDING_APPROVAL/APPROVED/PAID/CANCELLED → lower_snake; `lineItems[]` → `payroll_line_items` (`line_no` = array index, `item_type` lower, `category` as written; ADR 0013 optional fields nullable); `penaltyApplications[]` → `payroll_penalty_applications`; `ledgerTransactionId` → FK. Line items of non-draft runs are inserted before the run's final status is set (the frozen trigger permits inserts only while `draft`), all inside one load transaction |
| `driver_penalties` (auth uid; int THB) | `driver_penalties` | `remainingThb > totalThb` → `rejected` + `bad_number` (CHECK would fail; fix at source) |
| `driver_compensation_config` `fuelIncentiveTiers[]`, `tripVolumeTiers[]`, `sso{}`, `penaltyTypes[]` | jsonb tiers; `sso_*` columns; `penalty_types` rows | `effectiveFrom` → `effective_from_at` |
| chats `driverId` (auth uid); `lastReadByAdmin{uid: ts}`; `lastReadByDriver` | `chats.driver_id`; `chat_read_state` rows | driver's read state keyed by `drivers.user_id`; an admin uid without a user → `user_unresolved`; more than one non-closed chat per driver → all loaded as-is + `duplicate_natural_key`; the partial unique index (0010) is created after the owner merges them |
| `chats/{id}/messages {senderId, senderRole, text, imageUrl, createdAt, type}` | `chat_messages` | `legacy_doc_id` unique per chat; `senderId` uid → user (`user_unresolved` when none); `imageUrl` → file; `client_message_id` NULL for legacy messages (R63) |
| broadcasts `readDriverAuthIds[]`, `readCount` | `broadcast_reads` rows; `readCount` dropped | uid → user (an unresolved reader is skipped + `user_unresolved`); `title` missing → NULL; `tenant_id` NULL; `broadcast_recipients` stays empty for legacy rows (recipients were never persisted), so `GET /v1/broadcasts` treats a legacy row without recipients as addressed by its `recipient_group` |
| security_events | `security_events` | `actorUid` → `actor_user_id` (else `actor_legacy_uid` + `user_unresolved`); `details.targetUid` → `target_user_id` when resolvable; `type` → `event_type`; `tenant_id` NULL |
| `settings/mobile_app` (two writers, Notes 0008) | `settings('mobile_app')` = floor half `{minAllowedVersion, minAllowedVersionSetAt, minAllowedVersionSetBy}` + release half `{latestVersion, latestBuildNumber, apkDownloadUrl, flavor, releasedAt}`; one `mobile_app_releases` row from `{latestVersion, latestBuildNumber, apkSizeBytes, apkSha256, flavor, releasedAt, releasedBy, releaseNotes}` | APK object → `file_objects` (`S3_PUBLIC_BUCKET` value, `visibility='public'`, `purpose='apk'`); the legacy `latestVersion` is strict semver already (the publish script parses `MAJOR.MINOR.PATCH` out of `pubspec.yaml`, `logitrack-web/scripts/publish-mobile-release.mjs:85-90`), which the `mobile_app_releases.version` CHECK requires |
| `fuel_daily_snapshots/{yyyy-MM-dd}`, `fuel_monthly_snapshots/{yyyy-MM}` | by key | monthly `status:'error'` rows loaded as-is (the clobber bug is fixed in Go, not by ETL) |
| holidays `date` Timestamp; `type` PUBLIC/COMPANY/OTHER; `status`; names | `holiday_date := bkk_date(date)`; `holiday_type`/`status` lower; `tenant_id` NULL for public, own fleet otherwise | the `id` field `saveHoliday` writes into the doc is ignored |
| leave_requests `driverId` (doc id, inferred; schemas report UNCERTAIN); `startDate`/`endDate` Timestamps; `type`, `status`; `attachments[]` | `driver_id` (doc id first, then auth uid); `start_date/end_date := bkk_date(...)`; lower vocabularies; `leave_request_attachments` | `approverId` → `approver_user_id` (`user_unresolved` when none); `approvedAt` → `decided_at`; `client_op_id` NULL for legacy rows (R63) |
| waitlist, partner-interest | `waitlist`, `partner_interest(payload)` | |
| `checkin` collection; Firestore named DB `trucks` (`firebase/server.ts`, dead) | not loaded | `dropped`; production counts verified before cut-over (`checkin` has no reader or writer, schemas report line 162 per DM) |

### A.3.11 Reconciliation (exit criteria per run)

`etl reconcile --period=YYYY-MM` (main spec §13.10) writes `etl.reconciliation_runs` and a markdown/CSV report; exit code 2 on any mismatch outside tolerance:

- per collection: Firestore count = loaded + quarantined + rejected + dropped (§A.3.0);
- Σ `billingEstimateThb` per (billing party, Bangkok period on the party's axis: `billingDate` for plan-basis, `deliveredTimestamp` otherwise) and unpriced counts per reason (`no_customer`, `no_rate`, `no_vehicle_class`, `no_billing_date`, R62); standby Σ per (party, `endedAt` month) and `no_ended_at` count;
- statement totals vs stored `tripSubtotal + standbySubtotal + multiDropSubtotal` (reported, not failed: client-computed at generation);
- payroll Σ `netPay` per (period, round, status); expenses Σ per (type, status, month);
- media: referenced keys vs MinIO objects (HEAD), `missing_at_source` count;
- quarantine-tenant row count per table (also published by the `tenancy.orphan-scan` job, R11) and open `etl.quarantine` findings per reason code (§A.3.0), compared with the previous run: a new code or an unexplained delta blocks the cut-over step (main spec §13.9).

Money comparisons are exact except the legacy multi-drop total and `netAmount` (0.005 THB, R20).

## A.4 Data-model decisions D1-D8 (resolved)

The data-model design left eight owner decisions (DM §4). The plan (R1–R35) and the resolutions (R36–R90) settle the mechanism of all eight; the remaining owner inputs are open questions of [main spec](../../../developer-spec.md) §19.2 ("question N"; "Qn" elsewhere in this appendix is a legacy quirk of the fact-base reports).

| # | Decision | Status | Resolution as implemented in this appendix |
|---|---|---|---|
| D1 | Default vehicle class `'4WJ'` for a blank `task.truckType` (`normalizeVehicleClass('')`, `billingCompute.ts:147-148`, critic-confirmed) vs the MANDATORY "never guess a class" rule | **Resolved — R15, R62** | No trip-level default: `tasks.truck_type` stays NULL (§A.3.5) and pricing records `unpriced_reason='no_vehicle_class'` (R62). Rate-card rows keep `normalizeVehicleClass` folding and stored `'4WJ'` values (§A.3.9). The golden vector `'' → 4WJ` becomes an explicit expectation on the P3 parity allow-list (R72); legacy priced snapshots are untouched. Recorded in ADR 0029 |
| D2 | Canonical lower_snake status vocabularies vs legacy literals | **Resolved — R30, R65, R25** | Every status/type vocabulary is lower_snake in the DB; domain codes keep their spelling (vehicle classes, `PRIMARY`/`SUPPLEMENTARY`, `PM`/`CM`, `R1`/`R2`, ADR 0013 payroll categories). The projection and the v1 mobile shim own the one translation table to the legacy literals until P8 (main spec §13.8); an unmapped literal is `status_out_of_vocab` |
| D3 | Firebase Auth passwords: lazy scrypt verify + Argon2id rehash vs forced reset | **Resolved — plan §2, R23, R29, R30, R55; hash parameters: question 1** | `users.legacy_scrypt_hash` / `_salt` (bytea) hold the exported pair; the first successful login verifies, rehashes with Argon2id and NULLs the pair (Appendix C §C.5.3–§C.5.4). Parameters from the Firebase Console into `FIREBASE_SCRYPT_*` (R23); weak defaults flagged `must_change_password` by `auth-weak-scan` (R29); unused pairs deleted by `auth-tail` 180 days after the P7b gate (R30) |
| D4 | Truck plate uniqueness per tenant vs platform-wide | **Owner decision — question 15** | Shipped default: `UNIQUE (tenant_id, license_plate)` on `trucks` (§A.2.2): a broker carrier's plate may appear under two carriers and `trucks.id` is the identity (legacy quirk Q19). Platform-wide uniqueness is one index plus a duplicate report |
| D5 | Legacy duplicates that block new UNIQUE constraints: service fees per (party, type), same-day rate entries, fuel `(driver, taxInvId)`, several open chats per driver | **Mechanism resolved — R11, R19, R31, R59, R88; winners: question 16** | ETL loads every row and flags `duplicate_service_fee` / `duplicate_natural_key`; same-day rate entries need no constraint (R16 ordering). Until sign-off Go reproduces the legacy winners (`extra_stop` first, `standby` last by doc id); afterwards the losers are corrected through the API and `0010_d5_unique_constraints.sql` (below) creates the three UNIQUE indexes. T04 authors the file; applying it is a step of the P1 initial-load runbook (T24) after the quarantine sign-off (R88): `goose up-to 9` → ETL → sign-off → `goose up` (R59). Dev, CI and seeded databases apply it at once; CI job `etl-fixtures` applies it after the fixture loser corrections (R31) |
| D6 | Tenant of legacy tasks with no resolvable driver, and the dispatcher axis | **Resolved — R6, R11, R13, R56, R86; re-home target: question 9** | Driverless legacy tasks load into the fixed-id quarantine tenant (`tenant_source='quarantine'`, no `etl_creator`) and are bulk re-homed through `POST /v1/tenants/quarantine/rows/{table}/{id}/rehome` once the owner confirms the target (recommended: own fleet). Dispatchers hold a membership in their own tenant (TTP is both a customer and a carrier tenant, R75), so `POST /v1/tasks` stamps `form`; cross-carrier visibility is `user_scopes(kind='dispatcher')` on `billing_parties.id`; cross-tenant reassignment is an audited `PATCH /v1/tasks/{id}` with `tenantId` |
| D7 | `billing_date` of CJSF trips priced before ADR 0027 (plan-basis party, NULL `billingDate`) and delivered trips without `deliveredTimestamp` | **ETL default resolved — R19, R62, R68; restamp policy: question 17** | Load as stored, never derive: `missing_billing_date`, `missing_delivered_at` (§A.3.6); pricing records `no_billing_date`, never `Date.now()` (R62). The Firestore repair (Billing Document "คำนวณใหม่ตามใบงาน" / `fetchTripsMissingBillingDate`, CLAUDE.md #47) runs per month before the final dump; leftovers are restamped by Go after P3 under the in-transaction period lock (R17); sent/paid periods are reported as `billing_date_locked` for credit-note handling. Alternative kept open: ETL stamps `billing_date := task.plan_at` where the period is unlocked |
| D8 | Withholding tax: statements hard-code 1 %, documents read `companies.withholdingTaxRate` (default 3), PDF label 1 % (billing divergence #6) | **Resolved — R18, R61, R69; rate: question 6** | `billing_statements.withholding_tax_rate NUMERIC(5,4)` is copied from the issuing company at creation, on the billing carrier's tenant (R61); legacy statements load with `0.0100` and stored totals (§A.3.9); documents render server-side from the statement into `statement_documents` from P3 (R69), so a re-download equals the original. The owner confirms 1 % for transport services before the seed |

**Follow-up migration for D5**

```sql
-- 0010_d5_unique_constraints.sql
-- +goose NO TRANSACTION
-- Authored in issue T04 with the baseline; applied as a step of the P1 initial full-load runbook (issue T24) after the
-- owner signs off the ETL quarantine report (R59, R88).
-- Production: goose up-to 9 -> ETL -> quarantine sign-off -> goose up.
-- Dev / CI / seed: applied right after 0009 (no legacy duplicates).
-- CREATE INDEX CONCURRENTLY cannot run inside a transaction block (R31); each statement runs on its own.

-- +goose Up
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS vehicle_expenses_fuel_taxinv
  ON vehicle_expenses (driver_id, tax_inv_id) WHERE expense_type = 'fuel' AND tax_inv_id IS NOT NULL;
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS chats_one_open_per_driver
  ON chats (driver_id) WHERE status <> 'closed';
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS customer_service_fees_one_per_type
  ON customer_service_fees (billing_party_id, fee_type) WHERE fee_type <> 'custom';
-- IF NOT EXISTS also accepts an INVALID index left by an earlier failed run: refuse to continue in that case.
-- +goose StatementBegin
DO $$
DECLARE bad text;
BEGIN
  SELECT string_agg(c.relname::text, ', ') INTO bad
    FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
   WHERE c.relname IN ('vehicle_expenses_fuel_taxinv','chats_one_open_per_driver','customer_service_fees_one_per_type')
     AND NOT i.indisvalid;
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'D5 unique index(es) % are INVALID: duplicates survived the sign-off; DROP INDEX CONCURRENTLY, fix the rows, rerun goose up', bad
      USING ERRCODE = 'unique_violation';
  END IF;
END
$$;
-- +goose StatementEnd
DROP INDEX CONCURRENTLY IF EXISTS vehicle_expenses_fuel_taxinv_lookup;   -- superseded by the unique index

-- +goose Down
CREATE INDEX CONCURRENTLY IF NOT EXISTS vehicle_expenses_fuel_taxinv_lookup
  ON vehicle_expenses (driver_id, tax_inv_id) WHERE expense_type = 'fuel' AND tax_inv_id IS NOT NULL;
DROP INDEX CONCURRENTLY IF EXISTS customer_service_fees_one_per_type;
DROP INDEX CONCURRENTLY IF EXISTS chats_one_open_per_driver;
DROP INDEX CONCURRENTLY IF EXISTS vehicle_expenses_fuel_taxinv;
```

`CREATE UNIQUE INDEX CONCURRENTLY` fails on a surviving duplicate and leaves an INVALID index; `IF NOT EXISTS` would skip that name on a rerun, so the `DO` check stops the migration until the operator drops the invalid index, fixes the rows (the runbook re-runs the D5 duplicate queries first) and reruns `goose up` (verified on `postgres:18-alpine`). The lookup index is dropped only after all three unique indexes are valid. 0005 creates only the non-unique `customer_service_fees_party_type`, so the ETL can load legacy duplicates. The file adds no table, hence no grant and no Appendix C §C.3.0 row; later schema changes start at 0011.
