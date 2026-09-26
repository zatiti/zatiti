import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:zatiti_desktop/src/app/desktop_discovery.dart';
import 'package:zatiti_desktop/src/app/startup.dart';

const id = '00000000-0000-4000-8000-0000000000aa';

void main() {
  late Directory dir;
  late DesktopDiscoveryReader reader;

  Map<String, Object> record(String socket) => {
    'schema': desktopDiscoverySchema,
    'protocol_version': 1,
    'socket_path': socket,
    'installation_id': id,
    'keychain_service': 'com.zatiti.owner',
    'keychain_account': 'installation/$id/owner',
  };

  Future<void> write(Map<String, Object> json) async {
    final file = File('${dir.path}/desktop.json');
    await file.writeAsString(jsonEncode(json));
    await Process.run('/bin/chmod', ['600', file.path]);
  }

  setUp(() async {
    dir = await Directory.systemTemp.createTemp('zt-discovery-');
    await Process.run('/bin/chmod', ['700', dir.path]);
    reader = DesktopDiscoveryReader(
      stateDirectory: dir.path,
      ownerOf: (_) async => 501,
      currentOwner: () async => 501,
    );
  });
  tearDown(() async => dir.delete(recursive: true));

  test(
    'reads initialized protected record and resolves installed launch',
    () async {
      await write(record('${dir.path}/controller.sock'));
      final got = await reader.read();
      expect(got!.installationId, id);
      expect(got.keychainService, 'com.zatiti.owner');
      final plan = await resolveStartupPlan(
        environment: const {},
        releaseMode: true,
        demoRequested: false,
        macOS: true,
        reader: reader,
      );
      expect((plan as StartLive).profile.installed, isTrue);
    },
  );

  test('accepts pre-bootstrap record as a repair state', () async {
    final r = record('${dir.path}/controller.sock')
      ..remove('installation_id')
      ..remove('keychain_service')
      ..remove('keychain_account');
    await write(r);
    final plan = await resolveStartupPlan(
      environment: const {},
      releaseMode: true,
      demoRequested: false,
      macOS: true,
      reader: reader,
    );
    expect((plan as NeedsConfiguration).issue, StartupIssue.awaitingBootstrap);
  });

  test('rejects socket outside protected state directory', () async {
    await write(record('/tmp/elsewhere.sock'));
    await expectLater(
      reader.read(),
      throwsA(
        isA<DiscoveryException>().having(
          (e) => e.kind,
          'kind',
          DiscoveryFailure.unsafe,
        ),
      ),
    );
  });

  test('rejects symlink and permissive discovery file', () async {
    final target = File('${dir.path}/other.json');
    await target.writeAsString(
      jsonEncode(record('${dir.path}/controller.sock')),
    );
    await Link('${dir.path}/desktop.json').create(target.path);
    await expectLater(
      reader.read(),
      throwsA(
        isA<DiscoveryException>().having(
          (e) => e.kind,
          'kind',
          DiscoveryFailure.unsafe,
        ),
      ),
    );
    await Link('${dir.path}/desktop.json').delete();
    await write(record('${dir.path}/controller.sock'));
    await Process.run('/bin/chmod', ['644', '${dir.path}/desktop.json']);
    await expectLater(
      reader.read(),
      throwsA(
        isA<DiscoveryException>().having(
          (e) => e.kind,
          'kind',
          DiscoveryFailure.unsafe,
        ),
      ),
    );
  });

  test('rejects duplicate keys, partial locator, and non-ASCII locator', () {
    expect(
      () => parseDesktopDiscovery(utf8.encode('{"schema":"x","schema":"y"}')),
      throwsA(isA<DiscoveryException>()),
    );
    final partial = record('/tmp/c.sock')..remove('keychain_account');
    expect(
      () => parseDesktopDiscovery(utf8.encode(jsonEncode(partial))),
      throwsA(isA<DiscoveryException>()),
    );
    final nonAscii = record('/tmp/c.sock')..['keychain_service'] = 'é';
    expect(
      () => parseDesktopDiscovery(utf8.encode(jsonEncode(nonAscii))),
      throwsA(isA<DiscoveryException>()),
    );
  });

  test('explicit environment profile remains available', () async {
    final plan = await resolveStartupPlan(
      environment: {envSocket: '/tmp/controller.sock', envInstallation: id},
      releaseMode: true,
      demoRequested: false,
      macOS: true,
      reader: reader,
    );
    expect((plan as StartLive).profile.installed, isFalse);
  });

  test('socket must be a live socket without group/world write', () async {
    final file = File('${dir.path}/controller.sock');
    await file.writeAsString('not socket');
    expect(await isSafeLiveSocket(file.path), isFalse);
    expect(await isSafeLiveSocket('${dir.path}/missing.sock'), isFalse);
    await file.delete();
    final server = await ServerSocket.bind(
      InternetAddress(file.path, type: InternetAddressType.unix),
      0,
    );
    addTearDown(server.close);
    await Process.run('/bin/chmod', ['600', file.path]);
    expect(await isSafeLiveSocket(file.path), isTrue);
    await Process.run('/bin/chmod', ['622', file.path]);
    expect(await isSafeLiveSocket(file.path), isFalse);
  });
}
