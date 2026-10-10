-- 0007_comms.sql: Appendix A §A.2.6 plus the Appendix C block of this file (end of the Up section).
-- Follows Appendix A until the P0 deploy applies the baseline to a database that is kept; after that it is
-- never edited and follow-ups take the next free number (as 0001_preamble.sql).
-- Depends on: 0001 (trg_set_updated_at, app_* GUC readers, app_rls_* generators), 0002 (tenants, users, file_objects),
--             0003 (drivers). No GRANT/REVOKE here (0009 is the single grant site, R66).

-- +goose Up

CREATE TABLE device_tokens (                                 -- one FCM token per (user, install); merges drivers.fcmToken + users.fcmTokens
  user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  install_id    text NOT NULL,                               -- body installId; legacy rows 'legacy:' + first 16 hex of sha256(token) (§A.3.1)
  token         text NOT NULL,
  driver_id     uuid REFERENCES drivers(id) ON DELETE SET NULL,   -- set when the user is a linked driver (R4)
  platform      text NOT NULL DEFAULT 'other' CHECK (platform IN ('ios','android','web','other')),
  app_flavor    text CHECK (app_flavor IN ('dev','prod')),
  app_version   text,
  legacy_source text CHECK (legacy_source IN ('drivers.fcmToken','users.fcmTokens')),
  created_at    timestamptz NOT NULL DEFAULT now(),
  last_seen_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, install_id)
);
CREATE UNIQUE INDEX device_tokens_token ON device_tokens (token);
CREATE INDEX device_tokens_driver       ON device_tokens (driver_id) WHERE driver_id IS NOT NULL;

CREATE TABLE chats (                                         -- one open conversation per driver (index in 0010, D5)
  id                      uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id           text,
  tenant_id               uuid NOT NULL REFERENCES tenants(id),
  tenant_source           text NOT NULL DEFAULT 'driver'
                          CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  driver_id               uuid REFERENCES drivers(id),
  legacy_driver_ref       text,                              -- legacy chats.driverId = auth uid
  driver_ref_match        text CHECK (driver_ref_match IN ('doc_id','auth_uid','none')),
  status                  text NOT NULL DEFAULT 'open' CHECK (status IN ('open','in_progress','closed')),
  assigned_admin_user_id  uuid REFERENCES users(id),
  assigned_at             timestamptz,
  closed_at               timestamptz,
  priority                text NOT NULL DEFAULT 'normal' CHECK (priority IN ('normal','urgent')),
  last_message_preview    text,                              -- '[Image]' for image-only messages
  last_message_at         timestamptz,
  last_message_by_user_id uuid REFERENCES users(id),
  last_message_type       text CHECK (last_message_type IN ('normal','broadcast')),
  created_at              timestamptz NOT NULL DEFAULT now(),
  updated_at              timestamptz NOT NULL DEFAULT now(),
  CHECK (driver_id IS NOT NULL OR tenant_source = 'quarantine')
);
CREATE UNIQUE INDEX chats_legacy ON chats (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX chats_my     ON chats (assigned_admin_user_id, last_message_at DESC, id DESC) WHERE status <> 'closed';
CREATE INDEX chats_queue  ON chats (tenant_id, last_message_at DESC, id DESC)
  WHERE assigned_admin_user_id IS NULL AND status <> 'closed';
CREATE INDEX chats_driver ON chats (driver_id, last_message_at DESC);
CREATE INDEX chats_tenant ON chats (tenant_id, last_message_at DESC, id DESC);
CREATE INDEX chats_urgent ON chats (tenant_id) WHERE priority = 'urgent' AND status <> 'closed';
CREATE TRIGGER chats_updated_at BEFORE UPDATE ON chats FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();

CREATE TABLE chat_messages (                                 -- chats/{id}/messages
  id                uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id     text,                                    -- unique per parent chat (subcollection doc id)
  chat_id           uuid NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
  sender_user_id    uuid REFERENCES users(id),
  sender_role       text NOT NULL CHECK (sender_role IN ('admin','driver')),
  text              text,
  image_file_id     uuid REFERENCES file_objects(id),
  message_type      text NOT NULL DEFAULT 'normal' CHECK (message_type IN ('normal','broadcast')),
  client_message_id uuid,                                    -- clientMessageId of POST /v1/chats/{id}/messages and
                                                             -- POST /v1/mobile/chat/messages; offline replay dedupe (R63)
  created_at        timestamptz NOT NULL DEFAULT now(),
  CHECK (text IS NOT NULL OR image_file_id IS NOT NULL)
);
CREATE UNIQUE INDEX chat_messages_legacy    ON chat_messages (chat_id, legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE UNIQUE INDEX chat_messages_client_id ON chat_messages (chat_id, client_message_id)
  WHERE client_message_id IS NOT NULL;                       -- unique per chat (R63)
CREATE INDEX chat_messages_chat_created     ON chat_messages (chat_id, created_at, id);   -- keyset both directions

CREATE TABLE chat_read_state (                               -- lastReadByAdmin{uid: ts} + lastReadByDriver
  chat_id      uuid NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
  user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  last_read_at timestamptz NOT NULL,
  PRIMARY KEY (chat_id, user_id)
);

CREATE TABLE broadcasts (
  id                 uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id      text,
  tenant_id          uuid REFERENCES tenants(id),             -- NULL = platform-wide (R12)
  created_by_user_id uuid REFERENCES users(id),
  created_by_name    text,
  title              text,                                    -- missing on legacy docs
  message_text       text NOT NULL,
  recipient_group    text NOT NULL DEFAULT 'all_driver',
  recipient_count    int  NOT NULL DEFAULT 0 CHECK (recipient_count >= 0),
  sent_at            timestamptz NOT NULL DEFAULT now(),
  voided_at          timestamptz,                             -- DELETE /v1/broadcasts/{id} is a soft delete (R14)
  voided_by          uuid REFERENCES users(id),
  created_at         timestamptz NOT NULL DEFAULT now(),
  CHECK (voided_by IS NULL OR voided_at IS NOT NULL)
);
CREATE UNIQUE INDEX broadcasts_legacy ON broadcasts (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX broadcasts_sent          ON broadcasts (sent_at DESC, id DESC) WHERE voided_at IS NULL;
CREATE INDEX broadcasts_tenant_sent   ON broadcasts (tenant_id, sent_at DESC) WHERE voided_at IS NULL;

CREATE TABLE broadcast_recipients (                          -- persisted from cut-over; legacy never stored recipients
  broadcast_id uuid NOT NULL REFERENCES broadcasts(id) ON DELETE CASCADE,
  user_id      uuid NOT NULL REFERENCES users(id),
  PRIMARY KEY (broadcast_id, user_id)
);
CREATE INDEX broadcast_recipients_user ON broadcast_recipients (user_id);

CREATE TABLE broadcast_reads (                               -- readDriverAuthIds[]; readCount is derived (COUNT)
  broadcast_id uuid NOT NULL REFERENCES broadcasts(id) ON DELETE CASCADE,
  user_id      uuid NOT NULL REFERENCES users(id),
  read_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (broadcast_id, user_id)
);
CREATE INDEX broadcast_reads_user ON broadcast_reads (user_id, read_at DESC);

CREATE TABLE notification_deliveries (                       -- FCM push log; exempt from RLS (Appendix C §C.3.0)
  id              uuid PRIMARY KEY DEFAULT uuidv7(),
  outbox_event_id bigint,                                    -- outbox_events.id that caused the push (no FK: outbox is pruned)
  user_id         uuid REFERENCES users(id) ON DELETE SET NULL,
  install_id      text,                                      -- device_tokens key at send time (no FK: tokens are deleted on UNREGISTERED)
  kind            text NOT NULL CHECK (kind IN ('task_assigned','task_unassigned','task_cancelled','tasks_changed',
                                                'maintenance_scheduled','chat','broadcast','leave_decided',
                                                'session_revoked')),   -- session_revoked: silent push (R50)
  payload         jsonb NOT NULL,                            -- FCM data map as sent (data.type contract unchanged)
  status          text NOT NULL DEFAULT 'queued'
                  CHECK (status IN ('queued','sent','failed','token_invalid','deduplicated')),
  error           text,
  created_at      timestamptz NOT NULL DEFAULT now(),
  sent_at         timestamptz
);
CREATE INDEX notification_deliveries_user    ON notification_deliveries (user_id, created_at DESC);
CREATE INDEX notification_deliveries_created ON notification_deliveries USING brin (created_at);

-- Row level security: Appendix C §C.3.0 marks these seven tables "yes"; notification_deliveries is exempt
-- (no ENABLE, explicit grants in 0009).
ALTER TABLE device_tokens        ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- parent-scoped: users (self)
ALTER TABLE chats                ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- tenant
ALTER TABLE chat_messages        ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- parent-scoped: chats
ALTER TABLE chat_read_state      ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- parent-scoped: chats
ALTER TABLE broadcasts           ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- nullable-tenant
ALTER TABLE broadcast_recipients ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- parent-scoped: broadcasts
ALTER TABLE broadcast_reads      ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- parent-scoped: broadcasts
-- Appended here, in this file: the Appendix C §C.3.5 "0007_comms" block (generator CALLs and the device-token, chat
-- and broadcast policies).

-- notification_deliveries is exempt (no call).
-- +goose StatementBegin
DO $$ BEGIN
  CALL app_rls_platform_table('device_tokens');      CALL app_rls_tenant_table('chats');
  CALL app_rls_platform_table('chat_messages');      CALL app_rls_platform_table('chat_read_state');
  CALL app_rls_platform_table('broadcasts');         CALL app_rls_platform_table('broadcast_recipients');
  CALL app_rls_platform_table('broadcast_reads');
  CALL app_rls_driver_rows('chats', false, true);
END $$;
-- +goose StatementEnd

CREATE POLICY p_self ON device_tokens USING (user_id = app_user_id()) WITH CHECK (user_id = app_user_id());

CREATE POLICY p_driver_insert ON chats FOR INSERT
  WITH CHECK (app_role() = 'driver' AND driver_id = app_driver_id() AND tenant_id = app_tenant_id()
              AND assigned_admin_user_id IS NULL);                                   -- firestore.rules:70-73

CREATE POLICY p_parent_read ON chat_messages FOR SELECT USING (EXISTS (SELECT 1 FROM chats c WHERE c.id = chat_id));
CREATE POLICY p_own_write   ON chat_messages FOR INSERT WITH CHECK (
  sender_user_id = app_user_id() AND EXISTS (SELECT 1 FROM chats c WHERE c.id = chat_id)
  AND ((app_is_staff() AND sender_role = 'admin') OR (app_role() = 'driver' AND sender_role = 'driver')));
CREATE POLICY p_parent_read ON chat_read_state FOR SELECT USING (EXISTS (SELECT 1 FROM chats c WHERE c.id = chat_id));
CREATE POLICY p_own_write   ON chat_read_state
  USING (user_id = app_user_id()) WITH CHECK (user_id = app_user_id() AND EXISTS (SELECT 1 FROM chats c WHERE c.id = chat_id));

-- broadcasts: tenant_id NULL = platform-wide (stewards only, R60); voided rows hidden from drivers
CREATE POLICY p_read ON broadcasts FOR SELECT USING (
     (app_is_staff() AND (tenant_id IS NULL OR app_tenant_in_reach(tenant_id)))
  OR (app_role() = 'driver' AND voided_at IS NULL AND (tenant_id IS NULL OR tenant_id = app_tenant_id()))
  OR (tenant_id IS NULL AND app_is_steward()));
CREATE POLICY p_staff_write ON broadcasts
  USING      ((app_is_staff() AND app_tenant_in_reach(tenant_id)) OR (tenant_id IS NULL AND app_is_steward()))
  WITH CHECK ((app_is_staff() AND app_tenant_in_reach(tenant_id)) OR (tenant_id IS NULL AND app_is_steward()));
CREATE POLICY p_read ON broadcast_recipients FOR SELECT USING (
  (app_is_staff() OR user_id = app_user_id()) AND EXISTS (SELECT 1 FROM broadcasts b WHERE b.id = broadcast_id));
CREATE POLICY p_write ON broadcast_recipients FOR INSERT WITH CHECK (
  app_is_staff() AND EXISTS (SELECT 1 FROM broadcasts b WHERE b.id = broadcast_id));
CREATE POLICY p_read ON broadcast_reads FOR SELECT USING (
  (app_is_staff() OR user_id = app_user_id()) AND EXISTS (SELECT 1 FROM broadcasts b WHERE b.id = broadcast_id));
CREATE POLICY p_write ON broadcast_reads FOR INSERT WITH CHECK (                     -- mark read; PK makes it idempotent
  user_id = app_user_id() AND EXISTS (SELECT 1 FROM broadcasts b WHERE b.id = broadcast_id));

-- +goose Down
DROP TABLE notification_deliveries;
DROP TABLE broadcast_reads;
DROP TABLE broadcast_recipients;
DROP TABLE broadcasts;
DROP TABLE chat_read_state;
DROP TABLE chat_messages;
DROP TABLE chats;
DROP TABLE device_tokens;
