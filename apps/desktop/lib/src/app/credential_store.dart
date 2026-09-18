// Client credentials live in the operating system's secure storage. A
// credential profile is startup configuration and never an operation
// argument. The stored value is the complete Authorization header value.

import 'package:flutter_secure_storage/flutter_secure_storage.dart';

abstract interface class CredentialStore {
  /// The stored Authorization header value, or null when none is stored.
  Future<String?> read();

  Future<void> write(String value);

  Future<void> delete();
}

/// Names of the secure-storage entries for one profile.
class CredentialKeys {
  const CredentialKeys(this.profile);
  final String profile;

  String get authorization => 'zatiti.$profile.authorization';
  String get clientCertificate => 'zatiti.$profile.tls.certificate';
  String get clientPrivateKey => 'zatiti.$profile.tls.private_key';
  String get trustedRoots => 'zatiti.$profile.tls.trusted_roots';
}

/// Keychain on macOS, libsecret on Linux, Credential Manager on Windows.
class SecureCredentialStore implements CredentialStore {
  SecureCredentialStore(String profile, {FlutterSecureStorage? storage})
    : _keys = CredentialKeys(profile),
      _storage = storage ?? const FlutterSecureStorage();

  final CredentialKeys _keys;
  final FlutterSecureStorage _storage;

  @override
  Future<String?> read() => _storage.read(key: _keys.authorization);

  @override
  Future<void> write(String value) =>
      _storage.write(key: _keys.authorization, value: value);

  @override
  Future<void> delete() => _storage.delete(key: _keys.authorization);

  /// Reads one PEM entry for mutual TLS.
  Future<String?> readPem(String key) => _storage.read(key: key);
}

/// Holds a credential for the life of the process only. Used by tests and by
/// the demo, which has no credential at all.
class MemoryCredentialStore implements CredentialStore {
  MemoryCredentialStore([this._value]);
  String? _value;

  @override
  Future<String?> read() async => _value;

  @override
  Future<void> write(String value) async => _value = value;

  @override
  Future<void> delete() async => _value = null;
}
