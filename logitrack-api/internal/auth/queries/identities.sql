-- External identities (auth_identities, Appendix A §A.2.1; Appendix C §C.4.10). Google sign-in resolves the
-- Google `sub` first, then links the user whose email the token verified (no self-signup). Every statement
-- runs inside db.WithSystem (R12).

-- name: GetGoogleIdentityUser :one
SELECT user_id FROM auth_identities
WHERE provider = 'google' AND provider_subject = sqlc.arg(subject)::text;

-- name: GetUserGoogleSubject :one
-- The Google account already linked to a user (UNIQUE (user_id, provider): at most one).
SELECT provider_subject FROM auth_identities
WHERE user_id = sqlc.arg(user_id) AND provider = 'google';

-- name: InsertGoogleIdentity :exec
INSERT INTO auth_identities (user_id, provider, provider_subject, email_at_link, linked_at, last_used_at)
VALUES (sqlc.arg(user_id), 'google', sqlc.arg(subject)::text, sqlc.arg(email_at_link)::text, sqlc.arg(at)::timestamptz,
        sqlc.arg(at)::timestamptz);

-- name: TouchGoogleIdentity :execrows
-- Written by the session transaction of a Google sign-in; 0 rows means the link vanished since it was resolved.
UPDATE auth_identities SET last_used_at = sqlc.arg(at)::timestamptz
WHERE provider = 'google' AND provider_subject = sqlc.arg(subject)::text AND user_id = sqlc.arg(user_id);
