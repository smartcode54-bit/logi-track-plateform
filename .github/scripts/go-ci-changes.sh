#!/usr/bin/env bash
# go-ci "changes" job: decide whether the Go jobs have work. They run when the commits of this
# push or pull request touch logitrack-api/ or a document its checks read (the env inventory of
# developer-spec.md §16.1, Appendix A and C, shared-docs/schemas), or go-ci itself. A range that
# cannot be compared (new branch, force push, tag) runs everything.
#
# The workflow triggers on every push and pull request of mv-go and mv-go-** (no `paths:` filter),
# so the aggregate `go-ci` check always reports and can be a required check on mv-go: a workflow
# skipped by a path filter would leave a required check pending forever.
#
# Inputs (environment): GITHUB_EVENT_NAME, GITHUB_REF, GITHUB_SHA, GITHUB_OUTPUT, and BASE_SHA +
# HEAD_SHA (pull_request) or BEFORE (push).
set -euo pipefail

pattern='^(logitrack-api/|shared-docs/schemas/|shared-docs/specs/mv-go/|developer-spec\.md$|\.github/workflows/go-ci\.yml$|\.github/actions/setup-go/|\.github/scripts/go-ci-)'

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
