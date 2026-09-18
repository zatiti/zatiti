import 'package:flutter_test/flutter_test.dart';
import 'package:zatiti_desktop/src/app/startup.dart';

const _installation = '00000000-0000-4000-8000-0000000000aa';

void main() {
  test('a configured socket starts the live client', () {
    final plan = resolveStartup(
      environment: {
        envSocket: '/run/example/controller.sock',
        envInstallation: _installation,
      },
      releaseMode: true,
      demoRequested: false,
    );
    expect(plan, isA<StartLive>());
    final profile = (plan as StartLive).profile;
    expect(profile.socketPath, '/run/example/controller.sock');
    expect(profile.profile, 'default');
  });

  test('a release build never falls back to the demo', () {
    final plan = resolveStartup(
      environment: const {},
      releaseMode: true,
      demoRequested: false,
    );
    expect(plan, isA<NeedsConfiguration>());
    expect((plan as NeedsConfiguration).demoOffered, isFalse);
    expect(plan.missing, hasLength(2));
  });

  test('a development build offers the demo but does not start it', () {
    final plan = resolveStartup(
      environment: const {},
      releaseMode: false,
      demoRequested: false,
    );
    expect(plan, isA<NeedsConfiguration>());
    expect((plan as NeedsConfiguration).demoOffered, isTrue);
  });

  test('the demo needs its explicit flag', () {
    expect(
      resolveStartup(
        environment: const {},
        releaseMode: true,
        demoRequested: true,
      ),
      isA<StartDemo>(),
    );
  });

  test('refuses a plain http remote and a malformed installation', () {
    final plan = resolveStartup(
      environment: {
        envRemoteUrl: 'http://controller.example:8443',
        envInstallation: 'not-a-uuid',
      },
      releaseMode: true,
      demoRequested: false,
    );
    expect((plan as NeedsConfiguration).missing, hasLength(2));
  });

  test('refuses both transports at once', () {
    final plan = resolveStartup(
      environment: {
        envSocket: '/run/example/controller.sock',
        envRemoteUrl: 'https://controller.example:8443',
        envInstallation: _installation,
      },
      releaseMode: true,
      demoRequested: false,
    );
    expect(plan, isA<NeedsConfiguration>());
  });
}
