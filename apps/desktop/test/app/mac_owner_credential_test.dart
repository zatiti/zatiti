import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter_secure_storage/test/test_flutter_secure_storage_platform.dart';
import 'package:flutter_secure_storage_platform_interface/flutter_secure_storage_platform_interface.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:zatiti_desktop/src/app/credential_store.dart';

class RecordingKeychain extends TestFlutterSecureStoragePlatform {
  RecordingKeychain(super.data);
  String? lastKey;
  Map<String, String>? lastOptions;
  PlatformException? failure;

  @override
  Future<String?> read({
    required String key,
    required Map<String, String> options,
  }) async {
    lastKey = key;
    lastOptions = options;
    if (failure != null) throw failure!;
    return super.read(key: key, options: options);
  }
}

void main() {
  late Map<String, String> backing;
  late RecordingKeychain native;
  late MacOwnerCredentialStore store;
  final token = base64Url.encode(List<int>.filled(32, 42)).replaceAll('=', '');
  final header = 'Bearer $token';

  setUp(() {
    debugDefaultTargetPlatformOverride = TargetPlatform.macOS;
    backing = {};
    native = RecordingKeychain(backing);
    FlutterSecureStoragePlatform.instance = native;
    store = MacOwnerCredentialStore(
      service: 'com.zatiti.owner',
      account: 'installation/00000000-0000-4000-8000-0000000000aa/owner',
    );
  });
  tearDown(() => debugDefaultTargetPlatformOverride = null);

  test(
    'reads existing encoded owner item using login Keychain service/account',
    () async {
      backing[store.account] = base64.encode(utf8.encode(header));
      expect(await store.read(), header);
      expect(native.lastKey, store.account);
      expect(native.lastOptions?['accountName'], store.service);
      expect(native.lastOptions?['usesDataProtectionKeychain'], 'false');
      expect(backing.containsKey(store.keys.authorization), isFalse);
      expect(store.authorizationManaged, isTrue);
    },
  );

  test('missing, malformed and noncanonical credential fail closed', () async {
    await expectLater(
      store.read(),
      throwsA(
        isA<MacCredentialException>().having(
          (e) => e.kind,
          'kind',
          MacCredentialFailure.missing,
        ),
      ),
    );
    for (final value in [
      '%%%',
      'Bearer $token',
      base64.encode(utf8.encode('Bearer ${List.filled(42, 'A').join()}B')),
    ]) {
      backing[store.account] = value;
      await expectLater(
        store.read(),
        throwsA(
          isA<MacCredentialException>().having(
            (e) => e.kind,
            'kind',
            MacCredentialFailure.malformed,
          ),
        ),
      );
    }
  });

  test('locked and refused OSStatus remain distinct', () async {
    native.failure = PlatformException(code: 'Keychain', details: -25308);
    await expectLater(
      store.read(),
      throwsA(
        isA<MacCredentialException>().having(
          (e) => e.kind,
          'kind',
          MacCredentialFailure.locked,
        ),
      ),
    );
    native.failure = PlatformException(code: 'Keychain', details: -34018);
    await expectLater(
      store.read(),
      throwsA(
        isA<MacCredentialException>().having(
          (e) => e.kind,
          'kind',
          MacCredentialFailure.refused,
        ),
      ),
    );
  });

  test('managed owner value cannot be replaced or deleted in app', () async {
    await expectLater(
      store.write(header),
      throwsA(isA<MacCredentialException>()),
    );
    await expectLater(store.delete(), throwsA(isA<MacCredentialException>()));
    expect(backing, isEmpty);
  });
}
