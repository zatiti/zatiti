#!/bin/sh
set -eu

if [ "$#" -ne 1 ]; then
  echo 'usage: verify.sh BUILD_DIRECTORY' >&2
  exit 2
fi
source_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
build_dir=$(CDPATH= cd -- "$1" && pwd)
test_bin="$build_dir/credential-helper-tests"
swiftc -DHELPER_TESTING -Onone \
  -import-objc-header "$build_dir/bridge.h" -Xcc "-I$build_dir" \
  "$source_dir/CredentialHelper.swift" "$build_dir/libzatiti_credential_bridge.a" \
  -framework AppKit -framework Security -framework Foundation -o "$test_bin"
"$test_bin"

helper="$build_dir/zatiti-credential-helper"
expected='{"reason_code":"invalid_result","schema":"zatiti.gui-credential-capture-result\/v1","status":"repair_required"}'
actual=$("$helper" capture --installation-id 11111111-1111-1111-1111-111111111111 --connection-id INVALID)
if [ "$actual" != "$expected" ]; then
  echo 'invalid identity did not fail with the strict redacted result' >&2
  exit 1
fi
actual=$("$helper" capture --installation-id 11111111-1111-1111-1111-111111111111 --connection-id 22222222-2222-2222-2222-222222222222 synthetic-secret)
if [ "$actual" != "$expected" ]; then
  echo 'extra argument was not refused without disclosure' >&2
  exit 1
fi
echo 'credential helper local protocol checks passed'
