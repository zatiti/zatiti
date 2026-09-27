#!/bin/sh
# Produce the Mac release set on a release-owner Mac: Apple-signed and
# notarized Intel and Apple Silicon controller, desktop and installer assets,
# the signed release descriptor, the signed six-asset delivery index, and a
# SHA256SUMS file. It publishes nothing; the output directory is for the
# release owner to review and upload.
#
# Apple signing is configured only through the ZATITI_APPLE_* environment
# (see packaging/mac_apple.go): identity names and a notarytool keychain
# profile that the operator prepared in a keychain. Credentials never pass
# through this script. Without that configuration the script stops before
# producing anything, with a "not configured" error. --unsigned-dry-run
# skips every Apple step to exercise the chain locally and marks its output
# UNSIGNED-DRY-RUN; that output is never a release.
#
# Input layout (--input DIR), one directory per architecture:
#   DIR/<arch>/controller/                staged controller tree: bin/zatiti,
#                                         bin/zatiti-credential-helper, LICENSE,
#                                         SBOM and notices named by its descriptor
#   DIR/<arch>/controller.descriptor.json zatiti-pack assemble descriptor
#   DIR/<arch>/desktop/                   staged desktop tree without the bundle
#   DIR/<arch>/desktop.descriptor.json    descriptor; desktop.bundle names the
#                                         archive path inside the desktop tree and
#                                         desktop.executable is
#                                         zatiti_desktop.app/Contents/MacOS/<runner>
#   DIR/<arch>/zatiti_desktop.app         the built release-mode application
# <arch> is amd64 and arm64; both are required.
set -eu

usage() {
  echo 'usage: produce-mac-release.sh --input DIR --out DIR --sequence N --base-url https://HOST/PATH (--release-key KEY.pem --trusted PUB.pem | --unsigned-dry-run)' >&2
  exit 2
}

input='' out='' sequence='' base_url='' release_key='' trusted='' dry_run=0
while [ "$#" -gt 0 ]; do
  case "$1" in
    --input) input=$2; shift 2 ;;
    --out) out=$2; shift 2 ;;
    --sequence) sequence=$2; shift 2 ;;
    --base-url) base_url=$2; shift 2 ;;
    --release-key) release_key=$2; shift 2 ;;
    --trusted) trusted=$2; shift 2 ;;
    --unsigned-dry-run) dry_run=1; shift ;;
    *) usage ;;
  esac
done
[ -n "$input" ] && [ -n "$out" ] && [ -n "$sequence" ] && [ -n "$base_url" ] || usage
# A dry run signs release metadata only with a throwaway key it mints itself,
# so its unsigned output can never verify against a trusted release key and
# be published by mistake. A signed run requires the operator's keys.
if [ "$dry_run" = 1 ]; then
  if [ -n "$release_key" ] || [ -n "$trusted" ]; then
    echo 'an unsigned dry run uses its own throwaway metadata key; do not pass --release-key or --trusted' >&2
    exit 2
  fi
elif [ -z "$release_key" ] || [ -z "$trusted" ]; then
  usage
fi
# Paths are later expanded unquoted in flag lists; refuse whitespace and glob
# characters rather than let them split or expand into other arguments.
for p in "$input" "$out" "$release_key" "$trusted" "$0"; do
  case "$p" in
    *[[:space:]]* | *[*?[]*)
      echo "paths must not contain whitespace or glob characters: $p" >&2
      exit 2 ;;
  esac
done
if [ "$(uname -s)" != Darwin ]; then
  echo 'Mac release production requires macOS' >&2
  exit 1
fi
if [ -e "$out" ]; then
  echo "output directory already exists: $out" >&2
  exit 1
fi

repo=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
input=$(CDPATH='' cd -- "$input" && pwd)
mkdir -p "$out"
out=$(CDPATH='' cd -- "$out" && pwd)
work="$out/.work"
mkdir -p "$work" "$out/assets"
chmod 0700 "$work"
pack="$work/zatiti-pack"
(cd "$repo" && go build -o "$pack" ./packaging/cmd/zatiti-pack)
entitlements="$repo/apps/desktop/macos/Runner/Release.entitlements"

if [ "$dry_run" = 1 ]; then
  echo 'UNSIGNED DRY RUN: every Apple step is skipped and metadata is signed with a throwaway key; this output is not a release.' > "$out/UNSIGNED-DRY-RUN"
  release_key="$work/dry-run-release-key.pem"
  trusted="$out/UNSIGNED-DRY-RUN.pub.pem"
  "$pack" keygen --private-out "$release_key" --public-out "$trusted" > /dev/null
else
  # Fail closed before any work when Apple signing is not configured.
  "$pack" apple --step status --require > "$out/apple-status.json"
fi

# notarize_app notarizes one application bundle and staples its ticket.
notarize_app() {
  zip="$work/$(basename "$1").zip"
  /usr/bin/ditto -c -k --keepParent "$1" "$zip"
  "$pack" apple --step notarize "$zip" > "$work/notarize-$(basename "$1").json"
  "$pack" apple --step staple "$1" > /dev/null
}

parts=
for arch in amd64 arm64; do
  src="$input/$arch"
  for f in controller desktop controller.descriptor.json desktop.descriptor.json zatiti_desktop.app; do
    if [ ! -e "$src/$f" ]; then
      echo "missing input: $arch/$f" >&2
      exit 1
    fi
  done
  dest="$work/$arch"
  mkdir -p "$dest"
  /usr/bin/ditto "$src/controller" "$dest/controller"
  /usr/bin/ditto "$src/desktop" "$dest/desktop"
  mkdir -p "$dest/app"
  /usr/bin/ditto "$src/zatiti_desktop.app" "$dest/app/zatiti_desktop.app"

  if [ "$dry_run" = 0 ]; then
    "$pack" apple --step codesign "$dest/controller/bin/zatiti-credential-helper" "$dest/controller/bin/zatiti" > /dev/null
    # Sign nested frameworks and libraries before the application itself.
    find "$dest/app/zatiti_desktop.app/Contents/Frameworks" -maxdepth 1 \( -name '*.framework' -o -name '*.dylib' \) -print 2>/dev/null |
      while IFS= read -r nested; do
        "$pack" apple --step codesign "$nested" > /dev/null
      done
    "$pack" apple --step codesign --entitlements "$entitlements" "$dest/app/zatiti_desktop.app" > /dev/null
    notarize_app "$dest/app/zatiti_desktop.app"
  fi

  bundle=$(/usr/bin/plutil -extract desktop.bundle raw -o - "$src/desktop.descriptor.json")
  mkdir -p "$(dirname "$dest/desktop/$bundle")"
  "$pack" assemble-bundle --dir "$dest/app" --archive "$dest/desktop/$bundle" > /dev/null

  for dist in controller desktop; do
    "$pack" assemble --root "$dest/$dist" --descriptor "$src/$dist.descriptor.json" --write > /dev/null
    "$pack" sign --manifest "$dest/$dist/manifest.json" --key "$release_key" > /dev/null
    "$pack" verify --manifest "$dest/$dist/manifest.json" --sig "$dest/$dist/manifest.sig.json" --trusted "$trusted" --tree "$dest/$dist" > /dev/null
    parts="$parts --part $dest/$dist"
    "$pack" assemble-bundle --dir "$dest/$dist" --archive "$dest/$dist.tar.gz" > /dev/null
    chmod 0600 "$dest/$dist.tar.gz"
  done
done

# shellcheck disable=SC2086 # parts is a deliberate word list of flags.
"$pack" mac-release $parts --trusted "$trusted" --key "$release_key" --out-dir "$out" > "$work/release-summary.json"
version=$(/usr/bin/plutil -extract version raw -o - "$work/release-summary.json")

assets=
for arch in amd64 arm64; do
  dest="$work/$arch"
  mkdir -p "$dest/pkg"
  "$pack" mac-pkg --release "$out/release.json" --arch "$arch" --sequence "$sequence" \
    --controller "$dest/controller.tar.gz" --desktop "$dest/desktop.tar.gz" --out-dir "$dest/pkg" > "$dest/pkg.json"
  stem="zatiti-$version-darwin-$arch"
  unsigned="$dest/pkg/$stem-installer.pkg"
  final="$out/assets/$stem-installer.pkg"
  if [ "$dry_run" = 1 ]; then
    cp "$unsigned" "$final"
  else
    "$pack" apple --step productsign --in "$unsigned" --out "$final" > /dev/null
    "$pack" apple --step notarize "$final" > "$work/notarize-$arch-installer.json"
    "$pack" apple --step staple "$final" > /dev/null
  fi
  cp "$dest/controller.tar.gz" "$out/assets/$stem-controller.tar.gz"
  cp "$dest/desktop.tar.gz" "$out/assets/$stem-desktop.tar.gz"
  for role in controller desktop; do
    assets="$assets --asset $arch/$role=$out/assets/$stem-$role.tar.gz"
  done
  assets="$assets --asset $arch/installer=$final"
done

# shellcheck disable=SC2086 # assets is a deliberate word list of flags.
"$pack" mac-delivery --release "$out/release.json" --release-sig "$out/release.sig.json" $assets \
  --base-url "$base_url" --sequence "$sequence" --key "$release_key" --out-dir "$out" > "$out/delivery-summary.json"

(cd "$out" && /usr/bin/shasum -a 256 release.json release.sig.json delivery.json delivery.sig.json assets/* > SHA256SUMS)
rm -rf "$work"
if [ "$dry_run" = 1 ]; then
  echo "unsigned dry run complete (not a release): $out"
else
  echo "signed and notarized Mac release set produced: $out"
fi
