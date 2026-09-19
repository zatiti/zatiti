#!/usr/bin/env bash
# Proves this client against a real controller.
#
# Builds the product's own `zatiti` binary from cmd/zatiti, then runs
# live_test/, which starts `zatiti serve`, runs `zatiti init`, reads the
# owner credential the controller writes, and drives the Dart transport over
# the controller's private Unix socket. Nothing is mocked and nothing is
# skipped: if the controller cannot be reached the suite fails.
#
# Run from apps/desktop:
#   tool/live-proof.sh
#
# Environment:
#   ZATITI_CONTROLLER_BIN   use this binary instead of building one
#   ZATITI_LIVE_STATE_BASE  where state directories are made (default /tmp).
#                           The controller's socket path must stay under 104
#                           bytes, so this must be a short path.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo="$(cd "$here/../.." && pwd)"
cd "$here"

if [ -z "${ZATITI_CONTROLLER_BIN:-}" ]; then
  build_dir="$(mktemp -d "${TMPDIR:-/tmp}/zatiti-live-XXXXXX")"
  trap 'rm -rf "$build_dir"' EXIT
  echo "building the controller from $repo/cmd/zatiti" >&2
  (cd "$repo" && go build -o "$build_dir/zatiti" ./cmd/zatiti)
  export ZATITI_CONTROLLER_BIN="$build_dir/zatiti"
fi
export ZATITI_LIVE_STATE_BASE="${ZATITI_LIVE_STATE_BASE:-/tmp}"

echo "controller: $ZATITI_CONTROLLER_BIN" >&2
echo "state base: $ZATITI_LIVE_STATE_BASE" >&2
exec flutter test live_test/ "$@"
