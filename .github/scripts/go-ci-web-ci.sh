#!/usr/bin/env bash
# go-ci "web-ci" job: wait for the CI (ci.yml) run of the same commit and event and pass its result
# on, so the go-ci check also stands for the web checks (developer-spec.md §17.2).
#
# CI keeps its path filter (its main entries are unchanged, R90), so a commit may have no CI run:
# when none appears within two minutes, the filter skipped the commit and the job passes. Except for
# a pull request that changes .github/workflows/ci.yml (CI_YML=true, from go-ci-changes.sh): only
# CI's push filter lists ci.yml, and its pull_request entry cannot (it is shared with main), so
# without a web path in the pull request the changed workflow would first run on mv-go, after the
# merge. The job fails such a pull request and asks for a change under a path of the filter.
#
# Inputs (environment): GITHUB_REPOSITORY, GITHUB_EVENT_NAME, SHA, CI_YML, GH_TOKEN (for gh).
# WEB_CI_POLLS, WEB_CI_POLL_SLEEP and WEB_CI_WAIT_SLEEP override the timing (12 polls 10 s apart,
# then a check every 20 s) for go-ci-selftest.sh.
set -euo pipefail

polls=${WEB_CI_POLLS:-12}
runs="repos/$GITHUB_REPOSITORY/actions/workflows/ci.yml/runs?head_sha=$SHA&event=$GITHUB_EVENT_NAME&per_page=20"
id=""
# CI starts on the same event as go-ci; when no run appears, its path filter skipped the commit.
for _ in $(seq 1 "$polls"); do
  id=$(gh api "$runs" --jq '.workflow_runs | sort_by(.created_at) | last | .id // empty')
  [ -n "$id" ] && break
  sleep "${WEB_CI_POLL_SLEEP:-10}"
done

if [ -z "$id" ]; then
  if [ "$GITHUB_EVENT_NAME" = pull_request ] && [ "${CI_YML:-false}" = true ]; then
    echo "::error title=web-ci::this pull request changes .github/workflows/ci.yml but no CI run started for ${SHA::12}: CI's pull_request path filter (logitrack-web/**, shared-docs/**, pnpm-lock.yaml) cannot list ci.yml because that entry is shared with main (R90). Change a file under that filter in this pull request too, such as its Change Log row in shared-docs/.vibe-rules.md, so CI runs the changed workflow before it reaches mv-go (developer-spec.md §17.2)."
    exit 1
  fi
  echo "::notice title=web-ci::no CI run for ${SHA::12} ($GITHUB_EVENT_NAME): CI's path filter matched no file of it"
  exit 0
fi

while :; do
  IFS=$'\t' read -r status conclusion url < <(gh api "repos/$GITHUB_REPOSITORY/actions/runs/$id" \
    --jq '[.status, (.conclusion // "-"), .html_url] | @tsv')
  [ "$status" = completed ] && break
  sleep "${WEB_CI_WAIT_SLEEP:-20}"
done
echo "CI run $url: $conclusion"
test "$conclusion" = success
