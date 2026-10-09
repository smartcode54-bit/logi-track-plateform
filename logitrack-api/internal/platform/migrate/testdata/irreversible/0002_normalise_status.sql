-- irreversible: the original spellings are not kept, so Down cannot restore them.
-- +goose Up
UPDATE fx_trips SET status = lower(status);

-- +goose Down
