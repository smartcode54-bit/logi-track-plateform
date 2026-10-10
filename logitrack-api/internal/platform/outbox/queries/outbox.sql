-- name: InsertEvent :one
-- outbox.Append: the only way to emit (main spec §7.1); the AFTER INSERT trigger NOTIFYs outbox_new.
INSERT INTO outbox_events (exchange, routing_key, aggregate_type, aggregate_id, event_type, tenant_id,
                           payload, headers, realtime_topics)
VALUES (@exchange, @routing_key, @aggregate_type, @aggregate_id, @event_type, @tenant_id,
        @payload, @headers, @realtime_topics)
RETURNING id, event_id;

-- name: LockBatch :many
-- The relay batch: oldest unpublished rows first, skipping rows another transaction holds.
SELECT id, event_id, exchange, routing_key, aggregate_type, aggregate_id, event_type, tenant_id,
       payload, headers, realtime_topics, created_at
  FROM outbox_events
 WHERE published_at IS NULL
 ORDER BY id
 LIMIT @batch_size::int
   FOR UPDATE SKIP LOCKED;

-- name: MarkPublished :exec
UPDATE outbox_events SET published_at = now() WHERE id = ANY(@ids::bigint[]);

-- name: RecordFailure :exec
UPDATE outbox_events SET attempts = attempts + 1, last_error = @last_error WHERE id = @id;

-- name: Backlog :one
-- outbox_pending and outbox_lag_seconds (partial index outbox_events_unpublished).
SELECT count(*)::bigint AS pending,
       coalesce(extract(epoch FROM now() - min(created_at)), 0)::float8 AS lag_seconds
  FROM outbox_events
 WHERE published_at IS NULL;

-- name: PruneEvents :execrows
-- outbox.prune (Appendix B §B.5.6): published rows older than the cutoff, one batch per call.
DELETE FROM outbox_events
 WHERE id IN (SELECT o.id FROM outbox_events AS o
               WHERE o.published_at IS NOT NULL AND o.published_at < @cutoff::timestamptz
               ORDER BY o.published_at LIMIT @batch_size::int);

-- name: ClaimInbox :execrows
-- consumer_inbox: the first statement of a consumer's side-effect transaction; 0 rows = duplicate.
INSERT INTO consumer_inbox (consumer, message_id) VALUES (@consumer, @message_id)
ON CONFLICT DO NOTHING;

-- name: PruneInbox :execrows
-- inbox.prune (Appendix B §B.5.6): rows processed before the cutoff, one batch per call.
DELETE FROM consumer_inbox
 WHERE (consumer, message_id) IN (SELECT i.consumer, i.message_id FROM consumer_inbox AS i
                                   WHERE i.processed_at < @cutoff::timestamptz
                                   ORDER BY i.processed_at LIMIT @batch_size::int);
