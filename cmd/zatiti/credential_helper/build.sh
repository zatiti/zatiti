#!/bin/sh
set -eu

# Local unsigned build only. The release owner signs, notarizes and verifies
# the architecture-specific result before placing it in the controller tree.
if [ "$(uname -s)" != Darwin ]; then
  echo 'credential helper build requires macOS' >&2
  exit 1
fi
case "$(uname -m)" in
  x86_64) expected_arch=amd64 ;;
  arm64) expected_arch=arm64 ;;
  *) echo 'credential helper requires native Intel or Apple Silicon' >&2; exit 1 ;;
esac
if [ "$(go env GOARCH)" != "$expected_arch" ]; then
  echo 'Go architecture differs from the native Mac host' >&2
  exit 1
fi
if [ "$#" -ne 1 ]; then
  echo 'usage: build.sh OUTPUT_DIRECTORY' >&2
  exit 2
fi
source_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_dir=$(CDPATH= cd -- "$source_dir/../../.." && pwd)
output_dir=$1
mkdir -p "$output_dir"
output_dir=$(CDPATH= cd -- "$output_dir" && pwd)
archive="$output_dir/libzatiti_credential_bridge.a"
(
  cd "$repo_dir"
  CGO_ENABLED=1 go build -tags zatiti_carchive -buildmode=c-archive -o "$archive" ./cmd/zatiti
)
cat > "$output_dir/bridge.h" <<'HEADER'
#include "libzatiti_credential_bridge.h"
HEADER
swiftc -O \
  -import-objc-header "$output_dir/bridge.h" \
  -Xcc "-I$output_dir" \
  "$source_dir/CredentialHelper.swift" "$archive" \
  -framework AppKit -framework Security -framework Foundation \
  -o "$output_dir/zatiti-credential-helper"
chmod 0700 "$output_dir/zatiti-credential-helper"
echo "$output_dir/zatiti-credential-helper"
