// A clean installation's real organization/worker/task creation journeys,
// against a fake controller that speaks the real envelope and enforces the
// same draft/plan/apply staging the real compiler does: nothing named by
// `organization.create` is visible through `organization.list`/`worker.list`
// until `configuration.apply` actually runs.
//
// Proves the behaviors P43 names directly:
//   - a clean user creates two child chiefs, starts work and receives a
//     human-verified output (a manually-decided task's `task.accept`);
//   - closing the app and reopening it (a fresh `WorkspaceController` over
//     the same controller state) restores the running task without this
//     client ever sending a cancel.

import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:zatiti_desktop/src/state/live_source.dart';
import 'package:zatiti_desktop/src/state/snapshot.dart';
import 'package:zatiti_desktop/src/state/view_state.dart';
import 'package:zatiti_desktop/src/state/workspace_controller.dart';
import 'package:zatiti_desktop/src/transport/controller_client.dart';
import 'package:zatiti_desktop/src/transport/endpoint.dart';
import 'package:zatiti_desktop/src/transport/operations.dart';

import '../support/fake_controller.dart';

String _id(int n) => '00000000-0000-4000-8000-${n.toString().padLeft(12, '0')}';

final _rootOrg = _id(1);
final _chief = _id(2);
final _human = _id(3);
final _service = _id(4);
final _conversation = _id(5);

Map<String, Object?> _scope([String? worker]) => {
  'installation_id': testInstallationId,
  'worker_id': ?worker,
};

Map<String, Object?> _workerJson(
  String id,
  String org,
  String key,
  String name,
) => {
  'id': id,
  'version': 1,
  'organization_id': org,
  'key': key,
  'name': name,
  'purpose': 'Coordinates $name',
  'instructions': '',
  'skill_versions': <Object?>[],
  'bindings': <Object?>[],
  'profile': null,
  'limits': null,
};

/// A staged (not yet applied) organization+chief bundle.
class _Draft {
  _Draft(this.orgJson, this.chiefJson);
  final Map<String, Object?> orgJson;
  final Map<String, Object?> chiefJson;
}

/// A minimal, stateful fake controller: it enforces the real staging
/// distinction (create → plan → apply) rather than shortcutting it, so a
/// test against it proves the client drives every step, not just the first.
class _CreationWorld {
  final List<Map<String, Object?>> organizations = [
    {
      'id': _rootOrg,
      'version': 1,
      'key': 'personal',
      'name': 'Personal',
      'chief_id': _chief,
    },
  ];
  final List<Map<String, Object?>> workers = [
    _workerJson(_chief, _rootOrg, 'wren', 'Wren'),
  ];
  final List<Map<String, Object?>> conversations = [
    {
      'id': _conversation,
      'version': 1,
      'scope': _scope(_chief),
      'kind': 'direct',
      'participant_ids': [_chief, _human],
      'title': 'Wren',
      'pinned': true,
    },
  ];
  final List<Map<String, Object?>> tasks = [];

  final Map<String, _Draft> _drafts = {};
  final Map<String, String> _plans = {};
  int _seq = 1000;
  String _nextId() => _id(_seq++);

  /// Every operation actually called, in order, for assertions about
  /// sequencing.
  final List<String> calls = [];

  Reply handle(RecordedRequest r) {
    final op = r.path.substring('/v1/operations/'.length);
    calls.add(op);
    Reply items(List<Object?> list) => completed(jsonEncode({'items': list}));
    switch (op) {
      case 'capabilities.list':
        return items([
          for (final o in Operations.all)
            {
              'id': o.id,
              'version': o.version,
              'owner': 'fixture',
              'input_schema': <String, Object?>{},
              'output_schema': <String, Object?>{},
              'effect': 'local',
              'scope_requirements': ['installation_id'],
              'cli': <Object?>[],
              'mcp': '',
              'submission_key': o.isMutation,
              'expected_version': o.expectedVersion,
            },
        ]);
      case 'installation.status':
        return completed(
          jsonEncode({
            'resource': {
              'installation_id': testInstallationId,
              'generation': 1,
              'paused': false,
              'maintenance': false,
              'initialized': true,
              'requirements': <Object?>[],
              'version': 1,
            },
          }),
        );
      case 'organization.list':
        return items(organizations);
      case 'worker.list':
        return items(workers);
      case 'conversation.list':
        return items(conversations);
      case 'principal.list':
        return items([
          {
            'id': _human,
            'version': 1,
            'kind': 'human',
            'name': 'You',
            'scope': _scope(),
            'revoked': false,
          },
          {
            'id': _service,
            'version': 1,
            'kind': 'service',
            'name': 'controller',
            'scope': _scope(),
            'revoked': false,
          },
        ]);
      case 'task.list':
        return items(tasks);
      case 'usage.get':
        return completed(
          jsonEncode({
            'resource': {
              'currency': 'XXX',
              'spent': 0,
              'reserved': 0,
              'estimated': 0,
              'unknown': 0,
              'advisory': false,
            },
          }),
        );
      case 'budget.get':
        return completed(
          jsonEncode({
            'limits': {
              'currency': 'XXX',
              'spend_micro_units': 0,
              'concurrency': 1,
              'model_steps': 1,
              'child_count': 0,
              'delegation_depth': 0,
              'attempt_seconds': 60,
              'root_deadline': '2026-09-18T18:00:00Z',
            },
          }),
        );
      case 'installation.verifier.list':
        return items([
          {
            'id': 'zatiti-verifier',
            'version': 1,
            'kind': 'artifact',
            'classification': 'internal',
          },
        ]);
      case 'organization.create':
        final def = r.input['definition']! as Map<String, Object?>;
        final chiefDef = r.input['chief']! as Map<String, Object?>;
        final orgId = _nextId();
        final chiefId = _nextId();
        final draftId = _nextId();
        final orgJson = {
          'id': orgId,
          'version': 1,
          'key': def['key'],
          'name': def['name'],
          'chief_id': chiefId,
          if (def['parent_id'] != null) 'parent_id': def['parent_id'],
        };
        final chiefJson = _workerJson(
          chiefId,
          orgId,
          chiefDef['key']! as String,
          chiefDef['name']! as String,
        );
        _drafts[draftId] = _Draft(orgJson, chiefJson);
        return completed(
          jsonEncode({
            'draft': {
              'id': draftId,
              'version': 1,
              'base_revision': 1,
              'changes': [
                {'kind': 'organization'},
                {'kind': 'worker'},
              ],
              'diagnostics': <Object?>[],
            },
            'resource': orgJson,
          }),
        );
      case 'configuration.plan':
        final draftId = r.input['draft_id']! as String;
        final planId = _nextId();
        _plans[planId] = draftId;
        return completed(
          jsonEncode({
            'resource': {
              'id': planId,
              'version': 1,
              'draft_id': draftId,
              'base_revision': 1,
              'candidate_digest': 'd' * 64,
              'changes': <Object?>[],
              'dependencies': <Object?>[],
              'compiler_version': 'fixture',
              'schema_version': 'fixture',
              'authority_requirements': <Object?>[],
              'decisions': <Object?>[],
              'diagnostics': <Object?>[],
              'requirements': <Object?>[],
            },
          }),
        );
      case 'configuration.apply':
        final planId = r.input['plan_id']! as String;
        final draft = _drafts[_plans[planId]]!;
        organizations.add(draft.orgJson);
        workers.add(draft.chiefJson);
        return completed(
          jsonEncode({
            'resource': {
              'id': _nextId(),
              'version': 1,
              'plan_id': planId,
              'candidate_digest': 'd' * 64,
              'activated_at': '2026-09-18T12:00:00Z',
            },
          }),
        );
      case 'conversation.create':
        final id = _nextId();
        final convo = {
          'id': id,
          'version': 1,
          'scope': _scope(),
          'kind': r.input['kind'],
          'participant_ids': r.input['participant_ids'],
          'title': r.input['title'],
          'pinned': false,
        };
        conversations.add(convo);
        return completed(jsonEncode({'resource': convo}));
      case 'task.create':
        final def = r.input['definition']! as Map<String, Object?>;
        final id = _nextId();
        final task = {
          'id': id,
          'version': 1,
          'scope': def['scope'],
          'owner_id': def['owner_id'],
          'worker_id': def['worker_id'],
          'outcome': def['outcome'],
          'inputs': <Object?>[],
          'required_outputs': def['required_outputs'],
          'acceptance': def['acceptance'],
          'limits': def['limits'],
          'dependencies': <Object?>[],
          'state': 'draft',
          'manual_acceptance': true,
        };
        tasks.add(task);
        return completed(jsonEncode({'resource': task}));
      case 'task.start':
        final id = r.input['id']! as String;
        final index = tasks.indexWhere((t) => t['id'] == id);
        final started = Map<String, Object?>.from(tasks[index])
          ..['version'] = (tasks[index]['version']! as int) + 1
          ..['state'] = 'ready';
        tasks[index] = started;
        return completed(
          jsonEncode({
            'task': started,
            'run': {
              'id': _nextId(),
              'version': 1,
              'task_id': id,
              'configuration_revision': 1,
              'input_versions': <Object?>[],
              'state': 'ready',
              'attempt_ids': <Object?>[],
            },
          }),
        );
      case 'task.accept':
        final id = r.input['id']! as String;
        final index = tasks.indexWhere((t) => t['id'] == id);
        final decision = r.input['decision'] as String;
        final decided = Map<String, Object?>.from(tasks[index])
          ..['version'] = (tasks[index]['version']! as int) + 1
          ..['state'] = decision == 'accept' ? 'succeeded' : 'failed';
        tasks[index] = decided;
        return completed(jsonEncode({'resource': decided}));
      default:
        return items([]);
    }
  }
}

void main() {
  late FakeController fake;
  late _CreationWorld world;
  late WorkspaceController c;
  final now = DateTime.utc(2026, 9, 22, 12);

  setUp(() async {
    fake = await FakeController.start();
    world = _CreationWorld();
    fake.script = world.handle;
    final client = ControllerClient(
      endpoint: LocalSocketEndpoint(fake.socketPath),
      installationId: testInstallationId,
      credentials: () async => 'Bearer test-fixture-credential',
      timeout: const Duration(seconds: 5),
    );
    c = WorkspaceController(
      LiveWorkspaceSource(client, clock: () => now),
      clock: () => now,
    );
    await c.start();
  });

  tearDown(() => fake.stop());

  test('a clean user creates two child chiefs, starts a task and receives a '
      'human-verified output', () async {
    expect(c.connection, ConnectionPhase.online);
    expect(c.snapshot.workers, hasLength(1), reason: 'only the root chief');

    // The apply gate is real: with no draft ever staged, applying does
    // nothing. If the phase guard in `applyDraftFlow` were ever removed,
    // this would start sending `configuration.apply` with no plan behind
    // it.
    await c.applyDraftFlow(DraftFlowKind.organization);
    expect(world.calls, isNot(contains('configuration.apply')));

    // Two child organizations, each its own chief: the real
    // draft → plan → review → apply sequence, not a shortcut.
    await c.createOrganization(
      key: 'marketing',
      name: 'Marketing',
      parentOrganizationId: _rootOrg,
      chiefKey: 'marketing-chief',
      chiefName: 'Marketing chief',
      chiefPurpose: 'Coordinates marketing',
      chiefInstructions: 'Coordinate marketing work.',
    );
    expect(c.draftFlow(DraftFlowKind.organization).phase, DraftFlowPhase.ready);
    await c.applyDraftFlow(DraftFlowKind.organization);
    expect(
      c.draftFlow(DraftFlowKind.organization).phase,
      DraftFlowPhase.applied,
    );

    await c.createOrganization(
      key: 'engineering',
      name: 'Engineering',
      parentOrganizationId: _rootOrg,
      chiefKey: 'engineering-chief',
      chiefName: 'Engineering chief',
      chiefPurpose: 'Coordinates engineering',
      chiefInstructions: 'Coordinate engineering work.',
    );
    await c.applyDraftFlow(DraftFlowKind.organization);
    expect(
      c.draftFlow(DraftFlowKind.organization).phase,
      DraftFlowPhase.applied,
    );

    // Two real child chiefs now exist, each reachable in the built app: a
    // real conversation was opened, not merely a listed identity.
    final chiefs = [
      for (final w in c.snapshot.workers)
        if (w.isOrganizationChief && w.parentId != null) w,
    ];
    expect(
      chiefs.map((w) => w.name),
      containsAll(['Marketing chief', 'Engineering chief']),
    );
    for (final chief in chiefs) {
      expect(
        chief.conversationId,
        isNotNull,
        reason: '${chief.name} must have a real conversation open',
      );
    }
    expect(world.calls.where((o) => o == 'organization.create'), hasLength(2));
    expect(world.calls.where((o) => o == 'configuration.plan'), hasLength(2));
    expect(world.calls.where((o) => o == 'configuration.apply'), hasLength(2));
    expect(
      world.calls.where((o) => o == 'conversation.create'),
      hasLength(2),
      reason: 'one direct conversation per new chief',
    );

    // Start bounded work under the new marketing chief.
    final marketingChief = chiefs.firstWhere(
      (w) => w.name == 'Marketing chief',
    );
    final verifiers = await c.loadTrustedVerifiers();
    await c.createTask(
      workerId: marketingChief.id.value,
      outcome: 'Draft the September newsletter',
      requiredOutputs: const ['newsletter.md'],
      verifier: verifiers.single,
    );
    expect(c.taskFlow.phase, TaskFlowPhase.started);
    final task = c.tasksFor(marketingChief.id).single;
    expect(task.manualAcceptance, isTrue);
    expect(task.state, TaskEntryState.inProgress);

    // "Receives a verified output": the eligible human's own decision —
    // never an automated label — establishes success.
    await c.decideTask(task, accept: true);
    final decided = c.tasksFor(marketingChief.id).single;
    expect(decided.state, TaskEntryState.completed);
    expect(world.calls, contains('task.accept'));
  });

  test('closing the app and reopening it restores a running task; nothing is '
      'cancelled', () async {
    await c.createOrganization(
      key: 'ops',
      name: 'Operations',
      parentOrganizationId: _rootOrg,
      chiefKey: 'ops-chief',
      chiefName: 'Operations chief',
      chiefPurpose: 'Coordinates operations',
      chiefInstructions: 'Coordinate operations work.',
    );
    await c.applyDraftFlow(DraftFlowKind.organization);
    final chief = c.snapshot.workers.firstWhere(
      (w) => w.name == 'Operations chief',
    );
    final verifiers = await c.loadTrustedVerifiers();
    await c.createTask(
      workerId: chief.id.value,
      outcome: 'Reconcile this week\'s expenses',
      requiredOutputs: const ['ledger.csv'],
      verifier: verifiers.single,
    );
    expect(c.taskFlow.phase, TaskFlowPhase.started);
    expect(c.tasksFor(chief.id).single.state, TaskEntryState.inProgress);

    // "Closing desktop": this client's own controller is discarded. No
    // pause/cancel operation is ever sent for a plain window close.
    c.dispose();
    expect(
      world.calls,
      isNot(anyElement(anyOf('task.cancel', 'responsibility.pause'))),
    );

    // "Reopening restores them": a brand new controller, same real
    // installation state, shows the same running task without this test
    // replaying anything by hand.
    final reopened = WorkspaceController(
      LiveWorkspaceSource(
        ControllerClient(
          endpoint: LocalSocketEndpoint(fake.socketPath),
          installationId: testInstallationId,
          credentials: () async => 'Bearer test-fixture-credential',
          timeout: const Duration(seconds: 5),
        ),
        clock: () => now,
      ),
      clock: () => now,
    );
    await reopened.start();
    final restoredChief = reopened.snapshot.workers.firstWhere(
      (w) => w.name == 'Operations chief',
    );
    final restoredTask = reopened.tasksFor(restoredChief.id).single;
    expect(restoredTask.state, TaskEntryState.inProgress);
    expect(restoredTask.title, 'Reconcile this week\'s expenses');
  });
}
