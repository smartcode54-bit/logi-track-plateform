-- deploy/postgres-init/00-roles.sql (superuser, once per cluster; Appendix C §C.3.2, R66).
-- LOGIN passwords are not set here: locally `make dev-db` sets them from the .env URLs, elsewhere
-- the deploy script sets them from the secret store. They are the values embedded in DATABASE_URL,
-- MIGRATE_DATABASE_URL and ETL_DATABASE_URL. No process connects as the superuser (superusers bypass RLS).
CREATE ROLE logitrack_migrator    LOGIN   NOSUPERUSER NOBYPASSRLS;   -- owns every schema object
CREATE ROLE logitrack_app         LOGIN   NOSUPERUSER NOBYPASSRLS;   -- api, worker, scheduler
CREATE ROLE logitrack_etl         LOGIN   NOSUPERUSER BYPASSRLS;     -- cmd/etl and seed writes (R12, R87)
CREATE ROLE logitrack_readonly    LOGIN   NOSUPERUSER NOBYPASSRLS;   -- reporting and forensics tools
CREATE ROLE logitrack_rls_definer NOLOGIN NOSUPERUSER BYPASSRLS;     -- owns the SECURITY DEFINER helpers only
GRANT logitrack_rls_definer TO logitrack_migrator WITH INHERIT FALSE; -- SET stays true: 0009 may hand functions over
DO $$ BEGIN EXECUTE format('ALTER DATABASE %I OWNER TO logitrack_migrator', current_database()); END $$;
                                                                     -- pg_database_owner then owns schema public
