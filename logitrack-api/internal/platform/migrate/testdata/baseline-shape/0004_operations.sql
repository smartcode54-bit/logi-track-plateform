-- Test fixture with the file names of the Appendix A chain (T03: up-to 9, status, round trip).
-- Not a migration of the product; the real chain is logitrack-api/migrations (T04).
-- +goose Up
CREATE TABLE shape_tasks (
  id        uuid PRIMARY KEY DEFAULT uuidv7(),
  driver_id uuid REFERENCES shape_drivers (id),
  plan_date date NOT NULL,
  plan_day  integer GENERATED ALWAYS AS (plan_date - DATE '2000-01-01') STORED
);

-- +goose Down
DROP TABLE shape_tasks;
