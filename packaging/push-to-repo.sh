#!/bin/sh
# Commit one generated file into another GitHub repository and push it: the
# Homebrew tap takes Formula/glute.rb, the Scoop bucket takes bucket/glute.json.
# Both packaging repos need the identical dance, so it lives here once instead of
# twice in release.yml — and it can be run by hand to publish what the workflow
# could not (no secret set, say), with `make formula` / `make manifest` output as
# the source file.
#
#   PUSH_TOKEN=<token> push-to-repo.sh <owner/repo> <source file> <path in repo> <commit message>
#
# PUSH_TOKEN needs write access to <owner/repo>. A GitHub Actions job token will
# not do: it is scoped to the repository the workflow runs in, and the packaging
# repos are different ones. Commits to the repository's default branch; exits 0
# without committing when the file there is already identical.
set -eu

if [ $# -ne 4 ]; then
	echo "usage: PUSH_TOKEN=<token> $0 <owner/repo> <source file> <path in repo> <commit message>" >&2
	exit 2
fi
repo=$1; src=$2; dest=$3; message=$4
: "${PUSH_TOKEN:?PUSH_TOKEN is not set}"
[ -f "$src" ] || { echo "error: $src not found" >&2; exit 1; }

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cp "$src" "$work/payload"

# A depth-1 clone lands on the default branch, so pushing HEAD needs no lookup of
# what that branch is called.
git clone --quiet --depth 1 "https://x-access-token:$PUSH_TOKEN@github.com/$repo.git" "$work/repo"
cd "$work/repo"
mkdir -p "$(dirname "$dest")"
cp "$work/payload" "$dest"

# In CI there is no identity configured; by hand, the caller's own is kept.
git config user.name > /dev/null 2>&1 || git config user.name "github-actions[bot]"
git config user.email > /dev/null 2>&1 || git config user.email "41898282+github-actions[bot]@users.noreply.github.com"

git add "$dest"
if git diff --cached --quiet; then
	echo "$repo: $dest already current"
	exit 0
fi
git commit --quiet -m "$message"
git push --quiet origin HEAD
echo "$repo: pushed $dest ($message)"
