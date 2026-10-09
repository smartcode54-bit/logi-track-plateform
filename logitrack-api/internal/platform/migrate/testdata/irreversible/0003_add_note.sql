-- +goose Up
ALTER TABLE fx_trips ADD COLUMN note text;

-- +goose Down
ALTER TABLE fx_trips DROP COLUMN note;
