import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:zatiti_desktop/src/app/credential_store.dart';
import 'package:zatiti_desktop/src/app/desktop_discovery.dart';
import 'package:zatiti_desktop/src/app/installed_bootstrap.dart';
import 'package:zatiti_desktop/src/transport/controller_client.dart';
import 'package:zatiti_desktop/src/transport/endpoint.dart';
import 'package:zatiti_desktop/src/transport/errors.dart';

import '../support/fake_controller.dart';

class Reader extends DesktopDiscoveryReader {
  Reader(this.record);
  DesktopDiscovery record;
  @override
  Future<DesktopDiscovery> read() async => record;
}

void main() {
  late FakeController fake;
  late Reader reader;
  late MemoryCredentialStore storage;

  setUp(() async {
    fake = await FakeController.start();
    await Process.run('/bin/chmod', ['600', fake.socketPath]);
    reader = Reader(DesktopDiscovery(socketPath: fake.socketPath));
    storage = MemoryCredentialStore();
  });
  tearDown(() async => fake.stop());

  test('happy path sends one unauthenticated unkeyed local init', () async {
    fake.script = (_) {
      reader.record = DesktopDiscovery(
        socketPath: fake.socketPath,
        installationId: testInstallationId,
        keychainService: 'com.zatiti.owner',
        keychainAccount: 'owner',
      );
      return completed('{"resource":{}}');
    };
    final flow = InstalledBootstrap(reader: reader, storage: storage);
    expect((await flow.initialize('Ada')).state, BootstrapState.ready);
    final requests = fake.requestsFor('installation.init');
    expect(requests, hasLength(1));
    expect(requests.single.input, {
      'credential_store': 'os',
      'owner_name': 'Ada',
    });
    expect(requests.single.submissionKey, isNull);
    expect(requests.single.headers.containsKey('authorization'), isFalse);
    expect((await flow.initialize('Ada')).state, BootstrapState.ready);
    expect(fake.requestsFor('installation.init'), hasLength(1));
  });

  test(
    'acknowledged init waits for locator and repeated tap never resends',
    () async {
      fake.script = (_) => completed('{"resource":{}}');
      final flow = InstalledBootstrap(reader: reader, storage: storage);
      expect((await flow.initialize('Ada')).state, BootstrapState.pending);
      expect((await flow.initialize('Ada')).state, BootstrapState.pending);
      expect(fake.requestsFor('installation.init'), hasLength(1));
    },
  );

  test(
    'unknown acknowledgment survives restart and reconciles discovery only',
    () async {
      fake.script = (_) => const DropReply();
      final flow = InstalledBootstrap(reader: reader, storage: storage);
      expect((await flow.initialize('Ada')).state, BootstrapState.pending);
      final restarted = InstalledBootstrap(reader: reader, storage: storage);
      expect((await restarted.inspect()).state, BootstrapState.pending);
      expect((await restarted.initialize('Ada')).state, BootstrapState.pending);
      expect(fake.requestsFor('installation.init'), hasLength(1));
      reader.record = DesktopDiscovery(
        socketPath: fake.socketPath,
        installationId: testInstallationId,
        keychainService: 'com.zatiti.owner',
        keychainAccount: 'owner',
      );
      expect((await restarted.inspect()).state, BootstrapState.ready);
      expect(fake.requestsFor('installation.init'), hasLength(1));
    },
  );

  test('already initialized conflict does not trigger second init', () async {
    fake.script = (_) => fault(409, 'conflict', 'already initialized');
    final flow = InstalledBootstrap(reader: reader, storage: storage);
    expect((await flow.initialize('Ada')).state, BootstrapState.pending);
    expect((await flow.initialize('Ada')).state, BootstrapState.pending);
    expect(fake.requestsFor('installation.init'), hasLength(1));
  });

  test(
    'proven pre-send failure allows only an explicit same-name retry',
    () async {
      var calls = 0;
      final flow = InstalledBootstrap(
        reader: reader,
        storage: storage,
        send: (_, _) async {
          calls++;
          throw const ControllerUnavailableException('not sent');
        },
      );
      expect((await flow.initialize('Ada')).state, BootstrapState.retryable);
      expect(
        (await flow.initialize('Different')).state,
        BootstrapState.retryable,
      );
      expect(calls, 1);
      expect((await flow.initialize('Ada')).state, BootstrapState.retryable);
      expect(calls, 2);
    },
  );

  test(
    'bootstrap client refuses remote and distinguishes pre-send failure',
    () async {
      final client = ControllerClient(
        endpoint: LocalSocketEndpoint('/tmp/nonexistent-zatiti.sock'),
        installationId: testInstallationId,
        credentials: () async => 'Bearer secret',
      );
      await expectLater(
        client.bootstrapInstallation('Ada'),
        throwsA(isA<ControllerUnavailableException>()),
      );
    },
  );
}
