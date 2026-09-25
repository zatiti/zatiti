// WorkspaceSettings: appearance, and the credential this client presents.

import 'package:flutter/material.dart';

import '../app/credential_store.dart';
import '../app/installed_credential_capture.dart';
import '../api/models.dart' as wire;
import '../state/live_source.dart';
import '../state/workspace_controller.dart';
import '../state/workspace_source.dart';
import 'theme.dart';
import 'widgets.dart';
import 'provider_model_editor.dart';
import 'provider_connection_editor.dart';

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
  Future<List<wire.Connection>>? _connections;
  Future<List<wire.ProviderDescriptor>>? _providers;
  Future<List<wire.ExecutionProfile>>? _profiles;
  String? _providerKeyStatus;
  bool _capturingProviderKey = false;

  @override
  void initState() {
    super.initState();
    _describeStored();
    _refreshConnections();
    final source = widget.controller.source;
    if (source is LiveWorkspaceSource && source.installedMac) {
      _providers = source.api.modelProviders();
      _profiles = source.api.executionProfiles();
    }
  }

  void _refreshConnections() {
    final source = widget.controller.source;
    if (source is LiveWorkspaceSource && source.installedMac) {
      _connections = source.api.connections();
    }
  }

  Future<void> _captureProviderKey(wire.Connection connection) async {
    final source = widget.controller.source;
    if (source is! LiveWorkspaceSource ||
        !source.installedMac ||
        _capturingProviderKey) {
      return;
    }
    setState(() {
      _capturingProviderKey = true;
      _providerKeyStatus = null;
    });
    String status;
    try {
      final result = await const InstalledCredentialCapture().capture(
        installedMac: source.installedMac,
        installationId: source.installationId,
        connectionId: connection.id,
      );
      // A helper disposition is not proof that the provider is ready.
      final current = await source.api.connectionGet(connection.id);
      await widget.controller.reconnect();
      _refreshConnections();
      status = switch (result.status) {
        CredentialCaptureStatus.completed
            when current.validationState ==
                wire.ConnectionValidationState.valid =>
          'Provider connection validated.',
        CredentialCaptureStatus.completed =>
          'Key submitted. Provider connection is still ${current.validationState.name}.',
        CredentialCaptureStatus.cancelled => 'Key entry cancelled.',
        CredentialCaptureStatus.retryable =>
          'Key entry could not finish. Please try again.',
        CredentialCaptureStatus.repairRequired =>
          'Credential helper needs repair before key entry can continue.',
      };
    } on Exception {
      status = 'Key entry could not start. Check the local installation.';
    }
    if (mounted) {
      setState(() {
        _capturingProviderKey = false;
        _providerKeyStatus = status;
      });
    }
  }

  Future<void> _addProvider() async {
    final source = widget.controller.source;
    if (source is! LiveWorkspaceSource || !source.installedMac) return;
    final applied = await showProviderConnectionEditor(
      context,
      controller: widget.controller,
    );
    if (applied == true && mounted) {
      setState(() => _connections = source.api.connections());
    }
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
      // A new credential may name a different principal. This client has
      // no `identity.current` operation to confirm either way, so it never
      // takes the chance: every locally cached identity, selection and
      // fetched message is dropped before the reconnect that would
      // otherwise briefly show them under the new credential.
      await widget.controller.resetForCredentialChange();
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
    await widget.controller.resetForCredentialChange();
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
              else if (widget.credentials!.authorizationManaged) ...[
                const Text(
                  'This Mac keeps the owner credential in Keychain. Zatiti '
                  'reads it when connecting to the local service.',
                ),
                if (_connections != null) ...[
                  const SizedBox(height: Space.xl),
                  const SectionLabel('Provider connections'),
                  Align(
                    alignment: Alignment.centerLeft,
                    child: OutlinedButton.icon(
                      key: const ValueKey('add-provider'),
                      onPressed: _capturingProviderKey ? null : _addProvider,
                      icon: const Icon(Icons.add),
                      label: const Text('Add provider'),
                    ),
                  ),
                  FutureBuilder<List<wire.Connection>>(
                    future: _connections,
                    builder: (context, snapshot) {
                      if (snapshot.hasError) {
                        return const Text('Connections could not be loaded.');
                      }
                      if (!snapshot.hasData) {
                        return const Text('Loading connections…');
                      }
                      final connections = snapshot.data!;
                      if (connections.isEmpty) {
                        return const Text(
                          'No provider connections are configured yet.',
                        );
                      }
                      return Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          for (final connection in connections)
                            ListTile(
                              contentPadding: EdgeInsets.zero,
                              title: Text(connection.provider),
                              subtitle: Text(
                                '${connection.accountIdentity} · '
                                '${connection.validationState.name}',
                              ),
                              trailing: TextButton(
                                key: ValueKey(
                                  'capture-provider-${connection.id}',
                                ),
                                onPressed: _capturingProviderKey
                                    ? null
                                    : () => _captureProviderKey(connection),
                                child: const Text('Set API key'),
                              ),
                            ),
                        ],
                      );
                    },
                  ),
                  if (_providerKeyStatus != null)
                    Text(_providerKeyStatus!, style: text.bodySmall),
                  const SizedBox(height: Space.xl),
                  const SectionLabel('Provider and model profiles'),
                  FutureBuilder<List<wire.ProviderDescriptor>>(
                    future: _providers,
                    builder: (context, snapshot) {
                      if (snapshot.hasError) {
                        return const Text(
                          'Provider choices are unavailable from this controller.',
                        );
                      }
                      if (!snapshot.hasData) {
                        return const Text('Loading provider choices…');
                      }
                      final providers = snapshot.data!;
                      return Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          for (final provider in providers)
                            ListTile(
                              contentPadding: EdgeInsets.zero,
                              title: Text(provider.displayName),
                              subtitle: Text(
                                '${provider.defaultEndpoint} · ${provider.sessionMode}',
                              ),
                            ),
                        ],
                      );
                    },
                  ),
                  const SizedBox(height: Space.md),
                  FutureBuilder<List<wire.ExecutionProfile>>(
                    future: _profiles,
                    builder: (context, snapshot) {
                      if (snapshot.hasError) {
                        return const Text(
                          'Saved profiles could not be loaded.',
                        );
                      }
                      if (!snapshot.hasData) {
                        return const Text('Loading saved profiles…');
                      }
                      final profiles = snapshot.data!;
                      if (profiles.isEmpty) {
                        return const Text(
                          'No controller-configured model profiles are available.',
                        );
                      }
                      return Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          for (final profile in profiles)
                            ListTile(
                              contentPadding: EdgeInsets.zero,
                              title: Text(profile.model),
                              subtitle: Text(
                                '${profile.provider ?? 'Provider not reported'} · '
                                '${profile.contextCapture} context capture · '
                                'cost ${profile.costEnforcement ?? 'not reported'} · '
                                '${profile.costBound.format()}',
                              ),
                            ),
                          if (widget.controller.snapshot.installedChiefWorkerId
                              case final workerId?)
                            Align(
                              alignment: Alignment.centerLeft,
                              child: OutlinedButton.icon(
                                key: const ValueKey(
                                  'select-chief-model-profile',
                                ),
                                onPressed: () => showProviderModelEditor(
                                  context,
                                  controller: widget.controller,
                                  workerId: workerId,
                                ),
                                icon: const Icon(Icons.tune),
                                label: const Text(
                                  'Choose model for personal chief',
                                ),
                              ),
                            ),
                        ],
                      );
                    },
                  ),
                ],
              ] else ...[
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
                const SizedBox(height: Space.xl),
                _TlsProfileImport(credentials: widget.credentials!),
              ],
            ],
          ),
        ),
      ),
    );
  }
}

/// Imports the client certificate, private key and (optionally) a trusted
/// root bundle an explicitly configured remote controller needs for mutual
/// TLS. This is local file/text import only: no operation in the public
/// catalog provisions this client's own TLS material (`connection.setup.*`
/// is for third-party service connections, not this client's identity to
/// its own controller), so the material never leaves this dialog except
/// into secure storage. Pasting text, not a native file picker, keeps this
/// free of an unpinned file-picker plugin the desktop's AGENTS.md would
/// require a dependency-lock-report to add.
class _TlsProfileImport extends StatefulWidget {
  const _TlsProfileImport({required this.credentials});
  final CredentialStore credentials;

  @override
  State<_TlsProfileImport> createState() => _TlsProfileImportState();
}

class _TlsProfileImportState extends State<_TlsProfileImport> {
  final TextEditingController _certificate = TextEditingController();
  final TextEditingController _privateKey = TextEditingController();
  final TextEditingController _trustedRoots = TextEditingController();
  String? _status;

  @override
  void initState() {
    super.initState();
    _describeStored();
  }

  Future<void> _describeStored() async {
    final keys = widget.credentials.keys;
    final cert = await widget.credentials.readNamed(keys.clientCertificate);
    if (!mounted) return;
    setState(
      () => _status = cert == null
          ? 'No TLS profile is stored.'
          : 'A TLS profile is stored. Its key material is never shown.',
    );
  }

  Future<void> _import() async {
    final certificate = _certificate.text.trim();
    final privateKey = _privateKey.text.trim();
    if (certificate.isEmpty || privateKey.isEmpty) {
      setState(
        () => _status =
            'A client certificate and a private key are both required.',
      );
      return;
    }
    final keys = widget.credentials.keys;
    try {
      await widget.credentials.writeNamed(keys.clientCertificate, certificate);
      await widget.credentials.writeNamed(keys.clientPrivateKey, privateKey);
      final roots = _trustedRoots.text.trim();
      if (roots.isEmpty) {
        await widget.credentials.deleteNamed(keys.trustedRoots);
      } else {
        await widget.credentials.writeNamed(keys.trustedRoots, roots);
      }
      _certificate.clear();
      _privateKey.clear();
      _trustedRoots.clear();
      await _describeStored();
    } on Exception {
      if (mounted) {
        setState(
          () => _status =
              'Secure storage refused the TLS profile. Nothing was saved.',
        );
      }
    }
  }

  Future<void> _remove() async {
    final keys = widget.credentials.keys;
    try {
      await widget.credentials.deleteNamed(keys.clientCertificate);
      await widget.credentials.deleteNamed(keys.clientPrivateKey);
      await widget.credentials.deleteNamed(keys.trustedRoots);
    } on Exception {
      // Reported by the status line below.
    }
    await _describeStored();
  }

  @override
  void dispose() {
    _certificate.dispose();
    _privateKey.dispose();
    _trustedRoots.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final text = Theme.of(context).textTheme;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const SectionLabel('Remote connection (mutual TLS)'),
        const SizedBox(height: Space.sm),
        Text(
          'Only needed for an explicitly configured remote controller. A '
          'local controller on this computer does not use this.',
          style: text.bodySmall,
        ),
        const SizedBox(height: Space.sm),
        Text(_status ?? 'Checking…', style: text.bodySmall),
        const SizedBox(height: Space.md),
        Semantics(
          label: 'Client certificate',
          child: TextField(
            key: const ValueKey('tls-certificate-field'),
            controller: _certificate,
            maxLines: 3,
            enableSuggestions: false,
            autocorrect: false,
            decoration: const InputDecoration(
              hintText: 'Paste the client certificate (PEM)',
            ),
          ),
        ),
        const SizedBox(height: Space.sm),
        Semantics(
          label: 'Client private key',
          child: TextField(
            key: const ValueKey('tls-private-key-field'),
            controller: _privateKey,
            maxLines: 3,
            obscureText: true,
            enableSuggestions: false,
            autocorrect: false,
            decoration: const InputDecoration(
              hintText: 'Paste the client private key (PEM)',
            ),
          ),
        ),
        const SizedBox(height: Space.sm),
        Semantics(
          label: 'Trusted roots, optional',
          child: TextField(
            key: const ValueKey('tls-trusted-roots-field'),
            controller: _trustedRoots,
            maxLines: 3,
            enableSuggestions: false,
            autocorrect: false,
            decoration: const InputDecoration(
              hintText: 'Trusted root bundle (PEM), optional',
            ),
          ),
        ),
        const SizedBox(height: Space.md),
        Wrap(
          spacing: Space.sm,
          children: [
            FilledButton(
              key: const ValueKey('tls-profile-save'),
              onPressed: _import,
              child: const Text('Save TLS profile to secure storage'),
            ),
            TextButton(
              key: const ValueKey('tls-profile-remove'),
              onPressed: _remove,
              child: const Text('Remove stored TLS profile'),
            ),
          ],
        ),
        const SizedBox(height: Space.sm),
        Text(
          'This material goes to your operating system’s secure storage, '
          'exactly like the credential above, and is presented only to the '
          'remote controller you explicitly configure.',
          style: text.bodySmall,
        ),
      ],
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
