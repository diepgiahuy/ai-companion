#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: $0 <checkpoint-tag> [output-dir]" >&2
  exit 2
}

[[ $# -ge 1 && $# -le 2 ]] || usage

tag="$1"
repo_root="$(git rev-parse --show-toplevel)"
out_dir="${2:-$repo_root/dist/checkpoints}"

cd "$repo_root"

if [[ -n "$(git status --porcelain --untracked-files=normal)" ]]; then
  echo "checkpoint snapshot requires a clean worktree" >&2
  git status --short >&2
  exit 1
fi

if ! git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
  echo "checkpoint tag does not exist: $tag" >&2
  exit 1
fi

review_note="docs/checkpoints/${tag}.md"
if [[ ! -f "$review_note" ]]; then
  echo "checkpoint note is required before snapshot: $review_note" >&2
  exit 1
fi
if ! grep -Fq "## Independent static review" "$review_note"; then
  echo "checkpoint note is missing independent static review section: $review_note" >&2
  exit 1
fi
if ! grep -Fq "Static review status: PASS" "$review_note"; then
  echo "checkpoint static review has not passed: $review_note" >&2
  exit 1
fi

review_report="docs/reviews/${tag}-static-review.md"
if [[ ! -f "$review_report" ]]; then
  echo "independent static review report is required before snapshot: $review_report" >&2
  exit 1
fi
if ! grep -Fq "Static review status: PASS" "$review_report"; then
  echo "independent static review report has not passed: $review_report" >&2
  exit 1
fi

head_commit="$(git rev-parse HEAD)"
tag_commit="$(git rev-list -n 1 "$tag")"
if [[ "$head_commit" != "$tag_commit" ]]; then
  echo "checkpoint tag $tag does not point to HEAD" >&2
  echo "HEAD: $head_commit" >&2
  echo "TAG : $tag_commit" >&2
  exit 1
fi

slug="$(printf '%s' "$tag" | tr '[:upper:]' '[:lower:]' | tr -cs 'a-z0-9._-' '-')"
slug="${slug%-}"
mkdir -p "$out_dir"

zip_path="$out_dir/companion-production-v1-${slug}.zip"
bundle_path="$out_dir/companion-production-v1-checkpoints-through-${slug}.bundle"
manifest_path="$out_dir/companion-production-v1-${slug}.manifest.txt"

rm -f "$zip_path" "$zip_path.sha256" "$bundle_path" "$bundle_path.sha256" "$manifest_path"

git archive \
  --format=zip \
  --prefix="companion-production-v1-${slug}/" \
  --output="$zip_path" \
  "$tag"

git bundle create "$bundle_path" --all

sha256sum "$zip_path" > "$zip_path.sha256"
sha256sum "$bundle_path" > "$bundle_path.sha256"

{
  echo "project=Companion Production v1"
  echo "checkpoint_tag=$tag"
  echo "commit=$tag_commit"
  echo "tree=$(git rev-parse "$tag^{tree}")"
  echo "created_utc=$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
  echo "archive=$(basename "$zip_path")"
  echo "archive_sha256=$(sha256sum "$zip_path" | awk '{print $1}')"
  echo "bundle=$(basename "$bundle_path")"
  echo "bundle_sha256=$(sha256sum "$bundle_path" | awk '{print $1}')"
  echo "restore_archive=unzip $(basename "$zip_path")"
  echo "restore_git=git clone $(basename "$bundle_path") companion-production-v1 && cd companion-production-v1 && git checkout $tag"
} > "$manifest_path"

unzip -tq "$zip_path" >/dev/null
git bundle verify "$bundle_path" >/dev/null

echo "checkpoint snapshot verified"
echo "  tag:      $tag"
echo "  commit:   $tag_commit"
echo "  archive:  $zip_path"
echo "  bundle:   $bundle_path"
echo "  manifest: $manifest_path"
