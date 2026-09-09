#!/bin/sh
# Print repository files in the OX_FILE block format so they can be pasted
# into an Ox Alpha conversation as context. Usage:
#   scripts/ox-context.sh go.mod cmd/zatiti internal/state docs/rfc.md | pbcopy
# Directories expand to their tracked text files. Nothing is sent anywhere;
# the output goes to stdout only.
set -eu
for target in "$@"; do
  if [ -d "$target" ]; then
    files="$(git ls-files -- "$target")"
  else
    files="$target"
  fi
  for f in $files; do
    case "$f" in *.db|*.sum|*.png|*.jpg|*.gif) continue ;; esac
    printf 'OX_FILE: %s\n`````\n' "$f"
    cat "$f"
    printf '\n`````\n\n'
  done
done
