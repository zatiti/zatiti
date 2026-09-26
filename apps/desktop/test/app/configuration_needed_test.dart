import 'dart:async';
import 'dart:ui' show SemanticsFlag;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:zatiti_desktop/src/app/app.dart';
import 'package:zatiti_desktop/src/app/installed_bootstrap.dart';
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

  testWidgets('installed form sends one init while first tap is pending', (
    t,
  ) async {
    final completed = Completer<BootstrapView>();
    var calls = 0;
    await t.pumpWidget(
      ConfigurationNeededApp(
        plan: const NeedsConfiguration(
          ['The local service is ready.'],
          demoOffered: false,
          issue: StartupIssue.awaitingBootstrap,
        ),
        onOpenDemo: () => fail('no demo'),
        onInitialize: (name) {
          expect(name, 'Ada');
          calls++;
          return completed.future;
        },
        bootstrapView: const BootstrapView(BootstrapState.fresh),
      ),
    );
    await t.enterText(
      find.byKey(const ValueKey('installed-owner-name')),
      'Ada',
    );
    await t.tap(find.byKey(const ValueKey('installed-initialize')));
    await t.pump();
    await t.tap(find.byKey(const ValueKey('installed-initialize')));
    expect(calls, 1);
    completed.complete(const BootstrapView(BootstrapState.pending));
    await t.pump();
    expect(find.text('Check setup status'), findsOneWidget);
  });

  testWidgets('owner name, setup action, Enter and Escape work by keyboard', (
    t,
  ) async {
    final completed = Completer<BootstrapView>();
    var calls = 0;
    await t.pumpWidget(
      ConfigurationNeededApp(
        plan: const NeedsConfiguration(
          ['Ready for setup.'],
          demoOffered: false,
          issue: StartupIssue.awaitingBootstrap,
        ),
        onOpenDemo: () => fail('no demo'),
        onInitialize: (_) {
          calls++;
          return completed.future;
        },
        bootstrapView: const BootstrapView(BootstrapState.fresh),
      ),
    );
    await t.pump();
    final field = t.widget<TextField>(
      find.byKey(const ValueKey('installed-owner-name')),
    );
    final action = t.widget<FilledButton>(
      find.byKey(const ValueKey('installed-initialize')),
    );
    expect(field.focusNode!.hasFocus, isTrue);
    await t.sendKeyEvent(LogicalKeyboardKey.tab);
    await t.pump();
    expect(action.focusNode!.hasFocus, isTrue);
    await t.sendKeyEvent(LogicalKeyboardKey.escape);
    await t.pump();
    expect(action.focusNode!.hasFocus, isFalse);
    expect(calls, 0);
    await t.tap(find.byKey(const ValueKey('installed-owner-name')));
    await t.enterText(
      find.byKey(const ValueKey('installed-owner-name')),
      'Ada',
    );
    await t.testTextInput.receiveAction(TextInputAction.done);
    await t.pump();
    expect(calls, 1);
    await t.testTextInput.receiveAction(TextInputAction.done);
    await t.pump();
    expect(calls, 1);
    completed.complete(const BootstrapView(BootstrapState.pending));
    await t.pump();
  });

  testWidgets('repair states have distinct headings and actionable detail', (
    t,
  ) async {
    for (final (issue, heading, detail) in [
      (
        StartupIssue.lockedKeychain,
        'Unlock Keychain',
        'Unlock your Mac Keychain',
      ),
      (
        StartupIssue.staleSocket,
        'Local service unavailable',
        'Restart the service',
      ),
      (
        StartupIssue.identityMismatch,
        'Repair the local connection',
        'different installation',
      ),
    ]) {
      await t.pumpWidget(
        ConfigurationNeededApp(
          key: ValueKey(issue),
          plan: NeedsConfiguration([detail], demoOffered: false, issue: issue),
          onOpenDemo: () => fail('no demo'),
        ),
      );
      expect(find.text(heading), findsOneWidget);
      expect(find.textContaining(detail), findsWidgets);
      expect(find.byKey(const ValueKey('installed-owner-name')), findsNothing);
      final semantics = t.widget<Semantics>(
        find
            .ancestor(of: find.text(heading), matching: find.byType(Semantics))
            .first,
      );
      expect(semantics.properties.header, isTrue);
    }
  });

  testWidgets(
    'welcome exposes a heading, named field, action and live status',
    (t) async {
      final handle = t.ensureSemantics();
      await t.pumpWidget(
        ConfigurationNeededApp(
          plan: const NeedsConfiguration(
            ['The local service is ready.'],
            demoOffered: false,
            issue: StartupIssue.awaitingBootstrap,
          ),
          onOpenDemo: () => fail('no demo'),
          onInitialize: (_) async =>
              const BootstrapView(BootstrapState.pending),
          bootstrapView: const BootstrapView(BootstrapState.fresh),
        ),
      );
      expect(
        t
            .getSemantics(find.text('Welcome to Zatiti'))
            .hasFlag(SemanticsFlag.isHeader),
        isTrue,
      );
      expect(find.bySemanticsLabel(RegExp('Your name')), findsWidgets);
      expect(find.bySemanticsLabel('Set up Zatiti'), findsOneWidget);
      expect(
        t
            .getSemantics(
              find.text(
                'This creates your local installation and personal chief once.',
              ),
            )
            .hasFlag(SemanticsFlag.isLiveRegion),
        isTrue,
      );
      handle.dispose();
    },
  );

  testWidgets(
    '200 percent text fits a narrow installed welcome and repair window',
    (t) async {
      await t.binding.setSurfaceSize(const Size(360, 480));
      addTearDown(() => t.binding.setSurfaceSize(null));
      Future<void> show(NeedsConfiguration plan, {bool form = false}) async {
        await t.pumpWidget(
          MediaQuery(
            data: const MediaQueryData(textScaler: TextScaler.linear(2)),
            child: ConfigurationNeededApp(
              key: ValueKey(plan.issue),
              plan: plan,
              onOpenDemo: () => fail('no demo'),
              onInitialize: form
                  ? (_) async => const BootstrapView(BootstrapState.pending)
                  : null,
              bootstrapView: form
                  ? const BootstrapView(BootstrapState.fresh)
                  : null,
            ),
          ),
        );
        await t.pump();
        expect(t.takeException(), isNull);
      }

      await show(
        const NeedsConfiguration(
          ['The local service is ready for workspace setup.'],
          demoOffered: false,
          issue: StartupIssue.awaitingBootstrap,
        ),
        form: true,
      );
      await t.ensureVisible(find.byKey(const ValueKey('installed-initialize')));
      await t.pump();
      expect(t.takeException(), isNull);
      await show(
        const NeedsConfiguration(
          [
            'Zatiti’s connection information is damaged or from an unsupported version.',
          ],
          demoOffered: false,
          issue: StartupIssue.malformedDiscovery,
        ),
      );
      expect(find.text('Repair the local connection'), findsOneWidget);
      expect(t.takeException(), isNull);
    },
  );
}
