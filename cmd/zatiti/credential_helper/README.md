# Native Mac credential helper

`build.sh OUTPUT_DIRECTORY` builds an **unsigned**, native-architecture
`zatiti-credential-helper` by linking the existing Go `zatiti_carchive` bridge
with this standalone AppKit source. Run `verify.sh OUTPUT_DIRECTORY` after the
build for local parser and redacted-output checks. The executable accepts only
`capture --installation-id <lowercase UUID> --connection-id <lowercase UUID>`.
The raw provider key is entered through `NSSecureTextField` and passed only to
the linked Go `Commit` function; stdout carries one redacted JSON result.

This local build is not a distributable release artifact. The distribution
owner must bind its hash and native architecture in the controller manifest,
apply the fixed Developer ID identity and Runner launch constraint, verify
code signing and notarization, then test real Keychain access, cancellation,
recovery, and provider setup on clean Intel and Apple Silicon Macs.
