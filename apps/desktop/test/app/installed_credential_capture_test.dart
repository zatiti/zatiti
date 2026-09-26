import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:zatiti_desktop/src/app/installed_credential_capture.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  const channel = MethodChannel(credentialCaptureChannel);
  const installation = '11111111-1111-1111-1111-111111111111';
  const connection = '22222222-2222-2222-2222-222222222222';

  tearDown(() {
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, null);
  });

  test('only schema and two UUIDs cross the native channel', () async {
    MethodCall? seen;
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, (call) async {
          seen = call;
          return {
            'schema': credentialCaptureResultSchema,
            'status': 'completed',
            'reason_code': 'none',
          };
        });
    final result = await const InstalledCredentialCapture().capture(
      installedMac: true,
      installationId: installation,
      connectionId: connection,
    );
    expect(result.status, CredentialCaptureStatus.completed);
    expect(seen?.method, 'capture');
    expect(seen?.arguments, {
      'schema': credentialCaptureSchema,
      'installation_id': installation,
      'connection_id': connection,
    });
  });

  test(
    'remote and development profiles cannot invoke native capture',
    () async {
      var invoked = false;
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(channel, (_) async {
            invoked = true;
            return null;
          });
      await expectLater(
        const InstalledCredentialCapture().capture(
          installedMac: false,
          installationId: installation,
          connectionId: connection,
        ),
        throwsFormatException,
      );
      await expectLater(
        const InstalledCredentialCapture().capture(
          installedMac: true,
          installationId: installation,
          connectionId: 'BAD',
        ),
        throwsFormatException,
      );
      expect(invoked, isFalse);
    },
  );

  test('secret-bearing or unknown native responses are refused', () async {
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(
          channel,
          (_) async => {
            'schema': credentialCaptureResultSchema,
            'status': 'completed',
            'reason_code': 'none',
            'credential': 'synthetic-secret',
          },
        );
    await expectLater(
      const InstalledCredentialCapture().capture(
        installedMac: true,
        installationId: installation,
        connectionId: connection,
      ),
      throwsFormatException,
    );
    expect(
      () => CredentialCaptureResult.parse({
        'schema': credentialCaptureResultSchema,
        'status': 'completed',
        'reason_code': 'provider_text',
      }),
      throwsFormatException,
    );
  });
}
