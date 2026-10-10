#!/usr/bin/env bash
# gitleaks over the commits a push or pull request brings to mv-go or a mv-go-* branch
# (developer-spec.md §16.5 rule 6, §17.2; workflow secret-scan.yml). Findings are redacted, so the
# log never shows a value. History before mv-go already holds the Cartrack credentials: the remedy
# is rotation, never an allow-list, so this scans only the commits under review. The one-off scan
# of the full history with a private report is the owner's (§16.5).
#
#   secret-scan.sh BASE HEAD   scan the commits of HEAD that BASE lacks (git log BASE..HEAD)
#   secret-scan.sh             derive the range from the GitHub event (CI): pull_request
#                              BASE_SHA..HEAD_SHA; push BEFORE..GITHUB_SHA, or, for a new or
#                              force-pushed branch, what it adds to origin/mv-go (to origin/main
#                              for mv-go itself)
#
# Run from the repository root; needs docker and the history of the range (fetch-depth: 0).
set -euo pipefail

IMAGE=zricethezav/gitleaks:v8.30.1@sha256:c00b6bd0aeb3071cbcb79009cb16a60dd9e0a7c60e2be9ab65d25e6bc8abbb7f

commit() { git rev-parse --verify --quiet "$1^{commit}"; }

if [ $# -eq 2 ]; then
  base=$1 head=$2
elif [ $# -eq 0 ]; then
  case "${GITHUB_EVENT_NAME:-}" in
    pull_request)
      base=$BASE_SHA head=$HEAD_SHA
      ;;
    push)
      head=$GITHUB_SHA
      if [[ -n "${BEFORE:-}" && ! "$BEFORE" =~ ^0+$ ]] && commit "$BEFORE" >/dev/null; then
        base=$BEFORE
      elif [ "${GITHUB_REF:-}" = refs/heads/mv-go ]; then
        base=origin/main
      else
        base=origin/mv-go
      fi
      ;;
    *)
      echo "secret-scan: unsupported event '${GITHUB_EVENT_NAME:-}'; pass BASE and HEAD" >&2
      exit 2
      ;;
  esac
else
  echo "usage: secret-scan.sh [BASE HEAD]" >&2
  exit 2
fi

base=$(commit "$base") || { echo "secret-scan: base is not a known commit" >&2; exit 2; }
head=$(commit "$head") || { echo "secret-scan: head is not a known commit" >&2; exit 2; }
count=$(git rev-list --count "$base..$head")
echo "secret-scan: $count commit(s) in ${base:0:12}..${head:0:12}"
if [ "$count" -eq 0 ]; then
  exit 0
fi

# A worktree keeps its git directory elsewhere; mount the common one at the same path.
top=$(git rev-parse --show-toplevel)
common=$(cd "$(git rev-parse --git-common-dir)" && pwd)
mounts=(-v "$top:$top:ro")
case "$common" in "$top"/*) ;; *) mounts+=(-v "$common:$common:ro") ;; esac

docker run --rm "${mounts[@]}" "$IMAGE" git "$top" \
  --log-opts="$base..$head" --redact --verbose --no-color --no-banner --exit-code 1
