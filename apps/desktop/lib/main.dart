import 'dart:convert';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import 'src/app/app.dart';
import 'src/app/credential_store.dart';
import 'src/app/desktop_discovery.dart';
import 'src/app/installed_bootstrap.dart';
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

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  await runZatiti(
    environment: Platform.environment,
    releaseMode: kReleaseMode,
    demoRequested: demoFlag,
    macOS: Platform.isMacOS,
  );
}

/// Testable launch decision; the installed reader is injectable in fixtures.
Future<void> runZatiti({
  required Map<String, String> environment,
  required bool releaseMode,
  required bool demoRequested,
  required bool macOS,
  DesktopDiscoveryReader? discoveryReader,
}) async {
  final plan = await resolveStartupPlan(
    environment: environment,
    releaseMode: releaseMode,
    demoRequested: demoRequested,
    macOS: macOS,
    reader: discoveryReader,
  );
  runApp(
    _Root(
      plan: plan,
      environment: environment,
      releaseMode: releaseMode,
      demoRequested: demoRequested,
      macOS: macOS,
      discoveryReader: discoveryReader,
    ),
  );
}

class _Root extends StatefulWidget {
  const _Root({
    required this.plan,
    required this.environment,
    required this.releaseMode,
    required this.demoRequested,
    required this.macOS,
    this.discoveryReader,
  });
  final StartupPlan plan;
  final Map<String, String> environment;
  final bool releaseMode;
  final bool demoRequested;
  final bool macOS;
  final DesktopDiscoveryReader? discoveryReader;

  @override
  State<_Root> createState() => _RootState();
}

class _RootState extends State<_Root> {
  final AppSettings _settings = AppSettings();
  late StartupPlan _plan = widget.plan;
  WorkspaceController? _controller;
  CredentialStore? _credentials;
  String? _startupError;
  late final InstalledBootstrap _bootstrap = InstalledBootstrap(
    reader: widget.discoveryReader ?? DesktopDiscoveryReader(),
  );
  BootstrapView? _bootstrapView;

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
      case NeedsConfiguration(:final issue):
        if (issue == StartupIssue.awaitingBootstrap) {
          final view = await _bootstrap.inspect();
          if (!mounted) return;
          if (view.state == BootstrapState.ready) {
            await _refreshInstalled();
          } else {
            setState(() => _bootstrapView = view);
          }
        }
        return;
      case StartDemo():
        source = DemoWorkspaceSource();
      case StartLive(:final profile):
        final CredentialStore store = profile.installed
            ? MacOwnerCredentialStore(
                service: profile.keychainService!,
                account: profile.keychainAccount!,
                profile: profile.profile,
              )
            : SecureCredentialStore(profile.profile);
        _credentials = store;
        installationId = profile.installationId;
        final keys = CredentialKeys(profile.profile);
        final ControllerClient client;
        try {
          if (profile.installed &&
              !await isSafeLiveSocket(profile.socketPath!)) {
            _showInstalledFailure(StartupIssue.staleSocket);
            return;
          }
          if (profile.installed) await store.read();
          client = ControllerClient(
            endpoint: await _endpoint(profile, store),
            installationId: profile.installationId,
            credentials: store.read,
          );
          // This query authenticates with the Keychain item. No cached or
          // controller-derived workspace state is shown until it agrees with
          // the protected discovery record.
          if (profile.installed) {
            final issue = await verifyInstalledController(
              client,
              profile.installationId,
            );
            if (issue != null) {
              _showInstalledFailure(issue);
              return;
            }
          }
        } on MacCredentialException catch (e) {
          _showInstalledFailure(switch (e.kind) {
            MacCredentialFailure.missing => StartupIssue.missingCredential,
            MacCredentialFailure.locked => StartupIssue.lockedKeychain,
            MacCredentialFailure.refused => StartupIssue.refusedKeychain,
            MacCredentialFailure.malformed => StartupIssue.malformedCredential,
          });
          return;
        } on ControllerUnavailableException {
          _showInstalledFailure(StartupIssue.staleSocket);
          return;
        } on OperationFailedException {
          _showInstalledFailure(StartupIssue.authenticationFailed);
          return;
        } on InvalidRequestException catch (e) {
          if (profile.installed) {
            _showInstalledFailure(StartupIssue.malformedDiscovery);
          } else if (mounted) {
            setState(() => _startupError = e.message);
          }
          return;
        } on SocketException {
          _showInstalledFailure(StartupIssue.staleSocket);
          return;
        } on FileSystemException {
          _showInstalledFailure(StartupIssue.staleSocket);
          return;
        } on Object {
          if (profile.installed) rethrow;
          if (mounted) {
            setState(
              () => _startupError = 'The controller could not be opened.',
            );
          }
          return;
        }
        // Read once, synchronously into local variables, so construction
        // stays as it was: everything the controller and the live source
        // need to resume across a restart is ready before either exists.
        final fileStore = FileLocalStore(profile.profile);
        localStore = fileStore;
        initialLocal = await fileStore.read(profile.installationId);
        final String? draftsJson;
        try {
          draftsJson = await store.readNamed(keys.drafts);
        } on PlatformException catch (e) {
          if (profile.installed) {
            _showInstalledFailure(
              e.details == -25308
                  ? StartupIssue.lockedKeychain
                  : StartupIssue.refusedKeychain,
            );
            return;
          }
          rethrow;
        }
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
            client,
            installedMac: profile.installed,
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

  void _showInstalledFailure(StartupIssue issue) {
    if (!mounted) return;
    setState(
      () => _plan = installedStartupFailure(issue, releaseMode: kReleaseMode),
    );
  }

  Future<void> _refreshInstalled() async {
    final plan = await resolveStartupPlan(
      environment: widget.environment,
      releaseMode: widget.releaseMode,
      demoRequested: widget.demoRequested,
      macOS: widget.macOS,
      reader: widget.discoveryReader,
    );
    if (!mounted) return;
    setState(() {
      _plan = plan;
      _bootstrapView = null;
    });
    if (plan is StartLive) await _open();
  }

  Future<BootstrapView> _initializeInstalled(String ownerName) async {
    final view = await _bootstrap.initialize(ownerName);
    if (!mounted) return view;
    if (view.state == BootstrapState.ready) {
      await _refreshInstalled();
    } else {
      setState(() => _bootstrapView = view);
    }
    return view;
  }

  Future<ControllerEndpoint> _endpoint(
    ConnectionProfile profile,
    CredentialStore store,
  ) async {
    final socket = profile.socketPath;
    if (socket != null) return LocalSocketEndpoint(socket);
    final keys = CredentialKeys(profile.profile);
    final certificate = await store.readNamed(keys.clientCertificate);
    final privateKey = await store.readNamed(keys.clientPrivateKey);
    final roots = await store.readNamed(keys.trustedRoots);
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
      onInitialize:
          plan is NeedsConfiguration &&
              plan.issue == StartupIssue.awaitingBootstrap
          ? _initializeInstalled
          : null,
      bootstrapView: _bootstrapView,
    );
  }
}
