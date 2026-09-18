import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';

import 'src/app/app.dart';
import 'src/app/credential_store.dart';
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
    final plan = _plan;
    WorkspaceSource? source;
    switch (plan) {
      case NeedsConfiguration():
        return;
      case StartDemo():
        source = DemoWorkspaceSource();
      case StartLive(:final profile):
        final store = SecureCredentialStore(profile.profile);
        _credentials = store;
        try {
          source = LiveWorkspaceSource(
            ControllerClient(
              endpoint: await _endpoint(profile, store),
              installationId: profile.installationId,
              credentials: store.read,
            ),
            principalId: profile.principalId,
            endpointLabel: profile.socketPath != null
                ? 'Your controller on this computer'
                : 'Your controller at ${profile.remoteUrl!.host}',
          );
        } on InvalidRequestException catch (e) {
          setState(() => _startupError = e.message);
          return;
        }
    }
    final controller = WorkspaceController(source);
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
