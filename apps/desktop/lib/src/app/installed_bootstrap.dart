// The installed Mac's one-time, unauthenticated bootstrap. A durable intent
// prevents an ambiguous response or process crash from minting another owner.

import 'dart:convert';

import 'credential_store.dart';
import 'desktop_discovery.dart';
import '../transport/controller_client.dart';
import '../transport/endpoint.dart';
import '../transport/envelope.dart';
import '../transport/errors.dart';

const _intentKey = 'zatiti.mac.bootstrap.pending-init';

enum BootstrapState {
  fresh,
  retryable,
  pending,
  ready,
  unavailable,
  storageUnavailable,
  invalidName,
  refused,
}

class BootstrapView {
  const BootstrapView(this.state, {this.ownerName});
  final BootstrapState state;
  final String? ownerName;
}

class _Intent {
  const _Intent(this.name, this.phase);
  final String name;
  final String phase;

  String encode() => jsonEncode({'owner_name': name, 'phase': phase});

  static _Intent decode(String value) {
    final data = jsonDecode(value);
    if (data is! Map<String, dynamic> ||
        data.length != 2 ||
        data['owner_name'] is! String ||
        !{
          'in_flight',
          'not_sent',
          'unknown',
          'awaiting_publication',
        }.contains(data['phase'])) {
      throw const FormatException('invalid bootstrap intent');
    }
    return _Intent(data['owner_name'] as String, data['phase'] as String);
  }
}

class InstalledBootstrap {
  InstalledBootstrap({
    required this.reader,
    CredentialStore? storage,
    Future<void> Function(String socketPath, String ownerName)? send,
  }) : _storage = storage ?? SecureCredentialStore('mac-bootstrap'),
       _send = send;

  final DesktopDiscoveryReader reader;
  final CredentialStore _storage;
  final Future<void> Function(String socketPath, String ownerName)? _send;
  bool _busy = false;

  Future<_Intent?> _readIntent() async {
    final value = await _storage.readNamed(_intentKey);
    return value == null ? null : _Intent.decode(value);
  }

  Future<void> _writeIntent(_Intent intent) =>
      _storage.writeNamed(_intentKey, intent.encode());

  Future<BootstrapView> inspect() async {
    try {
      final discovery = await reader.read();
      if (discovery?.initialized == true) {
        await _storage.deleteNamed(_intentKey);
        return const BootstrapView(BootstrapState.ready);
      }
      if (discovery == null || !await isSafeLiveSocket(discovery.socketPath)) {
        return const BootstrapView(BootstrapState.unavailable);
      }
      final intent = await _readIntent();
      if (intent == null) return const BootstrapView(BootstrapState.fresh);
      if (intent.phase == 'not_sent') {
        return BootstrapView(BootstrapState.retryable, ownerName: intent.name);
      }
      return BootstrapView(BootstrapState.pending, ownerName: intent.name);
    } on DiscoveryException {
      return const BootstrapView(BootstrapState.unavailable);
    } on FormatException {
      return const BootstrapView(BootstrapState.pending);
    } on Exception {
      return const BootstrapView(BootstrapState.storageUnavailable);
    }
  }

  /// Exactly one network attempt. A proven pre-send failure can be retried by
  /// an explicit later action; every ambiguous state only re-reads discovery.
  Future<BootstrapView> initialize(String ownerName) async {
    if (_busy) return const BootstrapView(BootstrapState.pending);
    _busy = true;
    try {
      final name = ownerName.trim();
      final current = await inspect();
      if (current.state == BootstrapState.ready ||
          current.state == BootstrapState.pending ||
          current.state == BootstrapState.unavailable ||
          current.state == BootstrapState.storageUnavailable) {
        return current;
      }
      if (name.isEmpty || name.runes.length > 8192) {
        return const BootstrapView(BootstrapState.invalidName);
      }
      if (current.state == BootstrapState.retryable &&
          current.ownerName != name) {
        return current;
      }
      final discovery = await reader.read();
      if (discovery == null ||
          discovery.initialized ||
          !await isSafeLiveSocket(discovery.socketPath)) {
        return await inspect();
      }
      // If the secure-storage write fails, no request is sent.
      await _writeIntent(_Intent(name, 'in_flight'));
      try {
        if (_send != null) {
          await _send(discovery.socketPath, name);
        } else {
          final client = ControllerClient(
            endpoint: LocalSocketEndpoint(discovery.socketPath),
            installationId: '00000000-0000-0000-0000-000000000000',
            credentials: () async => null,
          );
          await client.bootstrapInstallation(name);
        }
        await _writeIntent(_Intent(name, 'awaiting_publication'));
        return await inspect();
      } on ControllerUnavailableException {
        await _writeIntent(_Intent(name, 'not_sent'));
        return BootstrapView(BootstrapState.retryable, ownerName: name);
      } on OutcomeUnknownException {
        await _writeIntent(_Intent(name, 'unknown'));
        return await inspect();
      } on OperationFailedException catch (e) {
        if (e.fault.code == FaultCode.conflict) {
          await _writeIntent(_Intent(name, 'unknown'));
          return await inspect();
        }
        await _storage.deleteNamed(_intentKey);
        return const BootstrapView(BootstrapState.refused);
      }
    } on DiscoveryException {
      return const BootstrapView(BootstrapState.unavailable);
    } on FormatException {
      return const BootstrapView(BootstrapState.pending);
    } on Exception {
      return const BootstrapView(BootstrapState.storageUnavailable);
    } finally {
      _busy = false;
    }
  }
}
