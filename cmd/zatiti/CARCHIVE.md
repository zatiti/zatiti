# macOS in-process credential bridge

Build separately on each release architecture with the release Go toolchain:

```sh
CGO_ENABLED=1 go build -tags zatiti_carchive -buildmode=c-archive -o "$OUT/libzatiti_helper.a" ./cmd/zatiti
```

The generated `libzatiti_helper.h` declares `ZatitiPrepare`, `ZatitiCommit`, and `ZatitiCancel`. Statically link the archive into the standalone signed AppKit helper. The helper invokes these functions in its own process; it passes only the two UUIDs through its process arguments. `ZatitiCommit` receives the secure field's UTF-8 bytes directly as a pointer and length (1–4096 bytes), never through argv, environment, a method channel, or public operation JSON. The AppKit caller must zero its own credential buffer after the call; the bridge zeroes its bounded Go copy.

Each function writes compact JSON to a caller-owned output buffer (at most 16 KiB) and returns its byte count. A negative return indicates an invalid or insufficient output buffer. No NUL terminator is written. `Prepare` returns a one-use, process-local handle plus nonsecret provider/account labels. `Commit` and `Cancel` consume that handle. `reason_code` values are fixed diagnostics and carry no credential, receipt, or Keychain reference.

The bridge resolves only the default installed Mac state and `desktop.json`, opens Keychain custody using `secret:master`, validates the owner locator and authenticated controller identity, and uses the shared terminal receipt engine. This build command demonstrates archive generation; signing, notarization, Keychain access, Intel/Apple Silicon native execution, and AppKit presentation require separate release qualification.
