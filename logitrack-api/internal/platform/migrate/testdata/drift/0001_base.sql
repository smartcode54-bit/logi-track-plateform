-- irreversible: test fixture; the round trip rolls back only above this version.
-- +goose Up
CREATE TABLE fx_a (
  id integer PRIMARY KEY,
  v  integer
);
CREATE INDEX fx_a_v ON fx_a (v);

-- +goose Down
