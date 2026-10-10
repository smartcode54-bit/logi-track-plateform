#!/usr/bin/env bash
# go-ci "changes" job: decide whether the Go jobs have work. They run when the commits of this
# push or pull request touch logitrack-api/ or a file its checks read: the env inventory of
# developer-spec.md §16.1, Appendix A and C, shared-docs/schemas, every GitHub workflow and action
# (pgtest.TestEveryPostgresImageIsTheSame reads them all), go-ci's own scripts, and the web inputs of
# internal/authz/webroutes_test.go: every page under logitrack-web/app/app/ (an unmapped page fails
# TestEveryWebPageIsMapped) and logitrack-web/lib/capabilities.ts (the legacy route table, compared
# with its frozen snapshot until TW3 moves it; Appendix C §C.2.7). A push to mv-go
# runs everything, so every mv-go commit gets its image (§17.2, §17.4) whatever it touched; so
# does a range that cannot be compared (new branch, force push, tag).
#
# The workflow triggers on every push and pull request of mv-go and mv-go-** (no `paths:` filter),
# so the aggregate `go-ci` check always reports and can be a required check on mv-go: a workflow
# skipped by a path filter would leave a required check pending forever.
#
# Inputs (environment): GITHUB_EVENT_NAME, GITHUB_REF, GITHUB_SHA, GITHUB_OUTPUT, and BASE_SHA +
# HEAD_SHA (pull_request) or BEFORE (push).
set -euo pipefail

pattern='^(logitrack-api/|shared-docs/schemas/|shared-docs/specs/mv-go/|developer-spec\.md$|logitrack-web/app/app/|logitrack-web/lib/capabilities\.ts$|\.github/workflows/|\.github/actions/|\.github/scripts/go-ci-)'

if [ "$GITHUB_EVENT_NAME" = push ] && [ "$GITHUB_REF" = refs/heads/mv-go ]; then
  echo "go=true" >> "$GITHUB_OUTPUT"
  echo "push to mv-go: every job runs and the image is pushed as :$GITHUB_SHA"
  exit 0
fi

range=""
case "$GITHUB_EVENT_NAME" in
  pull_request)
    range="$BASE_SHA...$HEAD_SHA"
    ;;
  push)
    if [[ "$GITHUB_REF" == refs/heads/* && -n "${BEFORE:-}" && ! "$BEFORE" =~ ^0+$ ]] &&
      git cat-file -e "$BEFORE^{commit}" 2>/dev/null; then
      range="$BEFORE..$GITHUB_SHA"
    fi
    ;;
esac

if [ -z "$range" ]; then
  echo "go=true" >> "$GITHUB_OUTPUT"
  echo "no comparable range ($GITHUB_EVENT_NAME $GITHUB_REF): every job runs"
  exit 0
fi

files=$(git diff --name-only "$range")
if grep -Eq "$pattern" <<<"$files"; then go=true; else go=false; fi
echo "go=$go" >> "$GITHUB_OUTPUT"
echo "go=$go: $(grep -c . <<<"$files" || true) file(s) changed in $range"
grep -E "$pattern" <<<"$files" | head -50 || true
