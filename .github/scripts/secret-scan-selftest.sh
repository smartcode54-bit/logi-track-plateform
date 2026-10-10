#!/usr/bin/env bash
# Self-test of secret-scan.sh on throwaway repositories (developer-spec.md §16.5 rule 6, §17.2;
# step of secret-scan.yml): a secret in the commits under review fails the scan however those
# commits try to hide it (inline gitleaks:allow, a committed .gitleaks.toml or .gitleaksignore,
# allow-list files in the work tree, a conflict resolution, a merge with extra content), a clean
# range passes, and a scan that errors or skips commits fails instead of passing.
#
# Needs docker, make and git; the repositories live in a temporary directory that is removed on
# exit. Each run makes up a fresh fake GitHub token, so no file of this repository holds one.
set -euo pipefail
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE

scan=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/secret-scan.sh
work=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/secret-scan-selftest.XXXXXX")
work=$(cd "$work" && pwd -P)
trap 'rm -rf "$work"' EXIT

token() {
  local a=ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789 s=""
  while [ ${#s} -lt 36 ]; do s+=${a:RANDOM%62:1}; done
  printf 'ghp_%s' "$s"
}

# repo NAME: a repository with one clean commit on main; prints its path.
repo() {
  local d=$work/$1
  git init -q -b main "$d"
  git -C "$d" config user.email selftest@example.invalid
  git -C "$d" config user.name secret-scan-selftest
  git -C "$d" config commit.gpgsign false
  git -C "$d" config core.hooksPath /dev/null
  echo base >"$d/f.txt"
  git -C "$d" add f.txt
  git -C "$d" commit -qm base
  echo "$d"
}

# commit DIR FILE TEXT: write TEXT to FILE and commit it.
commit() {
  printf '%s\n' "$3" >"$1/$2"
  git -C "$1" add -f "$2"
  git -C "$1" commit -qm "edit $2"
}

failed=0
scanpath=$PATH
# expect NAME WANT_EXIT DIR BASE HEAD [TEXT]: run secret-scan.sh BASE HEAD in DIR.
expect() {
  local name=$1 want=$2 dir=$3 base=$4 head=$5 text=${6:-} rc=0 out
  out=$(cd "$dir" && PATH=$scanpath "$scan" "$base" "$head" 2>&1) || rc=$?
  if [ "$rc" -ne "$want" ] || { [ -n "$text" ] && ! grep -qF -- "$text" <<<"$out"; }; then
    echo "FAIL $name: exit $rc, want $want${text:+ with \"$text\"}"
    printf '    %s\n' "${out//$'\n'/$'\n'    }" # findings are redacted
    failed=1
  else
    echo "ok   $name (exit $rc)"
  fi
}

# A clean range passes, including commits that add no text (gitleaks does not count those).
d=$(repo clean)
commit "$d" g.txt "nothing secret here"
git -C "$d" rm -q f.txt && git -C "$d" commit -qm delete
git -C "$d" commit -q --allow-empty -m empty
git -C "$d" mv g.txt h.txt && git -C "$d" commit -qm rename
expect clean 0 "$d" HEAD~4 HEAD "no leaks found"

d=$(repo plain)
commit "$d" leak.txt "github_token = $(token)"
expect plain 1 "$d" HEAD~1 HEAD "github-pat"

d=$(repo inline-allow)
commit "$d" leak.txt "github_token = $(token) # gitleaks:allow"
expect inline-gitleaks-allow 1 "$d" HEAD~1 HEAD "github-pat"

d=$(repo committed-config)
commit "$d" leak.txt "github_token = $(token)"
commit "$d" .gitleaks.toml "$(printf '[extend]\nuseDefault = true\n[allowlist]\npaths = ['"'''"'leak\\.txt'"'''"']')"
expect committed-gitleaks-toml 1 "$d" HEAD~2 HEAD "allow-list"

d=$(repo committed-ignore)
commit "$d" leak.txt "github_token = $(token)"
mkdir "$d/sub"
commit "$d" sub/.gitleaksignore "$(git -C "$d" rev-parse HEAD):leak.txt:github-pat:1"
expect committed-gitleaksignore 1 "$d" HEAD~2 HEAD "allow-list"

# Allow-list files that exist only in the work tree: the scan reads the git directory, not a checkout.
d=$(repo worktree-allowlist)
commit "$d" leak.txt "github_token = $(token)"
printf '[extend]\nuseDefault = true\n[allowlist]\npaths = ['"'''"'leak\\.txt'"'''"']\n' >"$d/.gitleaks.toml"
echo "$(git -C "$d" rev-parse HEAD):leak.txt:github-pat:1" >"$d/.gitleaksignore"
expect worktree-allowlist 1 "$d" HEAD~1 HEAD "github-pat"

# A secret written while resolving a merge conflict exists only in the merge commit.
d=$(repo merge-conflict)
git -C "$d" checkout -qb feature
commit "$d" f.txt "feature side"
git -C "$d" checkout -q main
commit "$d" f.txt "main side"
git -C "$d" checkout -q feature
git -C "$d" merge -q main >/dev/null 2>&1 || true
printf 'both sides\ngithub_token = %s\n' "$(token)" >"$d/f.txt"
git -C "$d" add f.txt
git -C "$d" commit -qm "merge main into feature"
expect merge-conflict-resolution 1 "$d" main feature "github-pat"

# A merge without conflict that carries content of its own.
d=$(repo merge-extra)
git -C "$d" checkout -qb feature
commit "$d" g.txt "feature side"
git -C "$d" checkout -q main
commit "$d" h.txt "main side"
git -C "$d" checkout -q feature
git -C "$d" merge -q --no-ff --no-commit main >/dev/null 2>&1
printf 'github_token = %s\n' "$(token)" >"$d/leak.txt"
git -C "$d" add leak.txt
git -C "$d" commit -qm "merge main into feature"
expect merge-extra-content 1 "$d" main feature "github-pat"

# Fail closed: a stand-in for docker plays a gitleaks that errored or skipped commits and exited 0.
d=$(repo fail-closed)
commit "$d" g.txt "nothing secret here"
mkdir -p "$work/bin-err" "$work/bin-skip"
printf '#!/bin/sh\necho "5:07AM ERR [git] fatal: simulated"\necho "5:07AM INF 1 commits scanned."\necho "5:07AM INF no leaks found"\n' >"$work/bin-err/docker"
printf '#!/bin/sh\necho "5:07AM INF 0 commits scanned."\necho "5:07AM INF no leaks found"\n' >"$work/bin-skip/docker"
chmod +x "$work/bin-err/docker" "$work/bin-skip/docker"
scanpath=$work/bin-err:$PATH
expect gitleaks-error 2 "$d" HEAD~1 HEAD "reported an error"
scanpath=$work/bin-skip:$PATH
expect gitleaks-skipped-commits 2 "$d" HEAD~1 HEAD "scanned 0 commits"
scanpath=$PATH

if [ "$failed" -ne 0 ]; then
  echo "secret-scan self-test: FAILED" >&2
  exit 1
fi
echo "secret-scan self-test: ok"
