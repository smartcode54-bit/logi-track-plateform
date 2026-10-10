-- Evidence tokens (R30, R47): trip_records and standby_records, read and written under db.WithSystem (the
-- gallery has no principal; revocation is authorised in Go before it runs).

-- name: TripByEvidenceToken :one
SELECT id, tenant_id, evidence_token_revoked_at, created_at FROM trip_records WHERE evidence_token = @token::text;

-- name: StandbyByEvidenceToken :one
SELECT id, tenant_id, evidence_token_revoked_at, created_at FROM standby_records WHERE evidence_token = @token::text;

-- name: RevokeTripEvidence :execrows
UPDATE trip_records SET evidence_token_revoked_at = @revoked_at::timestamptz
 WHERE id = @id AND evidence_token IS NOT NULL AND evidence_token_revoked_at IS NULL;

-- name: RevokeStandbyEvidence :execrows
UPDATE standby_records SET evidence_token_revoked_at = @revoked_at::timestamptz
 WHERE id = @id AND evidence_token IS NOT NULL AND evidence_token_revoked_at IS NULL;

-- name: SetTripEvidenceToken :execrows
-- A minted token replaces a revoked one and clears the revocation (the next forced LINE send).
UPDATE trip_records SET evidence_token = @token::text, evidence_token_revoked_at = NULL WHERE id = @id;

-- name: SetStandbyEvidenceToken :execrows
UPDATE standby_records SET evidence_token = @token::text, evidence_token_revoked_at = NULL WHERE id = @id;
