-- Test fixture: this Down drops more than its Up created (the index of 0001), which the round
-- trip must report as schema drift.
-- +goose Up
ALTER TABLE fx_a ADD COLUMN note text;

-- +goose Down
ALTER TABLE fx_a DROP COLUMN note;
DROP INDEX fx_a_v;
