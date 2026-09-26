// The installed Mac controller publishes only nonsecret connection metadata.
// This reader accepts its frozen, owner-only record; it never opens controller
// state or follows a link into another location.

import 'dart:io';

import '../transport/strict_json.dart';

const desktopDiscoverySchema = 'zatiti.desktop-discovery/v1';
const maxDesktopDiscoveryBytes = 4096;

enum DiscoveryFailure { unreadable, malformed, unsafe }

class DiscoveryException implements Exception {
  const DiscoveryException(this.kind);
  final DiscoveryFailure kind;
}

class DesktopDiscovery {
  const DesktopDiscovery({
    required this.socketPath,
    this.installationId,
    this.keychainService,
    this.keychainAccount,
  });

  final String socketPath;
  final String? installationId;
  final String? keychainService;
  final String? keychainAccount;

  bool get initialized => installationId != null;
}

final _uuid = RegExp(
  r'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$',
);

final _asciiItem = RegExp(r'^[\x21-\x7e]{1,256}$');

DesktopDiscovery parseDesktopDiscovery(List<int> bytes) {
  if (bytes.isEmpty || bytes.length > maxDesktopDiscoveryBytes) {
    throw const DiscoveryException(DiscoveryFailure.malformed);
  }
  final Object? decoded;
  try {
    decoded = decodeStrictJsonBytes(bytes);
  } on StrictJsonException {
    throw const DiscoveryException(DiscoveryFailure.malformed);
  }
  if (decoded is! Map<String, Object?>) {
    throw const DiscoveryException(DiscoveryFailure.malformed);
  }
  const base = {'schema', 'protocol_version', 'socket_path'};
  const initialized = {
    ...base,
    'installation_id',
    'keychain_service',
    'keychain_account',
  };
  if (decoded.keys.toSet().difference(initialized).isNotEmpty ||
      !decoded.keys.toSet().containsAll(base) ||
      decoded['schema'] != desktopDiscoverySchema ||
      decoded['protocol_version'] != 1 ||
      decoded['protocol_version'] is! int) {
    throw const DiscoveryException(DiscoveryFailure.malformed);
  }
  final socket = decoded['socket_path'];
  if (socket is! String || !_safeSocketPath(socket)) {
    throw const DiscoveryException(DiscoveryFailure.unsafe);
  }
  final extra = decoded.keys.toSet().difference(base);
  if (extra.isEmpty) return DesktopDiscovery(socketPath: socket);
  if (extra.length != 3 || !extra.containsAll(initialized.difference(base))) {
    throw const DiscoveryException(DiscoveryFailure.malformed);
  }
  final installation = decoded['installation_id'];
  final service = decoded['keychain_service'];
  final account = decoded['keychain_account'];
  if (installation is! String ||
      !_uuid.hasMatch(installation) ||
      service is! String ||
      !_asciiItem.hasMatch(service) ||
      account is! String ||
      !_asciiItem.hasMatch(account)) {
    throw const DiscoveryException(DiscoveryFailure.malformed);
  }
  return DesktopDiscovery(
    socketPath: socket,
    installationId: installation,
    keychainService: service,
    keychainAccount: account,
  );
}

bool _safeSocketPath(String path) {
  if (!path.startsWith('/') ||
      utf8Length(path) >= 104 ||
      path.contains('\u0000')) {
    return false;
  }
  final parts = path.split('/');
  return parts.length > 2 &&
      parts.first.isEmpty &&
      parts.skip(1).every((p) => p.isNotEmpty && p != '.' && p != '..');
}

int utf8Length(String text) => text.runes.fold<int>(
  0,
  (n, r) =>
      n +
      (r <= 0x7f
          ? 1
          : r <= 0x7ff
          ? 2
          : r <= 0xffff
          ? 3
          : 4),
);

/// Reads `$HOME/Library/Application Support/zatiti/desktop.json` by default.
/// Injectable identity checks allow permission tests without changing the
/// test process's uid. Production uses macOS's stat/id tools without a shell.
class DesktopDiscoveryReader {
  DesktopDiscoveryReader({
    String? stateDirectory,
    Future<int> Function(String path)? ownerOf,
    Future<int> Function()? currentOwner,
  }) : stateDirectory = stateDirectory ?? _defaultStateDirectory(),
       _ownerOf = ownerOf ?? _statOwner,
       _currentOwner = currentOwner ?? _uid;

  final String stateDirectory;
  final Future<int> Function(String) _ownerOf;
  final Future<int> Function() _currentOwner;

  Future<DesktopDiscovery?> read() async {
    if (stateDirectory.isEmpty || !stateDirectory.startsWith('/')) {
      throw const DiscoveryException(DiscoveryFailure.unreadable);
    }
    final directory = Directory(stateDirectory);
    final file = File('${directory.path}/desktop.json');
    try {
      final dirType = await FileSystemEntity.type(
        directory.path,
        followLinks: false,
      );
      if (dirType == FileSystemEntityType.notFound) return null;
      if (dirType != FileSystemEntityType.directory) {
        throw const DiscoveryException(DiscoveryFailure.unsafe);
      }
      final dirStat = await directory.stat();
      if (dirStat.mode & 0x1ff != 0x1c0) {
        throw const DiscoveryException(DiscoveryFailure.unsafe);
      }
      final fileType = await FileSystemEntity.type(
        file.path,
        followLinks: false,
      );
      if (fileType == FileSystemEntityType.notFound) return null;
      if (fileType != FileSystemEntityType.file) {
        throw const DiscoveryException(DiscoveryFailure.unsafe);
      }
      final stat = await file.stat();
      if (stat.mode & 0x1ff != 0x180 || stat.size > maxDesktopDiscoveryBytes) {
        throw const DiscoveryException(DiscoveryFailure.unsafe);
      }
      final uid = await _currentOwner();
      if (await _ownerOf(directory.path) != uid ||
          await _ownerOf(file.path) != uid) {
        throw const DiscoveryException(DiscoveryFailure.unsafe);
      }
      final bytes = await file
          .openRead(0, maxDesktopDiscoveryBytes + 1)
          .expand((b) => b)
          .toList();
      final record = parseDesktopDiscovery(bytes);
      if (record.socketPath.substring(0, record.socketPath.lastIndexOf('/')) !=
          directory.absolute.path) {
        throw const DiscoveryException(DiscoveryFailure.unsafe);
      }
      return record;
    } on DiscoveryException {
      rethrow;
    } on FileSystemException {
      throw const DiscoveryException(DiscoveryFailure.unreadable);
    } on ProcessException {
      throw const DiscoveryException(DiscoveryFailure.unreadable);
    }
  }
}

String _defaultStateDirectory() {
  final home = Platform.environment['HOME'];
  if (home == null || home.isEmpty || !home.startsWith('/')) return '';
  return '$home/Library/Application Support/zatiti';
}

/// Reject a replaced or stale socket before reading the owner credential.
Future<bool> isSafeLiveSocket(String path) async {
  try {
    if (await FileSystemEntity.type(path, followLinks: false) !=
        FileSystemEntityType.unixDomainSock) {
      return false;
    }
    final stat = await File(path).stat();
    return stat.mode & 0x12 == 0;
  } on FileSystemException {
    return false;
  }
}

Future<int> _uid() async => _numberFrom('/usr/bin/id', ['-u']);

Future<int> _statOwner(String path) async =>
    _numberFrom('/usr/bin/stat', ['-f', '%u', path]);

Future<int> _numberFrom(String executable, List<String> args) async {
  final result = await Process.run(executable, args);
  if (result.exitCode != 0) {
    throw const DiscoveryException(DiscoveryFailure.unreadable);
  }
  final value = int.tryParse((result.stdout as String).trim());
  if (value == null || value < 0) {
    throw const DiscoveryException(DiscoveryFailure.unreadable);
  }
  return value;
}
