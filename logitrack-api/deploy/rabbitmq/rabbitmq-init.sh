#!/bin/sh
# Imports deploy/rabbitmq-definitions.json (Appendix B §B.5) through the management API.
# Credentials reach curl through a config on stdin (-K -), never through process arguments.
# POST /api/definitions adds what is missing; it does not change the arguments of a queue
# that already exists, so a topology change on an existing volume needs `make reset`
# (the worker asserts the topology at start from T10 and fails loudly on a mismatch).
set -eu
: "${RABBITMQ_DEFAULT_USER:?run make env}" "${RABBITMQ_DEFAULT_PASS:?run make env}"
api=http://rabbitmq:15672/api
auth() { printf 'user = "%s:%s"\n' "$RABBITMQ_DEFAULT_USER" "$RABBITMQ_DEFAULT_PASS"; }
i=0
until auth | curl -fsS -o /dev/null -K - "$api/overview"; do
  i=$((i + 1)); [ "$i" -ge 60 ] && { echo "rabbitmq-init: management API not reachable" >&2; exit 1; }
  sleep 2
done
auth | curl -fsS -o /dev/null -K - -H 'content-type: application/json' -X POST \
  --data-binary @/defs/rabbitmq-definitions.json "$api/definitions"
echo "rabbitmq-init: definitions imported"
