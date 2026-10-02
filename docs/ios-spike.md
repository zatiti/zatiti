# iOS feasibility spike

Status: exploratory, 2026-09-27. This is not an iOS release plan and not
qualification evidence. The iOS target, the Mac release and every release
gate remain as described in `docs/launch-readiness.md`.

## What worked

- `flutter create --platforms=ios` added an iOS runner to `apps/desktop`
  without changing any Dart code outside one storage path.
- The iOS simulator debug build (`flutter build ios --simulator --debug`)
  succeeded. The macOS debug build still succeeds, and all 256 Flutter tests
  still pass.
- The synthetic demo (`--dart-define=ZATITI_DEMO=true`) runs on an iPhone 17
  Pro simulator through `flutter run`. The conversation, decision card and
  composer render and are readable at phone width.
- The shell is already responsive:
  - Below 820 logical pixels the sidebar becomes a drawer opened from the
    conversation header.
  - Below 1180 pixels the worker details panel becomes an overlay, full width
    when the window is narrower than the panel.
  - An iPhone (about 400 pixels wide) takes those paths without code changes.
- Controller discovery is already skipped outside macOS. `resolveStartupPlan`
  reads `desktop.json` only when `macOS` is true, so the Unix socket, the
  `HOME/Library` discovery path and the `id` and `stat` subprocess calls are
  never reached on iOS.

## Code changes in this spike

- `lib/src/app/local_store.dart`: added an `ios` case. The home directory on
  iOS is the app's sandbox container, where only `Library` and `Documents`
  are writable. Without this case, local drafts would fall back to
  `$HOME/.zatiti`, which isn't writable. No dependency was added
  (`path_provider` wasn't needed).
- `ios/`: the generated runner, with bundle ID `dev.zatiti.zatitiDesktop`.
  `.metadata` records the iOS platform.

## What breaks on a phone

- **Safe area:** the demo banner draws under the status bar and the Dynamic
  Island, so its first line is partly hidden (screenshot `02-demo-home.png`).
  The shell uses `SafeArea` only in places. It needs a top inset on the
  banner and header, and a bottom inset for the home indicator under the
  composer.
- **Sidebar drawer:** it is a fixed 302 pixels wide, so it covers about 75% of
  a phone screen. A phone-sized navigation stack fits better than a desktop
  drawer.
- **Worker details:** the panel becomes a full-screen overlay, which works.
  It is still desktop-shaped: dense two-column rows, and dialogs sized for a
  window.
- **Dialogs:** creation, task, memory and provider dialogs are sized for a
  desktop window. They need full-screen sheets on a phone.
- **Input:** hover affordances and keyboard shortcuts have no touch
  equivalent. The composer doesn't account for the on-screen keyboard beyond
  Flutter's default resize.
- **Not verified:** the drawer, the details overlay and the dialogs were not
  captured, because no simulator tap tool is installed. The points above for
  those screens come from reading the layout code.

## Code tied to macOS

- `desktop_discovery.dart` handles local controller discovery through the Unix
  socket, `stat` and `id`. It is gated at startup, but it has no iOS
  replacement. A phone can't reach a controller over a Unix socket.
- `voice_mode.dart` uses `MethodChannel('zatiti/voice')`, which only the
  macOS runner implements in `MainFlutterWindow.swift`. On iOS every voice
  call would fail with `MissingPluginException`. Voice needs a Swift
  implementation using `AVAudioSession`, or voice hidden on iOS.
- `installed_credential_capture.dart` uses a native channel that only the
  macOS installer flow provides.
- The keychain storage `flutter_secure_storage` supports iOS. The entitlement
  and access-group settings still need checking.

## Work for a real iOS client (estimate)

1. **Remote controller access (largest).** The client already has a
   mutual-TLS endpoint (`RemoteTlsEndpoint`). The work is:
   - A pairing flow: the Mac shows a QR code that carries a one-time
     enrollment token and the pinned root certificate.
   - A controller operation that issues and revokes a client certificate for
     each device.
   - Keychain storage of the client identity.
   - A decision on how the phone reaches the Mac off the local network (VPN
     or relay).
   - An ADR, and a spec change through `tools/specgen`.
   - About 2 to 3 weeks, plus the reachability decision.
2. **Compact layout.** Safe areas; a navigation stack instead of the drawer;
   full-screen sheets for dialogs; touch targets; keyboard avoidance. Scope
   the first release to chat, the "Needs you" queue and task status. About 1
   to 2 weeks.
3. **Background and reconnect.** iOS suspends sockets when the app moves to
   the background. Reuse the snapshot, replay and submission-key semantics,
   and test resume after suspension. About 1 week.
4. **Push notifications (optional).** Delivering "Needs you" through Apple's
   push service needs a hosted relay that holds Apple credentials. That is a
   new hosted component, so it needs its own ADR. About 2 weeks.
5. **Voice on iOS (optional).** A Swift `AVAudioSession` implementation of
   the voice channel, with microphone permission strings. About 1 week.
6. **Distribution.** Signing, a privacy manifest, TestFlight, and an App
   Store review path through the demo mode. Platform qualification separate
   from the Mac. About 1 week.

Suggested order: remote-access ADR, then compact layout, then background and
reconnect, then TestFlight. Push and voice come after that. This work starts
after the Mac first-chat gate passes.
