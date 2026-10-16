-- name: SupportListVisitingUsers :many
SELECT DISTINCT actor_user_id FROM support_visits WHERE ended_at IS NULL ORDER BY actor_user_id;
