// What a person sees when they open this app against a real controller.
//
// The widget tree here is the shipped one: the real `ZatitiApp` over a real
// `WorkspaceController` over the real transport, on the private Unix socket
// of a controller this test started. Nothing is faked, and no fixture stands
// in for a controller response. Run it with tool/live-proof.sh.

@Timeout(Duration(minutes: 10))
library;

import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:zatiti_desktop/src/app/app.dart';
import 'package:zatiti_desktop/src/app/credential_store.dart';
import 'package:zatiti_desktop/src/state/live_source.dart';
import 'package:zatiti_desktop/src/state/view_state.dart';
import 'package:zatiti_desktop/src/state/workspace_controller.dart';
import 'package:zatiti_desktop/src/transport/controller_client.dart';
import 'package:zatiti_desktop/src/transport/endpoint.dart';
import 'package:zatiti_desktop/src/ui/workspace_settings.dart';

import 'support/live_controller.dart';

void main() {
  late LiveController controller;

  setUpAll(() async => controller = await LiveController.start());
  tearDownAll(() async => controller.stop());

  setUp(() {
    // A widget test binding installs HttpOverrides that answer every request
    // with a canned empty 400 and never open a socket. That is the right
    // default for a widget test and exactly wrong for this suite, whose whole
    // point is the real controller, so the override is removed here. The
    // binding reinstalls it for every other suite.
    HttpOverrides.global = null;
  });

  Finder key(String k) => find.byKey(ValueKey(k));

  /// Opens the shipped application against the live controller and returns
  /// its workspace controller once the first snapshot has loaded.
  ///
  /// Talking to the controller is real socket work, so every call that waits
  /// on it runs through [WidgetTester.runAsync]; a widget test's own zone
  /// does not run the event loop those futures complete on.
  Future<WorkspaceController> open(WidgetTester tester) async {
    tester.view.physicalSize = const Size(1440, 1000);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);

    final workspace = WorkspaceController(
      LiveWorkspaceSource(
        ControllerClient(
          endpoint: LocalSocketEndpoint(controller.socketPath),
          installationId: controller.installationId,
          credentials: () async => controller.authorization,
        ),
        endpointLabel: 'Your controller',
      ),
    );
    await tester.runAsync(workspace.start);
    expect(
      workspace.connection,
      ConnectionPhase.online,
      reason:
          'the app could not connect to a controller it is running against: '
          '${workspace.connectionMessage}',
    );
    await tester.pumpWidget(
      ZatitiApp(
        controller: workspace,
        settings: AppSettings(),
        credentials: MemoryCredentialStore(),
      ),
    );
    await tester.pumpAndSettle();
    return workspace;
  }

  testWidgets('a first launch opens the personal chief with a composer', (
    tester,
  ) async {
    final workspace = await open(tester);

    // The chief the controller created at bootstrap, by the name it gave it.
    final chief = workspace.snapshot.workers.single;
    expect(find.text(chief.name), findsWidgets);
    expect(key('tree-${chief.id.value}'), findsOneWidget);

    // Its conversation is open and ready to type in.
    expect(key('composer-field'), findsOneWidget);
    expect(key('composer-send'), findsOneWidget);

    // Nothing on this installation is waiting on the person, and the count
    // says exactly that rather than a number it cannot justify.
    expect(tester.widget<Text>(key('needs-you-count')).data, '0');
    expect(workspace.snapshot.reviews, isEmpty);

    // A real controller is connected, and this is not the demo.
    expect(key('offline-banner'), findsNothing);
    expect(key('demo-banner'), findsNothing);
  });

  testWidgets('settings name the identities the controller authenticates', (
    tester,
  ) async {
    await open(tester);
    await tester.tap(key('open-settings'));
    await tester.pumpAndSettle();

    expect(find.text('Your workspace'), findsOneWidget);
    expect(key('principal-list'), findsOneWidget);
    // The owner installation.init created, and the controller's own identity.
    expect(find.text(controller.ownerName), findsOneWidget);
    expect(find.text('controller'), findsOneWidget);
    expect(find.text('Controller service'), findsOneWidget);
    // The credential is stored, never shown.
    expect(key('credential-field'), findsOneWidget);
    expect(find.textContaining('Bearer'), findsNothing);
  });

  testWidgets('a typed message is sent and then shown', (tester) async {
    final workspace = await open(tester);

    const body = 'Create a marketing chief.';
    await tester.enterText(key('composer-field'), body);
    await tester.pumpAndSettle();
    final conversation = workspace.selectedConversation!.id;
    expect(workspace.draftFor(conversation), body);

    // Exactly what the send button's handler does — clear the field, keep the
    // body as the draft, send it — except that the send is started inside
    // runAsync. Tapping the button starts it in the widget test's own zone
    // instead, where a real socket future never completes and the message
    // stays "sending" forever. That is a constraint of the test harness, not
    // of the application: test/ui covers the button itself.
    await tester.enterText(key('composer-field'), '');
    workspace.setDraft(conversation, body);
    await tester.runAsync(() => workspace.sendDraft(conversation));
    await tester.pumpAndSettle();

    // One message on screen, in the thread and no longer in the composer.
    expect(find.text(body), findsOneWidget);
    expect(workspace.draftFor(conversation), isEmpty);
    expect(
      workspace.outgoingFor(conversation),
      isEmpty,
      reason: 'an acknowledged message leaves the unacknowledged queue',
    );
    expect(
      workspace.connection,
      ConnectionPhase.online,
      reason: 'sending must not take the workspace offline',
    );
    // No worker can answer yet: the conversation says what it cannot show
    // instead of inventing a reply.
    expect(
      workspace.selectedConversation!.historyNotice,
      contains('no operation that reads'),
    );
  });
}
