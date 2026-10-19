-- ADR 0030 P1.4: human web uploads are session/context-bound, never bearer PUTs.
-- +goose Up
CREATE TABLE storage_upload_grants (
 file_id uuid PRIMARY KEY REFERENCES file_objects(id) ON DELETE CASCADE,
 user_id uuid NOT NULL REFERENCES users(id),
 session_id uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
 tenant_id uuid REFERENCES tenants(id),
 auth_version integer NOT NULL,
 context_version bigint NOT NULL CHECK (context_version >= 0),
 expires_at timestamptz NOT NULL
);
CREATE INDEX storage_upload_grants_user ON storage_upload_grants(user_id);
CREATE INDEX storage_upload_grants_session ON storage_upload_grants(session_id);
CREATE INDEX storage_upload_grants_tenant ON storage_upload_grants(tenant_id);
ALTER TABLE storage_upload_grants ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY p_system ON storage_upload_grants USING (app_role()='system' AND app_bypass()) WITH CHECK (app_role()='system' AND app_bypass());
GRANT SELECT, INSERT, UPDATE ON storage_upload_grants TO logitrack_app;
GRANT SELECT ON storage_upload_grants TO logitrack_etl, logitrack_readonly;
-- +goose Down
DROP TABLE storage_upload_grants;
