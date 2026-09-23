// Client credentials live in the operating system's secure storage. A
// credential profile is startup configuration and never an operation
// argument. The stored value is the complete Authorization header value.

import 'dart:convert';

import 'package:flutter/services.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

abstract interface class CredentialStore {
  /// The installed owner credential is managed by the controller's Keychain
  /// item and cannot be replaced from the settings form.
  bool get authorizationManaged;

  /// The stored Authorization header value, or null when none is stored.
  Future<String?> read();

  Future<void> write(String value);

  Future<void> delete();

  /// Reads one named secure-storage entry: TLS PEM material or the drafts
  /// blob. Never the Authorization value; use [read] for that.
  Future<String?> readNamed(String key);

  Future<void> writeNamed(String key, String value);

  Future<void> deleteNamed(String key);

  /// The key names this store's entries live under, for a caller (settings
  /// UI, tests) that needs to name a specific entry such as the TLS client
  /// certificate without constructing its own profile-keyed strings.
  CredentialKeys get keys;
}

/// Names of the secure-storage entries for one profile. Offline drafts and
/// TLS key material are frozen (AGENTS.md, "Local IO, authentication...")
/// to live in secure storage exactly like the credential itself; only the
/// selected conversation and the bounded authorized cache live in ordinary
/// OS-protected local storage (see `local_store.dart`).
class CredentialKeys {
  const CredentialKeys(this.profile);
  final String profile;

  String get authorization => 'zatiti.$profile.authorization';
  String get clientCertificate => 'zatiti.$profile.tls.certificate';
  String get clientPrivateKey => 'zatiti.$profile.tls.private_key';
  String get trustedRoots => 'zatiti.$profile.tls.trusted_roots';

  /// One JSON object of conversation id to unsent draft text.
  String get drafts => 'zatiti.$profile.drafts';
}

/// Keychain on macOS, libsecret on Linux, Credential Manager on Windows.
class SecureCredentialStore implements CredentialStore {
  SecureCredentialStore(String profile, {FlutterSecureStorage? storage})
    : _keys = CredentialKeys(profile),
      _storage =
          storage ??
          const FlutterSecureStorage(
            // The data-protection keychain needs a Keychain Sharing
            // entitlement tied to a signing team. This build is not signed
            // with one, so it uses the login keychain. See the README.
            mOptions: MacOsOptions(usesDataProtectionKeychain: false),
          );

  final CredentialKeys _keys;
  final FlutterSecureStorage _storage;

  @override
  bool get authorizationManaged => false;

  @override
  CredentialKeys get keys => _keys;

  @override
  Future<String?> read() => _storage.read(key: _keys.authorization);

  @override
  Future<void> write(String value) =>
      _storage.write(key: _keys.authorization, value: value);

  @override
  Future<void> delete() => _storage.delete(key: _keys.authorization);

  @override
  Future<String?> readNamed(String key) => _storage.read(key: key);

  @override
  Future<void> writeNamed(String key, String value) =>
      _storage.write(key: key, value: value);

  @override
  Future<void> deleteNamed(String key) => _storage.delete(key: key);

  /// Reads one PEM entry for mutual TLS. Kept for callers written against
  /// the earlier name; identical to [readNamed].
  Future<String?> readPem(String key) => readNamed(key);
}

enum MacCredentialFailure { missing, locked, refused, malformed }

class MacCredentialException implements Exception {
  const MacCredentialException(this.kind);
  final MacCredentialFailure kind;
}

/// Reads the controller's existing login-Keychain item without copying it to
/// another item. Named drafts/TLS settings remain in this app's own store.
class MacOwnerCredentialStore implements CredentialStore {
  MacOwnerCredentialStore({
    required this.service,
    required this.account,
    String profile = 'default',
    FlutterSecureStorage? ownerStorage,
    SecureCredentialStore? namedStorage,
  }) : _ownerStorage =
           ownerStorage ??
           FlutterSecureStorage(
             mOptions: MacOsOptions(
               accountName: service,
               usesDataProtectionKeychain: false,
             ),
           ),
       _namedStorage = namedStorage ?? SecureCredentialStore(profile);

  final String service;
  final String account;
  final FlutterSecureStorage _ownerStorage;
  final SecureCredentialStore _namedStorage;

  @override
  bool get authorizationManaged => true;

  @override
  CredentialKeys get keys => _namedStorage.keys;

  @override
  Future<String?> read() async {
    final String? encoded;
    try {
      encoded = await _ownerStorage.read(key: account);
    } on PlatformException catch (e) {
      final status = e.details;
      throw MacCredentialException(
        status == -25308
            ? MacCredentialFailure.locked
            : MacCredentialFailure.refused,
      );
    } on Exception {
      throw const MacCredentialException(MacCredentialFailure.refused);
    }
    if (encoded == null) {
      throw const MacCredentialException(MacCredentialFailure.missing);
    }
    try {
      final bytes = base64.decode(encoded);
      if (base64.encode(bytes) != encoded) {
        throw const FormatException('noncanonical base64');
      }
      final header = utf8.decode(bytes, allowMalformed: false);
      if (!RegExp(r'^Bearer [A-Za-z0-9_-]{43}$').hasMatch(header)) {
        throw const FormatException('invalid header');
      }
      final token = header.substring(7);
      final secret = base64Url.decode('$token=');
      if (secret.length != 32 ||
          base64Url.encode(secret).replaceAll('=', '') != token) {
        throw const FormatException('invalid token');
      }
      return header;
    } on FormatException {
      throw const MacCredentialException(MacCredentialFailure.malformed);
    }
  }

  @override
  Future<void> write(String value) =>
      Future.error(const MacCredentialException(MacCredentialFailure.refused));

  @override
  Future<void> delete() =>
      Future.error(const MacCredentialException(MacCredentialFailure.refused));

  @override
  Future<String?> readNamed(String key) => _namedStorage.readNamed(key);

  @override
  Future<void> writeNamed(String key, String value) =>
      _namedStorage.writeNamed(key, value);

  @override
  Future<void> deleteNamed(String key) => _namedStorage.deleteNamed(key);
}

/// Holds a credential for the life of the process only. Used by tests and by
/// the demo, which has no credential at all.
class MemoryCredentialStore implements CredentialStore {
  MemoryCredentialStore([this._value, CredentialKeys? keys])
    : _keys = keys ?? const CredentialKeys('test');
  String? _value;
  final Map<String, String> _named = {};
  final CredentialKeys _keys;

  @override
  bool get authorizationManaged => false;

  @override
  CredentialKeys get keys => _keys;

  @override
  Future<String?> read() async => _value;

  @override
  Future<void> write(String value) async => _value = value;

  @override
  Future<void> delete() async => _value = null;

  @override
  Future<String?> readNamed(String key) async => _named[key];

  @override
  Future<void> writeNamed(String key, String value) async =>
      _named[key] = value;

  @override
  Future<void> deleteNamed(String key) async => _named.remove(key);
}
