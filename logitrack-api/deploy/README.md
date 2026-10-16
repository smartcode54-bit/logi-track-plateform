# Platform support gate 1 deployment

Deploy the Go D1/D2 changes together with the web header removal and `/v1/platform/*` migration.
Replace every API replica before verifying retired-header refusal; old replicas still honor that header.
`SupportEntryEnabled` stays false until the complete Phase 1 acceptance gate passes.

For non-local `APP_ENV`, configure the same base64 `SUPPORT_CURSOR_KEY` on every replica (at least 32 decoded bytes).
When it is absent, the API derives its cursor signing key from `JWT_SIGNING_KEY_FILE`; all replicas must
then use the same signing-key file. A local-only ephemeral random fallback exists for development. Never
rely on that fallback outside local mode. A key change invalidates outstanding support-search cursors.

Import `rabbitmq-definitions.json` into the broker as part of deployment. Its `lt.events` binding for
`tenant.support_admin_action` must route to `notify.email` before enabling D2 writes, so existing tenant
administrators receive the durable notifications. The notification contains action and record references,
never an API-key secret or a password.

Run `make gen-verify` and `make support-release-check` before release. CI also runs the release check
as a prerequisite of `make gen-check`. Run the integration suite and staging session-admission soak
in an environment permitted to start the required PostgreSQL, Redis and RabbitMQ test containers.
