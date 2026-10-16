-- Follow-up to 0027: index the consent actor FK for parent-key checks (Appendix A §A.2.19).
-- +goose NO TRANSACTION

-- +goose Up
CREATE INDEX CONCURRENTLY IF NOT EXISTS tenants_support_access_changed_by
  ON tenants (support_access_changed_by) WHERE support_access_changed_by IS NOT NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS tenants_support_access_changed_by;
