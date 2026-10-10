-- Test fixture with the file names of the Appendix A chain (T03: up-to 9, status, round trip).
-- Not a migration of the product; the real chain is logitrack-api/migrations (T04).
-- +goose Up
CREATE TABLE shape_vehicle_expenses (
  id         uuid PRIMARY KEY DEFAULT uuidv7(),
  driver_id  uuid NOT NULL REFERENCES shape_drivers (id),
  tax_inv_id text
);

-- +goose Down
DROP TABLE shape_vehicle_expenses;
