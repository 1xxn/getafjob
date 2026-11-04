#!/usr/bin/env bash
# Checks every commit of a pull request: Conventional Commits subject, author is a GitHub noreply address,
# no co-author or generator trailers. Prints only commit SHAs, never commit text (workflow command injection).
set -euo pipefail
base="$1"
head="$2"
pattern='^(feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert)(\([a-z0-9/_-]+\))?!?: .+'
fail=0
for sha in $(git rev-list --no-merges "$base..$head"); do
  subject="$(git log -1 --format=%s "$sha")"
  author="$(git log -1 --format=%ae "$sha")"
  body="$(git log -1 --format=%B "$sha")"
  if ! grep -qE "$pattern" <<<"$subject"; then
    echo "::error::$sha: subject does not follow Conventional Commits"
    fail=1
  fi
  case "$author" in
    *@users.noreply.github.com) ;;
    *) echo "::error::$sha: author email is not a GitHub noreply address"; fail=1 ;;
  esac
  if grep -qiE '^(co-authored-by:|generated with)' <<<"$body"; then
    echo "::error::$sha: co-author or generator trailer"
    fail=1
  fi
done
exit "$fail"
