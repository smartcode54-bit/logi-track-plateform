# Appendix C — Auth, sessions and multi-tenant RBAC

Part of the mv-go migration documentation (Firebase -> Go 1.27 + Fiber v3 + PostgreSQL 18 + RabbitMQ + Redis + MinIO). Main spec: [developer-spec.md](../../../developer-spec.md) §4 summarises this appendix; siblings: [Appendix A — data model](./A-data-model.md), [Appendix B — API catalog](./B-api-catalog.md), [Appendix D — seed and mock data](./D-seed-and-mock-data.md), [Appendix E — web fetch audit](./E-web-fetch-audit.md); decision record: [ADR 0029](../../adr/0029-migrate-firebase-stack-to-go-postgres.md).

**Scope.** Replaces Firebase Auth, custom claims, `permissions_config`, the authorization half of `firestore.rules`, the `users/{uid}` mirror document, the `security_events` writers and the session / force-logout mechanics. It supersedes `shared-docs/database-migration-plan.md:233-236, :284` ("Firebase Auth stays the source of truth; never duplicate users into SQL"), which ADR 0029 marks superseded.

**Precedence.** The approved plan's reconciliation decisions R1-R35 and web decisions W1-W10 override the earlier design drafts; the cross-document resolutions R36-R90 override both where they differ. Name authority: Appendix A DDL for tables and columns, Appendix B for routes, topics, queues and Redis keys, main spec §16 for environment names, and this appendix (§C.3) for which tables carry RLS and which policies apply. Where this appendix deviates from a draft it says so in place. Owner decisions that are still open point to main spec §19.

**Wire conventions (R48).** Every JSON body in this appendix is camelCase, auth bodies included (`accessToken`, `refreshToken`, `idToken`, `installId`, `expiresIn`). Errors use the envelope `{"error":{"code","message","details","requestId"}}` of Appendix B §B.1.5; auth codes include `token_expired`, `session_revoked`, `invalid_token`, `invalid_credentials`, `password_change_required`. SQL identifiers, JWT claim names and Redis keys keep their own spelling.

**ADR 0026 citations (R32).** ADR 0026 (multi-tenant carrier isolation) is referenced throughout, but its text is not on disk in any git ref (`git ls-tree -r origin/feat/multi-tenant-carrier-isolation` contains no `shared-docs/adr/0026-*` and no `shared-docs/specs/multi-tenant-carrier-isolation.md`). Every "ADR 0026" statement below takes its semantics from the branch glossary (`origin/feat/multi-tenant-carrier-isolation:shared-docs/glossary.md:560-670`, terms Tenant, `tenantId`, Rules are not filters, Dispatcher, Platform admin, Tenant admin, Broker carrier, Tenant orphan, `partnerCode`) and from `logitrack-web/functions/src/core/tenantResolve.ts` + `logitrack-web/functions/src/tenantLookups.ts` on that branch. ADR 0029 restates the six tenancy rules this appendix depends on:

1. `tenant_id` says which carrier organisation ran a row; it is resolved once at write time and frozen (`tenantResolve.ts:5-8`).
2. Resolution order is per table and link-first: task/trip links beat the driver approximation (`tenantResolve.ts:77-120`).
3. Nothing is guessed: an unresolvable row is an orphan, never a default (`tenantResolve.ts:13-14`); in PostgreSQL it lands in the quarantine tenant (R11).
4. The own-fleet tenant is data, not a constant (`tenantLookups.ts:12-21`); in PostgreSQL it is `tenants.kind = 'own_fleet'` (R7).
5. Dispatcher visibility is a second axis orthogonal to `tenant_id`; it sees the operational projection across tenants, never carrier-internal cost/HR data (glossary "Dispatcher").
6. Platform admin is distinct from tenant admin, and cross-tenant reads by a platform admin are audited (glossary "Platform admin").

**Evidence conventions.** As-is claims cite `path:line` from the fact-base reports or from files read in the repo; paths are repo-relative. Unverifiable items carry an `UNVERIFIED:` marker with the reason. Secrets appear as environment variable names only; the canonical name list is main spec §16.

---

## C.1 Identity model

### C.1.1 Four organisation concepts that must not be merged

| Concept | Meaning | PostgreSQL home | Legacy source |
|---|---|---|---|
| **Tenant** | Carrier organisation that *runs* rows (drivers, trucks, tasks, trips). Unit of isolation (glossary "Tenant"). | `tenants` (`kind` = `own_fleet` \| `carrier` \| `quarantine`, R7/R11). Subcontractors are tenants of kind `carrier`; there is **no** `subcontractors` table (R6). | every `subcontractors` doc (`tenants.legacy_doc_id`) + one `own_fleet` row + one `quarantine` row |
| **Billing party** | Who *pays* for a trip; legacy `billingCustomerId` / `*LinkedCustomerId` may point at a `customers` doc **or** a `subcontractors` doc. | `billing_parties(kind customer\|tenant)` — the single polymorphic FK target for every billed / linked reference (R6) | `customers`, `subcontractors` |
| **Company** | Legal entity that *issues the invoice* (`companies`, `companyType: owner\|subcontractor`). | `companies` (tenant-scoped, `tenant_id NOT NULL`) | `companies`. UNVERIFIED: relationship between `companies(companyType=subcontractor)` and `subcontractors` (as-is auth report §8); ETL matches by tax id / name, else quarantine. |
| **Dispatcher organisation** | The party that receives work from the end customer and allocates it across tenants (TTP today). | a tenant (its own fleet) **and** a billing party (as a customer) **and** a dispatcher grant on its users (`user_scopes.kind='dispatcher'`) | glossary "Dispatcher": TTP is customer, tenant and dispatcher at once |

### C.1.2 Principal types

| Principal | Authenticated by | Carries | Typical routes |
|---|---|---|---|
| Tenant staff user | password or Google, Go JWT | `tid` + `rol` ∈ {tenant_admin, manager, operation_staff, operator, user} | web `/app/**` via BFF |
| Driver | password or Google, Go JWT (APK 3.x reaches Go only through the callable shims with its Firebase ID token, C.6.5) | `tid` + `rol='driver'` + `drv` | mobile `/v1/mobile/*` |
| Customer-scope user | password or Google | no `tid`; `cs` = billing party ids (customer-kind scopes of a user who also has a membership are ignored, C.2.4) | web driver monitor / boards / incidents (read-only) |
| Dispatcher | password or Google | `tid` of its own organisation's tenant (R13) + `dsp=true` + `cs` = parties it allocates | web boards and monitor across tenants (operational projection) |
| Platform staff | password or Google | `plt` ∈ {platform_admin, support}; acts on tenant data only through `X-Act-On-Tenant` (C.3.9) | Security Center, tenancy admin |
| Service caller | `api_keys` row (C.4.11) | key tenant (or platform) + key capabilities | callable shims (R25, R45), the release CLI `cmd/release` on the private network (R43), scripts |

### C.1.3 Identity tables (DDL of record: Appendix A)

The DDL lives in Appendix A: `0002_identity.sql` (§A.2.1) creates the identity tables of C.3.0 marked (0002); `0003_master.sql` (§A.2.2) creates `user_scopes` right after `billing_parties`. PostgreSQL 18: ids are `uuid DEFAULT uuidv7()` (native, time-ordered, Go never mints ids, R16/R34/R57); vocabularies are `text` + `CHECK`. The columns this appendix depends on:

| Table | Columns used by auth, RLS and the bridge |
|---|---|
| `tenants` | `kind` (`own_fleet` \| `carrier` \| `quarantine`), `name_th`, `name_en`, `code`, `status`, `legacy_doc_id`, `contractor_tenant_id uuid NULL REFERENCES tenants(id)` (contractor reach, R56/R60, C.3.4); carrier profile folded in (R6) |
| `users` | `legacy_auth_uid` (Firebase uid, kept forever), `email` (citext), `email_verified`, `password_hash` (Argon2id PHC), `legacy_scrypt_hash` / `legacy_scrypt_salt` (bytea, both or neither, NULLed at re-hash), `must_change_password`, `password_changed_at`, `status` (`active` \| `disabled` \| `reset_required` \| `deleted`), `disabled_at`, `deleted_at`, `auth_version` (JWT `ver`), `display_name`, `photo_file_id`, `last_login_*`, `legacy_auth_created_at` (R55) |
| `auth_identities` | `provider` (`google` \| `firebase_legacy`), `provider_subject`, `email_at_link`, `linked_at`, `last_used_at`; UNIQUE `(provider, provider_subject)` and `(user_id, provider)` |
| `memberships` | PK `(user_id, tenant_id)`, `role` with CHECK `memberships_tenant_role_check` (`tenant_admin`, `manager`, `operation_staff`, `operator`, `user`, `driver`), `status` (`active` \| `suspended`) |
| `user_platform_roles` | PK `(user_id, role)`, `role` (`platform_admin` \| `support`), `granted_by` NULL = bootstrap from `PLATFORM_ADMIN_EMAILS` |
| `user_scopes` | `kind` (`customer` \| `dispatcher`, R86), `billing_party_id NOT NULL`; UNIQUE `(user_id, kind, billing_party_id)` |
| `sessions` | `platform`, `amr`, `active_tenant_id` (R83), `install_id` (R83), `absolute_expires_at`, `revoked_*` (C.4.4) |
| `refresh_tokens`, `password_reset_tokens`, `api_keys` (incl. `scope`, R82), `role_capability_overrides` | C.4.4, C.4.9, C.4.11, C.2.5 |

`users` has no platform-admin flag, revocation epoch or force-logout column: platform roles are `user_platform_roles` rows, revocation is `auth_version` + session revocation (C.4.7), `reset_required` serves the 180-day tail (C.5.8). There is no `company` scope kind (R86): the legacy `companyId` claim is never written by any code path (`logitrack-web/firestore.rules:532` and `logitrack-web/hooks/useCompanyScope.ts:13-21` only read it), so ETL creates no such rows, no token claim carries it and no RLS policy reads it. Draft table names replaced by these tables are listed in Appendix A §A.2.R; the subcontractors collection becomes `tenants` of kind `carrier` (no separate table, R6) and legacy driver references are kept per row (`legacy_driver_ref` + `driver_ref_match`, R12).

### C.1.4 Driver identity

Legacy code mixes three driver identifiers (as-is auth report §5): (a) the Firebase Auth uid (= `users/{uid}` = `drivers.authId`), (b) the `drivers/{docId}` id (= the `driverId` custom claim), (c) a legacy `drivers.authUid` fallback (`logitrack-web/functions/src/triggers.ts:164`). Which one a collection stores differs per collection: `tasks`, `truckAssignment`, `leave_requests`, `drivers/{id}/mobile_installations` hold the doc id (legacy `tasks` rows may hold the uid, `firestore.rules:194`); `trip_records`, `standby_records`, `incidentReport`, `vehicle_expenses`, `chats`, `payroll` hold the uid; the maintenance rule uses the `driverId` claim (`firestore.rules:380-418`).

PostgreSQL rules:

- **One FK.** Every domain table stores `driver_id uuid REFERENCES drivers(id)`; no table stores a user id to mean a driver. `drivers.user_id uuid UNIQUE REFERENCES users(id)` (partial unique `WHERE user_id IS NOT NULL`) replaces `drivers.authId` / `authUid`.
- **Legacy values kept per row (R12).** ETL resolves every legacy `driverId`-like value in the order `drivers.legacy_doc_id = v` -> `drivers.legacy_auth_uid = v` -> (tasks only) name match, stores the raw value in `legacy_driver_ref` and the match kind in `driver_ref_match` ∈ `doc_id|auth_uid|name|none`; this is the doc-id-then-authId order of `tenantLookups.ts:63-82`. `driver_ref_match='none'` on a `NOT NULL driver_id` column sends the row to the quarantine tenant (C.3.10), never to a guessed driver.
- **Login never searches drivers by uid.** Today mobile resolves the driver with `drivers.where('authId', '==', uid)` (`logitrack-mobile/lib/features/auth/data/repositories/auth_repository.dart:85-89`) and signs a user out with "Access Denied" when none exists. In Go the principal's `DriverID` comes from `drivers.user_id`; a user whose active membership role is `driver` but who has no `drivers` row gets `403 driver_profile_required`.
- **Link invariant (trigger `trg_driver_link_membership`, C.3.6).** `drivers.user_id = U` requires an active `memberships(U, drivers.tenant_id, 'driver')`. `PUT /v1/users/{id}/driver-link` moves the link atomically: clears any previous driver row's `user_id`, writes the membership, bumps `users.auth_version`, appends `security_events` `driver_linked`. Today `linkDriverToUser` revokes tokens but logs nothing (`logitrack-web/functions/src/users.ts:371-430`).
- **Broker carrier moves (glossary "Broker carrier").** A driver who starts running for another carrier is moved with `PATCH /v1/drivers/{id}` carrying `tenantId` (platform_admin, or a steward principal, C.2.2); the same transaction moves the driver membership and appends `driver_tenant_moved`. Past rows keep their frozen `tenant_id`; the driver still sees their own history because driver self-scope is by `driver_id`, not by tenant (C.3.5).

### C.1.5 Legacy identifier map

| Legacy identifier | Where it lives today | PostgreSQL |
|---|---|---|
| Firebase Auth `localId` (uid) | Auth export; `users/{uid}`; `drivers.authId`; uid-valued `driverId` fields | `users.legacy_auth_uid` + `auth_identities(provider='firebase_legacy')`; `drivers.legacy_auth_uid` |
| `drivers/{docId}` | `driverId` claim; doc-id-valued `driverId` fields | `drivers.legacy_doc_id` (nullable for drivers created in Go; for those the Firestore projection uses the uuid string, R25) |
| `subcontractors/{docId}` = `partnerScopeId` claim = `drivers.subcontractorId` = `mobile_installations.partnerId` | claims, drivers, heartbeat docs | `tenants.legacy_doc_id` (kind `carrier`) |
| `customers/{docId}` = `customerScopeId` claim | claims; `billingCustomerId` | `customers.legacy_doc_id` -> `billing_parties(kind='customer')` |
| `users.fcmTokens{app}` and `drivers.fcmToken` | two token stores (`logitrack-mobile/lib/core/services/fcm_service.dart:43-46`; `logitrack-mobile/lib/features/home/data/repositories/driver_repository.dart:73-83`) | `device_tokens` PK `(user_id, install_id)`, `token UNIQUE`, `driver_id` nullable (R4) |

### C.1.6 Legacy role -> PostgreSQL grant

Full mapping in C.5.5. In short: legacy `admin` -> own-fleet `tenant_admin` and **no** platform role (owner decision D1, main spec §19); only emails in `PLATFORM_ADMIN_EMAILS` also get `user_platform_roles(platform_admin)`, applied once by `cmd/seed bootstrap-platform-admins` and never at login (replaces the hardcoded `ADMIN_EMAILS`, `logitrack-web/functions/src/auth.ts:5-7`, and the `setAdminClaims` call on every auth state change, `logitrack-web/context/auth.tsx:49-61`); `partner` + `partnerScopeId` -> `tenant_admin` of that carrier tenant (D2); manager / operation_staff / operator / user -> own-fleet membership with that role; driver -> `drivers.user_id` link + driver membership; `customer` + `customerScopeId` -> `user_scopes(kind='customer')`, no membership; dispatcher is new and granted by a platform admin (C.1.7).

### C.1.7 Dispatcher (R13)

- A dispatcher user **must** hold a membership in its own organisation's tenant (TTP is a tenant for its own fleet). That membership gives it a `tid`, so `RequireTenant()` routes work and `POST /v1/tasks` stamps a driverless call-out with that tenant and `tenant_source='form'`.
- The dispatcher axis is `user_scopes(kind='dispatcher', billing_party_id)` rows naming the billing parties whose work the dispatcher allocates (TTP as a customer). Token claim `dsp=true`, `cs` = those party ids. The cross-tenant read predicate is the scope predicate of C.3.3 with `app.dispatcher=on`; there is no tenant filter on that branch.
- Assigning a call-out to a driver of another tenant moves `tasks.tenant_id` through `PATCH /v1/tasks/{id}` with an explicit `tenantId` (dispatcher for tasks in its scope, or platform_admin). The service sets `app.tenant_move=on` for that transaction only (C.3.6) and appends `task_tenant_reassigned`.
- Dispatcher grants are written only by platform_admin (`PUT /v1/users/{id}/scopes/dispatcher`).
- Collapsing dispatcher and tenant into one claim is forbidden (glossary "Dispatcher").

### C.1.8 Own fleet and quarantine tenants

- `tenants.kind='own_fleet'` has a partial unique index `tenants_one_own_fleet` (exactly one row per database, R7). The API reads it once and caches it in Redis `lt:{APP_ENV}:cache:tenant:own_fleet`. `OWN_FLEET_TENANT_ID` exists only so `cmd/seed` / `cmd/etl` can choose that row's uuid deterministically per environment; no request path reads it (replaces `settings/tenancy.ownFleetTenantId`, `tenantLookups.ts:12-21`, and avoids the hardcoded `DEFAULT_CUSTOMER_ID` trap of `logitrack-web/functions/src/backfillCustomerLinks.ts:6`).
- `tenants.kind='quarantine'` (one row, partial unique `tenants_one_quarantine`) owns every row whose tenant chain runs out at ETL (R11). It has no memberships; its rows are invisible to every tenant and every scope principal and are re-homed by a platform admin (C.3.10). Unlike the own-fleet row it is structural, not environment data (R56): migration `0002_identity` inserts it with the fixed id that `app_quarantine_tenant_id()` returns (C.3.3), so seed, ETL and RLS agree without configuration:

```sql
-- 0002_identity.sql, after CREATE TABLE tenants
INSERT INTO tenants (id, kind, name_th, name_en, status)
VALUES ('00000000-0000-7000-8000-00000000000f', 'quarantine', 'กักกันข้อมูล', 'Quarantine', 'active');
```

- TTP is both a billing party (customer) and a carrier tenant (its own fleet and dispatcher organisation, R13, R75); the two rows are linked only through `billing_parties(kind='tenant')` when TTP is billed as a tenant, never merged.

---

## C.2 Roles and capability catalog

### C.2.1 As-is: three key formats that never matched

| Format | Where | Effect today |
|---|---|---|
| Colon value `module:action` (50 keys) | `logitrack-web/lib/capabilities.ts:8-78` (code key `fleet_view_trucks` -> value `"fleet:view_trucks"`); read by `usePermission` (`logitrack-web/hooks/usePermission.ts:51-65`) and `bangchakOilPrice.ts:8-26` | `usePermission` never matches matrix-saved keys, so `PagePermissionGuard` always falls back to `DEFAULT_ROLE_CAPABILITIES` (`logitrack-web/lib/roles.ts:40-136`) |
| Matrix row ids (36 rows, underscore) | written by the Role Matrix to `permissions_config/{role}.capabilities` as `{rowId: boolean}` (`logitrack-web/app/app/security-center/roles/page.tsx:385-398`) | only 15 rows equal a code key; 21 exist nowhere else; 35 code keys are not in the matrix |
| Underscore key in rules | `canReadSecurityEvents` reads `security_view_audit` (`logitrack-web/firestore.rules:32-38`) | the only place the matrix takes effect |

Also resolved: `canAccessRoute` uses defaults only and ignores `permissions_config` (`logitrack-web/lib/permissions.ts:33-74`) and allows unmapped routes (`permissions.ts:72`); `/app/security-center/*` is admin-only regardless of capability (`permissions.ts:55-57`), so a partner holding `security:view_mobile_clients` cannot reach the page. The remaining as-is gaps (three admin definitions, hardcoded roles in `bangchakOilPrice.ts:10`, `handleDiscard`) are listed with their fixes in C.2.5 and C.7 #16-#17.

**Decision (R5).** One catalog in colon form, defined once in Go (`internal/authz/catalog.go`), generated into `shared-docs/schemas/capabilities.ts` for the web (R27: OpenAPI + Go are the SSOT; the TypeScript file is generated, never edited). Underscore ids and `permissions_config` disappear after the ETL translation in C.2.6.

### C.2.2 Roles, scopes and capability classes

| Axis | Values | Stored in | Token |
|---|---|---|---|
| Tenant role (one per membership) | `tenant_admin`, `manager`, `operation_staff`, `operator`, `user`, `driver` | `memberships.role` (CHECK `memberships_tenant_role_check`) | `tid`, `rol` (active membership only) |
| Scope | `customer`, `dispatcher` | `user_scopes.kind` | `cs`, `dsp` |
| Platform role | `platform_admin`, `support` | `user_platform_roles.role` | `plt` |

Every key has one **class** that decides where it can be effective and whether a tenant may override it:

| Class | Meaning | Effective when | Overridable by |
|---|---|---|---|
| `tenant` | acts on rows of the active tenant | principal has a tenant role in `tid` (or acts via `X-Act-On-Tenant`) | tenant override (C.2.5) |
| `global` | acts on platform-shared master or configuration data (hubs, customers / billing parties, carrier profiles, distance matrix, mobile release floor, system status, product waitlist) | principal is a **steward**: a staff role (not `driver`) in a tenant with `kind='own_fleet'`, or `platform_admin` | only overrides scoped to the own-fleet tenant or platform-wide (`tenant_id IS NULL`) |
| `self` | driver acts on its own rows (`mobile:*`) | membership role `driver` with a linked `drivers` row | tenant override for role `driver` |
| `scope` | cross-tenant operational projection | principal holds a dispatcher grant | never |
| `platform` | platform administration | principal holds a platform role | never |

**Steward rule (global class, R60).** Without it, every carrier `tenant_admin` (legacy `partner`, D2) would edit the hubs, customers and name maps every tenant prices against, and could raise the mobile version floor that locks out the whole fleet (ADR 0007). Keeping global master data with the own-fleet tenant reproduces today's behaviour (only Wanpen-Ratchada staff maintain it). The same rule decides writes of platform-wide rows in nullable-tenant tables: `holidays` with `tenant_id IS NULL` (`holiday_type='public'`) and `broadcasts` with `tenant_id IS NULL`. Whether stewardship should instead be platform-admin-only is an owner decision (main spec §19, with open question 4 on who becomes `platform_admin`). RLS sees stewardship through GUC `app.steward` (C.3.2); the seven global keys are `fleet:manage_customers`, `fleet:manage_subcontractors`, `operations:manage_sources`, `operations:calculate_distances`, `security:view_status`, `security:manage_mobile_release`, `waitlist:view`.

### C.2.3 Canonical catalog

The catalog has **81 keys (77 + 4 platform)** (R73): the 76 non-platform keys of the auth design, plus `mobile:create_hub` (R5), plus the four `platform:*` keys. All 81 are listed.

Holder abbreviations: TA tenant_admin, MG manager, OS operation_staff, OP operator, US user, DR driver, CU customer scope, DS dispatcher scope, PA platform_admin, SU support. "Replaces" names the legacy code key (`lib/capabilities.ts` line) and/or the matrix row id (`roles/page.tsx` line) whose value the ETL translates (C.2.6); "—" = no legacy key.

| # | Key | Class | Description | Default holders | Replaces |
|---|---|---|---|---|---|
| 1 | `fleet:view_trucks` | tenant | Truck list and detail (non-financial fields) | TA MG OS OP US | `fleet_view_trucks` (:10) |
| 2 | `fleet:create_truck` | tenant | Register a truck, Excel truck import | TA MG | `fleet_create_truck` (:11); row `create_truck` (:62) |
| 3 | `fleet:edit_truck` | tenant | Edit truck master incl. `gps_vehicle_id` / telemetry fields, status history | TA MG | `fleet_edit_truck` (:12); row `edit_telemetry` (:68) |
| 4 | `fleet:view_renewals` | tenant | Tax / insurance renewal state | TA MG OS OP | `fleet_view_renewals` (:13) |
| 5 | `fleet:manage_renewals` | tenant | Record renewals (writes `trucks` + `transactions`); today admin-only by rules (`firestore.rules:308-311, 421-425`) | TA MG | — (write side split from `fleet_view_renewals`) |
| 6 | `fleet:manage_maintenance` | tenant | Create / update / transition maintenance records | TA MG OS OP | `fleet_manage_maintenance` (:14); row `maintenance_logs` (:86) staff columns |
| 7 | `fleet:manage_subcontractors` | global | Create / edit carrier tenant profiles and documents (`tenants` kind `carrier`, `tenant_files`) | TA MG | `fleet_manage_subcontractors` (:15) |
| 8 | `fleet:manage_customers` | global | Customers and billing parties (incl. `billing_date_basis`, LINE group) | TA MG | `fleet_manage_customers` (:16) |
| 9 | `fleet:view_assignments` | tenant | Truck-driver assignment history | TA MG OS OP | `fleet_view_assignments` (:17) |
| 10 | `fleet:manage_assignments` | tenant | Assign / revoke truck-driver bindings; assign a driver to a task | TA MG OS OP | row `assign_driver` (:80) |
| 11 | `fleet:view_live_map` | tenant | `vehicle_locations`, dashboard live map | TA MG OS OP DS | row `view_live_map` (:74) |
| 12 | `drivers:view` | tenant | Driver list and detail without PII columns | TA MG OS OP US | `drivers_view` (:20) |
| 13 | `drivers:create` | tenant | Create driver profile (no account side effects) | TA MG | `drivers_create` (:21) |
| 14 | `drivers:edit` | tenant | Edit driver profile and status | TA MG | `drivers_edit` (:22) |
| 15 | `drivers:view_pii` | tenant | ID card number and image, licence image, birth date (5-minute presigned GET) | TA MG | — (today public in Storage) |
| 16 | `drivers:set_password` | tenant | Issue a generated temporary password to a driver user | TA | — (replaces the `updateDriverAccount` password path) |
| 17 | `chat:view` | tenant | Admin chat console read | TA MG OS OP | `chat_view` (:25); row `chat_view` (:167) staff columns |
| 18 | `chat:send` | tenant | Send messages as admin, assign / close chats | TA MG OS OP | `chat_send` (:26); row `chat_send` (:168) staff columns |
| 19 | `broadcasts:send` | tenant | Send a broadcast to the tenant's drivers; platform-wide rows need a steward | TA MG | row `broadcasts_send` (:169) |
| 20 | `broadcasts:view` | tenant | Broadcast history and read counts | TA MG OS OP | row `broadcasts_view` (:170) |
| 21 | `operations:view_first_mile` | tenant | First Mile board, job-assign read | TA MG OS OP CU DS | `operations_view_first_mile` (:29) |
| 22 | `operations:view_line_haul` | tenant | Line Haul board | TA MG OS OP CU DS | `operations_view_line_haul` (:30) |
| 23 | `operations:manage_tasks` | tenant | Create / edit / cancel / import / assign tasks, delivery stops | TA MG OS OP | — (boards write tasks directly today) |
| 24 | `operations:manage_sources` | global | Hubs / SOC master and name aliases | TA MG OS OP | `operations_manage_sources` (:31) |
| 25 | `operations:calculate_distances` | global | Run the Distance Matrix job (`hub_soc_distances`) | TA | `operations_calculate_distances` (:32) |
| 26 | `operations:view_driver_monitor` | tenant | Driver monitor, standby records list | TA MG OS OP CU DS | `operations_view_driver_monitor` (:33) |
| 27 | `operations:edit_trip_details` | tenant | Edit trip details, rename trip, revoke an evidence link (`POST /v1/trips/{id}/evidence/revoke`, `POST /v1/standby/{id}/evidence/revoke`; the next forced LINE send mints a new token, R47) | TA MG OS OP | `operations_edit_trip_details` (:34) |
| 28 | `operations:view_incidents` | tenant | Incident list and detail | TA MG OS OP CU DS | `operations_view_incidents` (:35) |
| 29 | `operations:create_standby` | tenant | Admin standby backfill | TA MG OS | `operations_create_standby` (:36) |
| 30 | `accounting:view_fuel` | tenant | Fuel expenses, Bangchak retail prices | TA MG OS | `accounting_view_fuel` (:49); row (:175) |
| 31 | `accounting:edit_fuel` | tenant | Edit fuel expense rows | TA MG | `accounting_edit_fuel` (:50); row (:176) |
| 32 | `accounting:view_other` | tenant | Other expenses (toll, parking, repair) | TA MG OS | `accounting_view_other` (:51); row (:177) |
| 33 | `accounting:edit_other` | tenant | Edit other expenses, toll import | TA MG | `accounting_edit_other` (:52); row (:178) |
| 34 | `accounting:audit_expense` | tenant | Approve / reject vehicle expenses | TA MG OS | `accounting_audit_expense` (:53); row (:179) |
| 35 | `accounting:view_rate_card` | tenant | Rate entries, fuel adjustments, service fees, standby rates | TA MG OS | `accounting_view_rate_card` (:54) |
| 36 | `accounting:edit_rate_card` | tenant | Create / void rate entries and fuel adjustments; fees; standby rates | TA MG | `accounting_edit_rate_card` (:55) |
| 37 | `accounting:view_income` | tenant | Income page, missing-billing tab, billing rows | TA MG OS | `accounting_view_income` (:56) |
| 38 | `accounting:billing_document` | tenant | Generate statements and documents | TA MG | `accounting_billing_document` (:57) |
| 39 | `accounting:billing_result` | tenant | Statement registry read | TA MG | `accounting_billing_result` (:58) |
| 40 | `accounting:shopee_report` | tenant | Shopee Express report | TA MG | `accounting_shopee_report` (:59) |
| 41 | `accounting:recompute_force` | tenant | Forced billing recompute (period lock still applies) | TA | — (`.vibe-rules.md` Confirmed Patterns, as of commit 4f552099: `forceRecompute` = admin only) |
| 42 | `accounting:override_price` | tenant | Manual price on one trip (the `EditBillingDialog` exception) | TA | — (`.vibe-rules.md` Confirmed Patterns "ราคาเที่ยวคิดที่ server เท่านั้น", as of commit 4f552099) |
| 43 | `accounting:manage_statements` | tenant | Mark statement sent / paid / cancelled; delete draft | TA MG | — (`firestore.rules:493-515` allow admin + manager) |
| 44 | `reporting:view_analytics` | tenant | Analytics pages | TA MG | `reporting_view_analytics` (:62) |
| 45 | `hr:view_payroll` | tenant | Payroll runs and line items | TA | `hr_view_payroll` (:65) |
| 46 | `hr:manage_payroll` | tenant | Generate / approve payroll, penalties, compensation config | TA | `hr_manage_payroll` (:66) |
| 47 | `hr:view_leave` | tenant | Leave requests | TA | `hr_view_leave` (:67) |
| 48 | `hr:manage_leave` | tenant | Approve / reject leave | TA | `hr_manage_leave` (:68) |
| 49 | `hr:manage_holidays` | tenant | Tenant (COMPANY) holidays; PUBLIC rows need a steward | TA | `hr_manage_holidays` (:69) |
| 50 | `company:view` | tenant | Issuer company profiles of the tenant | TA MG | `company_view` (:72) |
| 51 | `company:manage` | tenant | Edit company profile, logo / stamp / signature | TA MG | `company_manage` (:73) |
| 52 | `users:view` | tenant | User list of the tenant, sessions summary | TA MG PA SU | row `users_view` (:159); read half of `security_manage_users` (:40) |
| 53 | `users:manage` | tenant | Create / invite / edit / disable users, temporary passwords | TA PA | `security_manage_users` (:40); rows `users_create`, `users_edit` (:160-161) |
| 54 | `users:assign_role` | tenant | Change membership roles and customer scopes (privilege escalation; no tenant override, platform-wide override only) | TA PA | row `users_assign_role` (:162) |
| 55 | `users:revoke_sessions` | tenant | Revoke another user's sessions | TA PA | — (`revokeUserRefreshTokens`, `logitrack-web/functions/src/authSessions.ts:9`) |
| 56 | `security:view_overview` | tenant | Security Center overview | TA PA | `security_view_overview` (:39); row (:184) |
| 57 | `security:manage_roles` | tenant | Edit the tenant role matrix (overrides) | TA PA | `security_manage_roles` (:41); row (:186) |
| 58 | `security:view_audit` | tenant | `security_events` of the tenant (platform: all, through `X-Act-On-Tenant: *`) | TA PA SU | `security_view_audit` (:42); row (:187) |
| 59 | `security:manage_api_keys` | tenant | Create / revoke tenant API keys | TA PA | `security_manage_api_keys` (:43); row (:188) |
| 60 | `security:view_status` | global | System status (queues, DLQ depth, scheduler leader) | TA PA SU | `security_view_status` (:44); row (:189) |
| 61 | `security:view_mobile_clients` | tenant | Mobile installations of the tenant's drivers | TA PA SU | `security_view_mobile_clients` (:45); row (:190) incl. PARTNER column |
| 62 | `security:manage_mobile_release` | global | Set the platform-wide minimum driver-app version | TA PA | `security_manage_mobile_release` (:46); row (:191) |
| 63 | `waitlist:view` | global | Product waitlist and partner-interest submissions (prospect PII) | TA MG PA | `waitlist_view` (:76) |
| 64 | `packages:view` | tenant | Packages page (static content) | TA MG OS | `packages_view` (:77) |
| 65 | `mobile:view_tasks` | self | Own task queue (incl. tasks where the driver is helper), task detail | DR | row `view_assigned_tasks` (:102) |
| 66 | `mobile:checkin` | self | Check-in, truck confirmation, manual task creation | DR | row `checkin_task` (:108) |
| 67 | `mobile:submit_trip` | self | Create / update own trip, photos, stop progress, delivery | DR | rows `create_trip_record` (:114), `update_trip_record` (:120), `submit_delivery` (:126) |
| 68 | `mobile:submit_standby` | self | Submit standby records | DR | — |
| 69 | `mobile:report_incident` | self | Report incidents on own trips | DR | row `report_incident` (:144) |
| 70 | `mobile:submit_expense` | self | Fuel / other expenses | DR | row `submit_vehicle_expense` (:150) |
| 71 | `mobile:submit_maintenance` | self | Read / update maintenance of the active or home truck | DR | row `maintenance_logs` (:86) DRIVER column |
| 72 | `mobile:leave_request` | self | Create / cancel own leave requests | DR | — |
| 73 | `mobile:chat` | self | Own chat with admins | DR | row `send_chat` (:132); rows `chat_view`/`chat_send` DRIVER column |
| 74 | `mobile:broadcasts` | self | Read broadcasts, mark read | DR | row `read_broadcasts` (:138) |
| 75 | `mobile:view_history` | self | Own trip / standby / payroll history | DR | — |
| 76 | `mobile:create_hub` | self | Create a driver hub (`hubs.created_by_driver = true`) from manual check-in | DR | — (R5; drafts named it `operations:create_hub_as_driver`; today any signed-in user may create hubs, `firestore.rules:336-340`) |
| 77 | `dispatch:view_operations` | scope | Cross-tenant operational projection (boards, monitor, standby, incidents) | DS | — |
| 78 | `platform:manage_tenants` | platform | Create tenants, change kind / status, suspend | PA | — |
| 79 | `platform:manage_platform_roles` | platform | Grant / revoke platform roles and dispatcher grants | PA | — |
| 80 | `platform:cross_tenant_read` | platform | `X-Act-On-Tenant` reads; quarantine row listing | PA SU | — |
| 81 | `platform:cross_tenant_write` | platform | `X-Act-On-Tenant` writes; quarantine re-home | PA | — |

Dropped legacy entries: matrix row `delete_records` (:92) has no key (deletion rights are carried by each domain's manage key); the `user` role's role-matrix absence (`roles.ts:27-35`) and the `subcontractor` badge role (`logitrack-web/app/app/security-center/users/page.tsx:638`) have no equivalent.

### C.2.4 Role × capability matrix

Counts are default grants; "effective" applies the class rule (global keys only for stewards). Numbers refer to rows of C.2.3.

| Role | Keys (C.2.3 rows) | Count | Derived from |
|---|---|---|---|
| `tenant_admin` | 1-64 | 64 (global rows 7, 8, 24, 25, 60, 62, 63 effective only in the own-fleet tenant) | `roles.ts:44` admin `"*"`; legacy `partner` maps here (D2) |
| `manager` | 1-11, 12-15, 17-20, 21-24, 26-29, 30-40, 43, 44, 50, 51, 52, 63, 64 | 45 (= 36 from `roles.ts:45-82` + 5, 10, 11, 15, 19, 20, 23, 43, 52). Excluded: 16, 25, 41, 42, 45-49, 53-62 | `roles.ts`; matrix rows `assign_driver`, `broadcasts_send`, `users_view` default MANAGER=true (`roles/page.tsx:80, 169, 159`); `billing_statements` rules already allow manager (`firestore.rules:493-515`) |
| `operation_staff` | 1, 4, 6, 9, 10, 11, 12, 17, 18, 20, 21, 22, 23, 24, 26, 27, 28, 29, 30, 32, 34, 35, 37, 64 | 24 (= 19 from `roles.ts:83-103` + 10, 11, 20, 23, 28) | adds `operations:view_incidents` (operator had it, operation_staff did not); matrix `assign_driver` / `broadcasts_view` OPERATION_STAFF=true |
| `operator` | 1, 4, 6, 9, 10, 11, 12, 17, 18, 20, 21, 22, 23, 24, 26, 27, 28 | 17 (= 13 from `roles.ts:104-118` + 10, 11, 20, 23) | UNVERIFIED: that operators should create tasks (row 23); they can today only because the boards write Firestore directly |
| `user` | 1, 12 | 2 | `roles.ts:131-134`; fallback for a membership with no operational rights |
| `driver` | 65-76 | 12 | `roles.ts:135` `[]` + matrix OPERATIONS rows DRIVER=true; matrix `chat_*` DRIVER=true becomes `mobile:chat` |
| customer scope | 21, 22, 26, 28 | 4 | `roles.ts:119-124`; matrix `chat_*` CUSTOMER=true is dropped (no customer chat UI exists) |
| dispatcher scope | 11, 21, 22, 26, 28, 77 | 6 | glossary "Dispatcher" |
| `platform_admin` | 52-63, 78-81 | 16 (users 4 + security 7 + waitlist + platform 4); plus the `tenant_admin` set inside a tenant entered with `X-Act-On-Tenant` | glossary "Platform admin" |
| `support` | 52, 58, 60, 61, 80 | 5 | new, read-only; also reads every `jobs` row through `GET /v1/jobs` (service-enforced, not a capability key; Appendix B §B.2.20, T10) |

Effective set of a principal = (defaults of its active tenant role, after overrides, minus `global` keys unless steward) ∪ (scope sets) ∪ (platform sets). The customer-scope set applies only to a principal without a membership (C.1.2): customer-kind scopes of a member are ignored by capabilities and by RLS (`app.role` stays the tenant role and `app.customer_ids` is not set; T07), because RLS would serve the scope keys with the member's staff reach and never with the scope's rows. The dispatcher set comes with the membership of its own organisation (R13). For T19: `PUT /v1/users/{id}/scopes/customer` should refuse a user with an active membership (unless the owner decides on an explicit member-plus-customer model), and token issuance must stop merging customer-kind parties into a dispatcher's `cs` (T05's `claims()` puts both kinds into one `cs` and sets `dsp=true` when any scope is a dispatcher scope, which would give a user holding both kinds dispatcher reach over its customer-kind parties). `isAdmin` has one meaning per context: `p.TenantRole == TenantAdmin` for tenant actions, `p.HasPlatform(PlatformAdmin)` for platform actions.

### C.2.5 Per-tenant overrides (replaces `permissions_config`)

Table `role_capability_overrides` (Appendix A `0002_identity`, §A.2.1): `tenant_id` (NULL = platform-wide default override, platform_admin only), `role` (CHECK over the six tenant roles), `capability` (format CHECK `^[a-z]+:[a-z_]+$`; existence in the Go catalog is validated by the API and a CI test), `allowed`, `updated_by`, `updated_at`; constraints `rco_not_grantable` (no `platform:*` / `dispatch:*`), `rco_assign_role_platform` (`users:assign_role` only in a platform-wide row) and `rco_key UNIQUE NULLS NOT DISTINCT (tenant_id, role, capability)`.

- Resolution: catalog default for the role -> platform-wide override (`tenant_id IS NULL`) -> tenant override. `tenant_admin` is not overridable (the DDL CHECK admits the value; the API rejects it with `422 capability_not_overridable`), which prevents a tenant from locking itself out of `security:manage_roles`.
- Non-grantable: `platform`, `scope` classes (DDL); `users:assign_role` except platform-wide (DDL); `global` keys except in an override whose tenant is the own-fleet tenant or `NULL` (API). The API rejects such rows with `422 capability_not_overridable`.
- Saving the matrix (`PUT /v1/roles/matrix`) writes the override rows and one `security_events` row `role_matrix_saved` whose `details` carry the full before/after diff (today `{roleDocsUpdated: 7}`, `roles/page.tsx:400-409`), in the same transaction, then `INCR lt:{APP_ENV}:rbac:ver`.
- Cache: `lt:{APP_ENV}:rbac:caps:{tenant_id|platform}:{role}:{rbac_ver}:{fp}` (JSON array, TTL 10 min; key shape of Appendix B §B.6.2). Both inputs of the set are in the key: the version, so a save takes effect on the next request without explicit deletes, and `fp` = `authz.RoleSetFingerprint` (8 hex digits of sha256 over the default set of every tenant role, the key and class of every catalog entry and a rules version bumped with any change to the override rules), so a release that narrows or widens a default, or a rollback, never reads a set cached by another release, even while old and new pods run side by side (T07). The TTL only bounds what an `INCR` lost to a Redis outage leaves behind. Capabilities are never in the JWT.
- `GET /v1/roles/matrix` returns `{default, effective, overridden}` per key so the UI can diff and "discard" restores the loaded state (fixes `roles/page.tsx:422-426`).

### C.2.6 `permissions_config` migration (ETL step `auth-rbac`)

Input: every `permissions_config/{role}` doc, field `capabilities: {key: boolean}` (written by `roles/page.tsx:385-398`). UNVERIFIED: whether live docs also contain colon keys from an older writer (no such writer exists in current code; Firestore not readable from the research environment) — colon keys are accepted as-is.

Key translation (matrix row id or code key -> canonical):

| Legacy key(s) | Canonical |
|---|---|
| `create_truck` | `fleet:create_truck` |
| `edit_telemetry` | `fleet:edit_truck` |
| `view_live_map` | `fleet:view_live_map` |
| `assign_driver` | `fleet:manage_assignments` |
| `maintenance_logs` | `fleet:manage_maintenance` (staff roles); `mobile:submit_maintenance` (driver) |
| `delete_records` | dropped (report only) |
| `view_assigned_tasks` | `mobile:view_tasks` |
| `checkin_task` | `mobile:checkin` |
| `create_trip_record`, `update_trip_record`, `submit_delivery` | `mobile:submit_trip` (allowed if any is true) |
| `send_chat` | `mobile:chat` |
| `read_broadcasts` | `mobile:broadcasts` |
| `report_incident` | `mobile:report_incident` |
| `submit_vehicle_expense` | `mobile:submit_expense` |
| `users_view` | `users:view` |
| `users_create`, `users_edit`, `security_manage_users` | `users:manage` (allowed if any is true) |
| `users_assign_role` | `users:assign_role` (no tenant override exists for it: report only) |
| `chat_view`, `chat_send` | `chat:view`, `chat:send` for staff roles; `mobile:chat` for driver; dropped for customer / partner |
| `broadcasts_send`, `broadcasts_view` | `broadcasts:send`, `broadcasts:view` |
| `accounting_*` (5 rows), `security_*` (8 rows except `security_manage_users`) and every code key | same name, underscore after the module replaced by a colon (`accounting_view_fuel` -> `accounting:view_fuel`) |

Rules: only the role docs `manager`, `operation_staff`, `operator`, `driver` produce overrides (admin -> `tenant_admin` is not overridable; `partner` -> `tenant_admin`; `customer` scope is fixed). An override row (`tenant_id` = own fleet) is written only where the saved boolean differs from the new default. Unknown keys go to `etl.quarantine` with reason `unknown_capability_key`. Output `migration_rbac_report.csv` (role, legacy key, canonical key, saved, new default, action) is signed off by the owner before the Security Center cut-over (P6). Because the matrix only ever took effect for `security_view_audit`, importing differences may change live behaviour; the report is the review point.

### C.2.7 Route -> capability map and the web edge gate

`ROUTE_CAPABILITIES` (`logitrack-web/lib/capabilities.ts:340-394`, 53 entries) is regenerated from the catalog into the web. The Go copy is `internal/authz/webroutes.go` (T07), generated with the catalog into `shared-docs/schemas/capabilities.ts` (`go generate ./internal/authz`, checked by `make gen-check`); it carries the changes below and those of main spec §10.5, and `internal/authz/webroutes_test.go` checks it against the 53 legacy entries, frozen in `internal/authz/testdata/legacy_route_capabilities.json` (compared with the live `lib/capabilities.ts` for as long as that still holds the table: TW3 moves and re-keys it, and the generated `lib/routeCapabilities.ts` comes from `webroutes.go`, so neither can be the reference), and walks every `app/app/**/page.tsx` at run time: a page without a mapping or a legacy entry without a translation fails the Go tests. The go-ci `changes` job also runs the Go jobs for a change under `logitrack-web/app/app/` or to `logitrack-web/lib/capabilities.ts` (main spec §17.2), so a web-only pull request that adds an unmapped page fails before it merges, not on the next push to `mv-go`. Changes:

| Route | Today | New |
|---|---|---|
| `/app/security-center/*` | admin-only regardless of capability (`permissions.ts:55-57`) | mapped capability only: index `security:view_overview`, `/users` `users:view` (create/edit dialogs `users:manage`, role field `users:assign_role`), `/mobile-clients` `security:view_mobile_clients`, `/mobile-release` `security:manage_mobile_release` |
| `/app/dashboard` | open to any authenticated user | unchanged |
| unmapped routes | allowed (`permissions.ts:72`) | **denied**; a CI test fails when a page under `app/app/**` has no mapping |
| `/app/job-assign`, `/app/first-mile`, `/app/line-haul` write actions | page-level `operations:view_*` | buttons gated by `operations:manage_tasks` |
| `/app/accounting/billing-result` status actions | page capability only | `accounting:manage_statements` |
| `/app/utilities/*` | `security:view_overview` | `accounting:recompute_force` (backfills / billing impact) |

Edge gate (`proxy.ts`, W4, R39): after verifying `lt_at` against the Go JWKS it calls the internal `GET /v1/me` (cached in the web server process per `(sid, ver, tid)` for 60 s, so a role change, which bumps `ver`, and a tenant switch, which changes `tid`, are seen on the next navigation, and an override within 60 s) and checks the route's entry in `ROUTE_CAPABILITIES`, which moves out of `lib/capabilities.ts` into the edge module and is re-keyed to colon keys. A denied route redirects before any page code or query runs (fixes fetch-before-guard, plan §4b.1); the exact path `/app` is redirected to the role's home route (`getDefaultRouteForRole`, R89). Inside a page, `['me'].capabilities` only gates buttons and dialogs (`PagePermissionGuard` stays as a UI helper). Go enforces on every request; the edge check and the UI gating never widen what Go allows.

### C.2.8 Enforcement in Go (Fiber v3)

```go
// internal/authz/principal.go
type Principal struct {
    UserID      uuid.UUID
    SessionID   uuid.UUID        // zero for amr=firebase and amr=apikey
    AuthVersion int32
    AMR         string           // "pwd" | "google" | "firebase" (cf_shim request, C.6.2) | "apikey"

    TenantID    *uuid.UUID       // `tid`; nil for customer-scope and platform-only principals
    TenantKind  string           // own_fleet | carrier
    TenantRole  TenantRole       // "" when TenantID == nil
    Platform    []PlatformRole   // platform_admin, support
    Dispatcher  bool             // `dsp`
    DriverID    *uuid.UUID       // `drv`
    PartyIDs    []uuid.UUID      // `cs`: billing_parties.id (customer scope or dispatcher grant)
    Steward     bool             // (TenantKind == own_fleet && staff role) || HasPlatform(PlatformAdmin) || ActOnTenant != nil

    SubtenantIDs []uuid.UUID     // contractor reach of the effective tenant, staff only (C.3.4); GUC app.subtenant_ids
    Caps        CapSet           // resolved per request from catalog + overrides (Redis), never from the token
    ActOnTenant *uuid.UUID       // set only by RBAC.Authorize (X-Act-On-Tenant uuid form)
    ActOnAll    bool             // set only by RBAC.Authorize ("*" form): read-only bypass
    APIKeyID    *uuid.UUID       // set when the request carried X-Api-Key (T32)
    APIKeyScope APIKeyScope      // api_keys.scope of that key (R82)
    APIKeyCaps  []Cap            // api_keys.capabilities of a machine principal (amr=apikey)
}

func (p *Principal) Can(c Cap) bool                { return p.Caps.Has(c) }
func (p *Principal) HasPlatform(r PlatformRole) bool
func (p *Principal) EffectiveTenant() *uuid.UUID   { if p.ActOnTenant != nil { return p.ActOnTenant }; return p.TenantID }

// internal/auth/middleware.go (T05: a method of auth.Service, because internal/platform/httpx cannot import internal/auth)
func (s *Service) RequireAuth() fiber.Handler        // JWT | Firebase ID token + cf_shim key | X-Api-Key -> Principal; 401 codes in C.4.3;
                                                     // then auth.Deps.Authorizer (iam.RBAC, T07) completes the principal
// internal/iam/authorize.go (T07): RBAC.Authorize runs inside RequireAuth for every authenticated request
func (r *RBAC) Authorize(c fiber.Ctx, p *authz.Principal) error // X-Act-On-Tenant (C.3.9; the RequireCrossTenant of the drafts),
                                                     // then Resolve: tenant kind, Steward, SubtenantIDs, Caps (rbac:caps:{tid}:{role}:{rbac:ver}:{fp})
// internal/authz/http.go (T07; authz imports httpx, so the guards live next to the principal)
func RequireCap(caps ...authz.Cap) fiber.Handler     // any-of; 403 {"error":{"code":"permission_denied","message":"...","details":{"missingCapability":["operations:manage_tasks"]},"requestId":"..."}}
func RequireTenant() fiber.Handler                   // 403 tenant_required when EffectiveTenant() == nil (X-Act-On-Tenant: * passes)
func RequirePlatform(r authz.PlatformRole) fiber.Handler
func RequireSteward() fiber.Handler                  // implied by every global-class key; explicit for platform-wide rows

// route registration example
tasks := v1.Group("/tasks", authSvc.RequireAuth(), authz.RequireTenant())
tasks.Post("/", authz.RequireCap(authz.OperationsManageTasks), h.CreateTask)
tasks.Get("/", authz.RequireCap(authz.OperationsViewFirstMile, authz.OperationsViewLineHaul), h.ListTasks)
```

The cross-tenant step is part of the authorization of every authenticated request rather than a guard a route may forget: a route that does not want platform principals simply holds no key they have. A principal that was not resolved (no Authorizer wired) holds no capabilities, so every `RequireCap` refuses and `db.WithPrincipal` sets neither `app.steward` nor `app.subtenant_ids`: it fails closed.

Policy helpers for what RLS cannot express (`internal/authz/policy.go`):

```go
func CanEditTrip(p *Principal, t *domain.Trip, lock billing.PeriodLock) error {
    if !p.Can(OperationsEditTripDetails) { return ErrForbidden(OperationsEditTripDetails) }
    if lock.IsLocked(t.BillingPartyID, t.BillingAxisDate) { return ErrPeriodLocked(lock.InvoiceNumber) } // ADR 0008 §5
    return nil
}
func CanAssignRole(p *Principal, target TenantRole) error {
    if !p.Can(UsersAssignRole) { return ErrForbidden(UsersAssignRole) }
    if target == TenantAdmin && p.TenantRole != TenantAdmin && !p.HasPlatform(PlatformAdmin) { return ErrForbidden(UsersAssignRole) }
    return nil // no self-escalation: the service also rejects target user == p.UserID
}
func CanCheckIn(p *Principal, t *domain.Task) error { // R28: ownership by driver id only
    if p.DriverID == nil || !(eq(t.DriverID, p.DriverID) || eq(t.HelperDriverID, p.DriverID)) { return ErrNotTaskOwner }
    return nil
}
```

Scope is enforced in the data layer, not in handlers: every repository call runs inside `db.WithPrincipal(ctx, p, fn)` (C.3.2).

---

## C.3 Tenant isolation: RLS policies

### C.3.0 RLS coverage table

The single answer to "does table X have RLS, and which policies" (R67): every table of Appendix A, 81 in schema `public` (70 with RLS, 11 exempt) and the 4 of schema `etl` (exempt). Appendix A enables and forces RLS on exactly the "yes" rows. **Each table's `CREATE POLICY` statements (literal, or through the generator calls of C.3.5, which name every table) ship in the same Appendix A migration file as the table (0002-0009), after its `CREATE TABLE`.** The generators (C.3.5) and `trg_freeze_tenant_id()` (C.3.6) are appended to `0001_preamble`, whose GUC helpers Appendix A §A.2.0 prints (C.3.3); the other objects this appendix adds (SECURITY DEFINER helpers, C.3.6 triggers, C.3.7 views) to the Up section of the file named next to them. A later migration (0010+) that adds a table adds a row here and its policies in its own file; the catalog test (C.3.8) compares the database with this table.

Policy families (the bold names abbreviate the generator calls of C.3.5 in the Policies column):

| Family | Meaning | Mechanism |
|---|---|---|
| tenant | `tenant_id NOT NULL`; staff see their tenant and its sub-tenants (C.3.4); quarantine rows invisible; `tenant_id` frozen | **G-tenant** = `app_rls_tenant_table`: `p_bypass`, `p_not_quarantine` (restrictive), `p_tenant_staff`, trigger `t_freeze_tenant_id` |
| parent-scoped | no `tenant_id`; readable when the parent row is visible to the caller, writable when the parent is in the caller's reach | **G-child** = `app_rls_child_table`: `p_bypass`, `p_parent_read`, `p_parent_write`; or G-base plus explicit parent `EXISTS` policies |
| nullable-tenant | `tenant_id` NULL = platform-wide row | G-base plus explicit policies |
| global-read-steward-write | platform-shared master data; any authenticated principal reads, stewards write (R60) | **G-global** = `app_rls_global_table`: `p_bypass`, `p_read`, `p_steward_write` |
| platform-only | served through `WithSystem` (auth, iam, security, storage services) or a SECURITY DEFINER allocator; principals see at most the self / reach rows named | **G-base** = `app_rls_platform_table`: `p_bypass` |
| exempt-service-layer | RLS off; explicit GRANTs at the 0009 grant site (C.3.2); the named service is the only access path | none |

On tenant tables the uniform `p_driver_read` / `p_driver_insert` / `p_driver_update` come from `app_rls_driver_rows` (C.3.5); the other names are literal policies.

| Table | RLS | Family | Policies | Notes |
|---|---|---|---|---|
| `tenants` (0002) | yes | tenant (keyed on `id`) | G-base, `p_read`, `p_update_own` | quarantine row readable only under bypass; `kind`, `status`, `code`, `contractor_tenant_id` platform-only (iam, `WithSystem`); trigger `t_tenant_admin_columns` |
| `tenant_files` (0002) | yes | tenant | G-tenant, `p_steward` | carrier documents; stewards (`fleet:manage_subcontractors`) reach every carrier |
| `users` (0002) | yes | platform-only | G-base, `p_self_read`, `p_self_update`, `p_staff_read` | iam writes under `WithSystem`; `PATCH /v1/me` limited by trigger `t_users_self_columns` |
| `file_objects` (0002) | yes | nullable-tenant | G-base, `p_read`, `p_upload`, `p_commit` | NULL tenant = platform object (APK, unattributed legacy object); cross-tenant readers go through the storage service; outside `WithSystem` an update may only commit (`t_file_objects_commit_columns`, C.3.6; `storage_backend` fixed too since 0011) |
| `auth_identities`, `user_platform_roles` (0002) | yes | platform-only | G-base, `p_self_read` | auth / iam services write |
| `sessions` (0002) | yes | platform-only | G-base, `p_self_read` | auth service; staff views through iam |
| `refresh_tokens`, `password_reset_tokens` (0002) | yes | platform-only | G-base | auth service; reset tokens also the `notify.email` consumer |
| `api_keys` (0002) | yes | platform-only | G-base, `p_staff_read` | NULL tenant = platform key; `scope` per C.4.11 (R82); iam writes after `security:manage_api_keys` |
| `memberships` (0002) | yes | platform-only | G-base, `p_self_read`, `p_staff_read` | iam writes |
| `role_capability_overrides` (0002) | yes | platform-only | G-base, `p_staff_read` | NULL tenant = platform-wide override |
| `status_history` (0002) | yes | parent-scoped (polymorphic) | G-base, `p_entity` | append-only (trigger + REVOKE) |
| `customers`, `customer_driver_id_types` (0003) | yes | global-read-steward-write | G-global | `fleet:manage_customers` |
| `billing_parties` (0003) | yes | global-read-steward-write | G-global | its `tenant_id` (kind `tenant`) is a reference, not a tenancy stamp; on the catalog test's explicit list |
| `companies` (0003) | yes | tenant | G-tenant | issuer profiles of the tenant |
| `user_scopes` (0003) | yes | platform-only | G-base, `p_self_read` | iam writes |
| `hubs` (0003) | yes | global-read-steward-write | G-global, `p_driver_hub_insert` | `mobile:create_hub` |
| `hub_name_aliases` (0003) | yes | global-read-steward-write | G-global, `p_driver_hub_alias_insert` | |
| `hub_soc_distances` (0003) | yes | global-read-steward-write | G-global | distance job writes under `WithSystem` |
| `drivers` (0003) | yes | tenant | G-tenant, `p_self_read`, `p_self_update`, `p_scope_read` | triggers `t_driver_self_columns`, `t_driver_link_membership` |
| `driver_customer_codes` (0003) | yes | parent-scoped (`drivers`, staff read) | G-child | |
| `trucks` (0003) | yes | tenant | G-tenant, `p_driver_read`, `p_scope_read` | |
| `truck_files` (0003) | yes | parent-scoped (`trucks`, staff read) | G-child | |
| `truck_assignments` (0003) | yes | tenant | G-tenant, `p_driver_read` | |
| `task_number_counters` (0004) | yes | platform-only | G-base | no grant to `logitrack_app`; written only by `next_task_seq()` (R10) |
| `tasks` (0004) | yes | tenant | G-tenant, `p_driver_read`, `p_driver_update`, `p_driver_insert`, `p_scope_read` | |
| `task_delivery_stops` (0004) | yes | parent-scoped (`tasks`, driver writes) | G-child | |
| `trip_records` (0004) | yes | tenant | G-tenant, `p_driver_read`, `p_driver_insert`, `p_driver_update`, `p_scope_read` | |
| `trip_no_history` (0004) | yes | parent-scoped (`trip_records`, staff read) | G-child | append-only |
| `trip_delivery_stops`, `trip_photos` (0004) | yes | parent-scoped (`trip_records`, driver writes) | G-child | |
| `standby_records` (0004) | yes | tenant | G-tenant, `p_driver_read`, `p_driver_insert`, `p_scope_read` | inline `billing_*` columns hidden by a service projection (C.3.4, R61) |
| `standby_photos` (0004) | yes | parent-scoped (`standby_records`, driver writes) | G-child | |
| `incident_reports` (0004) | yes | tenant | G-tenant, `p_driver_read`, `p_driver_insert`, `p_driver_update`, `p_scope_read` | |
| `vehicle_locations` (0004) | yes | tenant | G-tenant, `p_dispatch_read` | written by `cartrack.sync` under `WithSystem` |
| `customer_rate_entries`, `customer_fuel_rate_adjustments`, `customer_service_fees`, `standby_rate_entries` (0005) | yes | tenant | G-tenant | billing carrier's tenant (R61); carrier-internal |
| `trip_billing_snapshots` (0005) | yes | tenant | G-tenant | billing carrier stamped by `billing.compute` |
| `trip_billing_stop_breakdown` (0005) | yes | parent-scoped (`trip_billing_snapshots` by `trip_id`, staff read) | G-child | |
| `billing_counters` (0005) | yes | platform-only | G-base | carries the billing carrier's `tenant_id` + `tenant_source` (R61); no grant to `logitrack_app`; written only by `next_invoice_seq()` |
| `billing_statements` (0005) | yes | tenant | G-tenant | billing carrier |
| `billing_statement_lines` (0005) | yes | parent-scoped (`billing_statements`, staff read) | G-child | append-only except cascade from a draft |
| `statement_documents` (0005) | yes | parent-scoped (`billing_statements`, staff read) | G-child | `documents.render` writes under `WithSystem` |
| `vehicle_expenses` (0006) | yes | tenant | G-tenant, `p_driver_read`, `p_driver_insert`, `p_driver_update_pending` | |
| `maintenance_records` (0006) | yes | tenant | G-tenant, `p_driver_read`, `p_driver_update` | |
| `maintenance_files` (0006) | yes | parent-scoped (`maintenance_records`, driver writes) | G-child | |
| `transactions` (0006) | yes | tenant | G-tenant | append-only |
| `driver_compensation_configs` (0006) | yes | tenant | G-tenant | |
| `penalty_types` (0006) | yes | parent-scoped (`driver_compensation_configs`, staff read) | G-child | |
| `driver_penalties` (0006) | yes | tenant | G-tenant | staff only (admin-only read today, `firestore.rules:469-472`) |
| `payroll_runs` (0006) | yes | tenant | G-tenant, `p_driver_read` | |
| `payroll_line_items`, `payroll_penalty_applications` (0006) | yes | parent-scoped (`payroll_runs`) | G-child | a driver reads own lines through the parent |
| `driver_advances` (0006) | yes | tenant | G-tenant | table shipped, UI deferred (R30) |
| `device_tokens` (0007) | yes | parent-scoped (`users` by `user_id`) | G-base, `p_self` | |
| `chats` (0007) | yes | tenant | G-tenant, `p_driver_read`, `p_driver_insert`, `p_driver_update` | |
| `chat_messages`, `chat_read_state` (0007) | yes | parent-scoped (`chats`) | G-base, `p_parent_read`, `p_own_write` | |
| `broadcasts` (0007) | yes | nullable-tenant | G-base, `p_read`, `p_staff_write` | NULL = platform-wide (steward write, R60) |
| `broadcast_recipients`, `broadcast_reads` (0007) | yes | parent-scoped (`broadcasts`) | G-base, `p_read`, `p_write` | |
| `notification_deliveries` (0007) | no | exempt-service-layer | — | `notify.fcm` consumer writes (kind `session_revoked` included, R84); platform status pages read |
| `settings` (0008) | no | exempt-service-layer | — | config service; `GET /v1/mobile/settings` serves non-sensitive fields anonymously (R42); floor writes only through `PUT /v1/app-releases/floor` |
| `mobile_app_releases` (0008) | no | exempt-service-layer | — | `POST /v1/app-releases` from the release CLI on the private network (R43) |
| `security_events` (0008) | yes | nullable-tenant | G-base, `p_staff_read` | append-only; written only by `internal/security` under `WithSystem` |
| `mobile_installations` (0008) | yes | tenant | G-tenant, `p_driver_read`, `p_driver_insert`, `p_driver_update` | |
| `holidays` (0008) | yes | nullable-tenant | G-base, `p_read`, `p_write` | NULL = PUBLIC (steward write, R60); `holiday_type='public'` iff `tenant_id IS NULL` (Appendix A CHECK) |
| `leave_requests` (0008) | yes | tenant | G-tenant, `p_driver_read`, `p_driver_insert`, `p_driver_update` | |
| `leave_request_attachments` (0008) | yes | parent-scoped (`leave_requests`, driver writes) | G-child | |
| `waitlist`, `partner_interest` (0008) | no | exempt-service-layer | — | anonymous `POST /v1/waitlist`, `POST /v1/partner-interest` (internal listener, reached through the BFF route handlers `POST /api/forms/waitlist`, `POST /api/forms/partner-interest`, `RATE_LIMIT_PUBLIC_FORMS`, R44, R77); reads need `waitlist:view` + steward, checked by the service |
| `fuel_daily_snapshots`, `fuel_monthly_snapshots` (0008) | no | exempt-service-layer | — | scheduler writes (daily append-only, monthly upsert on success); fuel pages read |
| `outbox_events` (0009) | no | exempt-service-layer | — | domain transactions insert under `WithPrincipal`; the relay reads and marks under `WithSystem` |
| `consumer_inbox`, `idempotency_keys` (0009) | no | exempt-service-layer | — | consumers; idempotency middleware (the scope carries the user id) |
| `jobs` (0009) | no | exempt-service-layer | — | the jobs service allows `owner_user_id` = caller, staff of the job's tenant holding the job's capability, or platform |
| `etl.source_docs`, `etl.quarantine`, `etl.watermarks`, `etl.reconciliation_runs` (0009) | no | exempt-service-layer | — | schema `etl` is granted to `logitrack_etl` (all) and `logitrack_readonly` (SELECT) only |

### C.3.1 Why RLS, and the division of labour

Predicate injection alone (`WHERE tenant_id = $1` in every sqlc query) was rejected: with 70 RLS tables, ~150 endpoints and five scope axes (tenant, driver self, customer, dispatcher, platform), one forgotten predicate is a silent cross-tenant leak — the same class of defect as today's rules, which let any signed-in user read `tasks`, `trip_records`, `drivers`, `trucks`, `vehicle_locations`, `hubs`, `settings` and `broadcasts` while customer and partner scoping happens only in the browser (evidence: C.7 #8).

| Layer | Enforces |
|---|---|
| PostgreSQL RLS | **which rows** a principal may see or write: tenant reach, driver self-scope, customer / dispatcher scope, quarantine invisibility, platform bypass |
| Triggers | frozen `tenant_id`, cross-row link consistency, column whitelists for self-service writes, append-only / void-only tables (Appendix A immutability rules) |
| Go capabilities (C.2) | **which verbs** a principal may perform (`RequireCap`) |
| Go services + sqlc projections | **which columns** a scope principal receives (C.3.7); business invariants (period lock, frozen price); access to the exempt tables of C.3.0 |

### C.3.2 Database roles and request context

**Roles (R66).** The superuser init script `deploy/postgres-init/00-roles.sql` creates every role once (compose mounts it under `/docker-entrypoint-initdb.d`; CI testcontainers mount the same file; a managed database runs it once from its admin console). Role attributes are per role and are **not** inherited through membership, so `BYPASSRLS` / `NOBYPASSRLS` sit on the roles that log in, and **no process switches role with `SET ROLE`**:

```sql
-- deploy/postgres-init/00-roles.sql (superuser, once). LOGIN passwords are set by the deploy script from the
-- secret store; they are the values embedded in DATABASE_URL, MIGRATE_DATABASE_URL and ETL_DATABASE_URL.
CREATE ROLE logitrack_migrator    LOGIN   NOSUPERUSER NOBYPASSRLS;   -- owns every schema object
CREATE ROLE logitrack_app         LOGIN   NOSUPERUSER NOBYPASSRLS;   -- api, worker, scheduler
CREATE ROLE logitrack_etl         LOGIN   NOSUPERUSER BYPASSRLS;     -- cmd/etl (R12)
CREATE ROLE logitrack_readonly    LOGIN   NOSUPERUSER NOBYPASSRLS;   -- reporting and forensics tools
CREATE ROLE logitrack_rls_definer NOLOGIN NOSUPERUSER BYPASSRLS;     -- owns the SECURITY DEFINER helpers only
GRANT logitrack_rls_definer TO logitrack_migrator WITH INHERIT FALSE; -- SET stays true: 0009 may hand functions over
DO $$ BEGIN EXECUTE format('ALTER DATABASE %I OWNER TO logitrack_migrator', current_database()); END $$;
                                                                     -- pg_database_owner then owns schema public
```

| Process | Login role | Connection |
|---|---|---|
| `api`, `worker`, `scheduler`; `seed --verify` isolation role-play (R87) | `logitrack_app` | `DATABASE_URL` |
| `migrate` (goose); `seed --reset` `TRUNCATE` and schema-version check | `logitrack_migrator` | `MIGRATE_DATABASE_URL` |
| `etl`; `seed` writes (same load path, R87) | `logitrack_etl` | `ETL_DATABASE_URL` |
| reporting tools | `logitrack_readonly` | operator-held, outside the Go env; RLS tables only after a logged `app.bypass_tenant` |

- `0001_preamble` only **asserts** the roles: it raises unless `current_user = 'logitrack_migrator'` and the four other roles exist with the attributes above (`rolcanlogin`, `rolbypassrls`, `NOT rolsuper`) and the migrator may `SET ROLE logitrack_rls_definer`. Objects are therefore always owned by `logitrack_migrator`.
- `0009_infra` is the **single GRANT site** (printed in Appendix A §A.2.8, `logitrack_readonly` included); it first checks that the RLS layout equals C.3.0 and ends with the SECURITY DEFINER hand-over (C.3.3). `logitrack_app`: USAGE on `public`; SELECT/INSERT/UPDATE/DELETE on every RLS table except the two counters, minus the append-only / void-only REVOKEs of Appendix A §A.1.8; SELECT on the seven `scope_*` views (C.3.7); sequence USAGE/SELECT; EXECUTE on the helpers; on the exempt tables exactly: `outbox_events` SELECT/INSERT/UPDATE/DELETE, `consumer_inbox` SELECT/INSERT/DELETE, `idempotency_keys` SELECT/INSERT/UPDATE/DELETE, `jobs` SELECT/INSERT/UPDATE/DELETE (DELETE for the nightly `jobs.prune`, T10), `notification_deliveries` SELECT/INSERT/UPDATE, `settings` SELECT/INSERT/UPDATE, `mobile_app_releases` SELECT/INSERT, `waitlist` SELECT/INSERT/DELETE, `partner_interest` SELECT/INSERT, `fuel_daily_snapshots` SELECT/INSERT, `fuel_monthly_snapshots` SELECT/INSERT/UPDATE; nothing on the counters or schema `etl`; SELECT on goose's `goose_db_version` (the `/startupz` version gate). `logitrack_etl` (`cmd/etl` and `cmd/seed` writes): SELECT/INSERT/UPDATE/DELETE on `public` and `etl` plus sequence USAGE/SELECT, **no `TRUNCATE`** (it would skip the append-only triggers), same REVOKEs. `logitrack_readonly`: USAGE + SELECT on `public` and `etl`. Function EXECUTE and `logitrack_rls_definer` grants: C.3.3.
- Every RLS table (C.3.0) has `ENABLE ROW LEVEL SECURITY` **and** `FORCE ROW LEVEL SECURITY`, so the owner `logitrack_migrator` obeys policies too; Go data migrations and `cmd/seed` batches run through `db.WithSystem`, which sets `app.bypass_tenant` (the seed never sets `app.etl_load`, so the deferred link triggers still run).
- Every request transaction is opened by `db.WithPrincipal(ctx, p, fn)`; every non-request transaction (worker consumer, scheduler job, outbox relay, `cmd/etl`, `cmd/seed`, login / refresh before a principal exists, identity writes after Go authorization, security-event append, anonymous form endpoints, storage key lookup after entity authorization, the three tenant-move paths) by `db.WithSystem(ctx, pool, tenantID *uuid.UUID, fn func(pgx.Tx) error)` (R12; `pool` is any `db.Beginner`, the logitrack_app pool in the api; shipped by T05 in `internal/platform/db/system.go`, `WithPrincipal` and the analyzer by T07). A CI analyzer (`tools/analyzers/withsystem`, run by `make lint`; standard library only, it parses the module and needs no type checking) fails the build when `WithSystem` is called outside the allow-listed packages (`internal/auth`, `internal/iam`, `internal/security`, `internal/storage`, `internal/public`, `internal/platform/outbox`, `internal/platform/tenancy`, `cmd/worker/...`, `cmd/scheduler/...`, `cmd/etl/...`, `cmd/seed/...`, `internal/platform/db` itself, and from T10: `internal/platform/inbox` (the consumer side-effect transaction with the event's tenant, R12, the counterpart of `platform/outbox`), `internal/notify` (the `notify.*` consumers: `password_reset_tokens` is platform-only, `notification_deliveries` follows with `notify.fcm`), `internal/scheduler` (leader cron runs, job rows and `auth.token-cleanup` on RLS-forced identity tables; the scheduler logic lives here, not under `cmd/scheduler`, main spec §2.3) and `internal/jobs` (rows of the exempt `jobs` table and their `lt.jobs` outbox commands, the in-transaction `security.Append` of `queue_replayed`, and the queue replays the leader runs)); `_test.go` files are exempt (fixtures play the system context as `cmd/seed` does). Two of them hand a system transaction to their callers, so the analyzer (T07) treats them as `WithSystem`-equivalent and reports them outside the same allow-list: a reference to `inbox.Run` (consumer side effects; today `internal/notify` runs it, and domain consumers such as `internal/billing`, `documents` or `cartrack` join the list in their own tasks), and an `InTx` hook of `jobs.Service.Submit`, that is a key `InTx` in a `jobs.SubmitInput` literal (or in one whose type is elided) or a write of `.InTx` in a file that imports `internal/jobs` (an unkeyed `jobs.SubmitInput` outside its package is refused by go vet's composites check); `InTx` may only touch RLS-exempt tables or call `security.Append` (a request that must read tenant rows does so under `WithPrincipal` before it calls `Submit`). The same analyzer also reports, outside their own allow-lists, every reference to `db.RLS` (only `internal/platform/db` and `internal/authz`: a hand-made `RLS()` could ask for the bypass, or any tenant with the steward flag), an `authz.Principal` literal (only `internal/auth`, `internal/iam`, `internal/authz`), a write of `ActOnAll` / `ActOnTenant` (only `internal/iam` and `internal/authz`), and a string literal that names a request GUC (only `internal/platform/db`), `app.tenant_move` (also `internal/platform/tenancy`) or `app.etl_load` (also `cmd/etl`), or calls `set_config` (those three); it matches names, not types, so it stops mistakes, not a deliberate evasion. `WithPrincipal(ctx, pool, p, fn)` takes a `db.Principal` (`authz.Principal` implements it through `RLS()`, which maps the principal kinds of C.1.2 to the GUCs below; a machine principal sets `app.user_id` to its key id, which no `users` row has) and refuses, before `BEGIN`, `app.bypass_tenant` outside a READ ONLY transaction (`db.ErrBypassNeedsReadOnly`) and a nil principal, a typed nil pointer included (`db.ErrNoPrincipal`: `authz.PrincipalFrom` returns a nil `*authz.Principal` on a route mounted without `RequireAuth`).
- Both helpers issue one round trip right after `BEGIN` (transaction-local `set_config`, reset at COMMIT/ROLLBACK, safe with `pgxpool` reuse):

```sql
SELECT set_config('app.user_id',        $1,  true),  -- users.id, '' for system
       set_config('app.tenant_id',      $2,  true),  -- EffectiveTenant(); '' when none
       set_config('app.role',           $3,  true),  -- tenant role | 'customer' (scope-only principal) | 'system' | ''
       set_config('app.driver_id',      $4,  true),  -- drivers.id for driver principals
       set_config('app.customer_ids',   $5,  true),  -- comma-separated billing_parties.id (`cs`)
       set_config('app.subtenant_ids',  $6,  true),  -- carrier tenants whose contractor_tenant_id = active tenant (C.3.4)
       set_config('app.dispatcher',     $7,  true),  -- 'on' | 'off'
       set_config('app.steward',        $8,  true),  -- 'on' for a staff role in the own-fleet tenant, or platform_admin (R60)
       set_config('app.bypass_tenant',  $9,  true);  -- 'on' only for WithSystem and for X-Act-On-Tenant: *
```

`app.tenant_move` is set to `'on'` only by the three explicit tenant-move service methods (task reassignment R13, driver move C.1.4, quarantine re-home C.3.10) inside their own transaction; `app.etl_load` only by `cmd/etl`. A transaction for `X-Act-On-Tenant: *` starts with `BEGIN ISOLATION LEVEL READ COMMITTED READ ONLY`, so the bypass can never write. `app.bypass_tenant` is an ordinary GUC; its protection is that only `db.WithSystem` and the read-only `X-Act-On-Tenant: *` path set it (`iam.RBAC.Authorize` sets `authz.Principal.ActOnAll`, whose `RLS()` gives `db.WithPrincipal` Bypass + ReadOnly; `WithPrincipal` refuses a bypass outside a READ ONLY transaction but otherwise trusts the `db.Principal` it is given, so only request code holding the principal from `RequireAuth` calls it, and the withsystem analyzer keeps `db.RLS`, `authz.Principal` literals, `ActOnAll` / `ActOnTenant` writes and raw GUC SQL inside their packages), and that `logitrack_app` connections never run caller-supplied SQL (sqlc-generated statements only).

### C.3.3 Helper functions

Appendix A §A.2.0 (`0001_preamble`) prints every GUC reader and composed helper the policies use; this appendix fixes their meaning and does **not** create them again. Readers: `app_user_id()`, `app_tenant_id()`, `app_role()`, `app_driver_id()`, `app_customer_ids()` (billing_parties ids), `app_is_dispatcher()`, `app_bypass()`, `app_subtenant_ids()` (`app.subtenant_ids`), `app_is_steward()` (`app.steward`), `app_tenant_move_allowed()` (`app.tenant_move`), `app_etl_load()` (`app.etl_load`). Composed: `app_is_staff()` (role in `tenant_admin`, `manager`, `operation_staff`, `operator`, `user`), `app_is_scope()` (`app_role() = 'customer' OR app_is_dispatcher()`), `app_is_authenticated()` (`app_user_id() IS NOT NULL OR app_bypass()`), `app_tenant_in_reach(t)` (`t = app_tenant_id() OR t = ANY (app_subtenant_ids())`) and the IMMUTABLE `app_quarantine_tenant_id()` (the fixed id of C.1.8). All are SQL-standard bodies and `PARALLEL SAFE`; an unset GUC yields NULL / `''` / false, so a forgotten `WithPrincipal` sees zero rows instead of all rows.

Scope helpers are `SECURITY DEFINER` (owned by `logitrack_rls_definer`, BYPASSRLS), so a policy on `tasks` can consult `task_delivery_stops` without the policies of the two tables referencing each other (which PostgreSQL rejects as infinite recursion); they exclude the quarantine tenant themselves because they bypass RLS. They are PL/pgSQL, whose table references bind at first execution, so they can be created in `0003_master` (where the `drivers` and `trucks` scope policies need them) although they read tables created in `0004`:

```sql
-- 0003_master.sql (before the drivers / trucks policies)
CREATE FUNCTION app_task_stops_in_scope(p_task_id uuid) RETURNS boolean
  LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  RETURN EXISTS (SELECT 1 FROM task_delivery_stops s
                  WHERE s.task_id = p_task_id AND s.destination_linked_party_id = ANY (app_customer_ids()));
END $$;

CREATE FUNCTION app_task_in_scope(p_task_id uuid) RETURNS boolean
  LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  RETURN cardinality(app_customer_ids()) > 0 AND EXISTS (
    SELECT 1 FROM tasks t
     WHERE t.id = p_task_id AND t.tenant_id <> app_quarantine_tenant_id()
       AND (ARRAY[t.billing_party_id, t.source_linked_party_id, t.destination_linked_party_id] && app_customer_ids()
            OR app_task_stops_in_scope(t.id)));
END $$;

CREATE FUNCTION app_trip_in_scope(p_trip_id uuid) RETURNS boolean
  LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  RETURN cardinality(app_customer_ids()) > 0 AND EXISTS (
    SELECT 1 FROM trip_records r
     WHERE r.id = p_trip_id AND r.tenant_id <> app_quarantine_tenant_id()
       AND (r.billing_party_id = ANY (app_customer_ids())
            OR (r.task_id IS NOT NULL AND app_task_in_scope(r.task_id))));
END $$;

-- drivers / trucks are visible to scope principals only through recent in-scope work (privacy: 180 days);
-- pass the driver id or the truck id, the other argument NULL
CREATE FUNCTION app_recent_work_in_scope(p_driver_id uuid, p_truck_id uuid) RETURNS boolean
  LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  RETURN cardinality(app_customer_ids()) > 0 AND EXISTS (
    SELECT 1 FROM tasks t
     WHERE (t.driver_id = p_driver_id OR t.helper_driver_id = p_driver_id OR t.truck_id = p_truck_id)
       AND t.plan_at > now() - interval '180 days' AND t.tenant_id <> app_quarantine_tenant_id()
       AND (ARRAY[t.billing_party_id, t.source_linked_party_id, t.destination_linked_party_id] && app_customer_ids()
            OR app_task_stops_in_scope(t.id)));
END $$;
```

Counters are reachable only through SECURITY DEFINER allocators (R10, R67): neither counter table is granted to `logitrack_app`.

```sql
-- 0004_operations.sql (after CREATE TABLE task_number_counters): global per (task_type, plan_date), R10
CREATE FUNCTION next_task_seq(p_task_type text, p_plan_date date) RETURNS int
  LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
  INSERT INTO task_number_counters AS c (task_type, plan_date, last_seq) VALUES (p_task_type, p_plan_date, 1)
  ON CONFLICT (task_type, plan_date) DO UPDATE SET last_seq = c.last_seq + 1
  RETURNING last_seq
$$;

-- 0005_billing.sql (after CREATE TABLE billing_counters): the counter row carries the billing carrier (R61);
-- a counter owned by another billing carrier returns NULL and the statement transaction fails.
CREATE FUNCTION next_invoice_seq(p_tenant uuid, p_party uuid, p_year int, p_month int, p_code text) RETURNS int
  LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
  INSERT INTO billing_counters AS c (billing_party_id, period_year, period_month, tenant_id, tenant_source,
                                     last_seq, customer_code)
  VALUES (p_party, p_year, p_month, p_tenant, 'form', 1, p_code)
  ON CONFLICT (billing_party_id, period_year, period_month)
    DO UPDATE SET last_seq = c.last_seq + 1 WHERE c.tenant_id = p_tenant
  RETURNING last_seq
$$;
```

`next_invoice_seq` is called inside the statement transaction after Go has checked `accounting:billing_document` and that `p_tenant` (the statement's `tenant_id`) is the principal's tenant; a failed statement transaction rolls the counter back too (no numbering gap, unlike today's client transaction).

The hand-over happens at the single grant site, **ACL first, then ownership** (the block of Appendix A §A.2.8). The migrator holds `logitrack_rls_definer` `WITH INHERIT FALSE`, so once a function belongs to the definer a `REVOKE`/`GRANT` on it by the migrator only raises a warning and changes nothing: PUBLIC would keep `EXECUTE` on the allocators (verified on `postgres:18-alpine`, Appendix A §A.2.8).

```sql
-- 0009_infra.sql — ALTER ... OWNER TO needs CREATE on the schema for the new owner during the statement (R66)
GRANT CREATE ON SCHEMA public TO logitrack_rls_definer;
-- +goose StatementBegin
DO $$ DECLARE f regprocedure; fname name; f_owner oid; BEGIN
  FOREACH f IN ARRAY ARRAY['app_task_stops_in_scope(uuid)','app_task_in_scope(uuid)','app_trip_in_scope(uuid)',
                           'app_recent_work_in_scope(uuid,uuid)','driver_directory()','trg_driver_link_membership()',
                           'next_task_seq(text,date)','next_invoice_seq(uuid,uuid,int,int,text)']::regprocedure[] LOOP
    SELECT p.proname, p.proowner INTO fname, f_owner FROM pg_proc p WHERE p.oid = f;
    CONTINUE WHEN f_owner = 'logitrack_rls_definer'::regrole::oid;       -- already handed over (re-run after Down)
    EXECUTE format('REVOKE ALL ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO logitrack_app', f);
    IF starts_with(fname::text, 'app_') THEN      -- policy helpers: every role that evaluates the policies
      EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO logitrack_readonly, pg_database_owner', f);
    ELSIF starts_with(fname::text, 'next_') THEN  -- allocators: cmd/seed numbers tasks and invoices (R87)
      EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO logitrack_etl', f);
    END IF;
    EXECUTE format('ALTER FUNCTION %s OWNER TO logitrack_rls_definer', f);
  END LOOP;
END $$;
-- +goose StatementEnd
REVOKE CREATE ON SCHEMA public FROM logitrack_rls_definer;
GRANT SELECT ON tasks, task_delivery_stops, trip_records, drivers, memberships TO logitrack_rls_definer;
GRANT SELECT, INSERT, UPDATE ON task_number_counters, billing_counters TO logitrack_rls_definer;
```

`pg_database_owner` (the migrator, as database owner) keeps `EXECUTE` on the policy helpers because `FORCE` RLS evaluates the policies for the owner too (a later foreign-key validation scan on `trucks` would otherwise fail). The Down sections drop these functions as their owner through `app_drop_definer_function()` (Appendix A §A.2.0).

Because `0001`-`0009` are applied together in P0 (R59), no request ever runs while these functions are still owned by `logitrack_migrator`.

### C.3.4 Contractor reach (legacy subcontractors) and billing-carrier stamping

Strict per-tenant isolation would break day-one operations: legacy `subcontractors` are, in their original meaning, companies that subcontract **from the own fleet** (glossary "Tenant": the collection's meaning is *widened* by ADR 0026), and own-fleet staff today plan, monitor, correct and invoice the trips those carriers run. Once ETL stamps those rows with the carrier tenant (`tenantResolve.ts:100-135`), own-fleet staff would no longer see them.

Design (R60; owner confirmation listed in main spec §19):

- `tenants.contractor_tenant_id uuid NULL REFERENCES tenants(id)` (one level, no transitive reach, R56). ETL sets it to the own-fleet tenant for every tenant created from a legacy `subcontractors` doc; carriers onboarded later through `fleet:manage_subcontractors` default to the own fleet; carriers onboarded by a platform admin to work directly for a dispatcher have `NULL`. Changing it is `platform:manage_tenants` and appends `tenant_contractor_changed`.
- The request's authorization (`iam.RBAC.Resolve`, T07) loads the active tenant's sub-tenants (`SELECT id FROM tenants WHERE contractor_tenant_id = $tid`, Redis `lt:{APP_ENV}:cache:tenant:subtenants:{tid}`, deleted on outbox `tenant.created` / `tenant.updated`) for staff roles only (a driver never reaches a sub-tenant), and `WithPrincipal` writes them into `app.subtenant_ids`. Staff of a contractor tenant therefore have the same row reach over sub-tenant rows as over their own (`app_tenant_in_reach`); the reverse is never true. This keeps today's behaviour for the own fleet while a carrier that works directly for a dispatcher stays isolated from the own fleet — the separation ADR 0026 §6 is about.
- **Billing carrier (R61).** Rate cards, fuel adjustments, service fees, standby rates, `trip_billing_snapshots`, `billing_counters` (`tenant_id` + `tenant_source`) and `billing_statements` carry the tenant that owns the rate card (the billing carrier, the own fleet today, owner decision D6 in main spec §19), never the tenant that ran the trip. The `billing.compute` consumer stamps `trip_billing_snapshots.tenant_id` from the selected rate entry (own fleet when unpriced). A carrier therefore never reads the price its contractor charges the customer.
- Standby billing fields stay inline on `standby_records` (Appendix A, `0004_operations`, R61), so a sub-tenant's staff could read them at row level. The standby repository therefore returns the `billing_*` columns only when the principal's tenant is the billing carrier of that row (the tenant owning the referenced `standby_rate_entries` / service fee, or the own fleet for unpriced rows); this is a service projection, asserted by the RLS test matrix (C.9.2 #13).

### C.3.5 Policy catalog

**Generators** (`0001_preamble.sql`) keep the uniform policies identical across tables. Every table that uses one is named in an explicit `DO` block of its own migration file (below), followed by that file's extra `CREATE POLICY` statements.

```sql
-- 0001_preamble.sql
-- Tenant table: platform bypass + quarantine invisibility (restrictive, AND-ed) + tenant staff reach + frozen tenant_id.
CREATE PROCEDURE app_rls_tenant_table(tbl regclass) LANGUAGE plpgsql AS $$
BEGIN
  EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY', tbl);
  EXECUTE format('ALTER TABLE %s FORCE ROW LEVEL SECURITY', tbl);
  EXECUTE format('CREATE POLICY p_bypass ON %s USING (app_bypass()) WITH CHECK (app_bypass())', tbl);
  EXECUTE format('CREATE POLICY p_not_quarantine ON %s AS RESTRICTIVE
                    USING (app_bypass() OR tenant_id IS DISTINCT FROM app_quarantine_tenant_id())
                    WITH CHECK (app_bypass() OR tenant_id IS DISTINCT FROM app_quarantine_tenant_id())', tbl);
  EXECUTE format('CREATE POLICY p_tenant_staff ON %s
                    USING (app_is_staff() AND app_tenant_in_reach(tenant_id))
                    WITH CHECK (app_is_staff() AND app_tenant_in_reach(tenant_id))', tbl);
  EXECUTE format('CREATE TRIGGER t_freeze_tenant_id BEFORE UPDATE OF tenant_id ON %s
                    FOR EACH ROW EXECUTE FUNCTION trg_freeze_tenant_id()', tbl);
END $$;

-- Child table without tenant_id: readable when the parent row is visible (the EXISTS runs under the parent's RLS);
-- writable only when the parent is in the writer's tenant reach and the writer is staff (or the owning driver when allowed).
CREATE PROCEDURE app_rls_child_table(tbl regclass, parent regclass, fk name, parent_key name DEFAULT 'id',
                                     read_staff_only boolean DEFAULT false, driver_writes boolean DEFAULT false)
LANGUAGE plpgsql AS $$
DECLARE vis text := format('EXISTS (SELECT 1 FROM %s p WHERE p.%I = %s.%I)', parent, parent_key, tbl, fk);
        wr  text := CASE WHEN driver_writes THEN 'OR app_role() = ''driver''' ELSE '' END;
BEGIN
  EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY', tbl);
  EXECUTE format('ALTER TABLE %s FORCE ROW LEVEL SECURITY', tbl);
  EXECUTE format('CREATE POLICY p_bypass ON %s USING (app_bypass()) WITH CHECK (app_bypass())', tbl);
  EXECUTE format('CREATE POLICY p_parent_read ON %s FOR SELECT USING (%s %s)', tbl,
                 CASE WHEN read_staff_only THEN 'app_is_staff() AND' ELSE '' END, vis);
  EXECUTE format('CREATE POLICY p_parent_write ON %s
                    USING (EXISTS (SELECT 1 FROM %s p WHERE p.%I = %s.%I AND app_tenant_in_reach(p.tenant_id)
                                   AND (app_is_staff() %s)))
                    WITH CHECK (EXISTS (SELECT 1 FROM %s p WHERE p.%I = %s.%I AND app_tenant_in_reach(p.tenant_id)
                                   AND (app_is_staff() %s)))',
                 tbl, parent, parent_key, tbl, fk, wr, parent, parent_key, tbl, fk, wr);
END $$;

-- Global master table (no tenancy stamp): any authenticated principal reads; stewards write (R60).
CREATE PROCEDURE app_rls_global_table(tbl regclass) LANGUAGE plpgsql AS $$
BEGIN
  EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY', tbl);
  EXECUTE format('ALTER TABLE %s FORCE ROW LEVEL SECURITY', tbl);
  EXECUTE format('CREATE POLICY p_bypass ON %s USING (app_bypass()) WITH CHECK (app_bypass())', tbl);
  EXECUTE format('CREATE POLICY p_read ON %s FOR SELECT USING (app_is_authenticated())', tbl);
  EXECUTE format('CREATE POLICY p_steward_write ON %s USING (app_is_steward()) WITH CHECK (app_is_steward())', tbl);
END $$;

-- Platform-only / nullable-tenant base: only WithSystem transactions, plus the policies added per table.
CREATE PROCEDURE app_rls_platform_table(tbl regclass) LANGUAGE plpgsql AS $$
BEGIN
  EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY', tbl);
  EXECUTE format('ALTER TABLE %s FORCE ROW LEVEL SECURITY', tbl);
  EXECUTE format('CREATE POLICY p_bypass ON %s USING (app_bypass()) WITH CHECK (app_bypass())', tbl);
END $$;

-- Driver self rows on a tenant table (added to G-tenant): p_driver_read always; p_driver_insert (own row in the
-- driver's current tenant) and p_driver_update (own row; tenant_id cannot change, t_freeze_tenant_id) on request.
CREATE PROCEDURE app_rls_driver_rows(tbl regclass, can_insert boolean, can_update boolean) LANGUAGE plpgsql AS $$
DECLARE own text := 'app_role() = ''driver'' AND driver_id = app_driver_id()';
BEGIN
  EXECUTE format('CREATE POLICY p_driver_read ON %s FOR SELECT USING (%s)', tbl, own);
  IF can_insert THEN
    EXECUTE format('CREATE POLICY p_driver_insert ON %s FOR INSERT WITH CHECK (%s AND tenant_id = app_tenant_id())', tbl, own);
  END IF;
  IF can_update THEN
    EXECUTE format('CREATE POLICY p_driver_update ON %s FOR UPDATE USING (%s) WITH CHECK (%s)', tbl, own, own);
  END IF;
END $$;
```

`ENABLE` / `FORCE` are idempotent, so Appendix A's per-table `ALTER TABLE ... ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY` lines and the generator calls coexist. PostgreSQL applies SELECT policies to rows targeted by `UPDATE ... WHERE` and to `RETURNING`, so every write policy below has a matching read branch. Inside the child write policy the parent's own RLS applies, so a driver writes a child row only under a parent it can see, and a dispatcher cannot write children of another tenant's rows it can read. goose needs `-- +goose StatementBegin` / `StatementEnd` around each `DO` block and function body.

**0002_identity** (after the quarantine insert of C.1.8).

```sql
DO $$ BEGIN
  CALL app_rls_platform_table('tenants');            CALL app_rls_tenant_table('tenant_files');
  CALL app_rls_platform_table('users');              CALL app_rls_platform_table('file_objects');
  CALL app_rls_platform_table('auth_identities');    CALL app_rls_platform_table('sessions');
  CALL app_rls_platform_table('refresh_tokens');     CALL app_rls_platform_table('password_reset_tokens');
  CALL app_rls_platform_table('api_keys');           CALL app_rls_platform_table('memberships');
  CALL app_rls_platform_table('user_platform_roles'); CALL app_rls_platform_table('role_capability_overrides');
  CALL app_rls_platform_table('status_history');
END $$;

-- tenants: own tenant, sub-tenants and (for stewards and scope principals) carrier names; quarantine only via bypass
CREATE POLICY p_read ON tenants FOR SELECT USING (
  kind <> 'quarantine' AND (app_tenant_in_reach(id) OR app_is_steward() OR app_is_scope()));
CREATE POLICY p_update_own ON tenants FOR UPDATE                     -- profile fields only (trigger t_tenant_admin_columns)
  USING (app_role() = 'tenant_admin' AND id = app_tenant_id())
  WITH CHECK (app_role() = 'tenant_admin' AND id = app_tenant_id());
CREATE POLICY p_steward ON tenant_files USING (app_is_steward()) WITH CHECK (app_is_steward());

-- users: self, and staff of a tenant the user belongs to; writes other than self-profile go through iam (WithSystem)
CREATE POLICY p_self_read   ON users FOR SELECT USING (id = app_user_id());
CREATE POLICY p_self_update ON users FOR UPDATE USING (id = app_user_id()) WITH CHECK (id = app_user_id());
CREATE POLICY p_staff_read  ON users FOR SELECT USING (app_is_staff() AND EXISTS (
  SELECT 1 FROM memberships m WHERE m.user_id = users.id AND app_tenant_in_reach(m.tenant_id)));
CREATE POLICY p_self_read  ON memberships FOR SELECT USING (user_id = app_user_id());
CREATE POLICY p_staff_read ON memberships FOR SELECT USING (app_is_staff() AND app_tenant_in_reach(tenant_id));
CREATE POLICY p_self_read ON auth_identities     FOR SELECT USING (user_id = app_user_id());
CREATE POLICY p_self_read ON user_platform_roles FOR SELECT USING (user_id = app_user_id());
CREATE POLICY p_self_read ON sessions            FOR SELECT USING (user_id = app_user_id());
CREATE POLICY p_staff_read ON role_capability_overrides FOR SELECT USING (app_is_staff() AND tenant_id = app_tenant_id());
CREATE POLICY p_staff_read ON api_keys                  FOR SELECT USING (app_is_staff() AND tenant_id = app_tenant_id());

-- status_history: visible / insertable exactly when the entity is. Invoker-rights PL/pgSQL, so the lookups run under
-- the caller's RLS and bind drivers / trucks / truck_assignments (created in 0003) at first call.
CREATE FUNCTION app_status_entity_visible(p_type text, p_id uuid, p_for_write boolean) RETURNS boolean
  LANGUAGE plpgsql STABLE AS $$
BEGIN
  RETURN CASE p_type
    WHEN 'driver'           THEN EXISTS (SELECT 1 FROM drivers d           WHERE d.id = p_id)
    WHEN 'truck'            THEN EXISTS (SELECT 1 FROM trucks t            WHERE t.id = p_id)
    WHEN 'truck_assignment' THEN EXISTS (SELECT 1 FROM truck_assignments a WHERE a.id = p_id)
    WHEN 'tenant'           THEN app_is_staff() AND app_tenant_in_reach(p_id)
    WHEN 'user'             THEN NOT p_for_write AND p_id = app_user_id()
    ELSE false END;
END $$;
CREATE POLICY p_entity ON status_history
  USING (app_status_entity_visible(entity_type, entity_id, false))
  WITH CHECK (app_is_staff() AND app_status_entity_visible(entity_type, entity_id, true));

-- file_objects (R1 columns): a principal sees its own uploads, its tenant's files and public files. Cross-tenant
-- readers (scope principals, evidence viewers) never read this table: the storage service resolves the key under
-- WithSystem only after the referencing row was read under the principal ("a file is readable iff its referencing
-- row is readable").
CREATE POLICY p_read ON file_objects FOR SELECT USING (
     visibility = 'public' OR uploaded_by = app_user_id()
  OR (app_is_staff() AND app_tenant_in_reach(tenant_id)));
CREATE POLICY p_upload ON file_objects FOR INSERT WITH CHECK (
  uploaded_by = app_user_id() AND status = 'pending'
  AND (tenant_id = app_tenant_id() OR (tenant_id IS NULL AND app_is_steward())));
CREATE POLICY p_commit ON file_objects FOR UPDATE                 -- commit only (trigger t_file_objects_commit_columns)
  USING (uploaded_by = app_user_id() OR (app_is_staff() AND app_tenant_in_reach(tenant_id)))
  WITH CHECK ((uploaded_by = app_user_id() AND (tenant_id = app_tenant_id() OR (tenant_id IS NULL AND app_is_steward())))
           OR (app_is_staff() AND app_tenant_in_reach(tenant_id)));
```

**0003_master** (after the scope helpers of C.3.3).

```sql
DO $$ BEGIN
  CALL app_rls_global_table('customers');   CALL app_rls_global_table('customer_driver_id_types');
  CALL app_rls_global_table('billing_parties'); CALL app_rls_global_table('hubs');
  CALL app_rls_global_table('hub_name_aliases'); CALL app_rls_global_table('hub_soc_distances');
  CALL app_rls_tenant_table('companies');   CALL app_rls_tenant_table('drivers');
  CALL app_rls_tenant_table('trucks');      CALL app_rls_tenant_table('truck_assignments');
  CALL app_rls_platform_table('user_scopes');
  CALL app_rls_child_table('driver_customer_codes', 'drivers', 'driver_id', read_staff_only => true);
  CALL app_rls_child_table('truck_files', 'trucks', 'truck_id', read_staff_only => true);
  CALL app_rls_driver_rows('truck_assignments', false, false);
END $$;

CREATE POLICY p_self_read ON user_scopes FOR SELECT USING (user_id = app_user_id());
CREATE POLICY p_driver_hub_insert ON hubs FOR INSERT
  WITH CHECK (app_role() = 'driver' AND created_by_driver);                         -- mobile:create_hub
CREATE POLICY p_driver_hub_alias_insert ON hub_name_aliases FOR INSERT
  WITH CHECK (app_role() = 'driver' AND EXISTS (SELECT 1 FROM hubs h WHERE h.id = hub_id AND h.created_by_driver));

CREATE POLICY p_self_read   ON drivers FOR SELECT USING (app_role() = 'driver' AND id = app_driver_id());
CREATE POLICY p_self_update ON drivers FOR UPDATE                                    -- active_* only (trigger C.3.6)
  USING (app_role() = 'driver' AND id = app_driver_id()) WITH CHECK (app_role() = 'driver' AND id = app_driver_id());
CREATE POLICY p_scope_read  ON drivers FOR SELECT USING (app_is_scope() AND app_recent_work_in_scope(id, NULL));

CREATE POLICY p_driver_read ON trucks FOR SELECT                                     -- own company/partner trucks only
  USING (app_role() = 'driver' AND tenant_id = app_tenant_id());                     -- (.vibe-rules.md "Vehicle identity")
CREATE POLICY p_scope_read  ON trucks FOR SELECT USING (app_is_scope() AND app_recent_work_in_scope(NULL, id));

```

**0004_operations.**

```sql
DO $$ BEGIN
  CALL app_rls_platform_table('task_number_counters');
  CALL app_rls_tenant_table('tasks');        CALL app_rls_tenant_table('trip_records');
  CALL app_rls_tenant_table('standby_records'); CALL app_rls_tenant_table('incident_reports');
  CALL app_rls_tenant_table('vehicle_locations');
  CALL app_rls_child_table('task_delivery_stops', 'tasks', 'task_id', driver_writes => true);
  CALL app_rls_child_table('trip_delivery_stops', 'trip_records', 'trip_id', driver_writes => true);
  CALL app_rls_child_table('trip_photos', 'trip_records', 'trip_id', driver_writes => true);
  CALL app_rls_child_table('trip_no_history', 'trip_records', 'trip_id', read_staff_only => true);
  CALL app_rls_child_table('standby_photos', 'standby_records', 'standby_id', driver_writes => true);
  CALL app_rls_driver_rows('trip_records', true, true);
  CALL app_rls_driver_rows('standby_records', true, false);
  CALL app_rls_driver_rows('incident_reports', true, true);
END $$;

-- tasks: own or helper task (R28 ownership; helper_driver_id is the scalar ADR 0011 cap-1 column)
CREATE POLICY p_driver_read ON tasks FOR SELECT
  USING (app_role() = 'driver' AND (driver_id = app_driver_id() OR helper_driver_id = app_driver_id()));
CREATE POLICY p_driver_update ON tasks FOR UPDATE                                    -- check-in, truck confirmation
  USING      (app_role() = 'driver' AND (driver_id = app_driver_id() OR helper_driver_id = app_driver_id()))
  WITH CHECK (app_role() = 'driver' AND (driver_id = app_driver_id() OR helper_driver_id = app_driver_id()));
CREATE POLICY p_driver_insert ON tasks FOR INSERT                                    -- manual check-in creates its own task
  WITH CHECK (app_role() = 'driver' AND driver_id = app_driver_id() AND tenant_id = app_tenant_id());
CREATE POLICY p_scope_read ON tasks FOR SELECT USING (app_is_scope() AND (
     ARRAY[billing_party_id, source_linked_party_id, destination_linked_party_id] && app_customer_ids()
  OR app_task_stops_in_scope(id)));

CREATE POLICY p_scope_read ON trip_records FOR SELECT USING (app_is_scope() AND (
     billing_party_id = ANY (app_customer_ids())                                     -- useDriverMonitor.ts:217 definition
  OR (task_id IS NOT NULL AND app_task_in_scope(task_id))));                         -- first-mile/page.tsx:262-266 definition

CREATE POLICY p_scope_read ON standby_records FOR SELECT USING (app_is_scope() AND (
     ARRAY[billing_party_id, customer_party_id] && app_customer_ids()
  OR (task_id IS NOT NULL AND app_task_in_scope(task_id))
  OR (trip_id IS NOT NULL AND app_trip_in_scope(trip_id))));

CREATE POLICY p_scope_read ON incident_reports FOR SELECT                            -- incidents of visible trips only
  USING (app_is_scope() AND trip_id IS NOT NULL AND app_trip_in_scope(trip_id));     -- (fixes firestore.rules:93-96)

CREATE POLICY p_dispatch_read ON vehicle_locations FOR SELECT                        -- fleet:view_live_map for dispatchers
  USING (app_is_dispatcher() AND app_recent_work_in_scope(NULL, truck_id));
```

**0005_billing.** Carrier-internal: no driver, scope or dispatcher branch.

```sql
DO $$ BEGIN
  CALL app_rls_tenant_table('customer_rate_entries');  CALL app_rls_tenant_table('customer_fuel_rate_adjustments');
  CALL app_rls_tenant_table('customer_service_fees');  CALL app_rls_tenant_table('standby_rate_entries');
  CALL app_rls_tenant_table('trip_billing_snapshots'); CALL app_rls_tenant_table('billing_statements');
  CALL app_rls_platform_table('billing_counters');
  CALL app_rls_child_table('trip_billing_stop_breakdown', 'trip_billing_snapshots', 'trip_id',
                           parent_key => 'trip_id', read_staff_only => true);
  CALL app_rls_child_table('billing_statement_lines', 'billing_statements', 'statement_id', read_staff_only => true);
  CALL app_rls_child_table('statement_documents', 'billing_statements', 'statement_id', read_staff_only => true);
END $$;
```

**0006_finance_hr.**

```sql
DO $$ BEGIN
  CALL app_rls_tenant_table('vehicle_expenses');      CALL app_rls_tenant_table('maintenance_records');
  CALL app_rls_tenant_table('transactions');          CALL app_rls_tenant_table('driver_compensation_configs');
  CALL app_rls_tenant_table('driver_penalties');      CALL app_rls_tenant_table('payroll_runs');
  CALL app_rls_tenant_table('driver_advances');
  CALL app_rls_child_table('maintenance_files', 'maintenance_records', 'maintenance_id', driver_writes => true);
  CALL app_rls_child_table('penalty_types', 'driver_compensation_configs', 'config_id', read_staff_only => true);
  CALL app_rls_child_table('payroll_line_items', 'payroll_runs', 'payroll_run_id');
  CALL app_rls_child_table('payroll_penalty_applications', 'payroll_runs', 'payroll_run_id');
  CALL app_rls_driver_rows('vehicle_expenses', true, false);
  CALL app_rls_driver_rows('payroll_runs', false, false);
END $$;

CREATE POLICY p_driver_update_pending ON vehicle_expenses FOR UPDATE
  USING (app_role() = 'driver' AND driver_id = app_driver_id() AND status = 'pending')
  WITH CHECK (app_role() = 'driver' AND driver_id = app_driver_id() AND status = 'pending');

-- maintenance gate (replaces firestore.rules:381-418 and the activeTruck denormalisation): the truck the driver is
-- responsible for now, or the home truck of an active assignment, in the driver's active tenant only. Invoker rights:
-- the lookups run under the driver's own RLS branches on drivers, truck_assignments and trucks (own tenant). Neither
-- drivers.active_truck_id nor truck_assignments.truck_id is tenant-checked by its FK, and an assignment left in a
-- former tenant stays visible to the driver (self-scope by driver_id), so the helper and both policies bind the
-- gate to app_tenant_id().
CREATE FUNCTION app_driver_truck_ids() RETURNS uuid[] LANGUAGE sql STABLE PARALLEL SAFE
  RETURN ARRAY(SELECT t.id FROM drivers d JOIN trucks t ON t.id = d.active_truck_id AND t.tenant_id = app_tenant_id()
                WHERE d.id = app_driver_id()
               UNION
               SELECT t.id FROM truck_assignments a JOIN trucks t ON t.id = a.truck_id AND t.tenant_id = app_tenant_id()
                WHERE a.driver_id = app_driver_id() AND a.status = 'active' AND a.tenant_id = app_tenant_id());
CREATE POLICY p_driver_read ON maintenance_records FOR SELECT
  USING (app_role() = 'driver' AND tenant_id = app_tenant_id() AND truck_id = ANY (app_driver_truck_ids()));
CREATE POLICY p_driver_update ON maintenance_records FOR UPDATE
  USING      (app_role() = 'driver' AND tenant_id = app_tenant_id() AND truck_id = ANY (app_driver_truck_ids()))
  WITH CHECK (app_role() = 'driver' AND tenant_id = app_tenant_id() AND truck_id = ANY (app_driver_truck_ids()));

```

The driver maintenance policies read `drivers`, `truck_assignments` and `trucks` under their own RLS (driver self branches; `trucks` only in the driver's tenant), so no `FieldValue.delete()` workaround (`.vibe-rules.md`, Confirmed Patterns "Vehicle identity", as of commit 4f552099) is needed. The gate is bound to the driver's active tenant (`app_tenant_id()`) in the helper and in both policies: the foreign keys `drivers.active_truck_id` and `truck_assignments.truck_id` are checked without RLS and so accept a truck of any tenant (a driver writes `active_truck_id` itself, staff insert assignments), and an active assignment left in a former tenant after a driver move (C.1.4) stays visible to the driver through the self-scope by `driver_id`; without the tenant term any of these would open another tenant's maintenance rows and, through `p_parent_read`, their `maintenance_files`. Drivers cannot create maintenance rows (today `create` is admin-only, `firestore.rules:412`); `driver_penalties` stay staff-only (admin-only read today, `firestore.rules:469-472`).

**0007_comms.** `notification_deliveries` is exempt (no call).

```sql
DO $$ BEGIN
  CALL app_rls_platform_table('device_tokens');      CALL app_rls_tenant_table('chats');
  CALL app_rls_platform_table('chat_messages');      CALL app_rls_platform_table('chat_read_state');
  CALL app_rls_platform_table('broadcasts');         CALL app_rls_platform_table('broadcast_recipients');
  CALL app_rls_platform_table('broadcast_reads');
  CALL app_rls_driver_rows('chats', false, true);
END $$;

CREATE POLICY p_self ON device_tokens USING (user_id = app_user_id()) WITH CHECK (user_id = app_user_id());

CREATE POLICY p_driver_insert ON chats FOR INSERT
  WITH CHECK (app_role() = 'driver' AND driver_id = app_driver_id() AND tenant_id = app_tenant_id()
              AND assigned_admin_user_id IS NULL);                                   -- firestore.rules:70-73

CREATE POLICY p_parent_read ON chat_messages FOR SELECT USING (EXISTS (SELECT 1 FROM chats c WHERE c.id = chat_id));
CREATE POLICY p_own_write   ON chat_messages FOR INSERT WITH CHECK (
  sender_user_id = app_user_id() AND EXISTS (SELECT 1 FROM chats c WHERE c.id = chat_id)
  AND ((app_is_staff() AND sender_role = 'admin') OR (app_role() = 'driver' AND sender_role = 'driver')));
CREATE POLICY p_parent_read ON chat_read_state FOR SELECT USING (EXISTS (SELECT 1 FROM chats c WHERE c.id = chat_id));
CREATE POLICY p_own_write   ON chat_read_state
  USING (user_id = app_user_id()) WITH CHECK (user_id = app_user_id() AND EXISTS (SELECT 1 FROM chats c WHERE c.id = chat_id));

-- broadcasts: tenant_id NULL = platform-wide (stewards only, R60); voided rows hidden from drivers
CREATE POLICY p_read ON broadcasts FOR SELECT USING (
     (app_is_staff() AND (tenant_id IS NULL OR app_tenant_in_reach(tenant_id)))
  OR (app_role() = 'driver' AND voided_at IS NULL AND (tenant_id IS NULL OR tenant_id = app_tenant_id()))
  OR (tenant_id IS NULL AND app_is_steward()));
CREATE POLICY p_staff_write ON broadcasts
  USING      ((app_is_staff() AND app_tenant_in_reach(tenant_id)) OR (tenant_id IS NULL AND app_is_steward()))
  WITH CHECK ((app_is_staff() AND app_tenant_in_reach(tenant_id)) OR (tenant_id IS NULL AND app_is_steward()));
CREATE POLICY p_read ON broadcast_recipients FOR SELECT USING (
  (app_is_staff() OR user_id = app_user_id()) AND EXISTS (SELECT 1 FROM broadcasts b WHERE b.id = broadcast_id));
CREATE POLICY p_write ON broadcast_recipients FOR INSERT WITH CHECK (
  app_is_staff() AND EXISTS (SELECT 1 FROM broadcasts b WHERE b.id = broadcast_id));
CREATE POLICY p_read ON broadcast_reads FOR SELECT USING (
  (app_is_staff() OR user_id = app_user_id()) AND EXISTS (SELECT 1 FROM broadcasts b WHERE b.id = broadcast_id));
CREATE POLICY p_write ON broadcast_reads FOR INSERT WITH CHECK (                     -- mark read; PK makes it idempotent
  user_id = app_user_id() AND EXISTS (SELECT 1 FROM broadcasts b WHERE b.id = broadcast_id));
```

**0008_platform.** `settings`, `mobile_app_releases`, `waitlist`, `partner_interest`, `fuel_daily_snapshots`, `fuel_monthly_snapshots` are exempt (no call).

```sql
DO $$ BEGIN
  CALL app_rls_platform_table('security_events');    CALL app_rls_platform_table('holidays');
  CALL app_rls_tenant_table('mobile_installations'); CALL app_rls_tenant_table('leave_requests');
  CALL app_rls_child_table('leave_request_attachments', 'leave_requests', 'leave_request_id', driver_writes => true);
  CALL app_rls_driver_rows('mobile_installations', true, true);   -- PK (driver_id, install_id) (R33)
  CALL app_rls_driver_rows('leave_requests', false, false);
END $$;

CREATE POLICY p_staff_read ON security_events FOR SELECT                              -- writes: WithSystem only (C.4.13)
  USING (app_is_staff() AND tenant_id = app_tenant_id());

-- holidays: tenant_id NULL = PUBLIC calendar (stewards, R60), otherwise company / other rows of a tenant
CREATE POLICY p_read  ON holidays FOR SELECT USING (
  app_is_authenticated() AND (tenant_id IS NULL OR tenant_id = app_tenant_id() OR (app_is_staff() AND app_tenant_in_reach(tenant_id))));
CREATE POLICY p_write ON holidays
  USING      ((app_is_staff() AND app_tenant_in_reach(tenant_id)) OR (tenant_id IS NULL AND app_is_steward()))
  WITH CHECK ((app_is_staff() AND app_tenant_in_reach(tenant_id)) OR (tenant_id IS NULL AND app_is_steward()));

CREATE POLICY p_driver_insert ON leave_requests FOR INSERT
  WITH CHECK (app_role() = 'driver' AND driver_id = app_driver_id() AND tenant_id = app_tenant_id() AND status = 'pending');
CREATE POLICY p_driver_update ON leave_requests FOR UPDATE                            -- cancel own pending request
  USING (app_role() = 'driver' AND driver_id = app_driver_id() AND status = 'pending')
  WITH CHECK (app_role() = 'driver' AND driver_id = app_driver_id() AND status IN ('pending','cancelled'));
```

**0009_infra.** No RLS table (all exempt); the file carries the grants and the function hand-over of C.3.2 / C.3.3.

### C.3.6 Triggers that complete the policies

```sql
-- 0001_preamble.sql: tenant_id is frozen at write time (glossary `tenantId`); moves only through the explicit paths
CREATE FUNCTION trg_freeze_tenant_id() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id AND NOT app_tenant_move_allowed() AND NOT app_etl_load() THEN
    RAISE EXCEPTION '%.tenant_id is frozen at write time', TG_TABLE_NAME USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  RETURN NEW;
END $$;

-- 0003_master.sql: a driver may change only its live-truck columns (check-in / end of job)
CREATE FUNCTION trg_driver_self_columns() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE allowed text[] := ARRAY['active_truck_id','active_truck_plate','active_task_id','active_started_at','updated_at'];
BEGIN
  IF app_role() = 'driver' AND NOT app_bypass() AND (to_jsonb(NEW) - allowed) IS DISTINCT FROM (to_jsonb(OLD) - allowed) THEN
    RAISE EXCEPTION 'drivers: a driver may only change active_* columns' USING ERRCODE = 'insufficient_privilege';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER t_driver_self_columns BEFORE UPDATE ON drivers FOR EACH ROW EXECUTE FUNCTION trg_driver_self_columns();

-- 0002_identity.sql: self-service profile columns only (PATCH /v1/me); role, status, password and auth_version
-- change only through iam/auth under WithSystem
CREATE FUNCTION trg_users_self_columns() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE allowed text[] := ARRAY['display_name','photo_file_id','last_login_at','last_login_lat','last_login_lng',
                                'last_login_geo_source','last_login_accuracy_m','updated_at'];
BEGIN
  IF NOT app_bypass() AND (to_jsonb(NEW) - allowed) IS DISTINCT FROM (to_jsonb(OLD) - allowed) THEN
    RAISE EXCEPTION 'users: only profile columns are self-service' USING ERRCODE = 'insufficient_privilege';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER t_users_self_columns BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION trg_users_self_columns();

-- 0002_identity.sql: outside WithSystem (and cmd/etl) a principal may only commit an upload through p_commit: the
-- identity, tenant and exposure columns are fixed at upload, and status moves only pending -> committed
-- (missing_at_source, re-home C.3.10 and storage.gc run under WithSystem)
CREATE FUNCTION trg_file_objects_commit_columns() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NOT app_bypass() AND NOT app_etl_load() AND (
       (NEW.bucket, NEW.object_key, NEW.tenant_id, NEW.purpose, NEW.visibility, NEW.uploaded_by, NEW.legacy_url, NEW.created_at)
         IS DISTINCT FROM (OLD.bucket, OLD.object_key, OLD.tenant_id, OLD.purpose, OLD.visibility, OLD.uploaded_by,
                           OLD.legacy_url, OLD.created_at)
    OR (NEW.status IS DISTINCT FROM OLD.status AND NOT (OLD.status = 'pending' AND NEW.status = 'committed'))) THEN
    RAISE EXCEPTION 'file_objects: identity, tenant and exposure columns are fixed at upload; status moves only pending -> committed'
      USING ERRCODE = 'insufficient_privilege';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER t_file_objects_commit_columns BEFORE UPDATE ON file_objects
  FOR EACH ROW EXECUTE FUNCTION trg_file_objects_commit_columns();
-- 0011_file_objects_storage_backend.sql (T11) replaces this body with storage_backend added to both column lists:
-- where an object lives (local disk or S3) is fixed at upload too; a copy job moves it under WithSystem.

-- 0002_identity.sql: without bypass (p_update_own) only profile columns of a tenant change; the structural columns
-- are platform-only (platform:manage_tenants, WithSystem)
CREATE FUNCTION trg_tenant_admin_columns() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NOT app_bypass() AND (NEW.kind, NEW.status, NEW.code, NEW.legacy_doc_id, NEW.contractor_tenant_id)
      IS DISTINCT FROM (OLD.kind, OLD.status, OLD.code, OLD.legacy_doc_id, OLD.contractor_tenant_id) THEN
    RAISE EXCEPTION 'tenants: kind, status, code, legacy_doc_id and contractor_tenant_id are platform-only'
      USING ERRCODE = 'insufficient_privilege';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER t_tenant_admin_columns BEFORE UPDATE ON tenants FOR EACH ROW EXECUTE FUNCTION trg_tenant_admin_columns();

-- 0003_master.sql: a linked driver row requires a driver membership in the driver's tenant (checked at COMMIT,
-- so the link and the membership can be written in either order inside one transaction)
CREATE FUNCTION trg_driver_link_membership() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER
  SET search_path = pg_catalog, public AS $$
BEGIN
  IF NEW.user_id IS NOT NULL AND NOT app_etl_load() AND NOT EXISTS (
       SELECT 1 FROM memberships m WHERE m.user_id = NEW.user_id AND m.tenant_id = NEW.tenant_id
                                     AND m.role = 'driver' AND m.status = 'active') THEN
    RAISE EXCEPTION 'driver % is linked to user % without an active driver membership in tenant %',
      NEW.id, NEW.user_id, NEW.tenant_id USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER t_driver_link_membership AFTER INSERT OR UPDATE OF user_id, tenant_id ON drivers
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION trg_driver_link_membership();
```

Link consistency is Appendix A DDL (`0004_operations`, constraint triggers deferred to COMMIT, invoker rights): `trip_records_tenant_check`, `standby_records_tenant_check`, `incident_reports_tenant_check` keep a child on its parent's tenant (trip <- task; standby <- task, else trip; incident <- trip), and a parent the writer cannot see under RLS fails with `insufficient_privilege` (no linking to another tenant's task); `tasks_tenant_move_check` and `trip_records_tenant_move_check` require a parent that changes tenant to take its children along in the same transaction. Because these run with the caller's rights, the three tenant-move paths (C.3.2) run under `WithSystem` after Go authorization, so the move checks see every child; the R13 reassignment service additionally refuses a task that already has trip records (`409 task_has_trips`). Helper drivers: the task service rejects a `helper_driver_id` whose `drivers.tenant_id` is neither the task's tenant nor linked to it by `contractor_tenant_id` (either direction) with `409 helper_not_in_tenant`. UNVERIFIED: whether cross-carrier helpers occur at all (no data access during research); ETL reports violations with reason `helper_tenant_mismatch`.

### C.3.7 Projections for dispatcher and customer-scope principals

Row visibility comes from the `p_scope_read` policies; column projection comes from `security_invoker` views (PostgreSQL 15+, so RLS of the caller still applies through the view) and the sqlc queries built on them. Scope principals are served exclusively from these views. The queries live only in `internal/scope/repo/scope_*.sql` (T07; sqlc block `scope`, generated into `internal/scope/scopedb` with camelCase JSON tags, so a DTO's keys are exactly the view's columns); a domain that needs another scope query adds it there (a `scope_*.sql` file in any other package fails `go test ./internal/scope`, because sqlc vet offers a rule no file name, so only the block that reads `internal/scope/repo` is vetted). The sqlc vet rule `scope-views-only` of that block fails `make gen-check` when a query names any of the 85 tables of Appendix A as a whole word (joined, comma-joined, schema-qualified, quoted, in a subquery or a CTE) or writes; `make gen-check` also runs it over a negative fixture (`internal/scope/testdata/vetcheck`) and fails unless every fixture query is reported, and `internal/scope` tests keep the rule's table list equal to the `CREATE TABLE` statements and each DTO equal to its view.

```sql
-- 0004_operations.sql
CREATE VIEW scope_tasks WITH (security_invoker = true) AS
  SELECT id, tenant_id, task_no, task_type, job_category, status, plan_at, plan_date, plan_time, actual_pickup_at,
         source_hub_raw, source_hub_id, destination_raw, destination_hub_id, destination_soc_key,
         truck_id, truck_type, license_plate_snapshot, driver_id, helper_driver_id, run_order,
         check_in_at, check_in_photo_file_id, check_in_app_screenshot_file_id, check_in_lat, check_in_lng,
         is_multi_delivery, created_at, updated_at
    FROM tasks;                       -- hidden: billing/linked party ids, line_checkin_notified_at, created_by, legacy_*
CREATE VIEW scope_trips WITH (security_invoker = true) AS
  SELECT id, tenant_id, trip_no, status, job_type, job_category, task_id, driver_id,
         origin_raw, origin_hub_id, destination_raw, destination_hub_id, destination_soc_key, seal_code, partner_code,
         truck_id, truck_license_plate_snapshot, vehicle_class, parcel_count, total_weight_kg,
         loading_lat, loading_lng, std, sta, ata, delivered_at, delivered_lat, delivered_lng, is_multi_delivery,
         created_at, updated_at
    FROM trip_records;                -- hidden: billing_party_id, billing_date, evidence_token, review fields, ocr_data, line flags
CREATE VIEW scope_standby WITH (security_invoker = true) AS
  SELECT id, tenant_id, driver_id, task_id, trip_id, job_category, start_location_raw, end_location_raw,
         started_at, ended_at, duration_minutes, note, status, lat, lng, truck_id, truck_license_plate_snapshot, created_at
    FROM standby_records;             -- hidden: billing_*, customer_party_id, evidence_token
CREATE VIEW scope_incidents WITH (security_invoker = true) AS
  SELECT id, tenant_id, trip_id, driver_id, delay_cause, description, lat, lng, truck_id, truck_license_plate_snapshot,
         map_photo_file_id, situation1_photo_file_id, situation2_photo_file_id, reported_by_kind, created_at
    FROM incident_reports;
-- 0003_master.sql
CREATE VIEW scope_drivers WITH (security_invoker = true) AS
  SELECT id, tenant_id, full_name_th, first_name, last_name, mobile, status, active_truck_id FROM drivers;
                                       -- hidden: id_card*, licence*, birth_date, employment*, documents
CREATE VIEW scope_trucks WITH (security_invoker = true) AS
  SELECT id, tenant_id, license_plate, province, vehicle_class, type_raw, status FROM trucks;
                                       -- hidden: insurance*, tax*, cost fields, documents
CREATE VIEW scope_tenants WITH (security_invoker = true) AS
  SELECT id, kind, code, name_th, name_en FROM tenants;
```

Never readable by a scope principal (no `p_scope_read` exists): `payroll_runs`, `payroll_line_items`, `driver_penalties`, `driver_compensation_configs`, `driver_advances`, `vehicle_expenses`, `maintenance_records`, `transactions`, `customer_rate_entries`, `customer_fuel_rate_adjustments`, `customer_service_fees`, `standby_rate_entries`, `trip_billing_snapshots`, `billing_statements` (glossary "Dispatcher" exclusion list).

Driver peer directory: drivers never read other drivers' rows (today every driver loads the whole `drivers` collection for the helper picker, `logitrack-mobile/lib/features/home/presentation/pages/check_in_page.dart:56, 776, 1630`). `GET /v1/drivers?fields=minimal` and `GET /v1/mobile/drivers` (R33) are served for driver principals by `driver_directory()`, a `SECURITY DEFINER` function (handed over in C.3.3) that returns the minimal columns of the non-inactive (`active`, `on_duty`) drivers in the caller's active tenant only. The body of record is Appendix A §A.2.2 (`0003_master.sql`), reprinted here (an earlier copy here added `app_role() = 'driver'` and another sort; the service calls the function only for driver principals):

```sql
-- 0003_master.sql (after CREATE TABLE drivers)
CREATE FUNCTION driver_directory() RETURNS TABLE (id uuid, full_name_th text, first_name text, last_name text)
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
  SELECT d.id, d.full_name_th, d.first_name, d.last_name
    FROM drivers d
   WHERE d.tenant_id = app_tenant_id() AND d.tenant_id <> app_quarantine_tenant_id() AND d.status <> 'inactive'
   ORDER BY d.full_name_th NULLS LAST, d.first_name, d.id
$$;
```

### C.3.8 Explicit predicates in repositories

RLS is the safety net, not the only filter:

- Every sqlc list query on a tenant table names the reach explicitly (`WHERE tenant_id = ANY(sqlc.arg(tenant_ids)::uuid[])`, the scope predicate, or `driver_id = sqlc.arg(driver_id)`) so the planner uses the `(tenant_id, ...)` / `(driver_id, ...)` indexes; RLS then re-checks every row.
- A `sqlc vet` rule (CEL over the parsed query) fails CI when a `SELECT` on a table that has a `tenant_id` column lacks a `tenant_id`, `driver_id` or `id = ` predicate, unless the query carries `-- authz: system` (allowed only in `WithSystem` packages) or `-- authz: scope` (scope views).
- A catalog test (`internal/platform/db/rls_catalog_test.go`, T04; it reads the C.3.0 table and the exempt-table privileges of C.3.2 from this document at run time) queries `pg_class` and `pg_policy` after `goose up` (goose's own `goose_db_version` table is excluded by name), computes the effective table privileges of `logitrack_app` on schemas `public` and `etl` with `has_table_privilege` (so grants to `PUBLIC` and privileges inherited through a role membership count, not only the direct rows of `information_schema.role_table_grants`), and fails when the set of tables with `relrowsecurity AND relforcerowsecurity` differs from the C.3.0 "yes" rows, when a table outside that list is not one of the listed exempt tables, when a family-`tenant` table lacks `p_tenant_staff`, `p_not_quarantine` and `t_freeze_tenant_id`, when an RLS table's `logitrack_app` privileges differ from `SELECT`/`INSERT`/`UPDATE`/`DELETE` minus the append-only and void-only REVOKEs of Appendix A §A.1.8, when an exempt table holds a `logitrack_app` privilege other than those of C.3.2, when either counter or a table of schema `etl` is reachable by `logitrack_app`, when any relation of `public` or `etl` is granted to `PUBLIC`, when a `scope_*` view grants `logitrack_app` more than `SELECT`, when a table's policy names differ from its C.3.0 row (generators expanded), or when a policy references a function not in the helper list (C.3.3, C.3.5).

### C.3.9 Cross-tenant access: `X-Act-On-Tenant`

| Header value | Who | Effect | Audit |
|---|---|---|---|
| `<tenant uuid>` | `platform_admin` (reads need `platform:cross_tenant_read`, writes `platform:cross_tenant_write`) | principal acts as `tenant_admin` of that tenant: `app.tenant_id` = target, `app.role` = `tenant_admin`, `app.steward` = on | one `security_events` row per request |
| `*` | `platform_admin` or `support` with `platform:cross_tenant_read`; `GET`/`HEAD` only | read-only bypass: `BEGIN READ ONLY` + `app.bypass_tenant=on` | one `security_events` row per request |

- Accepted only on the internal listener (web via BFF, which forwards it on an allow-list); the public listener rejects it with `400 header_not_allowed`. Dispatchers never use it: their reach is the `dsp` scope (R4).
- Applied by `iam.RBAC.Authorize` inside `RequireAuth` for every authenticated request (T07; the `RequireCrossTenant()` of the drafts), so no route can ignore the header. A principal without a platform role (or an API key) gets `403 permission_denied` and no audit row; a malformed or repeated header is `400 bad_request`; an unknown tenant id `404 not_found`.
- For a platform principal on the internal listener, every request that carries the header writes `security_events{event_type:'platform_cross_tenant_access', severity:'warning', actor_user_id, tenant_id: <the target when it exists, else NULL>, details:{method, path, act_on_tenant, outcome, request_id, ip, platform_roles}}` in its **own committed transaction before** the handler's transaction begins, whatever the outcome: `Authorize` decides first, commits the row, then returns the refusal or applies the target. So a refused attempt is on record too: `details.outcome` is `accepted`, `refused` (403: support with a uuid, a write with `*`, a missing capability; C.9.2 #16-#17), `repeated` or `malformed` (400) or `unknown_tenant` (404), and those last three carry `tenant_id` NULL (it is an FK to `tenants`). `act_on_tenant` is the header value (repeated values joined with `, `) reduced to printable ASCII and at most 128 bytes, so a crafted header can neither bloat the row nor make its insert fail. If the insert fails the request fails with `503 unavailable` (no access without audit); a committed row for a request that later fails is harmless. A same-transaction insert is impossible for `*` because that transaction is read-only. Rows are append-only (trigger `security_events_immutable` in Appendix A `0008_platform` + `REVOKE UPDATE, DELETE` at the `0009` grant site), as today (`firestore.rules:475-478`).
- Capabilities while acting (T07): with `<uuid>` the principal holds the `tenant_admin` set of the target (all 64 keys: acting as a tenant makes it a steward) plus its platform set, and gets the target's contractor reach; with `*` a `platform_admin` holds the `tenant_admin` set plus its platform set for reads only (GET/HEAD and the READ ONLY transaction), `support` only its own set; `RequireTenant` passes under `*` because the bypass covers every tenant.

### C.3.10 Write-time tenant resolution, orphans and quarantine

`internal/platform/tenancy` is the pure Go port of `tenantResolve.ts` with its 223-line test file ported (`tenantResolve.test.ts`, issue T28). `tenant_source` is an inline `text NOT NULL CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine'))` on every tenant-stamped table (R11, R58; no DOMAIN, no ENUM). Main spec §13.5 owns the per-collection resolution table; the summary below is what the RLS and service checks rely on.

| Table | Resolution order (first non-empty wins) | Notes |
|---|---|---|
| `tasks` | `driver` -> `form` (caller's active tenant; dispatcher's own tenant per R13) | `tenantResolve.ts:100-101` has `driver` only; `form` covers driverless call-outs |
| `trip_records` | `task` -> `driver` | `tenantResolve.ts:103-107` |
| `standby_records` | `task` -> `trip` -> `driver` | `tenantResolve.ts:109-114` |
| `incident_reports` | `trip` -> `driver` | `tenantResolve.ts:116-120` |
| `drivers` | `self` (tenant chosen at creation / legacy `subcontractorId`) -> own fleet | `tenantResolve.ts:122-127` |
| `trucks` | `self`: `ownership_type='subcontractor'` -> that tenant, else own fleet | `tenantResolve.ts:129-135` |
| `truck_assignments` | `truck` (tenant of the assigned truck) | Appendix A §A.3.0; unresolved truck or driver -> `rejected` |
| `vehicle_expenses`, `driver_penalties`, `payroll_runs`, `leave_requests`, `chats`, `mobile_installations` | `driver` (expenses without driver -> `truck`) | Appendix A ETL tenant derivation |
| `maintenance_records`, `vehicle_locations`, `transactions` (truck shape) | `truck` | Appendix A ETL tenant derivation |
| rate tables, `trip_billing_snapshots`, `billing_counters`, `billing_statements`, `driver_compensation_configs`, `transactions` (payout shape) | `form` = billing carrier / own fleet | C.3.4, R61 |

- The service asserts `app_tenant_in_reach(resolved tenant)` when a parent link exists: a tenant_admin cannot create a trip on another tenant's task (`403 truck_not_in_tenant` / `tenant_link_mismatch`).
- Rows never take `tenant_id` from a request header or body, except the three audited move paths (R13 task reassignment, C.1.4 driver move, quarantine re-home below).
- ETL loads every row (R11, R19): when the chain runs out the row is loaded with `tenant_id = app_quarantine_tenant_id()` and `tenant_source='quarantine'`, plus a row-level `etl.quarantine` entry. Legacy driverless tasks (D6) go to quarantine and are bulk re-homed after the owner confirms (main spec §19).
- `GET /v1/tenants/quarantine/rows?table=&cursor=` (`platform:cross_tenant_read`) lists quarantined rows with the resolver trace; `POST /v1/tenants/quarantine/rows/{table}/{id}/rehome {tenantId}` (`platform:cross_tenant_write`) moves one row and its children (under `WithSystem` with `app.tenant_move=on`; the deferred link triggers re-check at commit; appends `tenant_rehomed`).
- Scheduler job `tenancy.orphan-scan` (daily 03:00 Asia/Bangkok, main spec §7) counts quarantine rows per table, exports metric `tenant_orphan_rows_total{table}` and appends `tenant_orphans_detected` (severity `warning`) when the count is above zero.

---

## C.4 Tokens and sessions

### C.4.1 Access JWT (R3)

`github.com/golang-jwt/jwt/v5` (v5.3.1), algorithm `EdDSA` (Ed25519), header `{"alg":"EdDSA","typ":"JWT","kid":"<kid>"}`. Parse with `jwt.WithValidMethods([]string{"EdDSA"})`, `jwt.WithIssuer(JWT_ISSUER)`, `jwt.WithAudience(JWT_AUDIENCE)`, `jwt.WithExpirationRequired()`, `jwt.WithLeeway(30*time.Second)`. Issuer and audience are environment values, never literals.

| Claim | Value | Notes |
|---|---|---|
| `iss`, `aud` | `JWT_ISSUER`, `JWT_AUDIENCE` | |
| `iat`, `nbf`, `exp` | `exp = iat + JWT_ACCESS_TTL` (default 15 m) | |
| `sub` | `users.id` | |
| `jti` | uuidv7 | log correlation only; revocation is by `sid` / `ver` |
| `sid` | `sessions.id` | |
| `ver` | `users.auth_version` at issue time | |
| `tid`, `rol` | active tenant and its membership role | absent for customer-scope and platform-only principals |
| `plt` | `["platform_admin"]`, `["support"]` | absent when empty |
| `dsp` | `true` | present only with a dispatcher grant |
| `drv` | `drivers.id` | present only when `rol = "driver"` |
| `cs` | array of `billing_parties.id` | customer scope or dispatcher grant; the API caps scopes at 20 per user (`422 too_many_scopes`), so the claim is always complete |
| `amr` | `"pwd"` \| `"google"` | a session created by `POST /v1/auth/exchange` (P7a) takes the Firebase token's `firebase.sign_in_provider` (`password` -> `pwd`, `google.com` -> `google`); Firebase-token requests and API keys never produce a Go JWT |

No capabilities, email, name or tenant list in the token (size and PII); `GET /v1/me` serves them.

### C.4.2 Signing keys, rotation, JWKS

| Env | Content |
|---|---|
| `JWT_SIGNING_KEY_FILE` | path to the PKCS#8 PEM Ed25519 private key (secret file mount) |
| `JWT_PREVIOUS_KEY_FILE` | optional path to the previous key (private or public PEM); verify-only |
| `JWT_ACTIVE_KID` | `kid` of the signing key |

- `kid` of every key is its RFC 7638 JWK thumbprint (base64url SHA-256 of `{"crv","kty","x"}`). At start-up the api computes the thumbprint of `JWT_SIGNING_KEY_FILE` and refuses to start when it differs from `JWT_ACTIVE_KID` — a guard against mounting the wrong key. The previous key's `kid` is its own thumbprint.
- Rotation runbook: generate a key pair; deploy with the new key as `JWT_SIGNING_KEY_FILE`, the old one as `JWT_PREVIOUS_KEY_FILE`, the new thumbprint as `JWT_ACTIVE_KID`; after `JWT_ACCESS_TTL` + 30 s leeway + the web JWKS cache period (5 min) remove `JWT_PREVIOUS_KEY_FILE`. Emergency rotation (key compromise) skips the previous key and bumps every `users.auth_version` (all sessions re-authenticate through refresh; refresh tokens are not JWTs and stay valid).
- `GET /.well-known/jwks.json` (internal listener only, plan §4b ingress policy): `{"keys":[{"kty":"OKP","crv":"Ed25519","x":"<b64url>","kid":"<kid>","use":"sig","alg":"EdDSA"}, ...]}` with the active and (when configured) previous public keys, `Cache-Control: public, max-age=300`. Consumer: the web `proxy.ts` edge gate (W4) via `jose` `createRemoteJWKSet` against `GO_API_INTERNAL_URL`, cached in memory by `kid`, refetched on an unknown `kid` with a 30 s cooldown; a `kid` first seen inside the cooldown forces one more fetch (at most one per 5 s) before the token counts as invalid, so a rotation signs nobody out on the web (TW3, `lib/bff/accessToken.ts`). Mobile never verifies JWTs; `worker` and `scheduler` hold no JWT keys.

### C.4.3 Request authentication and revocation checks

`RequireAuth` accepts exactly one of:

1. `Authorization: Bearer <Go JWT>` — signature, `iss`, `aud`, `exp` checked in memory; then one Redis pipeline: `EXISTS lt:{APP_ENV}:auth:sess:revoked:{sid}` and `GET lt:{APP_ENV}:auth:user:ver:{sub}` (issue T05: "revocation via `auth:ver`"; key shape of Appendix B §B.6.2). A `ver` miss, and a token whose `ver` is newer than the cached value (the cache lags a bump whose post-commit raise was lost or is still in flight, C.4.7), read `users.auth_version` and `sessions.revoked_at` (`WithSystem`, one query) and rebuild both keys (version cached for 1 h, a revoked marker re-set), so a lost post-commit write heals on the next new token or cache miss instead of rejecting every token issued after it (T05). A revoked `sid` fails with `session_revoked`; a `ver` older than `users.auth_version` on a live session fails with `token_expired` and `details.reason = "claims_changed"`, so the client refreshes (web: `force`, C.4.5) and receives a token with the new claims (R50, R78). When Redis is unreachable (the pipeline is bounded to 300 ms, so a slow Redis cannot hold requests) the same checks run against PostgreSQL (`users.auth_version`, `sessions.revoked_at`) and `auth_revocation_fallback_total` is incremented — the check never fails open. A `ver` newer than `users.auth_version` itself (never issued) is `invalid_token`; a `ver` newer than only the cached value is decided by that PostgreSQL read.
2. `Authorization: Bearer <Firebase ID token>` — only on the shim targets of the public listener (Appendix B §B.2.22) **together with** an `X-Api-Key` of scope `cf_shim`, from P2 **regardless of** `AUTH_FIREBASE_BRIDGE_MODE` (R45). The APK's own Firebase token is accepted only as `idToken` in the body of `POST /v1/auth/exchange` (C.4.6), never as a bearer: a Firebase token alone is `401 invalid_token` on every route.
3. `X-Api-Key: <secret>` (C.4.11); a `cf_shim` key also needs (2).

Failures use the error envelope (R48, R76): `401 {"error":{"code":"token_expired"|"session_revoked"|"invalid_token"|"unauthenticated","message":"...","details":{...},"requestId":"..."}}` (`unauthenticated` = no credential; `token_expired` carries `details.reason` = `expired` \| `claims_changed`, R78). Clients refresh only on `token_expired` (or when no token is held); `session_revoked` goes straight to the login screen. Account state after a valid credential: `403 account_disabled`, `403 driver_profile_required`, `403 password_change_required` (C.4.8).

### C.4.4 Sessions and refresh tokens (R2)

DDL: Appendix A `0002_identity` (§A.2.1). `sessions.id` is the JWT `sid`; `install_id` is the mobile install id (API field `installId`, R83; `logitrack-mobile/lib/core/services/mobile_install_id_service.dart`), NULL on the web; `absolute_expires_at` caps the session; `revoked_reason` values used here: `logout`, `logout_all`, `admin_revoke`, `disabled`, `password_changed`, `password_reset`, `refresh_reuse`, `device_relogin`, `expired`. The active tenant that refresh re-issues as `tid` is kept per session in `active_tenant_id uuid NULL REFERENCES tenants(id)`, written at login and by `POST /v1/auth/tenant`. `refresh_tokens` keeps `family_id` (= `sessions.id` for the login family), `token_hash` (sha256 of the 43-character base64url token string, UNIQUE; the token is never stored; password-reset tokens are hashed the same way) and `rotated_at` + `replaced_by` (set together).

- **Issue.** Login creates a `sessions` row and the first refresh token (32 random bytes, base64url). The default tenant (`defaultTenantId`, stored as `active_tenant_id`) is the active tenant of the user's most recent session when that membership is still active, else the first active membership with the own fleet first, then by `name_th`; memberships in a `suspended` tenant are never issued as `tid` (T05). A new login on a device that already has a live session for the same user first revokes the older one (`device_relogin`; no `user.sessions_revoked` event, because the push would reach the very install that is signing in); the partial unique index `sessions_user_install_live (user_id, install_id) WHERE install_id IS NOT NULL AND revoked_at IS NULL` enforces it (R83). Redis `lt:{APP_ENV}:auth:rt:{sha256hex}` = `{sessionId, familyId, userId, exp}` with TTL = remaining life is the fast path; PostgreSQL is the source of truth and is written in the same transaction as every rotation.
- **TTLs (R3).** Web: `expires_at = min(now + REFRESH_TOKEN_TTL_WEB, absolute_expires_at)` with `REFRESH_TOKEN_TTL_WEB` default 7 d sliding and `absolute_expires_at = created_at + 30 d`. Mobile: `REFRESH_TOKEN_TTL_MOBILE` default 90 d absolute, no sliding (drivers must not be logged out in the field; today Firebase refresh tokens never expire).
- **`POST /v1/auth/refresh`** `{refreshToken}`: unknown or expired -> `invalid_token`; session revoked -> `session_revoked`; otherwise mark `rotated_at`, insert the successor (same family), update `sessions.last_seen_at`, re-read the active membership (a removed membership falls back to another active one, else no `tid`) and the current `auth_version`, and return `{accessToken, expiresIn, refreshToken}`.
- **Reuse detection with a 30 s grace (R37).** A token with `rotated_at IS NOT NULL` is reuse unless it was rotated less than 30 s ago and its successor has never been presented (concurrent refresh from two tabs, a retried mobile request); in that grace case a sibling successor is issued and the earlier successor revoked (`refresh_tokens.revoked_reason = 'superseded'`; presenting it afterwards is `invalid_token`), and the rotated token's `replaced_by` points at the sibling while its `rotated_at` keeps the first rotation time, so the window never grows. Real reuse revokes the whole family and the session, bumps `users.auth_version`, appends `security_events{event_type:'refresh_token_reuse', severity:'critical'}` in the same transaction, emits outbox `user.sessions_revoked` and returns `session_revoked`.
- **Lock order (T05).** Every transaction that writes `sessions` or `refresh_tokens` locks the user's `users` row first (`SELECT ... FOR NO KEY UPDATE`), then `sessions`, then `refresh_tokens`: refresh resolves the token's user without a lock (`sessions.user_id` never changes) before it locks the token and its session; logout, tenant switch, login, password set and every revocation (`RevokeInTx`, so also T19's disable, role, scope and admin-revoke paths) lock the user first. Concurrent requests of one user therefore wait instead of deadlocking (`40P01`). Memory-hard hashing never runs while the row is held: a new password is hashed before the transaction.
- **Clean-up.** Scheduler job `auth.token-cleanup` (every 10 min, main spec §7) deletes refresh tokens and password reset tokens expired or used more than 1 day ago and sessions revoked or expired more than 30 days ago (kept that long for the Security Center session history).

### C.4.5 Web: BFF session cookies and the edge gate (W2-W4, R36-R39)

The browser never calls Go. Next.js (standalone, behind Caddy) is the only web client of Go and talks to `GO_API_INTERNAL_URL` over the private network. Go returns tokens in the JSON body to this internal caller (`platform:"web"`) and **never sets cookies**; only the BFF does. The BFF auth routes are exactly these (R38):

| BFF route | Go call (internal listener) | Cookies |
|---|---|---|
| `POST /api/auth/login` | `POST /v1/auth/login` (`platform:"web"`) | sets `lt_at`, `lt_rt` |
| `GET /api/auth/google/nonce` | `GET /v1/auth/google/nonce` | none |
| `POST /api/auth/google` | `POST /v1/auth/google` | sets both on success |
| `POST /api/auth/refresh` | `POST /v1/auth/refresh` with the `lt_rt` value in the body; a no-op `204` while `lt_at` has more than 120 s left, unless the body is `{"force":true}` (R78) | rotates both; refused (no `lt_rt`, or Go `401`): clears both, `401`; any other failure (Go `429`/`5xx`, unreachable `502`, timeout `504`) passes through and keeps both |
| `GET /api/auth/refresh?next=` | `POST /v1/auth/refresh` with the `lt_rt` value (always rotates, no 120 s no-op), then `303` to `next` (a same-origin path under `/app`, else `/app`); serves navigations that `proxy.ts` redirected. Top-level navigations only: any other `Sec-Fetch-Mode` is `400` without rotating | rotates both; refused (Go `401`, or no `lt_rt`): clears both, `303` `/login?next=` (`&reason=revoked` for `session_revoked`); Go unreachable or failing: `503` (`504` on timeout) with `Retry-After`, cookies kept |
| `POST /api/auth/logout` | `POST /v1/auth/logout` (bearer from `lt_at`, `refreshToken` from `lt_rt`) | clears both |
| `POST /api/auth/tenant` | `POST /v1/auth/tenant` | replaces `lt_at` |
| `POST /api/auth/firebase-token` | `POST /v1/bridge/firebase-token` (C.6.3, R40) | none (returns the custom token) |

Everything else goes through the generic proxy `app/api/go/[...path]/route.ts`, which maps `/api/go/v1/...` to Go with `Authorization: Bearer <lt_at>`, `X-Request-Id`, `X-Forwarded-For`, forwards `X-Act-On-Tenant`, and **returns 404** for `v1/auth/(login|google|refresh|tenant|logout|logout-all|sse-ticket|exchange)` and `v1/bridge/*` (R38). `v1/auth/password/*` passes through it. The proxy attaches `lt_at` whenever the cookie exists, so a ticket change may arrive with a stale or another user's bearer: Go then ignores the `Authorization` header whenever the body carries `passwordChangeTicket` (C.4.8); forgot and reset never read a bearer. The web offers no "log out everywhere" call; the session list (`DELETE /v1/me/sessions/{sid}`) covers it. Two more unauthenticated route handlers, `POST /api/forms/waitlist` and `POST /api/forms/partner-interest`, read no cookie and forward the anonymous forms to the internal `POST /v1/waitlist` / `POST /v1/partner-interest` with `X-Forwarded-For` (R44, R77).

| Cookie | Value | Attributes (R36) |
|---|---|---|
| `lt_at` | access JWT | `HttpOnly; Secure` (when `SESSION_COOKIE_SECURE`); `SameSite=Lax`; **`Path=/`** (so `proxy.ts` sees it on `/app/*`); `Max-Age` = `JWT_ACCESS_TTL`; `Domain` = `SESSION_COOKIE_DOMAIN` (host-only when empty) |
| `lt_rt` | refresh token | same flags; **`Path=/api/auth`** (never sent to `/api/go/*` or pages); `Max-Age` = `REFRESH_TOKEN_TTL_WEB`, re-set on every rotation, capped by the session's absolute expiry |

R36 replaces the cookie paths of plan W3. The web server therefore also reads `JWT_ISSUER`, `JWT_AUDIENCE`, `JWT_ACCESS_TTL` and `REFRESH_TOKEN_TTL_WEB` (R39).

- **Refresh (R37, R78).** The generic proxy never refreshes: it cannot see `lt_rt`. On a `401` whose `error.code` is `token_expired` (or when no `lt_at` exists) the browser's `goFetch` wrapper runs **one** shared refresh — `POST /api/auth/refresh` serialized across tabs with `navigator.locks` — and retries the request once. When `details.reason` is `claims_changed` (or SSE `session.revoked` arrives with that reason, C.4.7) it sends `{"force":true}`, which skips the 120 s no-op; inside the lock the `localStorage["lt:lastRefreshAt"]` and `["lt:lastForcedRefreshAt"]` timestamps (no token material) let a tab skip a refresh that another tab completed after its trigger (a `claims_changed` caller counts only a forced one, since an unforced refresh may have been the 120 s no-op; T17). Any other 401, or a failed refresh, clears the TanStack cache and goes to `/login?next=` (`QueryCache.onError`, W5). Refreshes that still race (a `GET /api/auth/refresh?next=` navigation outside the lock) are merged by the BFF, one Go rotation per refresh token per web process (main spec §10.4 step 2); the 30 s reuse grace in Go (C.4.4) covers a retry and requests on different web replicas, where the browser stays signed in only if it applies the later `Set-Cookie` last.
- **Edge gate (`proxy.ts`, R39).** No `lt_at`, or an expired one -> `307` to `/api/auth/refresh?next=<path>` (which refreshes through `lt_rt` or lands on `/login`). A present `lt_at` is verified with `jose` against the JWKS (C.4.2), checking `iss` = `JWT_ISSUER` and `aud` = `JWT_AUDIENCE`; then the `GET /v1/me` capability check of C.2.7 runs. This replaces the client-side guard and the per-load `setAdminClaims` call (C.7 #15, #18).
- **CSRF.** Mutating BFF requests must carry `Origin` equal to `WEB_PUBLIC_ORIGIN`; without `Origin` they need `Sec-Fetch-Site: same-origin`, and a present `Sec-Fetch-Site` other than `same-origin` is refused (main spec §10.3, TW3); Go is bearer-only, so cookie CSRF does not reach it.
- **Realtime.** Web SSE is same-origin through the generic proxy (`/api/go/v1/events`, cookie -> bearer, W7); no SSE ticket on the web. Go ends a web stream at the access token's `exp`; the client refreshes and reopens `EventSource` with `Last-Event-ID`.

### C.4.6 Mobile tokens, session exchange and SSE ticket

- Login / Google login send `platform` (`android`|`ios`), `installId` (the install id already generated by `mobile_install_id_service.dart`, stored as `sessions.install_id`, R83) and `appVersion`; the response carries `accessToken`, `refreshToken`, `expiresIn`. Every driver-app endpoint is under `/v1/mobile/*` (R42; aliases and the anonymous version gate in C.8).
- **Session exchange (P7a, R42).** The first launch of APK 4.x calls `POST /v1/auth/exchange {idToken, platform, installId, appVersion}` with the Firebase ID token of the still-signed-in Firebase user; Go verifies it (C.6.2; the route answers `404` unless the mode is `mobile` or `both`), resolves the user by `users.legacy_auth_uid` and returns the login body. Afterwards the app uses Go tokens only. No password is re-entered and no Firebase credential is stored by Go.
- The refresh token is stored with `flutter_secure_storage` (Android Keystore / iOS Keychain; the package is not in `logitrack-mobile/pubspec.yaml` today); the access token stays in memory. A serialized 401 interceptor refreshes once and otherwise routes to the login page; it replaces `AuthSessionListener` (`logitrack-mobile/lib/core/auth_session_listener.dart:32-45`) and the `getIdToken(true)` call on resume (`logitrack-mobile/lib/features/home/presentation/pages/main_layout.dart:194-238`).
- **SSE ticket (R4, mobile only).** `POST /v1/auth/sse-ticket` (bearer; `403` unless the session platform is `android` or `ios`) returns `{ticket, expiresIn: 60}`; Redis `lt:{APP_ENV}:auth:sse:{ticket}` = `{userId, sessionId}`, consumed once with `GETDEL`. The stream is `GET /v1/mobile/events?ticket=` on the public listener; the internal web stream `GET /v1/events` accepts only a bearer (from the BFF), never a ticket. The stream closes on `session.revoked` and when the session expires. Query-string bearer tokens are never accepted (they leak into proxy logs).

### C.4.7 Revocation and force logout (replaces `forceLogoutAt`)

Claim changes keep the user signed in; only disable, password events, admin revoke and refresh-token reuse end sessions (R50).

| Trigger | `auth_version++` | Sessions revoked | SSE `session.revoked` `reason` | `security_events` |
|---|---|---|---|---|
| membership added / role changed / removed | yes | none | `claims_changed` | `user_role_changed` |
| customer scope or dispatcher grant changed | yes | none | `claims_changed` | `user_scope_changed` |
| driver link changed | yes | none | `claims_changed` | `driver_linked` / `driver_unlinked` |
| platform role granted / revoked | yes | none | `claims_changed` | `platform_role_granted` / `platform_role_revoked` |
| user disabled (or soft-deleted) | yes | all | `disabled` | `user_disabled` / `user_deleted` |
| password changed by the user | yes | all except the current | `password_changed` | `password_changed` |
| password reset / temporary password issued | yes | all | `password_reset` | `password_reset_completed` / `user_password_temporary_issued` |
| admin revokes sessions | no | selected / all | `admin_revoke` | `user_sessions_revoked` |
| `POST /v1/auth/logout-all` (mobile) | yes | all own | `logout_all` | — |
| `POST /v1/auth/logout`, `DELETE /v1/me/sessions/{sid}` (T05) | no | the one session (logout also deletes that install's `device_tokens` row) | `logout` | — |
| refresh token reuse | yes | family + session | `refresh_reuse` | `refresh_token_reuse` (critical) |

- Every row above is one transaction (under `WithSystem` in `internal/iam` / `internal/auth`, after Go authorization): update rows, bump `auth_version`, set `sessions.revoked_*`, append the `security_events` row, insert outbox `user.sessions_revoked {userId, sessionIds, reason}` with `realtime_topics = ['user:{uid}']`. That outbox event is also bound to queue `notify.fcm` (routing key `user.sessions_revoked`, R50, R84): for every revoked session the consumer sends an FCM data message of type `session_revoked` to the `device_tokens` row whose `install_id` equals that session's `sessions.install_id`, logged in `notification_deliveries` with kind `session_revoked` (claims-only changes revoke no session and send no push). Post-commit hook: `SET lt:{APP_ENV}:auth:user:ver:{uid}`, `SET lt:{APP_ENV}:auth:sess:revoked:{sid}` (TTL = `JWT_ACCESS_TTL` + 30 s) for each revoked session, `DEL` the `auth:rt:*` entries of those sessions (hashes read in the transaction). (`auth:fbuid:{uid}` needs no `DEL`: it is only a hint that is checked against the row on every use, C.6.2.) The version raise and the revoked markers are security-relevant until the access tokens they judge expire, so a failed write is retried in the background with capped backoff (100 ms to 5 s) until it lands or `JWT_ACCESS_TTL` + 30 s have passed, and counted in `auth_postcommit_failures_total{op}` (T05); every token issue (login, refresh, tenant switch) also raises the cached version. A process that dies between COMMIT and the hook loses the retry: the outbox relay (which already reaches Redis to publish `rt:user:{uid}`) re-applies the hook idempotently from `user.sessions_revoked` (`SET auth:sess:revoked:{sid}` for each `sessionIds` entry, raise `auth:user:ver:{uid}` from `users.auth_version`), giving at-least-once delivery. As built (T10): `auth.RevocationHook` is registered as the relay hook of `user.sessions_revoked` in the scheduler (`JWT_ACCESS_TTL` + 30 s on the markers); it runs before the row is published, so the marker exists before `rt:user:{uid}` closes the streams; a Redis or PostgreSQL error stops the batch at that row like a failed publish and it is retried; an unreadable payload or a deleted user is logged and skipped. The `auth:rt:*` deletes stay best effort (Refresh always decides in PostgreSQL).
- The outbox relay publishes on Redis `rt:user:{uid}` (R22); every api replica pushes SSE `event: session.revoked` to that user's streams. Web: `claims_changed` -> `POST /api/auth/refresh {"force":true}` and refetch `['me']` (still signed in; the edge cache keyed by `(sid, ver, tid)` misses on the next navigation); any other reason -> clear cookies, go to `/login` (~1 s; today a Firestore listener on `users/{uid}.forceLogoutAt`, `logitrack-web/context/auth.tsx:91-129`). Mobile: `claims_changed` -> refresh; other reasons -> `401 session_revoked` on the next request, at once over an open SSE stream, and the FCM data message reaches a backgrounded app (today a revoked driver keeps working until the SDK refresh fails, as-is auth report §6).
- Admin routes cannot target the caller (kept from `logitrack-web/functions/src/authSessions.ts:23-28` and `users.ts:318-320`); users manage their own sessions through `/v1/me/sessions` and `logout-all`.

### C.4.8 Passwords

- Argon2id via `github.com/alexedwards/argon2id` v1.0.0 (wraps `golang.org/x/crypto/argon2`): `ARGON2_MEMORY_KB` (default 65536), `ARGON2_ITERATIONS` (default 3), `ARGON2_PARALLELISM` (default 2), 16-byte salt, 32-byte key, PHC string. Parameters are read back from the stored hash; a successful login with weaker stored parameters re-hashes.
- Policy: length `PASSWORD_MIN_LENGTH` (default 10) to 128, NFC-normalised Unicode, must not equal the email local part or the user's mobile digits, must not be in the embedded top-10k list; no composition rules, no periodic expiry. Violations are `422 invalid_argument` with `details.fields[0] = {field: "newPassword", reason: too_short|too_long|equals_email|equals_mobile|too_common, params: {min, max}}`. T05 embeds a seed subset of the common list (`internal/auth/password/common.txt`); the vetted top-10k list replaces it before the P0 cut-over. Driver minimum length is owner decision D5 (main spec §19).
- Admin-issued temporary password: 12 characters from a 32-symbol unambiguous alphabet (`crypto/rand`), returned once in the API response, never logged, never emailed (R29); sets `must_change_password=true`. `drivers:set_password` always generates, never accepts a caller-chosen value.
- Login of a `must_change_password` user returns **no tokens**: `403 {"error":{"code":"password_change_required","details":{"passwordChangeTicket":"...","expiresIn":600}}}`. The single-use ticket (Redis `lt:{APP_ENV}:auth:pwchg:{ticket}`, 10 min) is redeemed at `POST /v1/auth/password/change {passwordChangeTicket, newPassword}` -> `204` (R79: this route completes the flow; there is no separate ticket route); the client then logs in with the new password (on the web through `POST /api/auth/login`, so only the BFF ever sets cookies). This keeps the JWT claim set at R3 (no restricted-scope token). The policy is checked before the ticket is consumed (`GETDEL`), so a rejected password does not burn it; an unknown, expired or used ticket is `422 invalid_argument` (`details.fields[0].field = "passwordChangeTicket"`, reason `invalid_or_expired`), and with a bearer `currentPassword` is required (`422 invalid_argument`, reason `required` when missing, `mismatch` when wrong), never a 401 that would sign the client out (T05). When the body carries `passwordChangeTicket` the ticket form is used and any `Authorization` header is ignored (the BFF proxy forwards `lt_at` whenever the cookie exists, C.4.5). The ticket stores `{userId, authVersion}` and is void once `auth_version` moved or `must_change_password` was cleared (a reset or a new temporary password after it was issued). A user without a password (Google-only) creates one through forgot / reset (C.5.6), never through the bearer form. Shim principals (C.6.2) are not blocked by the flag before P7a, because APK 3.x has no Go password flow; the flag takes effect at their first Go login or exchange.
- Every password set (change, reset, temporary password) writes `password_changed_at = now()`; the transparent Argon2id re-hash of a legacy or weaker hash (C.5.4) does not, because the plaintext is unchanged. A login opens its session, and replaces a legacy or weaker hash, only while the `users` row (under the lock) still holds the credential it verified: a password set by a reset or change that commits while the login's hash runs wins, and the racing login answers `401 invalid_credentials` (T05).
- Memory-hard work is bounded per process: at most `GOMAXPROCS / ARGON2_PARALLELISM` (at least 1) Argon2id or Firebase scrypt computations run at once; a caller waits at most 3 s for a slot and otherwise gets `503 unavailable` (the login attempt is then not counted). This keeps unauthenticated logins from allocating `ARGON2_MEMORY_KB` per request without bound (T05; a code constant, no environment name).
- Lockout: 5 failed attempts per email within 15 min lock the email for 15 min (`423 locked`; buckets `rl:login_ip` and `rl:login_fail` of Appendix B §B.6.3; `security_events` `login_lockout`). The attempt is counted atomically before the password is checked and a success clears the count, so concurrent wrong passwords cannot all pass the check (at most 5 verifications per window). Every failed check costs one Argon2id verification and, while `FIREBASE_SCRYPT_*` is configured, one Firebase scrypt verification, whichever credential the account holds (unknown email, Google-only, `reset_required`, Argon2id or legacy user; dummy computations fill the gap), so response time reveals neither whether an account exists nor whether it signed in since the import (T05); a correct password on a `disabled` account returns `403 account_disabled`, a wrong one, an unknown email, a `deleted` user and a `reset_required` user the generic `401 invalid_credentials`.

### C.4.9 Password reset and invite tokens

DDL: `password_reset_tokens` in Appendix A `0002_identity` (`purpose` `reset` \| `invite`, sha256 `token_hash`, `used_at`, `requested_by` = inviting admin).

- `POST /v1/auth/password/forgot {email}` always returns **202** (no account enumeration). For an existing user whose status is `active` or `reset_required`, the api inserts outbox `auth.password_reset_requested {userId, purpose:'reset', locale, requestedIp}`; the token is **not** created by the api and never travels through the outbox.
- The `notify.email` consumer (R22) generates the token (32 random bytes, base64url), inserts the hash with `expires_at = now() + PASSWORD_RESET_TTL` (default 30 min), sends the en/th email with link `{PUBLIC_WEB_BASE_URL}/reset-password#token=<token>` (fragment: the token stays out of server logs and `Referer`; the page sends `Referrer-Policy: no-referrer`) and then commits. A crash after sending and before commit invalidates that link; the retry sends a new one.
- Invites (R29): `POST /v1/users` with `sendInvite:true` emits `user.created {userId, sendInvite:true, purpose:'invite', requestedBy, locale}`, and `POST /v1/users/{id}/invite` emits `user.invited {userId, purpose:'invite', requestedBy, locale}` (TTL 72 h, code constant). The `notify.email` consumer mails on `user.created` only when `sendInvite` is `true` and then always an invite, so a creation without `sendInvite:true` never mails, whatever `purpose` it carries (T10). No password is ever emailed.
- The consumer creates the token with `auth.Service.IssuePasswordResetToken(ctx, tx, userID, purpose, requestedIP, requestedBy)` inside its transaction (T05), which inserts only the hash.
- `POST /v1/auth/password/reset {token, newPassword}` (an unknown, used or expired token, or one of a disabled user, is `422 invalid_argument` with `details.fields[0].field = "token"`): valid, unused, unexpired token of a non-disabled user -> set `password_hash`, `legacy_scrypt_hash = NULL`, `legacy_scrypt_salt = NULL`, `must_change_password = false`, `status = 'active'`, mark used, revoke all sessions, bump `auth_version`, mirror to Firebase while the bridge is on (C.6.4), append `password_reset_completed`; `204`.
- Mobile has no reset flow today (`logitrack-mobile/lib/features/auth/presentation/pages/login_page.dart:134-140` shows a snackbar). Until the P7a app, drivers receive temporary passwords from their tenant admin. UNVERIFIED: how many driver accounts carry a deliverable email address (driver Auth users are created or linked from the email typed on the driver form, `logitrack-web/functions/src/triggers.ts:48-131`), which decides whether the 4.x app shows "forgot password".

### C.4.10 Google OIDC

- **Web:** Google Identity Services button with `NEXT_PUBLIC_GOOGLE_OIDC_CLIENT_ID` (the only `NEXT_PUBLIC_` variable this design adds); nonce from `GET /api/auth/google/nonce` -> Go `GET /v1/auth/google/nonce` (Redis `lt:{APP_ENV}:auth:google:nonce:{nonce}`, 10 min, single use); ID token -> `POST /api/auth/google` -> Go `POST /v1/auth/google {idToken, nonce, platform:'web'}`. No authorization-code flow (R23, R35/T06): no client secret, no redirect URL.
- **Mobile:** `google_sign_in` `authenticate()` with `serverClientId` = dart-define `GOOGLE_OIDC_CLIENT_ID` (the same OAuth web client the installed APKs pass today as `FIREBASE_WEB_CLIENT_ID`, `auth_repository.dart:117-120`) -> `POST /v1/auth/google {idToken, platform, installId}` without nonce. On iOS the token still carries a `nonce` claim the app never sees: `google_sign_in_ios` passes a nil nonce to GoogleSignIn-iOS, whose AppAuth authorization request then makes up a random one (`OIDAuthorizationRequest` `generateState`) and Google copies it into the ID token. It is not one of ours, and the app cannot fetch one either: `google_sign_in` 7 takes a nonce only in `initialize()`, once per process, so a single-use 10-minute server nonce would break the next sign-in of the same process and the lightweight/restore path. Go therefore ignores the nonce of a driver-app token (below).
- **Verification:** `github.com/coreos/go-oidc/v3` v3.21.0, `oidc.NewProvider(ctx, "https://accounts.google.com")`; because the verifier takes one client id, it is built with `SkipClientIDCheck: true` and `aud` is checked against the comma list `GOOGLE_OIDC_ALLOWED_CLIENT_IDS` (must contain the client id used by installed APKs and the web client id). Required: valid signature, issuer, `exp`, `email_verified == true`, `nonce` match for `platform=web`.
- **Verification details (T06, `internal/auth/google`).** Discovery is lazy: it runs on the first Google sign-in, not at start-up, so the api starts while Google is unreachable; a failed discovery is retried at most every 10 s (code constant). The keys come from the discovery document's `jwks_uri` through go-oidc's `RemoteKeySet` (cached, refetched when a token names an unknown `kid`); only `RS256` is accepted; go-oidc also accepts Google's `iss` spelling `accounts.google.com`. **Every** `aud` value must be in `GOOGLE_OIDC_ALLOWED_CLIENT_IDS` (OIDC Core §3.1.3.7); `azp` is not checked against the list, because the driver app's token carries `aud` = the server client id and `azp` = its Android or iOS client (GoogleSignIn-iOS sends `audience=serverClientID`), and Google mints a token for another client's `aud` only inside one Cloud project. `azp` instead tells a driver-app token (`azp` present and not among its `aud` values) from a GIS token (`azp` = `aud`) for the nonce rule below; `hd` (Workspace domain) is read for the email rule. `email_verified` is read as a boolean or the string `"true"`; `sub` and `email` must be present. When the discovery document or the keys cannot be fetched the token is not judged: `503 unavailable`, never a 401. Each entry of `GOOGLE_OIDC_ALLOWED_CLIENT_IDS` must end in `.apps.googleusercontent.com` (start-up check, value never echoed); while the variable is unset Google sign-in is off and both routes answer `404` (as `POST /v1/auth/exchange` does outside its bridge modes). Tests use `internal/auth/google/googletest`: discovery and a JWKS with a locally generated RSA key served through an in-process `http.RoundTripper`, so no test reaches Google or opens a socket.
- **Request and errors (T06).** `{idToken, nonce?, platform: web|android|ios, installId?, appVersion?}` (no `script` platform). A missing `idToken`, a `platform=web` request without `nonce` and a `nonce` that cannot be one of ours (not 32 bytes base64url) are `422 invalid_argument` (`details.fields[0].field` `idToken` / `nonce`, reason `required` / `invalid`). A token that fails verification (signature, issuer, expiry, an `aud` outside the list, `email_verified` not true) is `401 invalid_token`. **Nonce binding:** a body `nonce` (required on the web, optional elsewhere) must equal the token's `nonce` claim and be an unused issued one, and only then is it consumed (`GETDEL auth:google:nonce:{nonce}`). Without a body nonce (`android`/`ios`), a token with no `nonce` claim passes, and so does a driver-app token (`azp` ≠ `aud`) whose `nonce` is the SDK's own (iOS, above), which is ignored; a GIS-shaped token (`azp` = `aud`, or no `azp`) carrying a `nonce` is refused. A mismatch, an unknown, expired or already used nonce is `401 invalid_token`, and a mismatch consumes nothing. So a GIS token signs in once even if it is replayed through the driver-app path without its nonce; sending the SDK's nonce in the body is `401` too (it is the shape of ours but was never issued). `rl:google_ip` 30/min per IP on `POST`, `rl:google_nonce_ip` 60/min per IP on `GET /v1/auth/google/nonce` (C.4.12).
- **Account resolution:** `auth_identities(provider='google', provider_subject=sub)` -> that user; else, when Google is authoritative for the token's email (below), a user whose verified email matches and who has no Google identity yet -> link (insert `auth_identities`, append `google_identity_linked`); else `403 no_account`. There is no self-signup and no hosted-domain switch: today `onUserCreated` auto-creates a `role:'user'` doc for any new Firebase user (`logitrack-web/functions/src/triggers.ts:12-31`), which under multi-tenancy would be a member of nothing.
- **Resolution details (T06).** "Verified email" is the token's `email` with `email_verified == true` **for which Google is authoritative** (Google, "Authenticate with a backend server", fields `email`, `email_verified`, `hd`): an `@gmail.com` or `@googlemail.com` address, or any address of a Google Workspace / Cloud Identity account (`hd` present; its admin verified the domain; `hd` need not equal the email's domain, so secondary domains work). For any other address `email_verified` only says the address was verified when the Google account was created; whoever held the mailbox then (a former employee of a carrier whose mail runs on Microsoft 365, a recycled shared mailbox) could own that Google account, and a link would outlive every later password reset. Such a token links nothing and is `403 no_account`, decided before any user lookup, so it does not reveal whether the address has a user (not even a disabled one); the server logs `reason=email_not_authoritative`, never the address. An address with a non-ASCII character is never authoritative (Google issues none for Gmail or Workspace users), and only ASCII letters are lower-cased, so no case folding (U+212A KELVIN SIGN -> `k`) can map a look-alike onto an ASCII address. This is a fixed rule, not a hosted-domain switch (none exists, main spec §16.4). The match is case-insensitive (`citext`) against a non-deleted user; `users.email_verified` is not required (imported password users mostly carry `false`) and would not help, since it proves who held the mailbox when we verified it, not who owns the Google account. Firebase does not link these either: it treats Google as a trusted provider only for Gmail addresses, so today a Google sign-in on another domain's email/password account is refused (`auth/account-exists-with-different-credential`, one account per email) or creates a separate `role:'user'` uid (multiple accounts per email); neither inherits the password user's access. Users who sign in with Google today keep working: the import links them by `sub` (`providerUserInfo[google.com].rawId`, C.5.5; Appendix A `auth_identities`). Resolution and link run in one `WithSystem` transaction under the user's row lock (`LockUser`, C.4.4 lock order), so concurrent first sign-ins of one account link once; the link writes `auth_identities` (`email_at_link`, `linked_at`, `last_used_at`) and `google_identity_linked` (severity `info`, actor = target = the user, no tenant, `details {provider:'google', linkedBy:'verified_email', platform, clientId}`) in that transaction. A user already linked to another Google `sub` is never re-linked (`403 no_account`); `deleted` and `reset_required` users (no usable credential, C.5.8: they recover through forgot-password or a temporary password) are `403 no_account`; a `disabled` user is `403 account_disabled` and is never linked. A `must_change_password` user gets `403 password_change_required` with a ticket and no tokens (R79, as login). The session (`amr` `google`, also on refresh) is then created as for a password login (C.4.4: default tenant, `device_relogin`, `last_login_*`, outbox `user.logged_in`); its transaction also writes the identity's `last_used_at` and refuses the session when the link vanished meanwhile.
- Disabled users are rejected before a session is created; both login paths write `last_login_*` (today only the mobile Google path does, `auth_repository.dart:170` vs `:71-109`).

### C.4.11 API keys (service callers, callable shims)

DDL: `api_keys` in Appendix A `0002_identity`: `name`, `scope` (`text NOT NULL CHECK (scope IN ('integration','script','cf_shim','release_publisher'))`, R82), `key_prefix` (8 chars, UNIQUE, shown in the UI), `key_hash` (32 bytes = sha256(secret ‖ `API_KEY_PEPPER`)), `tenant_id` (NULL = platform-level key, created only by `platform_admin`; CHECK `api_keys_cf_shim_platform` makes every `cf_shim` key a platform key), `capabilities text[]` (non-empty, subset of the catalog, never `platform:*` or `dispatch:*`), `rate_per_min`, `created_by`, `expires_at`, `last_used_at`, `revoked_at`, `revoked_by`.

- Secret format `ltk_<prefix>_<43 base64url chars>`; shown once by `POST /v1/api-keys` (P2, R49); lookup by `key_prefix`, constant-time hash comparison; `API_KEY_PEPPER` exists only in the `api` process. Sent as `X-Api-Key` (not `Authorization`, because shim requests also carry the end user's bearer). `last_used_at` is written asynchronously (batched); per-key limit `rl:apikey:{id}`.
- Tenant keys may hold only `tenant`-class keys of their tenant; platform keys may hold `global`-class and `mobile:*` keys. The stored `scope` decides listener and routes; a key used outside its scope gets `403 permission_denied`:

| `scope` (R82) | Key | Listener | Principal | Allowed routes |
|---|---|---|---|---|
| `integration`, `script` | tenant or platform key | internal (inside the private network) | service principal with exactly the key's capabilities; RLS context `app.role='user'` in the key's tenant (staff reach, no contractor reach), or `app.steward=on` without tenant for a platform key; `app.user_id` = the key id (T07: `authz.Principal` with `AMR="apikey"`; issuance and scope routing T32) | any internal route its capabilities allow, except `PUT /v1/app-releases/floor`, which accepts only a human JWT (ADR 0007: publishing a build never moves the floor) |
| `release_publisher` (R43) | platform key holding `security:manage_mobile_release` | internal | service principal | only `POST /v1/app-releases/presign` and `POST /v1/app-releases` |
| `cf_shim` (R25, R45) | platform key holding only the `mobile:*` keys of the shim targets | public, shim targets only | the **end user** identified by the Firebase ID token that must accompany the key (C.6.2), verified from P2 regardless of `AUTH_FIREBASE_BRIDGE_MODE`; the key adds attribution (`APIKeyID`) and the shim counter used as retirement evidence (R26) | the shim targets of the five mobile-called callables (C.6.5), paths as in Appendix B §B.2.22 |

The APK publish CLI `cmd/release` runs on the private network (on the VM or a CI job over SSH) with a `release_publisher` key read from env `RELEASE_API_KEY` and calls the internal `POST /v1/app-releases/presign` and `POST /v1/app-releases` at `GO_API_INTERNAL_URL` (R43, R82); no release route exists on the public listener.

### C.4.12 Rate limits and lockout

Redis GCRA buckets `lt:{APP_ENV}:rl:{bucket}:{subject}` (names of Appendix B §B.6.3); switch `RATE_LIMIT_ENABLED`; configurable limits `RATE_LIMIT_LOGIN` (login per IP, format `count/window`, default `10/1m`) and `RATE_LIMIT_PUBLIC_FORMS` (anonymous waitlist and partner-interest forms, R44); other thresholds are code constants (changing them is a code change reviewed with the security tests). The `login_fail` lockout (5 failures / 15 min, then locked 15 min from the fifth failure; each attempt is counted before its password check and cleared by a success, C.4.8) is an account-protection rule: it is a code constant and applies whatever `RATE_LIMIT_ENABLED` says. The request buckets of the auth routes (`login_ip`, `refresh_session`, `sse_ticket`, `forgot_email`, `forgot_ip`, `reset_ip`) run through the process's shared GCRA limiter (`internal/platform/httpx/ratelimit`, T09): `internal/auth` calls `Limiter.Allow` inside the handler, where the subject is known (IP subjects through `ratelimit.IPSubject`, so an IPv6 client counts by its /64; session ids; the normalised email), and answers `429 resource_exhausted` with `Retry-After` and `details.bucket` like the middleware; the fixed-window counters of T05 are gone. `RATE_LIMIT_ENABLED` and `RATE_LIMIT_LOGIN` are parsed once (`ratelimit.Config`, which also refuses an interval `window/count` under 1 µs) and handed to auth. Only the `login_fail` lockout keeps a counter of its own in `internal/auth` (`rl:login_fail:{sha256hex(email)}`, Appendix B §B.6.2). A Redis error lets the request through (logged): the buckets protect capacity, not credentials; ticket stores answer `503 unavailable` instead.

| Endpoint | Limit |
|---|---|
| `POST /v1/auth/login` | `login_ip` 10/min per IP; `login_fail` 5 failures / 15 min per email (subject = sha256 of the email) -> `423 locked` for 15 min |
| `POST /v1/auth/google` | `google_ip` 30/min per IP |
| `GET /v1/auth/google/nonce` | `google_nonce_ip` 60/min per IP (T06: the route writes a Redis key per call) |
| `POST /v1/auth/exchange` | `exchange_ip` 30/min per IP |
| `POST /v1/auth/refresh` | `refresh_session` 60/min per session |
| `POST /v1/auth/password/forgot` | `forgot_email` 3/h, `forgot_ip` 20/h; always `202` |
| `POST /v1/auth/password/reset`, ticket `/password/change` | `reset_ip` 10/h per IP |
| `POST /v1/auth/sse-ticket` | `sse_ticket` 30/min per session |
| API keys | `api_keys.rate_per_min` (`apikey` bucket) |
| everything else | per-user global limiter (`user` bucket) |

### C.4.13 Security events written by auth and IAM

Appended **in the same transaction** as the change (R22), by `internal/security.Append(ctx, tx, security.Event{...})` under `WithSystem` (no audit, no change; the package, its `securitydb` sqlc block and `security.Event`, whose camelCase JSON is the outbox `security.event` payload, ship with T05): `refresh_token_reuse` (critical), `device_token_moved` (warning, T13: `PUT /v1/me/devices` took a push token from another user's row of the install the caller's session signed in on; actor the caller, target the previous holder, `details {installId, sessionId, legacy}`), `user_created`, `user_invited`, `user_role_changed`, `user_scope_changed`, `user_disabled`, `user_enabled`, `user_deleted`, `user_sessions_revoked`, `user_password_temporary_issued`, `password_changed`, `password_reset_completed`, `google_identity_linked`, `driver_linked`, `driver_unlinked`, `driver_tenant_moved`, `task_tenant_reassigned`, `platform_role_granted`, `platform_role_revoked`, `role_matrix_saved` (full diff), `api_key_created`, `api_key_revoked`, `tenant_created`, `tenant_contractor_changed`, `tenant_rehomed`. `platform_cross_tenant_access` (warning) is committed in its own transaction before the handler (C.3.9). Written by the `security.audit` consumer from outbox `security.event`: `login_failed`, `login_lockout`, `password_reset_requested`, `tenant_orphans_detected`. `user.logged_in` never becomes a `security_events` row (it only updates `users.last_login_*`). Other domains use the same two paths: in-transaction `trip_record_renamed`, `mobile_floor_changed` (`PUT /v1/app-releases/floor`), `queue_replayed` (DLQ replay); through the consumer `evidence_viewed`, `webhook_signature_failed` (sampled). These names and placements are normative (R85; Appendix B §B.5.7 copies them); the draft names `password_reset` and `driver_password_reset` do not exist (now `password_reset_completed` and `user_password_temporary_issued`, the latter for drivers and staff alike). The audit page's type filter is generated from these lists (today `logitrack-web/lib/securityEventsConstants.ts:2-7` lists 4 types and misses `user_disabled` / `user_enabled`).

### C.4.14 Redis keys owned by auth (prefix `lt:{APP_ENV}:`, R26)

Appendix B §B.6.2 is normative for `auth:rt:{sha256(token)}`, `auth:sess:revoked:{sid}`, `auth:user:ver:{userId}`, `auth:sse:{ticket}`, `auth:google:nonce:{nonce}`, `auth:fbuid:{firebaseUid}` (string, `users.id`, 5 min, a hint checked against the row on every use, C.6.2), `rbac:ver`, `rbac:caps:{tenantId|platform}:{role}:{ver}:{fp}`, `cache:tenant:own_fleet` and the `rl:{bucket}:{subject}` buckets. Auth also owns two keys that Appendix B §B.6.2 copies:

| Key | Type / TTL | Content |
|---|---|---|
| `auth:pwchg:{ticket}` | string / 10 min, single use (`GETDEL`) | `{userId, authVersion}` of the must-change-password ticket; void once `auth_version` moved (C.4.8, R79) |
| `cache:tenant:subtenants:{tenantId}` | string JSON / 10 min | contractor reach (C.3.4), deleted on outbox `tenant.created` / `tenant.updated` |

### C.4.15 Attestation

App Check is not enforced by rules today (`logitrack-web/firestore.rules:8-10` returns true). R30: no attestation in the parity scope; `MOBILE_ATTESTATION_MODE` defaults to `off`. Turning it to `log` / `enforce` (Play Integrity on `/v1/auth/*` for Android) is a later owner decision (main spec §19).

### C.4.16 Environment names used by this appendix

Names only; spelling, secret flag, consuming process and phase are those of main spec §16 (R23, R74). Database: `DATABASE_URL` (`logitrack_app`; also `seed --verify`), `MIGRATE_DATABASE_URL` (`logitrack_migrator`; also `seed --reset`), `ETL_DATABASE_URL` (`logitrack_etl`; `etl` and `seed` writes) (R66, R87). Auth: `JWT_SIGNING_KEY_FILE`, `JWT_PREVIOUS_KEY_FILE`, `JWT_ACTIVE_KID`, `JWT_ISSUER`, `JWT_AUDIENCE`, `JWT_ACCESS_TTL`, `REFRESH_TOKEN_TTL_WEB`, `REFRESH_TOKEN_TTL_MOBILE`, `ARGON2_MEMORY_KB`, `ARGON2_ITERATIONS`, `ARGON2_PARALLELISM`, `PASSWORD_MIN_LENGTH`, `PASSWORD_RESET_TTL`, `GOOGLE_OIDC_ALLOWED_CLIENT_IDS`, `PLATFORM_ADMIN_EMAILS`, `API_KEY_PEPPER`, `RATE_LIMIT_ENABLED`, `RATE_LIMIT_LOGIN`, `RATE_LIMIT_PUBLIC_FORMS`, `MOBILE_ATTESTATION_MODE`. Migration and bridge: `FIREBASE_SCRYPT_SIGNER_KEY`, `FIREBASE_SCRYPT_SALT_SEPARATOR`, `FIREBASE_SCRYPT_ROUNDS`, `FIREBASE_SCRYPT_MEM_COST`, `AUTH_FIREBASE_BRIDGE_MODE`, `FIREBASE_PROJECT_ID` (securetoken issuer and audience; distinct from `ETL_FIRESTORE_PROJECT_ID`), `GOOGLE_APPLICATION_CREDENTIALS`, `OWN_FLEET_TENANT_ID` (seed / etl only); Cloud Functions params of the shims `LOGITRACK_API_BASE_URL`, `LOGITRACK_API_KEY`. Release CLI: `RELEASE_API_KEY` (consumer `release`, R82) with `GO_API_INTERNAL_URL`. Email: `SMTP_HOST`, `SMTP_PORT`, `SMTP_USER`, `SMTP_PASSWORD`, `SMTP_FROM`, `PUBLIC_WEB_BASE_URL`. Ingress: `API_INTERNAL_ADDR`, `API_PUBLIC_ADDR`, `TRUSTED_PROXY_CIDRS`. Web (server-only): `GO_API_INTERNAL_URL`, `GO_API_INTERNAL_TIMEOUT_MS`, `SESSION_COOKIE_DOMAIN`, `SESSION_COOKIE_SECURE`, `WEB_PUBLIC_ORIGIN`, `JWT_ISSUER`, `JWT_AUDIENCE`, `JWT_ACCESS_TTL`, `REFRESH_TOKEN_TTL_WEB` (R39); web public: `NEXT_PUBLIC_GOOGLE_OIDC_CLIENT_ID`. Mobile dart-define: `API_BASE_URL`, `SSE_BASE_URL`, `GOOGLE_OIDC_CLIENT_ID`. Also referenced: `APP_ENV` (Redis prefix), `EVIDENCE_TOKEN_TTL_DAYS`, `CARTRACK_API_USERNAME`, `CARTRACK_API_PASSWORD` (C.7). No build-time API URL or domain-flag variable exists; domain flags come from `GET /v1/config/web-flags` (Go env `WEB_FLAG_OVERRIDES`) (R41). There is no authorization-code client secret or redirect setting, no shared internal token (machine callers use `api_keys`), no Google hosted-domain or self-signup switch, and no default-member-domain variable (`cmd/etl auth-import --default-member-domain` is a CLI flag) (R74).

---
## C.5 Firebase user migration

Decision (§2 of the plan, D4 / R30): **verify Firebase scrypt in Go and re-hash to Argon2id on first login**; forced reset only for the flagged weak driver passwords and for the 180-day tail. Forced reset for everyone is not viable: mobile has no reset flow and nearly all driver passwords were set by admins (`logitrack-web/functions/src/triggers.ts:66-69`).

### C.5.1 Export (ETL step `auth-import`, issue T19, phase P0)

- `firebase auth:export users.json --format=json --project <firebase-project-id>` for dev and prod. Per user: `localId`, `email`, `emailVerified`, `displayName`, `photoUrl`, `passwordHash` (base64), `salt` (base64), `providerUserInfo[]` (`providerId`, `rawId`, `email`), `customAttributes` (JSON string of the claims), `disabled`, `createdAt`, `lastSignedInAt` (ms epoch).
- The export contains password hashes and is handled as a secret: never in the repo, passed to `cmd/etl auth-import --export <path>` from an encrypted location, deleted after the last import (C.5.8).
- Joined with Firestore `users/{uid}` docs for `lastLogin` (ISO string **or** Auth-metadata string), `lastLoginLat/Lng/GeoSource/LocationAccuracyM` (legacy nested `lastLoginLocation` as fallback), `fcmTokens`, and the scope mirrors. Claims are authoritative; the users doc is a mirror (`logitrack-web/functions/src/users.ts:124-144, 239-250`).

### C.5.2 Hash parameters

The four project-level parameters come from **Firebase Console -> Authentication -> Users -> "Password hash parameters"** (`base64_signer_key`, `base64_salt_separator`, `rounds`, `mem_cost`), **not** from the `auth:export` output, which contains only per-user `passwordHash` and `salt` (plan §5; corrects the delivery-plan draft). They are stored as `FIREBASE_SCRYPT_SIGNER_KEY`, `FIREBASE_SCRYPT_SALT_SEPARATOR` (secrets), `FIREBASE_SCRYPT_ROUNDS`, `FIREBASE_SCRYPT_MEM_COST`, present only in the `api` process (login) and in `cmd/etl` runs of `auth-weak-scan`; never in `worker` or `scheduler`. Owner action: main spec §19, open question 1.

### C.5.3 Verifying Firebase scrypt in Go

Algorithm (verified by the auth design against the `firebase/scrypt` source: `main.c:147-160`, `lib/scryptenc/scryptenc.c:99-121`): salt = `base64dec(user.salt) || base64dec(salt_separator)`; derive `scrypt(password, salt, N = 2^mem_cost, r = rounds, p = 1, 64 bytes)`; AES-256 in CTR mode with key = derived bytes `[0:32]` and an all-zero 16-byte IV, applied to the 64-byte decoded signer key; the base64 of the result must equal `passwordHash`.

```go
// internal/auth/firebasescrypt/verify.go
package firebasescrypt

import (
    "crypto/aes"
    "crypto/cipher"
    "crypto/subtle"
    "encoding/base64"
    "fmt"

    "golang.org/x/crypto/scrypt"
)

// Params: Firebase Console -> Authentication -> Users -> "Password hash parameters".
type Params struct {
    SignerKey     []byte // decoded FIREBASE_SCRYPT_SIGNER_KEY (64 bytes)
    SaltSeparator []byte // decoded FIREBASE_SCRYPT_SALT_SEPARATOR
    Rounds        int    // FIREBASE_SCRYPT_ROUNDS  -> scrypt r (block size), not a loop count
    MemCost       int    // FIREBASE_SCRYPT_MEM_COST -> scrypt N = 1 << MemCost
}

// salt and want are users.legacy_scrypt_salt / legacy_scrypt_hash (bytea, decoded once by cmd/etl with DecodeB64).
func Verify(password string, salt, want []byte, p Params) (bool, error) {
    s := make([]byte, 0, len(salt)+len(p.SaltSeparator))
    s = append(append(s, salt...), p.SaltSeparator...)                // salt || separator (main.c:148-151)
    dk, err := scrypt.Key([]byte(password), s, 1<<p.MemCost, p.Rounds, 1, 64)
    if err != nil {
        return false, err
    }
    block, err := aes.NewCipher(dk[:32])                               // AES-256 key = first 32 derived bytes
    if err != nil {
        return false, err
    }
    got := make([]byte, len(p.SignerKey))
    cipher.NewCTR(block, make([]byte, aes.BlockSize)).XORKeyStream(got, p.SignerKey) // CTR, zero IV, over the signer key
    return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// DecodeB64 (used by cmd/etl auth-import) accepts standard and URL-safe alphabets, padded or not.
// UNVERIFIED: which alphabet a given export uses; accepting both is harmless.
func DecodeB64(v string) ([]byte, error) {
    for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
        if b, err := enc.DecodeString(v); err == nil {
            return b, nil
        }
    }
    return nil, fmt.Errorf("not base64")
}
```

`x/crypto/scrypt` requires N to be a power of two greater than 1, which `1 << mem_cost` is. Golden tests in `internal/auth/firebasescrypt/verify_test.go` use the known vectors of the `firebase/scrypt` repository (the README example, `tests/01-known-value.sh`, the 13 lines of `tests/02-more-values-passwords.good` and of `tests/03-more-values-different-key-passwords.good`, which uses another signer key) plus one fixture user exported from the dev project with a throw-away password (issue T05 acceptance). The public vectors are all ASCII: the non-ASCII and real-export check is that dev fixture user (owner item until the dev project is reachable).

### C.5.4 Login with a legacy hash

```
POST /v1/auth/login {email, password}
  user := users WHERE email = $1 AND status <> 'deleted'           (WithSystem)
  count the attempt (C.4.8; > 5 in 15 min -> 423 locked); every failed check costs one Argon2id + one scrypt
  (dummies fill the gap), 401 invalid_credentials
  if user.password_hash != NULL:   ok := argon2id.Compare(...)       (re-hash when stored params are weaker)
  elif user.legacy_scrypt_hash != NULL: ok := firebasescrypt.Verify(password, legacy_scrypt_salt, legacy_scrypt_hash, params)
       if ok: in one transaction, while the locked row still holds the verified hash, set password_hash = argon2id(password),
              legacy_scrypt_hash = NULL, legacy_scrypt_salt = NULL
  else: ok := false                                                  (Google-only, or tail reached)
  !ok -> failure counter, 401 invalid_credentials
  status = 'disabled' -> 403 account_disabled
  must_change_password -> 403 password_change_required {passwordChangeTicket} (C.4.8)
  else create session + tokens (C.4.4) under the user lock, only while the row still holds the verified credential,
       write last_login_*
```

The re-hash keeps the plaintext unchanged, so no Firebase mirror is needed for it (the Firebase hash stays valid for the APK during coexistence).

### C.5.5 Claims and users-doc mapping

| Source (`customAttributes` / users doc / export) | PostgreSQL result |
|---|---|
| `admin === true` or `role === 'admin'` | `memberships(own_fleet, 'tenant_admin')`; no platform role (D1) |
| email in `PLATFORM_ADMIN_EMAILS` | additionally `user_platform_roles(platform_admin, granted_by NULL)` + `security_events platform_role_granted {source:'bootstrap'}`; applied by `cmd/seed bootstrap-platform-admins`, idempotent |
| `role` ∈ manager / operation_staff / operator / user | `memberships(own_fleet, role)` |
| `role === 'driver'` and/or `driverId` | driver row by `drivers.legacy_doc_id = driverId`, else `drivers.legacy_auth_uid = uid`; `drivers.user_id = user`; `memberships(drivers.tenant_id, 'driver')`. A stale `driverId` left after a role change (`logitrack-web/functions/src/users.ts:78`) is ignored when `role !== 'driver'` and reported |
| `role === 'partner'` + `partnerScopeId` | `memberships(tenant WHERE legacy_doc_id = partnerScopeId, 'tenant_admin')` (D2); missing tenant -> report, no membership. `partnerScopeId` is free text today (`logitrack-web/app/app/security-center/users/page.tsx:95-100`) |
| `role === 'customer'` + `customerScopeId` | `user_scopes(kind='customer', billing_party_id)` resolved through `customers.legacy_doc_id`, else `tenants.legacy_doc_id`; unresolved free text (create dialog allows it, `users/page.tsx:816-826`) -> report, no scope |
| `companyId` | ignored (never written by any code path) |
| no role / unknown role (today the fallback `user`, `logitrack-web/lib/permissions.ts:17-25`) | no membership by default; `cmd/etl auth-import --default-member-domain <domain>` (CLI flag, not env) grants `memberships(own_fleet, 'user')` to emails of that domain; everyone else listed in the report |
| `disabled: true` | `status='disabled'`, `disabled_at` = export time |
| `lastSignedInAt`, users doc `lastLogin` (either format) | `last_login_at` = latest parsed value; geo fields copied |
| `createdAt` / `metadata.creationTime` | `legacy_auth_created_at` |
| `providerUserInfo[google.com].rawId` | `auth_identities(provider='google', provider_subject=rawId)` |
| `localId` | `users.legacy_auth_uid` + `auth_identities(provider='firebase_legacy')` |
| `passwordHash`, `salt` | `legacy_scrypt_hash`, `legacy_scrypt_salt` (bytea, `DecodeB64` of each) |
| users doc `fcmTokens` (map `{app: token}` or legacy array) + `drivers.fcmToken` | `device_tokens` rows, deduplicated by token; `install_id` unknown for legacy tokens -> `'legacy:'` followed by the first 16 hex characters of `sha256(token)` so the PK `(user_id, install_id)` holds until the device re-registers |
| users doc `forceLogoutAt`, `role`, `partnerScopeId`, `customerScopeId`, `providerData` | dropped (claims are authoritative; force logout is session-based) |
| `permissions_config/{role}` | C.2.6 |

### C.5.6 Special accounts

| Case | Handling |
|---|---|
| Google-only (no `passwordHash`) | `password_hash` and the legacy scrypt pair NULL; signs in with Google; may create a password through forgot-password (the email link proves ownership) |
| password + Google | both paths work; Google sign-in never touches the password |
| disabled | imported disabled, no sessions; re-enable through `POST /v1/users/{id}/enable` |
| no email (phone-only or anonymous provider) | not imported; listed in the report (UNVERIFIED: whether any exist — the apps only offer email/password and Google) |
| duplicate email across uids (case-insensitive, `citext`) | second user not imported; report for owner decision |

### C.5.7 Weak-password scan (offline, before the P0 cut-over)

`cmd/etl auth-weak-scan --candidates-file <path>` runs `firebasescrypt.Verify` for every driver user against a local candidate file that the operator builds from each driver's mobile digits and the legacy fallback literal of `logitrack-web/functions/src/triggers.ts:66-69, 212-215` (the literal is never committed or written into the spec). Matches get `must_change_password = true` and are listed per tenant for the tenant admins. The candidate file is deleted after the run.

### C.5.8 Tail and re-import

- **Re-import before P7a.** Drivers keep signing in to Firebase Auth until the 4.x app; a password changed on the Firebase side after the first import (legacy `updateDriverAccount` until its traffic reaches zero, console resets) would leave a stale legacy hash. Before the P7a rollout `cmd/etl auth-import --refresh-legacy` re-reads a fresh export and replaces `legacy_scrypt_hash` / `legacy_scrypt_salt` only where `password_hash IS NULL`; it never overwrites an Argon2id hash.
- **180-day tail (R30).** T0 = the day the P7b gate passes (every client authenticates through Go). At T0 + 180 days `cmd/etl auth-tail` sets `legacy_scrypt_hash` and `legacy_scrypt_salt` to NULL for every user and `status = 'reset_required'` for users with no `password_hash` and no Google identity; then the export file and the `FIREBASE_SCRYPT_*` secrets are destroyed. A `reset_required` user gets the generic `401 invalid_credentials` at login (no enumeration); web users use forgot-password, drivers get a temporary password from their tenant admin.

### C.5.9 Migration report and sign-off

`migration_users_report.csv` (uid, email, outcome `imported|skipped|quarantined`, memberships, scopes, flags `weak_password|stale_driver_claim|unresolved_scope|no_role|duplicate_email`) and `migration_rbac_report.csv` (C.2.6) are signed off by the owner before the P0 web-login cut-over. `seed --verify` / ETL reconciliation asserts: every exported uid is either a `users` row or listed in the report; every `drivers.user_id` has a driver membership; no user holds `platform_admin` unless listed in `PLATFORM_ADMIN_EMAILS`.

---

## C.6 Firebase bridge

During the strangler (web first, mobile last) two directions are needed at the same time (R8): installed APKs still authenticate with Firebase and call callables, and web pages that have not moved yet still read Firestore under `firestore.rules`. One flag controls both: `AUTH_FIREBASE_BRIDGE_MODE = off | mobile | web | both`. The bridge uses no Firebase Admin SDK: ID tokens are verified with `go-oidc`, custom tokens are minted as RS256 JWTs with the service-account key (`GOOGLE_APPLICATION_CREDENTIALS`), and account changes go through the Identity Toolkit REST API with an OAuth2 token from the same service account.

### C.6.1 Modes and phases

The flag governs only what the **APK itself** and the **web** may do with Firebase credentials. The five callable shims are outside it (R45): a request carrying a `cf_shim` API key plus the caller's Firebase ID token is verified from P2 onwards in every mode, because the shim, not the APK, talks to Go.

| Mode | APK Firebase ID token at `POST /v1/auth/exchange` (route `404` otherwise) | Mints custom tokens for the web | Firebase account mirror (C.6.4) |
|---|---|---|---|
| `off` | no | no | no |
| `mobile` | yes | no | yes |
| `web` | no | yes | yes |
| `both` | yes | yes | yes |

| Phase | Mode | Reason |
|---|---|---|
| P0 (`off` in dev until the web-login cut-over) | `web` | web logins move to Go (TW3) while web pages still read Firestore; mobile still talks to Firebase directly, so users and password changes made in Go must be mirrored for the APK |
| P1-P6 | `web` | pages move domain by domain; shims (T32, from P2) work without the `mobile` mode; web Firestore reads remain until TW7 removes the Firebase SDK from the web at the end of P6 |
| P7a, P7b, P8 until Cloud Functions are deleted | `mobile` (`both` only while P7a overlaps an unfinished TW7) | APK 4.x exchanges its Firebase session for a Go session (R42); 3.x APKs keep signing in to Firebase and calling the shims until the P7b gate and the >= 2 release cycles of zero callable traffic (`shared-docs/.vibe-rules.md` API-versioning rule, as of commit 4f552099), so the mirror stays on |
| after P8 | `off` | `GOOGLE_APPLICATION_CREDENTIALS` is removed from the `api` process (FCM keeps its own credential in `worker`) |

### C.6.2 Firebase ID tokens accepted by Go (shims, exchange, APK)

- Verifier (T08, `internal/auth/firebase`): built directly, without OIDC discovery, as `oidc.NewVerifier("https://securetoken.google.com/"+FIREBASE_PROJECT_ID, oidc.NewRemoteKeySet(ctx, "https://www.googleapis.com/service_accounts/v1/jwk/securetoken@system.gserviceaccount.com"), &oidc.Config{ClientID: FIREBASE_PROJECT_ID, SupportedSigningAlgs: ["RS256"]})` (Firebase ID tokens carry the project id as `aud` and that URL as `iss`, RS256; the JWK URL is the JSON form of the X.509 key list the Admin SDK reads, so nothing depends on a discovery document; `FIREBASE_PROJECT_ID` is its own setting, never `ETL_FIRESTORE_PROJECT_ID`, R74). Keys are fetched on the first token and cached; keys that cannot be fetched are `503 unavailable`, never a judgement of the token. Beyond go-oidc (signature, `iss`, `aud`, `exp`): `sub` 1-128 bytes, `iat` and `auth_time` present and not more than 5 minutes ahead (the Admin SDK's skew), and no `firebase.tenant` claim (the project has no Identity Platform tenants; a tenant token would name a uid of another namespace).
- Additional checks: the user found by `users.legacy_auth_uid = sub` (unknown: `403 no_account`; its id cached as `lt:{APP_ENV}:auth:fbuid:{uid}` for 5 min as a hint only: the row is read on every use and a cached id whose row no longer carries the uid is dropped and looked up again, so no invalidation hook is needed) has `status = 'active'` (otherwise `403 account_disabled`; a disabled user is refused immediately) and, when `users.password_changed_at` is set, `auth_time >= password_changed_at` compared in whole seconds like Firebase's own `auth_time` and `validSince` (otherwise `401 invalid_token`: a Firebase session started before a password change or reset is refused even though Firebase still considers the ID token valid). A `must_change_password` user resolves and the caller is told so (the exchange answers it with a password-change ticket, R79). Claim changes need no token check because the principal is rebuilt from PostgreSQL on every request; an admin session revocation reaches Firebase through the mirror's `validSince` (C.6.4), so the remaining ID token lives at most its 1-hour lifetime, as today.
- For a shim request the principal is built from PostgreSQL memberships, scopes and driver link — **never** from the Firebase custom claims — with `AMR = "firebase"` and no session id: the `driver:self` principal of the forwarded token (the driver membership in `drivers.tenant_id` when the user has a linked driver, otherwise the default tenant, own fleet first). `auth.Service.VerifyFirebaseIDToken(ctx, idToken, use)` serves both callers: `FirebaseAPK` (the exchange, `404` unless the mode includes `mobile`) and `FirebaseShim` (every mode); without `FIREBASE_PROJECT_ID` both are `404`. `POST /v1/auth/exchange` instead creates a Go session (C.4.6).
- Accepted in exactly two places, both public route groups (Appendix B §B.2.21-§B.2.22): as the bearer of a shim-target request that also carries a `cf_shim` key (from P2, any mode), and as `idToken` in the body of `POST /v1/auth/exchange` while the mode includes `mobile`. A Firebase token alone as a bearer is `401 invalid_token` on every route, and a `cf_shim` key on a non-target route is `403`.

### C.6.3 Web direction: custom tokens with legacy claims

`POST /v1/bridge/firebase-token` (internal listener only; called by the BFF route `POST /api/auth/firebase-token`, R9, R40) returns `{customToken, expiresIn}`. The token is an RS256 JWT signed with the service-account private key: `iss` = `sub` = the service account's `client_email`, `aud` = `https://identitytoolkit.googleapis.com/google.identity.identitytoolkit.v1.IdentityToolkit`, `iat`, `exp` = `iat` + 3600 s, `uid`, `claims`. The browser calls `signInWithCustomToken`; the resulting Firebase ID token carries the legacy claims that `firestore.rules` and unmigrated pages read.

| Principal (active context) | `admin` | `role` | `driverId` | `customerScopeId` | `partnerScopeId` |
|---|---|---|---|---|---|
| own-fleet `tenant_admin`, or `platform_admin` | `true` | `"admin"` | — | — | — |
| own-fleet manager / operation_staff / operator / user | `false` | the role | — | — | — |
| carrier `tenant_admin` imported from a legacy `partner` | `false` | `"partner"` | — | — | `tenants.legacy_doc_id` |
| customer-scope user imported from a legacy `customer` | `false` | `"customer"` | — | `customers.legacy_doc_id` of the oldest scope | — |
| driver (account mirror only; drivers have no web access) | `false` | `"driver"` | `drivers.legacy_doc_id` (the uuid string for drivers created in Go, R25) | — | — |

"Imported" means the user has an `auth_identities` row of provider `firebase_legacy`, which only `cmd/etl auth-import` writes (main spec §4.11), so a user created in Go never counts as imported even after the bridge gives it a Firebase uid. The `customerScopeId` is the legacy doc id of the oldest customer scope that has one: `customers.legacy_doc_id`, else `tenants.legacy_doc_id` for a scope that C.5.5 resolved through a tenant. A dispatcher grant wins over everything but `platform_admin`; a driver principal, a `support`-only principal and anything outside the rows above get `403 permission_denied` and no Firebase uid. The response carries `Cache-Control: no-store`; `GET /v1/me` returns `legacyAuthUid` only while the mode includes `web` and the user has a uid (T08).

These are exactly the claims the legacy code reads: `isWebAdmin` (`firestore.rules:13-15`), `isCustomer` (`:18-21`), partner reads of `mobile_installations` (`:270-272`, `:485-487`), the maintenance `driverId` gate (`:381-386`), and `getRole` / `isAdmin` (`logitrack-web/lib/permissions.ts:17-25, 87-89`).

**Minting rule.** Custom tokens are minted only for principals whose Firestore exposure already exists today: own-fleet staff (any staff role), `platform_admin`, and users imported from a legacy `partner` or `customer` claim. They are never minted for dispatchers, carrier users created in Go, or customer users created in Go: because the rules let any signed-in user read `tasks`, `trip_records` and `drivers` (C.3.1), a Firebase session for a new carrier or customer user would open cross-tenant reads. Those users see only pages whose domain is already served by Go (main spec §10 lists the per-phase page set). `uid` = `users.legacy_auth_uid`; for an own-fleet user created in Go it is set to `users.id::text` at first mint (Firebase creates the account on first custom-token sign-in).

The Firebase web session refreshes itself independently of Go; on SSE `session.revoked` (any reason except `claims_changed`) and on logout the web client also calls Firebase `signOut()`, and the account mirror revokes Firebase refresh tokens on disable and password events (C.6.4).

### C.6.4 Account mirror (Go -> Firebase Auth)

While the mode is not `off`, account changes made in Go are written to Firebase Auth **synchronously inside the request, before the PostgreSQL commit**; a mirror failure fails the request with `503 bridge_unavailable` so the two stores never diverge. Plaintext passwords exist only in memory for the duration of the call and never enter the outbox, logs or any table.

| Go change | Firebase Auth effect |
|---|---|
| user created (driver or own-fleet staff) | create the account (`localId` = `users.id::text` unless a legacy uid exists), email, password when one was set, merged custom claims; on `EMAIL_EXISTS`, adopt the account only when it is the orphan of a rolled-back creation (implementation rules below), else `409 already_exists` |
| password set (change, reset, temporary) | set the password |
| user disabled / enabled | disable / enable; on disable also set `validSince = now` (revokes Firebase refresh tokens) |
| user soft-deleted (every session revoked with reason `disabled`, C.4.7) | disable, `validSince = now` (the password is left as it is; a disabled account cannot sign in with it) |
| sessions revoked by an admin | `validSince = now` |
| membership role, scope or driver link changed | recompute the legacy claim object (C.6.3 table) and write it **merged** with any unrelated existing attributes — fixes the no-merge overwrite of `triggers.ts:121-125, 236-240` |

Wire format (T08, `internal/auth/firebase.Accounts`; confirmed against the Firebase Admin SDK for Go, `firebase.google.com/go/v4` v4.20.0, `auth/user_mgt.go`): `POST https://identitytoolkit.googleapis.com/v1/projects/{FIREBASE_PROJECT_ID}/accounts` creates (`localId`, `email`, `emailVerified`, `password`, `displayName`, `disabled`), `…/accounts:lookup` reads (`{"localId": [uid]}`, or `{"email": [email]}` for the holder of an email; the response also carries the password hash, which is never decoded), `…/accounts:delete` deletes (`{"localId": uid}`) and `…/accounts:update` writes (`localId`, `password`, `disableUser`, `validSince` as a string of Unix seconds, `customAttributes` as a JSON string of at most 1000 bytes). The bearer is an OAuth2 access token from the RFC 7523 JWT bearer grant of the `GOOGLE_APPLICATION_CREDENTIALS` service account at its `token_uri` (scopes `identitytoolkit` and `cloud-platform`, `golang.org/x/oauth2/jwt`), reused until it expires. Google's error code (`USER_NOT_FOUND`, `DUPLICATE_LOCAL_ID`, `EMAIL_EXISTS`, …) is kept, its detail text never. A write is bounded by 15 s. The api's service account needs **Firebase Authentication Admin** (`roles/firebaseauth.admin`, which carries `firebaseauth.users.create`, `get`, `update` and `delete`) on `FIREBASE_PROJECT_ID`; no token-creator role, because custom tokens and the OAuth2 assertion are signed locally with the key (main spec §16.1). A missing grant is Identity Toolkit `403 PERMISSION_DENIED`, which the mirror turns into `503 bridge_unavailable` on every mirrored change and logs at error level as a service-account fault.

Implementation rules (T08). A user without `users.legacy_auth_uid`, or whose uid Firebase does not know (`USER_NOT_FOUND`: a Go-created staff user who never opened a legacy page), has no Firebase account and nothing is written. Claims are recomputed for the user's default context (own fleet first). One Go session cannot name a Firebase session, so an admin revocation sets `validSince` only when every session is revoked. Entry points in `internal/auth`, each inside the caller's `WithSystem` transaction: `RevokeInTx` (`claims_changed` merges the claims; `admin_revoke` of every session sets `validSince`; `disabled` of every session, the soft-delete row of C.4.7, disables the account and sets `validSince`), `SetStatusInTx` (disable / enable with the session revocation), `SetTemporaryPasswordInTx` with `NewTemporaryPassword` (R29, R79), `MirrorNewUserInTx` (creation for a driver of any tenant or an own-fleet user), and every password set of T05 (change, ticket change, reset). Creation and retries: Firebase is not transactional with PostgreSQL, so an account can outlive the users row it was created for. A failure inside `MirrorNewUserInTx` after the create deletes the account again (best effort); a failure after it (a later write of the caller, a deferred constraint at COMMIT) cannot. The retry inserts a new users row, so a new uid, and meets `EMAIL_EXISTS`; the holder of the email is then looked up and **adopted only when it is such an orphan**: its uid is a canonical uuid that no `users` row (deleted ones included, as id or `legacy_auth_uid`) and no `drivers.legacy_auth_uid` holds. Adoption stores that uid as the user's `legacy_auth_uid` and writes the new password (a random one for an invite, so a password typed for the failed attempt does not survive), the disabled flag, `validSince = now` and the claims. Any other holder (a legacy account `auth-import` has not loaded, a soft-deleted user's account, a self-registered one) is never adopted, since whoever knows its password would sign in as the new user: the creation is `409 already_exists` with `details {"field":"email","reason":"firebase_account_exists"}`, not counted as a mirror failure, and an operator resolves it in the Firebase console. An account that already exists under the user's own uid is updated instead. T19 routes the admin endpoints to them and appends its security events in the same transaction. Metrics: `auth_firebase_mirror_failures_total{op}`, `auth_firebase_custom_tokens_total`.

The mirror replaces the account side of `createDriverAccount`, `updateDriverAccount` and `setDriverClaims`. Because PostgreSQL is the users writer from P0 (main spec §12), the Security Center user actions (create, role, disable / enable, driver link, revoke sessions) and the account part of the driver form switch to the Go endpoints in P0 together with login; the legacy callables then return `failed-precondition`. A nightly `cmd/etl auth-import --delta` between P0 and P1 reports, without overwriting, any Firebase user unknown to PostgreSQL.

### C.6.5 Callable shims (R25, R45)

The five mobile-called callables (`setDriverClaims`, `sendCustomerLineNotification`, `computeTripBillingSnapshot`, `addDeliveryStop`, `markBroadcastRead`) become thin Cloud Functions shims (issue T32, from P2): the callable framework still verifies the caller's Firebase ID token; the shim forwards that token (`request.rawRequest.headers.authorization`) as `Authorization: Bearer` together with `X-Api-Key` (a platform `api_keys` row of scope `cf_shim`, C.4.11; Functions params `LOGITRACK_API_KEY` (secret) and `LOGITRACK_API_BASE_URL` = the public API origin) to the matching `/v1/mobile/*` shim target on the public listener (paths in Appendix B §B.2.22; there are no `/v1/mobile/compat/*` routes), and maps the JSON response back to the callable contract (`{ok, message}`, `{skipped, reason}`). Go verifies the token against the securetoken issuer for `FIREBASE_PROJECT_ID` in every bridge mode and acts as the `driver:self` principal of that user (C.6.2); it counts shim calls per callable (`shim_requests_total{callable}`), which together with Cloud Functions invocation metrics is the retirement evidence (R26). There is no shared internal token and no acting-user header: the end user is always proven by the forwarded Firebase ID token.

### C.6.6 Exit criteria

| Direction off | Criteria |
|---|---|
| `web` | TW7 done: no `firebase` import in the web bundle, `/v1/bridge/firebase-token` at zero traffic for 7 days |
| `mobile` and the mirror | P7b gate passed (≥ 95 % of heartbeats on ≥ 4.0.0, `minAllowedVersion` raised to 4.0.0, ADR 0007), all five shims and every callable at zero traffic for ≥ 2 release cycles, Cloud Functions deleted (P8) |

---

## C.7 Security fixes baked in

Sources: the auth design draft, R28, R29, R30 and the fact-base reports. Paths are repo-relative; `functions/` = `logitrack-web/functions/`.

| # | Gap (as-is) | Evidence | Fix in the Go design | Test (C.9) |
|---|---|---|---|---|
| 1 | `createDriverAccount` has no auth check; `updateDriverAccount` has its admin check commented out and can change any driver's Auth email and password | `functions/src/triggers.ts:48-131`, `:137`, `:142-145`, `:164-202` | `POST /v1/drivers`, `PATCH /v1/drivers/{id}` require `drivers:create` / `drivers:edit` + `RequireTenant()`; RLS `WITH CHECK` pins the tenant; accounts are created only by `POST /v1/users` (`users:manage`), which can only **add** a membership — there is no "set claims" primitive | anonymous -> 401, other tenant -> 403 / RLS error |
| 2 | Claims written without merge (can demote an admin); stale `driverId` survives a role change; `updateUserRole` ignores the `driverDocId` the dialog sends | `functions/src/triggers.ts:121-125, 236-240`; `functions/src/users.ts:78`, `:51-57`; `logitrack-web/app/app/security-center/users/page.tsx:159` | roles are rows (`memberships` PK `(user_id, tenant_id)`); the driver link is `drivers.user_id` + deferred trigger `t_driver_link_membership`; the Firebase mirror merges attributes (C.6.4) | role change touches one row; mirror keeps unrelated attributes |
| 3 | Weak default driver password (mobile digits or a hardcoded literal) | `functions/src/triggers.ts:66-69, 212-215` | no fallback exists; generated temporary password shown once + `must_change_password`; offline weak-password scan (C.5.7) | create without password returns a generated one; scan fixture flags matches |
| 4 | `users/{uid}` is self-writable for every field incl. `role`, `disabled`, `forceLogoutAt`; the Users page displays role from that doc | `logitrack-web/firestore.rules:52-61`; `users/page.tsx:372-398` | only `PATCH /v1/me` (profile columns) + trigger `t_users_self_columns`; role, status and scopes live in tables with no self-service route | `PATCH /v1/me {status}` -> 422; direct update under a principal -> trigger error |
| 5 | Unauthenticated side-effect callables: `notifyTaskUpdate`, `notifyChatMessageCreated`, `getNextTaskId` (count + 1, racy), `checkMaintenanceAlert` | `functions/src/triggers.ts:312`, `functions/src/chat.ts:77`, `functions/src/triggers.ts:397, 413-417, 432`; `logitrack-mobile/lib/features/home/presentation/pages/check_in_page.dart:2017-2022` | pushes are outbox side effects of authenticated writes; task numbers from `next_task_seq()` (global per `(task_type, plan_date)`, R10); PM check inside the approval transaction or `POST /v1/trucks/{id}/pm-check` (`fleet:manage_maintenance`) | route catalog has no client push endpoint; 50 concurrent creates -> 50 distinct numbers |
| 6 | Callables that only require "signed in": `submitDeliveryStopProgress` (no ownership), `sendCustomerLineNotification` (anyone can force a re-send), `notifyMaintenanceReminder`, `getNextRunOrderForDriver`; standby `forceRecompute` not admin-gated | functions report "Security gaps"; `functions/src/standbyBilling.ts:238-261` | every route has `RequireCap` + RLS ownership; forced LINE re-send needs `operations:edit_trip_details`; forced recompute needs `accounting:recompute_force` (`.vibe-rules.md` Confirmed Patterns, as of commit 4f552099) | role × route table |
| 7 | Check-in / add-stop ownership falls back to phone or plate equality (the phone branch reads `phone`, but the driver field is `mobile`) | `functions/src/multiDeliveryTrips.ts:31-53` | R28: ownership = `task.driver_id` or `task.helper_driver_id` equals the principal's driver (`CanCheckIn`, `tasks.p_driver_update`) | same plate, not assigned -> `403 not_task_owner` |
| 8 | Customer and partner scoping only in the browser; rules let any signed-in user read tasks, trips, drivers, trucks, vehicle locations, hubs, settings, broadcasts | `firestore.rules:195-196, 288-289, 258-259, 308-309, 302-303, 336-337, 235-236, 126-127`; `features/drivers/hooks/useDriverMonitor.ts:217`; `app/app/first-mile/page.tsx:262-266` | RLS scope policies (C.3.5) + `scope_*` projections (C.3.7); party ids come from the token, never from the request | two-customer and two-tenant RLS tests |
| 9 | Any customer reads every incident; a customer with zero trips sees all incidents | `firestore.rules:93-96`; `logitrack-web/app/app/incident-reports/page.tsx:185` | `incident_reports.p_scope_read` = incidents of in-scope trips only | customer without trips -> empty list |
| 10 | Partner scope enforced only for `mobile_installations`; partners cannot reach the Security Center page that needs it | `firestore.rules:266-284, 480-489`; `logitrack-web/lib/permissions.ts:55-57` | partner -> carrier `tenant_admin`; RLS on every tenant table; route map by capability | carrier admin sees only its installations |
| 11 | Storage objects publicly readable incl. driver ID-card and licence images; `companies/` has no rule; documents stored as permanent token URLs | `logitrack-web/storage.rules` (integrations report "Storage"); `logitrack-web/features/companies/api/companies.ts:101` | keys only (`file_objects`), private bucket, `GET /v1/drivers/{id}/documents/{kind}` -> 5-minute presigned GET with `drivers:view_pii`; "a file is readable iff its referencing row is" (C.3.5) | without capability -> 403; anonymous object GET -> denied by MinIO |
| 12 | Evidence gallery tokens never expire, cannot be revoked, and hand out permanent image URLs | `functions/src/tripEvidence.ts:118-163`; tokens minted at `functions/src/lineNotify.ts:384-388, 469-473` | R30, R47: tokens stay non-expiring (`EVIDENCE_TOKEN_TTL_DAYS=0`, old LINE cards keep working) but revocable (`evidence_token_revoked_at` on `trip_records` and `standby_records`); `GET /evidence/{token}` renders images through short-lived presigned URLs and logs views; `POST /v1/trips/{id}/evidence/revoke`, `POST /v1/standby/{id}/evidence/revoke` with `operations:edit_trip_details`; the next forced LINE send mints a new token. Supersedes the auth draft's 90-day expiry | revoked token -> 404 page; image URLs expire |
| 13 | Cartrack credentials committed in diagnostic scripts and in the mobile env files bundled into the APK | `functions/scripts/{check-creds,test-cartrack,test-cartrack-full}.js`; `logitrack-mobile/.env.dev`, `.env.prod` bundled by `logitrack-mobile/pubspec.yaml:95-96` (files not opened) | rotate before pushing `mv-go`; `CARTRACK_API_USERNAME` / `CARTRACK_API_PASSWORD` only in the `worker` env (consumer `cartrack.sync`); removed from mobile env files and scripts; gitleaks in CI; diagnostics become `GET /v1/admin/cartrack/probe` (`platform_admin`) | gitleaks job; APK asset check |
| 14 | Google Maps / Cloud Vision keys read from the APK's dotenv | `logitrack-mobile/lib/features/home/data/services/cloud_vision_ocr_service.dart:13-14, 21-22`; `photo_overlay_service.dart:37, 58` | server proxies (`/v1/mobile/ocr/annotate`, `/v1/mobile/geo/*`, main spec §11) with server keys; APK 4.x ships no Google API key | APK string scan |
| 15 | Hardcoded `ADMIN_EMAILS`; `setAdminClaims` runs on every web auth state change; the grant is not logged | `functions/src/auth.ts:5-7, 22`; `logitrack-web/context/auth.tsx:49-61` | `PLATFORM_ADMIN_EMAILS` read only by `cmd/seed bootstrap-platform-admins`; platform roles are rows; grants logged | login code path does not read the env |
| 16 | Three definitions of "admin" | `firestore.rules:13-15`; `token.admin===true` in callables (e.g. `functions/src/users.ts:13`); `logitrack-web/lib/permissions.ts:87-89` | one `Principal`; `RequireCap`; a lint forbids role-string comparisons outside `internal/authz` | lint |
| 17 | Role-matrix keys never applied (three formats); `bangchakOilPrice` hardcodes roles | `app/app/security-center/roles/page.tsx:385-398`; `hooks/usePermission.ts:51-65`; `firestore.rules:32-38`; `functions/src/bangchakOilPrice.ts:8-26` | one catalog (C.2.3); `role_capability_overrides`; cache keyed by `rbac:ver` | an override changes the next request's decision; generated TS file diff-checked in CI |
| 18 | Route guard runs client-side after render; unmapped routes allowed; Security Center admin-only regardless of capability; pages fetch before the guard | `logitrack-web/app/app/layout.tsx:115-142`; `permissions.ts:55-57, 72`; plan §4b.1 | `proxy.ts` edge gate + Go enforcement; unmapped routes denied; queries enabled only after the guard (C.2.7) | Playwright: direct URL without capability issues no data request |
| 19 | Revocation does not reach clients: rules ignore token revocation; mobile ignores `forceLogoutAt` / `disabled`; `revokeUserRefreshTokens` does not set `forceLogoutAt` | `logitrack-mobile/lib/core/auth_session_listener.dart:32-45`; `functions/src/authSessions.ts:9-61` | sessions + `auth_version` + SSE `session.revoked` (C.4.7) | revoked session -> next request 401; open tab logs out < 5 s (issue T18) |
| 20 | Every driver loads the whole `drivers` collection (PII) | `check_in_page.dart:56, 776, 1630` | drivers RLS self-only for drivers; `driver_directory()` minimal projection (C.3.7) | driver principal sees one `drivers` row |
| 21 | User listing capped at `listUsers(1000)` and one batch; the Users page silently shows 50 | `functions/src/users.ts:18, 441-464`; plan §4b.1 | keyset pagination on `GET /v1/users`; `syncExistingUsers` retired | 1 200 seeded users paginate completely |
| 22 | Privileged actions not logged (`setAdminClaims` grant, `setDriverClaims`, `linkDriverToUser`, `createDriverAccount` / `updateDriverAccount` incl. password changes, `syncExistingUsers`); audit filter list misses two types | as-is auth report §7; `logitrack-web/lib/securityEventsConstants.ts:2-7` | in-transaction audit of every IAM write (C.4.13); filter list generated | each IAM endpoint writes exactly one row |
| 23 | `companyId` claim read by rules and a hook but never written | `firestore.rules:532`; `hooks/useCompanyScope.ts:13-21` | dropped; company profiles are tenant rows | catalog test |
| 24 | Passwords would be emailed (runtime draft) | runtime draft `POST /v1/users sendInvite`; legacy default-password path `triggers.ts:66-69` | R29: never emailed; invite = reset link; temporary password shown once | email templates have no password field |
| 25 | Any Google account becomes a `role:'user'` account on first sign-in | `functions/src/triggers.ts:12-31` | no self-signup: `403 no_account` unless an existing user is linked by verified email | unknown Google account -> 403 |
| 26 | Mobile email/password login never records last login | `logitrack-mobile/lib/features/auth/data/repositories/auth_repository.dart:71-109` vs `:170` | server writes `last_login_*` on every login path | both paths update `last_login_at` |
| 27 | App Check helper returns `true`; 23 callables disable App Check | `firestore.rules:8-10`; functions report "Global options" | authorization never depends on attestation; `MOBILE_ATTESTATION_MODE=off` (R30) | flag default test |

---

## C.8 Auth and tenancy endpoints

This appendix owns `/v1/auth/*`, `/v1/me*`, `/v1/users*`, `/v1/tenants*`, `/v1/roles*`, `/v1/api-keys*`, `/v1/security-events` and `/v1/bridge/*` (R4, R46); Appendix B lists the same rows with its own columns and is normative for paths. camelCase JSON bodies and the error envelope of R48; `Authorization: Bearer <access>` unless noted.

**Listener column** (plan §4b ingress policy): `both` = served on `API_INTERNAL_ADDR` (BFF) and `API_PUBLIC_ADDR` (mobile; Appendix B writes this as `public`); `internal` = internal listener only, the public listener answers `404`; mobile reaches the `/v1/me*` functions through `/v1/mobile/me`, `/v1/mobile/me/sessions`, `/v1/mobile/me/devices` (same handlers, R42). Phase = when the endpoint must be live; user write endpoints ship in P0 because PostgreSQL is the users writer from P0 (R49).

| Method | Path | Listener | Auth | Request | Response | Phase | Replaces |
|---|---|---|---|---|---|---|---|
| POST | `/v1/auth/login` | both | public, rate-limited | `{email, password, platform, installId?, appVersion?, geo?}` | `200 {accessToken, expiresIn, refreshToken, tenants:[{id,nameTh,nameEn,kind,role}], defaultTenantId}`; `403 password_change_required` with `passwordChangeTicket` (C.4.8) | P0 | `signInWithEmailAndPassword` + `setAdminClaims` / `setDriverClaims`; `lib/updateUserLastLogin.ts` |
| GET | `/v1/auth/google/nonce` | both | public | — | `{nonce, expiresIn}` | P0 | — |
| POST | `/v1/auth/google` | both | public, rate-limited | `{idToken, nonce?, platform, installId?, appVersion?}` | as login | P0 | `signInWithPopup`; mobile `signInWithCredential` |
| POST | `/v1/auth/exchange` | both | Firebase ID token in body; `404` unless mode `mobile` / `both` | `{idToken, platform, installId, appVersion?}` | as login | P7a | Firebase session of APK 3.x carried into 4.x (R42) |
| POST | `/v1/auth/refresh` | both | refresh token | `{refreshToken}` | `{accessToken, expiresIn, refreshToken}` | P0 | Firebase SDK token refresh |
| POST | `/v1/auth/tenant` | both | bearer | `{tenantId}` | `{accessToken, expiresIn}` (session's active tenant updated) | P0 | — |
| POST | `/v1/auth/logout` | both | bearer | `{refreshToken?, installId?}` | `204`; revokes the current session; deletes that install's device token | P0 | `signOut` |
| POST | `/v1/auth/logout-all` | both | bearer | — | `204`; revokes all own sessions (mobile; the web proxy blocks it, C.4.5) | P0 | — |
| POST | `/v1/auth/password/forgot` | both | public, rate-limited | `{email, locale?}` | **`202`** always | P0 | `sendPasswordResetEmail` |
| POST | `/v1/auth/password/reset` | both | reset token in body | `{token, newPassword}` | `204`; all sessions revoked | P0 | Firebase reset page |
| POST | `/v1/auth/password/change` | both | bearer, or `passwordChangeTicket` (R79) | bearer `{currentPassword, newPassword}` \| ticket `{passwordChangeTicket, newPassword}` (a ticket in the body wins over any bearer) | `204`; bearer: other sessions revoked; ticket: the client then logs in | P0 | — |
| POST | `/v1/auth/sse-ticket` | both | bearer of an `android` / `ios` session | — | `{ticket, expiresIn: 60}` | P7a | — |
| GET | `/v1/mobile/events?ticket=` | both | SSE ticket | — | `text/event-stream` (`session.revoked` + the topics of main spec §8) | P7a | `FirebaseAuth.authStateChanges()` |
| GET | `/v1/events` | internal | bearer (BFF from `lt_at`) | `?topics=` | `text/event-stream` | P0 (topics added per domain, TW5) | `users/{uid}.forceLogoutAt` listener |
| POST | `/v1/bridge/firebase-token` | internal | bearer; mode `web` / `both`; minting rule C.6.3 | — | `{customToken, expiresIn}` | P0-P6 | `setAdminClaims` bootstrap loop |
| GET | `/.well-known/jwks.json` | internal | none | — | JWKS (C.4.2) | P0 | — |
| GET | `/v1/me` | internal (+ `/v1/mobile/me`) | bearer | — | `{id, email, displayName, photoUrl, tenant:{id,nameTh,nameEn,kind,role}\|null, tenants[], platformRoles[], dispatcher, steward, driver:{id}\|null, customerScopes:[{billingPartyId,name}], capabilities[], mustChangePassword, legacyAuthUid (bridge only)}` | P0 | `getIdTokenResult`, `checkAdminStatus` (dead), `permissions_config` reads, mobile `drivers where authId == uid` |
| PATCH | `/v1/me` | internal (+ alias) | bearer | `{displayName?, photoKey?, lastLoginGeo?:{lat,lng,source,accuracyM}}` | `/v1/me` body | P0 | `updateUserLastLogin`, mobile `_touchLastLogin` |
| GET | `/v1/me/tenants` | internal | bearer | — | `[{id, nameTh, nameEn, kind, role, status}]` | P0 | — |
| GET / DELETE | `/v1/me/sessions`, `/v1/me/sessions/{sid}` | internal (+ alias) | bearer | — | list / `204` | P0 | — |
| PUT | `/v1/me/devices` | internal (+ `/v1/mobile/me/devices`) | bearer | `{installId, token, platform, appFlavor?}` | `204` (upsert `device_tokens`, PK `(user_id, install_id)`, R4). As built (T13): a session with `sessions.install_id` registers that install only (`422 install_mismatch`); another user's row gives up the token only on that install (shared phone, `device_token_moved`), any other holder `409 already_exists`; at most 10 rows per user (Appendix B §B.2.3) | P5 (alias P7a) | `users.fcmTokens` (`fcm_service.dart:43-46`), `drivers.fcmToken` |
| DELETE | `/v1/me/devices/{installId}` | internal (+ alias) | bearer | — | `204` | P5 (alias P7a) | none (tokens are never removed today) |
| GET | `/v1/tenants` | internal | `platform:manage_tenants` | `?kind=&status=&cursor=` | page | P1 | subcontractor list (platform view) |
| GET | `/v1/tenants/{id}` | internal | `platform:manage_tenants`, or a member of `{id}` | — | profile incl. `lineGroupId`, `contractorTenantId`, status history | P1 | subcontractor detail |
| POST | `/v1/tenants` | internal | `platform:manage_tenants` (kind `carrier`; the own-fleet row comes from seed / ETL, the quarantine row from migration 0002) | `{kind, code, nameTh, nameEn?, ...profile}` | tenant (+ `billing_parties(kind='tenant')`) | P1 | subcontractor create (platform path) |
| PATCH | `/v1/tenants/{id}` | internal | `platform:manage_tenants` | `code`, `kind`, `status`, `contractorTenantId` | tenant; `tenant_contractor_changed` when the contractor changes | P1 | subcontractor edit (structural fields) |
| GET | `/v1/tenants/{id}/members` | internal | `users:view` with the tenant in reach, or platform via `X-Act-On-Tenant` | `?role=&cursor=` | `[{user, role, status, lastLoginAt}]` | P0 | `getUsers` |
| PUT / DELETE | `/v1/tenants/{id}/members/{userId}` | internal | `users:assign_role` (`CanAssignRole`) | `{role}` | membership / `204`; `auth_version++`; `user_role_changed` | P0 | `updateUserRole` (`functions/src/users.ts:42`) |
| GET | `/v1/tenants/quarantine/rows` | internal | `platform:cross_tenant_read` | `?table=&cursor=` | rows + resolver trace | P1 | — |
| POST | `/v1/tenants/quarantine/rows/{table}/{id}/rehome` | internal | `platform:cross_tenant_write` | `{tenantId}` | `204`; `tenant_rehomed` | P1 | — |
| GET | `/v1/users` | internal | `users:view` (tenant reach) or platform (`X-Act-On-Tenant: *`) | `?q=&role=&status=&sort=last_login_at&cursor=` | keyset page with memberships | P0 | `getUsers` (`users.ts:8`), users page listener |
| POST | `/v1/users` | internal | `users:manage`; role checked by `CanAssignRole`; `tenantId` other than the active one = platform only | `{email, displayName, role, tenantId?, driverId?, billingPartyIds?, sendInvite?}` | `{user, temporaryPassword?}` (once; absent when `sendInvite`) | P0 | `createUser` (`users.ts:177`), `onUserCreated`, account side of `createDriverAccount` |
| GET / PATCH | `/v1/users/{id}` | internal | `users:view` / `users:manage` | `{displayName?, email?}` | user (email change bumps `auth_version`, mirrored) | P0 | — |
| POST | `/v1/users/{id}/disable`, `/v1/users/{id}/enable` | internal | `users:manage`; not self | `{reason}` | `204`; `user_disabled` / `user_enabled` | P0 | `setUserDisabled` (`users.ts:299`) |
| POST | `/v1/users/{id}/invite` | internal | `users:manage` | — | `202` (reset-link email, R29) | P0 | — |
| POST | `/v1/users/{id}/password/temporary` | internal | `users:manage` (+ `drivers:set_password` when the user is a driver) | — | `{temporaryPassword}` once; sessions revoked | P0 | `updateDriverAccount` password path |
| GET / DELETE | `/v1/users/{id}/sessions`, `/v1/users/{id}/sessions/{sid}` | internal | `users:revoke_sessions`; not self | — | list / `204`; `user_sessions_revoked` | P0 | `revokeUserRefreshTokens` (`authSessions.ts:9`) |
| PUT / DELETE | `/v1/users/{id}/scopes/{kind}` | internal | `kind` ∈ `customer` \| `dispatcher` only (R86); `customer`: `users:assign_role`; `dispatcher`: `platform:manage_platform_roles` | `{billingPartyIds: [...]}` (<= 20) | scopes; `auth_version++`; `user_scope_changed` | P0 | `customerScopeId` / `partnerScopeId` edits (`users.ts:82-104`) |
| PUT / DELETE | `/v1/users/{id}/driver-link` | internal | `drivers:edit` + `users:manage` | `{driverId}` | `204`; `driver_linked` / `driver_unlinked` | P0 | `linkDriverToUser` (`users.ts:371-430`), `scripts/fix-driver-claim.js` |
| POST / DELETE | `/v1/users/{id}/platform-roles`, `/v1/users/{id}/platform-roles/{role}` | internal | `platform:manage_platform_roles`; not self | `{role}` | `204`; `platform_role_granted` / `_revoked` | P0 | hardcoded `ADMIN_EMAILS` |
| DELETE | `/v1/users/{id}` | internal | `platform_admin` | — | `204` (soft delete: `status='deleted'`, `deleted_at`; sessions revoked; `user_deleted`) | P6 | `onUserDeleted` (`triggers.ts:35`) |
| GET | `/v1/roles` | internal | bearer | — | `{capabilities:[{key, module, class, titleEn, titleTh}], roles:[{role, axis, capabilities}], stewardCapabilities}` (catalog, the 10 default sets of C.2.4, the 7 global keys; T07) | P0 | `CAPABILITY_META`, `DEFAULT_ROLE_CAPABILITIES` |
| GET | `/v1/roles/matrix` | internal | `security:manage_roles` | `?tenantId=` (platform) | `{roles:[{role, capabilities:{key:{default, effective, overridden}}}]}` | P6 | `permissions_config` reads |
| PUT | `/v1/roles/matrix` | internal | `security:manage_roles` | `{overrides:[{role, capability, allowed}]}` | `204`; `role_matrix_saved` with diff; `INCR rbac:ver` | P6 | matrix batch write + `logSecurityEvent` |
| GET / POST / DELETE | `/v1/api-keys`, `/v1/api-keys/{id}` | internal | `security:manage_api_keys` (tenant keys); `platform_admin` (platform keys, incl. scopes `cf_shim` and `release_publisher`) | `{name, scope, capabilities[], expiresAt?, tenantId?}` (C.4.11) | `{id, keyPrefix, secret}` (secret once) | P2 (first consumer: the shims, T32) | API-keys page (no backend today) |
| GET | `/v1/security-events` | internal | `security:view_audit` | `?type=&from=&to=&cursor=` | keyset page | P6 | audit page query (`canReadSecurityEvents`) |

Related routes owned by Appendix B but constrained here: `/v1/subcontractors*` (Appendix B §B.2.10: steward onboarding with `fleet:manage_subcontractors`, contractor = own fleet; profile fields only, the structural columns stay on `PATCH /v1/tenants/{id}`, trigger `t_tenant_admin_columns`), `PATCH /v1/tasks/{id}` with `tenantId` (R13 reassignment, `app.tenant_move`), `PATCH /v1/drivers/{id}` with `tenantId` (C.1.4), `GET /v1/drivers?fields=minimal` (driver directory, R33), `GET /v1/config/web-flags` (R35, cached as `['webFlags']`), `GET /v1/mobile/settings` (anonymous version gate, R42), `POST /v1/trips/{id}/evidence/revoke` and `POST /v1/standby/{id}/evidence/revoke` (R47), `PUT /v1/app-releases/floor` (human JWT only) and `POST /v1/app-releases/presign`, `POST /v1/app-releases` (release CLI, R43), and the anonymous internal `POST /v1/waitlist`, `POST /v1/partner-interest` (reached through the unauthenticated BFF route handlers `POST /api/forms/waitlist`, `POST /api/forms/partner-interest`, `RATE_LIMIT_PUBLIC_FORMS`, `WithSystem`, R44, R77). `/public/v1/*` stays empty (reserved for signed third-party postbacks).

---

## C.9 Test matrix

All database tests run against `postgres:18-alpine` through testcontainers-go (same image as compose and CI, R34) with `deploy/postgres-init/00-roles.sql` mounted, the full goose chain applied as `logitrack_migrator` and the tests connected as `logitrack_app` (R66); Redis tests use a real Redis container. Issue mapping: T05 (auth core), T06 (Google), T07 (RBAC + RLS), T08 (bridge), T18 (web login / logout), T19 (users ETL), T51 (Security Center APIs), T55 (mobile auth), TW3 (BFF + `proxy.ts`).

### C.9.1 Role × route (table-driven)

- Generated from the Appendix B route table (method, path, listener, required capabilities, scope rule) crossed with these principals: own-fleet TA, carrier TA (sub-tenant), independent carrier TA, MG, OS, OP, US, DR, CU, DS, PA without header, PA with `X-Act-On-Tenant: <uuid>`, PA with `*`, SU with `*`, `integration` key, `release_publisher` key, `cf_shim` key + Firebase token, `cf_shim` key without a token, anonymous.
- Expected outcome per cell from C.2.3 / C.2.4: `2xx`, `401`, `403 permission_denied`, `403 tenant_required`. Global-class keys for a carrier TA -> `403`. Writes with `*` -> `403`. Every non-allowed route group on the public listener -> `404` (never `401`); `X-Act-On-Tenant` on the public listener -> `400`.
- Web: the 53 `ROUTE_CAPABILITIES` entries × role classes against `proxy.ts` — no false denies (every route that some override could open is allowed at the edge); a page without a mapping fails the test.

### C.9.2 RLS with two tenants (and more)

Fixture: own fleet **O**; carriers **A** and **B** with `contractor_tenant_id = O`; independent carrier **C** (`contractor_tenant_id IS NULL`); quarantine **Q**; customers **X**, **Y** (billing parties); dispatcher organisation **D** (tenant + party) whose user holds a dispatcher grant over **X**; driver A1 (tenant A, helper on one task), driver A2 later moved to B.

| # | Principal | Action | Expected |
|---|---|---|---|
| 1 | staff A | read any tenant table rows of B, C, O | 0 rows |
| 2 | staff A | insert a task / trip / expense with `tenant_id = B` | RLS `WITH CHECK` violation |
| 3 | staff O | read rows of A and B | visible (contractor reach); rows of C: 0 |
| 4 | any principal without bypass | read rows of Q | 0 rows; PA with `*` sees them |
| 5 | driver A1 | read `tasks`, `trip_records`, `standby_records`, `incident_reports`, `vehicle_expenses`, `leave_requests`, `payroll_runs`, `chats` | only own rows (tasks also where helper) |
| 6 | driver A1 | read `drivers`; read `trucks`; read `maintenance_records` | own row only; tenant A trucks only; only the active or home-assigned truck's records, and only in tenant A (pointing `active_truck_id` or an assignment at a truck of B opens nothing) |
| 7 | driver A1 | update another driver's task; change `driver_id` on own task; update own `drivers.status` | 0 rows; `WITH CHECK` violation; trigger `insufficient_privilege` |
| 8 | driver A2 after the move to B | read old trips; insert a trip with `tenant_id = A`; read `maintenance_records` of A's truck through an assignment still active in A | old trips visible (driver self-scope); insert rejected; 0 rows (the maintenance gate is bound to the active tenant) |
| 9 | customer X | read tasks / trips / standby / incidents | rows whose billing party, source or destination link, or any delivery-stop link is X; none of Y; incidents only of visible trips; customer with no trips -> empty |
| 10 | customer X, dispatcher D | read `trip_billing_snapshots`, rate tables, `billing_statements`, `payroll_runs`, `driver_penalties`, `driver_compensation_configs`, `vehicle_expenses`, `maintenance_records`, `transactions` | **0 rows** (dispatcher cannot read cost / HR data) |
| 11 | dispatcher D | read through `scope_*` views; inspect view columns and the sqlc DTOs | rows of A, B, C in scope X; no `billing_*`, party ids, `evidence_token`, ID card, licence, birth date, insurance or tax columns present (column-set assertion) |
| 12 | dispatcher D | update a task of A; create a call-out; reassign it to a driver of B | 0 rows; created in D with `tenant_source='form'`; reassignment only through the tenant-move path, `task_tenant_reassigned` written |
| 13 | staff A reading own standby rows priced by O | standby DTO | `billing_*` absent (billing-carrier projection, C.3.4) |
| 14 | carrier TA (A) | insert / update `hubs`, `customers`, `billing_parties`; own-fleet driver the same; `PUT /v1/app-releases/floor` | RLS violation (steward only); driver may insert a hub only with `created_by_driver = true`; floor change `403` (global key, not a steward) |
| 15 | own-fleet MG | write a PUBLIC holiday / a platform-wide broadcast | allowed (steward); carrier TA -> RLS violation |
| 16 | SU with `*` | `GET` across tenants; any write | succeeds; `read-only transaction` error; one `platform_cross_tenant_access` row per request, committed before the data access |
| 17 | PA with `X-Act-On-Tenant: A` without `platform:cross_tenant_write` | write | `403`; audit row still present |
| 18 | bare transaction without `WithPrincipal` / `WithSystem` | read every tenant table | 0 rows (fails closed) |
| 19 | staff A | `UPDATE … SET tenant_id` without `app.tenant_move`; with it | trigger error; succeeds and link triggers re-check |
| 20 | staff B; driver A1 | read `file_objects` of A; customer X requesting a trip photo; A1 moves its own upload to B, to a NULL tenant or to `visibility='public'`, or sets a committed row back to pending | 0 rows; served only through the storage service after the trip row was read under the principal; `insufficient_privilege` (C.3.6), while committing its own pending upload succeeds |
| 21 | catalog test | `pg_class` / `pg_policy` / grants | RLS enabled and forced on exactly the C.3.0 "yes" rows; every family-`tenant` table has `p_tenant_staff`, `p_not_quarantine` and `t_freeze_tenant_id`; exempt tables hold only the C.3.2 grants; no counter is granted to `logitrack_app` |
| 22 | `logitrack_app` without `WithSystem` | `INSERT INTO task_number_counters`; `SELECT` on `billing_counters`; `next_task_seq()` x 50 concurrently | permission denied; permission denied; 50 distinct numbers |

### C.9.3 Tokens, sessions, passwords

- JWT: expired (beyond 30 s leeway) -> `token_expired`; `alg` `none` / `HS256` / `RS256` rejected; unknown `kid` rejected; previous key accepted until `JWT_PREVIOUS_KEY_FILE` is removed; start-up fails when `JWT_ACTIVE_KID` differs from the key thumbprint; JWKS document shape.
- Revocation: an `auth_version` bump on a live session answers the next request with `token_expired` and `details.reason = "claims_changed"` (an expired token: `details.reason = "expired"`), and the refreshed token carries the new claims; a revoked `sid` -> `session_revoked`; a revocation sends FCM data `session_revoked` to the revoked sessions' device tokens and none for a claims-only change; with Redis stopped the same checks pass through PostgreSQL (fail closed); a lost post-commit write (version raise, revoked marker) is retried and healed by the next newer token or cache miss.
- Refresh: rotation; reuse after 30 s -> family and session revoked, `refresh_token_reuse` row, SSE `session.revoked`; reuse inside 30 s with an unused successor -> sibling issued; web absolute cap at 30 d; mobile 90 d; device re-login revokes the older session.
- Passwords: Argon2id parameter upgrade on login; lockout after 5 failures; dummy verification for unknown emails (equal memory-hard work on every failed path, asserted by counting the computations, since a wall-clock tolerance is flaky); concurrent wrong passwords honour the lockout; a login racing a reset loses; refresh racing revocation, password change and tenant switch never deadlocks; a `must_change_password` login returns `403 password_change_required` and no token, the ticket is single use; forgot-password returns `202` for unknown, disabled and existing emails alike; reset token single use and expiring; no password in any email template.
- SSE ticket: single use, 60 s, rejected on `/v1/events`, rejected for `web` sessions.
- Google: wrong `aud`, `email_verified=false`, missing nonce on web -> rejected; unknown account -> `403 no_account`; verified-email linking writes `google_identity_linked`. T06 adds: a used, unknown or mismatched nonce and a nonce-bound token replayed without its nonce -> `401 invalid_token`; disabled -> `403 account_disabled` with no link, no session; `must_change_password` -> ticket, no session; Google unreachable -> `503`; Google off -> `404`; concurrent first sign-ins link once; a driver-app token (`azp` ≠ `aud`) with the SDK's own nonce and no body nonce signs in on `ios` and `android`, while a GIS-shaped token (`azp` = `aud` or none) with a nonce claim, the SDK's nonce sent in the body and an app token on the web are `401`; a verified email on a non-Google domain without `hd` (also a disabled user's) -> `403 no_account`, no link, no event, while the same address with `hd`, a Gmail address and an existing `sub` link sign in; a U+212A look-alike of a Gmail address is never linked (`internal/auth/google_integration_test.go`, `internal/auth/google/google_test.go`).

### C.9.4 Migration

- `firebasescrypt.Verify` golden vectors (C.5.3) and the dev fixture user.
- `auth-import` fixtures, one per claim shape of C.5.5 (admin, admin + bootstrap email, partner with existing / missing tenant, customer with doc id / free text, driver by `driverId` / by `authId`, stale `driverId`, no role, disabled, Google-only, duplicate email) produce exactly the expected rows and report lines.
- Weak-password scan flags matching fixtures; `--refresh-legacy` never overwrites an Argon2id hash; `auth-tail` sets `reset_required` only for users without a usable credential.
- `auth-rbac`: every legacy key of C.2.6 maps as listed; unknown keys reach `etl.quarantine` with `unknown_capability_key`.

### C.9.5 Firebase bridge

- Firebase ID token with a `cf_shim` key accepted on the shim targets in every mode (from P2), `403` on any other route; as `idToken` of `POST /v1/auth/exchange` accepted only with mode `mobile` / `both` (`404` with `web` / `off`); alone as a bearer `401 invalid_token` everywhere, internal listener included; token whose `auth_time` precedes `users.password_changed_at` rejected; disabled user rejected; principal comes from PostgreSQL even when the token carries different custom claims.
- Custom token claims equal the C.6.3 table for each principal; no token for a dispatcher (tested on an imported tenant_admin of a carrier tenant with a `legacy_doc_id`, whose twin without the grant is minted partner claims, and on own-fleet staff), a carrier user created in Go or a customer user created in Go; the minted token signs in against the Firebase Auth emulator and passes the existing `firestore.rules` tests.
- Mirror: unrelated custom attributes preserved; a mirror failure returns `503 bridge_unavailable` and leaves PostgreSQL unchanged; disable sets `validSince`; a soft delete (`RevokeInTx`, reason `disabled`) disables the account; a dispatcher's account loses its legacy keys; a creation that failed after Firebase created the account (inside the call, or at COMMIT) converges on retry by deleting or adopting the orphan, while an email held by anyone else is `409 already_exists`.
- Where (T08): `internal/auth/firebase` unit tests (key file, custom-token claims, verifier refusals and unreachable keys, Identity Toolkit wire format and errors) and `internal/auth/bridge_integration_test.go` (PostgreSQL 18 + Redis 7, both listeners) run against `firebasetest`, an in-process stand-in for the securetoken keys, the OAuth2 token endpoint and Identity Toolkit, so no test reaches Google. The custom token is verified as Firebase would (RS256 with the service-account key, `aud`, `iss`, `iat`, `exp`); signing it in to the dev project and running the legacy pages under `firestore.rules` for the admin, driver and customer fixtures is an owner step with the dev service account (CI runs no Firebase emulator).

### C.9.6 Web end to end (Playwright, TW3 / T18)

- Login sets `lt_at` (`Path=/`) and `lt_rt` (`Path=/api/auth`) as `HttpOnly; Secure; SameSite=Lax`; neither is readable from `document.cookie`.
- Navigating to `/app/*` with an expired `lt_at` passes through `GET /api/auth/refresh?next=` and lands on the page; with an invalid `lt_rt` lands on `/login?next=`; `/api/go/v1/auth/login`, `/api/go/v1/auth/refresh` and `/api/go/v1/bridge/firebase-token` answer `404`.
- A route whose capability the role lacks redirects at `proxy.ts` after the internal `GET /v1/me` check; after a role change the next navigation uses the new capabilities (cache key `(sid, ver, tid)`).
- A page the role cannot use issues no `/api/go` request before redirecting.
- Revoking the user's sessions from the Security Center signs out an open tab within 5 s; a `claims_changed` revocation keeps the tab signed in and refreshes `['me']`.
- Two tabs refreshing at token expiry run one refresh (`navigator.locks` + `lt:lastRefreshAt`, `lib/goFetch.test.ts`) and both continue; with the lock disabled the 30 s reuse grace still keeps both signed in; a role change answered with `401 token_expired` / `claims_changed` triggers one `{"force":true}` refresh although `lt_at` has more than 120 s left.
- `POST /api/forms/waitlist` (no cookie) reaches `POST /v1/waitlist`, rate-limited per visitor IP.
