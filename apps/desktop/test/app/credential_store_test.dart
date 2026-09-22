// Exercises the real `SecureCredentialStore` — the OS Keychain/Secret
// Service wrapper AGENTS.md requires ("credential, client TLS key material
// and offline drafts live in operating-system secure storage") — against
// `flutter_secure_storage`'s own officially shipped test platform
// (`TestFlutterSecureStoragePlatform`, `package:flutter_secure_storage/
// test/test_flutter_secure_storage_platform.dart`). That platform is the
// package's supported seam for substituting the unavoidable native channel
// boundary in a host-mode `flutter test` run — the actual macOS Keychain or
// Linux Secret Service is not reachable there. Nothing about
// `SecureCredentialStore`'s own logic (real per-profile key derivation, the
// real `FlutterSecureStorage` read/write/delete plumbing, drafts/TLS
// isolation) is faked here; only the last-mile OS call is, through the
// package's own contract — `MemoryCredentialStore` (used everywhere else in
// this suite) is a hand-rolled stand-in for `CredentialStore` itself and
// proves none of this.

import 'package:flutter_secure_storage/test/test_flutter_secure_storage_platform.dart';
import 'package:flutter_secure_storage_platform_interface/flutter_secure_storage_platform_interface.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:zatiti_desktop/src/app/credential_store.dart';

void main() {
  late Map<String, String> backing;

  setUp(() {
    backing = <String, String>{};
    FlutterSecureStoragePlatform.instance = TestFlutterSecureStoragePlatform(
      backing,
    );
  });

  test('writes and reads the credential under this profile’s real secure-'
      'storage key, populated with live-shaped bytes', () async {
    final store = SecureCredentialStore('proj-a');
    expect(await store.read(), isNull);

    await store.write('Bearer live-credential-bytes');
    expect(await store.read(), 'Bearer live-credential-bytes');
    expect(
      backing[store.keys.authorization],
      'Bearer live-credential-bytes',
      reason: 'the real key name this store uses, not a guess',
    );
  });

  test(
    'deletes the credential from secure storage, not merely locally',
    () async {
      final store = SecureCredentialStore('proj-a');
      await store.write('Bearer live-credential-bytes');
      await store.delete();
      expect(await store.read(), isNull);
      expect(backing.containsKey(store.keys.authorization), isFalse);
    },
  );

  test(
    'named entries (TLS client certificate, offline drafts) round-trip '
    'under their own real keys, and deleting one never touches another',
    () async {
      final store = SecureCredentialStore('proj-a');
      const pem =
          '-----BEGIN CERTIFICATE-----\nMIIB...\n-----END CERTIFICATE-----';
      await store.writeNamed(store.keys.clientCertificate, pem);
      expect(await store.readNamed(store.keys.clientCertificate), pem);
      expect(backing[store.keys.clientCertificate], pem);

      const draftsJson = '{"conv-1":"Book the room for Friday"}';
      await store.writeNamed(store.keys.drafts, draftsJson);
      expect(await store.readNamed(store.keys.drafts), draftsJson);

      await store.deleteNamed(store.keys.clientCertificate);
      expect(await store.readNamed(store.keys.clientCertificate), isNull);
      expect(await store.readNamed(store.keys.drafts), draftsJson);
    },
  );

  test(
    'two profiles never share storage, even on the same backing store',
    () async {
      final a = SecureCredentialStore('proj-a');
      final b = SecureCredentialStore('proj-b');
      expect(a.keys.authorization, isNot(b.keys.authorization));

      await a.write('Bearer credential-a');
      expect(await b.read(), isNull, reason: 'proj-b has written nothing yet');

      await b.write('Bearer credential-b');
      expect(await a.read(), 'Bearer credential-a');
      expect(await b.read(), 'Bearer credential-b');
      expect(
        backing.keys,
        containsAll([a.keys.authorization, b.keys.authorization]),
      );
    },
  );
}
