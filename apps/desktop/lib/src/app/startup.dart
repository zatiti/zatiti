// Startup configuration. The connection profile comes from the environment;
// secrets come from secure storage. The demo source is never the default in
// a release build: it needs the explicit compile-time flag there.

/// `--dart-define=ZATITI_DEMO=true` selects the labeled demo source.
const bool demoFlag = bool.fromEnvironment('ZATITI_DEMO');

class ConnectionProfile {
  const ConnectionProfile({
    required this.profile,
    required this.installationId,
    this.socketPath,
    this.remoteUrl,
  });

  final String profile;
  final String installationId;

  /// The controller's private Unix socket. Exactly one of this and
  /// [remoteUrl] is set.
  final String? socketPath;

  /// An explicitly configured remote controller, https only.
  final Uri? remoteUrl;
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
  const NeedsConfiguration(this.missing, {required this.demoOffered});
  final List<String> missing;
  final bool demoOffered;
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
