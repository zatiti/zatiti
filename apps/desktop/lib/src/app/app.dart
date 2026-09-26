// The application widget: themes from the design tokens, following the
// operating system's appearance unless the person chooses otherwise.

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../state/workspace_controller.dart';
import '../ui/theme.dart';
import '../ui/workspace_settings.dart';
import '../ui/workspace_shell.dart';
import 'credential_store.dart';
import 'installed_bootstrap.dart';
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
    this.onInitialize,
    this.bootstrapView,
  });

  final NeedsConfiguration plan;
  final VoidCallback onOpenDemo;
  final Future<BootstrapView> Function(String)? onInitialize;
  final BootstrapView? bootstrapView;

  String get _title => switch (plan.issue) {
    StartupIssue.awaitingBootstrap => 'Welcome to Zatiti',
    StartupIssue.lockedKeychain => 'Unlock Keychain',
    StartupIssue.refusedKeychain ||
    StartupIssue.missingCredential ||
    StartupIssue.malformedCredential ||
    StartupIssue.authenticationFailed => 'Repair your owner credential',
    StartupIssue.staleSocket ||
    StartupIssue.missingDiscovery => 'Local service unavailable',
    StartupIssue.identityMismatch ||
    StartupIssue.unsafeDiscovery ||
    StartupIssue.malformedDiscovery ||
    StartupIssue.unreadableDiscovery => 'Repair the local connection',
    null => 'Connect to your controller',
  };

  String get _instruction => switch (plan.issue) {
    StartupIssue.awaitingBootstrap =>
      'Create your personal installation and chief to begin.',
    StartupIssue.lockedKeychain =>
      'Unlock your login Keychain, then reopen Zatiti.',
    StartupIssue.staleSocket || StartupIssue.missingDiscovery =>
      'Start the installed local service, then reopen Zatiti.',
    null =>
      'Zatiti runs on your own controller. This app needs to know where it is. Set these before starting the app:',
    _ => 'Review the repair detail below, then reopen Zatiti.',
  };

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
                    Semantics(
                      header: true,
                      child: Text(_title, style: text.titleLarge),
                    ),
                    const SizedBox(height: Space.md),
                    Text(_instruction, style: text.bodyMedium),
                    const SizedBox(height: Space.lg),
                    for (final m in plan.missing)
                      Padding(
                        padding: const EdgeInsets.only(bottom: Space.sm),
                        child: Semantics(
                          label: m,
                          child: ExcludeSemantics(
                            child: Text('• $m', style: text.bodyMedium),
                          ),
                        ),
                      ),
                    const SizedBox(height: Space.md),
                    if (plan.issue == null)
                      Text(
                        'The credential is not an environment variable. Add it '
                        'in the app’s settings; it is kept in your operating '
                        'system’s secure storage.',
                        style: text.bodySmall,
                      ),
                    if (onInitialize != null)
                      InstalledBootstrapForm(
                        onInitialize: onInitialize!,
                        view: bootstrapView,
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

class InstalledBootstrapForm extends StatefulWidget {
  const InstalledBootstrapForm({
    super.key,
    required this.onInitialize,
    this.view,
  });
  final Future<BootstrapView> Function(String) onInitialize;
  final BootstrapView? view;

  @override
  State<InstalledBootstrapForm> createState() => _InstalledBootstrapFormState();
}

class _InstalledBootstrapFormState extends State<InstalledBootstrapForm> {
  final TextEditingController _name = TextEditingController();
  final FocusNode _nameFocus = FocusNode(debugLabel: 'owner name');
  final FocusNode _actionFocus = FocusNode(debugLabel: 'setup action');
  bool _busy = false;
  BootstrapView? _result;

  @override
  void initState() {
    super.initState();
    _name.text = widget.view?.ownerName ?? '';
  }

  @override
  void dispose() {
    _name.dispose();
    _nameFocus.dispose();
    _actionFocus.dispose();
    super.dispose();
  }

  @override
  void didUpdateWidget(covariant InstalledBootstrapForm oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.view != widget.view) {
      _result = null;
      if (_name.text.isEmpty && widget.view?.ownerName != null) {
        _name.text = widget.view!.ownerName!;
      }
    }
  }

  Future<void> _submit() async {
    if (_busy) return;
    setState(() => _busy = true);
    final result = await widget.onInitialize(_name.text);
    if (mounted) {
      setState(() {
        _busy = false;
        _result = result;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final state = (_result ?? widget.view)?.state ?? BootstrapState.fresh;
    final canSubmit =
        state == BootstrapState.fresh ||
        state == BootstrapState.retryable ||
        state == BootstrapState.invalidName ||
        state == BootstrapState.refused;
    final canCheck =
        state == BootstrapState.pending || state == BootstrapState.unavailable;
    final notice = switch (state) {
      BootstrapState.pending =>
        'Setup may already have completed. Zatiti will check the local service on the next launch. Do not start setup again.',
      BootstrapState.retryable =>
        'The service could not be reached before setup was sent. You can retry with the same name.',
      BootstrapState.unavailable =>
        'The local service is unavailable. Restart it and reopen Zatiti.',
      BootstrapState.storageUnavailable =>
        'Secure storage is unavailable. Unlock Keychain and reopen Zatiti.',
      BootstrapState.invalidName => 'Enter a name to continue.',
      BootstrapState.refused =>
        'The local service refused setup. Check its status before retrying.',
      BootstrapState.ready => 'Setup completed. Opening your workspace…',
      BootstrapState.fresh =>
        'This creates your local installation and personal chief once.',
    };
    return CallbackShortcuts(
      bindings: {
        const SingleActivator(LogicalKeyboardKey.escape): () =>
            FocusManager.instance.primaryFocus?.unfocus(),
      },
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const SizedBox(height: Space.md),
          TextField(
            key: const ValueKey('installed-owner-name'),
            controller: _name,
            focusNode: _nameFocus,
            autofocus: canSubmit,
            enabled: canSubmit && !_busy,
            maxLength: 8192,
            decoration: const InputDecoration(labelText: 'Your name'),
            onSubmitted: (_) {
              if (canSubmit && !_busy) _submit();
            },
          ),
          const SizedBox(height: Space.sm),
          Semantics(liveRegion: true, child: Text(notice)),
          const SizedBox(height: Space.md),
          FilledButton(
            key: const ValueKey('installed-initialize'),
            focusNode: _actionFocus,
            autofocus: canCheck,
            onPressed: (canSubmit || canCheck) && !_busy ? _submit : null,
            child: Text(
              _busy
                  ? 'Checking…'
                  : canCheck
                  ? 'Check setup status'
                  : 'Set up Zatiti',
            ),
          ),
        ],
      ),
    );
  }
}
