import 'dart:async';
import 'dart:ui' show SemanticsFlag;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:zatiti_desktop/src/app/app.dart';
import 'package:zatiti_desktop/src/app/credential_store.dart';
import 'package:zatiti_desktop/src/state/demo_source.dart';
import 'package:zatiti_desktop/src/state/snapshot.dart';
import 'package:zatiti_desktop/src/state/workspace_controller.dart';
import 'package:zatiti_desktop/src/ui/theme.dart';
import 'package:zatiti_desktop/src/ui/workspace_settings.dart';

const _review = DemoWorkspaceSource.pullRequestReview;
const _engineering = DemoWorkspaceSource.engineering;
const _quality = DemoWorkspaceSource.quality;

class _Harness {
  _Harness(this.source, this.controller, this.settings);
  final DemoWorkspaceSource source;
  final WorkspaceController controller;
  final AppSettings settings;
}

Future<_Harness> _pump(
  WidgetTester tester, {
  Size size = const Size(1440, 1000),
  double textScale = 1,
  Brightness platform = Brightness.dark,
}) async {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1;
  tester.platformDispatcher.platformBrightnessTestValue = platform;
  tester.platformDispatcher.textScaleFactorTestValue = textScale;
  addTearDown(tester.view.reset);
  addTearDown(tester.platformDispatcher.clearAllTestValues);

  final source = DemoWorkspaceSource();
  final controller = WorkspaceController(source);
  final settings = AppSettings();
  await controller.start();
  await tester.pumpWidget(
    ZatitiApp(
      controller: controller,
      settings: settings,
      credentials: MemoryCredentialStore(),
    ),
  );
  await tester.pumpAndSettle();
  return _Harness(source, controller, settings);
}

Finder _key(String k) => find.byKey(ValueKey(k));

/// The focus node a control owns, as keyboard traversal reaches it.
FocusNode _focusOf(WidgetTester t, Finder control) {
  final inner = find.descendant(of: control, matching: find.byType(Focus));
  // Focus builds an inherited scope and then its child; an element below
  // both resolves to that Focus's own node.
  var element = t.element(inner.first);
  for (var depth = 0; depth < 2; depth++) {
    Element? child;
    element.visitChildElements((e) => child ??= e);
    element = child!;
  }
  return Focus.maybeOf(element, createDependency: false)!;
}

String _text(WidgetTester tester, String key) =>
    tester.widget<Text>(_key(key)).data!;

void main() {
  group('WorkspaceShell', () {
    testWidgets('the demo source is labeled on screen at all times', (t) async {
      await _pump(t);
      expect(_key('demo-banner'), findsOneWidget);
      expect(
        find.textContaining('not connected to a controller'),
        findsWidgets,
      );
      expect(find.textContaining('fictional'), findsOneWidget);
    });

    testWidgets('does not advertise email or sending', (t) async {
      await _pump(t);
      expect(find.textContaining('email'), findsNothing);
      expect(find.textContaining('Email'), findsNothing);
    });

    testWidgets('sidebar count, card and tree share one state', (t) async {
      await _pump(t);
      expect(_text(t, 'needs-you-count'), '1');
      expect(_key('decision-card-${_review.value}'), findsOneWidget);
      // Engineering is collapsed, so the indicator bubbles up to it.
      expect(_key('decision-dot-${_engineering.value}'), findsOneWidget);
      expect(_key('decision-dot-${_quality.value}'), findsNothing);
    });

    testWidgets('the chevron expands without changing the conversation', (
      t,
    ) async {
      final h = await _pump(t);
      expect(h.controller.selectedWorker, DemoWorkspaceSource.wren);
      await t.tap(_key('toggle-${_engineering.value}'));
      await t.pumpAndSettle();

      expect(h.controller.selectedWorker, DemoWorkspaceSource.wren);
      expect(_key('open-${_quality.value}'), findsOneWidget);
      // Expanded: the indicator moves down to the worker that owns it.
      expect(_key('decision-dot-${_engineering.value}'), findsNothing);
      expect(_key('decision-dot-${_quality.value}'), findsOneWidget);

      await t.tap(_key('open-${_quality.value}'));
      await t.pumpAndSettle();
      expect(h.controller.selectedWorker, _quality);
      expect(
        find.text('Personal › Engineering › Quality › Quality chief'),
        findsOneWidget,
      );
    });

    testWidgets('arrow keys expand and collapse a focused row', (t) async {
      final h = await _pump(t);
      await t.tap(_key('open-${_engineering.value}'));
      await t.pumpAndSettle();
      h.controller.setExpanded(_engineering, expanded: false);
      await t.pumpAndSettle();

      _focusOf(t, _key('open-${_engineering.value}')).requestFocus();
      await t.pump();
      await t.sendKeyEvent(LogicalKeyboardKey.arrowRight);
      await t.pumpAndSettle();
      expect(h.controller.isExpanded(_engineering), isTrue);
      await t.sendKeyEvent(LogicalKeyboardKey.arrowLeft);
      await t.pumpAndSettle();
      expect(h.controller.isExpanded(_engineering), isFalse);
    });

    testWidgets('tree rows announce ancestry, state and disclosure', (t) async {
      final handle = t.ensureSemantics();
      final h = await _pump(t);
      expect(
        find.bySemanticsLabel(RegExp('Engineering chief.*decisions waiting')),
        findsOneWidget,
      );
      expect(find.bySemanticsLabel('Expand Engineering chief'), findsOneWidget);
      h.controller.selectWorker(_quality);
      await t.pumpAndSettle();
      expect(
        find.bySemanticsLabel('Collapse Engineering chief'),
        findsOneWidget,
      );
      final row = t.getSemantics(
        find
            .bySemanticsLabel(
              RegExp('Website reviewer.*Personal › Engineering › Quality'),
            )
            .first,
      );
      expect(row.hasFlag(SemanticsFlag.isSelected), isTrue);
      expect(
        find.bySemanticsLabel(RegExp('Needs you, 1 waiting')),
        findsOneWidget,
      );
      handle.dispose();
    });
  });

  group('ActionReviewDialog', () {
    Future<void> openReview(WidgetTester t) async {
      await t.tap(_key('open-review-${_review.value}'));
      await t.pumpAndSettle();
    }

    testWidgets('shows exact content and consequence; evidence is collapsed', (
      t,
    ) async {
      await _pump(t);
      await openReview(t);
      expect(_key('review-consequence'), findsOneWidget);
      expect(
        find.textContaining('Clarify the first-time setup steps'),
        findsOneWidget,
      );
      expect(find.textContaining('docs/getting-started.md'), findsOneWidget);
      expect(find.text('github.com/example/website · base main'), findsWidgets);
      expect(find.text('0.04 USD'), findsOneWidget);
      expect(find.textContaining('Standing rule'), findsNothing);

      await t.ensureVisible(find.text('Why this needs you · rules & evidence'));
      await t.pumpAndSettle();
      await t.tap(find.text('Why this needs you · rules & evidence'));
      await t.pumpAndSettle();
      expect(find.textContaining('Standing rule'), findsOneWidget);
    });

    testWidgets('approval shows only after acknowledgment, and the control '
        'is disabled while submitting', (t) async {
      final h = await _pump(t);
      final gate = Completer<void>();
      h.source.beforeSubmit = () => gate.future;
      await openReview(t);

      await t.tap(_key('review-approve'));
      await t.pump();
      expect(t.widget<FilledButton>(_key('review-approve')).onPressed, isNull);
      expect(
        t.widget<OutlinedButton>(_key('review-decline')).onPressed,
        isNull,
      );
      expect(find.text('Sending…'), findsOneWidget);
      expect(_text(t, 'needs-you-count'), '1');
      // A second activation while submitting reaches nothing.
      await t.tap(_key('review-approve'), warnIfMissed: false);
      await t.pump();
      expect(h.source.committed, isEmpty);

      gate.complete();
      await t.pumpAndSettle();
      expect(h.source.committed, hasLength(1));
      expect(_text(t, 'needs-you-count'), '0');
      expect(find.text('Your decision'), findsOneWidget);
      expect(find.text('Approved · delivery not confirmed'), findsWidgets);
      expect(_key('review-approve'), findsNothing);
    });

    testWidgets('decline records a decline everywhere', (t) async {
      await _pump(t);
      await openReview(t);
      await t.tap(_key('review-decline'));
      await t.pumpAndSettle();
      expect(_text(t, 'needs-you-count'), '0');
      expect(find.text('Declined · nothing will be sent'), findsWidgets);
    });

    testWidgets('a request that changes while open must be re-read', (t) async {
      final h = await _pump(t);
      await openReview(t);
      h.source.reviseReview();
      await h.controller.poll();
      await t.pumpAndSettle();

      expect(t.widget<FilledButton>(_key('review-approve')).onPressed, isNull);
      expect(find.textContaining('This request changed'), findsOneWidget);

      await t.tap(_key('review-refresh'));
      await t.pumpAndSettle();
      expect(find.textContaining('REVISION 2'), findsOneWidget);
      expect(
        t.widget<FilledButton>(_key('review-approve')).onPressed,
        isNotNull,
      );
      expect(h.source.committed, isEmpty);
    });

    testWidgets('Escape closes it and focus returns to the opener', (t) async {
      await _pump(t);
      final opener = _key('open-review-${_review.value}');
      _focusOf(t, opener).requestFocus();
      await t.pump();
      await t.sendKeyEvent(LogicalKeyboardKey.enter);
      await t.pumpAndSettle();
      expect(_key('review-approve'), findsOneWidget);

      await t.sendKeyEvent(LogicalKeyboardKey.escape);
      await t.pumpAndSettle();
      expect(_key('review-approve'), findsNothing);
      expect(_focusOf(t, opener).hasFocus, isTrue);
    });

    testWidgets('Tab reaches decline and approve', (t) async {
      await _pump(t);
      await openReview(t);
      final reached = <String>{};
      for (var i = 0; i < 12; i++) {
        await t.sendKeyEvent(LogicalKeyboardKey.tab);
        await t.pump();
        for (final k in ['review-decline', 'review-approve']) {
          if (_focusOf(t, _key(k)).hasFocus) reached.add(k);
        }
      }
      expect(reached, {'review-decline', 'review-approve'});
    });

    testWidgets('opens from Needs you', (t) async {
      await _pump(t);
      await t.tap(_key('needs-you'));
      await t.pumpAndSettle();
      expect(find.text('One decision across your workspace.'), findsOneWidget);
      await t.tap(_key('needs-open-${_review.value}'));
      await t.pumpAndSettle();
      expect(_key('review-approve'), findsOneWidget);
    });
  });

  group('offline', () {
    Future<_Harness> offline(WidgetTester t) async {
      final h = await _pump(t);
      await t.tap(_key('demo-offline-toggle'));
      await t.pumpAndSettle();
      return h;
    }

    testWidgets('labels the saved view and disables decisions', (t) async {
      await offline(t);
      expect(_key('offline-banner'), findsOneWidget);
      expect(_text(t, 'connection-status'), 'Saved view');
      await t.tap(_key('open-review-${_review.value}'));
      await t.pumpAndSettle();
      expect(t.widget<FilledButton>(_key('review-approve')).onPressed, isNull);
      expect(
        t.widget<OutlinedButton>(_key('review-decline')).onPressed,
        isNull,
      );
    });

    testWidgets('drafts stay visibly unsent, and reconnect sends nothing', (
      t,
    ) async {
      final h = await offline(t);
      await t.enterText(_key('composer-field'), 'Book the room for Friday');
      await t.pump();
      await t.tap(_key('composer-send'));
      await t.pumpAndSettle();
      expect(find.text('Unsent draft · not delivered'), findsOneWidget);
      expect(find.text('Book the room for Friday'), findsOneWidget);

      // The outage ends and the person reconnects.
      h.source.offline = false;
      await t.tap(_key('reconnect'));
      await t.pumpAndSettle();
      expect(_text(t, 'connection-status'), 'Available');
      expect(find.text('Unsent draft · not delivered'), findsOneWidget);
      expect(h.source.committed, isEmpty);

      await t.tap(find.text('Send now'));
      await t.pumpAndSettle();
      expect(find.text('Unsent draft · not delivered'), findsNothing);
      expect(h.source.committed, hasLength(1));
      expect(
        find.textContaining('No worker or model received'),
        findsOneWidget,
      );
    });
  });

  group('MessageComposer', () {
    testWidgets('keeps a separate draft per conversation', (t) async {
      final h = await _pump(t);
      await t.enterText(_key('composer-field'), 'for Wren');
      await t.pump();
      h.controller.selectWorker(DemoWorkspaceSource.ledger);
      await t.pumpAndSettle();
      expect(
        t.widget<TextField>(_key('composer-field')).controller!.text,
        isEmpty,
      );
      h.controller.selectWorker(DemoWorkspaceSource.wren);
      await t.pumpAndSettle();
      expect(
        t.widget<TextField>(_key('composer-field')).controller!.text,
        'for Wren',
      );
    });

    testWidgets('Enter sends and the send control is labeled', (t) async {
      final handle = t.ensureSemantics();
      final h = await _pump(t);
      expect(find.bySemanticsLabel(RegExp('Message Wren')), findsOneWidget);
      await t.enterText(_key('composer-field'), 'Hello');
      await t.pump();
      expect(find.bySemanticsLabel('Send message to Wren'), findsOneWidget);
      await t.sendKeyEvent(LogicalKeyboardKey.enter);
      await t.pumpAndSettle();
      expect(h.source.committed, hasLength(1));
      handle.dispose();
    });

    testWidgets('an empty draft cannot be sent', (t) async {
      await _pump(t);
      expect(t.widget<IconButton>(_key('composer-send')).onPressed, isNull);
    });
  });

  group('WorkerDetailsPanel', () {
    testWidgets('has five tabs fed by the same snapshot', (t) async {
      await _pump(t);
      await t.tap(_key('details-button'));
      await t.pumpAndSettle();
      for (final tab in DetailsTab.values) {
        expect(_key('details-tab-${tab.name}'), findsOneWidget);
      }
      // Work: the same pending decision as the card and the count.
      expect(find.text('Needs your decision'.toUpperCase()), findsOneWidget);
      expect(find.text('Needs changes'.toUpperCase()), findsOneWidget);
      expect(find.textContaining('1 of 3 checks failed'), findsOneWidget);

      await t.tap(_key('details-tab-files'));
      await t.pumpAndSettle();
      expect(find.text('Partnership shortlist'), findsOneWidget);

      await t.tap(_key('details-tab-memory'));
      await t.pumpAndSettle();
      expect(find.text('No shared preferences in this scope.'), findsOneWidget);

      await t.tap(_key('details-tab-access'));
      await t.pumpAndSettle();
      expect(
        find.text('No access is recorded for this worker.'),
        findsOneWidget,
      );
    });

    testWidgets('pause shows as paused only after acknowledgment', (t) async {
      final h = await _pump(t);
      final gate = Completer<void>();
      h.source.beforeSubmit = () => gate.future;
      h.controller.openDetails(DetailsTab.routines);
      await t.pumpAndSettle();
      final pause = _key('pause-${DemoWorkspaceSource.reconciliation.value}');
      await t.tap(pause);
      await t.pump();
      expect(find.text('Pausing…'), findsWidgets);
      expect(find.text('Paused'), findsNothing);
      expect(t.widget<OutlinedButton>(pause).onPressed, isNull);

      gate.complete();
      await t.pumpAndSettle();
      expect(find.text('Paused'), findsOneWidget);
      expect(pause, findsNothing);
    });

    testWidgets('overlays on a narrow window and can be dismissed', (t) async {
      final h = await _pump(t, size: const Size(900, 800));
      h.controller.openDetails();
      await t.pumpAndSettle();
      expect(_key('details-close'), findsOneWidget);
      await t.tap(_key('details-close'));
      await t.pumpAndSettle();
      expect(h.controller.detailsOpen, isFalse);
    });
  });

  group('appearance and scaling', () {
    testWidgets('follows the operating system preference', (t) async {
      await _pump(t, platform: Brightness.dark);
      var scaffold = t.widget<Scaffold>(find.byType(Scaffold).first);
      expect(scaffold.backgroundColor, const Color(0xFF141515));

      t.platformDispatcher.platformBrightnessTestValue = Brightness.light;
      await t.pumpAndSettle();
      scaffold = t.widget<Scaffold>(find.byType(Scaffold).first);
      expect(scaffold.backgroundColor, const Color(0xFFFAFBF8));
    });

    testWidgets('the person can override the system preference', (t) async {
      final h = await _pump(t, platform: Brightness.light);
      h.settings.themeMode = ThemeMode.dark;
      await t.pumpAndSettle();
      final palette = ZatitiPalette.of(t.element(find.byType(Scaffold).first));
      expect(palette.accent, const Color(0xFFB8D8C8));
      expect(palette.amber, const Color(0xFFE4C391));
    });

    testWidgets('doubled text does not overflow the shell or the dialog', (
      t,
    ) async {
      final h = await _pump(t, textScale: 2);
      h.controller.openDetails();
      await t.pumpAndSettle();
      for (final tab in DetailsTab.values) {
        expect(_key('details-tab-${tab.name}').hitTestable(), findsOneWidget);
      }
      await t.tap(_key('needs-you'));
      await t.pumpAndSettle();
      await t.tap(_key('needs-open-${_review.value}'));
      await t.pumpAndSettle();
      expect(t.takeException(), isNull);
      expect(_key('review-approve'), findsOneWidget);
    });

    testWidgets('a narrow window moves the tree into a dismissible overlay', (
      t,
    ) async {
      final h = await _pump(t, size: const Size(420, 800));
      expect(t.takeException(), isNull);
      expect(_key('open-${_engineering.value}'), findsNothing);
      await t.tap(find.byTooltip('Open conversations'));
      await t.pumpAndSettle();
      await t.tap(_key('open-${_engineering.value}'));
      await t.pumpAndSettle();
      expect(h.controller.selectedWorker, _engineering);
      expect(_key('open-${_engineering.value}'), findsNothing);
    });
  });

  group('WorkspaceSettings', () {
    testWidgets('the demo has no credential entry', (t) async {
      await _pump(t);
      await t.tap(_key('open-settings'));
      await t.pumpAndSettle();
      expect(find.text('Your workspace'), findsOneWidget);
      expect(_key('credential-field'), findsNothing);
      expect(find.text('Match system'), findsOneWidget);
    });

    testWidgets('it lists the identities the controller authenticates', (
      t,
    ) async {
      final h = await _pump(t);
      await t.tap(_key('open-settings'));
      await t.pumpAndSettle();
      expect(_key('principal-list'), findsOneWidget);
      for (final p in h.controller.snapshot.principals) {
        expect(find.text(p.name), findsOneWidget, reason: p.name);
      }
      expect(find.text('Controller service'), findsOneWidget);
      expect(
        find.textContaining('this app only shows them'),
        findsOneWidget,
        reason: 'identity administration is not a desktop surface',
      );
    });
  });
}
