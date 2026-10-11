#!/usr/bin/env bash
# Self-test of go-ci's scripts (developer-spec.md §17.2; first step of go-ci's changes job):
#   - go-ci-changes.sh on throwaway repositories: which ranges run the Go jobs (go) and which pull
#     requests change ci.yml (ci_yml);
#   - go-ci-web-ci.sh against a stub gh: it passes on a successful CI run or on none, fails on a
#     failed run, and fails a pull request that changes ci.yml when no CI run started for it.
#
# Needs git and jq; everything lives in a temporary directory that is removed on exit.
set -euo pipefail
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
work=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/go-ci-selftest.XXXXXX")
work=$(cd "$work" && pwd -P)
trap 'rm -rf "$work"' EXIT

failed=0
check() { # check NAME WANT GOT
  if [ "$2" = "$3" ]; then
    echo "ok   $1"
  else
    echo "FAIL $1: want '$2', got '$3'"
    failed=1
  fi
}

# --- go-ci-changes.sh ---------------------------------------------------------------------------

repo=$work/repo
git init -q -b mv-go "$repo"
git -C "$repo" config user.email selftest@example.invalid
git -C "$repo" config user.name go-ci-selftest
git -C "$repo" config commit.gpgsign false
git -C "$repo" config core.hooksPath /dev/null
# commit FILE...: change each FILE in one commit on the current branch; prints the new SHA.
commit() {
  local f
  for f in "$@"; do
    mkdir -p "$repo/$(dirname "$f")"
    echo "$RANDOM" >>"$repo/$f"
    git -C "$repo" add -f "$f"
  done
  git -C "$repo" commit -qm "edit $*"
  git -C "$repo" rev-parse HEAD
}
base=$(commit README.md)

# changes NAME EVENT REF [VAR=VALUE...]: run go-ci-changes.sh; prints "go=... ci_yml=...".
changes() {
  local out=$work/output-$1
  : >"$out"
  (cd "$repo" && env GITHUB_EVENT_NAME="$2" GITHUB_REF="$3" GITHUB_SHA="$(git rev-parse HEAD)" \
    GITHUB_OUTPUT="$out" "${@:4}" "$here/go-ci-changes.sh" >/dev/null)
  echo "go=$(sed -n 's/^go=//p' "$out") ci_yml=$(sed -n 's/^ci_yml=//p' "$out")"
}

# pr NAME FILE...: a branch off $base changing FILE..., checked as a pull request into mv-go.
pr() {
  local name=$1 head
  shift
  git -C "$repo" checkout -qb "mv-go-$name" "$base"
  head=$(commit "$@")
  changes "$name" pull_request "refs/pull/1/merge" BASE_SHA="$(git -C "$repo" rev-parse mv-go)" HEAD_SHA="$head"
}

check "pr changing ci.yml only" "go=true ci_yml=true" "$(pr ciyml .github/workflows/ci.yml)"
check "pr changing ci.yml and a web page" "go=true ci_yml=true" \
  "$(pr ciweb .github/workflows/ci.yml logitrack-web/app/page.tsx)"
check "pr changing a web component" "go=false ci_yml=false" "$(pr web logitrack-web/components/x.tsx)"
check "pr changing Go code" "go=true ci_yml=false" "$(pr api logitrack-api/main.go)"
check "pr changing another workflow" "go=true ci_yml=false" "$(pr deploy .github/workflows/deploy.yml)"
# A ci.yml change already on the base branch is not the pull request's (three-dot range).
git -C "$repo" checkout -q mv-go
commit .github/workflows/ci.yml >/dev/null
check "pr behind a ci.yml change on its base" "go=false ci_yml=false" "$(pr docs shared-docs/notes.txt)"

git -C "$repo" checkout -q mv-go
check "push to mv-go" "go=true ci_yml=false" "$(changes mvgo push refs/heads/mv-go BEFORE="$base")"
check "push of an api-v tag" "go=true ci_yml=false" "$(changes tag push refs/tags/api-v1.0.0 BEFORE=0000000)"

# --- go-ci-web-ci.sh ----------------------------------------------------------------------------

stub=$work/bin
mkdir -p "$stub"
# The stub answers `gh api PATH --jq EXPR` from $STUB: list.json for the runs of ci.yml (and logs
# the query), run.json for one run; run-1.json, run-2.json, ... are served in turn first.
cat >"$stub/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[ "$1" = api ] && [ "$3" = --jq ] || { echo "stub gh: unexpected arguments: $*" >&2; exit 2; }
case "$2" in
  */actions/workflows/ci.yml/runs\?*)
    echo "$2" >>"$STUB/queries"
    json=$(cat "$STUB/list.json")
    ;;
  */actions/runs/*)
    n=$(( $(cat "$STUB/served" 2>/dev/null || echo 0) + 1 ))
    echo "$n" >"$STUB/served"
    if [ -f "$STUB/run-$n.json" ]; then json=$(cat "$STUB/run-$n.json"); else json=$(cat "$STUB/run.json"); fi
    ;;
  *) echo "stub gh: unexpected path $2" >&2; exit 2 ;;
esac
jq -r "$4" <<<"$json"
EOF
chmod +x "$stub/gh"

list_none='{"workflow_runs":[]}'
list_one='{"workflow_runs":[{"id":7,"created_at":"2026-10-11T10:00:00Z"},{"id":9,"created_at":"2026-10-11T10:05:00Z"}]}'
run() { printf '{"status":"%s","conclusion":%s,"html_url":"https://example.invalid/run/9"}' "$1" "$2"; }

# webci NAME EVENT CI_YML LIST [RUN...]: run go-ci-web-ci.sh against the stub; prints its exit code.
webci() {
  local dir=$work/stub-$1 rc=0 i=0 r
  mkdir -p "$dir"
  printf '%s' "$4" >"$dir/list.json"
  for r in "${@:5}"; do
    i=$((i + 1))
    printf '%s' "$r" >"$dir/run-$i.json"
    printf '%s' "$r" >"$dir/run.json"
  done
  env PATH="$stub:$PATH" STUB="$dir" GITHUB_REPOSITORY=o/r GITHUB_EVENT_NAME="$2" SHA=0123456789abcdef \
    CI_YML="$3" WEB_CI_POLLS=2 WEB_CI_POLL_SLEEP=0 WEB_CI_WAIT_SLEEP=0 \
    "$here/go-ci-web-ci.sh" >"$dir/log" 2>&1 || rc=$?
  echo "$rc"
}

check "no CI run, pull request" 0 "$(webci none-pr pull_request false "$list_none")"
check "no CI run, push to mv-go" 0 "$(webci none-push push false "$list_none")"
check "no CI run, pull request changing ci.yml" 1 "$(webci none-ciyml pull_request true "$list_none")"
grep -q '::error title=web-ci::this pull request changes .github/workflows/ci.yml' "$work/stub-none-ciyml/log" ||
  check "error names ci.yml" found missing
check "the queries name the commit and the event" 2 \
  "$(grep -c 'runs?head_sha=0123456789abcdef&event=pull_request&per_page=20$' "$work/stub-none-ciyml/queries")"
check "successful CI run, pull request changing ci.yml" 0 \
  "$(webci ok-ciyml pull_request true "$list_one" "$(run completed '"success"')")"
check "CI run in progress, then successful" 0 \
  "$(webci wait push false "$list_one" "$(run queued null)" "$(run in_progress null)" "$(run completed '"success"')")"
check "failed CI run" 1 "$(webci failed pull_request false "$list_one" "$(run completed '"failure"')")"
check "cancelled CI run" 1 "$(webci cancelled push false "$list_one" "$(run completed '"cancelled"')")"

exit "$failed"
