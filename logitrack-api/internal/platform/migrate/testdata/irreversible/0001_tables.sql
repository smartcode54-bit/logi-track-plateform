-- Test fixture: a data migration marked irreversible sets the round-trip floor (R31).
-- +goose Up
CREATE TABLE fx_trips (
  id     uuid PRIMARY KEY DEFAULT uuidv7(),
  status text NOT NULL
);

-- +goose Down
DROP TABLE fx_trips;
