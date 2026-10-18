-- name: SupportReadLabelVersion :one
SELECT support_label_version FROM tenants WHERE id=$1;
