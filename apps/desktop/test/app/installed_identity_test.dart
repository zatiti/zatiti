import 'package:flutter_test/flutter_test.dart';
import 'package:zatiti_desktop/src/app/startup.dart';
import 'package:zatiti_desktop/src/transport/controller_client.dart';
import 'package:zatiti_desktop/src/transport/endpoint.dart';

import '../support/fake_controller.dart';

void main() {
  test(
    'authenticated status gates installed workspace by identity and bootstrap',
    () async {
      final fake = await FakeController.start();
      addTearDown(fake.stop);
      final client = ControllerClient(
        endpoint: LocalSocketEndpoint(fake.socketPath),
        installationId: testInstallationId,
        credentials: () async => 'Bearer ${List.filled(43, 'A').join()}',
      );
      String status(String id, bool initialized) =>
          '{"resource":{"installation_id":"$id","generation":1,'
          '"paused":false,"maintenance":false,"initialized":$initialized,'
          '"requirements":[],"version":1}}';
      fake.script = (_) => completed(status(testInstallationId, true));
      expect(
        await verifyInstalledController(client, testInstallationId),
        isNull,
      );
      fake.script = (_) =>
          completed(status('00000000-0000-4000-8000-0000000000bb', true));
      expect(
        await verifyInstalledController(client, testInstallationId),
        StartupIssue.identityMismatch,
      );
      fake.script = (_) => completed(status(testInstallationId, false));
      expect(
        await verifyInstalledController(client, testInstallationId),
        StartupIssue.awaitingBootstrap,
      );
      expect(fake.requestsFor('installation.status'), hasLength(3));
      expect(
        fake.requests.first.headers['authorization'],
        startsWith('Bearer '),
      );
    },
  );
}
