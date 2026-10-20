-- name: SupportStorageBrokerReady :one
SELECT (to_regclass('public.storage_upload_grants') IS NOT NULL)::boolean AS ready;
