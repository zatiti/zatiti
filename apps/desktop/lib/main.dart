import 'dart:convert';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';

import 'src/app/app.dart';
import 'src/app/credential_store.dart';
import 'src/app/local_store.dart';
import 'src/app/startup.dart';
import 'src/state/demo_source.dart';
import 'src/state/live_source.dart';
import 'src/state/workspace_controller.dart';
import 'src/state/workspace_source.dart';
import 'src/transport/controller_client.dart';
import 'src/transport/endpoint.dart';
import 'src/transport/errors.dart';
import 'src/ui/workspace_settings.dart';

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  final plan = resolveStartup(
    environment: Platform.environment,
    releaseMode: kReleaseMode,
    demoRequested: demoFlag,
  );
  runApp(_Root(plan: plan));
}

class _Root extends StatefulWidget {
  const _Root({required this.plan});
  final StartupPlan plan;

  @override
  State<_Root> createState() => _RootState();
}

class _RootState extends State<_Root> {
  final AppSettings _settings = AppSettings();
  late StartupPlan _plan = widget.plan;
  WorkspaceController? _controller;
  CredentialStore? _credentials;
  String? _startupError;

  @override
  void initState() {
    super.initState();
    _open();
  }

  Future<void> _open() async {
    // Never call setState from inside initState's own frame.
    await Future<void>.value();
    if (!mounted) return;
    final plan = _plan;
    WorkspaceSource? source;
    LocalStore localStore = MemoryLocalStore();
    String? installationId;
    LocalState initialLocal = LocalState.empty;
    Map<String, String> initialDrafts = const {};
    Future<void> Function(Map<String, String> drafts)? persistDrafts;
    switch (plan) {
      case NeedsConfiguration():
        return;
      case StartDemo():
        source = DemoWorkspaceSource();
      case StartLive(:final profile):
        final store = SecureCredentialStore(profile.profile);
        _credentials = store;
        installationId = profile.installationId;
        final keys = CredentialKeys(profile.profile);
        // Read once, synchronously into local variables, so construction
        // stays as it was: everything the controller and the live source
        // need to resume across a restart is ready before either exists.
        final fileStore = FileLocalStore(profile.profile);
        localStore = fileStore;
        initialLocal = await fileStore.read(profile.installationId);
        final draftsJson = await store.readNamed(keys.drafts);
        if (draftsJson != null) {
          try {
            initialDrafts = (jsonDecode(draftsJson) as Map<String, Object?>)
                .map((k, v) => MapEntry(k, v! as String));
          } on FormatException {
            // Corrupt drafts blob: start with none rather than fail startup.
          }
        }
        persistDrafts = (drafts) =>
            store.writeNamed(keys.drafts, jsonEncode(drafts));
        try {
          source = LiveWorkspaceSource(
            ControllerClient(
              endpoint: await _endpoint(profile, store),
              installationId: profile.installationId,
              credentials: store.read,
            ),
            endpointLabel: profile.socketPath != null
                ? 'Your controller on this computer'
                : 'Your controller at ${profile.remoteUrl!.host}',
            initialEventCursor: initialLocal.eventCursor,
            initialLastSequence: initialLocal.lastSequence,
            localStore: localStore,
          );
        } on InvalidRequestException catch (e) {
          if (mounted) setState(() => _startupError = e.message);
          return;
        }
        if (!mounted) return;
    }
    final controller = WorkspaceController(
      source,
      localStore: localStore,
      installationId: installationId,
      initialLocal: initialLocal,
      initialDrafts: initialDrafts,
      persistDrafts: persistDrafts,
    );
    setState(() => _controller = controller);
    await controller.start();
  }

  Future<ControllerEndpoint> _endpoint(
    ConnectionProfile profile,
    SecureCredentialStore store,
  ) async {
    final socket = profile.socketPath;
    if (socket != null) return LocalSocketEndpoint(socket);
    final keys = CredentialKeys(profile.profile);
    final certificate = await store.readPem(keys.clientCertificate);
    final privateKey = await store.readPem(keys.clientPrivateKey);
    final roots = await store.readPem(keys.trustedRoots);
    if (certificate == null || privateKey == null) {
      throw const InvalidRequestException(
        'The client certificate and private key for the remote controller '
        'are not in secure storage.',
      );
    }
    return RemoteTlsEndpoint(
      url: profile.remoteUrl!,
      tls: MutualTlsMaterial(
        clientCertificateChainPem: certificate.codeUnits,
        clientPrivateKeyPem: privateKey.codeUnits,
        trustedRootsPem: roots?.codeUnits,
      ),
    );
  }

  @override
  void dispose() {
    _controller?.dispose();
    _settings.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final controller = _controller;
    if (controller != null) {
      return ZatitiApp(
        controller: controller,
        settings: _settings,
        credentials: _credentials,
        pollEvery: const Duration(seconds: 5),
      );
    }
    final plan = _plan;
    return ConfigurationNeededApp(
      plan: plan is NeedsConfiguration
          ? plan
          : NeedsConfiguration([
              _startupError ?? 'Starting…',
            ], demoOffered: !kReleaseMode),
      onOpenDemo: () {
        if (kReleaseMode) return;
        setState(() => _plan = const StartDemo());
        _open();
      },
    );
  }
}
