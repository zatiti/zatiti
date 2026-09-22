// Setup/prerequisite cards (provider, currency, credential action) and
// unresolved external operations: real facts read from `worker.list`'s
// `profile`/`limits`, `connection.list`'s `validation_state`, and
// `operation.list` entries no review names — never invented, never
// silently dropped.

import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:zatiti_desktop/src/state/live_source.dart';
import 'package:zatiti_desktop/src/state/snapshot.dart';
import 'package:zatiti_desktop/src/transport/controller_client.dart';
import 'package:zatiti_desktop/src/transport/endpoint.dart';
import 'package:zatiti_desktop/src/transport/operations.dart';

import '../support/fake_controller.dart';

String _id(int n) => '00000000-0000-4000-8000-${n.toString().padLeft(12, '0')}';

final _rootOrg = _id(1);
final _configuredChief = _id(2);
final _unconfiguredChief = _id(3);
final _human = _id(4);
final _connection = _id(5);
final _toolId = _id(6);

void main() {
  late FakeController fake;

  setUp(() async {
    fake = await FakeController.start();
  });

  tearDown(() => fake.stop());

  Future<LiveWorkspaceSource> open(
    Future<Reply> Function(RecordedRequest) script,
  ) async {
    fake.script = script;
    final client = ControllerClient(
      endpoint: LocalSocketEndpoint(fake.socketPath),
      installationId: testInstallationId,
      credentials: () async => 'Bearer test-fixture-credential',
      timeout: const Duration(seconds: 5),
    );
    return LiveWorkspaceSource(client, endpointLabel: 'fixture');
  }

  Map<String, Object?> workerJson({
    required String id,
    required String name,
    Map<String, Object?>? profile,
    Map<String, Object?>? limits,
  }) => {
    'id': id,
    'version': 1,
    'organization_id': _rootOrg,
    'key': name.toLowerCase(),
    'name': name,
    'purpose': 'Coordinates $name',
    'instructions': '',
    'skill_versions': <Object?>[],
    'bindings': <Object?>[],
    'profile': profile,
    'limits': limits,
  };

  Map<String, Object?> configuredProfile() => {
    'id': _id(7),
    'version': 1,
    'executor': 'hosted',
    'model': 'gpt-fixture',
    'connection_id': _connection,
    'provider_destination': 'api.example.com',
    'capabilities': <Object?>[],
    'cost_bound': {'currency': 'USD', 'micro_units': 0},
    'classification': 'internal',
    'context_capture': 'complete',
  };

  Map<String, Object?> configuredLimits() => {
    'currency': 'USD',
    'spend_micro_units': 1000000,
    'concurrency': 1,
    'model_steps': 10,
    'child_count': 0,
    'delegation_depth': 0,
    'attempt_seconds': 60,
    'root_deadline': '2027-01-01T00:00:00Z',
  };

  Future<Reply> baseline(
    RecordedRequest r,
    List<Object?> Function(String) list,
  ) async {
    final op = r.path.substring('/v1/operations/'.length);
    Reply items(List<Object?> l) => completed(jsonEncode({'items': l}));
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
        return items([
          {
            'id': _rootOrg,
            'version': 1,
            'key': 'personal',
            'name': 'Personal',
            'chief_id': _configuredChief,
          },
        ]);
      case 'worker.list':
        return items(list('worker.list'));
      case 'principal.list':
        return items([
          {
            'id': _human,
            'version': 1,
            'kind': 'human',
            'name': 'You',
            'scope': {'installation_id': testInstallationId},
            'revoked': false,
          },
        ]);
      case 'usage.get':
        return completed(
          jsonEncode({
            'resource': {
              'currency': 'USD',
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
              'root_deadline': '2027-01-01T00:00:00Z',
            },
          }),
        );
      default:
        return items(list(op));
    }
  }

  test('a worker with no execution profile shows a real setup card', () async {
    final source = await open((r) async {
      return baseline(r, (op) {
        if (op == 'worker.list') {
          return [
            // A configured budget isolates this test to the provider gap
            // alone; a null profile is the fact under test.
            workerJson(
              id: _configuredChief,
              name: 'Wren',
              limits: configuredLimits(),
            ),
          ];
        }
        return const [];
      });
    });
    final snapshot = await source.loadSnapshot();

    final notice = snapshot.prerequisites.singleWhere(
      (p) => p.workerId == WorkerId(_configuredChief),
    );
    expect(notice.title, contains('No provider configured'));
    expect(notice.tab, DetailsTab.work);
  });

  test('a fully configured worker with a valid connection shows no provider '
      'or budget setup card', () async {
    final source = await open((r) async {
      return baseline(r, (op) {
        if (op == 'worker.list') {
          return [
            workerJson(
              id: _configuredChief,
              name: 'Wren',
              profile: configuredProfile(),
              limits: configuredLimits(),
            ),
          ];
        }
        if (op == 'connection.list') {
          return [
            {
              'id': _connection,
              'version': 1,
              'scope': {'installation_id': testInstallationId},
              'provider': 'example',
              'account_identity': 'wren@example.com',
              'credential_ref': 'ref-1',
              'destinations': <Object?>[],
              'allowed_scopes': <Object?>[],
              'validation_state': 'valid',
            },
          ];
        }
        return const [];
      });
    });
    final snapshot = await source.loadSnapshot();

    expect(
      snapshot.prerequisites.where(
        (p) => p.workerId == WorkerId(_configuredChief),
      ),
      isEmpty,
    );
  });

  test('a worker whose connection needs action shows a real credential card, '
      'not a provider gap', () async {
    final source = await open((r) async {
      return baseline(r, (op) {
        if (op == 'worker.list') {
          return [
            workerJson(
              id: _unconfiguredChief,
              name: 'Ledger',
              profile: configuredProfile(),
              limits: configuredLimits(),
            ),
          ];
        }
        if (op == 'connection.list') {
          return [
            {
              'id': _connection,
              'version': 1,
              'scope': {'installation_id': testInstallationId},
              'provider': 'example',
              'account_identity': 'ledger@example.com',
              'credential_ref': 'ref-1',
              'destinations': <Object?>[],
              'allowed_scopes': <Object?>[],
              'validation_state': 'expired',
            },
          ];
        }
        return const [];
      });
    });
    final snapshot = await source.loadSnapshot();

    final notice = snapshot.prerequisites.singleWhere(
      (p) => p.workerId == WorkerId(_unconfiguredChief),
    );
    expect(notice.title, contains('Credential needs action'));
    expect(notice.message, contains('expired'));
  });

  test('an external operation no review names stays visible while unsettled, '
      'and drops once it settles', () async {
    final action = {
      'scope': {
        'installation_id': testInstallationId,
        'worker_id': _configuredChief,
      },
      'tool': {'id': _toolId, 'version': 1},
      'connection': {'id': _connection, 'version': 1},
      'account_identity': 'wren@example.com',
      'destination': 'mailbox.example.com',
      'content': <Object?>[],
      'not_before': '2026-09-18T10:00:00Z',
      'expires_at': '2026-09-18T18:00:00Z',
      'preconditions': <String, Object?>{},
      'configuration_revision': 1,
      'parameters': <String, Object?>{},
      'cost_bound': {'currency': 'USD', 'micro_units': 0},
    };
    Map<String, Object?> op(String id, String digest, String state) => {
      'id': id,
      'version': 1,
      'action': action,
      'action_digest': digest,
      'state': state,
      'attempt_ids': <Object?>[],
    };

    final source = await open((r) async {
      return baseline(r, (o) {
        if (o == 'worker.list') {
          return [workerJson(id: _configuredChief, name: 'Wren')];
        }
        if (o == 'operation.list') {
          return [
            op(_id(100), 'a' * 64, 'executing'),
            op(_id(101), 'b' * 64, 'succeeded'),
          ];
        }
        return const [];
      });
    });
    final snapshot = await source.loadSnapshot();

    expect(snapshot.unresolvedOperations, hasLength(1));
    final entry = snapshot.unresolvedOperations.single;
    expect(entry.workerId, WorkerId(_configuredChief));
    expect(entry.state, EffectState.executing);
    expect(entry.destination, 'mailbox.example.com');
  });
}
