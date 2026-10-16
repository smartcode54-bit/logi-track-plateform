-- name: SupportReadOpenVisitID :one
SELECT id FROM support_visits WHERE session_id=$1 AND ended_at IS NULL;
