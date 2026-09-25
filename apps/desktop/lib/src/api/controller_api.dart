// Typed calls over the controller client. Inputs follow the catalog's input
// schemas; outputs decode strictly into the wire models.

import '../transport/controller_client.dart';
import '../transport/operations.dart';
import '../transport/strict_json.dart';
import '../transport/submission.dart';
import 'acceptance.dart';
import 'models.dart';

/// Pages a list operation reads at most, so one snapshot stays bounded.
const int maxListPages = 25;

/// One message that has been rendered but not yet sent, with the identity it
/// will carry once the controller acknowledges it.
class PreparedMessage {
  const PreparedMessage({required this.messageId, required this.submission});

  final String messageId;
  final Submission submission;
}

class ControllerApi {
  ControllerApi(this.client);

  final ControllerClient client;

  /// The public operation catalog the controller serves.
  Future<List<Capability>> capabilities() async {
    final r = await client.query(Operations.capabilitiesList, {
      'scope': client.scope(),
    });
    final data = StrictObject(
      r.requireData('capabilities.list'),
      'capabilities',
    );
    final items = data.list('items').map(Capability.fromJson).toList();
    data.optionalString('next_cursor');
    data.finish();
    return items;
  }

  Future<InstallationStatus> installationStatus() async {
    final r = await client.query(Operations.installationStatus, {
      'scope': client.scope(),
    });
    return InstallationStatus.fromJson(
      _resource(r.data, 'installation.status'),
    );
  }

  /// Reads every page of a list operation, following `next_cursor`.
  Future<List<T>> listAll<T>(
    OperationDescriptor operation,
    T Function(Object?) decode, {
    Map<String, Object?>? filter,
    Map<String, Object?> extra = const {},
    String? startCursor,
    void Function(String cursor)? onCursor,
  }) async {
    final out = <T>[];
    var cursor = startCursor;
    for (var page = 0; page < maxListPages; page++) {
      final r = await client.query(operation, {
        'scope': client.scope(),
        'limit': 200,
        'cursor': ?cursor,
        'filter': ?filter,
        ...extra,
      });
      final data = StrictObject(r.requireData(operation.id), operation.id);
      out.addAll(data.list('items').map(decode));
      // The envelope carries next_cursor. internal/messaging also places one
      // inside data, which the catalog's output schema does not declare; it
      // is accepted here so conversation and mailbox pages are not lost.
      final inData = data.optionalString('next_cursor');
      data.finish();
      final next = r.nextCursor ?? inData;
      if (next == null) break;
      cursor = next;
      onCursor?.call(next);
    }
    return out;
  }

  /// Reads a conversation's authorized message history, oldest first. Only
  /// participant-disclosed history is returned; a newly joined participant
  /// sees no retroactive restricted history (the controller enforces this,
  /// not this client).
  Future<List<Message>> listMessages(
    String conversationId, {
    String? startCursor,
    void Function(String cursor)? onCursor,
  }) => listAll(
    Operations.conversationMessageList,
    Message.fromJson,
    extra: {'conversation_id': conversationId},
    startCursor: startCursor,
    onCursor: onCursor,
  );

  Future<Review> reviewGet(String id) async {
    final r = await client.query(Operations.reviewGet, {
      'scope': client.scope(),
      'id': id,
    });
    return Review.fromJson(_resource(r.data, 'review.get'));
  }

  /// A tool's display name. Only the name is read: it labels the review and
  /// is not part of the exact content a decision binds to.
  Future<String> toolName(String id) async {
    final r = await client.query(Operations.toolGet, {
      'scope': client.scope(),
      'id': id,
    });
    return StrictObject(_resource(r.data, 'tool.get'), 'tool').string('name');
  }

  Future<Usage> usage({required String workerId}) async {
    final r = await client.query(Operations.usageGet, {
      'scope': client.scope(workerId: workerId),
    });
    return Usage.fromJson(_resource(r.data, 'usage.get'));
  }

  /// The effective spend/step ceiling for one scope: installation, ancestor,
  /// project, worker and root limits already intersected by the controller —
  /// never recomputed locally from a worker's own possibly-narrower
  /// `Worker.limits`.
  Future<Limits> budget({required String workerId}) async {
    final r = await client.query(Operations.budgetGet, {
      'scope': client.scope(workerId: workerId),
    });
    final o = StrictObject(r.requireData('budget.get'), 'budget.get');
    final limits = Limits.fromJson(o.object('limits'));
    o.finish();
    return limits;
  }

  // ---- memory: authorized claims, source/freshness and retraction --------

  Future<List<MemoryBinding>> memoryBindings() =>
      listAll(Operations.memoryBindingList, MemoryBinding.fromJson);

  /// The authorized claims for exactly the bindings named, never a paid
  /// retrieval: `memory.list` only ever reads what is already recorded.
  Future<List<Claim>> memoryClaims(List<String> bindingIds) {
    if (bindingIds.isEmpty) return Future.value(const []);
    return listAll(
      Operations.memoryList,
      Claim.fromJson,
      extra: {'binding_ids': bindingIds},
    );
  }

  /// Freezes a retraction bound to the exact claim version this view saw.
  /// Retraction is a `Job`: the acknowledgment rule applies to its `state`
  /// exactly as it does to a review or a pause, never to submission alone.
  Submission prepareMemoryRetract({
    required String brainId,
    required String claimId,
    required int claimVersion,
    required String reason,
  }) => client.prepare(Operations.memoryRetract, {
    'scope': client.scope(),
    'brain_id': brainId,
    'claim': {'id': claimId, 'version': claimVersion},
    'reason': reason,
  });

  /// Looks up a retraction job whose acknowledgment was unknown, or whose
  /// last known state was still `pending`/`running`.
  Future<Job> memoryJobGet(String id) async {
    final r = await client.query(Operations.memoryJobGet, {
      'scope': client.scope(),
      'id': id,
    });
    return Job.fromJson(_resource(r.data, 'memory.job.get'));
  }

  // ---- responsibility-to-schedule links -----------------------------------

  Future<List<Schedule>> schedules() =>
      listAll(Operations.scheduleList, Schedule.fromJson);

  // ---- autonomy evidence and recovery obligations -------------------------

  Future<List<Qualification>> autonomyQualifications() =>
      listAll(Operations.autonomyQualificationList, Qualification.fromJson);

  Future<List<Run>> runs() => listAll(Operations.runList, Run.fromJson);

  /// Generation, leases, conflicting resources and effect obligations this
  /// run still carries — read before any replacement, never invented from
  /// the run's bare state.
  Future<List<Requirement>> runRecovery(String runId) async {
    final r = await client.query(Operations.runRecovery, {
      'scope': client.scope(),
      'id': runId,
    });
    final o = StrictObject(r.requireData('run.recovery'), 'run.recovery');
    Run.fromJson(o.object('resource'));
    final obligations = [
      for (final req in o.list('obligations')) Requirement.fromJson(req),
    ];
    o.finish();
    return obligations;
  }

  /// Reads up to 1 MiB from the start of an artifact.
  Future<ArtifactRead> artifactRead(String id) async {
    final r = await client.query(Operations.artifactRead, {
      'scope': client.scope(),
      'id': id,
      'offset': 0,
      'length': 1048576,
    });
    return ArtifactRead.fromJson(r.requireData('artifact.read'));
  }

  /// A decision bound to the exact review version and action digest.
  Submission prepareReviewDecide({
    required String reviewId,
    required int expectedVersion,
    required String actionDigest,
    required DecisionChoice decision,
  }) => client.prepare(Operations.reviewDecide, {
    'scope': client.scope(),
    'id': reviewId,
    'expected_version': expectedVersion,
    'action_digest': actionDigest,
    'decision': decision.wire,
    'reason': '',
  });

  /// A message with its identity fixed now, so a retry cannot duplicate it.
  /// The controller keeps the `message_id` the client mints and returns it as
  /// the message's own id, so the caller holds the identity its message will
  /// have and can recognize it if it comes back from a mailbox.
  PreparedMessage prepareMessageSend({
    required String conversationId,
    required String body,
  }) {
    final messageId = newUuidV4();
    return PreparedMessage(
      messageId: messageId,
      submission: client.prepare(Operations.conversationMessageSend, {
        'scope': client.scope(),
        'conversation_id': conversationId,
        'message_id': messageId,
        'body': body,
        'attachments': const <Object?>[],
        'task_ids': const <Object?>[],
      }),
    );
  }

  Submission prepareResponsibilityPause({
    required String id,
    required int expectedVersion,
  }) => client.prepare(Operations.responsibilityPause, {
    'scope': client.scope(),
    'id': id,
    'expected_version': expectedVersion,
  });

  // ---- organization/worker/group/task/responsibility creation -----------

  Future<List<VerifierDescriptor>> trustedVerifiers() =>
      listAll(Operations.installationVerifierList, VerifierDescriptor.fromJson);

  Future<List<Connection>> connections() =>
      listAll(Operations.connectionList, Connection.fromJson);

  Future<List<ProviderDescriptor>> modelProviders() async {
    final r = await client.query(Operations.modelProviderList, {
      'scope': client.scope(),
    });
    final data = StrictObject(
      r.requireData('model.provider.list'),
      'model.provider.list',
    );
    final providers = data
        .list('items')
        .map(ProviderDescriptor.fromJson)
        .toList();
    data.finish();
    return providers;
  }

  Future<List<ExecutionProfile>> executionProfiles() =>
      listAll(Operations.executionProfileList, ExecutionProfile.fromJson);

  Submission prepareProviderConnection({
    required ProviderDescriptor provider,
    required String accountIdentity,
  }) {
    final scope = client.scope();
    return client.prepare(Operations.connectionCreate, {
      'scope': scope,
      'definition': {
        'scope': scope,
        'provider': provider.id,
        'account_identity': accountIdentity.trim(),
        // A reference only. The raw API key is entered later through the
        // signed local helper and never enters this request.
        'credential_ref': 'connections/credentials/${newUuidV4()}',
        'destinations': [provider.defaultEndpoint],
        'allowed_scopes': const <String>[],
      },
    });
  }

  Future<Worker> workerGet(String id) async {
    final r = await client.query(Operations.workerGet, {
      'scope': client.scope(),
      'id': id,
    });
    return Worker.fromJson(_resource(r.data, 'worker.get'));
  }

  Submission prepareWorkerProfileUpdate({
    required Worker worker,
    required ExecutionProfile profile,
  }) => client.prepare(Operations.workerUpdate, {
    'scope': client.scope(organizationId: worker.organizationId),
    'id': worker.id,
    'expected_version': worker.version,
    'definition': worker.definitionWithProfile(profile),
  });

  Future<Connection> connectionGet(String id) async {
    final response = await client.query(Operations.connectionGet, {
      'scope': client.scope(),
      'id': id,
    });
    return Connection.fromJson(_resource(response.data, 'connection.get'));
  }

  Future<List<Artifact>> taskArtifacts(String taskId) => listAll(
    Operations.artifactList,
    Artifact.fromJson,
    filter: {'task_id': taskId},
  );

  /// Stages an organization and its chief in one draft (see
  /// `handleOrganizationCreate` in `internal/configuration/handlers.go`:
  /// both changes land in the same bundle, so the atomic chief-creation
  /// invariant holds at apply).
  Submission prepareOrganizationCreate({
    required String key,
    required String name,
    String? parentOrganizationId,
    required String chiefKey,
    required String chiefName,
    required String chiefPurpose,
    required String chiefInstructions,
  }) => client.prepare(Operations.organizationCreate, {
    'scope': client.scope(),
    'definition': {
      'key': key,
      'name': name,
      'parent_id': ?parentOrganizationId,
    },
    'chief': {
      'key': chiefKey,
      'name': chiefName,
      'purpose': chiefPurpose,
      'instructions': chiefInstructions,
      'skill_versions': const <Object?>[],
      'bindings': const <Object?>[],
      'profile': null,
      'limits': null,
    },
  });

  Submission prepareWorkerCreate({
    required String organizationId,
    required String key,
    required String name,
    required String purpose,
    required String instructions,
  }) => client.prepare(Operations.workerCreate, {
    'scope': client.scope(organizationId: organizationId),
    'definition': {
      'organization_id': organizationId,
      'key': key,
      'name': name,
      'purpose': purpose,
      'instructions': instructions,
      'skill_versions': const <Object?>[],
      'bindings': const <Object?>[],
      'profile': null,
      'limits': null,
    },
  });

  Submission prepareResponsibilityCreate({
    required String workerId,
    required String outcome,
    required List<String> triggers,
    required int minIntervalSeconds,
    required VerifierDescriptor verifier,
    required DateTime rootDeadline,
    String currency = 'XXX',
  }) {
    final scope = client.scope(workerId: workerId);
    return client.prepare(Operations.responsibilityCreate, {
      'scope': scope,
      'definition': {
        'scope': scope,
        'worker_id': workerId,
        'outcome': outcome,
        'signals': const <Object?>[],
        'triggers': triggers,
        'reasoning_policy':
            'Decide only when a listed signal or trigger actually changed.',
        'min_interval_seconds': minIntervalSeconds,
        'cycle_limits': zeroSpendLimits(
          rootDeadline: rootDeadline,
          currency: currency,
        ),
        'aggregate_limits': zeroSpendLimits(
          rootDeadline: rootDeadline,
          currency: currency,
        ),
        'pause_conditions': const <Object?>[],
        'escalation_conditions': const <Object?>[],
        'acceptance': manualAcceptance(verifier),
        'paused': false,
      },
    });
  }

  Submission preparePlan({
    required String draftId,
    required int expectedVersion,
  }) => client.prepare(Operations.configurationPlan, {
    'scope': client.scope(),
    'draft_id': draftId,
    'expected_version': expectedVersion,
  });

  Submission prepareApplyPlan({
    required String planId,
    required int baseRevision,
    required String candidateDigest,
  }) => client.prepare(Operations.configurationApply, {
    'scope': client.scope(),
    'plan_id': planId,
    'base_revision': baseRevision,
    'candidate_digest': candidateDigest,
  });

  Submission prepareConversationCreate({
    required String kind,
    required List<String> participantIds,
    required String title,
  }) => client.prepare(Operations.conversationCreate, {
    'scope': client.scope(),
    'kind': kind,
    'participant_ids': participantIds,
    'title': title,
  });

  Submission prepareConversationUpdate({
    required String id,
    required int expectedVersion,
    List<String>? participantIds,
    String? title,
  }) => client.prepare(Operations.conversationUpdate, {
    'scope': client.scope(),
    'id': id,
    'expected_version': expectedVersion,
    'participant_ids': ?participantIds,
    'title': ?title,
  });

  Map<String, Object?> _taskDefinition({
    required String workerId,
    required String ownerId,
    required String outcome,
    required List<String> requiredOutputs,
    required VerifierDescriptor verifier,
    required DateTime rootDeadline,
    String currency = 'XXX',
  }) {
    final scope = client.scope(workerId: workerId);
    return {
      'scope': scope,
      'owner_id': ownerId,
      'worker_id': workerId,
      'outcome': outcome,
      'inputs': const <Object?>[],
      'required_outputs': requiredOutputs,
      'acceptance': manualAcceptance(verifier),
      'limits': zeroSpendLimits(rootDeadline: rootDeadline, currency: currency),
      'dependencies': const <Object?>[],
    };
  }

  Submission prepareTaskCreate({
    required String ownerId,
    required String workerId,
    required String outcome,
    required List<String> requiredOutputs,
    required VerifierDescriptor verifier,
    required DateTime rootDeadline,
    String currency = 'XXX',
  }) => client.prepare(Operations.taskCreate, {
    'scope': client.scope(workerId: workerId),
    'definition': _taskDefinition(
      workerId: workerId,
      ownerId: ownerId,
      outcome: outcome,
      requiredOutputs: requiredOutputs,
      verifier: verifier,
      rootDeadline: rootDeadline,
      currency: currency,
    ),
  });

  Submission prepareTaskStart({
    required String id,
    required int expectedVersion,
  }) => client.prepare(Operations.taskStart, {
    'scope': client.scope(),
    'id': id,
    'expected_version': expectedVersion,
  });

  Submission prepareTaskDelegate({
    required String id,
    required int expectedVersion,
    required String ownerId,
    required String childWorkerId,
    required String outcome,
    required List<String> requiredOutputs,
    required VerifierDescriptor verifier,
    required DateTime rootDeadline,
    String currency = 'XXX',
  }) => client.prepare(Operations.taskDelegate, {
    'scope': client.scope(),
    'id': id,
    'expected_version': expectedVersion,
    'child': _taskDefinition(
      workerId: childWorkerId,
      ownerId: ownerId,
      outcome: outcome,
      requiredOutputs: requiredOutputs,
      verifier: verifier,
      rootDeadline: rootDeadline,
      currency: currency,
    ),
  });

  Submission prepareTaskAccept({
    required String id,
    required int expectedVersion,
    required bool accept,
    String reason = '',
  }) => client.prepare(Operations.taskAccept, {
    'scope': client.scope(),
    'id': id,
    'expected_version': expectedVersion,
    'decision': accept ? 'accept' : 'reject',
    'evidence_ids': const <Object?>[],
    'reason': reason,
  });

  Object? _resource(Map<String, Object?>? data, String operation) {
    if (data == null) throw StrictJsonException('$operation returned no data');
    final o = StrictObject(data, operation);
    final resource = o.object('resource');
    o.finish();
    return resource;
  }
}
