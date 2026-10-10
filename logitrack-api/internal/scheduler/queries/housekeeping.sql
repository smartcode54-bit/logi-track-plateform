-- name: DeleteExpiredRefreshTokens :execrows
-- auth.token-cleanup (main spec §7.4, R2): refresh tokens expired more than a day ago. A rotated token
-- points at its successor (replaced_by); a token stays while the one it replaced is kept, so the self
-- reference never blocks the delete.
DELETE FROM refresh_tokens AS r
 WHERE r.expires_at < @cutoff::timestamptz
   AND NOT EXISTS (SELECT 1 FROM refresh_tokens AS p WHERE p.replaced_by = r.id AND p.expires_at >= @cutoff::timestamptz);

-- name: DeleteSpentPasswordResetTokens :execrows
-- auth.token-cleanup: used or expired reset and invite links.
DELETE FROM password_reset_tokens WHERE used_at IS NOT NULL OR expires_at < @now::timestamptz;

-- name: DeleteEndedSessions :execrows
-- auth.token-cleanup: sessions revoked, or past absolute_expires_at, more than 30 days ago (their
-- refresh tokens go with them, ON DELETE CASCADE).
DELETE FROM sessions WHERE revoked_at < @cutoff::timestamptz OR absolute_expires_at < @cutoff::timestamptz;
