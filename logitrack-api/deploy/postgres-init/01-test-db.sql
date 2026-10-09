-- deploy/postgres-init/01-test-db.sql: database for Go integration tests (developer-spec.md §15),
-- owned by the migrator like the main database so goose and the RLS assertions behave the same.
CREATE DATABASE logitrack_test OWNER logitrack_migrator;
