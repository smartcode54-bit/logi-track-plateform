-- Hubs and SOCs (Appendix B §B.2.9, Appendix A §A.2.2): global master data, read by every authenticated principal
-- and written by stewards (RLS G-global, R60) or, for the hubs a driver creates, by the driver (p_driver_hub_insert,
-- mobile:create_hub). Requests run in db.WithPrincipal; the shared caches of the list and the maps are loaded READ
-- ONLY in db.WithSystem, so every reader caches the same value (tenants' names are not readable by every principal,
-- and the service masks per request what RLS would hide).

-- name: ListHubRows :many
-- GET /v1/hubs (cache:hubs:all) and the payload of one hub (hub_id): each hub with the customer or carrier of its
-- linked billing party (linkedCustomer*, R6).
SELECT h.id, h.source_id, h.name_th, h.name_en, h.latitude, h.longitude, h.station_type, h.network,
       h.linked_party_id, h.created_by_driver, h.updated_at,
       bp.kind AS party_kind, c.id AS party_customer_id, c.code AS customer_code, c.name AS customer_name,
       t.id AS party_tenant_id, t.code AS tenant_code, t.name_th AS tenant_name
FROM hubs h
LEFT JOIN billing_parties bp ON bp.id = h.linked_party_id
LEFT JOIN customers c ON c.id = bp.customer_id
LEFT JOIN tenants t ON t.id = bp.tenant_id
WHERE (sqlc.narg(hub_id)::uuid IS NULL OR h.id = sqlc.narg(hub_id)::uuid)
ORDER BY h.source_id, h.id;

-- name: ListCodeNames :many
-- codeToName of GET /v1/hubs/maps (cache:hubs:c2n): each code to its Thai name (buildHubMaps,
-- fn:tripBillingOnDelivered.ts:124-141). A separate map from nameToCode, never merged (.vibe-rules.md:2379).
SELECT h.source_id::text AS code, btrim(h.name_th) AS name
FROM hubs h
WHERE btrim(h.name_th) <> ''
ORDER BY h.source_id, h.id;

-- name: ListNameAliases :many
-- nameToCode of GET /v1/hubs/maps (cache:hubs:n2c): hub_name_aliases, the name -> code direction only. An alias that
-- is itself a hub code is left out, so a code always resolves to itself (a destination already stored as a code, such
-- as SPK890174, is never translated into another hub or back into a name).
SELECT a.alias::text AS name, h.source_id::text AS code
FROM hub_name_aliases a
JOIN hubs h ON h.id = a.hub_id
WHERE btrim(a.alias::text) <> ''
  AND NOT EXISTS (SELECT 1 FROM hubs o WHERE o.source_id = a.alias)
ORDER BY a.created_at, a.alias;

-- name: LockHub :one
-- PATCH: the row is locked (RLS: FOR UPDATE needs the steward write policy, so a non-steward finds nothing).
SELECT * FROM hubs WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: LockHubsBySourceID :many
-- POST /v1/hubs/import: the existing rows of the imported codes, locked (citext: case-insensitive).
SELECT * FROM hubs WHERE source_id = ANY (sqlc.arg(source_ids)::text[]::citext[]) FOR UPDATE;

-- name: InsertHub :one
INSERT INTO hubs (source_id, name_th, name_en, latitude, longitude, station_type, network, linked_party_id,
                  created_by_driver, created_by)
VALUES (sqlc.arg(source_id)::text::citext, sqlc.arg(name_th), sqlc.narg(name_en), sqlc.narg(latitude)::float8,
        sqlc.narg(longitude)::float8, sqlc.arg(station_type), sqlc.arg(network), sqlc.narg(linked_party_id)::uuid,
        sqlc.arg(created_by_driver), sqlc.narg(created_by)::uuid)
RETURNING id;

-- name: UpdateHub :exec
-- PATCH and import write the merged row (Go merges the request into the locked row; updated_at by trigger).
UPDATE hubs
SET source_id = sqlc.arg(source_id)::text::citext, name_th = sqlc.arg(name_th), name_en = sqlc.narg(name_en),
    latitude = sqlc.narg(latitude)::float8, longitude = sqlc.narg(longitude)::float8,
    station_type = sqlc.arg(station_type), network = sqlc.arg(network),
    linked_party_id = sqlc.narg(linked_party_id)::uuid
WHERE id = sqlc.arg(id);

-- name: FindParty :one
-- The billing party a hub links to (R6), by its id or by the customer or carrier tenant it bills, and the code the
-- network rule reads (customers.code, else tenants.code).
SELECT bp.id, bp.kind, coalesce(c.code::text, t.code) AS code
FROM billing_parties bp
LEFT JOIN customers c ON c.id = bp.customer_id
LEFT JOIN tenants t ON t.id = bp.tenant_id
WHERE (sqlc.narg(party_id)::uuid IS NOT NULL AND bp.id = sqlc.narg(party_id)::uuid)
   OR (sqlc.narg(customer_id)::uuid IS NOT NULL AND bp.customer_id = sqlc.narg(customer_id)::uuid)
   OR (sqlc.narg(tenant_id)::uuid IS NOT NULL AND bp.tenant_id = sqlc.narg(tenant_id)::uuid)
LIMIT 1;

-- name: DeleteStaleNameAliases :many
-- The hub's own name aliases that are no longer its Thai or English name (current: the lower-cased names it keeps).
-- Aliases of the ETL (legacy_hubName) and manual ones stay.
DELETE FROM hub_name_aliases
WHERE hub_id = sqlc.arg(hub_id) AND source IN ('name_th', 'name_en')
  AND NOT (lower(alias::text) = ANY (sqlc.arg(current)::text[]))
RETURNING lower(alias::text)::text AS alias;

-- name: InsertNameAliases :exec
-- name -> code (hub_name_aliases): the first writer of a name keeps it (== `!nameToCode.has(alias)` of buildHubMaps).
INSERT INTO hub_name_aliases (alias, hub_id, source)
SELECT (sqlc.arg(aliases)::text[])[i]::citext, sqlc.arg(hub_id)::uuid, (sqlc.arg(sources)::text[])[i]
FROM generate_subscripts(sqlc.arg(aliases)::text[], 1) AS i
ON CONFLICT (alias) DO NOTHING;

-- name: ReclaimNameAliases :exec
-- A name another hub gave up goes to the oldest hub that still carries it as its Thai or English name (first writer
-- wins again), unless the name is that hub's own code.
INSERT INTO hub_name_aliases (alias, hub_id, source)
SELECT DISTINCT ON (lower(n.name)) n.name::citext, h.id, n.source
FROM hubs h
CROSS JOIN LATERAL (VALUES (btrim(h.name_th), 'name_th'), (btrim(h.name_en), 'name_en')) AS n(name, source)
WHERE n.name IS NOT NULL AND n.name <> ''
  AND lower(n.name) = ANY (sqlc.arg(freed)::text[])
  AND lower(n.name) <> lower(h.source_id::text)
ORDER BY lower(n.name), h.created_at, h.id
ON CONFLICT (alias) DO NOTHING;
