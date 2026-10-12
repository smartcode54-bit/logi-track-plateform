-- The anonymous landing forms (T53, Appendix A §A.2.7, Appendix B §B.2.19; R44, R77). waitlist and partner_interest are
-- exempt from RLS (Appendix C §C.3.0): the service is their only access path. The anonymous inserts run in a system
-- transaction (no principal exists); the waitlist reads and the delete run under db.WithPrincipal after the route
-- checked waitlist:view (a global key: stewards only, R60).

-- name: InsertWaitlist :one
INSERT INTO waitlist (email, name, country_code, phone)
VALUES (sqlc.arg(email), sqlc.arg(name), sqlc.arg(country_code), sqlc.narg(phone))
RETURNING id, created_at;

-- name: InsertPartnerInterest :one
INSERT INTO partner_interest (payload) VALUES (sqlc.arg(payload))
RETURNING id, created_at;

-- name: ListWaitlist :many
-- GET /v1/waitlist: newest first, keyset (created_at, id) on waitlist_created.
SELECT id, email, name, country_code, phone, created_at
  FROM waitlist
 WHERE sqlc.narg(before_created_at)::timestamptz IS NULL
    OR (created_at, id) < (sqlc.narg(before_created_at)::timestamptz, sqlc.narg(before_id)::uuid)
 ORDER BY created_at DESC, id DESC
 LIMIT sqlc.arg(row_limit);

-- name: CountWaitlist :one
SELECT count(*) FROM waitlist;

-- name: DeleteWaitlist :execrows
DELETE FROM waitlist WHERE id = sqlc.arg(id);
