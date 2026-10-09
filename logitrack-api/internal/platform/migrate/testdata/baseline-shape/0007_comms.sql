-- Test fixture with the file names of the Appendix A chain (T03: up-to 9, status, round trip).
-- Not a migration of the product; the real chain is logitrack-api/migrations (T04).
-- +goose Up
CREATE TABLE shape_chats (
  id        uuid PRIMARY KEY DEFAULT uuidv7(),
  driver_id uuid NOT NULL REFERENCES shape_drivers (id),
  closed    boolean NOT NULL DEFAULT false
);

-- +goose Down
DROP TABLE shape_chats;
