import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:zatiti_desktop/src/api/models.dart' as wire;
import 'package:zatiti_desktop/src/app/local_store.dart';
import 'package:zatiti_desktop/src/state/demo_source.dart';
import 'package:zatiti_desktop/src/state/installed_chief.dart';
import 'package:zatiti_desktop/src/state/snapshot.dart';
import 'package:zatiti_desktop/src/state/workspace_controller.dart';
import 'package:zatiti_desktop/src/ui/conversation_view.dart';
import 'package:zatiti_desktop/src/ui/theme.dart';

const installation = '00000000-0000-4000-8000-0000000000aa';
const chiefId = '00000000-0000-4000-8000-0000000000bb';
const otherId = '00000000-0000-4000-8000-0000000000cc';
const orgId = '00000000-0000-4000-8000-0000000000dd';
const pinnedId = '00000000-0000-4000-8000-0000000000ee';
const otherChatId = '00000000-0000-4000-8000-0000000000ff';
final now = DateTime.utc(2026, 9, 23);
final limits = wire.Limits(
  currency: 'USD',
  spendMicroUnits: 1000000,
  concurrency: 1,
  modelSteps: 10,
  childCount: 0,
  delegationDepth: 0,
  attemptSeconds: 60,
  rootDeadline: DateTime.utc(2027),
);
final profile = wire.ExecutionProfile(
  id: 'profile',
  version: 1,
  executor: 'hosted',
  model: 'configured-model',
  connectionId: 'conn',
  providerDestination: 'https://example.invalid',
  costBound: const wire.Money(currency: 'USD', microUnits: 100000),
  contextCapture: 'complete',
);

wire.Conversation conversation(
  String id,
  String participant, {
  bool pinned = false,
}) => wire.Conversation(
  id: id,
  version: 1,
  scope: const wire.Scope(installationId: installation),
  kind: wire.ConversationKind.direct,
  participantIds: ['owner', participant],
  title: 'Chat',
  pinned: pinned,
);

InstalledChief gate({
  List<wire.Conversation>? chats,
  wire.ConnectionValidationState state = wire.ConnectionValidationState.valid,
  wire.Limits? effective,
  wire.ExecutionProfile? chiefProfile,
  bool paused = false,
  bool? runtimeReady = true,
  List<wire.Requirement> requirements = const [],
}) => installedChiefFromRecords(
  installationId: installation,
  initialized: true,
  runtimeReady: runtimeReady,
  paused: paused,
  maintenance: false,
  requirements: requirements,
  now: now,
  organizations: [
    const wire.Organization(
      id: orgId,
      version: 1,
      key: 'root',
      name: 'Personal',
      chiefId: chiefId,
    ),
  ],
  workers: [
    const wire.Worker(
      id: otherId,
      version: 1,
      organizationId: orgId,
      key: 'a',
      name: 'Other',
      purpose: 'Other',
    ),
    wire.Worker(
      id: chiefId,
      version: 1,
      organizationId: orgId,
      key: 'z',
      name: 'Chief',
      purpose: 'Chief',
      profile: chiefProfile ?? profile,
      limits: limits,
    ),
  ],
  conversations:
      chats ??
      [
        conversation(otherChatId, chiefId),
        conversation(pinnedId, chiefId, pinned: true),
      ],
  connections: [
    wire.Connection(
      id: 'conn',
      version: 1,
      provider: 'provider',
      accountIdentity: 'account',
      validationState: state,
    ),
  ],
  effectiveBudget: effective ?? limits,
);

class InstalledSource extends DemoWorkspaceSource {
  InstalledSource(this.fixed);
  final WorkspaceSnapshot fixed;
  @override
  Future<WorkspaceSnapshot> loadSnapshot() async => fixed;
}

WorkspaceSnapshot snapshot({
  String? chiefConversation = pinnedId,
  String? issue,
  bool ready = true,
}) => WorkspaceSnapshot(
  takenAt: now,
  workers: [
    const WorkerEntry(
      id: WorkerId(otherId),
      name: 'Other',
      role: 'Other',
      organizationId: OrganizationId(orgId),
      organizationPath: ['Personal'],
      parentId: null,
      conversationId: ConversationId(otherChatId),
    ),
    WorkerEntry(
      id: const WorkerId(chiefId),
      name: 'Chief',
      role: 'Your chief',
      organizationId: const OrganizationId(orgId),
      organizationPath: const ['Personal'],
      parentId: null,
      conversationId: chiefConversation == null
          ? null
          : ConversationId(chiefConversation),
    ),
  ],
  conversations: [
    const ConversationEntry(
      id: ConversationId(otherChatId),
      title: 'Wrong first chat',
      kind: ConversationKind.direct,
      messages: [],
      workerId: WorkerId(otherId),
    ),
    const ConversationEntry(
      id: ConversationId(pinnedId),
      title: 'Personal chief',
      kind: ConversationKind.direct,
      messages: [],
      workerId: WorkerId(chiefId),
    ),
  ],
  reviews: const [],
  installedMac: true,
  installedChiefWorkerId: chiefId,
  installedChiefConversationId: chiefConversation,
  installedChiefIssue: ready ? issue : (issue ?? 'Setup is incomplete.'),
);

void main() {
  test('uses pinned chief chat, not first direct chat or first worker', () {
    final selected = gate();
    expect(selected.workerId, chiefId);
    expect(selected.conversationId, pinnedId);
    expect(selected.ready, isTrue);
  });

  test(
    'missing or ambiguous pinned chat cannot be replaced by another direct chat',
    () {
      final missing = gate(chats: [conversation(otherChatId, chiefId)]);
      expect(missing.conversationId, isNull);
      expect(missing.ready, isFalse);
      final ambiguous = gate(
        chats: [
          conversation(pinnedId, chiefId, pinned: true),
          conversation(otherChatId, chiefId, pinned: true),
        ],
      );
      expect(ambiguous.conversationId, isNull);
      expect(ambiguous.ready, isFalse);
      final extraParticipant = wire.Conversation(
        id: pinnedId,
        version: 1,
        scope: const wire.Scope(installationId: installation),
        kind: wire.ConversationKind.direct,
        participantIds: const ['owner', chiefId, otherId],
        title: 'Misleading pinned chat',
        pinned: true,
      );
      expect(gate(chats: [extraParticipant]).ready, isFalse);
    },
  );

  test(
    'committed provider, validation, active status and effective limits gate readiness',
    () {
      expect(
        gate(state: wire.ConnectionValidationState.unverified).ready,
        isFalse,
      );
      expect(gate(paused: true).ready, isFalse);
      expect(gate(runtimeReady: null).ready, isFalse);
      expect(gate(runtimeReady: false).ready, isFalse);
      final required = gate(
        requirements: const [
          wire.Requirement(
            code: 'provider_unavailable',
            message: 'Finish trusted setup',
          ),
        ],
      );
      expect(required.ready, isFalse);
      expect(required.issue, contains('remaining setup requirements'));
      expect(
        gate(
          effective: wire.Limits(
            currency: 'XXX',
            spendMicroUnits: 0,
            concurrency: 0,
            modelSteps: 0,
            childCount: 0,
            delegationDepth: 0,
            attemptSeconds: 0,
            rootDeadline: DateTime.utc(2027),
          ),
        ).ready,
        isFalse,
      );
    },
  );

  test(
    'installed first launch and stale restart select chief, never first root',
    () async {
      for (final state in [
        LocalState.empty,
        const LocalState(selectedWorkerId: 'stale'),
      ]) {
        final controller = WorkspaceController(
          InstalledSource(snapshot()),
          initialLocal: state,
        );
        await controller.start();
        expect(controller.selectedWorker, const WorkerId(chiefId));
        expect(
          controller.selectedConversation?.id,
          const ConversationId(pinnedId),
        );
        expect(controller.canCompose, isTrue);
        controller.dispose();
      }
    },
  );

  test(
    'missing pinned chat leaves installed composer blocked and no send',
    () async {
      final controller = WorkspaceController(
        InstalledSource(
          snapshot(
            chiefConversation: null,
            issue: 'Pinned chief chat is missing.',
            ready: false,
          ),
        ),
      );
      await controller.start();
      expect(controller.selectedWorker, const WorkerId(chiefId));
      expect(controller.selectedConversation, isNull);
      expect(controller.canCompose, isFalse);
      controller.setDraft(const ConversationId(otherChatId), 'hello');
      await controller.sendDraft(const ConversationId(otherChatId));
      expect(
        controller.outgoingFor(const ConversationId(otherChatId)),
        isEmpty,
      );
      controller.dispose();
    },
  );

  testWidgets(
    'installed missing chief chat shows repair notice instead of composer',
    (t) async {
      final controller = WorkspaceController(
        InstalledSource(
          snapshot(
            chiefConversation: null,
            issue: 'Pinned chief chat is missing.',
            ready: false,
          ),
        ),
      );
      await controller.start();
      await t.pumpWidget(
        MaterialApp(
          theme: buildZatitiTheme(Brightness.light),
          home: Scaffold(body: ConversationView(controller: controller)),
        ),
      );
      expect(find.text('Pinned chief chat is missing.'), findsOneWidget);
      expect(find.byKey(const ValueKey('composer-field')), findsNothing);
      controller.dispose();
    },
  );
}
