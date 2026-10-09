-- Test fixture with the file names of the Appendix A chain (T03: up-to 9, status, round trip).
-- Not a migration of the product; the real chain is logitrack-api/migrations (T04).
-- +goose Up
CREATE TABLE shape_outbox_events (
  id      bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  payload jsonb NOT NULL
);

-- +goose Down
DROP TABLE shape_outbox_events;
