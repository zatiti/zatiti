// The application widget: themes from the design tokens, following the
// operating system's appearance unless the person chooses otherwise.

import 'dart:async';

import 'package:flutter/material.dart';

import '../state/workspace_controller.dart';
import '../ui/theme.dart';
import '../ui/workspace_settings.dart';
import '../ui/workspace_shell.dart';
import 'credential_store.dart';
import 'startup.dart';

class ZatitiApp extends StatefulWidget {
  const ZatitiApp({
    super.key,
    required this.controller,
    required this.settings,
    this.credentials,
    this.pollEvery,
  });

  final WorkspaceController controller;
  final AppSettings settings;
  final CredentialStore? credentials;

  /// How often to replay events. Null disables polling (tests).
  final Duration? pollEvery;

  @override
  State<ZatitiApp> createState() => _ZatitiAppState();
}

class _ZatitiAppState extends State<ZatitiApp> {
  Timer? _poll;

  @override
  void initState() {
    super.initState();
    final every = widget.pollEvery;
    if (every != null) {
      _poll = Timer.periodic(every, (_) => widget.controller.poll());
    }
  }

  @override
  void dispose() {
    _poll?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: widget.settings,
    builder: (context, _) => MaterialApp(
      title: 'Zatiti',
      debugShowCheckedModeBanner: false,
      theme: buildZatitiTheme(Brightness.light),
      darkTheme: buildZatitiTheme(Brightness.dark),
      themeMode: widget.settings.themeMode,
      home: WorkspaceShell(
        controller: widget.controller,
        settings: widget.settings,
        credentials: widget.credentials,
      ),
    ),
  );
}

/// Shown when no controller is configured: what is missing and how to set it
/// up. The demo is offered only outside release builds.
class ConfigurationNeededApp extends StatelessWidget {
  const ConfigurationNeededApp({
    super.key,
    required this.plan,
    required this.onOpenDemo,
  });

  final NeedsConfiguration plan;
  final VoidCallback onOpenDemo;

  @override
  Widget build(BuildContext context) => MaterialApp(
    title: 'Zatiti',
    debugShowCheckedModeBanner: false,
    theme: buildZatitiTheme(Brightness.light),
    darkTheme: buildZatitiTheme(Brightness.dark),
    home: Builder(
      builder: (context) {
        final text = Theme.of(context).textTheme;
        return Scaffold(
          body: Center(
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 560),
              child: SingleChildScrollView(
                padding: const EdgeInsets.all(Space.xxl),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text('Connect to your controller', style: text.titleLarge),
                    const SizedBox(height: Space.md),
                    Text(
                      'Zatiti runs on your own controller. This app needs to '
                      'know where it is. Set these before starting the app:',
                      style: text.bodyMedium,
                    ),
                    const SizedBox(height: Space.lg),
                    for (final m in plan.missing)
                      Padding(
                        padding: const EdgeInsets.only(bottom: Space.sm),
                        child: Text('• $m', style: text.bodyMedium),
                      ),
                    const SizedBox(height: Space.md),
                    Text(
                      'The credential is not an environment variable. Add it '
                      'in the app’s settings; it is kept in your operating '
                      'system’s secure storage.',
                      style: text.bodySmall,
                    ),
                    if (plan.demoOffered) ...[
                      const SizedBox(height: Space.xl),
                      OutlinedButton(
                        key: const ValueKey('open-demo'),
                        onPressed: onOpenDemo,
                        child: const Text('Open the labeled demo instead'),
                      ),
                      const SizedBox(height: Space.sm),
                      Text(
                        'Development builds only. The demo uses fictional '
                        'data and says so on every screen.',
                        style: text.bodySmall,
                      ),
                    ],
                  ],
                ),
              ),
            ),
          ),
        );
      },
    ),
  );
}
