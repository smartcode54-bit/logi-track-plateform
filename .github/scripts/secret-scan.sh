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
# Nothing the commits under review contain can switch a finding off:
#   - a .gitleaks.toml or .gitleaksignore anywhere in HEAD's tree fails the scan before it starts;
#   - gitleaks scans the git directory, not the work tree, so it never loads a .gitleaks.toml or
#     .gitleaksignore from a checkout, and its rules come from a fixed config (the defaults);
#   - --ignore-gitleaks-allow reports lines marked `gitleaks:allow` like any other.
# Merge commits are scanned as their diff against the first parent: plain `git log -p` prints no
# patch for a merge, so a secret written while resolving a conflict would never be seen.
# The scan fails closed: a git error inside gitleaks, or fewer commits scanned than the range has
# commits that add text, is a failure, not "no leaks found".
#
# The image is pinned once, as GITLEAKS_IMG in logitrack-api/Makefile. Run from the repository
# root or any directory of the repository to scan; needs docker, make and the history of the range
# (fetch-depth: 0). secret-scan-selftest.sh checks these properties on a throwaway repository.
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
IMAGE=$(make -s --no-print-directory -C "$here/../../logitrack-api" print-gitleaks-img)
if [[ "$IMAGE" != *@sha256:* ]]; then
  echo "secret-scan: GITLEAKS_IMG in logitrack-api/Makefile must be pinned by digest (got '$IMAGE')" >&2
  exit 2
fi

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

if lists=$(git ls-tree -r --name-only "$head" | grep -E '(^|/)\.gitleaks(ignore|\.toml)$'); then
  echo "::error title=secret-scan::gitleaks allow-list in ${head:0:12}: $(tr '\n' ' ' <<<"$lists")" >&2
  echo "secret-scan: a finding is fixed by rotating the secret and removing it, never by an allow-list (developer-spec.md §16.5 rule 6); delete these files" >&2
  exit 1
fi

# gitleaks counts a commit when its diff adds text; anything below that count means it skipped some.
want=$(git log --diff-merges=first-parent --numstat --format=tformat:@ "$base..$head" |
  awk '$1 == "@" { open = 1; next } open && $1 ~ /^[0-9]+$/ && $1 > 0 { n++; open = 0 } END { print n + 0 }')

# A worktree keeps its git directory elsewhere; the common one holds every object.
common=$(cd "$(git rev-parse --git-common-dir)" && pwd)
log=$(mktemp)
trap 'rm -f "$log"' EXIT
rc=0
docker run --rm -v "$common:/repo.git:ro" -e GITLEAKS_CONFIG_TOML=$'[extend]\nuseDefault = true\n' \
  "$IMAGE" git /repo.git --log-opts="--diff-merges=first-parent $base..$head" --ignore-gitleaks-allow \
  --redact --verbose --no-color --no-banner --exit-code 1 2>&1 | tee "$log" || rc=$?

if awk '$2 == "ERR" || $2 == "FTL" { bad = 1 } END { exit !bad }' "$log"; then
  echo "::error title=secret-scan::gitleaks reported an error, so the scan is incomplete" >&2
  exit 2
fi
scanned=$(sed -nE 's/.* ([0-9]+) commits scanned.*/\1/p' "$log" | tail -1)
if [ -z "$scanned" ] || [ "$scanned" -lt "$want" ]; then
  echo "::error title=secret-scan::gitleaks scanned ${scanned:-no} commits; $want of the $count commits add text" >&2
  exit 2
fi
exit "$rc"
