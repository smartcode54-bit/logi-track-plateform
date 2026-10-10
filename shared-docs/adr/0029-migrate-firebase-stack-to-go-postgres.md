# ADR 0029 — Replace the Firebase stack with Go + Fiber v3 + PostgreSQL 18 (strangler per domain, web first, mobile last)

- **Status:** Proposed
- **Deciders:** product owner (acceptance pending) — proposed on branch `mv-go` with Claude
- **Area:** whole platform (web, mobile, every Firebase service, shared-docs). Spec:
  [`developer-spec.md`](../../developer-spec.md) + Appendices A–E.

> Documentation only; the work items are the developer-spec issue plan (§18). The rules superseded
> in Decision 15 stay in force until this ADR is **Accepted**. Branch policy: `main` (live
> production) is never touched; issues merge by PR into `mv-go`; the final `mv-go` → `main` merge
> is a separate owner decision after the whole plan.

## Context

The owner asked to move the whole backend to **Go 1.27 + Fiber v3.5, PostgreSQL 18, RabbitMQ,
Redis, MinIO, own auth and multi-tenant RBAC**, with the web reaching Go only over a private network.
Facts from the code (line numbers as of commit `4f552099`; this change later shifts `.vibe-rules.md`
by up to six lines after `:60`):

1. **There are no Firestore triggers.** Firestore lives in `asia-southeast3`
   (`logitrack-web/firebase.json:4`), which Gen1/Eventarc do not support
   (`functions/src/triggers.ts:5-6`). Server logic is 53 functions (46 onCall, 4 onSchedule,
   1 onRequest, 2 Auth triggers; `functions/src/index.ts:15-39`); side effects are callables the
   client fires best-effort after its write, plus two 15-minute safety-net schedulers.
2. **Money logic runs in two places.** `lib/billingCompute.ts` and
   `functions/src/core/billingCompute.ts` differ only in a header comment (`.vibe-rules.md:2441-2444`).
   The browser joins five-plus collections into billing rows, bumps `billing_counters` before `addDoc`
   (invoice gap on failure, `lib/billingStatement.ts:65-98`), numbers tasks as count+1 (race), and
   writes snapshots (`features/accounting/components/EditBillingDialog.tsx:120-190`).
3. **Authorization is not enforced server-side.** Any signed-in user may read `tasks` and
   `trip_records` (`firestore.rules:195-196`, `:288-289`); any customer reads every `incidentReport`
   (`:93-96`); customer/partner scope is filtered only in the browser. `createDriverAccount` has no
   auth check (`triggers.ts:48`), `updateDriverAccount`'s admin check is commented out (`:142-145`),
   `notifyTaskUpdate` is unauthenticated (`:312`). Three key formats leave `permissions_config`
   effective only for `security_view_audit` (`firestore.rules:32-38`).
4. **Driver identity is split.** `driverId` holds the Auth UID in trips, standby, chats, payroll and
   penalties but the `drivers` doc id in tasks, assignments, leave and installations; payroll queries
   `tasks.driverId == authId` (`functions/src/driverCompensation.ts:199-204`), a real bug.
5. **Multi-tenancy is required and cannot be expressed as rules.** ADR 0026 (Accepted 2026-09-11 in
   the index; text **not on disk** in any git ref, semantics in
   `origin/feat/multi-tenant-carrier-isolation`: `functions/src/core/tenantResolve.ts`,
   `functions/src/tenantLookups.ts:12-21`, glossary additions) opens the platform to TTP and its
   carriers. Rules are not filters, so tenant/dispatcher/customer axes fail on list queries.
6. **Storage and secrets leak.** Docs store download-token URLs, not keys; twelve prefixes are
   `allow read: if true` (`storage.rules:12` … `:89`), ID-card and licence images included;
   `isAppCheckVerified()` returns `true` (`:6-8`); evidence tokens never expire
   (`functions/src/tripEvidence.ts:118-163`). Cartrack credentials sit in git-tracked scripts
   (`functions/scripts/test-cartrack.js:11-12`, `test-cartrack-full.js:1-2`) and APK-bundled env
   files (`logitrack-mobile/pubspec.yaml:95-96`); the APK holds Vision and Maps keys.
7. **The web has no server boundary.** `app/app/layout.tsx:1` is `"use client"` and production is a
   static export (`next.config.ts:10`): every page queries Firestore on mount (112 `getDocs`,
   31 `onSnapshot`, ~10.7k documents per dashboard visit, seven silent truncations; Appendix E).
8. **Installed APKs change slowly.** They call five callables by name (`setDriverClaims`,
   `sendCustomerLineNotification`, `computeTripBillingSnapshot`, `addDeliveryStop`,
   `markBroadcastRead`), read Firestore directly and have no outbox (form drafts only). Breaking
   changes are forbidden (`.vibe-rules.md:365-393`, [ADR 0007](0007-mobile-forced-update-pipeline.md)).
9. **Written decisions point the other way:** the hybrid plan keeping users/chats/settings in
   Firestore and Firebase Auth (`.vibe-rules.md:58-73`,
   `database-migration-plan.md:233-236,274-300`), the 2026-09-11 "do not migrate yet" verdict
   (`sql-migration-business-case.md:145-159`), the MANDATORY "all server logic in Cloud Functions"
   rule (`.vibe-rules.md:340-361`) and the Zod SSOT rule (`:325-336`). The owner's product decision
   (multi-tenant platform + own auth) is the missing trigger.

## Decision

1. **Stack and processes.** A new `logitrack-api/` Go 1.27 module (Fiber v3.5, pgx v5.11, sqlc v1.31,
   goose v3.28), one binary per `cmd/`: `api`, `worker`, `scheduler`, `migrate`, `seed`, `etl`,
   `release`. `billing/compute` and `hr/compute` are pure packages with golden vectors from Vitest.
2. **PostgreSQL 18 is the system of record for every collection, users included** (users,
   credentials and sessions from P0). `postgres:18-alpine`; ids `uuid DEFAULT uuidv7()` except
   `outbox_events.id` (`bigint` identity); generated columns written `STORED` (PG 18 defaults to
   VIRTUAL); money `NUMERIC(14,2)`; `legacy_doc_id` on migrated rows; per-row `legacy_driver_ref` +
   `driver_ref_match`; one `driver_id` FK with `drivers.user_id` UNIQUE. Migrations `0001`–`0009`
   apply in P0; production runs `goose up-to 9` → ETL → owner quarantine sign-off → `goose up`
   (`0010`, the unique constraints legacy duplicates would break). A superuser init script creates
   `logitrack_migrator` (owner), `logitrack_app` (NOBYPASSRLS; `api`, `worker`, `scheduler`,
   `seed --verify`), `logitrack_etl` (BYPASSRLS; `etl`, `seed`), `logitrack_readonly`,
   `logitrack_rls_definer` (NOLOGIN, SECURITY DEFINER helpers); one URL per process, no `SET ROLE`.
3. **Own auth.** Argon2id passwords; Google sign-in by GIS ID token checked against
   `GOOGLE_OIDC_ALLOWED_CLIENT_IDS` (no code flow); Ed25519 access JWT, 15 min, claims
   `sub,sid,ver,tid,rol,plt,dsp,drv,cs,amr,jti`; rotating refresh tokens with family reuse detection
   (web 7 d sliding / 30 d absolute, mobile 90 d, 30 s reuse grace); tables `sessions`,
   `refresh_tokens`, `password_reset_tokens`, `api_keys`, `auth_identities`; JWKS (internal).
   Firebase users keep their UID in `users.legacy_auth_uid`; the first login verifies the
   Firebase-scrypt hash and re-hashes to Argon2id; legacy hashes are deleted **180 days** after the
   P7b gate, then a forced reset. No password is emailed (invite = reset link). Role, scope or
   driver-link changes bump `users.auth_version` (clients refresh, stay signed in); only disable,
   password events, admin revoke and refresh-token reuse end sessions.
   `AUTH_FIREBASE_BRIDGE_MODE=off|mobile|web|both`: `mobile` lets the APK exchange its Firebase
   session (`POST /v1/auth/exchange`, P7a); `web` mints Firestore custom tokens via the BFF (internal
   `POST /v1/bridge/firebase-token`) from the P0 login switch until TW7 (end of P6), only for
   own-fleet staff, platform admins and users imported from a legacy partner/customer claim.
4. **Multi-tenant RBAC, restating the six ADR 0026 tenancy rules** (self-contained here because that
   file is not on disk):
   1. *Tenant = the carrier organisation that runs rows* and is the unit of isolation.
      `tenants.kind` is `own_fleet` (exactly one, partial unique — configuration, not a constant),
      `carrier`, or `quarantine`. Carriers are tenants; there is no `subcontractors` table.
   2. *`tenant_id` is stamped at write time and frozen on the row*: `NOT NULL`, never taken from a
      header or body, never re-derived on read; moving a row is an explicit audited re-home.
   3. *Resolution order* is the port of `tenantResolve.ts` and its tests: tasks ← driver;
      trip_records ← task, driver; standby_records ← task, trip, driver; incident_reports ← trip,
      driver; drivers ← self (carrier) else own fleet; trucks ← ownership; vehicle_expenses ←
      driver, truck; maintenance and vehicle locations ← truck. API-created rows take the caller's
      active tenant (`form`) and must equal any parent's tenant. Provenance in
      `tenant_source` = `task|trip|driver|truck|self|form|quarantine`.
   4. *A row whose chain does not resolve is readable by nobody*: it is loaded, never dropped, under
      the single [[Quarantine tenant]] for a platform admin to re-home.
   5. *`platform_admin` is split from tenant admin*: platform roles live in `user_platform_roles`,
      bootstrapped from `PLATFORM_ADMIN_EMAILS`, later granted only by a platform admin (audited);
      cross-tenant work goes through `X-Act-On-Tenant`, and one append-only `security_events` row
      (`platform_cross_tenant_access`) per request is committed before the read.
   6. *Dispatcher and customer scope are axes orthogonal to the tenant role* and never collapsed
      into it: a dispatcher sees an operational projection (`scope_*` views; no cost, HR, rate or
      billing data) of rows linked to its billing parties across tenants; a customer sees only rows
      billed to or linked to its `billing_parties` row. TTP is both a billing party and a carrier
      tenant.

   Tables: `memberships` (tenant role CHECK; legacy `partner` → carrier `tenant_admin`, `admin` →
   own-fleet `tenant_admin`), `user_platform_roles`, `role_capability_overrides`, `user_scopes` →
   `billing_parties.id` (the one polymorphic FK target). Rate tables, `trip_billing_snapshots`,
   `billing_counters` and `billing_statements` carry the rate-card owner's tenant, not the one that
   ran the trip. The catalog, **81 keys (77 + 4 platform)** in colon format, lives in Go code.
   Enforcement: RLS (`FORCE ROW LEVEL SECURITY`, per-transaction GUCs) plus explicit predicates in
   hot queries; service-owned tables (outbox, inbox, jobs, …) are RLS-exempt (Appendix C §C.3.0);
   non-request work uses `db.WithSystem`. **Owner confirms (developer-spec §19 Q13):**
   *contractor reach* (`tenants.contractor_tenant_id`, GUC `app.subtenant_ids`,
   `app_tenant_in_reach()`: own-fleet staff see rows of carriers working for the own fleet, never the
   reverse) and the *steward rule* (global master-data keys and writes to public holidays or
   platform-wide broadcasts count only for own-fleet staff or a platform admin, GUC `app.steward`).
5. **Async = transactional outbox + RabbitMQ, with separate `api`, `worker`, `scheduler`.** `api`
   inserts `outbox_events` in the domain transaction and never publishes; the single-active relay in
   `scheduler` publishes with confirms to RabbitMQ and Redis realtime; consumers dedupe through
   `consumer_inbox`, retry 5 times, then dead-letter. Long admin work is a `jobs` row. Cron
   (Cartrack 3 min, Bangchak 05:00 Asia/Bangkok, billing sweep 15 min) runs in `scheduler`; queues
   per Appendix B §B.5.
6. **Redis is a cache and coordination layer, never a money source.** Prefix `lt:{APP_ENV}:`;
   namespaces `cache:` (hub maps `cache:hubs:n2c` and `cache:hubs:c2n`, never merged; rate-card UI
   hints; settings; mirror acks), `auth:`, `rbac:`, `idem:` (24 h hot copy; durable
   `idempotency_keys` rows live for `IDEMPOTENCY_TTL`, default 168 h), `rl:`, `rt:`, `rtlog:`,
   `lock:`. Pricing reads hub maps, rate tables and period locks from PostgreSQL in its transaction.
7. **Object storage = MinIO locally, any S3-compatible service later.** Two buckets (private,
   public); one registry `file_objects` (status `pending|committed|missing_at_source`, `purpose`,
   `tenant_id`); presigned PUT then commit, signed for the client-facing origin
   (`S3_PRESIGN_ENDPOINT`, Caddy site `MEDIA_DOMAIN`). ETL keeps **original keys** and rewrites token
   URLs to keys. Only APKs are public (`app_releases/`); trip/incident photos
   presigned (recommended; owner confirms, developer-spec §19 Q5). Evidence gallery = Go
   `/evidence/{token}` with short-TTL presigned GETs; tokens non-expiring
   (`EVIDENCE_TOKEN_TTL_DAYS=0`) but revocable (`evidence_token_revoked_at`;
   `POST /v1/trips/{id}/evidence/revoke` and the standby twin; the next forced LINE send mints a new
   token).
8. **Realtime = SSE from Go**, fed by the outbox relay through Redis pub/sub. Web: same-origin
   `EventSource` through the BFF to `GET /v1/events` (internal) with the cookie. Mobile:
   `GET /v1/mobile/events?ticket=` (public) with a single-use ticket from `POST /v1/auth/sse-ticket`.
   `session.revoked` replaces the `forceLogoutAt` listener. **FCM stays** the push channel only
   (HTTP v1 from `worker`, unified `device_tokens`); after P8 Firebase exists solely for FCM.
9. **Web tier (W1–W10): the browser never calls Go.** Next.js `output: 'standalone'` behind Caddy
   (static export removed in P0). The thin [[BFF (web backend-for-frontend)|BFF]] proxy
   `app/api/go/[...path]/route.ts` forwards to `GO_API_INTERNAL_URL`, never refreshes, and 404s Go's
   `v1/auth` session paths and `v1/bridge/*`. Only the BFF `/api/auth/*` routes (login, Google,
   refresh, logout, tenant, Firebase token; Appendix B §B.1.3) set the HttpOnly Secure SameSite=Lax
   cookies `lt_at` (Path `/`) and `lt_rt` (Path `/api/auth`); Go returns tokens in JSON. On a 401
   `token_expired` the browser `goFetch` runs one shared refresh and retries once. `proxy.ts` gates
   `/app/*` with Go's JWKS and `ROUTE_CAPABILITIES` checked against `GET /v1/me` (cached per
   `(sid, ver)`, 60 s). TanStack Query v5 replaces mount-time Firestore reads; aggregates move to
   Go; PDF/XLSX/billing-ZIP rendering moves with billing in **P3** (photo ZIPs stay a lazy browser
   chunk). Domain flags are runtime-only (`GET /v1/config/web-flags`, Go env `WEB_FLAG_OVERRIDES`);
   no build-time API URL or domain-flag variable exists; the only new public variable is
   `NEXT_PUBLIC_GOOGLE_OIDC_CLIENT_ID`.
10. **Ingress: two Go listeners.** `API_INTERNAL_ADDR` (private network) serves every route;
    `API_PUBLIC_ADDR` (behind Caddy) serves only `/v1/mobile/*`, `/v1/auth/*`, `/public/v1/*`
    (reserved for signed third-party postbacks; none today), `/evidence/*`, `/healthz`, else 404
    (and `/media/*` for the local storage backend since the owner addition of 2026-10-10, see Notes).
    Release admin (`/v1/app-releases*`, `/v1/app-installations*`; `cmd/release` runs on the
    private network) and the anonymous forms (BFF `/api/forms/*` → `POST /v1/waitlist`,
    `POST /v1/partner-interest`, rate-limited) stay internal.
11. **Strangler per domain, web first, mobile last** (P0 foundations, P1 master data, P2 operations,
    P3 billing, P4 HR/payroll, P5 comms, P6 security/platform/dashboard, P7a mobile app, P7b storage
    flip, P8 decommission; M0–M8). Each collection has an [[Ownership class]]: A (web-only) flips to
    a PG writer at its phase; B (mobile reads) flips with a PG→Firestore [[Compat projection]] until
    P7b; C (mobile writes) keeps Firestore as the single writer until P7b while Go reads a one-way
    Firestore→PG mirror and performs admin [[Write-back]]. ETL loads every collection at the start
    of P1 and mirrors every collection not yet PG-owned. The five mobile callables become
    [[Callable shim]]s calling the matching `/v1/mobile/*` route on the public listener with
    `X-Api-Key` (an `api_keys` secret, scope `cf_shim`) plus the caller's Firebase ID token as
    `Authorization: Bearer`, verified from P2 whatever the bridge mode (`driver:self` principal).
    `PG_OWNED_DOMAINS` selects the writer per domain; every phase has a reconciliation gate and a
    flag rollback.
12. **API versioning.** `/v1` is additive-only; breaking = new path or `/v2`; retired routes send
    `Deprecation`/`Sunset` and go only after zero traffic for two app release cycles
    (`.vibe-rules.md:365-393`). The mobile version floor is enforced server-side (`X-App-Version` →
    `426 version_blocked`, [ADR 0007](0007-mobile-forced-update-pipeline.md)).
13. **Single source of truth = OpenAPI 3.1 (`logitrack-api/api/openapi.yaml`) + the goose/sqlc
    schema.** Zod schemas (`shared-docs/schemas/`) and Dart models are generated or checked from it.
14. **Business invariants carry over unchanged:** server-side price only; one frozen definition;
    void-only announcement rows; sent/paid period lock; separate `nameToCode`/`codeToName`; three
    vehicle concepts; Bangkok calendar; per-customer billing axis; JS `Math.round` ties and FMA
    blocking; en+th i18n. **Closed here:** the trip-level `4WJ` default is dropped (a task without a
    vehicle class is unpriced, `no_vehicle_class`; rate-card and legacy priced rows untouched);
    deploy = one VM + docker compose for P0–P6, promoted when load requires (developer-spec §19 Q8
    keeps only domain names, DNS/ACME ownership and the Hosting redirect); cash advance (ADR 0014)
    ships its table, UI deferred; mobile attestation off (flag); statuses lower_snake in the DB
    (domain codes such as PM/CM, R1/R2 stay upper-case), translated to legacy literals by the
    Firestore projection and write-back encoder.
15. **Supersede list — effective on acceptance, not before:**
    - `shared-docs/database-migration-plan.md`: hybrid target (§"Architecture เป้าหมาย (Hybrid)"),
      Phase 1/Phase 2 scopes, the `users` note (`:233-236`), §"คงไว้ใน Firestore" (`:274-284`),
      §"Integration Pattern" (`:288-300`), §"Trigger สำหรับเริ่ม Phase", Decision Log rows
      (`:377-384`). Platform Management and Observability stay valid input.
    - `shared-docs/sql-migration-business-case.md:145-159` — the 2026-09-11 "ยังไม่ย้ายข้อมูลเดิม"
      verdict and triggers T1–T4.
    - `.vibe-rules.md:58-73` (Database Architecture Plan), `:340-361` (server logic in Cloud
      Functions; repeated at `:937-941`, `:2419`), `:325-336` (Zod SSOT), and the `4WJ` default at
      `:2460` (aligned with the MANDATORY "never guess a class", `:2343`).
    - ADR 0024 (Buzzebee on Supabase): status **unchanged**; whether Buzzebee is in scope is
      developer-spec §19 Q3.

## Consequences

**Positive**

- Constraints make historical failures unrepresentable (`NOT NULL` tenant/customer links, driver
  FKs, unique trip numbers, gap-free invoice numbers).
- One billing engine with golden vectors; tenant, customer and dispatcher isolation become
  auditable database policy; real retries and dead-letter queues replace best-effort callables.
- The web reads bounded, server-joined pages; keys leave the APK; PII stops being public.

**Negative / risks**

- Firestore and PostgreSQL coexist from P0 to P7b; the P2 write-back layer (~15 operations) is
  throwaway.
- [[Mirror lag]] can make admin writes look lost (mitigation: wait for the mirror ack, else `202`).
- Go must emulate JS float rounding; trips without a vehicle class become unpriced.
- Lazy password migration leaves a forced-reset tail; wrong scrypt parameters would lock users out.
- Mobile must ship APK 4.0.0 and force it before P7b; FCM keeps a Firebase dependency.
- One VM concentrates operations (backups, broker, cache, storage, TLS).

**Follow-ups**

- Rotate the Cartrack credentials and remove them from mobile env files and scripts.
- Row-level `billed_on_invoice_number`; WebSocket; attestation `log`/`enforce`; PG 18 temporal
  features; leaving the single VM — each a separate decision.
- On acceptance: set this status, mark the superseded sections "Superseded by 0029", update
  `.vibe-rules.md` Confirmed Patterns and the glossary (issue T60).

## Alternatives considered

- **Keep the hybrid Firestore + SQL plan.** Rejected: two auth models, permissions left in
  Firestore, no enforceable tenant isolation.
- **Supabase for everything.** Rejected: client-reachable RLS repeats the rules-are-not-filters
  exposure and ties auth, storage and realtime to one vendor.
- **Keep Firebase Auth, move only the data.** Rejected by the owner: tenant claims stay in unaudited
  callables and every request depends on two identity systems.
- **Big-bang cut-over.** Rejected: installed APKs read Firestore and call callables by name;
  billing parity needs month-closes on both systems.
- **Browser calling Go directly with CORS.** Rejected (local-only requirement): it exposes the whole
  API and keeps tokens in browser storage.

## Notes

- **2026-10-10, owner addition to issue T11 (local disk storage):** the first deployment (VM
  `showkhun.co`, pm2 + nginx) has no S3-compatible service yet, so Decision 7 gains a second
  backend behind the same storage interface: `STORAGE_BACKEND=local|s3` picks the backend of new
  uploads, `file_objects.storage_backend` (migration 0011) records it per object and every read
  dispatches on it, so local objects keep downloading after the switch to `s3`. Local objects live
  under `LOCAL_MEDIA_DIR` and are served by the api with HMAC-signed, expiring URLs
  (`LOCAL_MEDIA_PUBLIC_BASE_URL`, `LOCAL_MEDIA_SIGNING_KEY`); `app_releases/` stays unsigned.
  This **widens Decision 10**: the route group `/media` joins the public listener
  (`PUBLIC_ROUTE_GROUPS`, `ingress.PublicPrefixes`; nginx maps `logi.showkhun.co/media/` to it),
  approved by the owner. Spec: `developer-spec.md` §2.6, §9.11, §16.1; Appendix A §A.2.9.

## Related

- Spec: [`developer-spec.md`](../../developer-spec.md); appendices
  [A](../specs/mv-go/A-data-model.md) data model, [B](../specs/mv-go/B-api-catalog.md) API catalog,
  [C](../specs/mv-go/C-auth-rbac.md) auth/RBAC, [D](../specs/mv-go/D-seed-and-mock-data.md) seed,
  [E](../specs/mv-go/E-web-fetch-audit.md) web fetch audit.
- ADRs: [0007](0007-mobile-forced-update-pipeline.md) (version floor),
  [0008](0008-standby-billing-visibility-and-recompute-semantics.md),
  [0009](0009-multiple-rate-rounds-within-one-billing-period.md),
  [0010](0010-job-category-carried-on-trip-independent-of-billing.md) (billing),
  [0012](0012-helper-day-window.md), [0013](0013-payroll-lineitem-breakdown.md),
  [0014](0014-cash-advance.md) (payroll). Index-only ([README](README.md)): 0021 (SMTP),
  0024 (Buzzebee), 0025 (LINE), 0026 (tenancy), 0027, 0028 (plan date).
- Glossary ([../glossary.md](../glossary.md)): [[Ownership class]], [[Compat projection]],
  [[Write-back]], [[Callable shim]], [[Mirror lag]], [[ETL quarantine]], [[Quarantine tenant]],
  [[BFF (web backend-for-frontend)]], [[Internal vs public listener]]; existing [[Frozen price]],
  [[Announcement row]], [[Billing date]], [[Truck identity]], [[Evidence gallery]].
- Conventions: [0000-adr-conventions.md](0000-adr-conventions.md).
