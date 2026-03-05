#!/usr/bin/env bash
set -euo pipefail

if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  echo "Not inside a Git repository." >&2
  exit 1
fi

declare -a files=()
while IFS= read -r path; do
  [ -n "$path" ] && files+=("$path")
done < <(git diff --name-only)

while IFS= read -r path; do
  [ -n "$path" ] && files+=("$path")
done < <(git ls-files --others --exclude-standard)

declare -A seen_paths=()
declare -a deduped_files=()
for path in "${files[@]}"; do
  if [ -z "${seen_paths[$path]+x}" ]; then
    deduped_files+=("$path")
    seen_paths["$path"]=1
  fi
done
files=("${deduped_files[@]}")

if [ "${#files[@]}" -eq 0 ]; then
  echo "No unstaged files to commit."
  exit 0
fi

git add -- "${files[@]}"

status_lines="$(git diff --cached --name-status -- "${files[@]}")"
if [ -z "$status_lines" ]; then
  echo "No staged changes found for unstaged files." >&2
  exit 1
fi

adds=0
mods=0
dels=0
rens=0
tests=0
docs=0
configs=0
gomod_related=0
go_files=0
source_files=0

declare -A dir_count=()

while IFS=$'\t' read -r status path1 path2; do
  code="${status:0:1}"
  case "$code" in
    A) adds=$((adds + 1)) ;;
    M) mods=$((mods + 1)) ;;
    D) dels=$((dels + 1)) ;;
    R) rens=$((rens + 1)) ;;
  esac

  file_path="$path1"
  [ "$code" = "R" ] && file_path="$path2"

  top="${file_path%%/*}"
  if [ "$top" = "$file_path" ]; then
    top="root"
  fi
  dir_count["$top"]=$(( ${dir_count["$top"]:-0} + 1 ))

  case "$file_path" in
    *.md|docs/*|README*|CHANGELOG*) docs=$((docs + 1)) ;;
  esac
  case "$file_path" in
    *_test.go|test/*|tests/*|**/__tests__/*) tests=$((tests + 1)) ;;
  esac
  case "$file_path" in
    go.mod|go.sum|Makefile|*.yml|*.yaml|*.toml|*.json|.github/*|Dockerfile*) configs=$((configs + 1)) ;;
  esac
  case "$file_path" in
    go.mod|go.sum) gomod_related=$((gomod_related + 1)) ;;
  esac
  case "$file_path" in
    *.go) go_files=$((go_files + 1)) ;;
  esac
  case "$file_path" in
    *.go|*.js|*.ts|*.tsx|*.py|*.java|*.rs|*.c|*.cpp|*.h|*.hpp|*.sh) source_files=$((source_files + 1)) ;;
  esac
done <<< "$status_lines"

total="${#files[@]}"

scope="repo"
max_count=0
for k in "${!dir_count[@]}"; do
  c="${dir_count[$k]}"
  if [ "$c" -gt "$max_count" ]; then
    max_count="$c"
    scope="$k"
  fi
done

# Normalize common scopes to project layout.
case "$scope" in
  cmd|internal|root) ;;
  *) [ "$go_files" -gt 0 ] && scope="internal" || scope="repo" ;;
esac

type="chore"
if [ "$docs" -eq "$total" ]; then
  type="docs"
elif [ "$tests" -eq "$total" ]; then
  type="test"
elif [ "$configs" -eq "$total" ] || [ "$gomod_related" -gt 0 ]; then
  type="chore"
elif [ "$adds" -gt 0 ] && [ "$source_files" -gt 0 ]; then
  type="feat"
elif [ "$mods" -gt 0 ] && [ "$adds" -eq 0 ]; then
  type="refactor"
fi

branch_name="$(git rev-parse --abbrev-ref HEAD)"
if echo "$branch_name" | grep -Eqi '(fix|bug|hotfix)'; then
  type="fix"
fi

ticket=""
if echo "$branch_name" | grep -Eoq '[A-Z]{2,}-[0-9]+'; then
  ticket="$(echo "$branch_name" | grep -Eo '[A-Z]{2,}-[0-9]+' | head -n1)"
fi

summary_parts=()
[ "$adds" -gt 0 ] && summary_parts+=("add $adds")
[ "$mods" -gt 0 ] && summary_parts+=("modify $mods")
[ "$dels" -gt 0 ] && summary_parts+=("delete $dels")
[ "$rens" -gt 0 ] && summary_parts+=("rename $rens")

summary=""
for part in "${summary_parts[@]}"; do
  if [ -n "$summary" ]; then
    summary+=", "
  fi
  summary+="$part"
done
[ -z "$summary" ] && summary="update $total files"

prefix="${type}(${scope}):"
[ -n "$ticket" ] && prefix="${ticket} ${prefix}"
message="${prefix} ${summary}."

word_count="$(echo "$message" | wc -w | tr -d ' ')"
if [ "$word_count" -gt 20 ]; then
  compact="${type}(${scope}): update ${total} files."
  [ -n "$ticket" ] && compact="${ticket} ${compact}"
  message="$compact"
fi

git commit -m "$message" -- "${files[@]}"

commit_hash="$(git rev-parse --short HEAD)"
echo "Committed: $commit_hash"
echo "Message: $message"
