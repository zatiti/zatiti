import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:zatiti_desktop/src/app/app.dart';
import 'package:zatiti_desktop/src/app/startup.dart';

void main() {
  testWidgets('a release build explains setup and offers no demo', (t) async {
    await t.pumpWidget(
      ConfigurationNeededApp(
        plan: const NeedsConfiguration([
          'ZATITI_SOCKET: the path of your controller’s private socket',
        ], demoOffered: false),
        onOpenDemo: () => fail('the demo must not be reachable'),
      ),
    );
    expect(find.text('Connect to your controller'), findsOneWidget);
    expect(find.textContaining('ZATITI_SOCKET'), findsOneWidget);
    expect(find.byKey(const ValueKey('open-demo')), findsNothing);
  });

  testWidgets('a development build offers the labeled demo', (t) async {
    var opened = false;
    await t.pumpWidget(
      ConfigurationNeededApp(
        plan: const NeedsConfiguration(['x'], demoOffered: true),
        onOpenDemo: () => opened = true,
      ),
    );
    await t.tap(find.byKey(const ValueKey('open-demo')));
    expect(opened, isTrue);
    expect(find.textContaining('fictional'), findsOneWidget);
  });
}
