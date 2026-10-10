# Appendix D — Seed design and mock data

> Part of the mv-go migration spec (Firebase -> Go 1.27 + Fiber v3 + PostgreSQL 18 + RabbitMQ + Redis + MinIO). Main spec: [developer-spec.md](../../../developer-spec.md) — §14 summarises this appendix, §15 is the local stack that runs it, §16 is the canonical environment table, §6 owns the billing and compensation rules the golden vectors below exercise, §13 owns the ETL. Siblings: [Appendix A](./A-data-model.md) (DDL; every table and column name here follows it), [Appendix B](./B-api-catalog.md) (endpoints, queues, SSE topics, Redis keys), [Appendix C](./C-auth-rbac.md) (roles, capability catalog, RLS), [Appendix E](./E-web-fetch-audit.md) (web fetch audit). Decision record: [ADR 0029](../../adr/0029-migrate-firebase-stack-to-go-postgres.md).
>
> Status: `logitrack-api/cmd/seed` implements this appendix since T16 (issue #36): the smoke fixture is `cmd/seed/testdata/smoke/<table>.json` with the registry `cmd/seed/testdata/registry.json` (both win on conflict with this copy), the invariants of §D.3 run on PostgreSQL 18 in `cmd/seed/internal/seed/invariants.go`, and the demo / load profiles carry the T16 identity generator while the volume generators join with T26, T35 and T43. `logitrack-api/cmd/etl` loads the ETL fixtures of §D.5 since T15 (issue #35), in the layout §D.5.1 describes; the cases still to add are listed in §D.5.2. Names follow the final Appendix A DDL: a checker parsed its `CREATE TABLE` statements and confirmed that every sample key is a column or an `_` annotation, every NOT NULL column without a default is present, every simple `CHECK (col IN ...)` holds and every `@SYM` resolves. Applied: R1–R35 and R36–R90, notably R55 (`users.legacy_auth_uid`, `auth_identities`), R56 (`tenants.name_th`, `contractor_tenant_id`; the quarantine tenant with the fixed id `00000000-0000-7000-8000-00000000000f` comes from migration 0002), R57 (only `outbox_events.id` is an identity), R61 (billing rows on the billing carrier's tenant), R62 (stored unpriced reasons), R63 (`client_op_id`), R64 (`jobs.params`), R65 (lower_snake statuses), R68 (reason codes), R74 and R87 (bucket and database-URL names).

The seed has three jobs: (1) give every developer and CI run the same database, object store and Redis state in seconds; (2) cover every workflow state the Go services must handle, including the legacy shapes the ETL produces; (3) act as executable golden vectors — every priced row in §D.4 states the formula and expected result, and `seed --verify` recomputes them. The seed never touches Firestore, never runs against production, and is separate from the ETL, whose fixtures are in §D.5.

---

## D.1 Seed design

### D.1.1 `cmd/seed` command

`cmd/seed` builds into the same image as `api`, `worker` and `scheduler` (binary `/app/bin/seed`; delivery plan local stack, main spec §15). The compose service `seed` (profile `tools`) runs `/app/bin/seed --profile ${SEED_PROFILE}`; `make seed`, `make seed-verify` and `make reset` (down -v, up, migrate, seed) wrap it.

| Invocation | Effect |
|---|---|
| `seed --profile smoke` | Loads the fixture of §D.4 verbatim (58 tables, 286 rows incl. the quarantine tenant of migration 0002, 16 objects). CI default. Fixture files live in `logitrack-api/cmd/seed/testdata/smoke/<table>.json`; §D.4 is their review copy and the fixture files win on conflict (the PR that changes one changes both). |
| `seed --profile demo` | Smoke rows plus a generated three-month dataset ending at `SEED_ANCHOR_DATE` (volumes in §D.1.2). Used for local development, demos and Playwright e2e. |
| `seed --profile load` | Demo plus a twelve-month dataset at roughly ten times today's volume, for query-plan, keyset-pagination and billing-period performance work. Never run in CI. |
| `seed --verify [--profile p]` | Read-only. Runs the twelve checks of §D.3 against PostgreSQL, MinIO and Redis and compares table counts with the profile manifest. Exit `0` pass, `1` invariant violation, `2` dependency unreachable. |
| `seed --reset` | Default when `APP_ENV=local` (developer machines and CI; §16 defines `local`, `dev`, `prod`). Purges seeded state before loading (§D.1.7). |
| `seed --mode upsert` | Inserts only missing rows (`ON CONFLICT DO NOTHING`), never updates; for topping up a shared dev database in the `SEED_NAMESPACE` it was seeded with (§D.1.3; a database seeded in another namespace is refused before anything is written). The engine check covers the priced rows it inserts, and objects whose existing row records other bytes are kept (§D.1.7). |
| `seed --emit-events` | Leaves the seeded `outbox_events` rows unpublished so the `scheduler` relay publishes them and a running `worker` consumes them (end-to-end smoke of `billing.compute`, `notify.fcm`, `documents.render`). Without it, outbox rows are written as already-published history and no side effect fires. |
| `seed --dry-run` | Builds the plan, prints per-table counts, writes nothing. |
| `seed bootstrap-platform-admins` | Grants `user_platform_roles(platform_admin, granted_by NULL)` to every existing user listed in `PLATFORM_ADMIN_EMAILS` and appends a `platform_role_granted` security event (`{source:'bootstrap'}`); idempotent (Appendix C §C.5.5). The only subcommand allowed with `APP_ENV=prod`. Lands with T19 together with the bootstrap super admin of a fresh deployment (owner addition to issue #39); until then it exits 3. The profiles never create the deployment's super admin: they carry the fixture's `U_PLAT` only. |

**Environment** (names only; values never appear in this repository; canonical table with secret flags and owning process in main spec §16):

| Name | Use in `cmd/seed` |
|---|---|
| `SEED_PROFILE` | `smoke` \| `demo` \| `load` (flag `--profile` wins). |
| `SEED_RANDOM_SEED` | PCG seed for every generator (§D.1.4). |
| `SEED_ANCHOR_DATE` | Last day of generated data (Bangkok date); ignored by `smoke`, whose dates are literal. |
| `SEED_NAMESPACE` | uuid v5 namespace (§D.1.3); empty means the fixed default. |
| `SEED_DEFAULT_PASSWORD` | Secret, local/CI only. Hashed with Argon2id once per load (every seeded password user shares that PHC string, which keeps the 1,200-user load fast); never printed or logged. The must-change-password fixture `U_D7` gets a crypto/rand temporary password instead (`internal/auth/password.Temporary`, as `POST /v1/users/{id}/password/temporary` issues one), printed once on stdout only by the load that inserts the row; an upsert that finds `U_D7` leaves its password alone and says so. It is never derived from `SEED_RANDOM_SEED` or `SEED_NAMESPACE`: both are public, so a derived value could be recomputed and the account taken over on a shared dev database (R29, R79). |
| `OWN_FLEET_TENANT_ID` | Optional; when set, the own-fleet tenant row uses this uuid instead of the derived one (R7, R56: read only by `cmd/seed` and `cmd/etl`). The quarantine tenant is never seeded: migration 0002 inserts it with its fixed id. |
| `APP_ENV` | Safety guard and Redis key prefix `lt:{APP_ENV}:` (R26). |
| `ETL_DATABASE_URL` | Writes and the read-only `--verify` checks (`logitrack_etl`, R87). |
| `DATABASE_URL` | `--verify` isolation role-play as `logitrack_app` (#10c, #10d; R87). |
| `MIGRATE_DATABASE_URL` | `--reset` `TRUNCATE` and the schema-version check (owner; `logitrack_etl` has no `TRUNCATE`, Appendix A §A.2.8). |
| `REDIS_URL` | Key purge (§D.1.7) and the hub-map check (#8). |
| `STORAGE_BACKEND`, `S3_ENDPOINT`, `S3_REGION`, `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY`, `S3_BUCKET`, `S3_PUBLIC_BUCKET`, `S3_PUBLIC_BASE_URL`, `S3_USE_PATH_STYLE`, `S3_USE_SSL`, `LOCAL_MEDIA_DIR`, `LOCAL_MEDIA_PUBLIC_BASE_URL` | Placeholder media (§D.1.6) go through `internal/storage` to the backend of `STORAGE_BACKEND` (compose: `s3`, MinIO), and `file_objects.storage_backend` records it (main spec §9.11): `S3_BUCKET` private (local `logitrack`), `S3_PUBLIC_BUCKET` public (local `logitrack-public`, `app_releases/` only) (R23, R74), both also the labels of local rows; the backend's public base (`S3_PUBLIC_BASE_URL` or `LOCAL_MEDIA_PUBLIC_BASE_URL`) builds the APK link. The seed signs no URL, so `S3_PRESIGN_ENDPOINT` and `LOCAL_MEDIA_SIGNING_KEY` are not read. |
| `FIREBASE_SCRYPT_SIGNER_KEY`, `FIREBASE_SCRYPT_SALT_SEPARATOR`, `FIREBASE_SCRYPT_ROUNDS`, `FIREBASE_SCRYPT_MEM_COST` | Locally and in CI these hold the public test parameter set of the `firebase/scrypt` repository (its `tests/01-known-value.sh` vectors, Appendix C §C.5.3), never the production parameters. The seed uses them to produce one legacy Firebase-scrypt hash so the verify-then-rehash login path (main spec §4) runs locally. |
| `ARGON2_MEMORY_KB`, `ARGON2_ITERATIONS`, `ARGON2_PARALLELISM` | Argon2id parameters of every seeded password hash (the values the `api` verifies with). |
| `PLATFORM_ADMIN_EMAILS` | Read only by `seed bootstrap-platform-admins`; the profiles insert the fixture's platform role (`U_PLAT`) instead. |
| `PLAYWRIGHT_TEST_USER_EMAIL`, `PLAYWRIGHT_TEST_USER_PASSWORD` | Not read by the seed; e2e points them at `wrt.admin@logitrack.test` and `SEED_DEFAULT_PASSWORD`. |

**Safety guard.** Profile loads, `--reset`, `--dry-run` and `--verify` refuse `APP_ENV=prod` (only `bootstrap-platform-admins` will run there, T19); in `dev` they require `--allow-shared` and force `--mode upsert` (no truncation of a shared database; `--reset` is refused). Every e-mail address of a plan must be under `@logitrack.test` (the plan is refused otherwise), `SEED_DEFAULT_PASSWORD` is hashed once and never printed or logged, and each database URL is checked against its login role at start (`logitrack_etl`, `logitrack_migrator`, `logitrack_app`; none may be a superuser). It never connects to Firestore or GCS.

**Connections and row-level security (R66, R87).** Writes use `ETL_DATABASE_URL` (`logitrack_etl`, BYPASSRLS, the `cmd/etl` load path), the whole profile in one `db.WithSystem` transaction (R12), so a failed engine check (§D.1.4) rolls everything back. The seed never sets `app.etl_load`, so deferred checks such as `t_driver_link_membership` run (the load order satisfies them). `--verify` reads on the same login in a read-only transaction and role-plays isolation (#10c, #10d and the per-carrier check of §D.3 #10) on `DATABASE_URL` as `logitrack_app` with the context a request gets: `auth.RolePlayPrincipal` builds the user's principal from its memberships, platform roles and scopes exactly as an access token maps them, `iam.RBAC.Resolve` completes it (steward flag, contractor reach) and `db.WithPrincipal` sets the GUCs, so no `set_config` is written by hand (the WithSystem analyzer keeps raw GUC SQL in `internal/platform/db` and allows `auth.RolePlayPrincipal` only in `internal/auth` and `cmd/seed`, Appendix C §C.3.2); nothing uses `SET ROLE`. `FORCE ROW LEVEL SECURITY` stays on throughout.

### D.1.2 Profiles and volumes

`smoke` counts are exact (they are the §D.4 rows). The identity rows of `demo` and `load` are exact too (the T16 generator, below). The other `demo` counts adapt the delivery plan's "full" profile to two carrier tenants and the R24 additions and are targets of the volume generators (T26 master data, T35 operations, T43 billing and finance); until those land, `demo` and `load` carry the smoke rows for those tables. `load` counts are a sizing target of about ten times the delivery plan's estimate of today's volume, with users above the legacy `listUsers(1000)` cap so the keyset-pagination check of Appendix C §C.7 item 21 runs on seeded data. UNVERIFIED: real per-collection sizes (the estimate is not measured; owner question Q7, main spec §19.2).

| Table | smoke | demo | load | Notes |
|---|---:|---:|---:|---|
| tenants | 4 | 4 | 12 | own fleet + carriers NWR (broker, contractor of the own fleet, R60) and TTP (dispatcher org, no contractor link) + the quarantine row of migration 0002 (not seeded); load adds 8 carriers `L01`…`L08` (the even ones work for the own fleet). If the owner puts TTP in reach (Q13, main spec §19.2), set `TN_TTP.contractor_tenant_id` to `TN_OWN` and invert the TTP half of #10d |
| billing_parties | 5 | 6 | 30 | demo adds a tenant-kind party for TTP |
| customers | 4 | 4 | 12 | CJSF (plan basis), TTP, SPX, SPK |
| companies | 1 | 1 | 1 | owner (invoice issuer) |
| users | 12 | 23 | 1,199 | every role, every login path (§D.2 #23); demo adds 11 personas (below); load adds 147 per load carrier (tenant_admin, 2 managers, 144 operators), past the legacy 1,000-user listing cap |
| memberships | 11 | 19 | 1,195 | incl. one suspended (moved broker driver) |
| auth_identities | 5 | 5 | 5 | Google subject + legacy Firebase uids (Appendix A §A.3.1); the generated personas sign in with a password only |
| user_platform_roles | 1 | 2 | 2 | platform_admin; demo adds `support` (`platform.support@logitrack.test`, granted by `U_PLAT`) |
| user_scopes | 2 | 6 | 6 | customer + dispatcher; demo adds a dispatcher in the own fleet (over `BP_SPX`) and in NWR (over `BP_SPK`) and customer-scope users for SPX and SPK |
| role_capability_overrides | 1 | 1 | 1 | catalog itself lives in Go (R5/R6); demo's `nwr.operator` is the principal `RCO1` applies to; more overrides join with T26 |
| hubs | 9 | 27 | 404 | demo: 23 hubs + 4 SOC (incl. `0STANDBY`, skipped by distance rule) |
| hub_name_aliases | 7 | ~45 | ~800 | name→code only |
| hub_soc_distances | 2 | ~120 | ~2,400 | same-network pairs × 2 directions; synthetic haversine × 1.3 at 35 km/h |
| drivers | 7 | 15 | 300 | |
| trucks | 6 | 12 | 280 | plates match the `truckSchema` regex |
| truck_assignments | 4 | 15 | 350 | |
| tasks | 25 | ~900 | ~330,000 | every status; legacy unpadded numbers |
| task_delivery_stops | 5 | ~15 | ~9,000 | |
| trip_records | 21 | ~780 | ~300,000 | every status and billing case |
| trip_delivery_stops | 5 | ~15 | ~9,000 | |
| trip_photos | 4 | ~3,400 | ~30,000 | load: photos for 1 % of trips |
| trip_billing_snapshots | 17 | ~740 | ~285,000 | one per delivered trip, priced or with `unpriced_reason` (R62); engine-computed except legacy/manual rows |
| standby_records | 4 | 25 | 6,000 | |
| incident_reports | 1 | 12 | 3,000 | |
| customer_rate_entries | 10 | ~140 | ~5,000 | two rounds + voided + tie pair |
| customer_fuel_rate_adjustments | 6 | 8 | 120 | |
| customer_service_fees | 3 | 4 | 24 | |
| standby_rate_entries | 3 | 4 | 24 | |
| billing_statements | 4 | 4 | 132 | load: 12 parties × 11 closed months |
| billing_statement_lines | 9 | ~600 | ~280,000 | |
| vehicle_expenses | 4 | 180 | 60,000 | |
| maintenance_records | 3 | 6 | 1,500 | |
| payroll_runs | 3 | 22 | 7,200 | |
| driver_penalties | 4 | 6 | 600 | |
| chats / chat_messages | 2 / 3 | 6 / 60 | 3,000 / 60,000 | |
| broadcasts | 2 | 3 | 48 | |
| leave_requests | 3 | 8 | 1,200 | |
| holidays | 4 | 17 | 17 | fixed-date public holidays + one company draft |
| mobile_installations | 4 | 14 | 320 | |
| device_tokens | 3 | 11 | 300 | |
| file_objects | 16 | ~3,600 | ~31,000 | |
| outbox_events | 3 | ~1,500 | 0 | load writes no event history |
| jobs | 2 | 4 | 10 | |
| security_events | 1 | 12 | 2,000 | |

Time budgets: `smoke` load < 5 s (delivery-plan acceptance for T16; measured in T16 at about 0.5 s for the whole command against the testcontainers stack: role and schema checks, Argon2id at the `.env.example` cost, `--reset`, the 15 stored objects, the load and the engine check; asserted by `make seed-budget` in the go-ci `test` job, a run of the smoke test without the race detector, which slows hashing and encoding), `demo` < 2 min including ~3,600 objects once T35 adds them, `load` < 30 min using `COPY` once the volume generators exist (T16's identity-only load profile runs in about 0.5 s).

**Demo personas (T16, owner addition to issue #36).** `demo` makes multi-tenancy visible at once. On top of the smoke identities (`wrt.admin`, `wrt.manager`, `wrt.ops`, `nwr.admin`, `ttp.dispatch`, `cjsf.viewer`, the platform admin, and the broker driver `D6` whose NWR membership is suspended and TTP membership active, R24) the generator adds, with ids uuid v5 of `users:<email>` and `user_scopes:<email>:<kind>:<party code>` and the password `SEED_DEFAULT_PASSWORD`:

| Tenant | tenant_admin | staff | dispatcher (membership + `user_scopes` dispatcher) |
|---|---|---|---|
| own fleet `WRT` | `wrt.admin` (smoke) | `wrt.manager`, `wrt.ops` (smoke), `wrt.operator` | `wrt.dispatch` (operation_staff, over `BP_SPX`) |
| carrier `NWR` (contractor → own fleet) | `nwr.admin` (smoke) | `nwr.manager`, `nwr.ops`, `nwr.operator` | `nwr.dispatch` (operation_staff, over `BP_SPK`) |
| carrier `TTP` (no contractor link) | `ttp.admin` | `ttp.ops` | `ttp.dispatch` (smoke, over `BP_TTP`) |

Customer-scope users (no membership): `cjsf.viewer` (smoke), `spx.viewer`, `spk.viewer`. Platform: `platform.admin` (smoke), `platform.support` (`support`). `--verify` role-plays carrier principals, staff and drivers (§D.3 #10e): one per carrier tenant and role without a dispatcher grant (smoke: `nwr.admin`, `d7.kitti`; demo adds `nwr.manager`, `nwr.ops`, `nwr.operator`, `ttp.admin`, `ttp.ops`) plus every broker driver (`d6.amnat`, R24), whose own history in NWR is the one cross-tenant read the check allows.

### D.1.3 Deterministic identifiers: uuid v5 in the seed, `uuidv7()` at runtime

Every seeded `uuid` primary key is **uuid v5** of the string `"<table>:<natural key>"` in a fixed namespace:

- Default namespace `dd659aa7-e92b-55af-b6f0-51075452cf69` = uuid v5(`NAMESPACE_URL`, `"https://logitrack.test/seed/v1"`). `SEED_NAMESPACE` overrides it. A namespace separates databases (for example to check that nothing depends on the default ids); it does not let two seeded datasets share one database: the natural keys (tenant code, e-mail, hub `source_id`, invoice and trip numbers) and the singletons (`tenants_one_own_fleet`, `companies_owner_one`) are the same in every namespace. A load without `--reset` into a database whose own-fleet tenant is not the plan's (another namespace, or another `OWN_FLEET_TENANT_ID`) is refused with that reason before any object is written; top up a shared dev database in the namespace it was seeded with.
- Natural keys are business keys when one exists (tenant code, customer code, email, hub `source_id`, national ID, tenant + plate, `task_no`, `trip_no`, `invoice_number`, party code + rate `import_id` + route, bucket + object key) and owner + Bangkok timestamp otherwise. Keys that contain another entity's id (object keys) are hashed after that id is resolved. The registry in §D.4.2 lists all 227 keys.
- Worked example: uuid v5(namespace, `"tenants:own_fleet"`) = `661f033c-8980-5e51-b822-eb925ad2086a` (`TN_OWN`); `OWN_FLEET_TENANT_ID`, when set, replaces exactly this one id. The quarantine tenant `TN_QUAR` is the one row with a fixed, non-v5 id (`00000000-0000-7000-8000-00000000000f`, migration 0002, R56).

Runtime rows are different on purpose. Application inserts omit `id` and take `DEFAULT uuidv7()` — native in PostgreSQL 18, no extension, never `gen_random_uuid()` for new ids (main spec §3). Consequences the seed must respect:

1. **Ordering.** uuid v7 is time-ordered; uuid v5 is a hash. The rate-entry tie-break (R16) orders equal `effective_from_at` by `legacy_doc_id ASC` for legacy rows and by `created_at ASC, id ASC` for new rows. Seeded rows that share an `effective_from_at` always carry distinct `created_at`, so the hash order never decides a tie (`RE09`/`RE10` differ by five minutes).
2. **Provenance.** `uuid_extract_version(id)` (a built-in function in PostgreSQL 18) returns 5 for every seeded row and 7 for every runtime row (except the 0002 quarantine row); invariant 12 uses it, and a developer can always tell fixture rows from rows created by clicking through the app.
3. **Identity column.** `outbox_events.id` is the only identity column (R57). The seed never writes it; PostgreSQL assigns 1..n after `TRUNCATE ... RESTART IDENTITY`, so `trip_billing_snapshots.last_event_id` (`bigint`, no FK) can name it literally. `event_id`, `trip_no_history.id` and `security_events.id` are uuids.
4. **Composite keys.** Tables keyed by foreign or natural keys (`memberships`, `user_platform_roles`, `hub_name_aliases`, `device_tokens`, `mobile_installations`, `trip_billing_snapshots`, `trip_billing_stop_breakdown`, `billing_counters`, `task_number_counters`, `penalty_types`, `payroll_penalty_applications`, `broadcast_reads`, `settings`) get no surrogate id; every other table has a registered uuid `id`.
5. **Legacy-shaped ids.** Rows that stand for ETL-migrated data carry synthetic Firestore-shaped values: 20-character doc ids starting `seed…` in `legacy_doc_id`, 28-character auth uids starting `SEEDUID` in `legacy_driver_ref`, `users.legacy_auth_uid`, `drivers.legacy_auth_uid` and `auth_identities.provider_subject` (R55). They exercise the `doc_id` vs `auth_uid` resolution of R12 without real identifiers.

### D.1.4 Deterministic values

- **Randomness.** `math/rand/v2` PCG seeded from `SEED_RANDOM_SEED` (delivery-plan default `20260101`); each generator (tasks, trips, photos, expenses, …) derives its own stream from uuid v5 of its name, so adding a generator never shifts the others.
- **Calendar.** All instants are written with a fixed `+07:00` offset; every Bangkok-day column is computed with `bkk_date()` (Appendix A). `demo` covers anchor−2 … anchor months, `load` anchor−11 … anchor. `SEED_ANCHOR_DATE` defaults to `2026-09-30` (delivery plan); set it to today for live-looking boards. The smoke fixture is pinned to its literal July–September 2026 dates.
- **Prices are never typed for engine-computed rows.** The seed calls the Go billing engine (`internal/billing`, main spec §6) over the seeded rate tables and writes what it returns; in `smoke` the fixture value is the expected output and any difference aborts the load. Rows that stand for legacy writers are inserted verbatim and marked by `computed_by`: `etl` (legacy server or browser writer: `TR02`, `TR03`, `TR07`, `TR12`) and `manual_edit` (`TR13`). Payroll uses the compensation engine the same way.
- **Holidays.** `demo` and `load` load fixed-date Thai public holidays from a checked-in list; lunar-calendar dates are UNVERIFIED and must be copied from the official announcement before use (delivery plan). `smoke` uses three fixed-date holidays only.

### D.1.5 Load order and trigger-safe writes

Order (one `db.WithSystem` transaction for the whole profile, so the engine check of §D.1.4 can roll it back; one batched round trip per table):
tenants (own fleet first; carriers point at it through `contractor_tenant_id`; the quarantine row already exists) → users → auth_identities → file_objects → customers → billing_parties → companies → memberships, user_platform_roles, user_scopes, role_capability_overrides → hubs → hub_name_aliases → hub_soc_distances → trucks → drivers → truck_assignments → maintenance_records → customer_rate_entries, customer_fuel_rate_adjustments, customer_service_fees, standby_rate_entries → tasks → task_delivery_stops → trip_records → trip_no_history → trip_delivery_stops → trip_photos → standby_records → incident_reports → outbox_events → trip_billing_snapshots → trip_billing_stop_breakdown → billing_statements → billing_statement_lines → billing_counters → statement_documents → vehicle_expenses → driver_compensation_configs → penalty_types → driver_penalties → payroll_runs → payroll_line_items → payroll_penalty_applications → transactions → driver_advances → chats → chat_messages → broadcasts → broadcast_reads → leave_requests → holidays → mobile_app_releases → mobile_installations → device_tokens → settings → jobs → security_events; finally `task_number_counters` is derived (`last_seq` = highest three-digit `NNN` per `(task_type, plan_date)`, R10) so the next `POST` continues the sequence.

`file_objects.owner_id` has no foreign key, so objects load before their owners. Four forward references are written NULL first and back-filled in the same transaction: `drivers.current_assignment_id` ↔ `truck_assignments.driver_id`, `drivers.active_task_id` → `tasks`, `trucks.active_maintenance_id` ↔ `maintenance_records.truck_id`, `payroll_runs.ledger_transaction_id` ↔ `transactions.payroll_run_id` (deferred FK). The back-fill is an `UPDATE`, so `trg_set_updated_at` stamps those rows' `updated_at` with the load time (the only clock value of a load; the fingerprint of #12 leaves `updated_at` out). Every other omitted column whose default reads the clock gets the row's `created_at` (§D.4.1), and an omitted column with a random default (`uuidv7()`) is refused, so a fixture row without its id cannot slip through.

Trigger rules from Appendix A that shape the writes:

| Rule | Seed behaviour |
|---|---|
| `payroll_line_items` writable only while the run is `draft` | Insert every run as `draft`, then its lines and `payroll_penalty_applications`, then the ledger `transactions`, then `UPDATE` status / `approved_*` / `ledger_transaction_id` / `payment_*`. The trigger fires `BEFORE INSERT`, ahead of `ON CONFLICT`, so `--mode upsert` writes lines only under runs it inserted itself. |
| Append-only (`transactions`, `security_events`, `billing_statement_lines`, `trip_no_history`, `fuel_daily_snapshots`, `status_history`) | Insert only (`logitrack_etl` holds no `UPDATE` / `DELETE` on them, Appendix A §A.1.8); `--mode upsert` uses `ON CONFLICT DO NOTHING`. |
| Void-only announcements (`customer_rate_entries`, `customer_fuel_rate_adjustments`) | Inserted in their final state; the void trigger fires on `UPDATE` only, the `CHECK` requires `voided_at` on voided rows, and the seeded voided rows also carry `voided_by` (what the trigger demands of new voids). |
| `standby_rate_entries` soft delete (R20) | Voided rows carry `voided_at`; the engine treats them as absent (legacy deleted them). |
| Generated `STORED` columns (`tasks.plan_date`, `trip_records.billing_axis_date`, `standby_records.billing_axis_date`, `trip_photos.photo_type_known`, `vehicle_expenses.expense_date`, `driver_compensation_configs.effective_from_date`) and the identity `outbox_events.id` | Never written; their expected values appear as `_`-prefixed annotations in §D.4. |
| `t_driver_link_membership` (deferred, Appendix C §C.3.6) | Memberships load before drivers, so every linked driver holds an active driver membership in its tenant at COMMIT. |

### D.1.6 Placeholder media generator

All media goes through the same storage service the API uses (`internal/storage`, Appendix B) and is registered in `file_objects` (R1):

| Purpose | Generated bytes |
|---|---|
| Trip, check-in, standby, incident, chat, leave, expense, maintenance photos | 640×480 JPEG (quality 70); background colour from a hash of the object key; overlay `{photo_type or purpose} · {trip_no or entity code} · {Bangkok datetime}` drawn with Sarabun Regular (a copy of `logitrack-web/public/fonts/Sarabun-Regular.ttf`, OFL, vendored into `cmd/seed/assets/`) |
| Company logo / stamp / signature | 512×512 PNG with the same overlay |
| Statement documents | One-page placeholder PDF (A4, the placeholder JPEG with the same overlay as its image) until the `documents.render` renderer lands (T39, main spec §7); the rows and keys are already those of `statement_documents` |
| APK | 1,048,576 bytes of `0x00` (sha256 `30e14955ebf1352266dc2ff8067e68104607e750abb9d3b36582b8af909fcb58`) at `app_releases/prod/logitrack-prod-v3.5.0.apk` in `S3_PUBLIC_BUCKET` |

Flow per object: generate bytes → `Backend.Put` on the backend of `STORAGE_BACKEND` (main spec §9.11; compose: MinIO) into `S3_BUCKET` (private) or, for `app_releases/` only, `S3_PUBLIC_BUCKET` (R23, R74), eight objects at a time → insert `file_objects` with `status='committed'`, `committed_at` = `created_at`, `storage_backend` = the backend, `size_bytes` and `sha256` taken from the bytes (a fixture literal that differs, such as the APK's, aborts the load). Native rows use the key templates of `internal/storage.Purposes` (`trips/{trip uuid}/{photo_type}-{ms}.jpg`, `checkin/{task uuid}/{ms}.jpg`, `incidents/{id}/situation1-{ms}.jpg`, `chats/{chat id}/{ms}.jpg`, `expenses/{id}/receipt-{ms}.jpg`, `documents/statements/{statement id}/…`, `companies/{id}/{logo|stamp|signature}-{ms}.png`, `{ms}` = the row's `created_at`; key layout of main spec §9.2): a unit test rebuilds every native fixture key from its purpose, owner, variant and `created_at`. T16 moved the company assets to the `-{ms}` form T11 adopted (a fixed key could not take a second upload), which changed the natural keys and uuids of `FO_LOGO`, `FO_STAMP` and `FO_SIG` (§D.4.2). Rows that stand for ETL-migrated data keep legacy keys verbatim, including a photo that stays under the pre-rename trip number (`trip_records/36601950/seal.jpg` for trip `ZXZB26072300103`), because objects are never moved on rename.

Two deliberate non-happy states: `status='missing_at_source'` (row exists, no object; HEAD returns 404) and `status='pending'` (object uploaded, never committed, `expires_at` in the past, so `storage.gc` deletes object and row on its next run). The fixture shows generated sizes and hashes as `$seed:generated`. JPEG/PNG bytes are stable for a given Go toolchain (a test draws twice and compares); stability across Go versions is UNVERIFIED (the standard encoders do not promise it), so `--verify` compares the store with `file_objects`, never with hard-coded hashes — except the all-zero APK.

### D.1.7 Idempotent re-runs

`--reset` (default when `APP_ENV=local`, on developer machines and in CI):

1. Read every `(bucket, object_key)` from `file_objects` and `RemoveObjects` them.
2. Through `MIGRATE_DATABASE_URL`: `TRUNCATE` every `public` table except `tenants` and goose's `goose_db_version` (excluded by name, so the migration history survives), plus the `etl` tables, in one statement with `RESTART IDENTITY CASCADE` (row-level append-only triggers do not fire on `TRUNCATE`); then, through `ETL_DATABASE_URL`, `DELETE FROM tenants WHERE id <> app_quarantine_tenant_id()`, so the 0002 quarantine row survives (the seed never inserts it, R56).
3. Delete Redis keys under `lt:{APP_ENV}:` with `SCAN` + `UNLINK` — never `FLUSHDB`, because Redis may be shared with other services or environments.
4. Load the profile. RabbitMQ is untouched: the seed publishes nothing; outbox rows are inserted with `published_at` set unless `--emit-events`.

Two consecutive runs therefore produce identical ids, row counts, object keys and object hashes (invariant 12). `--mode upsert` inserts missing rows only; it never issues an `UPDATE` of an existing row (the back-fill of §D.1.5 touches only rows it inserted, and `task_number_counters` only rises), which the append-only and void-only tables would reject anyway. It writes an object only when its `file_objects` row is missing (the row is inserted next) or records the drawn sha256 (identical bytes; this also restores an object missing from the store); an object whose existing row records other bytes (another Go toolchain or renderer, §D.1.6; the statement PDFs once T39 renders them) is kept with its row and reported (a warning per key and `N existing kept` in the summary), so the store never drifts from rows the upsert does not update. Its engine check (§D.1.8) covers the snapshots and standby records it inserted: existing rows are not its output, and a shared database legitimately holds prices edited by hand or later rate rounds that the app never re-prices either.

### D.1.8 `seed --verify`

`--verify` runs the twelve checks of §D.3. Each SQL check returns violating rows (any row = failure). Go-side parts: (a) every `trip_billing_snapshots` row whose `computed_by` is not `etl` or `manual_edit`, and every completed standby row with a price or a stored reason, is recomputed by the engine (`compute.PriceTrip`, `compute.PriceStandby` over the tables read in the same transaction) and must match exactly: estimate, base and stop charge, rate entry, fuel adjustment, lookup codes, round and band, category, `manual_override`, or the unpriced reason (R20: the 0.005 THB tolerance applies only to legacy multi-drop totals and `net_amount`; smoke has none); the load runs the same check over the priced rows it inserted (every seeded one after `--reset`; an upsert skips rows that already existed) and rolls back on a difference, and a row whose stored `computed_by` is `etl` or `manual_edit` is never recomputed, by the load or by `--verify`; (b) every row of `file_objects` is stat-ed on its own backend: a committed or pending object must exist with the recorded size and its bytes must hash to the recorded sha256 (read through `storage.Reader`; S3 keeps no sha256 of its own), a `missing_at_source` row must have none; (c) the Redis hub maps are warmed through the API's read-through cache and checked (#8); (d) the isolation role-play runs on `DATABASE_URL` (#10c, #10d and the carrier principals of #10e, staff and drivers), every other check on `ETL_DATABASE_URL` in a read-only transaction (R87). Output is one line per invariant (PASS/FAIL with up to 20 violating rows), the per-table count diff against the profile manifest (the plan's rows plus the quarantine tenant and the derived `task_number_counters`), and the fingerprint block last. Exit 0 pass, 1 violation or count difference, 2 when a dependency is unreachable or a check cannot run. CI (main spec §17, go-ci `stack` job) stops the scheduler and worker, then runs `seed --profile smoke` then `seed --verify` twice against the compose stack and diffs the two fingerprint blocks: `--verify` checks a freshly seeded, quiescent database, and on a running stack the scheduler's crons write uuidv7 `jobs` rows (`auth.token-cleanup` every 10 minutes) and `storage.gc` deletes the expired pending fixture object (`FO_TR17_PEND`), which #12 and the count diff report by design (stop both before `make seed-verify` on a stack that ran for a while, or seed again). `make test-integration` covers smoke, demo and load on testcontainers (`postgres:18-alpine`, `redis:7-alpine`, MinIO), and `make seed-budget` (go-ci `test`) the smoke time budget.

### D.1.9 Synthetic data rules

- No real people, phone numbers, national IDs, LINE groups, tokens or credentials. Thai personal names combine common given and family names arbitrarily; customer and carrier names carry "ทดสอบ" / "ตัวอย่าง".
- E-mail addresses use the reserved `.test` TLD (`@logitrack.test`).
- Phone numbers use the non-dialable prefix `000-000-0xxx`.
- National IDs and tax IDs are 13 digits with the Thai mod-11 check digit, so validators accept them, and are **synthetic**: persons use `1999900000NN` + check digit (`1999900000013`, `1999900000021`, …), juristic persons `0999900000NN` + check digit (`0999900000015`, `0999900000023`, …).
- Plates match `^([ก-ฮ]{2}|[0-9][ก-ฮ]{2})-?\d{1,4}$`; the orphan plate `กข-1111` deliberately has no `trucks` row.
- LINE group ids are `C` + md5 hex of `seed-line:{code}`: well-formed placeholders that resolve to no real group. FCM tokens are plain strings answered by the local mocks.
- Hub codes reuse codes that already appear in the repo's tests and comments (`SPK890146`, `SPK-GW`, `SPK890103`, `SPK890174`, `ALANG-A`, `WANGTHONGLANG12`, `SOCE`; legacy-quirks report, golden-test inventory) so the vectors line up with `lib/billingCompute.test.ts`, `lib/billingRates.test.ts` and `lib/placeFilter.test.ts`; names are district-level labels and coordinates are synthetic.
- Strings starting with `$seed:` are computed at load time (password hashes, the scrypt fixture hash, generated object sizes and hashes, URLs built from `S3_PUBLIC_BASE_URL`); the fixture never contains a secret.

---

## D.2 Workflow coverage matrix

Symbols refer to the rows of §D.4 (registry in §D.4.2). "Demo" counts are additional generated rows on top of the smoke set. "Exercised by" names the consumer, queue, topic or invariant that must see the rows (queues and topics per Appendix B; invariants per §D.3).

| # | Workflow | Lifecycle states covered | Smoke rows | Demo | Exercised by |
|---|---|---|---|---|---|
| 1 | First mile (FM) task → trip | task `pending` → `assigned` → `checked_in` → `in_transit` → `completed`, `cancelled`; trip `in_transit` → `incident` / `delivered`, `cancelled` | `TK22` pending (TTP call-out, no driver), `TK23` assigned (queue `run_order` 2), `TK24` checked_in with check-in photo + app screenshot, `TK18`/`TR18` in_transit/incident, `TK08`/`TR08` and `TK02`/`TR02` completed/delivered, `TK20`/`TR20` cancelled | ~600 FM tasks, today's board: 6 pending, 8 assigned, 2 checked_in, 3 in_transit, 4 completed, 2 cancelled | `GET /v1/trips/monitor`, SSE `tenant:{tid}:tasks`, `notify.fcm` (`EV2` task.assigned), #1 |
| 2 | Line haul (LH) and task numbering | same states; padded `LH-ddMMyyyy-NNN` vs legacy unpadded; `task_no` not unique for legacy | `TK01`, `TK04`–`TK07`, `TK09`–`TK14`, `TK16`, `TK17`, `TK21`; legacy numbers `LH-07082026-2`, `FM-23072026-7`, `FM-05072026-3`, `FM-08092026-1`, `FM-28062026-12` | ~300 LH; 5 legacy unpadded | `task_number_counters` derivation (R10), #1 |
| 3 | Multi-drop J&T with stops | stops `pending` → `delivered`; completion order ≠ plan order; flat extra-stop fee vs legacy per-route | `TK16`/`TR16` + `TDS16_*`/`PDS16_*` + 3 breakdown rows (flat, `SF01` 300.00); `TK05`/`TR05` + `TDS05_*`/`PDS05_*` + 2 breakdown rows (CJSF has no `extra_stop` fee → legacy) | 3 more (one per mode + one with a failed stop) | `billing.compute`, invoice `multidrop_stop` lines in `ST3`, #2, #4 |
| 4 | Standby — 4 cases | `completed` priced by `standby_rate_entries`; service-fee fallback; unpriced; plan-date customer still billed on `ended_at` | `SB01` (`SR01` 800.00), `SB03` (`SR03` voided → `SF02` 500.00), `SB04` (SPX: no rate, no fee → stored `billing_unpriced_reason='no_rate'`, R62), `SB02` (CJSF plan basis, ended 2026-09-01 02:00 → September round `SR02` 850.00 while the same task's trip `TR06` bills in August) | 25: 15 rate, 4 fee, 3 `no_customer`, 2 `no_rate`, 1 `no_ended_at` (legacy) | `billing.compute` (`standby.completed`), `GET /v1/billing/standby-diagnostics`, #2, #5 |
| 5 | Incidents | driver report on an in-transit trip, photo, chat escalation | `IR1` + `FO_IR1_S1` on `TR18`; `CH01` urgent chat | 12 (2 admin-reported, 1 on a delivered trip → LINE delay note) | SSE `tenant:{tid}:trips` (`incident.created`), `notify.line` |
| 6 | Billing statements and period lock | `draft` → `sent` → `paid`; `cancelled`; invoice numbering; documents; recompute blocked by lock | `ST1` paid (lines + `statement_documents`), `ST2` sent (legacy row, no lines), `ST3` draft, `ST4` cancelled; counters for all four keys; `JB1` forced backfill blocked by `CJSF-202607-001` | same 4 + counters | `documents.render` (`EV3`), `GET /v1/billing/rows`, #5 |
| 7 | Frozen supplementary (เสริม) price | SUPPLEMENTARY priced without fuel and frozen; corrupted SUPPLEMENTARY with fuel (not frozen, repairable); manual price override | `TR11` 950.00 frozen; `TR03` legacy NULL category resolved to SUPPLEMENTARY; `TR12` stored 910.00, repair → 950.00; `TR13` manual 1500.00 (engine 1160.00) | 10 % SUPPLEMENTARY tasks | `accounting:recompute_force`, `accounting:override_price` (Appendix C), #4 |
| 8 | Fuel band rounds | three rounds, switch-day trip at 00:21 ICT, legacy 07:00 ICT announcement, voided announcement, voided rate | `FA01` −70.00 / `FA02` −50.00 (legacy instant) / `FA03` −40.00 / `FA04` voided; `RE05` voided; `TR04` (switch day), `TR06`, `TR14` | 2 rounds per customer | engine golden vectors §D.4.3, #11 |
| 9 | Payroll | `draft` → `approved` → `paid`; weekday/holiday trip pay; multi-stop exclusion; helper day; SSO; penalty instalment; ledger | `PR01` `paid` (D1 Aug R1), `PR02` `approved` (helper day 2026-08-25, SSO 750.00, penalty 200.00, net 100.00), `PR03` `draft` (D2: only a multi-stop trip → 0.00); `TX01`, `TX02` | 2 runs × 11 linked drivers | `payroll.run`, #6 |
| 10 | Penalties | `pending`, `partially_deducted` (instalments), `cleared` (legacy), `cancelled` | `PN02`, `PN01` (600.00 in 3, 1 paid), `PN03`, `PN04`; `payroll_penalty_applications` | 6 | #6 |
| 11 | Cash advance | `pending` (table shipped, UI deferred, R30) | `DA01` 2000.00 to deduct 2026-09 R2 | 2 | `payroll.run` reads it once the UI ships |
| 12 | Chat | `open` (urgent, unassigned) / `closed` (assigned); text and image messages | `CH01` + `CM1`, `CM2` (`FO_CH01_IMG`); `CH02` + `CM3`; every message carries `client_message_id` (R63) | 6 chats, 60 messages | SSE `chat:{id}`, `notify.fcm` |
| 13 | Broadcast | sent with reads; voided | `BC01` + 2 `broadcast_reads`; `BC02` `voided_at` | 3 | `notify.fcm` (`broadcast.created`) |
| 14 | Leave | `pending`, `approved`, `rejected` | `LV02`, `LV01`, `LV03` (with reason) | 8 incl. `cancelled` and 2 attachments | SSE `tenant:{tid}:hr` |
| 15 | Maintenance | PM `pm_booking` created by a fuel log crossing the service mileage; `completed` with invoice; CM `cancelled` | `MT03` (from `EX02` odometer 130,120 ≥ `T1.next_service_mileage` 130,000), `MT01`, `MT02` | 6 incl. `scheduled`, `in_progress` | `maintenance.created` → `notify.fcm` |
| 16 | Fuel and toll expenses | fuel `pending` / `approved`; toll import `approved`; other `rejected` | `EX02`, `EX01` (`tax_inv_id`, receipt), `EX03` (`toll_import_sequence` 1, `ผ่านทาง`), `EX04` | 180: fuel 120, toll 40, other 20; 40 % pending / 50 % approved / 10 % rejected | `GET /v1/expenses?type&status&cursor` |
| 17 | Mobile installations and forced update | `blocked`, `outdated`, `current`, `ahead` (dev flavor); release; invalid FCM token pruning | `MI03` 3.3.2 < floor 3.4.0, `MI02` 3.4.1, `MI01` 3.5.0, `MI04` 3.6.0 `dev`; `settings.mobile_app`; `MR01` + `FO_APK`; `device_tokens` row for D7 answered `UNREGISTERED` | 14 installations | `GET /v1/app-installations[/stats]` (`security:view_mobile_clients`, R43), `GET /v1/mobile/settings`, `notify.fcm` |
| 18 | Tenancy | own_fleet + 2 carriers + quarantine; carrier-run trip billed by the own fleet; contractor reach (R60) | `TN_OWN`, `TN_NWR` (broker, contractor of `TN_OWN`), `TN_TTP` (dispatcher org, also customer `CU_TTP`, no contractor link), `TN_QUAR` (0002) with `TK25` (`tenant_source='quarantine'`); `TR02` run by NWR, billed to SPX, snapshot on `TN_OWN` (R61) | + 1 carrier-run trip per carrier per day | RLS (Appendix C), #10, `tenancy.orphan-scan`, `GET /v1/tenants/quarantine/rows` |
| 19 | Broker driver who moved tenant (R24) | membership `suspended` in the old carrier, `active` in the new one; historic rows keep their frozen tenant; orphan scan reports `tenant_source='driver'` drift | `D6`, memberships `U_D6`@`TN_NWR` suspended / `U_D6`@`TN_TTP` active, `TA3` revoked / `TA4` active, `TK02` (`tenant_source='driver'`, tenant NWR), `TR02`, `JB2` result | 1 | `tenancy.orphan-scan`, #10 |
| 20 | Customer user | customer scope only, no membership | `U_CJSF_CUST` + `user_scopes` (`customer`, `BP_CJSF`) | + `spx.viewer`, `spk.viewer` (§D.1.2 demo personas) | RLS customer branch, #10 |
| 21 | Dispatcher | membership in its own organisation's tenant (R13) + dispatcher scope; creates the cross-tenant pending call-out; never sees cost/HR | `U_TTP_DISP` (`operation_staff` in `TN_TTP`, scope `dispatcher` → `BP_TTP`); creator of `TK20`, `TK22`, `TK24`; sees `TK08` (own-fleet task billed to TTP) | + `wrt.dispatch` (own fleet, over `BP_SPX`), `nwr.dispatch` (NWR, over `BP_SPK`): a dispatcher per tenant | `scope_*` views (R85), SSE `dispatch:*`, #10 |
| 22 | platform_admin | platform role, no tenant membership; audited cross-tenant read | `U_PLAT` + `user_platform_roles` (`granted_by` NULL = bootstrap); `SE1` (`platform_cross_tenant_access` on `TN_TTP`) | + `support` | `X-Act-On-Tenant` (Appendix C), #10 |
| 23 | Authentication paths | Argon2id; legacy Firebase scrypt verify-then-rehash; Google OIDC subject; disabled; must change password; legacy `partner` claim → `tenant_admin` | `U_WRT_*` (Argon2id), `U_D1` (scrypt fixture, `password_hash` NULL), `U_D2` (Google identity `AI_D2_G`), `AI_*_FB` (legacy Firebase uids), `U_D3` (`disabled`, `auth_version` 3), `U_D7` (`must_change_password`), `U_NWR_ADMIN` | 23 users | `/v1/auth/*` (Appendix C); `U_D7` login answers `403 password_change_required` with a single-use `passwordChangeTicket` and no tokens (R79); `U_D3` gets no session |
| 24 | Storage lifecycle and evidence links | `committed`; `pending` past `expires_at`; `missing_at_source`; legacy key under the old trip number; public APK; evidence link live and revoked (R47) | `FO_*` 16 rows: `FO_TR17_PEND`, `FO_TR19_PC`, `FO_TR03_SEAL`, `FO_APK`; `TR01` live token, `TR21` revoked token | ~3,600 | `storage.gc`, `GET /evidence/{token}` (200 for `TR01`, 404 for `TR21`), #9 |
| 25 | Async plumbing | published outbox history; long-running jobs; consumer replay guard | `EV1`–`EV3`; `JB1`, `JB2`; `TR14` snapshot `last_event_id` = 1, the identity of `EV1`'s row (R17, R57) | ~1,500 events | relay, `consumer_inbox` |
| 26 | Hub/place resolution | name→code aliases only; OCR text left unresolved; `SPK-GW` as destination collapses to `SPK`; composite `CODE - name`; driver-created hub | 7 `hub_name_aliases`; `TR09` origin `ประเวศ18 (OCR)` (no hub) and lookup destination `SPK`; `TK21` `SPK890174 - ห้วยขวาง10`; `H_BANGNA` (`created_by_driver`) | ~45 aliases, 10 unresolved origins | Redis `cache:hubs:n2c` / `cache:hubs:c2n`, #8 |
| 27 | Per-tenant capability override | one deny override | `RCO1` (NWR `operator` `fleet:view_live_map` = false) | `nwr.operator` is the principal it applies to; more overrides with T26 | `GET /v1/me` capability resolution |
| 28 | Offline idempotency (R63) | replay after `IDEMPOTENCY_TTL` returns the existing row | `client_op_id` on `IR1`, `SB01`–`SB04`, `EX01`, `EX02`, `EX04`, `LV01`–`LV03`; `client_message_id` on `CM1`–`CM3`; NULL on staff and legacy rows (`EX03`, `TK15`) | every driver-created row, incl. tasks | `(driver_id, client_op_id)` unique indexes |

---

## D.3 Invariants checked by seed --verify

The twelve invariants of the delivery plan, rewritten against Appendix A names. Every query returns **violating** rows; `--verify` fails on any row. Literals (trip numbers, symbols) refer to the smoke fixture, which `demo` and `load` contain unchanged. `:'sym'` placeholders are bound from the §D.4.2 registry. Since T16 the queries run on PostgreSQL 18 as written here (`cmd/seed/internal/seed/invariants.go`; ids are cast to text so a violating row prints readably) and pass on `smoke`, `demo` and `load`. The role-play of #10c and #10d no longer spells out `set_config`: the SELECTs below run inside the request context the API builds for the named user (§D.1.1, Connections), which sets exactly the GUC values shown in the comments (an integration test compares them).

**1. Trip integrity.** Every trip has a task in the same tenant and a truck snapshot (id + plate + class), except orphan-plate rows (no `truck_id`, plate present).

```sql
SELECT t.id, t.trip_no, 'task/tenant/truck snapshot' AS problem
FROM trip_records t
LEFT JOIN tasks k ON k.id = t.task_id
WHERE k.id IS NULL
   OR t.tenant_id <> k.tenant_id
   OR (t.truck_id IS NULL AND t.truck_license_plate_snapshot IS NULL)
   OR (t.truck_id IS NOT NULL AND (t.truck_license_plate_snapshot IS NULL OR t.vehicle_class IS NULL));
```

**2. Delivered work is priced or carries its stored unpriced reason.** Every delivered trip has a snapshot whose `estimate_thb` is set or whose `unpriced_reason` (R62) still holds when re-derived in the engine's precondition order (`no_customer` → `no_vehicle_class` (R15) → `no_rate`; `no_billing_date` cannot occur in stored data). Completed standby is priced or carries a `billing_unpriced_reason` that holds. The Go half recomputes every priced snapshot (§D.1.8).

```sql
-- 2a trips
SELECT t.id, t.trip_no, coalesce(s.unpriced_reason, 'no snapshot / no reason') AS problem
FROM trip_records t
JOIN tasks k ON k.id = t.task_id
LEFT JOIN trip_billing_snapshots s ON s.trip_id = t.id
CROSS JOIN LATERAL (SELECT COALESCE(k.billing_party_id, k.source_linked_party_id,
                                    k.destination_linked_party_id) AS party) p
WHERE t.status = 'delivered'
  AND (s.trip_id IS NULL
       OR (s.estimate_thb IS NULL AND NOT CASE s.unpriced_reason
             WHEN 'no_customer'      THEN p.party IS NULL
             WHEN 'no_vehicle_class' THEN p.party IS NOT NULL AND k.truck_type IS NULL
             WHEN 'no_rate'          THEN p.party IS NOT NULL AND k.truck_type IS NOT NULL AND NOT EXISTS (
                   SELECT 1 FROM customer_rate_entries r
                   WHERE NOT r.voided AND r.billing_party_id = p.party
                     AND r.hub_code = s.lookup_hub_code AND r.destination_code = s.lookup_destination_code)
             ELSE false END));                                -- NULL reason or no_billing_date
-- 2b standby (customer resolution never reads the task's billing party, R19)
SELECT s.id, coalesce(s.billing_unpriced_reason, 'no price / no reason') AS problem
FROM standby_records s
LEFT JOIN tasks k ON k.id = s.task_id
CROSS JOIN LATERAL (SELECT COALESCE(s.customer_party_id, k.source_linked_party_id,
                                    k.destination_linked_party_id) AS party) p
WHERE s.status = 'completed' AND s.billing_estimate_thb IS NULL
  AND NOT CASE s.billing_unpriced_reason
        WHEN 'no_ended_at' THEN s.ended_at IS NULL
        WHEN 'no_customer' THEN s.ended_at IS NOT NULL AND p.party IS NULL
        WHEN 'no_rate'     THEN s.ended_at IS NOT NULL AND p.party IS NOT NULL
                            AND NOT EXISTS (SELECT 1 FROM standby_rate_entries e
                                            WHERE e.billing_party_id = p.party AND e.voided_at IS NULL)
                            AND NOT EXISTS (SELECT 1 FROM customer_service_fees f
                                            WHERE f.billing_party_id = p.party AND f.fee_type = 'standby')
        ELSE false END;
```

Smoke: unpriced trips are `TR09` (`no_rate`), `TR10` (`no_vehicle_class`), `TR15` (`no_customer`); unpriced standby is `SB04` (`no_rate`). Both queries return 0 rows.

**3. หลัก/เสริม parity.** A trip's `job_category` equals its task's, except legacy tasks with NULL category (the trip then holds the category billing resolved).

```sql
SELECT t.id, t.trip_no
FROM trip_records t JOIN tasks k ON k.id = t.task_id
WHERE k.job_category IS NOT NULL AND t.job_category IS DISTINCT FROM k.job_category;
```

**4. Frozen rows.** A priced SUPPLEMENTARY trip carries `manual_override` and no fuel fields; the one deliberately corrupted fixture (`TR12`) must evaluate as **not** frozen so a forced recompute repairs it. `is_frozen` = `manual_override OR (job_category = 'SUPPLEMENTARY' AND NOT carries_fuel)`; `carries_fuel` = `fuel_adjustment_id IS NOT NULL OR rate_multiplier <> 1 OR add_thb_per_trip <> 0` (main spec §6).

```sql
SELECT t.trip_no, 'SUPPLEMENTARY not frozen' AS problem
FROM trip_records t JOIN trip_billing_snapshots s ON s.trip_id = t.id
WHERE s.estimate_thb IS NOT NULL AND t.job_category = 'SUPPLEMENTARY' AND t.trip_no <> 'ZXJB26090700112'
  AND NOT (s.manual_override AND s.fuel_adjustment_id IS NULL AND s.rate_multiplier = 1 AND s.add_thb_per_trip = 0)
UNION ALL
SELECT t.trip_no, 'corrupted fixture must not be frozen'
FROM trip_records t JOIN trip_billing_snapshots s ON s.trip_id = t.id
WHERE t.trip_no = 'ZXJB26090700112'
  AND (s.manual_override OR NOT (s.fuel_adjustment_id IS NOT NULL OR s.rate_multiplier <> 1 OR s.add_thb_per_trip <> 0));
```

**5. Period locks and invoice counters.** No price written after its period was sent; `last_seq` equals the number of statements per `(party, year, month)`; invoice numbers have the `{CODE}-{YYYYMM}-{NNN}` shape.

```sql
SELECT 'trip' AS kind, s.trip_id::text AS id, b.invoice_number
FROM trip_billing_snapshots s
JOIN trip_records t ON t.id = s.trip_id
JOIN billing_statements b ON b.billing_party_id = t.billing_party_id AND b.status IN ('sent','paid')
 AND b.period_year = EXTRACT(YEAR FROM t.billing_axis_date)::int
 AND b.period_month = EXTRACT(MONTH FROM t.billing_axis_date)::int
WHERE s.updated_at > b.sent_at
UNION ALL
SELECT 'standby', r.id::text, b.invoice_number
FROM standby_records r
JOIN billing_statements b ON b.billing_party_id = r.billing_party_id AND b.status IN ('sent','paid')
 AND b.period_year = EXTRACT(YEAR FROM r.billing_axis_date)::int
 AND b.period_month = EXTRACT(MONTH FROM r.billing_axis_date)::int
WHERE r.billing_computed_at > b.sent_at
UNION ALL
SELECT 'counter', concat_ws('/', billing_party_id, period_year, period_month), NULL
FROM billing_counters c
FULL JOIN (SELECT billing_party_id, period_year, period_month, count(*) AS n
           FROM billing_statements GROUP BY 1, 2, 3) n USING (billing_party_id, period_year, period_month)
WHERE c.last_seq IS DISTINCT FROM n.n
UNION ALL
SELECT 'invoice_number', id::text, invoice_number FROM billing_statements
WHERE invoice_number !~ ('^' || customer_code_snapshot || '-' || period_year || lpad(period_month::text, 2, '0') || '-[0-9]{3,}$');
```

**6. Payroll arithmetic and penalty balances.** Run totals equal the sum of their lines; `net = max(0, earnings − deductions)`; approved/paid runs have a ledger row; a non-legacy penalty's balance equals total minus instalments applied by approved/paid runs; `cleared` ⇔ zero balance.

```sql
SELECT r.id::text, 'run totals' AS problem
FROM payroll_runs r
LEFT JOIN (SELECT payroll_run_id,
                  COALESCE(sum(amount_thb) FILTER (WHERE item_type = 'earning'), 0)   AS e,
                  COALESCE(sum(amount_thb) FILTER (WHERE item_type = 'deduction'), 0) AS d
           FROM payroll_line_items GROUP BY 1) l ON l.payroll_run_id = r.id
WHERE r.total_earnings_thb <> COALESCE(l.e, 0) OR r.total_deductions_thb <> COALESCE(l.d, 0)
   OR r.net_pay_thb <> GREATEST(0, r.total_earnings_thb - r.total_deductions_thb)
   OR (r.status IN ('approved','paid') AND r.ledger_transaction_id IS NULL)
UNION ALL
SELECT p.id::text, 'penalty balance'
FROM driver_penalties p
LEFT JOIN (SELECT a.penalty_id, sum(a.applied_thb) AS applied, count(*) AS n
           FROM payroll_penalty_applications a JOIN payroll_runs r ON r.id = a.payroll_run_id
           WHERE r.status IN ('approved','paid') GROUP BY 1) x ON x.penalty_id = p.id
WHERE (p.legacy_doc_id IS NULL AND p.status <> 'cancelled'
       AND (p.remaining_thb <> p.total_thb - COALESCE(x.applied, 0) OR p.installments_paid <> COALESCE(x.n, 0)))
   OR (p.status = 'cleared' AND p.remaining_thb <> 0)
   OR (p.status IN ('pending','partially_deducted') AND p.remaining_thb = 0);
```

**7. Helper drivers.** At most one helper per task (structural: single `helper_driver_id` column, ADR 0011 cap); the helper is a linked driver and not the task's own driver.

```sql
SELECT k.id, k.task_no
FROM tasks k JOIN drivers h ON h.id = k.helper_driver_id
WHERE h.user_id IS NULL OR k.helper_driver_id = k.driver_id;
```

**8. Hub maps never merge directions; the two `SPK` regressions hold.** No alias equals a hub code (an alias that is also a code would fold `codeToName` into `nameToCode`); destination `SPK890174` has a live rate; destination `SPK-GW` collapses to `SPK` (documented quirk, kept for parity). Go half: after warming, Redis `lt:{APP_ENV}:cache:hubs:n2c` and `lt:{APP_ENV}:cache:hubs:c2n` both exist as separate hashes and no field of `n2c` equals a `hubs.source_id`.

```sql
SELECT 'alias equals a code' AS problem, a.alias::text
FROM hub_name_aliases a JOIN hubs h ON upper(h.source_id::text) = upper(a.alias::text)
UNION ALL
SELECT 'SPK890174 must resolve to a live rate', NULL
WHERE NOT EXISTS (SELECT 1 FROM customer_rate_entries WHERE destination_code = 'SPK890174' AND NOT voided)
UNION ALL
SELECT 'SPK-GW destination must look up as SPK', NULL
WHERE NOT EXISTS (SELECT 1 FROM trip_billing_snapshots s JOIN trip_records t ON t.id = s.trip_id
                  WHERE t.destination_raw = 'SPK-GW' AND s.lookup_destination_code = 'SPK');
```

**9. Media.** Every committed or pending object exists with the recorded size and sha256 (Go: `StatObject` per row of the first query); every `missing_at_source` row has no object (HEAD 404); no business row points at a `pending` object.

```sql
-- 9a objects to StatObject
SELECT bucket, object_key, size_bytes, sha256, status FROM file_objects WHERE deleted_at IS NULL;
-- 9b business rows referencing pending uploads
SELECT 'trip_photos' AS t, p.id::text FROM trip_photos p JOIN file_objects f ON f.id = p.file_id WHERE f.status = 'pending'
UNION ALL SELECT 'tasks', k.id::text FROM tasks k JOIN file_objects f
  ON f.id IN (k.check_in_photo_file_id, k.check_in_app_screenshot_file_id) WHERE f.status = 'pending'
UNION ALL SELECT 'incident_reports', i.id::text FROM incident_reports i JOIN file_objects f
  ON f.id IN (i.map_photo_file_id, i.situation1_photo_file_id, i.situation2_photo_file_id) WHERE f.status = 'pending'
UNION ALL SELECT 'chat_messages', m.id::text FROM chat_messages m JOIN file_objects f ON f.id = m.image_file_id WHERE f.status = 'pending'
UNION ALL SELECT 'vehicle_expenses', e.id::text FROM vehicle_expenses e JOIN file_objects f
  ON f.id IN (e.receipt_file_id, e.odometer_file_id) WHERE f.status = 'pending'
UNION ALL SELECT 'statement_documents', d.statement_id::text FROM statement_documents d JOIN file_objects f ON f.id = d.file_id WHERE f.status = 'pending'
UNION ALL SELECT 'companies', c.id::text FROM companies c JOIN file_objects f
  ON f.id IN (c.logo_file_id, c.stamp_file_id, c.signature_file_id) WHERE f.status = 'pending';
```

Smoke: 9a lists 16 rows — 14 committed (StatObject OK), `FO_TR17_PEND` pending (object present until `storage.gc`), `FO_TR19_PC` missing (404 expected); 9b returns 0 rows.

**10. Tenant isolation.** (a) Tenant-scoped operational rows reference drivers and trucks of the same tenant, or a driver who holds a `driver` membership (active or suspended) in that tenant — the broker-drift case; quarantine rows are exempt. (b) The `scope_*` projections of dispatcher and customer-scope principals (Appendix C §C.3.7) expose no billing, cost, evidence or PII column. (c) Role-play on `DATABASE_URL`: a dispatcher sees no carrier-internal rows; a carrier `tenant_admin` sees no own-fleet rate cards. (d) Contractor reach (R60): own-fleet staff see the trips of `TN_NWR` (its `contractor_tenant_id` is `TN_OWN`) and nothing of `TN_TTP` (no contractor link).

```sql
-- 10a
SELECT 'tasks' AS t, k.id::text
FROM tasks k LEFT JOIN drivers d ON d.id = k.driver_id LEFT JOIN trucks tr ON tr.id = k.truck_id
WHERE k.tenant_source <> 'quarantine'
  AND ((tr.id IS NOT NULL AND tr.tenant_id <> k.tenant_id)
    OR (d.id IS NOT NULL AND d.tenant_id <> k.tenant_id AND NOT EXISTS (
          SELECT 1 FROM memberships m WHERE m.user_id = d.user_id AND m.tenant_id = k.tenant_id AND m.role = 'driver')))
UNION ALL
SELECT 'trip_records', t.id::text
FROM trip_records t LEFT JOIN drivers d ON d.id = t.driver_id LEFT JOIN trucks tr ON tr.id = t.truck_id
WHERE (tr.id IS NOT NULL AND tr.tenant_id <> t.tenant_id)
   OR (d.id IS NOT NULL AND d.tenant_id <> t.tenant_id AND NOT EXISTS (
         SELECT 1 FROM memberships m WHERE m.user_id = d.user_id AND m.tenant_id = t.tenant_id AND m.role = 'driver'));
-- 10b
SELECT table_name, column_name FROM information_schema.columns
WHERE table_schema = 'public' AND table_name IN
      ('scope_tasks','scope_trips','scope_standby','scope_incidents','scope_drivers','scope_trucks','scope_tenants')
  AND (column_name LIKE 'billing%' OR column_name LIKE '%_thb' OR column_name LIKE '%party_id'
       OR column_name IN ('evidence_token','id_card','id_card_file_id','license_file_id','birth_date','truck_license_id'));
-- 10c: DATABASE_URL (logitrack_app), read-only, as each of
--   U_TTP_DISP in TN_TTP: app.role operation_staff, app.customer_ids BP_TTP, app.dispatcher on, app.steward off
--   U_NWR_ADMIN in TN_NWR: app.role tenant_admin, app.steward off
--   U_CJSF_CUST (no tenant): app.role customer, app.customer_ids BP_CJSF
-- $1 = the principal's tenant (NULL for the customer, hence IS DISTINCT FROM)
SELECT 'trip_billing_snapshots' AS t, count(*) FROM trip_billing_snapshots HAVING count(*) > 0
UNION ALL SELECT 'customer_rate_entries', count(*) FROM customer_rate_entries HAVING count(*) > 0
UNION ALL SELECT 'payroll_runs', count(*) FROM payroll_runs WHERE tenant_id IS DISTINCT FROM $1 HAVING count(*) > 0
UNION ALL SELECT 'vehicle_expenses', count(*) FROM vehicle_expenses WHERE tenant_id IS DISTINCT FROM $1 HAVING count(*) > 0
UNION ALL SELECT 'maintenance_records', count(*) FROM maintenance_records WHERE tenant_id IS DISTINCT FROM $1 HAVING count(*) > 0;
-- and, as U_TTP_DISP, the own-fleet task billed to TTP stays visible through the projection
SELECT 'scope_tasks must show FM-12082026-001 to the dispatcher' WHERE NOT EXISTS (SELECT 1 FROM scope_tasks WHERE id = :'TK08');
-- 10d: DATABASE_URL, as U_WRT_OPS in TN_OWN: app.role operation_staff, app.subtenant_ids TN_NWR (contractor reach,
-- loaded by iam.RBAC.Resolve, Appendix C §C.3.4), app.steward on
SELECT 'NWR trip TR02 must be in reach' AS problem
WHERE NOT EXISTS (SELECT 1 FROM trip_records WHERE trip_no = 'ZXZB26072200102')
UNION ALL
SELECT 'TTP tasks must be out of reach' FROM tasks WHERE tenant_id = :'TN_TTP' HAVING count(*) > 0;
-- 10e (owner addition to issue #36): DATABASE_URL, as carrier principals, staff and drivers: one active member per
-- carrier tenant and role without a dispatcher grant (smoke nwr.admin and the TTP driver d7.kitti; demo adds
-- nwr.manager, nwr.ops, nwr.operator, ttp.admin, ttp.ops; load the L01-L08 tenant_admins, managers and operators;
-- every policy branch keys on tenant and role) plus every broker driver (memberships in more than one tenant: d6.amnat,
-- R24). Every RLS table logitrack_app may SELECT (catalog: relrowsecurity, has_table_privilege('logitrack_app', ...))
-- is classified, deny by default (cmd/seed/internal/seed/isolation.go):
--   tenant_id stamp (33 tables): owned when tenant_id = :tenant or NULL (platform rows);
--   owner rule (29 tables): tenants (id); users (a membership in :tenant); the parent-scoped children of
--     app_rls_child_table and the comms tables (the parent row, looked up under the principal's RLS, is a :tenant or
--     NULL-tenant row: payroll_line_items -> payroll_runs, billing_statement_lines -> billing_statements,
--     trip_photos -> trip_records, chat_messages -> chats, broadcast_recipients -> broadcasts, ...); the user-keyed
--     tables (auth_identities, user_platform_roles, user_scopes, sessions, device_tokens, password_reset_tokens:
--     the user is a member of :tenant; refresh_tokens through sessions); status_history through its entity;
--   shared master data (6, skipped): customers, customer_driver_id_types, hubs, hub_name_aliases, hub_soc_distances
--     (app_rls_global_table) and billing_parties (its tenant_id references the party's tenant, C.3.0);
--   anything else, or a rule for a table that is no longer a readable RLS table: a violation.
-- A visible row that is neither owned nor the principal's own is a leak; the principal's own rows in another tenant
-- (driver_id or helper_driver_id = :driver, a driver's own drivers row, the user's own memberships, uploads and
-- user-keyed rows) are R24 history: driver self-scope is by driver_id, not by tenant (Appendix C §C.1 "Broker carrier
-- moves", §C.3.5 p_driver_read, memberships p_self_read), so they are reported in the detail line, never failed.
-- One statement per principal, the context as uuid literals, jit off (compiling the ~60-table statement cost seconds):
SELECT t, leak, own FROM (
  SELECT '<table>' AS t,
         count(*) FILTER (WHERE NOT coalesce(<owned>, false) AND NOT coalesce(<own>, false)) AS leak,
         count(*) FILTER (WHERE NOT coalesce(<owned>, false) AND coalesce(<own>, false)) AS own
  FROM <table> r
  UNION ALL ...) x WHERE leak > 0 OR own > 0;
-- and the principal must read its own tenants row (an empty context cannot pass)
```

The exempt-service-layer tables (RLS off: `outbox_events`, `jobs`, `notification_deliveries`, `settings`, `mobile_app_releases`, ...) are out of #10e on purpose: `logitrack_app` reads them unfiltered and the owning service decides access (Appendix C §C.3.0). A platform-wide broadcast (`tenant_id` NULL) is read by every tenant's staff by design, so its recipient and read rows count as platform rows. #10e fails with exit 1 (integration tests) when a permissive policy opens a stamped table (`tasks`), a parent-scoped child (`payroll_line_items`) or `users` to `logitrack_app`, when a driver policy loses its `driver_id` predicate (`tasks USING (app_role() = 'driver')`), and when a new readable RLS table has no rule. Detail line on smoke: `3 carrier principal(s) (1 staff, 2 driver(s)) over 62 RLS tables (33 by tenant_id, 29 by owner; 6 shared master tables skipped); R24 own history in other tenants: d6.amnat@logitrack.test (TTP, driver): memberships 1, tasks 1, trip_records 1, truck_assignments 1` (`U_D6`'s suspended NWR membership, `TK02`, `TR02`, `TA3`).

Smoke: every part returns 0 rows (`TK02`/`TR02` pass 10a through `U_D6`'s suspended NWR membership); the 10c dispatcher still sees `TK08` through `scope_tasks`. The drift itself is reported by `tenancy.orphan-scan` (`JB2`).

**11. Bangkok calendar.** Every Bangkok-day column equals `bkk_date()` of its instant under both legacy `effective_from_at` conventions (Bangkok midnight and 07:00 ICT); payroll windows start at Bangkok midnight and R1 ends on day 16; new ledger rows use the Bangkok date; the 00:21 ICT switch-day trip prices under the later round.

```sql
SELECT 'tasks.plan_date' AS c, id::text FROM tasks WHERE plan_date <> bkk_date(plan_at)
UNION ALL SELECT 'rate.effective_from_date', id::text FROM customer_rate_entries WHERE effective_from_date <> bkk_date(effective_from_at)
UNION ALL SELECT 'fuel.effective_from_date', id::text FROM customer_fuel_rate_adjustments WHERE effective_from_date <> bkk_date(effective_from_at)
UNION ALL SELECT 'standby_rate.effective_from_date', id::text FROM standby_rate_entries WHERE effective_from_date <> bkk_date(effective_from_at)
UNION ALL SELECT 'payroll window', id::text FROM payroll_runs
  WHERE period_start <> bkk_midnight(bkk_date(period_start)) OR period_end <> bkk_midnight(bkk_date(period_end))
     OR (pay_round = 'R1' AND EXTRACT(DAY FROM bkk_date(period_end)) <> 16)
UNION ALL SELECT 'ledger date', id::text FROM transactions WHERE tx_date_source = 'bangkok' AND tx_date <> bkk_date(created_at)
UNION ALL SELECT 'switch-day round', t.trip_no FROM trip_records t JOIN trip_billing_snapshots s ON s.trip_id = t.id
  WHERE t.trip_no = 'ZXJB26081600104' AND s.round_effective_from_date IS DISTINCT FROM DATE '2026-08-16';
```

**12. Determinism.** Running the profile twice yields the same fingerprint; every seeded uuid is version 5 (§D.1.3). `--verify` prints the fingerprint as its last output block: for every seeded table (and the derived `task_number_counters`) the row count and the md5 of its primary-key columns plus a few business columns, ordered; CI runs `seed --reset` twice and diffs the two blocks (nothing is stored in Redis, whose namespaces are reserved by R26). `updated_at` and the password hashes stay out: the back-fill of §D.1.5 stamps the load time, and Argon2id salts are random.

```sql
-- per table <t> with primary key (k1, k2, ...) and extra columns (trip_records: trip_no; tasks: task_no;
-- trip_billing_snapshots: estimate_thb; file_objects: object_key, sha256; outbox_events: event_id; users: email)
SELECT count(*), md5(coalesce(string_agg(r, ',' ORDER BY r), ''))
FROM (SELECT concat_ws('|', coalesce(k1::text, '-'), coalesce(k2::text, '-'), ..., coalesce(extra::text, '-')) AS r FROM <t>) x;
-- non-v5 ids, for every seeded table whose primary key is a uuid id
SELECT 'non-v5 id' AS problem, 'tenants' AS t, id FROM tenants
  WHERE uuid_extract_version(id) <> 5
    AND kind <> 'quarantine'   -- fixed id from migration 0002 (R56)
    AND kind <> 'own_fleet'    -- may come from OWN_FLEET_TENANT_ID (R7); compared with it by the Go half
UNION ALL SELECT 'non-v5 id', 'tasks', id FROM tasks WHERE uuid_extract_version(id) <> 5
UNION ALL SELECT 'non-v5 id', 'trip_records', id FROM trip_records WHERE uuid_extract_version(id) <> 5;
-- (generated for every table with a uuid primary key)
```

Rows created by the running application after the seed (uuidv7) fail #12 and the count diff by design: `--verify` checks a freshly seeded, quiescent database (the CI `stack` job stops the scheduler and worker first, §D.1.8). The housekeeping crons also remove seeded rows on a long-lived stack: `storage.gc` the expired pending `FO_TR17_PEND` (hourly), `outbox.prune` the published seeded outbox rows (daily, older than 7 days), `jobs.prune` `JB1`/`JB2` (older than 30 days); seed again before verifying such a stack.

---

## D.4 Sample rows

These rows are the `smoke` profile: 286 rows in 58 tables (285 seeded, plus the quarantine tenant of migration 0002) and 16 objects. The generator that produced this appendix checked that every `@SYM` reference resolves to a row shown here (foreign-key closure) and that every registered symbol is used. The same rows are the expected outputs of the engine vectors in §D.4.3–§D.4.5.

### D.4.1 Conventions

- `@SYM` is replaced by the uuid of symbol `SYM` from §D.4.2 before insert — also inside strings (object keys, realtime topics, JSON payloads).
- Keys starting with `_` are annotations, never inserted: expected values of generated `STORED` columns (`_plan_date`, `_billing_axis_date`, `_expense_date`, `_effective_from_date`) and expected outcomes (`_billing`, `_status`, `_effect`) that `--verify` checks.
- Strings starting with `$seed:` are computed at load time (§D.1.9).
- `file_objects.bucket` shows the local `S3_BUCKET` / `S3_PUBLIC_BUCKET` values (R74); the seed writes the configured ones, while natural keys keep these literals.
- `client_op_id` / `client_message_id` on native driver-created rows are uuid v5 of `client_op:<symbol>` in the seed namespace (R63); staff-created and legacy rows leave them NULL. The fixture holds the values of the default namespace (a test recomputes them); another `SEED_NAMESPACE` keeps them, which only matters for their uniqueness per driver (a database holds one namespace's dataset, §D.1.3).
- The `TN_QUAR` row is shown for completeness (`_source`): migration 0002 inserts it and the seed never does (R56).
- An omitted column takes its Appendix A default or NULL, except that, for determinism, an omitted timestamp whose default is `now()` (`updated_at`, `computed_at`, `last_seen_at`, …) and the `committed_at` of a committed object are set to the row's `created_at`, or `2026-01-05T09:00:00+07:00` when the row has none.
- Money is a JSON string with two decimals (`NUMERIC(14,2)`, R20); multipliers six decimals; litres and price per litre three; reference fuel prices two.
- Instants are ISO-8601 with `+07:00`; dates are Bangkok calendar dates.
- Tables are grouped by domain; load order is §D.1.5.

### D.4.2 Id registry

Namespace `dd659aa7-e92b-55af-b6f0-51075452cf69`; uuid = uuid v5(namespace, `"<table>:<natural key>"`), with any `@SYM` inside the natural key resolved first. The one exception is `TN_QUAR`, whose id is fixed by migration 0002 (R56). Legacy-shaped natural keys (`legacy:seedTaskDoc…`) stand for migrated rows that have no business key. `EV1`–`EV3` are `outbox_events.event_id` values (the table's own key is a bigint identity).

<details>
<summary>Registry: 227 symbols</summary>

| Symbol | Natural key (v5 name = `<table>:<natural key>`) | uuid |
|---|---|---|
| **tenants** | | |
| `TN_OWN` | `own_fleet` | `661f033c-8980-5e51-b822-eb925ad2086a` |
| `TN_NWR` | `NWR` | `29125658-5fc9-5d1c-ad98-57649d7a892c` |
| `TN_TTP` | `TTP` | `96eb2738-e88d-5f4d-85b1-ee93aebe9b13` |
| `TN_QUAR` | `(migration 0002, not v5)` | `00000000-0000-7000-8000-00000000000f` |
| **customers** | | |
| `CU_CJSF` | `CJSF` | `5eeed910-248e-59da-a5ab-692a114d1379` |
| `CU_TTP` | `TTP` | `6f50c699-5388-5160-9a5b-2deba770925a` |
| `CU_SPX` | `SPX` | `b38d54b4-fe76-54fd-bb8e-6d90a398e6a0` |
| `CU_SPK` | `SPK` | `629a232a-8f1c-5627-be15-11573a8853ca` |
| **billing_parties** | | |
| `BP_CJSF` | `customer:CJSF` | `98efaced-3d2e-5c71-8f95-e1ababbb8c9b` |
| `BP_TTP` | `customer:TTP` | `c704cab5-d81b-5beb-a8e7-77d3963b8a35` |
| `BP_SPX` | `customer:SPX` | `9478ca88-965d-5284-9eba-73757661b5d7` |
| `BP_SPK` | `customer:SPK` | `0b2da36c-9abf-5798-8323-48322a9e3f88` |
| `BP_NWR` | `tenant:NWR` | `8061ad45-34aa-522d-9ac4-268d85695228` |
| **companies** | | |
| `CO_WRT` | `WRT` | `a5239558-fb6c-592e-a8cc-6405387b5a96` |
| **users** | | |
| `U_PLAT` | `platform.admin@logitrack.test` | `5611853b-0ef9-5a86-bb51-29636f96c24c` |
| `U_WRT_ADMIN` | `wrt.admin@logitrack.test` | `e66abef9-955c-5671-9345-3fe88244e982` |
| `U_WRT_MGR` | `wrt.manager@logitrack.test` | `317bc283-36f2-57f9-b219-466e8c559a1a` |
| `U_WRT_OPS` | `wrt.ops@logitrack.test` | `48e4f51e-e37e-5062-9c22-f70e7c1cad0e` |
| `U_NWR_ADMIN` | `nwr.admin@logitrack.test` | `e5f44c4e-e46a-5139-8f43-675ae0976ffa` |
| `U_TTP_DISP` | `ttp.dispatch@logitrack.test` | `0eae1831-1758-5a5c-bb6e-ac3f4de3dc31` |
| `U_CJSF_CUST` | `cjsf.viewer@logitrack.test` | `54b1626c-647f-5b70-8c84-34684591ed15` |
| `U_D1` | `d1.somchai@logitrack.test` | `99f18503-f0b7-5b62-834a-f136189d02d8` |
| `U_D2` | `d2.wichai@logitrack.test` | `96f58d46-d4db-5f5a-8cb9-0135d25f880a` |
| `U_D3` | `d3.anan@logitrack.test` | `584cbe7d-cb73-52eb-a589-c88f3ac9bdb3` |
| `U_D6` | `d6.amnat@logitrack.test` | `644fe3fd-ec2f-5f0e-9fed-cff669b6c22b` |
| `U_D7` | `d7.kitti@logitrack.test` | `4fecb05e-78b3-5310-af86-cf8ee01ca0e1` |
| **auth_identities** | | |
| `AI_NWR_FB` | `firebase_legacy:SEEDUID0000000000000000000N1` | `1a6063ca-5d32-5a60-aad0-aa1c0d78539d` |
| `AI_D1_FB` | `firebase_legacy:SEEDUID0000000000000000000D1` | `6c2cf32c-8e19-52ee-9915-41333e876af6` |
| `AI_D2_FB` | `firebase_legacy:SEEDUID0000000000000000000D2` | `35ec7c1a-ec70-55b4-bef6-123ca2bbe8d1` |
| `AI_D2_G` | `google:100000000000000000002` | `7a960754-9864-57f9-94ce-0af15403c926` |
| `AI_D6_FB` | `firebase_legacy:SEEDUID0000000000000000000D6` | `fa68d411-d670-5233-8a1d-3fdbc2acaaf1` |
| **user_scopes** | | |
| `US_CJSF` | `cjsf.viewer@logitrack.test:customer:CJSF` | `ae4ae61f-bd14-5216-8eb5-250a2e790825` |
| `US_TTP` | `ttp.dispatch@logitrack.test:dispatcher:TTP` | `d3598227-7804-5d29-b6c2-b0a9b97e7af6` |
| **role_capability_overrides** | | |
| `RCO1` | `NWR:operator:fleet:view_live_map` | `da8862fa-e196-55cf-a471-32aefc7ecef7` |
| **hubs** | | |
| `H_ALANGA` | `ALANG-A` | `2b850bad-c57b-5ca8-8327-48bdb1c0b754` |
| `H_WTL12` | `WANGTHONGLANG12` | `82806ed7-e75a-553e-b332-af2ee045a54a` |
| `H_SPKGW` | `SPK-GW` | `6f5c6e35-4fe0-54e2-abd6-4569ac242468` |
| `H_SPK103` | `SPK890103` | `07414c77-c7a2-51bf-9651-9db73d540d09` |
| `H_SPK146` | `SPK890146` | `bd1a30b6-ca3a-5d0a-ac12-069b13d9ba8b` |
| `H_SPK174` | `SPK890174` | `00a97127-d9ec-594b-a996-4562b6e86a2d` |
| `H_TTP1` | `TTP-HUB1` | `159a9b0e-a932-5271-a510-7ef4ae077d1d` |
| `H_SOCE` | `SOCE` | `3d828d0f-f6a6-5606-b88c-822d1ec1d3c8` |
| `H_BANGNA` | `BANGNA-SPX` | `3f6dad53-35ab-5729-bd22-8a7dd34af413` |
| **hub_soc_distances** | | |
| `HD_ALANGA_SOCE` | `ALANG-A:SOCE:hub_to_soc` | `4f8dcf70-7b20-55c7-8373-a8190bfdf440` |
| `HD_SOCE_ALANGA` | `ALANG-A:SOCE:soc_to_hub` | `7fcab48c-2787-5af8-a981-028cbe00ece9` |
| **drivers** | | |
| `D1` | `1999900000013` | `09f866b5-5549-5b23-ad9a-d6b2740ff5d4` |
| `D2` | `1999900000021` | `26b52356-df59-571e-b7ee-52de036f98d9` |
| `D3` | `1999900000030` | `fdfd9c7d-3bd8-57ee-9470-37fe4fd103e8` |
| `D4` | `1999900000048` | `ebf51e82-0d12-5a30-bf6e-7677ab10880a` |
| `D5` | `1999900000056` | `b6aac387-807e-5ebe-8d46-cb86bb25eae2` |
| `D6` | `1999900000064` | `1b187f9f-7f5e-5bb7-b157-88548ceb0b22` |
| `D7` | `1999900000072` | `a44f9d04-22be-540f-9b1b-84e1b6d1aaae` |
| **trucks** | | |
| `T1` | `WRT:1ขค-1234` | `af125961-37ff-5320-b36a-2be8d3abc85a` |
| `T2` | `WRT:2ขค-5678` | `e354d095-245b-5ac8-9bd4-1df6ed311268` |
| `T3` | `WRT:3ฒท-9012` | `41482616-d2ff-5d90-b68a-4873efe19bfd` |
| `T4` | `WRT:ฆฆ-999` | `63dacaa1-fa55-5b02-a8d1-7c52596e8508` |
| `T5` | `NWR:4กข-3456` | `ae4197ed-ad60-5d98-8fba-8041ca6526a9` |
| `T6` | `TTP:5กข-7890` | `a53f27a7-eaf6-5586-bd47-888b536b7d78` |
| **truck_assignments** | | |
| `TA1` | `1ขค-1234\|D1\|2026-01-05` | `1df94aee-7f78-5325-8435-08eadc313226` |
| `TA2` | `2ขค-5678\|D2\|2026-01-05` | `6e8757de-9fcb-5124-b6d3-7ce9bc47838e` |
| `TA3` | `4กข-3456\|D6\|2026-06-01` | `2c900aad-ab58-5cb9-9994-6c00eaa987dc` |
| `TA4` | `5กข-7890\|D6\|2026-09-01` | `74382847-e98d-564c-8f06-4433ef1e7586` |
| **tasks** | | |
| `TK01` | `LH-10072026-001` | `c681e5c8-745f-515f-b477-0f0e3ee7f965` |
| `TK02` | `legacy:seedTaskDoc000000T02` | `a0f55965-fac1-5b11-93cd-558816583447` |
| `TK03` | `legacy:seedTaskDoc000000T03` | `5160d4e8-3e33-5be1-8aa8-6d84b18a55dd` |
| `TK04` | `LH-16082026-001` | `2f90686a-a4bf-5f92-a10f-2f83e11820c8` |
| `TK05` | `LH-20082026-001` | `898215c8-0cbd-53cc-b35d-5757108b94bb` |
| `TK06` | `LH-31082026-001` | `4da9035b-c6b2-5eec-ba04-3562b508a385` |
| `TK07` | `legacy:seedTaskDoc000000T07` | `200888f9-de55-5d6c-8295-ab195a7a7b3f` |
| `TK08` | `FM-12082026-001` | `1f9fd489-d08a-5038-aa4a-5951ce5cb50b` |
| `TK09` | `LH-03092026-001` | `d01d20db-117c-5ace-819a-e3beb31546dd` |
| `TK10` | `LH-04092026-001` | `ffeaeb2b-b9af-51e3-b9d4-44d3d0eacb30` |
| `TK11` | `LH-05092026-001` | `6925d8c6-f46c-58b1-983c-d1f362ebcb71` |
| `TK12` | `LH-07092026-001` | `f6d08448-7ad7-54d3-8bad-f46b04b7cbfa` |
| `TK13` | `LH-10092026-001` | `188384ef-cadb-5ff6-ace0-0ea262a715a5` |
| `TK14` | `LH-20092026-001` | `682156ac-b9a8-52e5-9cb1-1d50ce7e63e5` |
| `TK15` | `legacy:seedTaskDoc000000T15` | `2e29b05f-b799-57c6-8c49-8b1c749e8d77` |
| `TK16` | `LH-26082026-001` | `39ebae81-9e37-5022-9def-070f8898ad5c` |
| `TK17` | `LH-30092026-001` | `695efe0e-a3e1-5623-8ecc-18c4772584da` |
| `TK18` | `FM-29092026-001` | `cc272efb-edcd-5262-baf5-19cab9d99ef5` |
| `TK19` | `legacy:seedTaskDoc000000T19` | `36104352-e4ea-5947-bf86-99ac538174e3` |
| `TK20` | `FM-12092026-001` | `485be8da-74fb-5a9e-b9d1-6e31c8a96409` |
| `TK21` | `LH-27082026-001` | `ac111b14-3877-576c-ae27-3c822fe16ef1` |
| `TK22` | `FM-30092026-001` | `c226f279-4c14-5314-b637-017e8d5a3312` |
| `TK23` | `FM-01102026-001` | `95c4f4ad-0b30-507f-b1aa-a5a9cf48f0c9` |
| `TK24` | `FM-30092026-002` | `153cb3dd-701f-5f87-90e0-f3864eddec46` |
| `TK25` | `legacy:seedTaskDoc000000T25` | `2af9fadc-0c72-5fd1-b6ec-5eb6b14fe631` |
| **trip_records** | | |
| `TR01` | `ZXJB26071000101` | `ed7fae76-1ded-54b7-b40a-b896387e2daa` |
| `TR02` | `ZXZB26072200102` | `0a6bedb5-d1b2-5c18-b675-612ff6ed2dd2` |
| `TR03` | `ZXZB26072300103` | `107da4b2-faa0-5765-b6ed-3c77dc47acc4` |
| `TR04` | `ZXJB26081600104` | `c8b0fa5a-3c2c-5845-9253-c60fe1e501f2` |
| `TR05` | `ZXJB26082000105` | `a361dd7f-31aa-5d80-a00a-cbcb05bb3f40` |
| `TR06` | `ZXJB26083100106` | `eb5ed83c-cab8-53ae-b2f9-d4cc27f41a29` |
| `TR07` | `ZXJB26080700107` | `900aa4c7-1387-592b-9f13-33ac1bd00b3e` |
| `TR08` | `ZXZB26081200108` | `c5e411a6-0315-5585-b6f8-5d5baf37df7d` |
| `TR09` | `ZXJB26090300109` | `b141be22-6d20-5730-83f5-09b50c3c783d` |
| `TR10` | `ZXJB26090400110` | `40425323-5e5f-565f-aacf-67d92397d5d6` |
| `TR11` | `ZXJB26090500111` | `1af326ff-0d95-597b-87e1-b3bc07a0993a` |
| `TR12` | `ZXJB26090700112` | `ecb35df6-56b2-5ec1-b403-b30ac8ddcf43` |
| `TR13` | `ZXJB26091000113` | `f8eb7229-a983-5403-bcc1-d8ec21740587` |
| `TR14` | `ZXJB26092000114` | `65e5fb24-0355-5985-816a-0f170d244121` |
| `TR15` | `ZXZB26090800115` | `eeb91eca-be64-5658-83b0-fc07c4645ffd` |
| `TR16` | `ZXJB26082600116` | `b57a8929-2f88-5c14-995b-52b85759ccfa` |
| `TR17` | `ZXJB26093000117` | `34b5ce36-bc67-5b7c-9bd3-20b6567702e7` |
| `TR18` | `ZXZB26092900118` | `2441454a-94f1-50f4-a45f-9ff78bfd9c85` |
| `TR19` | `36601877` | `9c109ccc-5234-5b0a-a5d1-0fd5ad7d9901` |
| `TR20` | `ZXZB26091200120` | `4fabb0d9-461c-557f-8ff0-cb9b4ad121a7` |
| `TR21` | `ZXJB26082700121` | `d6334e7d-2bbf-5a97-a054-68a85ad76f17` |
| **trip_no_history** | | |
| `TNH03` | `36601950>ZXZB26072300103` | `d5919c97-a416-5263-9215-4c918b37f563` |
| **task_delivery_stops** | | |
| `TDS05_1` | `LH-20082026-001:1` | `e7821ea2-c7b8-5522-9c2b-ab2543ccb7d9` |
| `TDS05_2` | `LH-20082026-001:2` | `467bd6f6-7a4c-53d2-92d1-897ed1ae797c` |
| `TDS16_1` | `LH-26082026-001:1` | `4612922a-d28e-5611-80ec-d021d468c23c` |
| `TDS16_2` | `LH-26082026-001:2` | `2a0f7115-8fa8-59a2-854b-a51543273b9a` |
| `TDS16_3` | `LH-26082026-001:3` | `c8c70db3-a161-5617-95de-d097ff9d99a2` |
| **trip_delivery_stops** | | |
| `PDS05_1` | `ZXJB26082000105:1` | `15c474e5-4921-5cb5-a973-ff5ffb0a79bf` |
| `PDS05_2` | `ZXJB26082000105:2` | `d063997c-aad6-577b-bba7-c95d003fa7c7` |
| `PDS16_1` | `ZXJB26082600116:1` | `5ec52cda-5fa7-5414-9d0e-2c9035aae2e6` |
| `PDS16_2` | `ZXJB26082600116:2` | `d5d73e3b-e142-543d-a16a-4162466dcb4c` |
| `PDS16_3` | `ZXJB26082600116:3` | `74027dc7-7f36-5972-b622-2e1a2dd618d3` |
| **trip_photos** | | |
| `PH01` | `ZXJB26071000101:seal` | `82d5467b-9108-5738-aa7e-9fa099b3a42f` |
| `PH03` | `ZXZB26072300103:seal` | `2494c6de-b0bf-5210-9185-b4e6a64cdc9c` |
| `PH16` | `ZXJB26082600116:stop_2_arrived` | `90482e92-e4da-59dd-b3b0-b35dc314434d` |
| `PH19` | `36601877:pre_close` | `2c9812ac-fe60-590b-99ad-4babb2308806` |
| **standby_records** | | |
| `SB01` | `D2:2026-07-18T08:00` | `f163f04e-bb3f-5b7a-9c26-0632a36b8ed2` |
| `SB02` | `D1:2026-08-31T20:00` | `c727daf6-b31a-553a-b0f0-6d7d42135dd8` |
| `SB03` | `D1:2026-08-12T05:00` | `5d1c38d2-7934-5e42-9bea-1a9f984ea894` |
| `SB04` | `D2:2026-09-09T13:00` | `f2fdd96b-be78-59e3-a43d-b6e0a85678f3` |
| **incident_reports** | | |
| `IR1` | `ZXZB26092900118:2026-09-29T10:05` | `89165165-ccd8-588e-a662-f6c9a4f0b459` |
| **customer_rate_entries** | | |
| `RE01` | `CJSF:rc_1782354600000:SPK-GW:SPK890103:4WJ:PRIMARY` | `8b787ff4-3904-5817-afeb-4501fd15349c` |
| `RE02` | `CJSF:rc_1782354600000:SPK-GW:SPK890103:4WJ:SUPPLEMENTARY` | `1a827112-64ab-5f40-b031-9e7a6a68b1d2` |
| `RE03` | `CJSF:rc_1782354600000:SPK-GW:SPK890146:4WJ:PRIMARY` | `b4e424c8-07da-5566-a544-ddb5157c6306` |
| `RE04` | `CJSF:rc_1782354600000:SPK-GW:SPK890174:6WH:PRIMARY` | `047237d6-a2e1-552e-80e6-4b222a3dd197` |
| `RE05` | `CJSF:manual_1789376400000:SPK-GW:SPK890103:4WJ:PRIMARY` | `11864c30-1b79-53b2-b31c-ab758953ec9e` |
| `RE06` | `SPX:rc_1782354600000:ALANG-A:SOCE:4WJ:PRIMARY` | `1b0c2327-940e-5f36-bd03-daa56d526782` |
| `RE07` | `SPX:rc_1782354600000:ALANG-A:WANGTHONGLANG12:4WJ:SUPPLEMENTARY` | `697b38a0-f67b-5ec7-8c13-96dce16871a0` |
| `RE08` | `SPK:rc_1782354600000:SPK-GW:SPK890146:4WJ:PRIMARY` | `5f9746ec-eebb-5d48-b92d-bc9217fff974` |
| `RE09` | `TTP:rc_1784948400000:TTP-HUB1:SOCE:6WH:PRIMARY` | `7f81cb58-0c3b-5028-afde-01273a654a43` |
| `RE10` | `TTP:rc_1784948700000:TTP-HUB1:SOCE:6WH:PRIMARY` | `d41c9b30-b0ad-5bce-9bab-2770db740b9f` |
| **customer_fuel_rate_adjustments** | | |
| `FA01` | `CJSF:2026-07-01:1` | `ee840afe-1aa5-59aa-bbc0-19e6615fc879` |
| `FA02` | `CJSF:2026-08-16:1` | `dac71280-ba35-5d1f-b4d9-54a3334829ab` |
| `FA03` | `CJSF:2026-09-01:1` | `0de76964-aaef-54b9-82af-7e6fee474ad1` |
| `FA04` | `CJSF:2026-09-16:1` | `e71949cd-36fb-5c0b-bf3e-d30358009faf` |
| `FA05` | `SPX:2026-07-01:1` | `b34378df-994a-5acd-8baa-f79a55cf9720` |
| `FA06` | `SPK:2026-07-01:1` | `01dedcd2-0d3b-572c-b630-d3fb2a5b8022` |
| **customer_service_fees** | | |
| `SF01` | `SPK:extra_stop` | `815b13ec-a97c-51b5-8d77-078bcdeca019` |
| `SF02` | `TTP:standby` | `ee4e7fb5-9521-5c4f-b538-e16a44431e8b` |
| `SF03` | `CJSF:waiting_time` | `21297f15-d1bb-5ab8-ba30-01d2c40a9fc9` |
| **standby_rate_entries** | | |
| `SR01` | `CJSF:2026-07-01` | `a1444ec6-e175-518f-9f81-0d48d99c4d1e` |
| `SR02` | `CJSF:2026-09-01` | `918bebcd-e1f6-5c63-bc71-8b5438a17521` |
| `SR03` | `TTP:2026-07-01` | `20b8d70a-cefa-516d-a656-138836b27ac2` |
| **billing_statements** | | |
| `ST1` | `CJSF-202607-001` | `e47e89d7-d299-5402-a8ac-26c060b66437` |
| `ST2` | `SPX-202607-001` | `16819c6f-9940-5778-ae11-9e8fc6535cba` |
| `ST3` | `CJSF-202608-001` | `2026e9cd-af0f-5961-82fb-922a18824b54` |
| `ST4` | `TTP-202608-001` | `9b019f13-548e-5548-80a7-d2092b2cdcbd` |
| **billing_statement_lines** | | |
| `SL1_1` | `CJSF-202607-001:1` | `a8705389-f91f-5c2c-8bf0-a001e190373d` |
| `SL1_2` | `CJSF-202607-001:2` | `1f3e6de0-9345-559a-abf9-c54f35ff6072` |
| `SL3_1` | `CJSF-202608-001:1` | `5705bfb7-479d-515f-81a6-12b958e28fd4` |
| `SL3_2` | `CJSF-202608-001:2` | `f8ec621e-d075-53db-bfe0-83f223029a6c` |
| `SL3_3` | `CJSF-202608-001:3` | `09e74d8e-a1e7-55fe-8153-b27e337048a9` |
| `SL3_4` | `CJSF-202608-001:4` | `a2e5fa4e-5895-51b1-b28d-fbe4df0ae1d1` |
| `SL3_5` | `CJSF-202608-001:5` | `0e9c95b6-2670-50f8-85b6-414e9bcf71d5` |
| `SL4_1` | `TTP-202608-001:1` | `f05138ab-4284-5901-8ed4-70dfbfd0fea4` |
| `SL4_2` | `TTP-202608-001:2` | `89579a63-8d03-53a1-be61-17eb1cd5f63b` |
| **statement_documents** | | |
| `SD1_INV` | `CJSF-202607-001:invoice_summary_pdf` | `3d98622f-da02-5ad1-a58d-85a4b5eb6e9a` |
| `SD1_RCPT` | `CJSF-202607-001:receipt_pdf` | `8f4745a3-0dda-5ba8-8310-441f46b01729` |
| **vehicle_expenses** | | |
| `EX01` | `1ขค-1234:2026-09-18T07:40` | `d1d140ff-2770-5e7a-88aa-43070430f8c5` |
| `EX02` | `1ขค-1234:2026-09-28T18:10` | `6704ae65-04d6-5c73-85d7-0db13a1e952f` |
| `EX03` | `3ฒท-9012:2026-08-27T09:12` | `4ed8841e-fd77-5b3d-8a0c-aab9c20f7d7b` |
| `EX04` | `2ขค-5678:2026-09-12T19:30` | `b0c19afb-ab01-520e-879c-c2da0299c838` |
| **maintenance_records** | | |
| `MT01` | `2ขค-5678:PM:2026-08-05` | `bdf76489-cc81-5287-aba8-9519953bfd36` |
| `MT02` | `3ฒท-9012:CM:2026-09-02` | `4f6732ca-b82b-5ce7-a519-19fc2996824e` |
| `MT03` | `1ขค-1234:PM:2026-10-02` | `088f01fa-d7d6-5813-b268-80bb98035c14` |
| **driver_compensation_configs** | | |
| `CC1` | `WRT:2026-01-01` | `3dc316c9-8002-526d-aa95-d9bf1933ee4e` |
| `CC2` | `WRT:2026-09-01` | `a3c4d3ca-516c-5c38-8425-d33ecf6eaa15` |
| **driver_penalties** | | |
| `PN01` | `D1:2026-08-21T09:00` | `f048a9aa-2cb5-5349-b67e-6c21f3265437` |
| `PN02` | `D2:2026-08-28T10:00` | `65821f8b-9616-5baa-828a-ed20fdd35547` |
| `PN03` | `D1:2026-06-10T09:00` | `52e65b90-0ad9-5ad6-888b-734ea06017f8` |
| `PN04` | `D6:2026-07-30T09:00` | `bae4c380-eaad-58b6-908a-38b47b1e1f7f` |
| **payroll_runs** | | |
| `PR01` | `D1:2026-08:R1` | `5df5daa2-5519-552c-a847-bf028daa8e87` |
| `PR02` | `D1:2026-08:R2` | `25c25436-e886-53c0-8b63-9e608de36ccb` |
| `PR03` | `D2:2026-08:R2` | `aa312606-c3c2-5b5f-b416-ccedebe68ff8` |
| **payroll_line_items** | | |
| `PL01_1` | `D1:2026-08:R1:1` | `077bd776-a656-5897-8961-2eafcb6188a9` |
| `PL02_1` | `D1:2026-08:R2:1` | `13a1e798-0e14-57c4-9bb8-78ca278547c8` |
| `PL02_2` | `D1:2026-08:R2:2` | `df96a32f-424e-5bd4-86ac-456b5605290d` |
| `PL02_3` | `D1:2026-08:R2:3` | `1a760ba8-f7c6-5198-bff3-4393d801bddc` |
| `PL02_4` | `D1:2026-08:R2:4` | `34090bf7-4a15-5aef-8e34-b7d536da95ce` |
| `PL03_1` | `D2:2026-08:R2:1` | `417110ce-d9db-5233-bc06-63a054daa165` |
| **transactions** | | |
| `TX01` | `payout:D1:2026-08:R1` | `838ef529-f268-57d3-822b-7c729c1ec0f1` |
| `TX02` | `payout:D1:2026-08:R2` | `401daefd-6cf6-581c-b03e-28ceb1097c4e` |
| **driver_advances** | | |
| `DA01` | `D1:2026-09-08T12:00` | `a000f45e-eff7-54da-abcf-625b5a849f77` |
| **chats** | | |
| `CH01` | `D2:2026-09-29T10:08` | `3fc9ef38-f30c-57c3-931a-01795be22d2d` |
| `CH02` | `D1:2026-09-18T08:00` | `447194e5-cf75-5f5b-8904-d599680c4489` |
| **chat_messages** | | |
| `CM1` | `CH01:2026-09-29T10:08` | `5945c9ae-4e6f-530e-8e8c-5440b2d4deaf` |
| `CM2` | `CH01:2026-09-29T10:09` | `00adbfb1-fc9a-5c69-8c05-4572a5367f17` |
| `CM3` | `CH02:2026-09-18T08:20` | `7003db82-cedc-52f0-8299-27a5acd16fa5` |
| **broadcasts** | | |
| `BC01` | `2026-08-10T09:00` | `79abca5e-a725-5551-bcdd-ec973ccf9eb0` |
| `BC02` | `2026-09-01T09:00` | `11650ea6-53e6-551c-8e88-9e7321b5d9f0` |
| **leave_requests** | | |
| `LV01` | `D1:2026-09-14` | `63fad54c-0495-50e0-b698-a83a885ac72e` |
| `LV02` | `D2:2026-10-05` | `fb46df0f-fb5a-5f52-b8e0-b5c3399ea43b` |
| `LV03` | `D3:2026-08-03` | `933d6b65-7964-5afd-8e37-bf57bcce82bc` |
| **holidays** | | |
| `HO1` | `public:2026-07-28:public` | `51e57dfc-f427-5de4-95e3-d27bc032032f` |
| `HO2` | `public:2026-08-12:public` | `7ac37e18-91b7-5125-ad81-6017891cf8dc` |
| `HO3` | `public:2026-10-13:public` | `30f548fe-d700-5796-8eca-9413c42ff1bf` |
| `HO4` | `WRT:2026-12-30:company` | `e87ed5a3-7645-55ef-ac82-8ea49f307a3c` |
| **mobile_app_releases** | | |
| `MR01` | `prod:3.5.0` | `3ed967f3-bcc3-5b74-ae1f-b7b370081085` |
| **jobs** | | |
| `JB1` | `billing.backfill-trips:2026-09-25T10:00` | `fb57e682-6639-5ccf-b4f6-ff9ce661f99c` |
| `JB2` | `tenancy.orphan-scan:2026-09-30T03:00` | `9d16b0ba-1f8e-5558-8b22-b6212ddc6f02` |
| **security_events** | | |
| `SE1` | `platform_cross_tenant_access:2026-09-30T11:00` | `9c84176b-ddeb-575c-9979-67edfa4b85e6` |
| **outbox_events** | | |
| `EV1` | `trip.delivered:ZXJB26092000114` | `097ad950-2ab0-53c9-8c6a-3177fe5fc59b` |
| `EV2` | `task.assigned:FM-01102026-001` | `8deb2367-8450-5701-853d-abbb5ca284d2` |
| `EV3` | `statement.created:CJSF-202608-001` | `086e497a-beeb-5a46-a6f2-348296439855` |
| **file_objects** | | |
| `FO_LOGO` | `logitrack:companies/@CO_WRT/logo-1767578400000.png` | `13d07204-f31e-5885-8bd6-b58aedb4b39d` |
| `FO_STAMP` | `logitrack:companies/@CO_WRT/stamp-1767578400000.png` | `5642c434-e7f8-5a4d-b27f-9f5e00b21f14` |
| `FO_SIG` | `logitrack:companies/@CO_WRT/signature-1767578400000.png` | `3dc86679-a13e-5ecf-9946-25fd81e21550` |
| `FO_TR01_SEAL` | `logitrack:trips/@TR01/seal-1783651800000.jpg` | `85916ff1-773d-5919-a3d7-43f0d63ff845` |
| `FO_TR03_SEAL` | `logitrack:trip_records/36601950/seal.jpg` | `9af3308e-07c5-5a09-889c-b3226d5e7df0` |
| `FO_TR16_S2` | `logitrack:trips/@TR16/stop_2_arrived-1787724300000.jpg` | `a036b0c2-9075-5490-8cc5-ec16d8fb9d63` |
| `FO_TR19_PC` | `logitrack:trip_records/36601877/pre_close.jpg` | `be407eb7-fa8c-55c3-8100-42220d316165` |
| `FO_TK24_CI` | `logitrack:checkin/@TK24/1790732520000.jpg` | `00d4b5da-1b99-5433-8409-9ef29f29026c` |
| `FO_TK24_APP` | `logitrack:checkin/@TK24/app_screenshot_1790732580000.jpg` | `40d3ac7c-2312-582e-901a-1e860712e953` |
| `FO_IR1_S1` | `logitrack:incidents/@IR1/situation1-1790651040000.jpg` | `3131477b-9ad3-5113-bbd3-d3d87f01dd84` |
| `FO_CH01_IMG` | `logitrack:chats/@CH01/1790651340000.jpg` | `ba0267ee-0190-55b7-a838-2668b597d77d` |
| `FO_EX01_R` | `logitrack:expenses/@EX01/receipt-1789692060000.jpg` | `ef7a535e-defc-5da1-967c-c5be2bc0db67` |
| `FO_APK` | `logitrack-public:app_releases/prod/logitrack-prod-v3.5.0.apk` | `1f355add-de9c-57f2-82e2-4173cf276765` |
| `FO_ST1_INV` | `logitrack:documents/statements/@ST1/invoice_summary.pdf` | `60836e27-afec-5d30-bd1e-9ac6e82db43e` |
| `FO_ST1_RCPT` | `logitrack:documents/statements/@ST1/receipt.pdf` | `db42625d-6823-53d0-9d9e-32d659149a6f` |
| `FO_TR17_PEND` | `logitrack:trips/@TR17/arrived-1790729700000.jpg` | `00ed7fc8-db5f-5d8b-a3b1-46323293e20e` |

</details>

### D.4.3 Billing golden vectors — trips

Notation (main spec §6): `round2(x) = jsRound(x × 100) / 100` in float64, where `jsRound(y) = f + (y − f ≥ 0.5 ? 1 : 0)` with `f = floor(y)` — the JS `Math.round` semantics (ties toward +∞, so −2.5 → −2; also exact for 0.49999999999999994, unlike `floor(y + 0.5)`), with fused multiply-add blocked by an explicit `float64(b × m)` conversion. Final rate = `round2(base × multiplier + add)`. Fuel band (ADR 0009 §3): floor `f = ceil(round(p × 100) / 100) − 1`, band `(f + 0.01, f + 1]`, surcharge `round2((f − baseline) × THB per baht)` with baseline 41 and 10 THB per baht, signed and never clamped. Selection compares **Bangkok dates**, not instants: newest non-voided rate entry with `effective_from_date ≤ bill date` (else the oldest), newest non-voided fuel adjustment on or before the bill date (no fallback, skipped for SUPPLEMENTARY). Customer = `billing_party_id`, else source-hub link, else destination-hub link. Bill date = `billing_date` (plan basis: the task's `plan_at`; delivered basis: `delivered_at`), else `delivered_at`, else `created_at`.

| Vector | Trip | Party, basis, bill instant | Lookup (hub → dest, class, category) | Rate entry | Fuel adjustment | Computation | Expected snapshot |
|---|---|---|---|---|---|---|---|
| V01 | `TR01` | CJSF, plan, 2026-07-10 06:00 | SPK-GW → `SPK890103 - ลาดกระบัง26` ⇒ SPK890103, 4WJ, PRIMARY | `RE01` 1200.00 (eff 2026-07-01, legacy 07:00 ICT instant) | `FA01` ×1.000000 −70.00; ref 34.50 ⇒ f = 34 ⇒ (34 − 41) × 10 | round2(1200.00 × 1 − 70.00) | **1130.00**; round 2026-07-01; band 34.01–35.00 |
| V02 | `TR02` (run by NWR) | SPX, delivered, 2026-07-22 10:15 | ALANG-A → `SOCE (บัวโรย)` ⇒ SOCE, 4WJ, PRIMARY | `RE06` 1000.00 | `FA05` ×1.100000 +50.00; no reference price ⇒ no band | round2(1000 × 1.1 + 50) | **1150.00** (golden `lib/billingCompute.test.ts:279`); legacy `etl` row with NULL `billing_date` (axis falls back to delivery) |
| V03 | `TR03` (renamed, legacy) | SPX, delivered | ALANG-A → WANGTHONGLANG12, 4WJ; task category NULL | PRIMARY probe: none ⇒ SUPPLEMENTARY `RE07` 1250.00 | skipped | 1250.00 | **1250.00**; `manual_override` true; trip category written SUPPLEMENTARY (golden `billingCompute.test.ts:389`) |
| V04 | `TR04` | CJSF, plan, **2026-08-16 00:21** | as V01 | `RE01` | `FA02` −50.00, eff 2026-08-16 stored at 07:00 ICT; same Bangkok day ⇒ in round | round2(1200 − 50) | **1150.00**; round 2026-08-16; band 36.01–37.00. Comparing instants would pick `FA01` ⇒ 1130.00 (golden `billingCompute.test.ts:198`) |
| V05 | `TR05` multi-drop, legacy per-route | CJSF, plan, 2026-08-20 07:00 | delivered stops in completion order SPK890103, SPK890146; CJSF has no `extra_stop` fee | `RE01` 1200.00, `RE03` 1150.00 | `FA02` −50.00 on every stop | stop 1 = round2(1200 − 50) = 1150.00 (base); stop 2 = round2(1150 − 50) = 1100.00 (stop charge); total = float sum, not re-rounded | **2250.00**; `base_rate_thb` 1150.00 is the fuel-adjusted base (multi-row meaning); breakdown (1, SPK890103, 1200.00, 1150.00), (2, SPK890146, 1150.00, 1100.00) |
| V06 | `TR06` | CJSF, plan, 2026-08-31 22:00 (delivered 2026-09-01 03:10) | as V01 | `RE01` | `FA02` | round2(1200 − 50) | **1150.00** on the August plan axis; on the delivery axis it would be `FA03` ⇒ 1160.00 in September |
| V07 | `TR07` (legacy) | CJSF, plan, but `billing_date` NULL; legacy priced at delivery 2026-08-08 11:00 | as V01 | `RE01` | `FA01` | round2(1200 − 70) | **1130.00** (`etl`). Diagnostic `missing_billing_date`: invisible to the plan-axis August query. Repair (D7): stamp `billing_date := TK07.plan_at` (2026-08-07 08:00) because August is not locked (`ST3` draft); reprice ⇒ same round ⇒ 1130.00 |
| V08 | `TR08` | TTP, delivered, 2026-08-12 15:20 | TTP-HUB1 → SOCE, 6WH | `RE09` 2100.00 vs `RE10` 2150.00, both eff 2026-08-01 00:00; R16 ⇒ `created_at` ASC (10:00 < 10:05) ⇒ `RE09` | none for TTP ⇒ ×1 +0 | 2100.00 | **2100.00**; round 2026-08-01 (rate-entry date when no fuel applies) |
| V09 | `TR09` | NWR (tenant-kind party, plan), 2026-09-03 08:00 | SPK890146 → `SPK-GW` ⇒ lookup **`SPK`** (dash collapse) | none for `BP_NWR` | — | — | **unpriced `no_rate`**; snapshot row with NULL estimate and `unpriced_reason='no_rate'`; `billing_date` still stamped |
| V10 | `TR10` | CJSF, plan, 2026-09-04 06:00 | SPK-GW → SPK890103; task `truck_type` NULL; plate กข-1111 with no truck | — | — | R15: no class is guessed | **unpriced `no_vehicle_class`** (stored `unpriced_reason`). The legacy engine (blank ⇒ 4WJ) would have priced 1160.00; divergence recorded in ADR 0029 |
| V11 | `TR11` | CJSF, plan, 2026-09-05 07:00 | as V01, SUPPLEMENTARY explicit | `RE02` 950.00 | skipped | 950.00 | **950.00**; `manual_override` true ⇒ frozen |
| V12 | `TR12` (corrupted) | CJSF, plan, 2026-09-07 07:00, SUPPLEMENTARY | as V11 | `RE02` 950.00 | stored `FA03` −40.00 (legacy browser writer) | stored round2(950 − 40) | stored **910.00**, `manual_override` false ⇒ carries fuel ⇒ **not** frozen ⇒ a forced recompute (`accounting:recompute_force`) rewrites **950.00**, no fuel, `manual_override` true |
| V13 | `TR13` | CJSF, plan, 2026-09-10 07:00 | as V01 | `RE01` | engine: `FA03` ⇒ 1160.00 | admin typed 1500.00; \|1500 − 1160\| > 0.001 ⇒ manual | **1500.00**; `manual_override` true; `computed_by` `manual_edit`; no round provenance (the dialog never wrote it) |
| V14 | `TR14` | CJSF, plan, 2026-09-20 07:00 | as V01 | `RE05` 1300.00 (eff 2026-09-16) is voided ⇒ `RE01` 1200.00 | `FA04` −30.00 (eff 2026-09-16) is voided ⇒ `FA03` −40.00 | round2(1200 − 40) | **1160.00**; round 2026-09-01; band 37.01–38.00 (value of golden `lib/billingRates.test.ts:65`; void fallback of `billingCompute.test.ts:142`); `last_event_id` = 1 (the outbox row of `EV1`) |
| V15 | `TR15` | no party: legacy task without hub links | BANGNA-DRV → SOCE | — | — | — | **unpriced `no_customer`**; snapshot row from the scheduler safety net with NULL estimate and `unpriced_reason='no_customer'`; trip `billing_party_id` NULL |
| V16 | `TR16` multi-drop, flat fee | SPK, delivered, 2026-08-26 17:55 | planned SPK890146; completion order SPK890103, SPK890146, SPK890174 | `RE08` 1100.00 for the planned stop only | `FA06` ×1.000000 +0.00 on the base only | base = round2(1100 × 1 + 0) = 1100.00; every other delivered stop = `SF01` 300.00, no fuel, numbered 2, 3 in array order | **1700.00**; breakdown (1, SPK890146, 1100.00, 1100.00), (2, SPK890103, 300.00, 300.00), (3, SPK890174, 300.00, 300.00) |
| V21 | `TR21` | CJSF, plan, 2026-08-27 06:00 | SPK-GW → `SPK890174 - ห้วยขวาง10` ⇒ SPK890174, 6WH | `RE04` 1850.00 | `FA02` −50.00 | round2(1850 − 50) | **1800.00** (`scheduler` safety net); regression "SPK890174 resolves to a rate" |
| E1 | engine-only | `billing_date`, `delivered_at` and `created_at` all absent | — | — | — | legacy fell back to `Date.now()` | **unpriced `no_billing_date`** (R19). Not representable as a stored trip (`trip_records.created_at` is NOT NULL; the ETL fills it with `COALESCE(createdAt, std, deliveredTimestamp, Firestore create time)` and records `created_at_derived`, R68), so it lives only in the engine's test vectors |

Unpriced reasons are stored by the engine in `trip_billing_snapshots.unpriced_reason` and `standby_records.billing_unpriced_reason` (R62) and surfaced by the diagnostics endpoints; the `_billing` annotations only name the vector, and invariant #2 re-derives every stored reason.

### D.4.4 Standby and statement vectors

| Vector | Row | Inputs | Result |
|---|---|---|---|
| S01 | `SB01` | CJSF, ended 2026-07-18 15:30; record customer `BP_CJSF` | `SR01` (eff 2026-07-01) ⇒ **800.00**, source `standby_rate` |
| S02 | `SB02` | CJSF (plan basis), task `TK06`, ended **2026-09-01 02:00** | standby always bills on `ended_at` ⇒ `SR02` (eff 2026-09-01) ⇒ **850.00** in September, while `TR06` of the same task bills 1150.00 in August |
| S03 | `SB03` | TTP via task `TK08`; `SR03` voided (R20) | no live standby rate ⇒ `SF02` fee ⇒ **500.00**, source `service_fee`, `billing_rate_entry_id` NULL, effective date NULL |
| S04 | `SB04` | SPX, ended 2026-09-09 | no standby rate, no `standby` fee ⇒ **unpriced**, stored `billing_unpriced_reason='no_rate'` (R62) |

Customer resolution keeps legacy parity (R19): record customer, else the task's source-hub link, else its destination-hub link — never the task's `billing_party_id`.

| Statement | Status | Rows | Totals (WHT rate 0.0100 snapshot, R18) | Effect |
|---|---|---|---|---|
| `ST1` `CJSF-202607-001` | paid | `TR01` 1130.00, `SB01` 800.00 | 1930.00; WHT round2(1930.00 × 0.0100) = 19.30; net 1910.70; `trip_count` 2 (all rows), `trip_only_count` 1, `standby_count` 1 | locks CJSF 2026-07 (`JB1` forced backfill ⇒ blocked) |
| `ST2` `SPX-202607-001` | sent | `TR02` 1150.00, `TR03` 1250.00 (legacy statement: no line rows) | 2400.00; 24.00; 2376.00 | locks SPX 2026-07 |
| `ST3` `CJSF-202608-001` | draft | `TR04` 1150.00, `TR05` stop 1 1150.00, stop 2 1100.00, `TR06` 1150.00, `TR21` 1800.00; excluded: `TR07` (`billing_date` NULL), `SB02` (ended in September) | 6350.00; 63.50; 6286.50; trip only 3 = 4100.00; multi-drop rows 2 = 2250.00 | no lock |
| `ST4` `TTP-202608-001` | cancelled | `TR08` 2100.00, `SB03` 500.00 | 2600.00; 26.00; 2574.00 | no lock; counter stays 1 |

The 0.0100 rate is the statement snapshot of `companies.withholding_tax_rate` = 1.00 (R18); the owner confirms 1 % before the seed is final (owner question Q6, main spec §19.2).

### D.4.5 Payroll golden vector — driver `D1`, August 2026, config `CC1`

Rules (compensation engine, main spec §6): R1 = Bangkok days 1–15, R2 = 16–end; trip pay counts trips delivered in the round, excluding multi-stop and standby; holiday = Sunday or a `holidays` date; helper day key = Bangkok date of (check-in − 12 h), paid only on days with no own driving key; R2 only: trip-volume tier, SSO, penalty instalments (`round(total / installments)` capped at the balance), applied after SSO and never below zero; `net = max(0, earnings − deductions)`. The Go port fixes the legacy query on `tasks.driverId == authId` (main spec §1) by using `driver_id`.

| Run | Inputs from the smoke rows | Lines | Totals |
|---|---|---|---|
| `PR01` R1 (`paid`) | `TR07` delivered Sat 2026-08-08 ⇒ weekday 300; `TR08` delivered Wed 2026-08-12 = `HO2` ⇒ holiday 350 | `PL01_1` TRIP_COMMISSION 650.00 | earnings 650.00, deductions 0.00, net **650.00**; ledger `TX01` 650.00 dated 2026-08-18 (Bangkok) |
| `PR02` R2 (`approved`) | `TR04` Sun 2026-08-16 ⇒ 350; `TR21` Thu 2026-08-27 ⇒ 300; `TR05` multi-stop excluded. Helper on `TK16` (check-in 2026-08-26 07:10 ⇒ key 2026-08-25); D1's driving keys 08-07, 08-12, 08-15, 08-19, 08-20, 08-26, 08-27, 08-31 ⇒ eligible ⇒ 1 × 400. Month payable trips 4 < 50 ⇒ no volume tier. SSO: hired 2024 (< 2026) ⇒ base 15,000 × 5 % = 750 (probation passed, age 42). `PN01` 600 in 3 ⇒ instalment 200. | `PL02_1` TRIP_COMMISSION 650.00, `PL02_2` HELPER_PAY 400.00, `PL02_3` SOCIAL_SECURITY 750.00, `PL02_4` PENALTY 200.00 | earnings 1050.00; SSO 750 leaves 300; penalty 200 leaves 100; net **100.00**; ledger `TX02` 100.00; `PN01` ⇒ remaining 400.00, 1 of 3 paid, `partially_deducted` |
| `PR03` R2 (`draft`, driver `D2`) | only `TR16` (multi-stop) delivered in R2 | `PL03_1` TRIP_COMMISSION 0.00 (always written); SSO and `PN02` apply 0 ⇒ no lines | all 0.00 |

### D.4.6 Identity and tenancy

#### `tenants` (4)

```json
[
{"id": "@TN_OWN", "kind": "own_fleet", "code": "WRT", "name_th": "วันเพ็ญ-รัชดา", "name_en": "Wanpen-Ratchada (seed)", "legal_type": "company", "tax_id": "0999900000015", "contact_person": "ฝ่ายปฏิบัติการ", "phone": "000-000-0001", "email": "ops@logitrack.test", "fleet_size": 4, "status": "active", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@TN_NWR", "legacy_doc_id": "seedSubDoc0000000NWR", "kind": "carrier", "code": "NWR", "name_th": "บจก. นภาวารีขนส่ง (ตัวอย่าง)", "name_en": "Napawaree Transport (sample)", "legal_type": "company", "tax_id": "0999900000023", "contact_person": "หัวหน้ากลุ่ม NWR", "phone": "000-000-0002", "designation": "นายหน้าหารถ (broker carrier)", "fleet_size": 1, "service_regions": ["กรุงเทพมหานคร", "สมุทรปราการ"], "vehicle_types": ["4WJ"], "line_group_id": "C84c410d7aace719d6f143c168783119f", "contractor_tenant_id": "@TN_OWN", "status": "active", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@TN_TTP", "kind": "carrier", "code": "TTP", "name_th": "ทีทีพี ดิสแพตช์ (ตัวอย่าง)", "name_en": "TTP Dispatch (sample)", "legal_type": "company", "tax_id": "0999900000031", "contact_person": "ศูนย์จ่ายงาน TTP", "phone": "000-000-0003", "designation": "dispatcher + own fleet", "fleet_size": 1, "vehicle_types": ["6WH"], "line_group_id": "Cbe2afe030e16ef426cb06d0becb7051f", "status": "active", "created_at": "2026-01-05T09:00:00+07:00", "_note": "Go-created, no contractor link (R60)"},
{"id": "@TN_QUAR", "kind": "quarantine", "name_th": "กักกันข้อมูล", "name_en": "Quarantine", "status": "active", "_source": "migration 0002 (R56), not seeded"}
]
```

#### `customers` (4)

```json
[
{"id": "@CU_CJSF", "code": "CJSF", "name": "ลูกค้าทดสอบ CJSF", "address": "เลขที่ 1 ถนนทดสอบ เขตบางนา กรุงเทพมหานคร 10260", "tax_id": "0999900000040", "branch_type": "hq", "payment_terms_days": 30, "billing_email": "billing.cjsf@logitrack.test", "line_group_id": "C32dc1e8ee8be0f9c208c831822762f4b", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@CU_TTP", "code": "TTP", "name": "ลูกค้าทดสอบ TTP", "tax_id": "0999900000031", "branch_type": "hq", "payment_terms_days": 30, "line_group_id": "Cbe2afe030e16ef426cb06d0becb7051f", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@CU_SPX", "code": "SPX", "name": "ลูกค้าทดสอบ SPX", "tax_id": "0999900000058", "branch_type": "hq", "payment_terms_days": 15, "line_group_id": "Ccbcc507873126e32c572820ea77977ee", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@CU_SPK", "code": "SPK", "name": "ลูกค้าทดสอบ SPK (เครือข่าย J&T)", "tax_id": "0999900000066", "branch_type": "branch", "branch_number": "00001", "payment_terms_days": 30, "line_group_id": "C6a597d94b94dc7c587ea38748c6833ce", "created_at": "2026-01-05T09:00:00+07:00"}
]
```

#### `billing_parties` (5)

```json
[
{"id": "@BP_CJSF", "kind": "customer", "customer_id": "@CU_CJSF", "billing_date_basis": "plan"},
{"id": "@BP_TTP", "kind": "customer", "customer_id": "@CU_TTP", "billing_date_basis": "delivered"},
{"id": "@BP_SPX", "kind": "customer", "customer_id": "@CU_SPX", "billing_date_basis": "delivered"},
{"id": "@BP_SPK", "kind": "customer", "customer_id": "@CU_SPK", "billing_date_basis": "delivered"},
{"id": "@BP_NWR", "kind": "tenant", "tenant_id": "@TN_NWR", "billing_date_basis": "plan"}
]
```

#### `companies` (1)

```json
[
{"id": "@CO_WRT", "tenant_id": "@TN_OWN", "tenant_source": "self", "name_th": "วันเพ็ญ-รัชดา", "name_en": "Wanpen-Ratchada (seed)", "short_name": "WRT", "tax_id": "0999900000015", "branch_type": "headquarters", "branch_number": "00000", "address": "เลขที่ 9 ถนนทดสอบ เขตลาดกระบัง กรุงเทพมหานคร 10520", "phone": "000-000-0001", "email": "billing@logitrack.test", "logo_file_id": "@FO_LOGO", "stamp_file_id": "@FO_STAMP", "signature_file_id": "@FO_SIG", "signatory_name": "ผู้จัดการฝ่ายบัญชี (ตัวอย่าง)", "bank_name": "ธนาคารทดสอบ", "account_number": "000-0-00000-0", "account_name": "วันเพ็ญ-รัชดา (seed)", "withholding_tax_rate": "1.00", "company_type": "owner", "is_active": true, "created_at": "2026-01-05T09:00:00+07:00"}
]
```

#### `users` (12)

```json
[
{"id": "@U_PLAT", "email": "platform.admin@logitrack.test", "email_verified": true, "display_name": "Platform Admin (seed)", "password_hash": "$seed:argon2id(SEED_DEFAULT_PASSWORD)", "status": "active", "auth_version": 1, "last_login_at": "2026-09-30T10:58:00+07:00", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@U_WRT_ADMIN", "email": "wrt.admin@logitrack.test", "email_verified": true, "display_name": "WRT Tenant Admin", "password_hash": "$seed:argon2id(SEED_DEFAULT_PASSWORD)", "status": "active", "auth_version": 1, "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@U_WRT_MGR", "email": "wrt.manager@logitrack.test", "email_verified": true, "display_name": "WRT Manager", "password_hash": "$seed:argon2id(SEED_DEFAULT_PASSWORD)", "status": "active", "auth_version": 1, "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@U_WRT_OPS", "email": "wrt.ops@logitrack.test", "email_verified": true, "display_name": "WRT Operation Staff", "password_hash": "$seed:argon2id(SEED_DEFAULT_PASSWORD)", "status": "active", "auth_version": 1, "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@U_NWR_ADMIN", "email": "nwr.admin@logitrack.test", "email_verified": true, "display_name": "NWR หัวหน้ากลุ่ม", "password_hash": "$seed:argon2id(SEED_DEFAULT_PASSWORD)", "status": "active", "auth_version": 1, "legacy_auth_uid": "SEEDUID0000000000000000000N1", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@U_TTP_DISP", "email": "ttp.dispatch@logitrack.test", "email_verified": true, "display_name": "TTP Dispatcher", "password_hash": "$seed:argon2id(SEED_DEFAULT_PASSWORD)", "status": "active", "auth_version": 1, "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@U_CJSF_CUST", "email": "cjsf.viewer@logitrack.test", "email_verified": true, "display_name": "CJSF Viewer", "password_hash": "$seed:argon2id(SEED_DEFAULT_PASSWORD)", "status": "active", "auth_version": 1, "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@U_D1", "email": "d1.somchai@logitrack.test", "email_verified": true, "display_name": "สมชาย ใจดี", "password_hash": null, "status": "active", "auth_version": 1, "legacy_auth_uid": "SEEDUID0000000000000000000D1", "legacy_scrypt_hash": "$seed:firebase_scrypt(SEED_DEFAULT_PASSWORD, FIREBASE_SCRYPT_* test params)", "legacy_scrypt_salt": "$seed:16 bytes from SEED_RANDOM_SEED", "legacy_auth_created_at": "2025-11-02T10:00:00+07:00", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@U_D2", "email": "d2.wichai@logitrack.test", "email_verified": true, "display_name": "วิชัย ทองดี", "password_hash": null, "status": "active", "auth_version": 1, "legacy_auth_uid": "SEEDUID0000000000000000000D2", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@U_D3", "email": "d3.anan@logitrack.test", "email_verified": true, "display_name": "Anan Kumar", "password_hash": "$seed:argon2id(SEED_DEFAULT_PASSWORD)", "password_changed_at": "2026-05-01T09:00:00+07:00", "status": "disabled", "auth_version": 3, "disabled_at": "2026-09-01T09:00:00+07:00", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@U_D6", "email": "d6.amnat@logitrack.test", "email_verified": true, "display_name": "อำนาจ ดวงดี", "password_hash": "$seed:argon2id(SEED_DEFAULT_PASSWORD)", "status": "active", "auth_version": 1, "legacy_auth_uid": "SEEDUID0000000000000000000D6", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@U_D7", "email": "d7.kitti@logitrack.test", "email_verified": true, "display_name": "กิตติ พงษ์พันธ์", "password_hash": "$seed:argon2id(temporary password, shown once)", "status": "active", "auth_version": 1, "must_change_password": true, "created_at": "2026-01-05T09:00:00+07:00"}
]
```

#### `auth_identities` (5)

```json
[
{"id": "@AI_NWR_FB", "user_id": "@U_NWR_ADMIN", "provider": "firebase_legacy", "provider_subject": "SEEDUID0000000000000000000N1", "email_at_link": "nwr.admin@logitrack.test", "linked_at": "2026-01-05T09:00:00+07:00"},
{"id": "@AI_D1_FB", "user_id": "@U_D1", "provider": "firebase_legacy", "provider_subject": "SEEDUID0000000000000000000D1", "email_at_link": "d1.somchai@logitrack.test", "linked_at": "2026-01-05T09:00:00+07:00"},
{"id": "@AI_D2_FB", "user_id": "@U_D2", "provider": "firebase_legacy", "provider_subject": "SEEDUID0000000000000000000D2", "email_at_link": "d2.wichai@logitrack.test", "linked_at": "2026-01-05T09:00:00+07:00"},
{"id": "@AI_D2_G", "user_id": "@U_D2", "provider": "google", "provider_subject": "100000000000000000002", "email_at_link": "d2.wichai@logitrack.test", "linked_at": "2026-01-05T09:00:00+07:00", "last_used_at": "2026-09-29T06:50:00+07:00"},
{"id": "@AI_D6_FB", "user_id": "@U_D6", "provider": "firebase_legacy", "provider_subject": "SEEDUID0000000000000000000D6", "email_at_link": "d6.amnat@logitrack.test", "linked_at": "2026-01-05T09:00:00+07:00"}
]
```

Legacy Firebase uids are `firebase_legacy` identities (Appendix A §A.3.1); `U_D2` also signs in with Google.

#### `memberships` (11)

```json
[
{"user_id": "@U_WRT_ADMIN", "tenant_id": "@TN_OWN", "role": "tenant_admin", "status": "active", "created_at": "2026-01-05T09:00:00+07:00"},
{"user_id": "@U_WRT_MGR", "tenant_id": "@TN_OWN", "role": "manager", "status": "active", "created_at": "2026-01-05T09:00:00+07:00"},
{"user_id": "@U_WRT_OPS", "tenant_id": "@TN_OWN", "role": "operation_staff", "status": "active", "created_at": "2026-01-05T09:00:00+07:00"},
{"user_id": "@U_NWR_ADMIN", "tenant_id": "@TN_NWR", "role": "tenant_admin", "status": "active", "created_at": "2026-01-05T09:00:00+07:00"},
{"user_id": "@U_TTP_DISP", "tenant_id": "@TN_TTP", "role": "operation_staff", "status": "active", "created_at": "2026-01-05T09:00:00+07:00"},
{"user_id": "@U_D1", "tenant_id": "@TN_OWN", "role": "driver", "status": "active", "created_at": "2026-01-05T09:00:00+07:00"},
{"user_id": "@U_D2", "tenant_id": "@TN_OWN", "role": "driver", "status": "active", "created_at": "2026-01-05T09:00:00+07:00"},
{"user_id": "@U_D3", "tenant_id": "@TN_OWN", "role": "driver", "status": "active", "created_at": "2026-01-05T09:00:00+07:00"},
{"user_id": "@U_D6", "tenant_id": "@TN_NWR", "role": "driver", "status": "suspended", "created_at": "2026-06-01T09:00:00+07:00", "updated_at": "2026-09-01T09:00:00+07:00"},
{"user_id": "@U_D6", "tenant_id": "@TN_TTP", "role": "driver", "status": "active", "created_at": "2026-09-01T09:00:00+07:00"},
{"user_id": "@U_D7", "tenant_id": "@TN_TTP", "role": "driver", "status": "active", "created_at": "2026-01-05T09:00:00+07:00"}
]
```

#### `user_platform_roles` (1)

```json
[
{"user_id": "@U_PLAT", "role": "platform_admin", "granted_at": "2026-01-05T09:00:00+07:00"}
]
```

#### `user_scopes` (2)

```json
[
{"id": "@US_CJSF", "user_id": "@U_CJSF_CUST", "kind": "customer", "billing_party_id": "@BP_CJSF", "created_by": "@U_WRT_ADMIN", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@US_TTP", "user_id": "@U_TTP_DISP", "kind": "dispatcher", "billing_party_id": "@BP_TTP", "created_by": "@U_PLAT", "created_at": "2026-01-05T09:00:00+07:00"}
]
```

#### `role_capability_overrides` (1)

```json
[
{"id": "@RCO1", "tenant_id": "@TN_NWR", "role": "operator", "capability": "fleet:view_live_map", "allowed": false, "updated_by": "@U_NWR_ADMIN", "updated_at": "2026-09-02T09:00:00+07:00"}
]
```

### D.4.7 Master data

#### `hubs` (9)

```json
[
{"id": "@H_ALANGA", "source_id": "ALANG-A", "name_th": "วังทองหลาง", "latitude": 13.7766, "longitude": 100.6087, "station_type": "HUB", "network": "SPX", "linked_party_id": "@BP_SPX", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@H_WTL12", "source_id": "WANGTHONGLANG12", "name_th": "วังทองหลาง12", "latitude": 13.7801, "longitude": 100.6152, "station_type": "HUB", "network": "SPX", "linked_party_id": "@BP_SPX", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@H_SPKGW", "source_id": "SPK-GW", "name_th": "ศูนย์เกตเวย์บางปู", "name_en": "Bangpu Gateway", "latitude": 13.5236, "longitude": 100.6583, "station_type": "HUB", "network": "SPK", "linked_party_id": "@BP_SPK", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@H_SPK103", "source_id": "SPK890103", "name_th": "ลาดกระบัง26", "latitude": 13.7225, "longitude": 100.7569, "station_type": "HUB", "network": "SPK", "linked_party_id": "@BP_SPK", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@H_SPK146", "source_id": "SPK890146", "name_th": "ประเวศ18", "name_en": "Prawet 18", "latitude": 13.6957, "longitude": 100.6918, "station_type": "HUB", "network": "SPK", "linked_party_id": "@BP_SPK", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@H_SPK174", "source_id": "SPK890174", "name_th": "ห้วยขวาง10", "latitude": 13.7691, "longitude": 100.5747, "station_type": "HUB", "network": "SPK", "linked_party_id": "@BP_SPK", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@H_TTP1", "source_id": "TTP-HUB1", "name_th": "ลานจอด TTP ร่มเกล้า", "latitude": 13.748, "longitude": 100.702, "station_type": "HUB", "network": "SPX", "linked_party_id": "@BP_TTP", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@H_SOCE", "source_id": "SOCE", "name_th": "บัวโรย", "latitude": 13.6157, "longitude": 100.7368, "station_type": "SOC", "network": "SPX", "linked_party_id": "@BP_SPX", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@H_BANGNA", "source_id": "BANGNA-DRV", "name_th": "บางนา (คนขับเพิ่ม)", "latitude": 13.6676, "longitude": 100.6342, "station_type": "HUB", "network": "SPX", "created_by_driver": true, "created_at": "2026-01-05T09:00:00+07:00"}
]
```

#### `hub_name_aliases` (7)

```json
[
{"alias": "วังทองหลาง", "hub_id": "@H_ALANGA", "source": "name_th", "created_at": "2026-01-05T09:00:00+07:00"},
{"alias": "ประเวศ18", "hub_id": "@H_SPK146", "source": "name_th", "created_at": "2026-01-05T09:00:00+07:00"},
{"alias": "Prawet 18", "hub_id": "@H_SPK146", "source": "name_en", "created_at": "2026-01-05T09:00:00+07:00"},
{"alias": "ห้วยขวาง10", "hub_id": "@H_SPK174", "source": "name_th", "created_at": "2026-01-05T09:00:00+07:00"},
{"alias": "ศูนย์เกตเวย์บางปู", "hub_id": "@H_SPKGW", "source": "name_th", "created_at": "2026-01-05T09:00:00+07:00"},
{"alias": "Bangpu Gateway", "hub_id": "@H_SPKGW", "source": "name_en", "created_at": "2026-01-05T09:00:00+07:00"},
{"alias": "ลาดกระบัง26", "hub_id": "@H_SPK103", "source": "name_th", "created_at": "2026-01-05T09:00:00+07:00"}
]
```

#### `hub_soc_distances` (2)

```json
[
{"id": "@HD_ALANGA_SOCE", "hub_id": "@H_ALANGA", "soc_key": "SOCE", "direction": "hub_to_soc", "network": "SPX", "distance_m": 29405, "distance_km": "29.40", "duration_s": 3024, "duration_min": "50.40", "hub_lat": 13.7766, "hub_lng": 100.6087, "soc_lat": 13.6157, "soc_lng": 100.7368, "created_by": "@U_WRT_OPS", "created_at": "2026-07-01T03:00:00+07:00"},
{"id": "@HD_SOCE_ALANGA", "hub_id": "@H_ALANGA", "soc_key": "SOCE", "direction": "soc_to_hub", "network": "SPX", "distance_m": 29405, "distance_km": "29.40", "duration_s": 3024, "duration_min": "50.40", "hub_lat": 13.7766, "hub_lng": 100.6087, "soc_lat": 13.6157, "soc_lng": 100.7368, "created_by": "@U_WRT_OPS", "created_at": "2026-07-01T03:00:00+07:00"}
]
```

#### `drivers` (7)

```json
[
{"id": "@D1", "legacy_doc_id": "seedDrvDoc00000000D1", "legacy_auth_uid": "SEEDUID0000000000000000000D1", "tenant_id": "@TN_OWN", "tenant_source": "self", "user_id": "@U_D1", "first_name": "Somchai", "last_name": "Jaidee", "full_name_th": "สมชาย ใจดี", "mobile": "000-000-0101", "birth_date": "1984-05-14", "id_card": "1999900000013", "license_type": "ท.2", "employment_type": "full_time", "hire_date": "2024-03-01", "probation_passed": true, "current_assignment_id": "@TA1", "active_truck_id": "@T1", "active_truck_plate": "1ขค-1234", "active_task_id": "@TK17", "active_started_at": "2026-09-30T06:15:00+07:00", "status": "active", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@D2", "legacy_doc_id": "seedDrvDoc00000000D2", "legacy_auth_uid": "SEEDUID0000000000000000000D2", "tenant_id": "@TN_OWN", "tenant_source": "self", "user_id": "@U_D2", "first_name": "Wichai", "last_name": "Thongdee", "full_name_th": "วิชัย ทองดี", "mobile": "000-000-0102", "birth_date": "1990-11-02", "id_card": "1999900000021", "license_type": "ท.2", "employment_type": "full_time", "hire_date": "2025-06-01", "probation_passed": true, "current_assignment_id": "@TA2", "active_truck_id": "@T2", "active_truck_plate": "2ขค-5678", "active_task_id": "@TK18", "active_started_at": "2026-09-29T07:05:00+07:00", "status": "active", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@D3", "tenant_id": "@TN_OWN", "tenant_source": "self", "user_id": "@U_D3", "first_name": "Anan", "last_name": "Kumar", "full_name_th": null, "_note": "no Thai name (the ETL loads a whitespace-only legacy value as NULL): display falls back to first + last name", "mobile": "000-000-0103", "birth_date": "1995-02-11", "id_card": "1999900000030", "license_type": "บ.2", "employment_type": "part_time", "employment_start": "2026-05-01", "employment_end": "2026-10-31", "probation_passed": false, "status": "inactive", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@D4", "tenant_id": "@TN_OWN", "tenant_source": "self", "first_name": "Preecha", "last_name": "Srisuk", "full_name_th": "ปรีชา ศรีสุข", "mobile": "000-000-0104", "birth_date": "1970-01-20", "id_card": "1999900000048", "license_type": "ท.2", "employment_type": "full_time", "hire_date": "2026-08-01", "probation_passed": false, "status": "active", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@D5", "tenant_id": "@TN_NWR", "tenant_source": "self", "first_name": "Surachai", "last_name": "Boonma", "full_name_th": "สุรชัย บุญมา", "mobile": "000-000-0105", "birth_date": "1988-07-07", "id_card": "1999900000056", "license_type": "ท.2", "employment_type": "subcontractor", "status": "active", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@D6", "legacy_doc_id": "seedDrvDoc00000000D6", "legacy_auth_uid": "SEEDUID0000000000000000000D6", "tenant_id": "@TN_TTP", "tenant_source": "self", "user_id": "@U_D6", "first_name": "Amnat", "last_name": "Duangdee", "full_name_th": "อำนาจ ดวงดี", "mobile": "000-000-0106", "birth_date": "1986-03-30", "id_card": "1999900000064", "license_type": "ท.2", "employment_type": "subcontractor", "current_assignment_id": "@TA4", "updated_at": "2026-09-01T09:00:00+07:00", "status": "active", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@D7", "tenant_id": "@TN_TTP", "tenant_source": "self", "user_id": "@U_D7", "first_name": "Kitti", "last_name": "Phongphan", "full_name_th": "กิตติ พงษ์พันธ์", "mobile": "000-000-0107", "birth_date": "1992-12-01", "id_card": "1999900000072", "license_type": "ท.2", "employment_type": "subcontractor", "active_truck_id": "@T6", "active_truck_plate": "5กข-7890", "active_task_id": "@TK24", "active_started_at": "2026-09-30T08:42:00+07:00", "status": "active", "created_at": "2026-01-05T09:00:00+07:00"}
]
```

#### `trucks` (6)

```json
[
{"id": "@T1", "tenant_id": "@TN_OWN", "tenant_source": "self", "ownership_type": "own", "license_plate": "1ขค-1234", "province": "กรุงเทพมหานคร", "gps_vehicle_id": "CT-SEED-0001", "pm_interval_km": 10000, "current_mileage": 130120, "next_service_mileage": 130000, "last_alert_mileage": 130120, "active_maintenance_id": "@MT03", "status": "active", "brand": "Isuzu", "model": "D-Max Spark", "year": 2022, "color": "ขาว", "type_raw": "4 Wheels Jumbo", "vehicle_class": "4WJ", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@T2", "tenant_id": "@TN_OWN", "tenant_source": "self", "ownership_type": "own", "license_plate": "2ขค-5678", "province": "สมุทรปราการ", "gps_vehicle_id": "CT-SEED-0002", "pm_interval_km": 10000, "current_mileage": 88210, "next_service_mileage": 90000, "status": "active", "brand": "Isuzu", "model": "D-Max Spark", "year": 2023, "color": "ขาว", "type_raw": "4 Wheels", "vehicle_class": "4WJ", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@T3", "tenant_id": "@TN_OWN", "tenant_source": "self", "ownership_type": "own", "license_plate": "3ฒท-9012", "province": "กรุงเทพมหานคร", "gps_vehicle_id": "CT-SEED-0003", "pm_interval_km": 15000, "current_mileage": 210400, "next_service_mileage": 215000, "status": "active", "brand": "Hino", "model": "300 XZU", "year": 2021, "color": "ขาว", "type_raw": "6 Wheels", "vehicle_class": "6WH", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@T4", "tenant_id": "@TN_OWN", "tenant_source": "self", "ownership_type": "own", "license_plate": "ฆฆ-999", "province": "กรุงเทพมหานคร", "status": "maintenance", "brand": "Toyota", "model": "Hilux Revo", "year": 2019, "color": "ขาว", "type_raw": "Pickup", "vehicle_class": "4W", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@T5", "tenant_id": "@TN_NWR", "tenant_source": "self", "ownership_type": "subcontractor", "license_plate": "4กข-3456", "province": "นนทบุรี", "status": "active", "brand": "Isuzu", "model": "D-Max Spark", "year": 2020, "color": "ขาว", "type_raw": "4 Wheels Jumbo", "vehicle_class": "4WJ", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@T6", "tenant_id": "@TN_TTP", "tenant_source": "self", "ownership_type": "subcontractor", "license_plate": "5กข-7890", "province": "กรุงเทพมหานคร", "status": "active", "brand": "Hino", "model": "300 XZU", "year": 2022, "color": "ขาว", "type_raw": "6 Wheels", "vehicle_class": "6WH", "created_at": "2026-01-05T09:00:00+07:00"}
]
```

#### `truck_assignments` (4)

```json
[
{"id": "@TA1", "tenant_id": "@TN_OWN", "tenant_source": "self", "truck_id": "@T1", "driver_id": "@D1", "status": "active", "admin_user_id": "@U_WRT_OPS", "admin_label": "WRT Operation Staff", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@TA2", "tenant_id": "@TN_OWN", "tenant_source": "self", "truck_id": "@T2", "driver_id": "@D2", "status": "active", "admin_user_id": "@U_WRT_OPS", "admin_label": "WRT Operation Staff", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@TA3", "tenant_id": "@TN_NWR", "tenant_source": "self", "truck_id": "@T5", "driver_id": "@D6", "status": "revoked", "admin_user_id": "@U_NWR_ADMIN", "admin_label": "NWR หัวหน้ากลุ่ม", "created_at": "2026-06-01T09:00:00+07:00", "revoked_at": "2026-09-01T09:00:00+07:00"},
{"id": "@TA4", "tenant_id": "@TN_TTP", "tenant_source": "self", "truck_id": "@T6", "driver_id": "@D6", "status": "active", "admin_label": "System", "created_at": "2026-09-01T09:00:00+07:00"}
]
```

### D.4.8 Operations

#### `tasks` (25)

```json
[
{"id": "@TK01", "tenant_id": "@TN_OWN", "tenant_source": "form", "task_no": "LH-10072026-001", "task_type": "line_haul", "job_category": "PRIMARY", "status": "completed", "plan_at": "2026-07-10T06:00:00+07:00", "_plan_date": "2026-07-10", "plan_time": "06:00", "source_hub_raw": "SPK-GW", "source_hub_id": "@H_SPKGW", "source_linked_party_id": "@BP_SPK", "billing_party_id": "@BP_CJSF", "destination_raw": "SPK890103 - ลาดกระบัง26", "destination_hub_id": "@H_SPK103", "destination_linked_party_id": "@BP_SPK", "truck_id": "@T1", "truck_type": "4WJ", "license_plate_snapshot": "1ขค-1234", "driver_id": "@D1", "run_order": 1, "check_in_at": "2026-07-10T06:20:00+07:00", "created_by": "@U_WRT_OPS", "created_at": "2026-07-09T16:00:00+07:00"},
{"id": "@TK02", "legacy_doc_id": "seedTaskDoc000000T02", "tenant_id": "@TN_NWR", "tenant_source": "driver", "task_no": "FM-22072026-001", "task_type": "first_mile", "job_category": "PRIMARY", "status": "completed", "plan_at": "2026-07-22T07:00:00+07:00", "_plan_date": "2026-07-22", "plan_time": "07:00", "source_hub_raw": "ALANG-A", "source_hub_id": "@H_ALANGA", "source_linked_party_id": "@BP_SPX", "destination_raw": "SOCE (บัวโรย)", "destination_hub_id": "@H_SOCE", "destination_soc_key": "SOCE", "destination_linked_party_id": "@BP_SPX", "billing_party_id": "@BP_SPX", "truck_id": "@T5", "truck_type": "4WJ", "license_plate_snapshot": "4กข-3456", "driver_id": "@D6", "legacy_driver_ref": "seedDrvDoc00000000D6", "driver_ref_match": "doc_id", "check_in_at": "2026-07-22T07:10:00+07:00", "created_at": "2026-07-21T15:00:00+07:00"},
{"id": "@TK03", "legacy_doc_id": "seedTaskDoc000000T03", "tenant_id": "@TN_OWN", "tenant_source": "driver", "task_no": "FM-23072026-7", "task_type": "first_mile", "status": "completed", "plan_at": "2026-07-23T07:30:00+07:00", "_plan_date": "2026-07-23", "plan_time": "07:30", "source_hub_raw": "ALANG-A", "source_hub_id": "@H_ALANGA", "source_linked_party_id": "@BP_SPX", "destination_raw": "WANGTHONGLANG12", "destination_hub_id": "@H_WTL12", "destination_linked_party_id": "@BP_SPX", "truck_id": "@T2", "truck_type": "4WJ", "license_plate_snapshot": "2ขค-5678", "driver_id": "@D2", "legacy_driver_ref": "SEEDUID0000000000000000000D2", "driver_ref_match": "auth_uid", "check_in_at": "2026-07-23T07:40:00+07:00", "created_at": "2026-07-22T18:00:00+07:00"},
{"id": "@TK04", "tenant_id": "@TN_OWN", "tenant_source": "form", "task_no": "LH-16082026-001", "task_type": "line_haul", "job_category": "PRIMARY", "status": "completed", "plan_at": "2026-08-16T00:21:00+07:00", "_plan_date": "2026-08-16", "plan_time": "00:21", "source_hub_raw": "SPK-GW", "source_hub_id": "@H_SPKGW", "source_linked_party_id": "@BP_SPK", "billing_party_id": "@BP_CJSF", "destination_raw": "SPK890103 - ลาดกระบัง26", "destination_hub_id": "@H_SPK103", "destination_linked_party_id": "@BP_SPK", "truck_id": "@T1", "truck_type": "4WJ", "license_plate_snapshot": "1ขค-1234", "driver_id": "@D1", "run_order": 1, "check_in_at": "2026-08-16T00:40:00+07:00", "created_by": "@U_WRT_OPS", "created_at": "2026-08-15T17:00:00+07:00"},
{"id": "@TK05", "tenant_id": "@TN_OWN", "tenant_source": "form", "task_no": "LH-20082026-001", "task_type": "line_haul", "job_category": "PRIMARY", "status": "completed", "plan_at": "2026-08-20T07:00:00+07:00", "_plan_date": "2026-08-20", "plan_time": "07:00", "source_hub_raw": "SPK-GW", "source_hub_id": "@H_SPKGW", "source_linked_party_id": "@BP_SPK", "billing_party_id": "@BP_CJSF", "destination_raw": "SPK890103", "destination_hub_id": "@H_SPK103", "destination_linked_party_id": "@BP_SPK", "truck_id": "@T1", "truck_type": "4WJ", "license_plate_snapshot": "1ขค-1234", "driver_id": "@D1", "run_order": 1, "check_in_at": "2026-08-20T07:30:00+07:00", "is_multi_delivery": true, "created_by": "@U_WRT_OPS", "created_at": "2026-08-19T16:00:00+07:00"},
{"id": "@TK06", "tenant_id": "@TN_OWN", "tenant_source": "form", "task_no": "LH-31082026-001", "task_type": "line_haul", "job_category": "PRIMARY", "status": "completed", "plan_at": "2026-08-31T22:00:00+07:00", "_plan_date": "2026-08-31", "plan_time": "22:00", "source_hub_raw": "SPK-GW", "source_hub_id": "@H_SPKGW", "source_linked_party_id": "@BP_SPK", "billing_party_id": "@BP_CJSF", "destination_raw": "SPK890103 - ลาดกระบัง26", "destination_hub_id": "@H_SPK103", "destination_linked_party_id": "@BP_SPK", "truck_id": "@T1", "truck_type": "4WJ", "license_plate_snapshot": "1ขค-1234", "driver_id": "@D1", "run_order": 1, "check_in_at": "2026-08-31T21:00:00+07:00", "created_by": "@U_WRT_OPS", "created_at": "2026-08-31T10:00:00+07:00"},
{"id": "@TK07", "legacy_doc_id": "seedTaskDoc000000T07", "tenant_id": "@TN_OWN", "tenant_source": "driver", "task_no": "LH-07082026-2", "task_type": "line_haul", "job_category": "PRIMARY", "status": "completed", "plan_at": "2026-08-07T08:00:00+07:00", "_plan_date": "2026-08-07", "plan_time": "08:00", "source_hub_raw": "SPK-GW", "source_hub_id": "@H_SPKGW", "source_linked_party_id": "@BP_SPK", "billing_party_id": "@BP_CJSF", "destination_raw": "SPK890103 - ลาดกระบัง26", "destination_hub_id": "@H_SPK103", "destination_linked_party_id": "@BP_SPK", "truck_id": "@T1", "truck_type": "4WJ", "license_plate_snapshot": "1ขค-1234", "driver_id": "@D1", "legacy_driver_ref": "seedDrvDoc00000000D1", "driver_ref_match": "doc_id", "check_in_at": "2026-08-07T08:20:00+07:00", "created_at": "2026-08-06T17:00:00+07:00"},
{"id": "@TK08", "tenant_id": "@TN_OWN", "tenant_source": "form", "task_no": "FM-12082026-001", "task_type": "first_mile", "job_category": "PRIMARY", "status": "completed", "plan_at": "2026-08-12T08:00:00+07:00", "_plan_date": "2026-08-12", "plan_time": "08:00", "source_hub_raw": "TTP-HUB1", "source_hub_id": "@H_TTP1", "source_linked_party_id": "@BP_TTP", "destination_raw": "SOCE (บัวโรย)", "destination_hub_id": "@H_SOCE", "destination_soc_key": "SOCE", "destination_linked_party_id": "@BP_SPX", "billing_party_id": "@BP_TTP", "truck_id": "@T3", "truck_type": "6WH", "license_plate_snapshot": "3ฒท-9012", "driver_id": "@D1", "run_order": 1, "check_in_at": "2026-08-12T08:15:00+07:00", "created_by": "@U_WRT_OPS", "created_at": "2026-08-11T15:00:00+07:00"},
{"id": "@TK09", "tenant_id": "@TN_OWN", "tenant_source": "form", "task_no": "LH-03092026-001", "task_type": "line_haul", "job_category": "PRIMARY", "status": "completed", "plan_at": "2026-09-03T08:00:00+07:00", "_plan_date": "2026-09-03", "plan_time": "08:00", "source_hub_raw": "SPK890146", "source_hub_id": "@H_SPK146", "source_linked_party_id": "@BP_SPK", "destination_raw": "SPK-GW", "destination_hub_id": "@H_SPKGW", "destination_linked_party_id": "@BP_SPK", "billing_party_id": "@BP_NWR", "truck_id": "@T2", "truck_type": "4WJ", "license_plate_snapshot": "2ขค-5678", "driver_id": "@D2", "run_order": 1, "check_in_at": "2026-09-03T08:10:00+07:00", "created_by": "@U_WRT_OPS", "created_at": "2026-09-02T15:00:00+07:00"},
{"id": "@TK10", "tenant_id": "@TN_OWN", "tenant_source": "form", "task_no": "LH-04092026-001", "task_type": "line_haul", "job_category": "PRIMARY", "status": "completed", "plan_at": "2026-09-04T06:00:00+07:00", "_plan_date": "2026-09-04", "plan_time": "06:00", "source_hub_raw": "SPK-GW", "source_hub_id": "@H_SPKGW", "source_linked_party_id": "@BP_SPK", "billing_party_id": "@BP_CJSF", "destination_raw": "SPK890103", "destination_hub_id": "@H_SPK103", "destination_linked_party_id": "@BP_SPK", "license_plate_snapshot": "กข-1111", "driver_id": "@D1", "run_order": 1, "check_in_at": "2026-09-04T06:15:00+07:00", "created_by": "@U_WRT_OPS", "created_at": "2026-09-03T15:00:00+07:00"},
{"id": "@TK11", "tenant_id": "@TN_OWN", "tenant_source": "form", "task_no": "LH-05092026-001", "task_type": "line_haul", "job_category": "SUPPLEMENTARY", "status": "completed", "plan_at": "2026-09-05T07:00:00+07:00", "_plan_date": "2026-09-05", "plan_time": "07:00", "source_hub_raw": "SPK-GW", "source_hub_id": "@H_SPKGW", "source_linked_party_id": "@BP_SPK", "billing_party_id": "@BP_CJSF", "destination_raw": "SPK890103 - ลาดกระบัง26", "destination_hub_id": "@H_SPK103", "destination_linked_party_id": "@BP_SPK", "truck_id": "@T2", "truck_type": "4WJ", "license_plate_snapshot": "2ขค-5678", "driver_id": "@D2", "run_order": 1, "check_in_at": "2026-09-05T07:15:00+07:00", "created_by": "@U_WRT_OPS", "created_at": "2026-09-04T15:00:00+07:00"},
{"id": "@TK12", "tenant_id": "@TN_OWN", "tenant_source": "form", "task_no": "LH-07092026-001", "task_type": "line_haul", "job_category": "SUPPLEMENTARY", "status": "completed", "plan_at": "2026-09-07T07:00:00+07:00", "_plan_date": "2026-09-07", "plan_time": "07:00", "source_hub_raw": "SPK-GW", "source_hub_id": "@H_SPKGW", "source_linked_party_id": "@BP_SPK", "billing_party_id": "@BP_CJSF", "destination_raw": "SPK890103 - ลาดกระบัง26", "destination_hub_id": "@H_SPK103", "destination_linked_party_id": "@BP_SPK", "truck_id": "@T1", "truck_type": "4WJ", "license_plate_snapshot": "1ขค-1234", "driver_id": "@D1", "run_order": 1, "check_in_at": "2026-09-07T07:10:00+07:00", "created_by": "@U_WRT_OPS", "created_at": "2026-09-06T15:00:00+07:00"},
{"id": "@TK13", "tenant_id": "@TN_OWN", "tenant_source": "form", "task_no": "LH-10092026-001", "task_type": "line_haul", "job_category": "PRIMARY", "status": "completed", "plan_at": "2026-09-10T07:00:00+07:00", "_plan_date": "2026-09-10", "plan_time": "07:00", "source_hub_raw": "SPK-GW", "source_hub_id": "@H_SPKGW", "source_linked_party_id": "@BP_SPK", "billing_party_id": "@BP_CJSF", "destination_raw": "SPK890103 - ลาดกระบัง26", "destination_hub_id": "@H_SPK103", "destination_linked_party_id": "@BP_SPK", "truck_id": "@T2", "truck_type": "4WJ", "license_plate_snapshot": "2ขค-5678", "driver_id": "@D2", "run_order": 1, "check_in_at": "2026-09-10T07:20:00+07:00", "created_by": "@U_WRT_OPS", "created_at": "2026-09-09T15:00:00+07:00"},
{"id": "@TK14", "tenant_id": "@TN_OWN", "tenant_source": "form", "task_no": "LH-20092026-001", "task_type": "line_haul", "job_category": "PRIMARY", "status": "completed", "plan_at": "2026-09-20T07:00:00+07:00", "_plan_date": "2026-09-20", "plan_time": "07:00", "source_hub_raw": "SPK-GW", "source_hub_id": "@H_SPKGW", "source_linked_party_id": "@BP_SPK", "billing_party_id": "@BP_CJSF", "destination_raw": "SPK890103 - ลาดกระบัง26", "destination_hub_id": "@H_SPK103", "destination_linked_party_id": "@BP_SPK", "truck_id": "@T1", "truck_type": "4WJ", "license_plate_snapshot": "1ขค-1234", "driver_id": "@D1", "run_order": 1, "check_in_at": "2026-09-20T07:20:00+07:00", "created_by": "@U_WRT_OPS", "created_at": "2026-09-19T15:00:00+07:00"},
{"id": "@TK15", "legacy_doc_id": "seedTaskDoc000000T15", "tenant_id": "@TN_OWN", "tenant_source": "driver", "task_no": "FM-08092026-1", "task_type": "first_mile", "job_category": "PRIMARY", "status": "completed", "plan_at": "2026-09-08T07:20:00+07:00", "_plan_date": "2026-09-08", "plan_time": "07:20", "source_hub_raw": "BANGNA-DRV", "source_hub_id": "@H_BANGNA", "destination_raw": "SOCE", "destination_hub_id": "@H_SOCE", "destination_soc_key": "SOCE", "truck_id": "@T2", "truck_type": "4WJ", "license_plate_snapshot": "2ขค-5678", "driver_id": "@D2", "legacy_driver_ref": "SEEDUID0000000000000000000D2", "driver_ref_match": "auth_uid", "check_in_at": "2026-09-08T07:20:00+07:00", "created_at": "2026-09-08T07:20:00+07:00"},
{"id": "@TK16", "tenant_id": "@TN_OWN", "tenant_source": "form", "task_no": "LH-26082026-001", "task_type": "line_haul", "job_category": "PRIMARY", "status": "completed", "plan_at": "2026-08-26T07:00:00+07:00", "_plan_date": "2026-08-26", "plan_time": "07:00", "source_hub_raw": "SPK-GW", "source_hub_id": "@H_SPKGW", "source_linked_party_id": "@BP_SPK", "destination_raw": "SPK890146", "destination_hub_id": "@H_SPK146", "destination_linked_party_id": "@BP_SPK", "billing_party_id": "@BP_SPK", "truck_id": "@T2", "truck_type": "4WJ", "license_plate_snapshot": "2ขค-5678", "driver_id": "@D2", "helper_driver_id": "@D1", "run_order": 1, "check_in_at": "2026-08-26T07:10:00+07:00", "is_multi_delivery": true, "created_by": "@U_WRT_OPS", "created_at": "2026-08-25T15:00:00+07:00"},
{"id": "@TK17", "tenant_id": "@TN_OWN", "tenant_source": "form", "task_no": "LH-30092026-001", "task_type": "line_haul", "job_category": "PRIMARY", "status": "in_transit", "plan_at": "2026-09-30T06:00:00+07:00", "_plan_date": "2026-09-30", "plan_time": "06:00", "source_hub_raw": "SPK-GW", "source_hub_id": "@H_SPKGW", "source_linked_party_id": "@BP_SPK", "billing_party_id": "@BP_CJSF", "destination_raw": "SPK890103 - ลาดกระบัง26", "destination_hub_id": "@H_SPK103", "destination_linked_party_id": "@BP_SPK", "truck_id": "@T1", "truck_type": "4WJ", "license_plate_snapshot": "1ขค-1234", "driver_id": "@D1", "run_order": 1, "check_in_at": "2026-09-30T06:15:00+07:00", "created_by": "@U_WRT_OPS", "created_at": "2026-09-29T15:00:00+07:00"},
{"id": "@TK18", "tenant_id": "@TN_OWN", "tenant_source": "form", "task_no": "FM-29092026-001", "task_type": "first_mile", "job_category": "PRIMARY", "status": "in_transit", "plan_at": "2026-09-29T07:00:00+07:00", "_plan_date": "2026-09-29", "plan_time": "07:00", "source_hub_raw": "ALANG-A", "source_hub_id": "@H_ALANGA", "source_linked_party_id": "@BP_SPX", "destination_raw": "SOCE", "destination_hub_id": "@H_SOCE", "destination_soc_key": "SOCE", "destination_linked_party_id": "@BP_SPX", "billing_party_id": "@BP_SPX", "truck_id": "@T2", "truck_type": "4WJ", "license_plate_snapshot": "2ขค-5678", "driver_id": "@D2", "run_order": 1, "check_in_at": "2026-09-29T07:05:00+07:00", "created_by": "@U_WRT_OPS", "created_at": "2026-09-28T15:00:00+07:00"},
{"id": "@TK19", "legacy_doc_id": "seedTaskDoc000000T19", "tenant_id": "@TN_OWN", "tenant_source": "driver", "task_no": "FM-05072026-3", "task_type": "first_mile", "status": "completed", "plan_at": "2026-07-05T07:00:00+07:00", "_plan_date": "2026-07-05", "plan_time": "07:00", "source_hub_raw": "ALANG-A", "source_hub_id": "@H_ALANGA", "source_linked_party_id": "@BP_SPX", "destination_raw": "SOCE (บัวโรย)", "destination_hub_id": "@H_SOCE", "destination_soc_key": "SOCE", "destination_linked_party_id": "@BP_SPX", "truck_id": "@T2", "truck_type": "4WJ", "license_plate_snapshot": "2ขค-5678", "driver_id": "@D2", "legacy_driver_ref": "SEEDUID0000000000000000000D2", "driver_ref_match": "auth_uid", "check_in_at": "2026-07-05T07:05:00+07:00", "created_at": "2026-07-04T17:00:00+07:00"},
{"id": "@TK20", "tenant_id": "@TN_TTP", "tenant_source": "form", "task_no": "FM-12092026-001", "task_type": "first_mile", "job_category": "PRIMARY", "status": "cancelled", "plan_at": "2026-09-12T07:00:00+07:00", "_plan_date": "2026-09-12", "plan_time": "07:00", "source_hub_raw": "TTP-HUB1", "source_hub_id": "@H_TTP1", "source_linked_party_id": "@BP_TTP", "destination_raw": "SOCE (บัวโรย)", "destination_hub_id": "@H_SOCE", "destination_soc_key": "SOCE", "destination_linked_party_id": "@BP_SPX", "truck_id": "@T6", "truck_type": "6WH", "license_plate_snapshot": "5กข-7890", "driver_id": "@D7", "run_order": 1, "check_in_at": "2026-09-12T07:05:00+07:00", "created_by": "@U_TTP_DISP", "created_at": "2026-09-11T14:00:00+07:00"},
{"id": "@TK21", "tenant_id": "@TN_OWN", "tenant_source": "form", "task_no": "LH-27082026-001", "task_type": "line_haul", "job_category": "PRIMARY", "status": "completed", "plan_at": "2026-08-27T06:00:00+07:00", "_plan_date": "2026-08-27", "plan_time": "06:00", "source_hub_raw": "SPK-GW", "source_hub_id": "@H_SPKGW", "source_linked_party_id": "@BP_SPK", "billing_party_id": "@BP_CJSF", "destination_raw": "SPK890174 - ห้วยขวาง10", "destination_hub_id": "@H_SPK174", "destination_linked_party_id": "@BP_SPK", "truck_id": "@T3", "truck_type": "6WH", "license_plate_snapshot": "3ฒท-9012", "driver_id": "@D1", "run_order": 1, "check_in_at": "2026-08-27T06:30:00+07:00", "created_by": "@U_WRT_OPS", "created_at": "2026-08-26T15:00:00+07:00"},
{"id": "@TK22", "tenant_id": "@TN_TTP", "tenant_source": "form", "task_no": "FM-30092026-001", "task_type": "first_mile", "job_category": "PRIMARY", "status": "pending", "plan_at": "2026-09-30T13:00:00+07:00", "_plan_date": "2026-09-30", "plan_time": "13:00", "source_hub_raw": "TTP-HUB1", "source_hub_id": "@H_TTP1", "source_linked_party_id": "@BP_TTP", "destination_raw": "SOCE (บัวโรย)", "destination_hub_id": "@H_SOCE", "destination_soc_key": "SOCE", "destination_linked_party_id": "@BP_SPX", "billing_party_id": "@BP_TTP", "truck_type": "6WH", "created_by": "@U_TTP_DISP", "created_at": "2026-09-30T09:30:00+07:00"},
{"id": "@TK23", "tenant_id": "@TN_OWN", "tenant_source": "form", "task_no": "FM-01102026-001", "task_type": "first_mile", "job_category": "PRIMARY", "status": "assigned", "plan_at": "2026-10-01T07:00:00+07:00", "_plan_date": "2026-10-01", "plan_time": "07:00", "source_hub_raw": "ALANG-A", "source_hub_id": "@H_ALANGA", "source_linked_party_id": "@BP_SPX", "destination_raw": "SOCE (บัวโรย)", "destination_hub_id": "@H_SOCE", "destination_soc_key": "SOCE", "destination_linked_party_id": "@BP_SPX", "billing_party_id": "@BP_SPX", "truck_id": "@T2", "truck_type": "4WJ", "license_plate_snapshot": "2ขค-5678", "driver_id": "@D2", "run_order": 2, "created_by": "@U_WRT_OPS", "created_at": "2026-09-30T16:00:00+07:00"},
{"id": "@TK24", "tenant_id": "@TN_TTP", "tenant_source": "form", "task_no": "FM-30092026-002", "task_type": "first_mile", "job_category": "PRIMARY", "status": "checked_in", "plan_at": "2026-09-30T09:00:00+07:00", "_plan_date": "2026-09-30", "plan_time": "09:00", "source_hub_raw": "TTP-HUB1", "source_hub_id": "@H_TTP1", "source_linked_party_id": "@BP_TTP", "destination_raw": "SOCE (บัวโรย)", "destination_hub_id": "@H_SOCE", "destination_soc_key": "SOCE", "destination_linked_party_id": "@BP_SPX", "billing_party_id": "@BP_TTP", "truck_id": "@T6", "truck_type": "6WH", "license_plate_snapshot": "5กข-7890", "driver_id": "@D7", "run_order": 1, "check_in_at": "2026-09-30T08:42:00+07:00", "check_in_photo_file_id": "@FO_TK24_CI", "check_in_app_screenshot_file_id": "@FO_TK24_APP", "check_in_lat": 13.748, "check_in_lng": 100.702, "created_by": "@U_TTP_DISP", "created_at": "2026-09-29T18:00:00+07:00"},
{"id": "@TK25", "legacy_doc_id": "seedTaskDoc000000T25", "tenant_id": "@TN_QUAR", "tenant_source": "quarantine", "task_no": "FM-28062026-12", "task_type": "first_mile", "status": "pending", "plan_at": "2026-06-28T08:00:00+07:00", "_plan_date": "2026-06-28", "plan_time": "08:00", "source_hub_raw": "ALANG-A", "source_hub_id": "@H_ALANGA", "source_linked_party_id": "@BP_SPX", "destination_raw": "SOCE (บัวโรย)", "destination_hub_id": "@H_SOCE", "destination_soc_key": "SOCE", "destination_linked_party_id": "@BP_SPX", "driver_ref_match": "none", "created_at": "2026-06-27T17:00:00+07:00"}
]
```

#### `task_delivery_stops` (5)

```json
[
{"id": "@TDS05_1", "task_id": "@TK05", "stop_index": 1, "destination_raw": "SPK890103", "destination_hub_id": "@H_SPK103", "destination_linked_party_id": "@BP_SPK", "status": "delivered", "delivered_at": "2026-08-20T11:10:00+07:00"},
{"id": "@TDS05_2", "task_id": "@TK05", "stop_index": 2, "destination_raw": "SPK890146", "destination_hub_id": "@H_SPK146", "destination_linked_party_id": "@BP_SPK", "status": "delivered", "delivered_at": "2026-08-20T16:30:00+07:00"},
{"id": "@TDS16_1", "task_id": "@TK16", "stop_index": 1, "destination_raw": "SPK890146", "destination_hub_id": "@H_SPK146", "destination_linked_party_id": "@BP_SPK", "status": "delivered", "delivered_at": "2026-08-26T15:20:00+07:00"},
{"id": "@TDS16_2", "task_id": "@TK16", "stop_index": 2, "destination_raw": "SPK890103", "destination_hub_id": "@H_SPK103", "destination_linked_party_id": "@BP_SPK", "status": "delivered", "delivered_at": "2026-08-26T13:05:00+07:00"},
{"id": "@TDS16_3", "task_id": "@TK16", "stop_index": 3, "destination_raw": "SPK890174", "destination_hub_id": "@H_SPK174", "destination_linked_party_id": "@BP_SPK", "status": "delivered", "delivered_at": "2026-08-26T17:55:00+07:00"}
]
```

#### `trip_records` (21)

```json
[
{"id": "@TR01", "tenant_id": "@TN_OWN", "tenant_source": "task", "trip_no": "ZXJB26071000101", "status": "delivered", "job_type": "line_haul", "job_category": "PRIMARY", "task_id": "@TK01", "driver_id": "@D1", "origin_raw": "SPK-GW", "origin_hub_id": "@H_SPKGW", "destination_raw": "SPK890103", "destination_hub_id": "@H_SPK103", "truck_id": "@T1", "truck_license_plate_snapshot": "1ขค-1234", "truck_type_raw": "4WJ", "vehicle_class": "4WJ", "std": "2026-07-10T09:55:00+07:00", "seal_code": "SPXS0000000101", "delivered_at": "2026-07-10T15:40:00+07:00", "delivered_via": "mobile", "billing_party_id": "@BP_CJSF", "billing_date": "2026-07-10T06:00:00+07:00", "line_delivered_notified_at": "2026-07-10T15:41:00+07:00", "evidence_token": "seedEvidenceTokenTR01", "_billing_axis_date": "2026-07-10", "_billing": "V01 priced 1130.00", "created_at": "2026-07-10T09:55:00+07:00"},
{"id": "@TR02", "legacy_doc_id": "ZXZB26072200102", "tenant_id": "@TN_NWR", "tenant_source": "task", "trip_no": "ZXZB26072200102", "status": "delivered", "job_type": "first_mile", "job_category": "PRIMARY", "task_id": "@TK02", "driver_id": "@D6", "legacy_driver_ref": "SEEDUID0000000000000000000D6", "driver_ref_match": "auth_uid", "origin_raw": "ALANG-A", "origin_hub_id": "@H_ALANGA", "destination_raw": "SOCE", "destination_hub_id": "@H_SOCE", "destination_soc_key": "SOCE", "truck_id": "@T5", "truck_license_plate_snapshot": "4กข-3456", "truck_type_raw": "4WJ", "vehicle_class": "4WJ", "std": "2026-07-22T07:40:00+07:00", "seal_code": "SPXS0000000102", "delivered_at": "2026-07-22T10:15:00+07:00", "delivered_via": "mobile", "billing_party_id": "@BP_SPX", "_billing_axis_date": "2026-07-22", "_billing": "V02 priced 1150.00 (legacy, carrier-run, billed by own fleet)", "created_at": "2026-07-22T07:40:00+07:00"},
{"id": "@TR03", "legacy_doc_id": "ZXZB26072300103", "tenant_id": "@TN_OWN", "tenant_source": "task", "trip_no": "ZXZB26072300103", "status": "delivered", "job_type": "first_mile", "job_category": "SUPPLEMENTARY", "task_id": "@TK03", "legacy_task_ref": "seedTaskDoc000000T03", "task_ref_match": "doc_id", "driver_id": "@D2", "legacy_driver_ref": "SEEDUID0000000000000000000D2", "driver_ref_match": "auth_uid", "origin_raw": "ALANG-A", "origin_hub_id": "@H_ALANGA", "destination_raw": "WANGTHONGLANG12", "destination_hub_id": "@H_WTL12", "truck_id": "@T2", "truck_license_plate_snapshot": "2ขค-5678", "truck_type_raw": "4WJ", "vehicle_class": "4WJ", "std": "2026-07-23T08:00:00+07:00", "seal_code": "SPXS0000000103", "delivered_at": "2026-07-23T14:05:00+07:00", "delivered_via": "mobile", "billing_party_id": "@BP_SPX", "_billing_axis_date": "2026-07-23", "_billing": "V03 priced 1250.00 (legacy NULL category -> SUPPLEMENTARY fallback; renamed)", "created_at": "2026-07-23T08:00:00+07:00"},
{"id": "@TR04", "tenant_id": "@TN_OWN", "tenant_source": "task", "trip_no": "ZXJB26081600104", "status": "delivered", "job_type": "line_haul", "job_category": "PRIMARY", "task_id": "@TK04", "driver_id": "@D1", "origin_raw": "SPK-GW", "origin_hub_id": "@H_SPKGW", "destination_raw": "SPK890103", "destination_hub_id": "@H_SPK103", "truck_id": "@T1", "truck_license_plate_snapshot": "1ขค-1234", "truck_type_raw": "4WJ", "vehicle_class": "4WJ", "std": "2026-08-16T01:10:00+07:00", "seal_code": "SPXS0000000104", "delivered_at": "2026-08-16T09:05:00+07:00", "delivered_via": "mobile", "billing_party_id": "@BP_CJSF", "billing_date": "2026-08-16T00:21:00+07:00", "_billing_axis_date": "2026-08-16", "_billing": "V04 priced 1150.00 (switch-day 00:21 ICT)", "created_at": "2026-08-16T01:10:00+07:00"},
{"id": "@TR05", "tenant_id": "@TN_OWN", "tenant_source": "task", "trip_no": "ZXJB26082000105", "status": "delivered", "job_type": "line_haul", "job_category": "PRIMARY", "task_id": "@TK05", "driver_id": "@D1", "origin_raw": "SPK-GW", "origin_hub_id": "@H_SPKGW", "destination_raw": "SPK890103", "destination_hub_id": "@H_SPK103", "truck_id": "@T1", "truck_license_plate_snapshot": "1ขค-1234", "truck_type_raw": "4WJ", "vehicle_class": "4WJ", "std": "2026-08-20T08:10:00+07:00", "seal_code": "SPXS0000000105", "delivered_at": "2026-08-20T16:30:00+07:00", "delivered_via": "mobile", "is_multi_delivery": true, "billing_party_id": "@BP_CJSF", "billing_date": "2026-08-20T07:00:00+07:00", "_billing_axis_date": "2026-08-20", "_billing": "V05 priced 2250.00 (multi-drop legacy per-route)", "created_at": "2026-08-20T08:10:00+07:00"},
{"id": "@TR06", "tenant_id": "@TN_OWN", "tenant_source": "task", "trip_no": "ZXJB26083100106", "status": "delivered", "job_type": "line_haul", "job_category": "PRIMARY", "task_id": "@TK06", "driver_id": "@D1", "origin_raw": "SPK-GW", "origin_hub_id": "@H_SPKGW", "destination_raw": "SPK890103", "destination_hub_id": "@H_SPK103", "truck_id": "@T1", "truck_license_plate_snapshot": "1ขค-1234", "truck_type_raw": "4WJ", "vehicle_class": "4WJ", "std": "2026-08-31T22:40:00+07:00", "seal_code": "SPXS0000000106", "delivered_at": "2026-09-01T03:10:00+07:00", "delivered_via": "mobile", "billing_party_id": "@BP_CJSF", "billing_date": "2026-08-31T22:00:00+07:00", "_billing_axis_date": "2026-08-31", "_billing": "V06 priced 1150.00 (plan axis Aug, delivered Sep)", "created_at": "2026-08-31T22:40:00+07:00"},
{"id": "@TR07", "legacy_doc_id": "ZXJB26080700107", "tenant_id": "@TN_OWN", "tenant_source": "task", "trip_no": "ZXJB26080700107", "status": "delivered", "job_type": "line_haul", "job_category": "PRIMARY", "task_id": "@TK07", "legacy_task_ref": "seedTaskDoc000000T07", "task_ref_match": "doc_id", "driver_id": "@D1", "legacy_driver_ref": "SEEDUID0000000000000000000D1", "driver_ref_match": "auth_uid", "origin_raw": "SPK-GW", "origin_hub_id": "@H_SPKGW", "destination_raw": "SPK890103", "destination_hub_id": "@H_SPK103", "truck_id": "@T1", "truck_license_plate_snapshot": "1ขค-1234", "truck_type_raw": "4WJ", "vehicle_class": "4WJ", "std": "2026-08-08T06:30:00+07:00", "seal_code": "SPXS0000000107", "delivered_at": "2026-08-08T11:00:00+07:00", "delivered_via": "mobile", "billing_party_id": "@BP_CJSF", "_billing_axis_date": "2026-08-08", "_billing": "V07 priced 1130.00 (legacy; billing_date NULL on a plan-basis party -> missing_billing_date)", "created_at": "2026-08-08T06:30:00+07:00"},
{"id": "@TR08", "tenant_id": "@TN_OWN", "tenant_source": "task", "trip_no": "ZXZB26081200108", "status": "delivered", "job_type": "first_mile", "job_category": "PRIMARY", "task_id": "@TK08", "driver_id": "@D1", "origin_raw": "TTP-HUB1", "origin_hub_id": "@H_TTP1", "destination_raw": "SOCE", "destination_hub_id": "@H_SOCE", "destination_soc_key": "SOCE", "truck_id": "@T3", "truck_license_plate_snapshot": "3ฒท-9012", "truck_type_raw": "6WH", "vehicle_class": "6WH", "std": "2026-08-12T09:00:00+07:00", "seal_code": "SPXS0000000108", "delivered_at": "2026-08-12T15:20:00+07:00", "delivered_via": "mobile", "billing_party_id": "@BP_TTP", "billing_date": "2026-08-12T15:20:00+07:00", "_billing_axis_date": "2026-08-12", "_billing": "V08 priced 2100.00 (tie-break)", "created_at": "2026-08-12T09:00:00+07:00"},
{"id": "@TR09", "tenant_id": "@TN_OWN", "tenant_source": "task", "trip_no": "ZXJB26090300109", "status": "delivered", "job_type": "line_haul", "job_category": "PRIMARY", "task_id": "@TK09", "driver_id": "@D2", "origin_raw": "ประเวศ18 (OCR)", "destination_raw": "SPK-GW", "destination_hub_id": "@H_SPKGW", "truck_id": "@T2", "truck_license_plate_snapshot": "2ขค-5678", "truck_type_raw": "4WJ", "vehicle_class": "4WJ", "std": "2026-09-03T08:40:00+07:00", "seal_code": "SPXS0000000109", "delivered_at": "2026-09-03T13:30:00+07:00", "delivered_via": "mobile", "billing_party_id": "@BP_NWR", "billing_date": "2026-09-03T08:00:00+07:00", "_billing_axis_date": "2026-09-03", "_billing": "V09 unpriced no_rate", "created_at": "2026-09-03T08:40:00+07:00"},
{"id": "@TR10", "tenant_id": "@TN_OWN", "tenant_source": "task", "trip_no": "ZXJB26090400110", "status": "delivered", "job_type": "line_haul", "job_category": "PRIMARY", "task_id": "@TK10", "driver_id": "@D1", "origin_raw": "SPK-GW", "origin_hub_id": "@H_SPKGW", "destination_raw": "SPK890103", "destination_hub_id": "@H_SPK103", "truck_license_plate_snapshot": "กข-1111", "std": "2026-09-04T07:00:00+07:00", "seal_code": "SPXS0000000110", "delivered_at": "2026-09-04T14:00:00+07:00", "delivered_via": "mobile", "billing_party_id": "@BP_CJSF", "billing_date": "2026-09-04T06:00:00+07:00", "_billing_axis_date": "2026-09-04", "_billing": "V10 unpriced no_vehicle_class (orphan plate)", "created_at": "2026-09-04T07:00:00+07:00"},
{"id": "@TR11", "tenant_id": "@TN_OWN", "tenant_source": "task", "trip_no": "ZXJB26090500111", "status": "delivered", "job_type": "line_haul", "job_category": "SUPPLEMENTARY", "task_id": "@TK11", "driver_id": "@D2", "origin_raw": "SPK-GW", "origin_hub_id": "@H_SPKGW", "destination_raw": "SPK890103", "destination_hub_id": "@H_SPK103", "truck_id": "@T2", "truck_license_plate_snapshot": "2ขค-5678", "truck_type_raw": "4WJ", "vehicle_class": "4WJ", "std": "2026-09-05T07:50:00+07:00", "seal_code": "SPXS0000000111", "delivered_at": "2026-09-05T12:45:00+07:00", "delivered_via": "mobile", "billing_party_id": "@BP_CJSF", "billing_date": "2026-09-05T07:00:00+07:00", "_billing_axis_date": "2026-09-05", "_billing": "V11 priced 950.00 frozen (SUPPLEMENTARY)", "created_at": "2026-09-05T07:50:00+07:00"},
{"id": "@TR12", "tenant_id": "@TN_OWN", "tenant_source": "task", "trip_no": "ZXJB26090700112", "status": "delivered", "job_type": "line_haul", "job_category": "SUPPLEMENTARY", "task_id": "@TK12", "driver_id": "@D1", "origin_raw": "SPK-GW", "origin_hub_id": "@H_SPKGW", "destination_raw": "SPK890103", "destination_hub_id": "@H_SPK103", "truck_id": "@T1", "truck_license_plate_snapshot": "1ขค-1234", "truck_type_raw": "4WJ", "vehicle_class": "4WJ", "std": "2026-09-07T07:40:00+07:00", "seal_code": "SPXS0000000112", "delivered_at": "2026-09-07T15:10:00+07:00", "delivered_via": "mobile", "billing_party_id": "@BP_CJSF", "billing_date": "2026-09-07T07:00:00+07:00", "_billing_axis_date": "2026-09-07", "_billing": "V12 corrupted 910.00 -> repair 950.00", "created_at": "2026-09-07T07:40:00+07:00"},
{"id": "@TR13", "tenant_id": "@TN_OWN", "tenant_source": "task", "trip_no": "ZXJB26091000113", "status": "delivered", "job_type": "line_haul", "job_category": "PRIMARY", "task_id": "@TK13", "driver_id": "@D2", "origin_raw": "SPK-GW", "origin_hub_id": "@H_SPKGW", "destination_raw": "SPK890103", "destination_hub_id": "@H_SPK103", "truck_id": "@T2", "truck_license_plate_snapshot": "2ขค-5678", "truck_type_raw": "4WJ", "vehicle_class": "4WJ", "std": "2026-09-10T07:50:00+07:00", "seal_code": "SPXS0000000113", "delivered_at": "2026-09-10T13:20:00+07:00", "delivered_via": "admin_web", "billing_party_id": "@BP_CJSF", "billing_date": "2026-09-10T07:00:00+07:00", "_billing_axis_date": "2026-09-10", "_billing": "V13 manual override 1500.00 (engine would give 1160.00)", "created_at": "2026-09-10T07:50:00+07:00"},
{"id": "@TR14", "tenant_id": "@TN_OWN", "tenant_source": "task", "trip_no": "ZXJB26092000114", "status": "delivered", "job_type": "line_haul", "job_category": "PRIMARY", "task_id": "@TK14", "driver_id": "@D1", "origin_raw": "SPK-GW", "origin_hub_id": "@H_SPKGW", "destination_raw": "SPK890103", "destination_hub_id": "@H_SPK103", "truck_id": "@T1", "truck_license_plate_snapshot": "1ขค-1234", "truck_type_raw": "4WJ", "vehicle_class": "4WJ", "std": "2026-09-20T08:00:00+07:00", "seal_code": "SPXS0000000114", "delivered_at": "2026-09-20T14:50:00+07:00", "delivered_via": "mobile", "billing_party_id": "@BP_CJSF", "billing_date": "2026-09-20T07:00:00+07:00", "_billing_axis_date": "2026-09-20", "_billing": "V14 priced 1160.00 (voided rate + voided fuel fall back)", "created_at": "2026-09-20T08:00:00+07:00"},
{"id": "@TR15", "legacy_doc_id": "ZXZB26090800115", "tenant_id": "@TN_OWN", "tenant_source": "task", "trip_no": "ZXZB26090800115", "status": "delivered", "job_type": "first_mile", "job_category": "PRIMARY", "task_id": "@TK15", "legacy_task_ref": "seedTaskDoc000000T15", "task_ref_match": "doc_id", "driver_id": "@D2", "legacy_driver_ref": "SEEDUID0000000000000000000D2", "driver_ref_match": "auth_uid", "origin_raw": "BANGNA-DRV", "origin_hub_id": "@H_BANGNA", "destination_raw": "SOCE", "destination_hub_id": "@H_SOCE", "destination_soc_key": "SOCE", "truck_id": "@T2", "truck_license_plate_snapshot": "2ขค-5678", "truck_type_raw": "4WJ", "vehicle_class": "4WJ", "std": "2026-09-08T07:50:00+07:00", "seal_code": "SPXS0000000115", "delivered_at": "2026-09-08T11:40:00+07:00", "delivered_via": "mobile", "_billing": "V15 unpriced no_customer (safety-net snapshot with the stored reason)", "created_at": "2026-09-08T07:50:00+07:00"},
{"id": "@TR16", "tenant_id": "@TN_OWN", "tenant_source": "task", "trip_no": "ZXJB26082600116", "status": "delivered", "job_type": "line_haul", "job_category": "PRIMARY", "task_id": "@TK16", "driver_id": "@D2", "origin_raw": "SPK-GW", "origin_hub_id": "@H_SPKGW", "destination_raw": "SPK890146", "destination_hub_id": "@H_SPK146", "truck_id": "@T2", "truck_license_plate_snapshot": "2ขค-5678", "truck_type_raw": "4WJ", "vehicle_class": "4WJ", "std": "2026-08-26T08:00:00+07:00", "seal_code": "SPXS0000000116", "delivered_at": "2026-08-26T17:55:00+07:00", "delivered_via": "mobile", "is_multi_delivery": true, "billing_party_id": "@BP_SPK", "billing_date": "2026-08-26T17:55:00+07:00", "_billing_axis_date": "2026-08-26", "_billing": "V16 priced 1700.00 (multi-drop flat fee)", "created_at": "2026-08-26T08:00:00+07:00"},
{"id": "@TR17", "tenant_id": "@TN_OWN", "tenant_source": "task", "trip_no": "ZXJB26093000117", "status": "in_transit", "job_type": "line_haul", "job_category": "PRIMARY", "task_id": "@TK17", "driver_id": "@D1", "origin_raw": "SPK-GW", "origin_hub_id": "@H_SPKGW", "destination_raw": "SPK890103", "destination_hub_id": "@H_SPK103", "truck_id": "@T1", "truck_license_plate_snapshot": "1ขค-1234", "truck_type_raw": "4WJ", "vehicle_class": "4WJ", "std": "2026-09-30T07:00:00+07:00", "seal_code": "SPXS0000000117", "_billing": "not delivered", "created_at": "2026-09-30T07:00:00+07:00"},
{"id": "@TR18", "tenant_id": "@TN_OWN", "tenant_source": "task", "trip_no": "ZXZB26092900118", "status": "incident", "job_type": "first_mile", "job_category": "PRIMARY", "task_id": "@TK18", "driver_id": "@D2", "origin_raw": "ALANG-A", "origin_hub_id": "@H_ALANGA", "destination_raw": "SOCE", "destination_hub_id": "@H_SOCE", "destination_soc_key": "SOCE", "truck_id": "@T2", "truck_license_plate_snapshot": "2ขค-5678", "truck_type_raw": "4WJ", "vehicle_class": "4WJ", "std": "2026-09-29T07:30:00+07:00", "seal_code": "SPXS0000000118", "_billing": "not delivered", "created_at": "2026-09-29T07:30:00+07:00"},
{"id": "@TR19", "legacy_doc_id": "36601877", "tenant_id": "@TN_OWN", "tenant_source": "task", "trip_no": "36601877", "status": "standby", "job_type": "first_mile", "task_id": "@TK19", "legacy_task_ref": "seedTaskDoc000000T19", "task_ref_match": "doc_id", "driver_id": "@D2", "legacy_driver_ref": "SEEDUID0000000000000000000D2", "driver_ref_match": "auth_uid", "origin_raw": "ALANG-A", "origin_hub_id": "@H_ALANGA", "destination_raw": "SOCE", "destination_hub_id": "@H_SOCE", "destination_soc_key": "SOCE", "truck_id": "@T2", "truck_license_plate_snapshot": "2ขค-5678", "truck_type_raw": "4WJ", "vehicle_class": "4WJ", "std": "2026-07-05T07:20:00+07:00", "_billing": "legacy standby trip, never priced (ETL quarantine legacy_standby_trip)", "created_at": "2026-07-05T07:20:00+07:00"},
{"id": "@TR20", "tenant_id": "@TN_TTP", "tenant_source": "task", "trip_no": "ZXZB26091200120", "status": "cancelled", "job_type": "first_mile", "job_category": "PRIMARY", "task_id": "@TK20", "driver_id": "@D7", "origin_raw": "TTP-HUB1", "origin_hub_id": "@H_TTP1", "destination_raw": "SOCE", "destination_hub_id": "@H_SOCE", "destination_soc_key": "SOCE", "truck_id": "@T6", "truck_license_plate_snapshot": "5กข-7890", "truck_type_raw": "6WH", "vehicle_class": "6WH", "std": "2026-09-12T07:30:00+07:00", "_billing": "cancelled", "created_at": "2026-09-12T07:30:00+07:00"},
{"id": "@TR21", "tenant_id": "@TN_OWN", "tenant_source": "task", "trip_no": "ZXJB26082700121", "status": "delivered", "job_type": "line_haul", "job_category": "PRIMARY", "task_id": "@TK21", "driver_id": "@D1", "origin_raw": "SPK-GW", "origin_hub_id": "@H_SPKGW", "destination_raw": "SPK890174", "destination_hub_id": "@H_SPK174", "truck_id": "@T3", "truck_license_plate_snapshot": "3ฒท-9012", "truck_type_raw": "6WH", "vehicle_class": "6WH", "std": "2026-08-27T07:00:00+07:00", "seal_code": "SPXS0000000121", "delivered_at": "2026-08-27T13:00:00+07:00", "delivered_via": "mobile", "billing_party_id": "@BP_CJSF", "billing_date": "2026-08-27T06:00:00+07:00", "line_delivered_notified_at": "2026-08-27T13:01:00+07:00", "evidence_token": "seedEvidenceTokenTR21", "evidence_token_revoked_at": "2026-08-28T09:00:00+07:00", "_billing_axis_date": "2026-08-27", "_billing": "V21 priced 1800.00", "created_at": "2026-08-27T07:00:00+07:00"}
]
```

#### `trip_delivery_stops` (5)

```json
[
{"id": "@PDS05_1", "trip_id": "@TR05", "stop_index": 1, "destination_raw": "SPK890103", "destination_hub_id": "@H_SPK103", "status": "delivered", "delivered_at": "2026-08-20T11:10:00+07:00", "completed_seq": 1},
{"id": "@PDS05_2", "trip_id": "@TR05", "stop_index": 2, "destination_raw": "SPK890146", "destination_hub_id": "@H_SPK146", "status": "delivered", "delivered_at": "2026-08-20T16:30:00+07:00", "completed_seq": 2},
{"id": "@PDS16_1", "trip_id": "@TR16", "stop_index": 1, "destination_raw": "SPK890146", "destination_hub_id": "@H_SPK146", "status": "delivered", "delivered_at": "2026-08-26T15:20:00+07:00", "completed_seq": 2},
{"id": "@PDS16_2", "trip_id": "@TR16", "stop_index": 2, "destination_raw": "SPK890103", "destination_hub_id": "@H_SPK103", "status": "delivered", "delivered_at": "2026-08-26T13:05:00+07:00", "completed_seq": 1},
{"id": "@PDS16_3", "trip_id": "@TR16", "stop_index": 3, "destination_raw": "SPK890174", "destination_hub_id": "@H_SPK174", "status": "delivered", "delivered_at": "2026-08-26T17:55:00+07:00", "completed_seq": 3}
]
```

#### `trip_photos` (4)

```json
[
{"id": "@PH01", "trip_id": "@TR01", "photo_type": "seal", "_photo_type_known": true, "file_id": "@FO_TR01_SEAL", "geocode_lat": 13.5236, "geocode_lng": 100.6583, "position": 0, "created_at": "2026-07-10T09:50:00+07:00"},
{"id": "@PH03", "trip_id": "@TR03", "photo_type": "seal", "_photo_type_known": true, "file_id": "@FO_TR03_SEAL", "position": 0, "created_at": "2026-07-23T07:58:00+07:00"},
{"id": "@PH16", "trip_id": "@TR16", "stop_id": "@PDS16_2", "photo_type": "stop_2_arrived", "_photo_type_known": true, "file_id": "@FO_TR16_S2", "position": 0, "created_at": "2026-08-26T13:05:00+07:00"},
{"id": "@PH19", "trip_id": "@TR19", "photo_type": "pre_close", "_photo_type_known": true, "file_id": "@FO_TR19_PC", "position": 0, "created_at": "2026-07-05T07:15:00+07:00"}
]
```

#### `trip_no_history` (1)

```json
[
{"id": "@TNH03", "trip_id": "@TR03", "old_trip_no": "36601950", "new_trip_no": "ZXZB26072300103", "renamed_by": "@U_WRT_ADMIN", "renamed_at": "2026-07-23T16:00:00+07:00"}
]
```

#### `standby_records` (4)

```json
[
{"id": "@SB01", "tenant_id": "@TN_OWN", "tenant_source": "driver", "driver_id": "@D2", "customer_party_id": "@BP_CJSF", "customer_resolved": true, "customer_resolved_from": "origin_hub", "start_location_raw": "SPK-GW", "end_location_raw": "SPK-GW", "started_at": "2026-07-18T08:00:00+07:00", "ended_at": "2026-07-18T15:30:00+07:00", "_billing_axis_date": "2026-07-18", "duration_minutes": 450, "status": "completed", "truck_id": "@T2", "truck_license_plate_snapshot": "2ขค-5678", "billing_estimate_thb": "800.00", "billing_party_id": "@BP_CJSF", "billing_rate_source": "standby_rate", "billing_rate_entry_id": "@SR01", "billing_effective_from_date": "2026-07-01", "billing_computed_at": "2026-07-18T15:31:00+07:00", "client_op_id": "5d3a8b53-1548-59fe-8011-8f9d0137dfe4", "created_at": "2026-07-18T08:00:00+07:00", "_billing": "S01 standby_rate 800.00"},
{"id": "@SB02", "tenant_id": "@TN_OWN", "tenant_source": "task", "driver_id": "@D1", "task_id": "@TK06", "customer_party_id": "@BP_CJSF", "customer_resolved": true, "customer_resolved_from": "task", "job_category": "PRIMARY", "start_location_raw": "SPK-GW", "end_location_raw": "SPK-GW", "started_at": "2026-08-31T20:00:00+07:00", "ended_at": "2026-09-01T02:00:00+07:00", "_billing_axis_date": "2026-09-01", "duration_minutes": 360, "status": "completed", "truck_id": "@T1", "truck_license_plate_snapshot": "1ขค-1234", "billing_estimate_thb": "850.00", "billing_party_id": "@BP_CJSF", "billing_rate_source": "standby_rate", "billing_rate_entry_id": "@SR02", "billing_effective_from_date": "2026-09-01", "billing_computed_at": "2026-09-01T02:01:00+07:00", "client_op_id": "fce31bdf-37f9-5285-add2-04b11c29bfd1", "created_at": "2026-08-31T20:00:00+07:00", "_billing": "S02 plan-basis party, ended_at axis -> September round 850.00 while its task's trip TR06 bills in August"},
{"id": "@SB03", "tenant_id": "@TN_OWN", "tenant_source": "task", "driver_id": "@D1", "task_id": "@TK08", "customer_party_id": "@BP_TTP", "customer_resolved": true, "customer_resolved_from": "task", "start_location_raw": "TTP-HUB1", "end_location_raw": "TTP-HUB1", "started_at": "2026-08-12T05:00:00+07:00", "ended_at": "2026-08-12T08:10:00+07:00", "_billing_axis_date": "2026-08-12", "duration_minutes": 190, "status": "completed", "truck_id": "@T3", "truck_license_plate_snapshot": "3ฒท-9012", "billing_estimate_thb": "500.00", "billing_party_id": "@BP_TTP", "billing_rate_source": "service_fee", "billing_computed_at": "2026-08-12T08:11:00+07:00", "client_op_id": "c25ae402-8328-53e9-acec-a9bb86e4a9d6", "created_at": "2026-08-12T05:00:00+07:00", "_billing": "S03 service-fee fallback 500.00 (SR03 voided)"},
{"id": "@SB04", "tenant_id": "@TN_OWN", "tenant_source": "driver", "driver_id": "@D2", "customer_party_id": "@BP_SPX", "customer_resolved": true, "customer_resolved_from": "origin_hub", "start_location_raw": "ALANG-A", "end_location_raw": "ALANG-A", "started_at": "2026-09-09T13:00:00+07:00", "ended_at": "2026-09-09T15:00:00+07:00", "_billing_axis_date": "2026-09-09", "duration_minutes": 120, "status": "completed", "truck_id": "@T2", "truck_license_plate_snapshot": "2ขค-5678", "billing_unpriced_reason": "no_rate", "billing_computed_at": "2026-09-09T15:01:00+07:00", "client_op_id": "9f449586-2bcc-5779-b061-a10d6eeabb17", "created_at": "2026-09-09T13:00:00+07:00", "_billing": "S04 unpriced no_rate"}
]
```

#### `incident_reports` (1)

```json
[
{"id": "@IR1", "tenant_id": "@TN_OWN", "tenant_source": "trip", "driver_id": "@D2", "trip_id": "@TR18", "delay_cause": "incident_cause_tire", "description": "ยางหลังซ้ายแตก รอช่างมาเปลี่ยน", "lat": 13.6702, "lng": 100.6601, "truck_id": "@T2", "truck_license_plate_snapshot": "2ขค-5678", "situation1_photo_file_id": "@FO_IR1_S1", "reported_by_kind": "driver", "reported_by_user_id": "@U_D2", "client_op_id": "259323da-26f1-5c89-8a84-c6b22928d321", "created_at": "2026-09-29T10:05:00+07:00"}
]
```

### D.4.9 Billing

#### `trip_billing_snapshots` (17)

```json
[
{"trip_id": "@TR01", "tenant_id": "@TN_OWN", "estimate_thb": "1130.00", "base_rate_thb": "1200.00", "rate_entry_id": "@RE01", "rate_import_id": "rc_1782354600000", "lookup_hub_code": "SPK-GW", "lookup_destination_code": "SPK890103", "fuel_adjustment_id": "@FA01", "rate_multiplier": "1.000000", "add_thb_per_trip": "-70.00", "fuel_effective_from_date": "2026-07-01", "round_effective_from_date": "2026-07-01", "fuel_band_lower_thb": "34.01", "fuel_band_upper_thb": "35.00", "reference_fuel_price_thb": "34.50", "is_multi_delivery": false, "manual_override": false, "job_category_at_pricing": "PRIMARY", "computed_by": "mobile_delivery", "compute_version": 1, "computed_at": "2026-07-10T15:40:00+07:00", "updated_at": "2026-07-10T15:40:00+07:00"},
{"trip_id": "@TR02", "tenant_id": "@TN_OWN", "estimate_thb": "1150.00", "base_rate_thb": "1000.00", "rate_entry_id": "@RE06", "rate_import_id": "rc_1782354600000", "lookup_hub_code": "ALANG-A", "lookup_destination_code": "SOCE", "fuel_adjustment_id": "@FA05", "rate_multiplier": "1.100000", "add_thb_per_trip": "50.00", "fuel_effective_from_date": "2026-07-01", "round_effective_from_date": "2026-07-01", "is_multi_delivery": false, "manual_override": false, "job_category_at_pricing": "PRIMARY", "computed_by": "etl", "compute_version": 0, "computed_at": "2026-07-22T10:15:00+07:00", "updated_at": "2026-07-22T10:15:00+07:00"},
{"trip_id": "@TR03", "tenant_id": "@TN_OWN", "estimate_thb": "1250.00", "base_rate_thb": "1250.00", "rate_entry_id": "@RE07", "rate_import_id": "rc_1782354600000", "lookup_hub_code": "ALANG-A", "lookup_destination_code": "WANGTHONGLANG12", "rate_multiplier": "1.000000", "add_thb_per_trip": "0.00", "round_effective_from_date": "2026-07-01", "is_multi_delivery": false, "manual_override": true, "job_category_at_pricing": "SUPPLEMENTARY", "computed_by": "etl", "compute_version": 0, "computed_at": "2026-07-23T14:06:00+07:00", "updated_at": "2026-07-23T14:06:00+07:00"},
{"trip_id": "@TR04", "tenant_id": "@TN_OWN", "estimate_thb": "1150.00", "base_rate_thb": "1200.00", "rate_entry_id": "@RE01", "rate_import_id": "rc_1782354600000", "lookup_hub_code": "SPK-GW", "lookup_destination_code": "SPK890103", "fuel_adjustment_id": "@FA02", "rate_multiplier": "1.000000", "add_thb_per_trip": "-50.00", "fuel_effective_from_date": "2026-08-16", "round_effective_from_date": "2026-08-16", "fuel_band_lower_thb": "36.01", "fuel_band_upper_thb": "37.00", "reference_fuel_price_thb": "36.50", "is_multi_delivery": false, "manual_override": false, "job_category_at_pricing": "PRIMARY", "computed_by": "mobile_delivery", "compute_version": 1, "computed_at": "2026-08-16T09:05:00+07:00", "updated_at": "2026-08-16T09:05:00+07:00"},
{"trip_id": "@TR05", "tenant_id": "@TN_OWN", "estimate_thb": "2250.00", "base_rate_thb": "1150.00", "stop_charge_thb": "1100.00", "rate_entry_id": "@RE01", "rate_import_id": "rc_1782354600000", "lookup_hub_code": "SPK-GW", "lookup_destination_code": "SPK890103", "fuel_adjustment_id": "@FA02", "rate_multiplier": "1.000000", "add_thb_per_trip": "-50.00", "fuel_effective_from_date": "2026-08-16", "round_effective_from_date": "2026-08-16", "fuel_band_lower_thb": "36.01", "fuel_band_upper_thb": "37.00", "reference_fuel_price_thb": "36.50", "is_multi_delivery": true, "manual_override": false, "job_category_at_pricing": "PRIMARY", "computed_by": "mobile_delivery", "compute_version": 1, "computed_at": "2026-08-20T16:30:00+07:00", "updated_at": "2026-08-20T16:30:00+07:00"},
{"trip_id": "@TR06", "tenant_id": "@TN_OWN", "estimate_thb": "1150.00", "base_rate_thb": "1200.00", "rate_entry_id": "@RE01", "rate_import_id": "rc_1782354600000", "lookup_hub_code": "SPK-GW", "lookup_destination_code": "SPK890103", "fuel_adjustment_id": "@FA02", "rate_multiplier": "1.000000", "add_thb_per_trip": "-50.00", "fuel_effective_from_date": "2026-08-16", "round_effective_from_date": "2026-08-16", "fuel_band_lower_thb": "36.01", "fuel_band_upper_thb": "37.00", "reference_fuel_price_thb": "36.50", "is_multi_delivery": false, "manual_override": false, "job_category_at_pricing": "PRIMARY", "computed_by": "mobile_delivery", "compute_version": 1, "computed_at": "2026-09-01T03:10:00+07:00", "updated_at": "2026-09-01T03:10:00+07:00"},
{"trip_id": "@TR07", "tenant_id": "@TN_OWN", "estimate_thb": "1130.00", "base_rate_thb": "1200.00", "rate_entry_id": "@RE01", "rate_import_id": "rc_1782354600000", "lookup_hub_code": "SPK-GW", "lookup_destination_code": "SPK890103", "fuel_adjustment_id": "@FA01", "rate_multiplier": "1.000000", "add_thb_per_trip": "-70.00", "fuel_effective_from_date": "2026-07-01", "round_effective_from_date": "2026-07-01", "fuel_band_lower_thb": "34.01", "fuel_band_upper_thb": "35.00", "reference_fuel_price_thb": "34.50", "is_multi_delivery": false, "manual_override": false, "job_category_at_pricing": "PRIMARY", "computed_by": "etl", "compute_version": 0, "computed_at": "2026-08-08T11:00:00+07:00", "updated_at": "2026-08-08T11:00:00+07:00"},
{"trip_id": "@TR08", "tenant_id": "@TN_OWN", "estimate_thb": "2100.00", "base_rate_thb": "2100.00", "rate_entry_id": "@RE09", "rate_import_id": "rc_1784948400000", "lookup_hub_code": "TTP-HUB1", "lookup_destination_code": "SOCE", "rate_multiplier": "1.000000", "add_thb_per_trip": "0.00", "round_effective_from_date": "2026-08-01", "is_multi_delivery": false, "manual_override": false, "job_category_at_pricing": "PRIMARY", "computed_by": "mobile_delivery", "compute_version": 1, "computed_at": "2026-08-12T15:20:00+07:00", "updated_at": "2026-08-12T15:20:00+07:00"},
{"trip_id": "@TR09", "tenant_id": "@TN_OWN", "estimate_thb": null, "unpriced_reason": "no_rate", "base_rate_thb": null, "lookup_hub_code": "SPK890146", "lookup_destination_code": "SPK", "rate_multiplier": "1.000000", "add_thb_per_trip": "0.00", "is_multi_delivery": false, "manual_override": false, "job_category_at_pricing": "PRIMARY", "computed_by": "mobile_delivery", "compute_version": 1, "computed_at": "2026-09-03T13:30:00+07:00", "updated_at": "2026-09-03T13:30:00+07:00"},
{"trip_id": "@TR10", "tenant_id": "@TN_OWN", "estimate_thb": null, "unpriced_reason": "no_vehicle_class", "base_rate_thb": null, "lookup_hub_code": "SPK-GW", "lookup_destination_code": "SPK890103", "rate_multiplier": "1.000000", "add_thb_per_trip": "0.00", "is_multi_delivery": false, "manual_override": false, "job_category_at_pricing": "PRIMARY", "computed_by": "mobile_delivery", "compute_version": 1, "computed_at": "2026-09-04T14:00:00+07:00", "updated_at": "2026-09-04T14:00:00+07:00"},
{"trip_id": "@TR11", "tenant_id": "@TN_OWN", "estimate_thb": "950.00", "base_rate_thb": "950.00", "rate_entry_id": "@RE02", "rate_import_id": "rc_1782354600000", "lookup_hub_code": "SPK-GW", "lookup_destination_code": "SPK890103", "rate_multiplier": "1.000000", "add_thb_per_trip": "0.00", "round_effective_from_date": "2026-07-01", "is_multi_delivery": false, "manual_override": true, "job_category_at_pricing": "SUPPLEMENTARY", "computed_by": "mobile_delivery", "compute_version": 1, "computed_at": "2026-09-05T12:45:00+07:00", "updated_at": "2026-09-05T12:45:00+07:00"},
{"trip_id": "@TR12", "tenant_id": "@TN_OWN", "estimate_thb": "910.00", "base_rate_thb": "950.00", "rate_entry_id": "@RE02", "rate_import_id": "rc_1782354600000", "lookup_hub_code": "SPK-GW", "lookup_destination_code": "SPK890103", "fuel_adjustment_id": "@FA03", "rate_multiplier": "1.000000", "add_thb_per_trip": "-40.00", "fuel_effective_from_date": "2026-09-01", "round_effective_from_date": "2026-09-01", "fuel_band_lower_thb": "37.01", "fuel_band_upper_thb": "38.00", "reference_fuel_price_thb": "37.20", "is_multi_delivery": false, "manual_override": false, "job_category_at_pricing": "SUPPLEMENTARY", "computed_by": "etl", "compute_version": 0, "computed_at": "2026-09-07T15:12:00+07:00", "updated_at": "2026-09-07T15:12:00+07:00"},
{"trip_id": "@TR13", "tenant_id": "@TN_OWN", "estimate_thb": "1500.00", "base_rate_thb": "1200.00", "rate_entry_id": "@RE01", "rate_import_id": "rc_1782354600000", "lookup_hub_code": "SPK-GW", "lookup_destination_code": "SPK890103", "rate_multiplier": "1.000000", "add_thb_per_trip": "0.00", "is_multi_delivery": false, "manual_override": true, "job_category_at_pricing": "PRIMARY", "computed_by": "manual_edit", "compute_version": 1, "computed_at": "2026-09-10T16:00:00+07:00", "updated_at": "2026-09-10T16:00:00+07:00"},
{"trip_id": "@TR14", "tenant_id": "@TN_OWN", "estimate_thb": "1160.00", "base_rate_thb": "1200.00", "rate_entry_id": "@RE01", "rate_import_id": "rc_1782354600000", "lookup_hub_code": "SPK-GW", "lookup_destination_code": "SPK890103", "fuel_adjustment_id": "@FA03", "rate_multiplier": "1.000000", "add_thb_per_trip": "-40.00", "fuel_effective_from_date": "2026-09-01", "round_effective_from_date": "2026-09-01", "fuel_band_lower_thb": "37.01", "fuel_band_upper_thb": "38.00", "reference_fuel_price_thb": "37.20", "is_multi_delivery": false, "manual_override": false, "job_category_at_pricing": "PRIMARY", "computed_by": "mobile_delivery", "compute_version": 1, "computed_at": "2026-09-20T14:50:00+07:00", "updated_at": "2026-09-20T14:50:00+07:00", "last_event_id": 1},
{"trip_id": "@TR15", "tenant_id": "@TN_OWN", "estimate_thb": null, "unpriced_reason": "no_customer", "computed_by": "scheduler", "compute_version": 1, "computed_at": "2026-09-08T12:00:00+07:00", "updated_at": "2026-09-08T12:00:00+07:00", "_billing": "V15 (safety net)"},
{"trip_id": "@TR16", "tenant_id": "@TN_OWN", "estimate_thb": "1700.00", "base_rate_thb": "1100.00", "stop_charge_thb": "600.00", "rate_entry_id": "@RE08", "rate_import_id": "rc_1782354600000", "lookup_hub_code": "SPK-GW", "lookup_destination_code": "SPK890146", "fuel_adjustment_id": "@FA06", "rate_multiplier": "1.000000", "add_thb_per_trip": "0.00", "fuel_effective_from_date": "2026-07-01", "round_effective_from_date": "2026-07-01", "is_multi_delivery": true, "manual_override": false, "job_category_at_pricing": "PRIMARY", "computed_by": "mobile_delivery", "compute_version": 1, "computed_at": "2026-08-26T17:55:00+07:00", "updated_at": "2026-08-26T17:55:00+07:00"},
{"trip_id": "@TR21", "tenant_id": "@TN_OWN", "estimate_thb": "1800.00", "base_rate_thb": "1850.00", "rate_entry_id": "@RE04", "rate_import_id": "rc_1782354600000", "lookup_hub_code": "SPK-GW", "lookup_destination_code": "SPK890174", "fuel_adjustment_id": "@FA02", "rate_multiplier": "1.000000", "add_thb_per_trip": "-50.00", "fuel_effective_from_date": "2026-08-16", "round_effective_from_date": "2026-08-16", "fuel_band_lower_thb": "36.01", "fuel_band_upper_thb": "37.00", "reference_fuel_price_thb": "36.50", "is_multi_delivery": false, "manual_override": false, "job_category_at_pricing": "PRIMARY", "computed_by": "scheduler", "compute_version": 1, "computed_at": "2026-08-27T13:15:00+07:00", "updated_at": "2026-08-27T13:15:00+07:00"}
]
```

#### `trip_billing_stop_breakdown` (5)

```json
[
{"trip_id": "@TR05", "stop_index": 1, "destination_code": "SPK890103", "base_rate_thb": "1200.00", "final_rate_thb": "1150.00"},
{"trip_id": "@TR05", "stop_index": 2, "destination_code": "SPK890146", "base_rate_thb": "1150.00", "final_rate_thb": "1100.00"},
{"trip_id": "@TR16", "stop_index": 1, "destination_code": "SPK890146", "base_rate_thb": "1100.00", "final_rate_thb": "1100.00"},
{"trip_id": "@TR16", "stop_index": 2, "destination_code": "SPK890103", "base_rate_thb": "300.00", "final_rate_thb": "300.00"},
{"trip_id": "@TR16", "stop_index": 3, "destination_code": "SPK890174", "base_rate_thb": "300.00", "final_rate_thb": "300.00"}
]
```

#### `customer_rate_entries` (10)

```json
[
{"id": "@RE01", "legacy_doc_id": "seedRateDoc00000RE01", "tenant_id": "@TN_OWN", "tenant_source": "form", "billing_party_id": "@BP_CJSF", "import_id": "rc_1782354600000", "hub_code": "SPK-GW", "raw_hub_name": "SPK-GW", "destination_code": "SPK890103", "vehicle_class": "4WJ", "rate_thb": "1200.00", "job_category": "PRIMARY", "effective_from_date": "2026-07-01", "effective_from_at": "2026-07-01T07:00:00+07:00", "imported_at": "2026-06-25T09:30:00+07:00", "created_by": "@U_WRT_MGR", "created_at": "2026-06-25T09:30:00+07:00", "voided": false},
{"id": "@RE02", "tenant_id": "@TN_OWN", "tenant_source": "form", "billing_party_id": "@BP_CJSF", "import_id": "rc_1782354600000", "hub_code": "SPK-GW", "raw_hub_name": "SPK-GW", "destination_code": "SPK890103", "vehicle_class": "4WJ", "rate_thb": "950.00", "job_category": "SUPPLEMENTARY", "effective_from_date": "2026-07-01", "effective_from_at": "2026-07-01T00:00:00+07:00", "imported_at": "2026-06-25T09:30:00+07:00", "created_by": "@U_WRT_MGR", "created_at": "2026-06-25T09:30:00+07:00", "voided": false},
{"id": "@RE03", "tenant_id": "@TN_OWN", "tenant_source": "form", "billing_party_id": "@BP_CJSF", "import_id": "rc_1782354600000", "hub_code": "SPK-GW", "raw_hub_name": "SPK-GW", "destination_code": "SPK890146", "vehicle_class": "4WJ", "rate_thb": "1150.00", "job_category": "PRIMARY", "effective_from_date": "2026-07-01", "effective_from_at": "2026-07-01T00:00:00+07:00", "imported_at": "2026-06-25T09:30:00+07:00", "created_by": "@U_WRT_MGR", "created_at": "2026-06-25T09:30:00+07:00", "voided": false},
{"id": "@RE04", "tenant_id": "@TN_OWN", "tenant_source": "form", "billing_party_id": "@BP_CJSF", "import_id": "rc_1782354600000", "hub_code": "SPK-GW", "raw_hub_name": "SPK-GW", "destination_code": "SPK890174", "vehicle_class": "6WH", "rate_thb": "1850.00", "job_category": "PRIMARY", "effective_from_date": "2026-07-01", "effective_from_at": "2026-07-01T00:00:00+07:00", "imported_at": "2026-06-25T09:30:00+07:00", "created_by": "@U_WRT_MGR", "created_at": "2026-06-25T09:30:00+07:00", "voided": false},
{"id": "@RE05", "tenant_id": "@TN_OWN", "tenant_source": "form", "billing_party_id": "@BP_CJSF", "import_id": "manual_1789376400000", "hub_code": "SPK-GW", "raw_hub_name": "SPK-GW", "destination_code": "SPK890103", "vehicle_class": "4WJ", "rate_thb": "1300.00", "job_category": "PRIMARY", "effective_from_date": "2026-09-16", "effective_from_at": "2026-09-16T00:00:00+07:00", "imported_at": "2026-09-14T16:00:00+07:00", "created_by": "@U_WRT_MGR", "created_at": "2026-09-14T16:00:00+07:00", "voided": true, "voided_at": "2026-09-14T16:20:00+07:00", "voided_by": "@U_WRT_ADMIN", "voided_reason": "พิมพ์ราคาผิด ราคาที่ตกลงคือ 1,200"},
{"id": "@RE06", "tenant_id": "@TN_OWN", "tenant_source": "form", "billing_party_id": "@BP_SPX", "import_id": "rc_1782354600000", "hub_code": "ALANG-A", "raw_hub_name": "ALANG-A", "destination_code": "SOCE", "vehicle_class": "4WJ", "rate_thb": "1000.00", "job_category": "PRIMARY", "effective_from_date": "2026-07-01", "effective_from_at": "2026-07-01T00:00:00+07:00", "imported_at": "2026-06-25T09:30:00+07:00", "created_by": "@U_WRT_MGR", "created_at": "2026-06-25T09:30:00+07:00", "voided": false},
{"id": "@RE07", "tenant_id": "@TN_OWN", "tenant_source": "form", "billing_party_id": "@BP_SPX", "import_id": "rc_1782354600000", "hub_code": "ALANG-A", "raw_hub_name": "ALANG-A", "destination_code": "WANGTHONGLANG12", "vehicle_class": "4WJ", "rate_thb": "1250.00", "job_category": "SUPPLEMENTARY", "effective_from_date": "2026-07-01", "effective_from_at": "2026-07-01T00:00:00+07:00", "imported_at": "2026-06-25T09:30:00+07:00", "created_by": "@U_WRT_MGR", "created_at": "2026-06-25T09:30:00+07:00", "voided": false},
{"id": "@RE08", "tenant_id": "@TN_OWN", "tenant_source": "form", "billing_party_id": "@BP_SPK", "import_id": "rc_1782354600000", "hub_code": "SPK-GW", "raw_hub_name": "SPK-GW", "destination_code": "SPK890146", "vehicle_class": "4WJ", "rate_thb": "1100.00", "job_category": "PRIMARY", "effective_from_date": "2026-07-01", "effective_from_at": "2026-07-01T00:00:00+07:00", "imported_at": "2026-06-25T09:30:00+07:00", "created_by": "@U_WRT_MGR", "created_at": "2026-06-25T09:30:00+07:00", "voided": false},
{"id": "@RE09", "tenant_id": "@TN_OWN", "tenant_source": "form", "billing_party_id": "@BP_TTP", "import_id": "rc_1784948400000", "hub_code": "TTP-HUB1", "raw_hub_name": "TTP-HUB1", "destination_code": "SOCE", "vehicle_class": "6WH", "rate_thb": "2100.00", "job_category": "PRIMARY", "effective_from_date": "2026-08-01", "effective_from_at": "2026-08-01T00:00:00+07:00", "imported_at": "2026-07-25T10:00:00+07:00", "created_by": "@U_WRT_MGR", "created_at": "2026-07-25T10:00:00+07:00", "voided": false},
{"id": "@RE10", "tenant_id": "@TN_OWN", "tenant_source": "form", "billing_party_id": "@BP_TTP", "import_id": "rc_1784948700000", "hub_code": "TTP-HUB1", "raw_hub_name": "TTP-HUB1", "destination_code": "SOCE", "vehicle_class": "6WH", "rate_thb": "2150.00", "job_category": "PRIMARY", "effective_from_date": "2026-08-01", "effective_from_at": "2026-08-01T00:00:00+07:00", "imported_at": "2026-07-25T10:05:00+07:00", "created_by": "@U_WRT_MGR", "created_at": "2026-07-25T10:05:00+07:00", "voided": false}
]
```

#### `customer_fuel_rate_adjustments` (6)

```json
[
{"id": "@FA01", "tenant_id": "@TN_OWN", "tenant_source": "form", "billing_party_id": "@BP_CJSF", "effective_from_date": "2026-07-01", "effective_from_at": "2026-07-01T00:00:00+07:00", "rate_multiplier": "1.000000", "add_thb_per_trip": "-70.00", "reference_fuel_price_thb": "34.50", "announcement_note": "รอบ ก.ค. ดีเซลอ้างอิง 34.50", "fuel_band_enabled": true, "fuel_band_baseline_floor": 41, "fuel_band_thb_per_baht": "10.00", "created_by": "@U_WRT_MGR", "created_at": "2026-06-28T10:00:00+07:00", "voided": false},
{"id": "@FA02", "legacy_doc_id": "seedFuelDoc00000FA02", "tenant_id": "@TN_OWN", "tenant_source": "form", "billing_party_id": "@BP_CJSF", "effective_from_date": "2026-08-16", "effective_from_at": "2026-08-16T07:00:00+07:00", "rate_multiplier": "1.000000", "add_thb_per_trip": "-50.00", "reference_fuel_price_thb": "36.50", "announcement_note": "รอบ 16 ส.ค. ดีเซลอ้างอิง 36.50", "fuel_band_enabled": true, "fuel_band_baseline_floor": 41, "fuel_band_thb_per_baht": "10.00", "created_by": "@U_WRT_MGR", "created_at": "2026-08-05T14:00:00+07:00", "voided": false},
{"id": "@FA03", "tenant_id": "@TN_OWN", "tenant_source": "form", "billing_party_id": "@BP_CJSF", "effective_from_date": "2026-09-01", "effective_from_at": "2026-09-01T00:00:00+07:00", "rate_multiplier": "1.000000", "add_thb_per_trip": "-40.00", "reference_fuel_price_thb": "37.20", "announcement_note": "รอบ ก.ย. ดีเซลอ้างอิง 37.20", "fuel_band_enabled": true, "fuel_band_baseline_floor": 41, "fuel_band_thb_per_baht": "10.00", "created_by": "@U_WRT_MGR", "created_at": "2026-08-28T10:00:00+07:00", "voided": false},
{"id": "@FA04", "tenant_id": "@TN_OWN", "tenant_source": "form", "billing_party_id": "@BP_CJSF", "effective_from_date": "2026-09-16", "effective_from_at": "2026-09-16T00:00:00+07:00", "rate_multiplier": "1.000000", "add_thb_per_trip": "-30.00", "reference_fuel_price_thb": "39.00", "announcement_note": "รอบ 16 ก.ย. (ยกเลิก)", "fuel_band_enabled": true, "fuel_band_baseline_floor": 41, "fuel_band_thb_per_baht": "10.00", "created_by": "@U_WRT_MGR", "created_at": "2026-09-12T10:00:00+07:00", "voided": true, "voided_at": "2026-09-14T16:30:00+07:00", "voided_by": "@U_WRT_ADMIN", "voided_reason": "ประกาศซ้ำรอบ ยกเลิก"},
{"id": "@FA05", "tenant_id": "@TN_OWN", "tenant_source": "form", "billing_party_id": "@BP_SPX", "effective_from_date": "2026-07-01", "effective_from_at": "2026-07-01T00:00:00+07:00", "rate_multiplier": "1.100000", "add_thb_per_trip": "50.00", "announcement_note": "golden vector billingCompute.test.ts:279 (1000 x 1.1 + 50)", "created_by": "@U_WRT_MGR", "created_at": "2026-06-28T10:00:00+07:00", "voided": false},
{"id": "@FA06", "tenant_id": "@TN_OWN", "tenant_source": "form", "billing_party_id": "@BP_SPK", "effective_from_date": "2026-07-01", "effective_from_at": "2026-07-01T00:00:00+07:00", "rate_multiplier": "1.000000", "add_thb_per_trip": "0.00", "announcement_note": "ไม่ปรับราคาน้ำมันรอบนี้", "created_by": "@U_WRT_MGR", "created_at": "2026-06-28T10:00:00+07:00", "voided": false}
]
```

#### `customer_service_fees` (3)

```json
[
{"id": "@SF01", "tenant_id": "@TN_OWN", "tenant_source": "form", "billing_party_id": "@BP_SPK", "fee_type": "extra_stop", "amount_thb": "300.00", "unit": "per_stop", "note": "ค่าโยกจุดที่ 2 ขึ้นไป", "created_at": "2026-06-25T09:40:00+07:00"},
{"id": "@SF02", "tenant_id": "@TN_OWN", "tenant_source": "form", "billing_party_id": "@BP_TTP", "fee_type": "standby", "amount_thb": "500.00", "unit": "per_trip", "created_at": "2026-06-25T09:40:00+07:00"},
{"id": "@SF03", "tenant_id": "@TN_OWN", "tenant_source": "form", "billing_party_id": "@BP_CJSF", "fee_type": "waiting_time", "amount_thb": "200.00", "unit": "per_trip", "note": "ไม่ถูกใช้โดย engine; มีไว้ยืนยันว่า fee_type อื่นไม่รั่วเข้า multi-drop", "created_at": "2026-06-25T09:40:00+07:00"}
]
```

#### `standby_rate_entries` (3)

```json
[
{"id": "@SR01", "tenant_id": "@TN_OWN", "tenant_source": "form", "billing_party_id": "@BP_CJSF", "rate_thb": "800.00", "effective_from_date": "2026-07-01", "effective_from_at": "2026-07-01T00:00:00+07:00", "created_at": "2026-06-25T09:45:00+07:00"},
{"id": "@SR02", "tenant_id": "@TN_OWN", "tenant_source": "form", "billing_party_id": "@BP_CJSF", "rate_thb": "850.00", "effective_from_date": "2026-09-01", "effective_from_at": "2026-09-01T00:00:00+07:00", "created_at": "2026-08-28T10:05:00+07:00"},
{"id": "@SR03", "tenant_id": "@TN_OWN", "tenant_source": "form", "billing_party_id": "@BP_TTP", "rate_thb": "700.00", "effective_from_date": "2026-07-01", "effective_from_at": "2026-07-01T00:00:00+07:00", "note": "ยกเลิก: TTP คิด standby เป็นค่าบริการแทน", "created_at": "2026-06-25T09:45:00+07:00", "voided_at": "2026-08-01T10:00:00+07:00", "voided_by": "@U_WRT_ADMIN"}
]
```

#### `billing_statements` (4)

```json
[
{"id": "@ST1", "tenant_id": "@TN_OWN", "tenant_source": "form", "billing_party_id": "@BP_CJSF", "invoice_number": "CJSF-202607-001", "customer_name_snapshot": "ลูกค้าทดสอบ CJSF", "customer_code_snapshot": "CJSF", "period_year": 2026, "period_month": 7, "total_amount": "1930.00", "withholding_tax_rate": "0.0100", "withholding_tax": "19.30", "net_amount": "1910.70", "trip_count": 2, "trip_only_count": 1, "trip_subtotal": "1130.00", "standby_count": 1, "standby_subtotal": "800.00", "multi_drop_count": 0, "multi_drop_subtotal": "0.00", "status": "paid", "generated_at": "2026-08-02T10:00:00+07:00", "sent_at": "2026-08-03T09:00:00+07:00", "paid_at": "2026-08-25T14:00:00+07:00", "due_date": "2026-09-01T10:00:00+07:00", "generated_by": "@U_WRT_MGR"},
{"id": "@ST2", "legacy_doc_id": "seedStmtDoc00000ST02", "tenant_id": "@TN_OWN", "tenant_source": "form", "billing_party_id": "@BP_SPX", "invoice_number": "SPX-202607-001", "customer_name_snapshot": "ลูกค้าทดสอบ SPX", "customer_code_snapshot": "SPX", "period_year": 2026, "period_month": 7, "total_amount": "2400.00", "withholding_tax_rate": "0.0100", "withholding_tax": "24.00", "net_amount": "2376.00", "trip_count": 2, "trip_only_count": 2, "trip_subtotal": "2400.00", "standby_count": 0, "standby_subtotal": "0.00", "multi_drop_count": 0, "multi_drop_subtotal": "0.00", "status": "sent", "generated_at": "2026-08-02T11:00:00+07:00", "sent_at": "2026-08-04T09:00:00+07:00", "due_date": "2026-08-17T11:00:00+07:00", "generated_by": "@U_WRT_MGR"},
{"id": "@ST3", "tenant_id": "@TN_OWN", "tenant_source": "form", "billing_party_id": "@BP_CJSF", "invoice_number": "CJSF-202608-001", "customer_name_snapshot": "ลูกค้าทดสอบ CJSF", "customer_code_snapshot": "CJSF", "period_year": 2026, "period_month": 8, "total_amount": "6350.00", "withholding_tax_rate": "0.0100", "withholding_tax": "63.50", "net_amount": "6286.50", "trip_count": 5, "trip_only_count": 3, "trip_subtotal": "4100.00", "standby_count": 0, "standby_subtotal": "0.00", "multi_drop_count": 2, "multi_drop_subtotal": "2250.00", "status": "draft", "generated_at": "2026-09-02T10:00:00+07:00", "due_date": "2026-10-02T10:00:00+07:00", "generated_by": "@U_WRT_MGR"},
{"id": "@ST4", "tenant_id": "@TN_OWN", "tenant_source": "form", "billing_party_id": "@BP_TTP", "invoice_number": "TTP-202608-001", "customer_name_snapshot": "ลูกค้าทดสอบ TTP", "customer_code_snapshot": "TTP", "period_year": 2026, "period_month": 8, "total_amount": "2600.00", "withholding_tax_rate": "0.0100", "withholding_tax": "26.00", "net_amount": "2574.00", "trip_count": 2, "trip_only_count": 1, "trip_subtotal": "2100.00", "standby_count": 1, "standby_subtotal": "500.00", "multi_drop_count": 0, "multi_drop_subtotal": "0.00", "status": "cancelled", "generated_at": "2026-09-02T11:00:00+07:00", "note": "ออกซ้ำ ยกเลิกเพื่อออกใหม่", "due_date": "2026-10-02T11:00:00+07:00", "generated_by": "@U_WRT_MGR"}
]
```

#### `billing_statement_lines` (9)

```json
[
{"id": "@SL1_1", "statement_id": "@ST1", "row_type": "trip", "trip_id": "@TR01", "amount_thb": "1130.00"},
{"id": "@SL1_2", "statement_id": "@ST1", "row_type": "standby", "standby_id": "@SB01", "amount_thb": "800.00"},
{"id": "@SL3_1", "statement_id": "@ST3", "row_type": "trip", "trip_id": "@TR04", "amount_thb": "1150.00"},
{"id": "@SL3_2", "statement_id": "@ST3", "row_type": "multidrop_stop", "trip_id": "@TR05", "stop_index": 1, "amount_thb": "1150.00"},
{"id": "@SL3_3", "statement_id": "@ST3", "row_type": "multidrop_stop", "trip_id": "@TR05", "stop_index": 2, "amount_thb": "1100.00"},
{"id": "@SL3_4", "statement_id": "@ST3", "row_type": "trip", "trip_id": "@TR06", "amount_thb": "1150.00"},
{"id": "@SL3_5", "statement_id": "@ST3", "row_type": "trip", "trip_id": "@TR21", "amount_thb": "1800.00"},
{"id": "@SL4_1", "statement_id": "@ST4", "row_type": "trip", "trip_id": "@TR08", "amount_thb": "2100.00"},
{"id": "@SL4_2", "statement_id": "@ST4", "row_type": "standby", "standby_id": "@SB03", "amount_thb": "500.00"}
]
```

#### `billing_counters` (4)

```json
[
{"billing_party_id": "@BP_CJSF", "period_year": 2026, "period_month": 7, "tenant_id": "@TN_OWN", "tenant_source": "form", "last_seq": 1, "customer_code": "CJSF", "updated_at": "2026-08-02T10:00:00+07:00"},
{"billing_party_id": "@BP_SPX", "period_year": 2026, "period_month": 7, "tenant_id": "@TN_OWN", "tenant_source": "form", "last_seq": 1, "customer_code": "SPX", "updated_at": "2026-08-02T11:00:00+07:00"},
{"billing_party_id": "@BP_CJSF", "period_year": 2026, "period_month": 8, "tenant_id": "@TN_OWN", "tenant_source": "form", "last_seq": 1, "customer_code": "CJSF", "updated_at": "2026-09-02T10:00:00+07:00"},
{"billing_party_id": "@BP_TTP", "period_year": 2026, "period_month": 8, "tenant_id": "@TN_OWN", "tenant_source": "form", "last_seq": 1, "customer_code": "TTP", "updated_at": "2026-09-02T11:00:00+07:00"}
]
```

#### `statement_documents` (2)

```json
[
{"id": "@SD1_INV", "statement_id": "@ST1", "kind": "invoice_summary_pdf", "file_id": "@FO_ST1_INV", "status": "ready", "rendered_at": "2026-08-02T10:00:00+07:00", "created_at": "2026-08-02T10:00:00+07:00"},
{"id": "@SD1_RCPT", "statement_id": "@ST1", "kind": "receipt_pdf", "file_id": "@FO_ST1_RCPT", "status": "ready", "rendered_at": "2026-08-25T14:00:00+07:00", "created_at": "2026-08-25T14:00:00+07:00"}
]
```

### D.4.10 Finance and HR

#### `vehicle_expenses` (4)

```json
[
{"id": "@EX01", "tenant_id": "@TN_OWN", "tenant_source": "driver", "driver_id": "@D1", "truck_id": "@T1", "truck_license_plate_snapshot": "1ขค-1234", "expense_type": "fuel", "expense_at": "2026-09-18T07:40:00+07:00", "_expense_date": "2026-09-18", "amount_thb": "1696.70", "status": "approved", "volume_liters": "45.500", "price_per_liter": "37.290", "odometer_km": 128450, "tax_inv_id": "SEED-TAXINV-0001", "receipt_file_id": "@FO_EX01_R", "client_op_id": "8287a655-f546-5949-b64e-43764da439e5", "created_by_user_id": "@U_D1", "created_at": "2026-09-18T07:41:00+07:00"},
{"id": "@EX02", "tenant_id": "@TN_OWN", "tenant_source": "driver", "driver_id": "@D1", "truck_id": "@T1", "truck_license_plate_snapshot": "1ขค-1234", "expense_type": "fuel", "expense_at": "2026-09-28T18:10:00+07:00", "_expense_date": "2026-09-28", "amount_thb": "1957.80", "status": "pending", "volume_liters": "52.000", "price_per_liter": "37.650", "odometer_km": 130120, "tax_inv_id": "SEED-TAXINV-0002", "client_op_id": "26813846-b6f9-5407-acf1-d7dd3709c904", "created_by_user_id": "@U_D1", "created_at": "2026-09-28T18:10:00+07:00", "_effect": "odometer 130120 >= trucks.next_service_mileage 130000 -> MT03 pm_booking"},
{"id": "@EX03", "tenant_id": "@TN_OWN", "tenant_source": "driver", "driver_id": "@D1", "truck_id": "@T3", "truck_license_plate_snapshot": "3ฒท-9012", "expense_type": "other", "category": "toll", "expense_at": "2026-08-27T09:12:00+07:00", "_expense_date": "2026-08-27", "amount_thb": "75.00", "status": "approved", "toll_import_sequence": 1, "toll_location": "ด่านตัวอย่าง A", "toll_lane": "07", "toll_source_type": "ผ่านทาง", "created_by_user_id": "@U_WRT_OPS", "created_at": "2026-09-01T10:00:00+07:00"},
{"id": "@EX04", "tenant_id": "@TN_OWN", "tenant_source": "driver", "driver_id": "@D2", "truck_id": "@T2", "truck_license_plate_snapshot": "2ขค-5678", "expense_type": "other", "category": "parking", "expense_at": "2026-09-12T19:30:00+07:00", "_expense_date": "2026-09-12", "amount_thb": "40.00", "status": "rejected", "admin_note": "ไม่มีใบเสร็จ", "client_op_id": "586f4f0e-e40b-5424-b593-399e9965b56b", "created_by_user_id": "@U_D2", "created_at": "2026-09-12T19:31:00+07:00"}
]
```

#### `maintenance_records` (3)

```json
[
{"id": "@MT01", "tenant_id": "@TN_OWN", "tenant_source": "truck", "truck_id": "@T2", "truck_license_plate_snapshot": "2ขค-5678", "truck_brand_snapshot": "Isuzu", "maintenance_type": "PM", "service_type": "เปลี่ยนถ่ายน้ำมันเครื่องและไส้กรอง", "start_date": "2026-08-05", "end_date": "2026-08-06", "status": "completed", "cost_labor_thb": "1200.00", "cost_parts_thb": "3650.00", "total_cost_thb": "4850.00", "provider": "อู่ทดสอบบางนา", "payment_method": "transfer", "current_mileage": 80150, "next_service_mileage": 90000, "invoice_amount_thb": "4850.00", "driver_submitted": true, "check_in_at": "2026-08-05T08:30:00+07:00", "check_out_at": "2026-08-06T16:00:00+07:00", "created_by": "@U_WRT_OPS", "created_at": "2026-08-01T09:00:00+07:00"},
{"id": "@MT02", "tenant_id": "@TN_OWN", "tenant_source": "truck", "truck_id": "@T3", "truck_license_plate_snapshot": "3ฒท-9012", "maintenance_type": "CM", "service_type": "ไฟหน้าซ้ายไม่ติด", "start_date": "2026-09-02", "status": "cancelled", "notes": "คนขับเปลี่ยนหลอดเอง", "created_by": "@U_WRT_OPS", "created_at": "2026-09-01T11:00:00+07:00"},
{"id": "@MT03", "tenant_id": "@TN_OWN", "tenant_source": "truck", "truck_id": "@T1", "truck_license_plate_snapshot": "1ขค-1234", "maintenance_type": "PM", "service_type": "เช็คระยะ 130,000 กม.", "start_date": "2026-10-02", "status": "pm_booking", "current_mileage": 130120, "next_service_mileage": 140000, "created_at": "2026-09-28T18:11:00+07:00"}
]
```

#### `driver_compensation_configs` (2)

```json
[
{"id": "@CC1", "tenant_id": "@TN_OWN", "tenant_source": "form", "effective_from_at": "2026-01-01T00:00:00+07:00", "_effective_from_date": "2026-01-01", "weekday_rate_thb": "300.00", "holiday_rate_thb": "350.00", "pay_standby": false, "helper_day_rate_thb": "400.00", "fuel_incentive_tiers": [{"minKmPerLitre": 10, "amountThb": 1000}, {"minKmPerLitre": 11, "amountThb": 1100}, {"minKmPerLitre": 12, "amountThb": 1200}, {"minKmPerLitre": 13, "amountThb": 1400}, {"minKmPerLitre": 14, "amountThb": 1800}], "fuel_min_refuels_per_month": 5, "trip_volume_tiers": [{"minTrips": 50, "amountThb": 1000}, {"minTrips": 60, "amountThb": 1500}, {"minTrips": 70, "amountThb": 2000}], "sso_rate_percent": "5.00", "sso_base_existing_thb": "15000.00", "sso_base_new_thb": "12000.00", "sso_existing_hired_before_year": 2026, "sso_max_age_inclusive": 55, "sso_probation_months": 3, "created_by": "@U_WRT_ADMIN", "created_at": "2026-01-01T00:00:00+07:00"},
{"id": "@CC2", "tenant_id": "@TN_OWN", "tenant_source": "form", "effective_from_at": "2026-09-01T00:00:00+07:00", "_effective_from_date": "2026-09-01", "weekday_rate_thb": "320.00", "holiday_rate_thb": "350.00", "pay_standby": false, "helper_day_rate_thb": "400.00", "fuel_incentive_tiers": [{"minKmPerLitre": 10, "amountThb": 1000}, {"minKmPerLitre": 11, "amountThb": 1100}, {"minKmPerLitre": 12, "amountThb": 1200}, {"minKmPerLitre": 13, "amountThb": 1400}, {"minKmPerLitre": 14, "amountThb": 1800}], "fuel_min_refuels_per_month": 5, "trip_volume_tiers": [{"minTrips": 50, "amountThb": 1000}, {"minTrips": 60, "amountThb": 1500}, {"minTrips": 70, "amountThb": 2000}], "sso_rate_percent": "5.00", "sso_base_existing_thb": "15000.00", "sso_base_new_thb": "12000.00", "sso_existing_hired_before_year": 2026, "sso_max_age_inclusive": 55, "sso_probation_months": 3, "created_by": "@U_WRT_ADMIN", "created_at": "2026-09-01T00:00:00+07:00"}
]
```

#### `penalty_types` (2)

```json
[
{"config_id": "@CC1", "code": "LATE_SLA", "name_en": "Late against SLA", "name_th": "ส่งงานล่าช้ากว่า SLA", "default_amount_thb": "3000.00"},
{"config_id": "@CC2", "code": "LATE_SLA", "name_en": "Late against SLA", "name_th": "ส่งงานล่าช้ากว่า SLA", "default_amount_thb": "3000.00"}
]
```

#### `driver_penalties` (4)

```json
[
{"id": "@PN01", "tenant_id": "@TN_OWN", "tenant_source": "driver", "driver_id": "@D1", "type_code": "LATE_SLA", "type_name_snapshot": "ส่งงานล่าช้ากว่า SLA", "total_thb": "600.00", "remaining_thb": "400.00", "installments_total": 3, "installments_paid": 1, "reason": "ส่งปลายทางช้า 3 ชม. (ลดจากอัตรามาตรฐาน)", "status": "partially_deducted", "incurred_at": "2026-08-21T09:00:00+07:00", "created_by": "@U_WRT_MGR", "created_at": "2026-08-21T09:00:00+07:00", "updated_at": "2026-09-02T10:00:00+07:00"},
{"id": "@PN02", "tenant_id": "@TN_OWN", "tenant_source": "driver", "driver_id": "@D2", "type_code": "LATE_SLA", "type_name_snapshot": "ส่งงานล่าช้ากว่า SLA", "total_thb": "3000.00", "remaining_thb": "3000.00", "installments_total": 1, "installments_paid": 0, "reason": "ส่งปลายทางช้าเกิน SLA", "status": "pending", "incurred_at": "2026-08-28T10:00:00+07:00", "created_by": "@U_WRT_MGR", "created_at": "2026-08-28T10:00:00+07:00"},
{"id": "@PN03", "legacy_doc_id": "seedPenDoc000000PN03", "tenant_id": "@TN_OWN", "tenant_source": "driver", "driver_id": "@D1", "type_code": "LATE_SLA", "type_name_snapshot": "ส่งงานล่าช้ากว่า SLA", "total_thb": "300.00", "remaining_thb": "0.00", "installments_total": 1, "installments_paid": 1, "reason": "legacy (ETL) หักครบแล้ว", "status": "cleared", "incurred_at": "2026-06-10T09:00:00+07:00", "created_by": "@U_WRT_MGR", "created_at": "2026-06-10T09:00:00+07:00"},
{"id": "@PN04", "tenant_id": "@TN_OWN", "tenant_source": "driver", "driver_id": "@D3", "type_code": "LATE_SLA", "type_name_snapshot": "ส่งงานล่าช้ากว่า SLA", "total_thb": "1000.00", "remaining_thb": "1000.00", "installments_total": 2, "installments_paid": 0, "reason": "ยกเลิกหลังตรวจสอบ GPS", "status": "cancelled", "incurred_at": "2026-07-30T09:00:00+07:00", "created_by": "@U_WRT_MGR", "created_at": "2026-07-30T09:00:00+07:00"}
]
```

#### `payroll_runs` (3)

```json
[
{"id": "@PR01", "tenant_id": "@TN_OWN", "tenant_source": "driver", "driver_id": "@D1", "pay_period": "2026-08", "pay_round": "R1", "period_start": "2026-08-01T00:00:00+07:00", "period_end": "2026-08-16T00:00:00+07:00", "status": "paid", "total_earnings_thb": "650.00", "total_deductions_thb": "0.00", "net_pay_thb": "650.00", "currency": "THB", "payment_date": "2026-08-20T10:00:00+07:00", "payment_method": "transfer", "approved_by": "@U_WRT_ADMIN", "approved_at": "2026-08-18T10:00:00+07:00", "ledger_transaction_id": "@TX01", "created_at": "2026-08-17T09:00:00+07:00"},
{"id": "@PR02", "tenant_id": "@TN_OWN", "tenant_source": "driver", "driver_id": "@D1", "pay_period": "2026-08", "pay_round": "R2", "period_start": "2026-08-16T00:00:00+07:00", "period_end": "2026-09-01T00:00:00+07:00", "status": "approved", "total_earnings_thb": "1050.00", "total_deductions_thb": "950.00", "net_pay_thb": "100.00", "currency": "THB", "approved_by": "@U_WRT_ADMIN", "approved_at": "2026-09-02T10:00:00+07:00", "ledger_transaction_id": "@TX02", "created_at": "2026-09-01T09:00:00+07:00"},
{"id": "@PR03", "tenant_id": "@TN_OWN", "tenant_source": "driver", "driver_id": "@D2", "pay_period": "2026-08", "pay_round": "R2", "period_start": "2026-08-16T00:00:00+07:00", "period_end": "2026-09-01T00:00:00+07:00", "status": "draft", "total_earnings_thb": "0.00", "total_deductions_thb": "0.00", "net_pay_thb": "0.00", "currency": "THB", "created_at": "2026-09-01T09:00:00+07:00"}
]
```

#### `payroll_line_items` (6)

```json
[
{"id": "@PL01_1", "payroll_run_id": "@PR01", "line_no": 1, "item_type": "earning", "category": "TRIP_COMMISSION", "name": "Trip pay", "amount_thb": "650.00", "meta": {"weekdayTrips": ["@TR07"], "holidayTrips": ["@TR08"], "rates": {"weekday": 300, "holiday": 350}}},
{"id": "@PL02_1", "payroll_run_id": "@PR02", "line_no": 1, "item_type": "earning", "category": "TRIP_COMMISSION", "name": "Trip pay", "amount_thb": "650.00", "meta": {"weekdayTrips": ["@TR21"], "holidayTrips": ["@TR04"], "excludedMultiStop": ["@TR05"]}},
{"id": "@PL02_2", "payroll_run_id": "@PR02", "line_no": 2, "item_type": "earning", "category": "HELPER_PAY", "name": "Helper/training pay", "amount_thb": "400.00", "quantity": "1.000", "unit_rate_thb": "400.00", "meta": {"helperDayKeys": ["2026-08-25"], "taskIds": ["@TK16"]}},
{"id": "@PL02_3", "payroll_run_id": "@PR02", "line_no": 3, "item_type": "deduction", "category": "SOCIAL_SECURITY", "name": "Social security", "amount_thb": "750.00", "meta": {"baseThb": 15000, "ratePercent": 5}},
{"id": "@PL02_4", "payroll_run_id": "@PR02", "line_no": 4, "item_type": "deduction", "category": "PENALTY", "name": "ส่งงานล่าช้ากว่า SLA", "amount_thb": "200.00", "reference_id": "@PN01"},
{"id": "@PL03_1", "payroll_run_id": "@PR03", "line_no": 1, "item_type": "earning", "category": "TRIP_COMMISSION", "name": "Trip pay", "amount_thb": "0.00", "meta": {"excludedMultiStop": ["@TR16"]}}
]
```

#### `payroll_penalty_applications` (1)

```json
[
{"payroll_run_id": "@PR02", "penalty_id": "@PN01", "applied_thb": "200.00"}
]
```

#### `transactions` (2)

```json
[
{"id": "@TX01", "tenant_id": "@TN_OWN", "tenant_source": "form", "tx_type": "driver_payout", "sub_type": "Driver Compensation", "amount_thb": "650.00", "payment_method": "transfer", "tx_date": "2026-08-18", "tx_date_source": "bangkok", "driver_id": "@D1", "payroll_run_id": "@PR01", "pay_period": "2026-08", "pay_round": "R1", "performed_by_user_id": "@U_WRT_ADMIN", "created_at": "2026-08-18T10:00:00+07:00"},
{"id": "@TX02", "tenant_id": "@TN_OWN", "tenant_source": "form", "tx_type": "driver_payout", "sub_type": "Driver Compensation", "amount_thb": "100.00", "payment_method": "transfer", "tx_date": "2026-09-02", "tx_date_source": "bangkok", "driver_id": "@D1", "payroll_run_id": "@PR02", "pay_period": "2026-08", "pay_round": "R2", "performed_by_user_id": "@U_WRT_ADMIN", "created_at": "2026-09-02T10:00:00+07:00"}
]
```

#### `driver_advances` (1)

```json
[
{"id": "@DA01", "tenant_id": "@TN_OWN", "tenant_source": "driver", "driver_id": "@D1", "amount_thb": "2000.00", "withdrawn_at": "2026-09-08T12:00:00+07:00", "deduct_period": "2026-09", "deduct_round": "R2", "status": "pending", "reason": "เบิกล่วงหน้าค่าซ่อมยางระหว่างทาง", "created_by": "@U_WRT_MGR", "created_at": "2026-09-08T12:05:00+07:00"}
]
```

### D.4.11 Comms, leave and holidays

#### `chats` (2)

```json
[
{"id": "@CH01", "tenant_id": "@TN_OWN", "tenant_source": "driver", "driver_id": "@D2", "status": "open", "priority": "urgent", "last_message_preview": "[รูปภาพ]", "last_message_at": "2026-09-29T10:09:00+07:00", "last_message_by_user_id": "@U_D2", "last_message_type": "normal", "created_at": "2026-09-29T10:08:00+07:00"},
{"id": "@CH02", "tenant_id": "@TN_OWN", "tenant_source": "driver", "driver_id": "@D1", "status": "closed", "assigned_admin_user_id": "@U_WRT_OPS", "assigned_at": "2026-09-18T08:10:00+07:00", "closed_at": "2026-09-18T09:00:00+07:00", "priority": "normal", "last_message_preview": "รับทราบ เปลี่ยนเส้นทางได้ครับ", "last_message_at": "2026-09-18T08:20:00+07:00", "last_message_by_user_id": "@U_WRT_OPS", "last_message_type": "normal", "created_at": "2026-09-18T08:00:00+07:00"}
]
```

#### `chat_messages` (3)

```json
[
{"id": "@CM1", "chat_id": "@CH01", "sender_user_id": "@U_D2", "sender_role": "driver", "text": "ยางหลังซ้ายแตกช่วงบางนา ขอความช่วยเหลือ", "message_type": "normal", "client_message_id": "39c7b7db-3b71-5ef7-b5af-853bd09f5585", "created_at": "2026-09-29T10:08:00+07:00"},
{"id": "@CM2", "chat_id": "@CH01", "sender_user_id": "@U_D2", "sender_role": "driver", "image_file_id": "@FO_CH01_IMG", "message_type": "normal", "client_message_id": "829f0111-1ef7-5fdf-a068-c72129e641d8", "created_at": "2026-09-29T10:09:00+07:00"},
{"id": "@CM3", "chat_id": "@CH02", "sender_user_id": "@U_WRT_OPS", "sender_role": "admin", "text": "รับทราบ เปลี่ยนเส้นทางได้ครับ", "message_type": "normal", "client_message_id": "9ad4a825-c451-51d2-a939-ff8e3debd586", "created_at": "2026-09-18T08:20:00+07:00"}
]
```

#### `broadcasts` (2)

```json
[
{"id": "@BC01", "tenant_id": "@TN_OWN", "created_by_user_id": "@U_WRT_MGR", "created_by_name": "ฝ่ายปฏิบัติการ", "title": "วันหยุด 12 ส.ค.", "message_text": "วันที่ 12 ส.ค. เป็นวันหยุด คิดค่าเที่ยวอัตราวันหยุด", "recipient_group": "all_driver", "recipient_count": 3, "sent_at": "2026-08-10T09:00:00+07:00"},
{"id": "@BC02", "tenant_id": "@TN_OWN", "created_by_user_id": "@U_WRT_MGR", "created_by_name": "ฝ่ายปฏิบัติการ", "title": "ทดสอบ", "message_text": "ข้อความส่งผิดกลุ่ม", "recipient_group": "all_driver", "recipient_count": 3, "sent_at": "2026-09-01T09:00:00+07:00", "voided_at": "2026-09-01T09:05:00+07:00"}
]
```

#### `broadcast_reads` (2)

```json
[
{"broadcast_id": "@BC01", "user_id": "@U_D1", "read_at": "2026-08-10T09:12:00+07:00"},
{"broadcast_id": "@BC01", "user_id": "@U_D2", "read_at": "2026-08-10T12:30:00+07:00"}
]
```

#### `leave_requests` (3)

```json
[
{"id": "@LV01", "tenant_id": "@TN_OWN", "tenant_source": "driver", "driver_id": "@D1", "leave_type": "sick", "status": "approved", "start_date": "2026-09-14", "end_date": "2026-09-15", "reason": "ไข้หวัด", "approver_user_id": "@U_WRT_MGR", "decided_at": "2026-09-13T17:00:00+07:00", "client_op_id": "fa948f1f-c859-5f3e-9882-814b53d6ddec", "created_at": "2026-09-13T08:00:00+07:00"},
{"id": "@LV02", "tenant_id": "@TN_OWN", "tenant_source": "driver", "driver_id": "@D2", "leave_type": "business", "status": "pending", "start_date": "2026-10-05", "end_date": "2026-10-06", "reason": "ติดต่อราชการ", "client_op_id": "9871df56-5ad1-574c-ab0b-fed94f900eb4", "created_at": "2026-09-29T20:00:00+07:00"},
{"id": "@LV03", "tenant_id": "@TN_OWN", "tenant_source": "driver", "driver_id": "@D3", "leave_type": "sick", "status": "rejected", "start_date": "2026-08-03", "end_date": "2026-08-04", "reason": "ปวดหลัง", "approver_user_id": "@U_WRT_MGR", "decided_at": "2026-08-03T12:00:00+07:00", "rejection_reason": "ไม่มีใบรับรองแพทย์", "client_op_id": "8bf05e3a-3ad2-5515-9bbc-27ce3690175d", "created_at": "2026-08-03T07:00:00+07:00"}
]
```

#### `holidays` (4)

```json
[
{"id": "@HO1", "holiday_date": "2026-07-28", "holiday_type": "public", "status": "published", "name": "วันเฉลิมพระชนมพรรษา", "name_en": "King's Birthday", "name_th": "วันเฉลิมพระชนมพรรษา", "is_recurring": true, "created_by": "@U_WRT_ADMIN", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@HO2", "holiday_date": "2026-08-12", "holiday_type": "public", "status": "published", "name": "วันแม่แห่งชาติ", "name_en": "Mother's Day", "name_th": "วันแม่แห่งชาติ", "is_recurring": true, "created_by": "@U_WRT_ADMIN", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@HO3", "holiday_date": "2026-10-13", "holiday_type": "public", "status": "published", "name": "วันนวมินทรมหาราช", "name_en": "King Bhumibol Memorial Day", "name_th": "วันนวมินทรมหาราช", "is_recurring": true, "created_by": "@U_WRT_ADMIN", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@HO4", "tenant_id": "@TN_OWN", "holiday_date": "2026-12-30", "holiday_type": "company", "status": "draft", "name": "วันหยุดบริษัท (ร่าง)", "is_recurring": false, "created_by": "@U_WRT_ADMIN", "created_at": "2026-09-20T10:00:00+07:00"}
]
```

### D.4.12 Platform and infrastructure

#### `mobile_installations` (4)

```json
[
{"driver_id": "@D1", "install_id": "fSEEDinstall0000000D01", "tenant_id": "@TN_OWN", "platform": "android", "app_version": "3.5.0", "build_number": "1", "flavor": "prod", "first_seen_at": "2026-09-11T06:00:00+07:00", "last_seen_at": "2026-09-30T06:14:00+07:00", "_status": "current"},
{"driver_id": "@D2", "install_id": "fSEEDinstall0000000D02", "tenant_id": "@TN_OWN", "platform": "android", "app_version": "3.4.1", "build_number": "1", "flavor": "prod", "first_seen_at": "2026-08-20T06:00:00+07:00", "last_seen_at": "2026-09-29T19:02:00+07:00", "_status": "outdated"},
{"driver_id": "@D7", "install_id": "fSEEDinstall0000000D07", "tenant_id": "@TN_TTP", "platform": "android", "app_version": "3.3.2", "build_number": "2", "flavor": "prod", "first_seen_at": "2026-07-01T06:00:00+07:00", "last_seen_at": "2026-09-30T09:05:00+07:00", "_status": "blocked"},
{"driver_id": "@D6", "install_id": "fSEEDinstall0000000D06", "tenant_id": "@TN_TTP", "platform": "android", "app_version": "3.6.0", "build_number": "1", "flavor": "dev", "first_seen_at": "2026-09-20T18:00:00+07:00", "last_seen_at": "2026-09-20T18:00:00+07:00", "_status": "ahead (dev flavor badge)"}
]
```

#### `mobile_app_releases` (1)

```json
[
{"id": "@MR01", "flavor": "prod", "version": "3.5.0", "build_number": "1", "apk_file_id": "@FO_APK", "apk_size_bytes": 1048576, "apk_sha256": "30e14955ebf1352266dc2ff8067e68104607e750abb9d3b36582b8af909fcb58", "release_notes": "seed placeholder APK (1 MiB of 0x00)", "released_at": "2026-09-10T18:00:00+07:00", "released_by": "platform.admin@logitrack.test"}
]
```

#### `device_tokens` (3)

```json
[
{"user_id": "@U_D1", "install_id": "fSEEDinstall0000000D01", "driver_id": "@D1", "token": "seed-fcm-token-d1", "platform": "android", "app_flavor": "prod", "last_seen_at": "2026-09-30T06:14:00+07:00", "created_at": "2026-09-11T06:00:00+07:00"},
{"user_id": "@U_D2", "install_id": "fSEEDinstall0000000D02", "driver_id": "@D2", "token": "seed-fcm-token-d2", "platform": "android", "app_flavor": "prod", "last_seen_at": "2026-09-29T19:02:00+07:00", "created_at": "2026-08-20T06:00:00+07:00"},
{"user_id": "@U_D7", "install_id": "fSEEDinstall0000000D07", "driver_id": "@D7", "token": "seed-fcm-token-invalid-d7", "platform": "android", "app_flavor": "prod", "last_seen_at": "2026-09-30T09:05:00+07:00", "created_at": "2026-07-01T06:00:00+07:00", "_effect": "FCM mock answers UNREGISTERED -> notify.fcm deletes the row"}
]
```

#### `settings` (2)

```json
[
{"key": "mobile_app", "value": {"minAllowedVersion": "3.4.0", "minAllowedVersionSetAt": "2026-09-15T10:00:00+07:00", "minAllowedVersionSetBy": "@U_PLAT", "latestVersion": "3.5.0", "latestBuildNumber": "1", "apkDownloadUrl": "$seed:S3_PUBLIC_BASE_URL + /app_releases/prod/logitrack-prod-v3.5.0.apk", "flavor": "prod", "releasedAt": "2026-09-10T18:00:00+07:00"}, "updated_at": "2026-09-15T10:00:00+07:00", "updated_by": "@U_PLAT"},
{"key": "distances_last_calculated", "value": {"at": "2026-07-01T03:00:00+07:00", "pairs": 2}, "updated_at": "2026-07-01T03:00:00+07:00", "updated_by": "@U_WRT_OPS"}
]
```

#### `file_objects` (16)

```json
[
{"id": "@FO_LOGO", "bucket": "logitrack", "object_key": "companies/@CO_WRT/logo-1767578400000.png", "content_type": "image/png", "size_bytes": "$seed:generated", "sha256": "$seed:generated", "visibility": "private", "purpose": "company_logo", "owner_kind": "company", "owner_id": "@CO_WRT", "tenant_id": "@TN_OWN", "status": "committed", "uploaded_by": "@U_WRT_ADMIN", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@FO_STAMP", "bucket": "logitrack", "object_key": "companies/@CO_WRT/stamp-1767578400000.png", "content_type": "image/png", "size_bytes": "$seed:generated", "sha256": "$seed:generated", "visibility": "private", "purpose": "company_stamp", "owner_kind": "company", "owner_id": "@CO_WRT", "tenant_id": "@TN_OWN", "status": "committed", "uploaded_by": "@U_WRT_ADMIN", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@FO_SIG", "bucket": "logitrack", "object_key": "companies/@CO_WRT/signature-1767578400000.png", "content_type": "image/png", "size_bytes": "$seed:generated", "sha256": "$seed:generated", "visibility": "private", "purpose": "company_signature", "owner_kind": "company", "owner_id": "@CO_WRT", "tenant_id": "@TN_OWN", "status": "committed", "uploaded_by": "@U_WRT_ADMIN", "created_at": "2026-01-05T09:00:00+07:00"},
{"id": "@FO_TR01_SEAL", "bucket": "logitrack", "object_key": "trips/@TR01/seal-1783651800000.jpg", "content_type": "image/jpeg", "size_bytes": "$seed:generated", "sha256": "$seed:generated", "visibility": "private", "purpose": "trip_photo", "owner_kind": "trip", "owner_id": "@TR01", "tenant_id": "@TN_OWN", "status": "committed", "uploaded_by": "@U_D1", "created_at": "2026-07-10T09:50:00+07:00"},
{"id": "@FO_TR03_SEAL", "bucket": "logitrack", "object_key": "trip_records/36601950/seal.jpg", "content_type": "image/jpeg", "size_bytes": "$seed:generated", "sha256": "$seed:generated", "visibility": "private", "purpose": "trip_photo", "owner_kind": "trip", "owner_id": "@TR03", "tenant_id": "@TN_OWN", "status": "committed", "legacy_url": "https://firebasestorage.googleapis.com/v0/b/seed-legacy-bucket/o/trip_records%2F36601950%2Fseal.jpg?alt=media", "created_at": "2026-07-23T07:58:00+07:00"},
{"id": "@FO_TR16_S2", "bucket": "logitrack", "object_key": "trips/@TR16/stop_2_arrived-1787724300000.jpg", "content_type": "image/jpeg", "size_bytes": "$seed:generated", "sha256": "$seed:generated", "visibility": "private", "purpose": "trip_photo", "owner_kind": "trip", "owner_id": "@TR16", "tenant_id": "@TN_OWN", "status": "committed", "uploaded_by": "@U_D2", "created_at": "2026-08-26T13:05:00+07:00"},
{"id": "@FO_TR19_PC", "bucket": "logitrack", "object_key": "trip_records/36601877/pre_close.jpg", "content_type": "image/jpeg", "size_bytes": null, "sha256": null, "visibility": "private", "purpose": "trip_photo", "owner_kind": "trip", "owner_id": "@TR19", "tenant_id": "@TN_OWN", "status": "missing_at_source", "legacy_url": "https://firebasestorage.googleapis.com/v0/b/seed-legacy-bucket/o/trip_records%2F36601877%2Fpre_close.jpg?alt=media", "created_at": "2026-07-05T07:15:00+07:00"},
{"id": "@FO_TK24_CI", "bucket": "logitrack", "object_key": "checkin/@TK24/1790732520000.jpg", "content_type": "image/jpeg", "size_bytes": "$seed:generated", "sha256": "$seed:generated", "visibility": "private", "purpose": "checkin_photo", "owner_kind": "task", "owner_id": "@TK24", "tenant_id": "@TN_TTP", "status": "committed", "uploaded_by": "@U_D7", "created_at": "2026-09-30T08:42:00+07:00"},
{"id": "@FO_TK24_APP", "bucket": "logitrack", "object_key": "checkin/@TK24/app_screenshot_1790732580000.jpg", "content_type": "image/jpeg", "size_bytes": "$seed:generated", "sha256": "$seed:generated", "visibility": "private", "purpose": "checkin_app_screenshot", "owner_kind": "task", "owner_id": "@TK24", "tenant_id": "@TN_TTP", "status": "committed", "uploaded_by": "@U_D7", "created_at": "2026-09-30T08:43:00+07:00"},
{"id": "@FO_IR1_S1", "bucket": "logitrack", "object_key": "incidents/@IR1/situation1-1790651040000.jpg", "content_type": "image/jpeg", "size_bytes": "$seed:generated", "sha256": "$seed:generated", "visibility": "private", "purpose": "incident_photo", "owner_kind": "incident", "owner_id": "@IR1", "tenant_id": "@TN_OWN", "status": "committed", "uploaded_by": "@U_D2", "created_at": "2026-09-29T10:04:00+07:00"},
{"id": "@FO_CH01_IMG", "bucket": "logitrack", "object_key": "chats/@CH01/1790651340000.jpg", "content_type": "image/jpeg", "size_bytes": "$seed:generated", "sha256": "$seed:generated", "visibility": "private", "purpose": "chat_image", "owner_kind": "chat", "owner_id": "@CH01", "tenant_id": "@TN_OWN", "status": "committed", "uploaded_by": "@U_D2", "created_at": "2026-09-29T10:09:00+07:00"},
{"id": "@FO_EX01_R", "bucket": "logitrack", "object_key": "expenses/@EX01/receipt-1789692060000.jpg", "content_type": "image/jpeg", "size_bytes": "$seed:generated", "sha256": "$seed:generated", "visibility": "private", "purpose": "expense_receipt", "owner_kind": "expense", "owner_id": "@EX01", "tenant_id": "@TN_OWN", "status": "committed", "uploaded_by": "@U_D1", "created_at": "2026-09-18T07:41:00+07:00"},
{"id": "@FO_APK", "bucket": "logitrack-public", "object_key": "app_releases/prod/logitrack-prod-v3.5.0.apk", "content_type": "application/vnd.android.package-archive", "size_bytes": 1048576, "sha256": "30e14955ebf1352266dc2ff8067e68104607e750abb9d3b36582b8af909fcb58", "visibility": "public", "purpose": "apk", "owner_kind": "release", "owner_id": "@MR01", "status": "committed", "created_at": "2026-09-10T18:00:00+07:00"},
{"id": "@FO_ST1_INV", "bucket": "logitrack", "object_key": "documents/statements/@ST1/invoice_summary.pdf", "content_type": "application/pdf", "size_bytes": "$seed:generated", "sha256": "$seed:generated", "visibility": "private", "purpose": "statement_document", "owner_kind": "statement", "owner_id": "@ST1", "tenant_id": "@TN_OWN", "status": "committed", "created_at": "2026-08-02T10:00:00+07:00"},
{"id": "@FO_ST1_RCPT", "bucket": "logitrack", "object_key": "documents/statements/@ST1/receipt.pdf", "content_type": "application/pdf", "size_bytes": "$seed:generated", "sha256": "$seed:generated", "visibility": "private", "purpose": "statement_document", "owner_kind": "statement", "owner_id": "@ST1", "tenant_id": "@TN_OWN", "status": "committed", "created_at": "2026-08-25T14:00:00+07:00"},
{"id": "@FO_TR17_PEND", "bucket": "logitrack", "object_key": "trips/@TR17/arrived-1790729700000.jpg", "content_type": "image/jpeg", "size_bytes": "$seed:generated", "sha256": "$seed:generated", "visibility": "private", "purpose": "trip_photo", "owner_kind": "trip", "owner_id": "@TR17", "tenant_id": "@TN_OWN", "status": "pending", "uploaded_by": "@U_D1", "expires_at": "2026-09-30T08:10:00+07:00", "created_at": "2026-09-30T07:55:00+07:00"}
]
```

#### `outbox_events` (3)

```json
[
{"_id": 1, "event_id": "@EV1", "aggregate_type": "trip", "aggregate_id": "@TR14", "event_type": "trip.delivered", "exchange": "lt.events", "routing_key": "trip.delivered", "realtime_topics": ["tenant:@TN_OWN:trips", "dispatch:@BP_CJSF:trips", "dispatch:@BP_SPK:trips"], "tenant_id": "@TN_OWN", "payload": {"tripId": "@TR14", "tripNo": "ZXJB26092000114", "deliveredAt": "2026-09-20T14:50:00+07:00", "billingPartyId": "@BP_CJSF"}, "headers": {"actor": "@U_D1", "occurred_at": "2026-09-20T14:50:00+07:00"}, "created_at": "2026-09-20T14:50:00+07:00", "published_at": "2026-09-20T14:50:00+07:00", "attempts": 1},
{"_id": 2, "event_id": "@EV2", "aggregate_type": "task", "aggregate_id": "@TK23", "event_type": "task.assigned", "exchange": "lt.events", "routing_key": "task.assigned", "realtime_topics": ["tenant:@TN_OWN:tasks", "dispatch:@BP_SPX:tasks", "driver:@D2"], "tenant_id": "@TN_OWN", "payload": {"taskId": "@TK23", "taskNo": "FM-01102026-001", "driverId": "@D2"}, "headers": {"actor": "@U_WRT_OPS", "occurred_at": "2026-09-30T16:00:00+07:00"}, "created_at": "2026-09-30T16:00:00+07:00", "published_at": "2026-09-30T16:00:00+07:00", "attempts": 1},
{"_id": 3, "event_id": "@EV3", "aggregate_type": "statement", "aggregate_id": "@ST3", "event_type": "statement.created", "exchange": "lt.events", "routing_key": "statement.created", "realtime_topics": ["tenant:@TN_OWN:billing"], "tenant_id": "@TN_OWN", "payload": {"billingPartyId": "@BP_CJSF", "statementId": "@ST3", "invoiceNumber": "CJSF-202608-001"}, "headers": {"actor": "@U_WRT_MGR", "occurred_at": "2026-09-02T10:00:00+07:00"}, "created_at": "2026-09-02T10:00:00+07:00", "published_at": "2026-09-02T10:00:00+07:00", "attempts": 1}
]
```

#### `jobs` (2)

```json
[
{"id": "@JB1", "type": "billing.backfill-trips", "status": "succeeded", "owner_user_id": "@U_WRT_ADMIN", "tenant_id": "@TN_OWN", "params": {"billingPartyId": "@BP_CJSF", "from": "2026-07-01", "to": "2026-07-31", "force": true}, "progress": {"done": 1, "total": 1}, "result": {"scanned": 1, "eligible": 1, "written": 0, "skipped": 0, "failed": 0, "blocked": 1, "blockedInvoices": ["CJSF-202607-001"]}, "created_at": "2026-09-25T10:00:00+07:00", "started_at": "2026-09-25T10:00:00+07:00", "finished_at": "2026-09-25T10:00:00+07:00"},
{"id": "@JB2", "type": "tenancy.orphan-scan", "status": "succeeded", "params": {}, "progress": {"done": 1, "total": 1}, "result": {"quarantineRows": {"tasks": 1}, "driverMoved": [{"table": "tasks", "id": "@TK02", "driverId": "@D6", "rowTenant": "@TN_NWR", "driverTenant": "@TN_TTP"}], "truckTenantMismatch": []}, "created_at": "2026-09-30T03:00:00+07:00", "started_at": "2026-09-30T03:00:00+07:00", "finished_at": "2026-09-30T03:00:00+07:00", "_effect": "count > 0 also emits outbox security.event tenant_orphans_detected (warning, Appendix C §C.3.10); that event and its security_events row are not part of the smoke history"}
]
```

#### `security_events` (1)

```json
[
{"id": "@SE1", "created_at": "2026-09-30T11:00:00+07:00", "event_type": "platform_cross_tenant_access", "severity": "warning", "summary": "platform_admin read tenant TTP tasks via X-Act-On-Tenant", "details": {"method": "GET", "path": "/v1/tasks", "act_on_tenant": "@TN_TTP"}, "actor_user_id": "@U_PLAT", "actor_email": "platform.admin@logitrack.test", "tenant_id": "@TN_TTP", "request_id": "seed-req-0001"}
]
```

---

## D.5 ETL fixtures

The seed shows data in its **post-ETL** shape; the ETL fixtures hold the same kinds of cases in their **pre-ETL Firestore** shape so `cmd/etl` (main spec §13) can be tested without touching Firestore. They are separate from the seed (delivery plan §3) and back three checks: T15 acceptance ("fixture dump loads idempotently twice; quarantine set matches snapshot"), the go-ci job `etl-fixtures` (delivery plan §6, extended by R31), and the R24 quarantine snapshot.

### D.5.1 Layout and format

As delivered by T15 (the layout below replaces the draft `collections/` + `auth/` + `storage/` + `expected/` tree; the parts not yet delivered are listed in §D.5.2 with the task that adds them):

```
logitrack-api/cmd/etl/testdata/firestore-fixtures/
├── manifest.json            format logitrack-etl-dump/1, projectId logitrack-fixtures, exportedAt; one entry per collection
│                            file (name, file, count, group for a collection group; sha256 optional for hand-written files)
├── <collection>.ndjson      one Firestore document per line (names as in Firestore, e.g. incidentReport); flat: dump.Open
│                            reads only base names listed in manifest.json, the same layout `etl dump` writes (.ndjson.gz)
└── quarantine.snapshot      the expected report: findings (collection, doc_path, field, reason_code) and every document's
                             outcome and tenant_source; rewritten with `go test ./cmd/etl -tags=integration -run TestFixtureLoad -update`
```

Each line uses the `etl dump` format of main spec §13.1: `{_id, _path, _createTime, _updateTime, fields}`. `_updateTime` is required and `_createTime` optional; `etl dump` writes both as untagged RFC 3339 strings and the reader also accepts `{"$ts": …}`, so the example below loads verbatim. Field values use the tags `{"$ts": …}`, `{"$geo": …}`, `{"$ref": …}`, `{"$bytes": …}`, `{"$double": …}` and the escape `{"$map": …}`. A top-level `_note` stating the case is ignored by the loader; the T15 files carry none yet, and a line added later should have one. Ids are synthetic and never copied from production: T15 uses readable ids (`task_1`, `cust_spx`, `drv_own`, auth uids `uid_own`, `uid_alpha`, `uid_unknown`); fixtures added by later tasks use the `fx…` doc ids and 28-character `FXUID…` auth uids of §D.1.9, and both styles may coexist. Example:

```json
{"_id": "fxTaskLegacyUid00001", "_path": "tasks/fxTaskLegacyUid00001", "_createTime": {"$ts": "2026-07-21T08:00:00Z"}, "_updateTime": {"$ts": "2026-07-22T03:16:00Z"}, "_note": "legacy task: driverId holds an auth uid, Title-case status, PICKUP truckType, unpadded taskId", "fields": {"taskId": "FM-22072026-7", "date": {"$ts": "2026-07-21T17:00:00Z"}, "dateStr": "22072026", "status": "Completed", "taskType": "FIRST_MILE", "sourceHub": "ALANG-A", "destination": "SOCE (บัวโรย)", "driverId": "FXUID0000000000000000000DRV1", "truckType": "PICKUP", "licensePlate": "1ขค-1234", "sourceHubLinkedCustomerId": "fxCustSPX00000000001", "sourceHubCustomerLinkKind": "customer", "createdAt": {"$ts": "2026-07-21T08:00:00Z"}}}
```

`quarantine.snapshot` stands in for `expected/counts.json`, `etl_quarantine.json` and `quarantine_tenant_rows.json` (the per-document outcome gives the counts; `tenant_source` `quarantine` gives the quarantine-tenant rows). `resolution.json` is covered by assertions in `cmd/etl/etl_integration_test.go` instead of a file.

ETL rows receive runtime `uuidv7()` ids, so every expected file is keyed by Firestore doc path or `legacy_doc_id`, never by uuid. Reason codes are the canonical list of Appendix A §A.3.0 (R68), the set the `etl.quarantine.reason_code` CHECK enforces.

### D.5.2 Fixture cases

**Coverage today.** T15 ships the documents of `subcontractors`, `customers`, `trucks`, `drivers`, `tasks`, `trip_records`, `standby_records`, `incidentReport`, `vehicle_expenses`, `maintenance`, `settings` (`settings/tenancy`, recorded `pending`), `checkin` and the unknown `legacy_tmp`, with a subset of the cases below for those collections (`quarantine.snapshot` lists every finding they raise). Not yet covered there: the sub-array cases T24 maps (truck and driver `statusHistory`, `customerDriverIds`, `currentAssignment`, task delivery stops, trip stop progress and multi-drop breakdown), the broker driver's July tasks (the orphan-scan bucket of §D.5.3), and cases whose check needs a table T15 does not load (a sent statement for `billing_date_locked`, rate imports). Deferred with the mapping they test: `auth/users.json`, `users.ndjson` and `permissions_config.ndjson` → T19; `hubs`, the distance tables, `companies`, `truckAssignment`, `drivers__mobile_installations`, the rate tables, fees, standby rates, statements and counters, `transactions`, `payroll`, `driver_penalties`, `chats__messages`, `broadcasts`, `leave_requests`, `holidays`, the fuel snapshots, `security_events`, `vehicle_locations`, `waitlist`, `partner-interest` and the `settings/mobile_app` split → T24; `storage/objects.ndjson` and `rewrites.json` → T24 (T15 tests `media-copy` against objects it puts into the in-process `gcptest` backend); `orphan_scan.json` → T28 with the job. The table is the target set: a case moves into the fixtures with the task that maps or checks it.

| Fixture | Synthetic documents | Proves | Expected outcome | Post-ETL shape in the seed |
|---|---|---|---|---|
| `auth/users.json` + `users.ndjson` | password user with scrypt hash and `{admin:true}`; Google-only user; `role:'partner'` + `partnerScopeId`; `role:'customer'` + `customerScopeId`; driver with a stale `driverId` claim; disabled user; `lastLogin` in Auth-metadata string format and `fcmTokens` as a legacy array | claims → `memberships` / `user_platform_roles` / `user_scopes` mapping (Appendix C §C.5.5); `legacy_scrypt_*` kept for verify-then-rehash; `users.legacy_auth_uid` + `auth_identities` (`firebase_legacy`, `google`, R55); token merge into `device_tokens` | admin → own-fleet `tenant_admin` and no platform role unless the e-mail is in the fixture's `PLATFORM_ADMIN_EMAILS`; partner → carrier `tenant_admin`; customer → `user_scopes(customer)`; stale `driverId` resolved through `drivers.legacy_auth_uid`, reported | `U_D1`, `U_D2`, `U_D3`, `U_NWR_ADMIN`, `AI_*` |
| `permissions_config.ndjson` | 7 role docs with colon keys, underscore matrix ids and the 21 matrix-only ids | key reconciliation (R5): only catalog keys survive as `role_capability_overrides` deltas | 15 underscore ids converted; 21 rows → `unknown_capability_key` | `role_capability_overrides` |
| `subcontractors.ndjson`, `customers.ndjson`, `companies.ndjson` | NWR (broker, `billingDateBasis: plan`, `code` present), TTP; customers using `name`/`customerName` and `code`/`customerCode` aliases, Thai `branchType` literals, a duplicate code; owner company | carriers → `tenants(kind='carrier')` with `name_th`, `contractor_tenant_id` = own fleet (R56, R60) + tenant-kind `billing_parties`; aliases; duplicate codes | duplicate customer → second row rejected + `duplicate_natural_key`; `branch_type` `hq`/`branch` | `TN_NWR`, `BP_NWR`, `CU_*`, `CO_WRT` (the seed's `TN_TTP` is Go-created, so it has no contractor link) |
| `hubs.ndjson` | two docs for one `source_id` (different completeness); Thai text in `source_name_en` with empty `_th`; legacy `hubId`/`hubName`/`lat`/`lng`; doc id `unknown_3`; `station_type` `FM_HUB` and `RETURN_CENTER`; `createdByDriver`; SPK-prefixed and SPK-linked hubs | dedupe rule, TH/EN repair, alias folding, network rule, `hub_name_aliases` built name→code only | loser rejected + `duplicate_hub_source_id`, mapped to the winner; Thai moved to `name_th`; `RETURN_CENTER` → `SOC`; aliases exclude names equal to a code | `H_*`, `hub_name_aliases` |
| `hub_soc_distances.ndjson`, `soc_hub_distances.ndjson` | `{hub}_{soc}` and `{soc}_{hub}` doc ids incl. a SOC spelled `soce` | one table with `direction`; SOC key normalisation | 2 directions per pair; key `SOCE` | `HD_ALANGA_SOCE`, `HD_SOCE_ALANGA` |
| `drivers.ndjson`, `drivers__mobile_installations.ndjson` | `authUid` legacy alias; Timestamp-shaped `statusHistory`; whitespace-only `fullNameTh`; `activeTruck` + `currentAssignment`; unknown code in `customerDriverIds`; `subcontractorId` pointing at no document; `partnerId: ''` and a mismatching `partnerId`; **broker driver** whose `subcontractorId` is now TTP | driver identity columns, `status_history`, tenant derivation | missing carrier → row loaded into the quarantine tenant + `tenant_unresolved`; unknown code → `customer_unresolved`, mismatching `partnerId` → `tenant_mismatch` (field level); whitespace-only `fullNameTh` → NULL | `D1`–`D7`, `MI*` |
| `trucks.ndjson`, `truckAssignment.ndjson` | `truckStatus` `Active` and `Available`; type `4 Wheels` and an unknown `Trailer`; ISO-string `statusHistory`; subcontractor truck of NWR | status and class folding; never guess a class | `Available` → `active` + `legacy_status`; `Trailer` → `vehicle_class` NULL + `vehicle_class_out_of_enum` | `T1`–`T6`, `TA*` |
| `tasks.ndjson` | `driverId` as auth uid; no `driverId` but a Thai `driverName`; driverless pending task; one doc per Title-case status; `truckType` `PICKUP`, `6 Wheels` and blank; `dateStr` as `YYYYMMDD` and `ddMMyyyy`; two docs with the same unpadded `taskId`; blanket `DEFAULT_CUSTOMER_ID` link while the hub links elsewhere; `helperDriverIds` of length 2; **broker** tasks of July whose `truckId` is NWR's truck | driver resolution order doc id → auth uid → name (R12); status vocabulary (D2); R15 blank class; non-unique `task_no`; R11 quarantine tenant for tasks with no tenant chain (D6) | `driver_ref_match` `auth_uid` / `name`; driverless task in the quarantine tenant + `tenant_unresolved`; `link_blanket_default`; `helper_overflow` (first helper kept); blank class → NULL; broker tasks stamped with the driver's current tenant (TTP), `tenant_source='driver'` | `TK02`, `TK03`, `TK07`, `TK15`, `TK19`, `TK25` |
| `trip_records.ndjson` | `taskId` holding a unique business `taskId` and an ambiguous one; `spxTripId` ≠ doc id without rename provenance; a callable rename (`renamedFromTripId`); a trip id with Thai characters; status `loading`; legacy status `standby`; delivered trip without `deliveredTimestamp`; trip without `createdAt` but with `std`; multi-drop trip priced as single; `billingRateImportId` resolvable and `manual`; explicit JS `null` billing fields; Firebase download URLs with tokens; photo type `odometer_selfie`; two photos of one type; non-numeric `distance`; `sealTime` `dd-MM-yyyy HH:mm:ss`; `partnerCode` only in `ocrData`; CJSF trip with NULL `billingDate` in a month that has a sent statement | task-ref resolution, `trip_no` vs `legacy_doc_id`, `trip_no_history`, R19 "never drop a billable row", D7 restamp guard | `task_ref_match` `task_no`; ambiguous → `task_ambiguous`; `trip_no_mismatch`; `loading` → row rejected `status_out_of_vocab`; `legacy_standby_trip`; `missing_delivered_at`; `created_at := COALESCE(createdAt, std, deliveredTimestamp, Firestore create time)` + info `created_at_derived` (R68); `multidrop_priced_as_single`; unknown photo type loaded with `photo_type_known=false` + `photo_type_unknown` (R19); last photo per type wins; `bad_number`; seal time parsed as Bangkok local; NULL `billingDate` loaded NULL + `missing_billing_date`, and its month has a sent statement → also `billing_date_locked` (no restamp, D7) | `TR02`, `TR03`, `TR07`, `TR12`, `TR15`, `TR19`, `trip_no_history` |
| `standby_records.ndjson` | `billingRateEntryId: 'service_fee'`; missing `endedAt`; `migratedFromTripId` | standby billing columns; no derived end time | `billing_rate_source='service_fee'`; `ended_at` NULL + `missing_ended_at`, stored `billing_unpriced_reason='no_ended_at'` (R62) | `SB01`–`SB04` |
| `incidentReport.ndjson` | `tripId` that is a pre-rename trip id; admin report with `truckPlate` instead of `truckLicensePlate` | trip resolution through `trip_no_history.old_trip_no` | resolved; plate from either key | `IR1` |
| `customer_rate_entries.ndjson` | `effectiveFrom` at UTC midnight without `effectiveFromDateStr`; missing `jobCategory`; voided row; two legacy rows with the same `effectiveFrom`; class `2 WHEELS`; destination already collapsed to `SPK` | Bangkok-date selection key, R16 legacy tie-break, void-only import | `effective_from_date` = Bangkok date of the instant; missing category → `PRIMARY` (the one legitimate default); tie winner = lower `legacy_doc_id`; `2W` kept as stored | `RE01`, `RE05`, `RE09`/`RE10` |
| `customer_fuel_rate_adjustments.ndjson` | `referenceFuelPriceThbPerLitre`; voided; legacy 07:00 ICT instant | band provenance inputs | loaded as-is | `FA01`–`FA06` |
| `customer_service_fees.ndjson` | two `extra_stop` docs and two `standby` docs for one customer | D5 winner rules (`extra_stop` first by doc id, `standby` last) | all four loaded + `duplicate_service_fee`; Go selects the legacy winners until sign-off; after the loser correction `0010` builds `customer_service_fees_one_per_type` | `SF01`–`SF03` |
| `standby_rate_entries.ndjson` | `effectiveFrom` at browser-local midnight | Bangkok-date key (UNVERIFIED that admin browsers ran in ICT) | `effective_from_date = bkk_date(effectiveFrom)` | `SR01`–`SR03` |
| `billing_statements.ndjson`, `billing_counters.ndjson` | statement without `invoiceNumber`; float totals like `1234.5600000001`; counter doc `{customerId}_{YYYYMM}` | invoice number fallback to doc id; JS half-up rounding of stored floats; `withholding_tax_rate = 0.0100` (D8/R18); no lines for legacy statements | totals rounded to 2 dp; counters split on the last `_` | `ST2`, counters |
| `vehicle_expenses.ndjson` | two fuel docs with the same `(driverId, taxInvId)`; `refillLocation` as `"lat,lng"`; legacy `gasStation` | D5: report, never rewrite | both rows loaded unchanged + `duplicate_natural_key` (info); the CI loser correction (§D.5.4 step 4; a direct UPDATE as `logitrack_etl` in T15, the API once it exists) fixes one before `0010` builds `vehicle_expenses_fuel_taxinv` | `EX01`–`EX04` |
| `maintenance.ndjson` | statuses `PM Booking`, `Scheduled`, `In-Progress`, `in_progress` | canonical status vocabulary | `pm_booking`, `scheduled`, `in_progress` | `MT01`–`MT03` |
| `transactions.ndjson`, `payroll.ndjson`, `driver_penalties.ndjson` | truck-renewal shape and payout shape; `payroll/{uid}_{YYYY-MM}_{R1\|R2}`; penalty with `remainingThb > totalThb` | two ledger shapes; doc-id parsing; CHECK safety | payout `tx_date_source='legacy_utc'`; bad penalty → row rejected + `bad_number` | `TX01`, `PR01`–`PR03`, `PN01`–`PN04` |
| `chats.ndjson`, `chats__messages.ndjson` | two non-closed chats for one driver; `lastReadByAdmin` map | read-state rows; partial unique index created only after the report | both chats loaded + `duplicate_natural_key`; `chats_one_open_per_driver` builds after the loser correction | `CH01`, `CH02` |
| `broadcasts.ndjson`, `leave_requests.ndjson`, `holidays.ndjson` | `readDriverAuthIds`; missing `title`; leave with attachments; holiday `date` Timestamp | `broadcast_reads`; leave driver by doc id; `holiday_date = bkk_date(date)` | loaded | `BC01`, `LV01`–`LV03`, `HO1`–`HO4` |
| `settings.ndjson` | `settings/mobile_app` written by both writers | split into `settings('mobile_app')` + one `mobile_app_releases` row | APK object registered with `visibility='public'` | `MR01`, `settings` |
| `fuel_daily_snapshots.ndjson`, `fuel_monthly_snapshots.ndjson`, `security_events.ndjson`, `vehicle_locations.ndjson`, `waitlist.ndjson`, `partner-interest.ndjson` | monthly snapshot with `status: 'error'`; security event whose `actorUid` is unknown | load as-is; actor kept in `actor_legacy_uid` + `user_unresolved` | loaded | `SE1` |
| `checkin.ndjson`, `tmp_debug.ndjson` | the rules-only `checkin` collection; an unknown collection | dropped collections | `etl.source_docs.status='dropped'`; `unknown_collection` | — |
| `storage/objects.ndjson` | objects for every URL above, minus one | URL → key rewrite; copy with identical key; objects stay under the pre-rename trip id | missing object → `file_objects.status='missing_at_source'` + `file_missing_at_source` | `FO_TR03_SEAL`, `FO_TR19_PC` |

### D.5.3 Quarantine snapshot (R24) and orphan scan

In T15 the first two items are both held by `quarantine.snapshot` (§D.5.1) and the invariant is also checked by `etl reconcile`; `orphan_scan.json` arrives with the job (T28).

- `counts.json` enforces `fs_count = loaded + quarantined + rejected + dropped` per collection (Appendix A §A.3.0). Rows whose tenant chain runs out are loaded into the quarantine tenant (the 0002 row with the fixed id, `tenant_source='quarantine'`, R11, R56) with `etl.source_docs.status='quarantined'` and an `etl.quarantine` row `tenant_unresolved`; `rejected` contains only rows that cannot load: another NOT NULL or CHECK violation (`status_out_of_vocab`, `missing_required`, the penalty CHECK) or a duplicate loser (`duplicate_hub_source_id`, the second customer with a duplicate code). R19 forbids rejecting a billable trip for a missing `createdAt`.
- `quarantine_tenant_rows.json` lists the driverless legacy task, its trip chain and the driver with a dangling `subcontractorId`. Re-homing them is an owner decision (D6, owner question Q9 in main spec §19.2); the snapshot pins the pre-decision state.
- `orphan_scan.json` is the expected result of the `tenancy.orphan-scan` job after load: the quarantine-row counts above, plus the R24 drift report for `tenant_source='driver'` rows. Two buckets (job-result keys, as in smoke job `JB2`): `driverMoved` (the row's tenant differs from the driver's current tenant — the post-cutover signal) and `truckTenantMismatch` (the row was stamped from the driver's current tenant but its truck belongs to another tenant — the only drift evidence available for ETL-stamped history; the broker fixture's July tasks land here).

### D.5.4 CI procedure (go-ci job `etl-fixtures`)

`make etl-fixtures-check` runs the integration tests of `./cmd/etl/...` on testcontainers (`postgres:18-alpine`, MinIO), each on a fresh database at `up-to 9` + `apply 11` (the production P0 schema):

1. `etl load --dump=cmd/etl/testdata/firestore-fixtures --dry-run --report=…`: the report equals `quarantine.snapshot` and no row of schemas `public` and `etl` changes (`xmin`/`ctid` fingerprint).
2. Real load (`--fixtures`): the database's quarantine report equals the snapshot; a second load applies 0 documents and changes no row (upsert keyed by `legacy_doc_id`, newer `_updateTime` wins).
3. `etl reconcile --fixtures`: exit 0, then exit 2 on an injected money mismatch and on a missing row; the reason-code gate fails on a new code and on a higher count, on every rerun, until the findings are resolved or accepted.
4. After the loser correction (`exp2`), `goose up` applies `0010_d5_unique_constraints.sql` (R59, R88): its D5 unique indexes must build, proving the dedupe and quarantine worked (R31).
5. The other passes against in-process fakes and containers: `export-back` round trip through `gcptest` (a stale document is rewritten, one changed after the freeze is refused); `dump` → `load` through `gcptest`; `--since=watermark` with its look-back, and pending documents that never move a watermark; `quarantine resolve` (`rehome` with its `tenant_rehomed` event, files and tenant-only resolution, `retry`, `skip`); re-applies that keep the tenant and a fix at source; `media-copy` and `--verify` into MinIO, then `rewrite-urls` exit 0.

Still to add with their tasks: `tenancy.orphan-scan` against `orphan_scan.json` (T28); `rewrite-urls` and `media-copy --verify` against `storage/` and `rewrites.json`, and `etl reconcile` against `reconcile.md` (T24).

A change to the ETL that alters `quarantine.snapshot` (or a later expected file) must update the fixture, and its `_note` where the line has one, in the same PR; the PR description states which legacy case changed behaviour.
