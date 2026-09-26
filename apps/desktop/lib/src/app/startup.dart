// Explicit development profiles come from the environment. An installed Mac
// reads the controller's protected, nonsecret discovery record by default.

import 'desktop_discovery.dart';
import '../api/controller_api.dart';
import '../transport/controller_client.dart';

/// `--dart-define=ZATITI_DEMO=true` selects the labeled demo source.
const bool demoFlag = bool.fromEnvironment('ZATITI_DEMO');

class ConnectionProfile {
  const ConnectionProfile({
    required this.profile,
    required this.installationId,
    this.socketPath,
    this.remoteUrl,
    this.keychainService,
    this.keychainAccount,
  });

  final String profile;
  final String installationId;

  /// The controller's private Unix socket. Exactly one of this and
  /// [remoteUrl] is set.
  final String? socketPath;

  /// An explicitly configured remote controller, https only.
  final Uri? remoteUrl;

  /// Nonsecret locator for the installed Mac owner's existing Keychain item.
  final String? keychainService;
  final String? keychainAccount;

  bool get installed => keychainService != null;
}

sealed class StartupPlan {
  const StartupPlan();
}

final class StartLive extends StartupPlan {
  const StartLive(this.profile);
  final ConnectionProfile profile;
}

final class StartDemo extends StartupPlan {
  const StartDemo();
}

/// Nothing is configured. The app explains what is missing and provides the
/// setup path. [demoOffered] is true only outside release builds.
final class NeedsConfiguration extends StartupPlan {
  const NeedsConfiguration(
    this.missing, {
    required this.demoOffered,
    this.issue,
  });
  final List<String> missing;
  final bool demoOffered;
  final StartupIssue? issue;
}

enum StartupIssue {
  missingDiscovery,
  unreadableDiscovery,
  malformedDiscovery,
  unsafeDiscovery,
  awaitingBootstrap,
  missingCredential,
  lockedKeychain,
  refusedKeychain,
  malformedCredential,
  staleSocket,
  identityMismatch,
  authenticationFailed,
}

const envSocket = 'ZATITI_SOCKET';
const envRemoteUrl = 'ZATITI_REMOTE_URL';
const envInstallation = 'ZATITI_INSTALLATION_ID';
const envProfile = 'ZATITI_PROFILE';

final RegExp _uuid = RegExp(
  r'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$',
);

StartupPlan resolveStartup({
  required Map<String, String> environment,
  required bool releaseMode,
  required bool demoRequested,
}) {
  if (demoRequested) return const StartDemo();

  String? value(String name) {
    final v = environment[name]?.trim();
    return v == null || v.isEmpty ? null : v;
  }

  final socket = value(envSocket);
  final remote = value(envRemoteUrl);
  final installation = value(envInstallation);
  final missing = <String>[];

  if (socket == null && remote == null) {
    missing.add(
      '$envSocket: the path of your controller’s private socket '
      '(or $envRemoteUrl for a remote controller over mutual TLS)',
    );
  }
  if (socket != null && remote != null) {
    missing.add('Set only one of $envSocket and $envRemoteUrl');
  }
  Uri? remoteUri;
  if (remote != null) {
    remoteUri = Uri.tryParse(remote);
    if (remoteUri == null || remoteUri.scheme != 'https') {
      missing.add('$envRemoteUrl must be an https URL');
    }
  }
  if (installation == null) {
    missing.add('$envInstallation: your installation’s identity');
  } else if (!_uuid.hasMatch(installation)) {
    missing.add('$envInstallation must be a lowercase UUID');
  }

  if (missing.isNotEmpty) {
    return NeedsConfiguration(missing, demoOffered: !releaseMode);
  }
  return StartLive(
    ConnectionProfile(
      profile: value(envProfile) ?? 'default',
      installationId: installation!,
      socketPath: socket,
      remoteUrl: remoteUri,
    ),
  );
}

/// Resolves one installed launch. Any explicit endpoint/installation override
/// keeps the existing advanced profile path and never mixes it with discovery.
Future<StartupPlan> resolveStartupPlan({
  required Map<String, String> environment,
  required bool releaseMode,
  required bool demoRequested,
  required bool macOS,
  DesktopDiscoveryReader? reader,
}) async {
  if (demoRequested ||
      !macOS ||
      [
        envSocket,
        envRemoteUrl,
        envInstallation,
      ].any((key) => environment[key]?.trim().isNotEmpty ?? false)) {
    return resolveStartup(
      environment: environment,
      releaseMode: releaseMode,
      demoRequested: demoRequested,
    );
  }
  final DesktopDiscovery? discovery;
  try {
    discovery = await (reader ?? DesktopDiscoveryReader()).read();
  } on DiscoveryException catch (e) {
    final issue = switch (e.kind) {
      DiscoveryFailure.unreadable => StartupIssue.unreadableDiscovery,
      DiscoveryFailure.malformed => StartupIssue.malformedDiscovery,
      DiscoveryFailure.unsafe => StartupIssue.unsafeDiscovery,
    };
    return NeedsConfiguration(
      [_messageFor(issue)],
      demoOffered: !releaseMode,
      issue: issue,
    );
  }
  if (discovery == null) {
    return NeedsConfiguration(
      [_messageFor(StartupIssue.missingDiscovery)],
      demoOffered: !releaseMode,
      issue: StartupIssue.missingDiscovery,
    );
  }
  if (!discovery.initialized) {
    return NeedsConfiguration(
      [_messageFor(StartupIssue.awaitingBootstrap)],
      demoOffered: !releaseMode,
      issue: StartupIssue.awaitingBootstrap,
    );
  }
  return StartLive(
    ConnectionProfile(
      profile: 'default',
      installationId: discovery.installationId!,
      socketPath: discovery.socketPath,
      keychainService: discovery.keychainService,
      keychainAccount: discovery.keychainAccount,
    ),
  );
}

NeedsConfiguration installedStartupFailure(
  StartupIssue issue, {
  required bool releaseMode,
}) => NeedsConfiguration(
  [_messageFor(issue)],
  demoOffered: !releaseMode,
  issue: issue,
);

/// Authenticated identity gate before any cached workspace state is opened.
Future<StartupIssue?> verifyInstalledController(
  ControllerClient client,
  String expectedInstallationId,
) async {
  final status = await ControllerApi(client).installationStatus();
  if (status.installationId != expectedInstallationId) {
    return StartupIssue.identityMismatch;
  }
  if (!status.initialized) return StartupIssue.awaitingBootstrap;
  return null;
}

String _messageFor(StartupIssue issue) => switch (issue) {
  StartupIssue.missingDiscovery =>
    'Zatiti’s local service has not published its connection yet.',
  StartupIssue.unreadableDiscovery =>
    'Zatiti cannot read its protected connection information.',
  StartupIssue.malformedDiscovery =>
    'Zatiti’s connection information is damaged or from an unsupported version.',
  StartupIssue.unsafeDiscovery =>
    'Zatiti’s connection information has unsafe permissions or location.',
  StartupIssue.awaitingBootstrap =>
    'The local service is ready for workspace setup.',
  StartupIssue.missingCredential =>
    'The owner credential is missing from Keychain. Repair the installation.',
  StartupIssue.lockedKeychain =>
    'Unlock your Mac Keychain, then reopen Zatiti.',
  StartupIssue.refusedKeychain =>
    'Keychain refused access to Zatiti’s owner credential.',
  StartupIssue.malformedCredential =>
    'The stored owner credential is damaged. Repair the installation.',
  StartupIssue.staleSocket =>
    'Zatiti’s local service is unavailable. Restart the service and retry.',
  StartupIssue.identityMismatch =>
    'The local service belongs to a different installation. Repair the connection.',
  StartupIssue.authenticationFailed =>
    'The local service refused the owner credential. Repair the installation.',
};
