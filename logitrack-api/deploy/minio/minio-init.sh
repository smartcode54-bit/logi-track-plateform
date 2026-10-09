#!/bin/sh
# MinIO bootstrap for the local stack (developer-spec.md §9, §15.1; issue T02). Idempotent.
# Credentials reach mc through MC_HOST_local and stdin, never through process arguments.
set -eu
for v in MINIO_ROOT_USER MINIO_ROOT_PASSWORD S3_BUCKET S3_PUBLIC_BUCKET S3_ACCESS_KEY_ID S3_SECRET_ACCESS_KEY; do
  eval "val=\${$v:-}"
  if [ -z "$val" ]; then echo "minio-init: $v is not set (run make env)" >&2; exit 2; fi
done
export MC_HOST_local="http://${MINIO_ROOT_USER}:${MINIO_ROOT_PASSWORD}@minio:9000"

i=0
until mc ls local >/dev/null 2>&1; do
  i=$((i + 1)); [ "$i" -ge 60 ] && { echo "minio-init: MinIO not reachable" >&2; exit 1; }
  sleep 1
done

mc mb --ignore-existing "local/$S3_BUCKET" "local/$S3_PUBLIC_BUCKET"

# Private bucket: no anonymous access. Public bucket: anonymous GetObject under app_releases/
# only — no listing, no sibling prefixes such as app_releases-x/ (R23, ADR 0007).
mc anonymous set none "local/$S3_BUCKET"
cat > /tmp/public.json <<JSON
{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":["*"]},
 "Action":["s3:GetObject"],"Resource":["arn:aws:s3:::$S3_PUBLIC_BUCKET/app_releases/*"]}]}
JSON
mc anonymous set-json /tmp/public.json "local/$S3_PUBLIC_BUCKET"

# 30-day expiry on cache/ (static-map cache) in the private bucket; import replaces the whole config.
cat > /tmp/ilm.json <<JSON
{"Rules":[{"ID":"expire-cache","Status":"Enabled","Filter":{"Prefix":"cache/"},"Expiration":{"Days":30}}]}
JSON
mc ilm import "local/$S3_BUCKET" < /tmp/ilm.json

# Application user: read/write on the two buckets only (one S3 key for Go, §16.5).
cat > /tmp/policy.json <<JSON
{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:*"],"Resource":[
 "arn:aws:s3:::$S3_BUCKET","arn:aws:s3:::$S3_BUCKET/*",
 "arn:aws:s3:::$S3_PUBLIC_BUCKET","arn:aws:s3:::$S3_PUBLIC_BUCKET/*"]}]}
JSON
mc admin policy create local logitrack-rw /tmp/policy.json
printf '%s\n%s\n' "$S3_ACCESS_KEY_ID" "$S3_SECRET_ACCESS_KEY" | mc admin user add local >/dev/null
mc admin policy attach local logitrack-rw --user "$S3_ACCESS_KEY_ID" 2>/dev/null || true

echo "minio-init: buckets $S3_BUCKET (private) and $S3_PUBLIC_BUCKET (app_releases/ public) ready"
