// WorkspaceSettings: appearance, and the credential this client presents.

import 'package:flutter/material.dart';

import '../app/credential_store.dart';
import '../state/workspace_controller.dart';
import '../state/workspace_source.dart';
import 'theme.dart';
import 'widgets.dart';

/// Appearance follows the operating system unless the person chooses.
class AppSettings extends ChangeNotifier {
  ThemeMode _themeMode = ThemeMode.system;

  ThemeMode get themeMode => _themeMode;

  set themeMode(ThemeMode mode) {
    if (mode == _themeMode) return;
    _themeMode = mode;
    notifyListeners();
  }
}

Future<void> showWorkspaceSettings(
  BuildContext context, {
  required WorkspaceController controller,
  required AppSettings settings,
  required CredentialStore? credentials,
}) => showDialog<void>(
  context: context,
  barrierLabel: 'Close settings',
  builder: (_) => WorkspaceSettings(
    controller: controller,
    settings: settings,
    credentials: credentials,
  ),
);

class WorkspaceSettings extends StatefulWidget {
  const WorkspaceSettings({
    super.key,
    required this.controller,
    required this.settings,
    required this.credentials,
  });

  final WorkspaceController controller;
  final AppSettings settings;

  /// Null for the demo source, which has no credential.
  final CredentialStore? credentials;

  @override
  State<WorkspaceSettings> createState() => _WorkspaceSettingsState();
}

class _WorkspaceSettingsState extends State<WorkspaceSettings> {
  final TextEditingController _credential = TextEditingController();
  String? _credentialStatus;

  @override
  void initState() {
    super.initState();
    _describeStored();
  }

  Future<void> _describeStored() async {
    final store = widget.credentials;
    if (store == null) return;
    String status;
    try {
      status = await store.read() == null
          ? 'No credential is stored.'
          : 'A credential is stored. It is never shown.';
    } on Exception {
      status = 'Secure storage could not be read on this machine.';
    }
    if (mounted) setState(() => _credentialStatus = status);
  }

  Future<void> _save() async {
    final value = _credential.text.trim();
    if (value.isEmpty) return;
    try {
      await widget.credentials!.write(value);
      _credential.clear();
      await _describeStored();
      await widget.controller.reconnect();
    } on Exception {
      if (mounted) {
        setState(
          () => _credentialStatus =
              'Secure storage refused the credential. Nothing was saved.',
        );
      }
    }
  }

  Future<void> _remove() async {
    try {
      await widget.credentials!.delete();
    } on Exception {
      // Reported by the status line below.
    }
    await _describeStored();
  }

  @override
  void dispose() {
    _credential.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final text = Theme.of(context).textTheme;
    final demo = widget.controller.source.kind == SourceKind.demo;
    return Dialog(
      insetPadding: const EdgeInsets.all(Space.xl),
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 560),
        child: SingleChildScrollView(
          padding: const EdgeInsets.all(28),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Expanded(
                    child: Semantics(
                      header: true,
                      namesRoute: true,
                      child: Text('Your workspace', style: text.titleLarge),
                    ),
                  ),
                  IconButton(
                    tooltip: 'Close settings',
                    onPressed: () => Navigator.of(context).pop(),
                    icon: const Icon(Icons.close),
                  ),
                ],
              ),
              const SizedBox(height: Space.xl),
              const SectionLabel('Appearance'),
              ListenableBuilder(
                listenable: widget.settings,
                builder: (context, _) => SegmentedButton<ThemeMode>(
                  segments: const [
                    ButtonSegment(
                      value: ThemeMode.system,
                      label: Text('Match system'),
                    ),
                    ButtonSegment(value: ThemeMode.dark, label: Text('Dark')),
                    ButtonSegment(value: ThemeMode.light, label: Text('Light')),
                  ],
                  selected: {widget.settings.themeMode},
                  onSelectionChanged: (s) =>
                      widget.settings.themeMode = s.first,
                ),
              ),
              const SizedBox(height: Space.xl),
              const SectionLabel('When you close the app'),
              Text(
                'Authorized work continues on your running controller. Pause '
                'a responsibility to stop new work from starting.',
                style: text.bodySmall!.copyWith(fontSize: 13),
              ),
              const SizedBox(height: Space.xl),
              const SectionLabel('Who holds authority here'),
              _Principals(widget.controller),
              const SizedBox(height: Space.xl),
              const SectionLabel('Connection'),
              Text(widget.controller.source.label, style: text.bodyMedium),
              const SizedBox(height: Space.sm),
              if (demo)
                Text(
                  'Demo data has no controller and no credential.',
                  style: text.bodySmall,
                )
              else ...[
                Text(_credentialStatus ?? 'Checking…', style: text.bodySmall),
                const SizedBox(height: Space.md),
                Semantics(
                  label: 'Client credential',
                  child: TextField(
                    key: const ValueKey('credential-field'),
                    controller: _credential,
                    obscureText: true,
                    enableSuggestions: false,
                    autocorrect: false,
                    decoration: const InputDecoration(
                      hintText: 'Paste the credential for this client',
                    ),
                    onSubmitted: (_) => _save(),
                  ),
                ),
                const SizedBox(height: Space.md),
                Wrap(
                  spacing: Space.sm,
                  children: [
                    FilledButton(
                      key: const ValueKey('credential-save'),
                      onPressed: _save,
                      child: const Text('Save to secure storage'),
                    ),
                    TextButton(
                      onPressed: _remove,
                      child: const Text('Remove stored credential'),
                    ),
                  ],
                ),
                const SizedBox(height: Space.sm),
                Text(
                  'The credential goes to your operating system’s secure '
                  'storage and is sent only as the Authorization header to '
                  'your controller.',
                  style: text.bodySmall,
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }
}

/// The identities the controller authenticates, from `principal.list`. The
/// client reads them and never creates, changes or revokes one: identity
/// administration is not a desktop surface.
class _Principals extends StatelessWidget {
  const _Principals(this.controller);

  final WorkspaceController controller;

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: controller,
    builder: (context, _) {
      final text = Theme.of(context).textTheme;
      final principals = controller.snapshot.principals;
      if (principals.isEmpty) {
        return Text(
          'No identities have been read from your controller yet.',
          style: text.bodySmall,
        );
      }
      return Column(
        key: const ValueKey('principal-list'),
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          for (final p in principals)
            Padding(
              padding: const EdgeInsets.only(bottom: Space.sm),
              child: Semantics(
                label:
                    '${p.name}, ${p.kindLabel}'
                    '${p.revoked ? ', revoked' : ''}',
                child: ExcludeSemantics(
                  child: Row(
                    children: [
                      Expanded(child: Text(p.name, style: text.bodyMedium)),
                      Text(
                        p.revoked ? '${p.kindLabel} · revoked' : p.kindLabel,
                        style: text.bodySmall,
                      ),
                    ],
                  ),
                ),
              ),
            ),
          const SizedBox(height: Space.sm),
          Text(
            'Identities come from your controller. Add, change or revoke one '
            'on the machine that runs it; this app only shows them.',
            style: text.bodySmall,
          ),
        ],
      );
    },
  );
}
