// The live workspace source: maps controller operations to the workspace
// snapshot. Nothing here decides anything. Where the public operation catalog
// offers no operation for a designed surface, the snapshot carries a
// prerequisite notice and the surface renders its designed empty state.

import 'dart:convert';

import '../api/controller_api.dart';
import '../api/models.dart' as wire;
import '../app/local_store.dart';
import '../transport/controller_client.dart';
import '../transport/envelope.dart';
import '../transport/errors.dart';
import '../transport/operations.dart';
import '../transport/strict_json.dart';
import '../transport/submission.dart';
import 'installed_chief.dart';
import 'snapshot.dart';
import 'workspace_source.dart';

class _LiveSubmission implements PendingSubmission {
  _LiveSubmission(this.submission);

  final Submission submission;

  @override
  String get description => submission.operation;
}

/// A [ResourceSubmission] whose acknowledged (or `command.get`-resolved)
/// response is decoded into [T] as a side effect of [LiveWorkspaceSource.
/// submit]/[LiveWorkspaceSource.resolve] — never guessed, never set from the
/// request the client sent.
class _LiveResourceSubmission<T> implements ResourceSubmission<T> {
  _LiveResourceSubmission(this.submission, this.decode, this._description);

  final Submission submission;
  final T Function(Map<String, Object?> data) decode;
  final String _description;

  @override
  T? result;

  @override
  String get description => _description;
}

/// What this source knows about one conversation from the last snapshot,
/// enough to interpret a message's authorship without ever querying "who am
/// I": the catalog has no such operation (see the class doc below).
class _ConversationMeta {
  const _ConversationMeta({required this.kind, this.workerId});
  final wire.ConversationKind kind;
  final String? workerId;
}

class LiveWorkspaceSource implements WorkspaceSource {
  /// [initialEventCursor]/[initialLastSequence] resume event replay across a
  /// restart instead of re-baselining to "now", which would silently skip
  /// whatever happened while the app was closed; the caller reads them from
  /// [LocalStore] once at startup, before this source exists. [localStore]
  /// (optional; omitted in tests) is where this source persists the cursor
  /// as it advances, read-modify-write against the same file the controller
  /// persists its own selection/cache fields to.
  LiveWorkspaceSource(
    ControllerClient client, {
    String? endpointLabel,
    DateTime Function()? clock,
    String? initialEventCursor,
    int initialLastSequence = 0,
    LocalStore? localStore,
    this.installedMac = false,
  }) : api = ControllerApi(client),
       _clock = clock ?? (() => DateTime.now().toUtc()),
       label = endpointLabel ?? 'Controller',
       _eventCursor = initialEventCursor,
       _lastSequence = initialLastSequence,
       _localStore = localStore,
       _installationId = client.installationId;

  final ControllerApi api;
  final bool installedMac;

  final DateTime Function() _clock;
  final Map<String, String> _toolNames = {};
  final Map<String, _ConversationMeta> _conversationMeta = {};

  /// Every known identity's display name, from the last `principal.list`
  /// read. Used to label a group message's real sender (see [_chatMessage]);
  /// never used to guess which identity is "me".
  final Map<String, String> _principalNames = {};
  String? _eventCursor;
  int _lastSequence;
  final LocalStore? _localStore;
  final String _installationId;

  /// Nonsecret identity already authenticated during installed Mac startup.
  String get installationId => _installationId;

  /// Persists the fully-consistent (cursor, sequence) pair once the page
  /// loop that may have advanced both has finished — never mid-page, so a
  /// persisted pair is never observed half-updated. Read-modify-write: the
  /// controller may hold the same file's selection/cache fields, so this
  /// only ever changes the cursor fields, never overwriting the rest with a
  /// stale copy. Best-effort: a write failure here changes nothing about
  /// event replay, which the in-memory cursor keeps regardless.
  Future<void> _persistCursor() async {
    final cursor = _eventCursor;
    final store = _localStore;
    if (cursor == null || store == null) return;
    try {
      final current = await store.read(_installationId);
      await store.write(
        current.copyWith(
          installationId: _installationId,
          eventCursor: cursor,
          lastSequence: _lastSequence,
        ),
      );
    } on Object {
      // Best-effort, as documented above.
    }
  }

  @override
  SourceKind get kind => SourceKind.controller;

  @override
  final String label;

  // ---- failure mapping ----------------------------------------------------

  Future<T> _guard<T>(Future<T> Function() body) async {
    try {
      return await body();
    } on ControllerUnavailableException catch (e) {
      throw SourceUnavailable(e.message);
    } on TlsCertificateException catch (e) {
      throw SourceUnavailable(
        'The controller’s certificate was not accepted: ${e.message}',
      );
    } on OutcomeUnknownException catch (e) {
      throw AcknowledgmentUnknown(e.message);
    } on OperationFailedException catch (e) {
      throw _refusal(e.fault);
    } on InvalidRequestException catch (e) {
      throw SourceRefusal(RefusalKind.other, e.message);
    } on StrictJsonException catch (e) {
      throw SourceRefusal(
        RefusalKind.other,
        'The controller sent data this version of the app does not '
        'understand: ${e.message}',
      );
    }
  }

  SourceRefusal _refusal(Fault fault) {
    final kind = switch (fault.code) {
      FaultCode.staleVersion ||
      FaultCode.conflict ||
      FaultCode.submissionConflict => RefusalKind.stale,
      FaultCode.prerequisiteMissing ||
      FaultCode.externalActionRequired ||
      FaultCode.budgetUnavailable ||
      FaultCode.capabilityUnsupported ||
      FaultCode.permissionDenied ||
      FaultCode.reviewRequired => RefusalKind.prerequisiteMissing,
      _ => RefusalKind.other,
    };
    return SourceRefusal(kind, fault.message);
  }

  // ---- submissions ----------------------------------------------------------

  @override
  PendingSubmission prepareDecision(
    ReviewEntry review,
    DecisionChoice choice,
  ) => _LiveSubmission(
    api.prepareReviewDecide(
      reviewId: review.id.value,
      expectedVersion: review.version,
      actionDigest: review.actionDigest,
      decision: choice == DecisionChoice.approve
          ? wire.DecisionChoice.approve
          : wire.DecisionChoice.reject,
    ),
  );

  @override
  PendingSubmission prepareMessage(ConversationId conversation, String body) {
    // The controller keeps the message_id this client mints, so once this
    // submission is acknowledged, the next `conversation.message.list` read
    // returns the same message by the same id: the single authoritative
    // copy, not a locally remembered echo.
    final prepared = api.prepareMessageSend(
      conversationId: conversation.value,
      body: body,
    );
    return _LiveSubmission(prepared.submission);
  }

  @override
  PendingSubmission preparePause(RoutineEntry routine) => _LiveSubmission(
    api.prepareResponsibilityPause(
      id: routine.id.value,
      expectedVersion: routine.version,
    ),
  );

  @override
  ResourceSubmission<MemoryRetractOutcome> prepareMemoryRetract(
    MemoryEntry claim,
    String reason,
  ) => _LiveResourceSubmission(
    api.prepareMemoryRetract(
      brainId: claim.brainId,
      claimId: claim.id.value,
      claimVersion: claim.claimVersion,
      reason: reason,
    ),
    (data) {
      final o = StrictObject(data, 'memory.retract');
      final job = wire.Job.fromJson(o.object('resource'));
      o.finish();
      return MemoryRetractOutcome(jobId: job.id, jobStatus: job.label);
    },
    'retract claim ${claim.id.value}',
  );

  @override
  Future<String> checkMemoryJob(String jobId) =>
      _guard(() async => (await api.memoryJobGet(jobId)).label);

  // ---- organization/worker/group/task/responsibility creation -----------

  DraftedResource _draftedResource(
    Map<String, Object?> data,
    String operation,
    (String, String?) Function(Map<String, Object?> resource) idOf,
  ) {
    final o = StrictObject(data, operation);
    final draft = wire.Draft.fromJson(o.object('draft'));
    final (resourceId, conversationTarget) = idOf(o.object('resource'));
    o.finish();
    return DraftedResource(
      draftId: draft.id,
      draftVersion: draft.version,
      resourceId: resourceId,
      conversationTargetId: conversationTarget,
    );
  }

  @override
  ResourceSubmission<DraftedResource> prepareCreateOrganization({
    required String key,
    required String name,
    String? parentOrganizationId,
    required String chiefKey,
    required String chiefName,
    required String chiefPurpose,
    required String chiefInstructions,
  }) => _LiveResourceSubmission(
    api.prepareOrganizationCreate(
      key: key,
      name: name,
      parentOrganizationId: parentOrganizationId,
      chiefKey: chiefKey,
      chiefName: chiefName,
      chiefPurpose: chiefPurpose,
      chiefInstructions: chiefInstructions,
    ),
    (data) => _draftedResource(data, 'organization.create', (r) {
      final org = wire.Organization.fromJson(r);
      return (org.id, org.chiefId);
    }),
    'create organization $key',
  );

  @override
  ResourceSubmission<DraftedResource> prepareCreateWorker({
    required String organizationId,
    required String key,
    required String name,
    required String purpose,
    required String instructions,
  }) => _LiveResourceSubmission(
    api.prepareWorkerCreate(
      organizationId: organizationId,
      key: key,
      name: name,
      purpose: purpose,
      instructions: instructions,
    ),
    (data) => _draftedResource(data, 'worker.create', (r) {
      final worker = wire.Worker.fromJson(r);
      return (worker.id, worker.id);
    }),
    'create worker $key',
  );

  @override
  ResourceSubmission<DraftedResource> prepareCreateResponsibility({
    required String workerId,
    required String outcome,
    required List<String> triggers,
    required int minIntervalSeconds,
    required VerifierIdentity verifier,
    required DateTime rootDeadline,
    String currency = 'XXX',
  }) => _LiveResourceSubmission(
    api.prepareResponsibilityCreate(
      workerId: workerId,
      outcome: outcome,
      triggers: triggers,
      minIntervalSeconds: minIntervalSeconds,
      verifier: wire.VerifierDescriptor(
        id: verifier.id,
        version: verifier.version,
        kind: 'artifact',
        classification: 'internal',
      ),
      rootDeadline: rootDeadline,
      currency: currency,
    ),
    (data) => _draftedResource(
      data,
      'responsibility.create',
      (r) => (wire.Responsibility.fromJson(r).id, null),
    ),
    'create responsibility for $workerId',
  );

  @override
  ResourceSubmission<PlanOutcome> preparePlan({
    required String draftId,
    required int expectedVersion,
  }) => _LiveResourceSubmission(
    api.preparePlan(draftId: draftId, expectedVersion: expectedVersion),
    (data) {
      final o = StrictObject(data, 'configuration.plan');
      final plan = wire.Plan.fromJson(o.object('resource'));
      o.finish();
      return PlanOutcome(
        planId: plan.id,
        baseRevision: plan.baseRevision,
        candidateDigest: plan.candidateDigest,
        diagnostics: [
          for (final d in plan.diagnostics) '${d.severity}: ${d.message}',
        ],
        pendingRequirements: [
          for (final r in plan.authorityRequirements)
            'Authority needed: ${r.message}',
          for (final d in plan.decisions)
            'A decision is needed on action ${d.actionDigest.substring(0, 8)}…',
          for (final r in plan.requirements) r.message,
        ],
      );
    },
    'plan draft $draftId',
  );

  @override
  ResourceSubmission<PlanOutcome> prepareApplyPlan(PlanOutcome plan) =>
      _LiveResourceSubmission(
        api.prepareApplyPlan(
          planId: plan.planId,
          baseRevision: plan.baseRevision,
          candidateDigest: plan.candidateDigest,
        ),
        (data) {
          final o = StrictObject(data, 'configuration.apply');
          wire.Revision.fromJson(o.object('resource'));
          o.finish();
          return plan;
        },
        'apply plan ${plan.planId}',
      );

  ConversationOutcome _conversationOutcome(
    Map<String, Object?> data,
    String operation,
  ) {
    final o = StrictObject(data, operation);
    final c = wire.Conversation.fromJson(o.object('resource'));
    o.finish();
    return ConversationOutcome(id: c.id, version: c.version);
  }

  @override
  ResourceSubmission<ConversationOutcome> prepareOpenDirectConversation({
    required String humanPrincipalId,
    required String workerId,
    required String title,
  }) => _LiveResourceSubmission(
    api.prepareConversationCreate(
      kind: 'direct',
      participantIds: [humanPrincipalId, workerId],
      title: title,
    ),
    (data) => _conversationOutcome(data, 'conversation.create'),
    'open a conversation with $workerId',
  );

  @override
  ResourceSubmission<ConversationOutcome> prepareCreateGroup({
    required String title,
    required List<String> participantIds,
  }) => _LiveResourceSubmission(
    api.prepareConversationCreate(
      kind: 'group',
      participantIds: participantIds,
      title: title,
    ),
    (data) => _conversationOutcome(data, 'conversation.create'),
    'create group $title',
  );

  @override
  ResourceSubmission<ConversationOutcome> prepareAddParticipant({
    required ConversationEntry conversation,
    required int expectedVersion,
    required String newParticipantId,
  }) => _LiveResourceSubmission(
    api.prepareConversationUpdate(
      id: conversation.id.value,
      expectedVersion: expectedVersion,
      participantIds: {
        ...conversation.participantIds,
        newParticipantId,
      }.toList(),
    ),
    (data) => _conversationOutcome(data, 'conversation.update'),
    'add a participant to ${conversation.id.value}',
  );

  TaskOutcome _taskOutcome(wire.Task task) =>
      TaskOutcome(id: task.id, version: task.version, state: task.state.name);

  @override
  ResourceSubmission<TaskOutcome> prepareCreateTask({
    required String ownerId,
    required String workerId,
    required String outcome,
    required List<String> requiredOutputs,
    required VerifierIdentity verifier,
    required DateTime rootDeadline,
    String currency = 'XXX',
  }) => _LiveResourceSubmission(
    api.prepareTaskCreate(
      ownerId: ownerId,
      workerId: workerId,
      outcome: outcome,
      requiredOutputs: requiredOutputs,
      verifier: wire.VerifierDescriptor(
        id: verifier.id,
        version: verifier.version,
        kind: 'artifact',
        classification: 'internal',
      ),
      rootDeadline: rootDeadline,
      currency: currency,
    ),
    (data) {
      final o = StrictObject(data, 'task.create');
      final task = wire.Task.fromJson(o.object('resource'));
      o.finish();
      return _taskOutcome(task);
    },
    'create task for $workerId',
  );

  @override
  ResourceSubmission<TaskOutcome> prepareStartTask({
    required String id,
    required int expectedVersion,
  }) => _LiveResourceSubmission(
    api.prepareTaskStart(id: id, expectedVersion: expectedVersion),
    (data) {
      final o = StrictObject(data, 'task.start');
      final started = wire.Task.fromJson(o.object('task'));
      wire.Run.fromJson(o.object('run'));
      o.finish();
      return _taskOutcome(started);
    },
    'start task $id',
  );

  @override
  ResourceSubmission<TaskOutcome> prepareDelegateTask({
    required String parentId,
    required int parentExpectedVersion,
    required String ownerId,
    required String childWorkerId,
    required String outcome,
    required List<String> requiredOutputs,
    required VerifierIdentity verifier,
    required DateTime rootDeadline,
    String currency = 'XXX',
  }) => _LiveResourceSubmission(
    api.prepareTaskDelegate(
      id: parentId,
      expectedVersion: parentExpectedVersion,
      ownerId: ownerId,
      childWorkerId: childWorkerId,
      outcome: outcome,
      requiredOutputs: requiredOutputs,
      verifier: wire.VerifierDescriptor(
        id: verifier.id,
        version: verifier.version,
        kind: 'artifact',
        classification: 'internal',
      ),
      rootDeadline: rootDeadline,
      currency: currency,
    ),
    (data) {
      final o = StrictObject(data, 'task.delegate');
      final task = wire.Task.fromJson(o.object('resource'));
      o.finish();
      return _taskOutcome(task);
    },
    'delegate $parentId to $childWorkerId',
  );

  @override
  ResourceSubmission<TaskOutcome> prepareAcceptTask({
    required String id,
    required int expectedVersion,
    required bool accept,
    String reason = '',
  }) => _LiveResourceSubmission(
    api.prepareTaskAccept(
      id: id,
      expectedVersion: expectedVersion,
      accept: accept,
      reason: reason,
    ),
    (data) {
      final o = StrictObject(data, 'task.accept');
      final decided = wire.Task.fromJson(o.object('resource'));
      o.finish();
      return _taskOutcome(decided);
    },
    '${accept ? 'accept' : 'reject'} task $id',
  );

  @override
  Future<List<VerifierIdentity>> loadTrustedVerifiers() => _guard(() async {
    final verifiers = await api.trustedVerifiers();
    return [
      for (final v in verifiers) VerifierIdentity(id: v.id, version: v.version),
    ];
  });

  @override
  Future<List<TaskArtifactEntry>> loadTaskArtifacts(String taskId) =>
      _guard(() async {
        final artifacts = await api.taskArtifacts(taskId);
        return [
          for (final a in artifacts)
            TaskArtifactEntry(
              id: a.id,
              digest: a.digest,
              mediaType: a.mediaType,
              sizeBytes: a.size,
              createdAt: a.createdAt,
            ),
        ];
      });

  @override
  Future<ReviewContentPart> readTaskArtifact(TaskArtifactEntry artifact) =>
      _guard(
        () => _contentPart(
          1,
          wire.ArtifactRef(id: artifact.id, digest: artifact.digest),
          label: artifact.mediaType,
        ),
      );

  @override
  Future<void> submit(PendingSubmission submission) => _guard(() async {
    if (submission is _LiveResourceSubmission) {
      final envelope = await api.client.submit(submission.submission);
      submission.result = submission.decode(
        envelope.requireData(submission.submission.operation),
      );
      return;
    }
    final live = submission as _LiveSubmission;
    await api.client.submit(live.submission);
  });

  @override
  Future<Resolution> resolve(PendingSubmission submission) => _guard(() async {
    final inner = submission is _LiveResourceSubmission
        ? submission.submission
        : (submission as _LiveSubmission).submission;
    switch (await api.client.resolve(inner)) {
      case DispositionNotCommitted():
        return const ResolvedNotReceived();
      case DispositionFound(:final original):
        final fault = original.error;
        if (fault != null) return ResolvedRefused(_refusal(fault));
        if (submission is _LiveResourceSubmission) {
          submission.result = submission.decode(
            original.requireData(inner.operation),
          );
        }
        return const ResolvedAcknowledged();
    }
  });

  // ---- messages -----------------------------------------------------------

  @override
  Future<List<ChatMessage>> loadMessages(ConversationId conversation) =>
      _guard(() async {
        final meta = _conversationMeta[conversation.value];
        final messages = await api.listMessages(conversation.value);
        return [for (final m in messages) _chatMessage(m, meta)]
          ..sort((a, b) => a.at.compareTo(b.at));
      });

  /// Whether a message is shown as the person's own. A direct conversation
  /// has exactly the worker and the human as participants, so a sender who
  /// is not that worker is the human — derived from data the contract
  /// actually returns, never from a "current identity" operation the
  /// catalog does not have (see the class doc). A group conversation may
  /// hold more than one non-worker participant, and this client cannot
  /// safely tell "me" apart from another human or client agent there
  /// without that operation; it never guesses, so a group message always
  /// renders under its real sender name instead of a fabricated "you".
  ChatMessage _chatMessage(wire.Message m, _ConversationMeta? meta) {
    final fromUser =
        meta != null &&
        meta.kind == wire.ConversationKind.direct &&
        m.senderId != meta.workerId;
    return ChatMessage(
      id: m.id,
      fromUser: fromUser,
      senderName: fromUser ? 'You' : _principalNames[m.senderId] ?? 'Worker',
      body: m.body,
      at: m.createdAt,
    );
  }

  // ---- events -----------------------------------------------------------------

  @override
  Future<bool> hasChanges() async {
    try {
      return await _guard(() async {
        final events = await api.listAll(
          Operations.eventList,
          wire.WireEvent.fromJson,
          startCursor: _eventCursor,
          onCursor: (c) => _eventCursor = c,
        );
        var changed = false;
        for (final e in events) {
          if (e.sequence > _lastSequence) {
            _lastSequence = e.sequence;
            changed = true;
          }
        }
        await _persistCursor();
        return changed;
      });
    } on SourceRefusal {
      // An expired cursor, or any refusal to replay: never fill the gap by
      // guessing. Drop the cursor and have the caller take a fresh snapshot.
      _eventCursor = null;
      return true;
    }
  }

  /// Baselines the event cursor to "now" only when nothing was resumed from
  /// a prior launch, or a resumed cursor turned out to be expired. A cursor
  /// already in hand (from [LocalStore], across a restart) is replayed
  /// forward instead, so a fresh launch never silently skips whatever
  /// happened while the app was closed.
  Future<void> _baselineEvents() async {
    if (_eventCursor != null) {
      try {
        final events = await api.listAll(
          Operations.eventList,
          wire.WireEvent.fromJson,
          startCursor: _eventCursor,
          onCursor: (c) => _eventCursor = c,
        );
        for (final e in events) {
          if (e.sequence > _lastSequence) _lastSequence = e.sequence;
        }
        await _persistCursor();
        return;
      } on OperationFailedException catch (e) {
        if (e.fault.code != FaultCode.cursorExpired) rethrow;
        // snapshot_required: the resumed cursor is gone. The snapshot this
        // call is already building is the recovery itself; only the cursor
        // for subsequent quiet polling needs a fresh baseline.
        _eventCursor = null;
      }
    }
    final events = await api.listAll(
      Operations.eventList,
      wire.WireEvent.fromJson,
      onCursor: (c) => _eventCursor = c,
    );
    for (final e in events) {
      if (e.sequence > _lastSequence) _lastSequence = e.sequence;
    }
    await _persistCursor();
  }

  // ---- snapshot -----------------------------------------------------------------

  @override
  Future<ReviewEntry> refreshReview(ReviewId id) => _guard(() async {
    final review = await api.reviewGet(id.value);
    final operations = await api.listAll(
      Operations.operationList,
      wire.ExternalOperation.fromJson,
    );
    return _reviewEntry(review, operations, const {});
  });

  /// Confirms every operation this client calls exists at the version it was
  /// written against. A missing or mismatched operation is a named
  /// unsupported state.
  Future<void> _confirmCapabilities() async {
    final served = {for (final c in await api.capabilities()) c.id: c};
    final problems = <String>[];
    for (final op in Operations.all) {
      final c = served[op.id];
      if (c == null) {
        problems.add('${op.id} is not offered');
      } else if (c.version != op.version) {
        problems.add(
          '${op.id} is version ${c.version}; this app needs ${op.version}',
        );
      } else if (c.submissionKey != op.isMutation ||
          c.expectedVersion != op.expectedVersion) {
        problems.add('${op.id} has a different request shape');
      }
    }
    if (problems.isNotEmpty) {
      throw SourceUnsupported(
        'This controller does not serve what this version of the app '
        'needs: ${problems.join('; ')}.',
      );
    }
  }

  @override
  Future<WorkspaceSnapshot> loadSnapshot() => _guard(() async {
    await _confirmCapabilities();
    final status = await api.installationStatus();
    if (installedMac && status.installationId != _installationId) {
      throw const SourceRefusal(
        RefusalKind.other,
        'The local service belongs to a different installation.',
      );
    }
    final organizations = await api.listAll(
      Operations.organizationList,
      wire.Organization.fromJson,
    );
    final workers = await api.listAll(
      Operations.workerList,
      wire.Worker.fromJson,
    );
    final conversations = await api.listAll(
      Operations.conversationList,
      wire.Conversation.fromJson,
    );
    final reviews = await api.listAll(
      Operations.reviewList,
      wire.Review.fromJson,
    );
    final operations = await api.listAll(
      Operations.operationList,
      wire.ExternalOperation.fromJson,
    );
    final tasks = await api.listAll(Operations.taskList, wire.Task.fromJson);
    final responsibilities = await api.listAll(
      Operations.responsibilityList,
      wire.Responsibility.fromJson,
    );
    final artifacts = await api.listAll(
      Operations.artifactList,
      wire.Artifact.fromJson,
    );
    final grants = await api.listAll(Operations.grantList, wire.Grant.fromJson);
    final principals = await api.listAll(
      Operations.principalList,
      wire.Principal.fromJson,
    );
    final connections = await api.connections();
    final schedules = await api.schedules();
    final qualifications = await api.autonomyQualifications();
    var memoryBindings = <wire.MemoryBinding>[];
    var claims = <wire.Claim>[];
    String? memoryFailure;
    try {
      memoryBindings = await api.memoryBindings();
      claims = await api.memoryClaims([for (final b in memoryBindings) b.id]);
    } on OperationFailedException catch (e) {
      memoryFailure = e.fault.message;
    }
    var runs = <wire.Run>[];
    try {
      runs = await api.runs();
    } on OperationFailedException {
      // No runs visible; recovery obligations simply stay empty below.
    }
    await _baselineEvents();

    final budgets = <String, wire.Limits?>{};
    final rootOrganizations = organizations
        .where((o) => o.parentId == null)
        .toList();
    final rootChiefId = rootOrganizations.length == 1
        ? rootOrganizations.single.chiefId
        : null;
    if (installedMac) {
      if (rootChiefId != null) {
        try {
          budgets[rootChiefId] = await api.budget(workerId: rootChiefId);
        } on OperationFailedException {
          budgets[rootChiefId] = null;
        }
      }
    }
    final installedChief = installedMac
        ? installedChiefFromRecords(
            installationId: status.installationId,
            initialized: status.initialized,
            runtimeReady: status.runtimeReady,
            paused: status.paused,
            maintenance: status.maintenance,
            requirements: status.requirements,
            organizations: organizations,
            workers: workers,
            conversations: conversations,
            connections: connections,
            effectiveBudget: budgets[rootChiefId],
            now: _clock(),
          )
        : null;
    final workerEntries = _tree(
      organizations,
      workers,
      conversations,
      installedChiefWorkerId: installedChief?.workerId,
      installedChiefConversationId: installedChief?.conversationId,
      strictInstalledChief: installedMac,
    );
    final workerIds = {for (final w in workerEntries) w.id.value};
    _principalNames
      ..clear()
      ..addAll({for (final p in principals) p.id: p.name});
    final principalNames = _principalNames;
    _conversationMeta
      ..clear()
      ..addEntries(
        conversations.map(
          (c) => MapEntry(
            c.id,
            _ConversationMeta(kind: c.kind, workerId: _workerOf(c, workerIds)),
          ),
        ),
      );

    final reviewEntries = <ReviewEntry>[];
    for (final r in reviews) {
      reviewEntries.add(await _reviewEntry(r, operations, workerIds));
    }

    // ---- cost liabilities and context-capture/advisory posture -----------
    final spending = <SpendingEntry>[];
    for (final w in workers.take(25)) {
      wire.Usage? usage;
      try {
        usage = await api.usage(workerId: w.id);
      } on OperationFailedException {
        // No spend visible for this worker; still show ceiling/posture.
      }
      wire.Limits? budget;
      try {
        budget = budgets.containsKey(w.id)
            ? budgets[w.id]
            : await api.budget(workerId: w.id);
      } on OperationFailedException {
        // No effective ceiling visible for this worker.
      }
      final contextCapture = w.profile?.contextCapture;
      if (usage == null &&
          (budget == null || budget.isUnconfigured) &&
          contextCapture == null) {
        continue;
      }
      String money(String currency, int micro) =>
          wire.Money(currency: currency, microUnits: micro).format();
      final headline = usage == null
          ? 'Spend not visible for this worker'
          : '${money(usage.currency, usage.spent)} spent · '
                '${money(usage.currency, usage.reserved)} reserved';
      final detail = usage == null
          ? 'usage.get returned nothing for this scope.'
          : usage.unknown > 0
          ? '${money(usage.currency, usage.unknown)} of cost is unknown and '
                'still reserved'
          : usage.advisory
          ? 'Prices are advisory; a missing price is not zero.'
          : 'No unknown costs.';
      final ceiling = budget == null || budget.isUnconfigured
          ? null
          : '${money(budget.currency, budget.spendMicroUnits)} ceiling · '
                '${budget.concurrency} concurrent · '
                '${budget.modelSteps} model steps';
      spending.add(
        SpendingEntry(
          workerId: WorkerId(w.id),
          headline: headline,
          detail: detail,
          fraction:
              usage != null &&
                  budget != null &&
                  !budget.isUnconfigured &&
                  budget.spendMicroUnits > 0
              ? usage.spent / budget.spendMicroUnits
              : null,
          ceiling: ceiling,
          contextCapture: contextCapture,
        ),
      );
    }

    // ---- responsibility-to-schedule links ---------------------------------
    final schedulesByWorker = <String, wire.Schedule>{};
    for (final s in schedules) {
      schedulesByWorker.putIfAbsent(s.workerId, () => s);
    }
    ScheduleLink? scheduleFor(String workerId) {
      final s = schedulesByWorker[workerId];
      if (s == null) return null;
      return ScheduleLink(
        id: s.id,
        timezone: s.timezone,
        expression: s.expression,
        misfire: s.misfire,
        catchUpSeconds: s.catchUpSeconds,
        paused: s.paused,
        nextWake: s.nextWake,
      );
    }

    // ---- artifact output names, task provenance and sealed checks --------
    final tasksById = {for (final t in tasks) t.id: t};
    String checkLabel(wire.ExpectedCheck c) {
      final target = c.artifactName ?? c.checkId;
      return '${c.kind} on $target: expected to ${c.expected}';
    }

    // ---- authorized memory claims: source, freshness, retract ------------
    final orgById = {for (final o in organizations) o.id: o};
    String? chiefOf(String organizationId) {
      var org = orgById[organizationId];
      final seen = <String>{};
      while (org != null && seen.add(org.id)) {
        if (workerIds.contains(org.chiefId)) return org.chiefId;
        org = org.parentId == null ? null : orgById[org.parentId];
      }
      return null;
    }

    String? attributeScope(wire.Scope scope) {
      if (scope.workerId != null && workerIds.contains(scope.workerId)) {
        return scope.workerId;
      }
      if (scope.organizationId != null) {
        final chief = chiefOf(scope.organizationId!);
        if (chief != null) return chief;
      }
      // Installation-wide memory (no organization or worker named) is the
      // personal chief's to curate (R15-005): the root of the tree.
      for (final w in workerEntries) {
        if (w.parentId == null) return w.id.value;
      }
      return null;
    }

    final bindingsByBrain = <String, List<wire.MemoryBinding>>{};
    for (final b in memoryBindings) {
      bindingsByBrain.putIfAbsent(b.brainId, () => []).add(b);
    }
    final memory = <MemoryEntry>[];
    for (final c in claims) {
      final bindings = bindingsByBrain[c.brainId] ?? const [];
      // No authorized binding names this brain: this client cannot attribute
      // or show it, and never guesses which worker it belongs to.
      final binding = bindings.isEmpty ? null : bindings.first;
      if (binding == null) continue;
      final owner = attributeScope(binding.scope);
      if (owner == null) continue;
      memory.add(
        MemoryEntry(
          id: ClaimId(c.id),
          workerId: WorkerId(owner),
          bindingId: binding.id,
          brainId: c.brainId,
          claimVersion: c.version,
          title: c.text.length > 64 ? '${c.text.substring(0, 61)}…' : c.text,
          text: c.text,
          provenance: [
            'Source: ${c.sources.isEmpty ? 'none recorded' : c.sources.map((s) => _short(s.digest)).join(', ')}',
            'Freshness: ${c.freshness.toIso8601String()}',
            if (c.curatorId != null)
              'Curated by ${principalNames[c.curatorId] ?? 'an identity that is not listed'}',
            if (c.redaction != null && c.redaction!.isNotEmpty)
              'Redacted: ${c.redaction}',
          ],
          freshness: c.freshness,
          active: c.active,
          confidence: c.confidence,
          canRetract: bindings.any((b) => b.canRetract),
        ),
      );
    }

    // ---- autonomy evidence -------------------------------------------------
    final autonomy = [
      for (final q in qualifications)
        if (workerIds.contains(q.workerId))
          AutonomyEntry(
            id: q.id,
            workerId: WorkerId(q.workerId),
            capability: q.capability,
            destinations: q.destinations,
            state: q.state,
            explanation: q.explanation,
          ),
    ];

    // ---- recovery obligations -----------------------------------------------
    final recovery = <RecoveryEntry>[];
    for (final r in runs.take(20)) {
      if (!r.isRecoverable) continue;
      final task = tasksById[r.taskId];
      if (task == null || !workerIds.contains(task.workerId)) continue;
      try {
        final obligations = await api.runRecovery(r.id);
        if (obligations.isEmpty) continue;
        recovery.add(
          RecoveryEntry(
            id: r.id,
            workerId: WorkerId(task.workerId),
            label: 'Run ${_short(r.id)} for ${task.outcome}',
            obligations: [for (final req in obligations) req.message],
          ),
        );
      } on OperationFailedException {
        // No recovery detail visible for this run.
      }
    }

    return WorkspaceSnapshot(
      takenAt: _clock(),
      workspaceName: 'Installation ${_short(status.installationId)}',
      installedChiefWorkerId: installedChief?.workerId,
      installedChiefConversationId: installedChief?.conversationId,
      installedChiefIssue: installedChief?.issue,
      installedMac: installedMac,
      workers: workerEntries,
      conversations: [
        for (final c in conversations) _conversation(c, workerIds),
      ],
      reviews: reviewEntries,
      tasks: [
        for (final t in tasks)
          if (workerIds.contains(t.workerId)) _task(t),
      ],
      routines: [
        for (final r in responsibilities)
          if (workerIds.contains(r.workerId))
            RoutineEntry(
              id: RoutineId(r.id),
              version: r.version,
              workerId: WorkerId(r.workerId),
              title: r.outcome,
              triggers: r.triggers,
              signals: r.signals,
              minIntervalSeconds: r.minIntervalSeconds,
              paused: r.paused,
              lastCycleId: r.lastCycleId,
              nextRun: r.nextWake,
              schedule: scheduleFor(r.workerId),
            ),
      ],
      files: [
        for (final a in artifacts)
          if (a.scope.workerId != null && workerIds.contains(a.scope.workerId))
            FileEntry(
              id: a.id,
              workerId: WorkerId(a.scope.workerId!),
              title: a.purpose ?? '${a.mediaType} · ${_short(a.digest)}',
              detail: a.available
                  ? '${a.size} bytes · ${a.classification} · '
                        '${a.createdAt.toIso8601String()}'
                  : 'Integrity failure: bytes are missing or do not match '
                        'the recorded digest.',
              verified: a.available,
              digest: a.digest,
              classification: a.classification,
              taskId: a.scope.taskId,
              taskTitle: a.scope.taskId == null
                  ? null
                  : tasksById[a.scope.taskId]?.outcome,
              checks: a.scope.taskId == null
                  ? const []
                  : [
                      for (final c
                          in tasksById[a.scope.taskId]
                                  ?.acceptance
                                  .expectedObservations ??
                              const [])
                        checkLabel(c),
                    ],
            ),
      ],
      access: [
        for (final g in grants)
          if (g.scope.workerId != null && workerIds.contains(g.scope.workerId))
            AccessEntry(
              id: g.id,
              workerId: WorkerId(g.scope.workerId!),
              title: g.capabilities.join(', '),
              detail: _accessDetail(g, principalNames),
              allowed: !g.denied,
            ),
      ],
      spending: spending,
      memory: memory,
      autonomy: autonomy,
      recovery: recovery,
      principals: [
        for (final p in principals)
          PrincipalEntry(
            id: p.id,
            name: p.name,
            kind: p.kind,
            revoked: p.revoked,
          ),
      ]..sort((a, b) => a.name.compareTo(b.name)),
      prerequisites: [
        if (!status.initialized)
          const PrerequisiteNotice(
            title: 'This installation is not initialized',
            message:
                'Run the installer on this machine, then reconnect. '
                'Initialization is local-only and is not done from this app.',
            setupPath: 'zatiti installation init',
          ),
        if (status.paused)
          const PrerequisiteNotice(
            title: 'The installation is paused',
            message: 'No new work starts until it is resumed.',
          ),
        if (status.maintenance)
          const PrerequisiteNotice(
            title: 'Maintenance is in progress',
            message: 'Some actions are refused until maintenance ends.',
          ),
        for (final r in status.requirements)
          PrerequisiteNotice(title: r.code, message: r.message),
        // memory.recall/.remember/.promote/.inspect (authoring and paid
        // retrieval) are out of this client's scope; only claims/source/
        // freshness/retract are read here. A real read failure — never a
        // permanently assumed gap — surfaces as its own notice.
        if (memoryFailure != null)
          PrerequisiteNotice(
            tab: DetailsTab.memory,
            title: 'Memory could not be read',
            message: memoryFailure,
          ),
        ..._workerSetupPrerequisites(workers, connections),
      ],
      unresolvedOperations: _unresolvedOperations(
        operations,
        reviewEntries,
        workerIds,
      ),
    );
  });

  /// Setup a task or responsibility actually needs: a worker's own execution
  /// profile (provider/model), its budget currency and the connection that
  /// profile depends on. Every fact here is real (`Worker.profile`,
  /// `Worker.limits`, `connection.list`'s `validation_state`), never
  /// inferred from a task's own success or failure.
  List<PrerequisiteNotice> _workerSetupPrerequisites(
    List<wire.Worker> workers,
    List<wire.Connection> connections,
  ) {
    final connectionsById = {for (final c in connections) c.id: c};
    final notices = <PrerequisiteNotice>[];
    for (final w in workers) {
      final workerId = WorkerId(w.id);
      final profile = w.profile;
      if (profile == null) {
        notices.add(
          PrerequisiteNotice(
            workerId: workerId,
            tab: DetailsTab.work,
            title: 'No provider configured for ${w.name}',
            message:
                '${w.name} can be talked to, but hosted execution refuses '
                'paid work until a provider and model are configured '
                '(execution_profile.create).',
          ),
        );
      } else {
        final connection = connectionsById[profile.connectionId];
        if (connection == null ||
            connection.validationState !=
                wire.ConnectionValidationState.valid) {
          notices.add(
            PrerequisiteNotice(
              workerId: workerId,
              tab: DetailsTab.work,
              title: 'Credential needs action for ${w.name}',
              message: connection == null
                  ? '${w.name}’s provider connection is not listed here.'
                  : '${w.name}’s connection to ${connection.provider} is '
                        '${connection.validationState.name}, not valid.',
            ),
          );
        }
      }
      final limits = w.limits;
      if (limits == null || limits.isUnconfigured) {
        notices.add(
          PrerequisiteNotice(
            workerId: workerId,
            tab: DetailsTab.work,
            title: 'No budget configured for ${w.name}',
            message:
                'Only a zero-spend task or responsibility is possible for '
                '${w.name} until a currency and spend limit are configured.',
          ),
        );
      }
    }
    return notices;
  }

  /// Operations an authorized worker started on its own that no review
  /// names — a message send, or a worker-driven action under an existing
  /// grant — and whose disposition is not yet settled. Never silently
  /// dropped: [_reviewEntry] already reads a matching operation's state for
  /// the reviews it does explain; this is the remainder.
  List<UnresolvedOperationEntry> _unresolvedOperations(
    List<wire.ExternalOperation> operations,
    List<ReviewEntry> reviews,
    Set<String> workerIds,
  ) {
    final reviewed = {for (final r in reviews) r.actionDigest};
    const settled = {'succeeded', 'failed', 'denied', 'expired', 'cancelled'};
    final out = <UnresolvedOperationEntry>[];
    for (final op in operations) {
      if (reviewed.contains(op.actionDigest)) continue;
      if (settled.contains(op.state)) continue;
      final workerId = op.action.scope.workerId;
      out.add(
        UnresolvedOperationEntry(
          id: op.id,
          workerId: workerId != null && workerIds.contains(workerId)
              ? WorkerId(workerId)
              : null,
          title: op.action.destination,
          destination: op.action.destination,
          state: switch (op.state) {
            'executing' => EffectState.executing,
            'awaiting_confirmation' => EffectState.deliveryAccepted,
            'outcome_unknown' => EffectState.outcomeUnknown,
            _ => EffectState.notStarted,
          },
        ),
      );
    }
    return out;
  }

  String _short(String id) => id.length <= 8 ? id : id.substring(0, 8);

  /// What an access card says beneath its capability list. A grant is held by
  /// a principal, not by the worker whose scope it sits in, so the card names
  /// that identity. An identity `principal.list` did not return is said to be
  /// unlisted; it is never guessed at or silently attributed to the worker.
  String _accessDetail(wire.Grant grant, Map<String, String> principalNames) {
    final holder =
        principalNames[grant.principalId] ?? 'an identity that is not listed';
    final destinations = grant.destinations.isEmpty
        ? 'No external destinations.'
        : 'Destinations: ${grant.destinations.join(', ')}';
    return 'Held by $holder. $destinations';
  }

  /// Builds the organization conversation tree in stable key order: each
  /// organization's chief, then the chiefs of its child organizations and its
  /// other workers beneath that chief.
  List<WorkerEntry> _tree(
    List<wire.Organization> organizations,
    List<wire.Worker> workers,
    List<wire.Conversation> conversations, {
    String? installedChiefWorkerId,
    String? installedChiefConversationId,
    bool strictInstalledChief = false,
  }) {
    final orgById = {for (final o in organizations) o.id: o};
    final workerById = {for (final w in workers) w.id: w};
    final sortedOrgs = [...organizations]
      ..sort((a, b) => a.key.compareTo(b.key));
    final sortedWorkers = [...workers]..sort((a, b) => a.key.compareTo(b.key));

    List<String> pathOf(wire.Organization org) {
      final path = <String>[];
      wire.Organization? current = org;
      final seen = <String>{};
      while (current != null && seen.add(current.id)) {
        path.insert(0, current.name);
        current = current.parentId == null ? null : orgById[current.parentId];
      }
      return path;
    }

    /// The nearest chief above [org] that exists as a worker.
    String? chiefAbove(wire.Organization org) {
      var parent = org.parentId == null ? null : orgById[org.parentId];
      final seen = <String>{};
      while (parent != null && seen.add(parent.id)) {
        if (workerById.containsKey(parent.chiefId)) return parent.chiefId;
        parent = parent.parentId == null ? null : orgById[parent.parentId];
      }
      return null;
    }

    String? conversationOf(String workerId) {
      if (strictInstalledChief && workerId == installedChiefWorkerId) {
        return installedChiefConversationId;
      }
      for (final c in conversations) {
        if (c.kind != wire.ConversationKind.direct) continue;
        if (c.scope.workerId == workerId ||
            c.participantIds.contains(workerId)) {
          return c.id;
        }
      }
      return null;
    }

    final out = <WorkerEntry>[];
    final placed = <String>{};

    void place(wire.Worker w, String? parent, {required bool chief}) {
      if (!placed.add(w.id)) return;
      final org = orgById[w.organizationId];
      final conversation = conversationOf(w.id);
      out.add(
        WorkerEntry(
          id: WorkerId(w.id),
          name: w.name,
          role: chief
              ? (parent == null ? 'Your chief' : 'Organization chief')
              : w.purpose,
          organizationId: OrganizationId(w.organizationId),
          organizationPath: org == null ? const [] : pathOf(org),
          parentId: parent == null ? null : WorkerId(parent),
          conversationId: conversation == null
              ? null
              : ConversationId(conversation),
          isOrganizationChief: chief,
        ),
      );
    }

    void visit(wire.Organization org) {
      final chief = workerById[org.chiefId];
      final parent = chief == null ? chiefAbove(org) : null;
      if (chief != null) place(chief, chiefAbove(org), chief: true);
      final under = chief?.id ?? parent;
      for (final w in sortedWorkers) {
        if (w.organizationId == org.id && w.id != org.chiefId) {
          place(w, under, chief: false);
        }
      }
      for (final child in sortedOrgs) {
        if (child.parentId == org.id) visit(child);
      }
    }

    for (final org in sortedOrgs) {
      if (org.parentId == null || !orgById.containsKey(org.parentId)) {
        visit(org);
      }
    }
    // Workers whose organization is not visible still get a row.
    for (final w in sortedWorkers) {
      place(w, null, chief: false);
    }
    return out;
  }

  /// The worker a direct conversation belongs to: its scoped worker when
  /// that worker is visible, otherwise the first participant this client
  /// recognizes as a worker. Null for a group, and for a direct
  /// conversation whose worker is not (yet) visible.
  String? _workerOf(wire.Conversation c, Set<String> workerIds) {
    if (c.kind != wire.ConversationKind.direct) return null;
    final scoped = c.scope.workerId;
    if (scoped != null && workerIds.contains(scoped)) return scoped;
    for (final p in c.participantIds) {
      if (workerIds.contains(p)) return p;
    }
    return null;
  }

  /// Builds the conversation entry from what a snapshot can cheaply know for
  /// every conversation: identity, kind, the controller's own unread/read-
  /// marker projection and its meaningful-activity ordering signal. The
  /// message history itself is not fetched here — eagerly reading every
  /// conversation's full history on every snapshot does not scale, and only
  /// an opened conversation needs it. [WorkspaceController] overlays
  /// [loadMessages]'s result onto this entry once the person opens it.
  ConversationEntry _conversation(wire.Conversation c, Set<String> workerIds) {
    final workerId = _workerOf(c, workerIds);
    return ConversationEntry(
      id: ConversationId(c.id),
      title: c.title,
      kind: c.kind == wire.ConversationKind.group
          ? ConversationKind.group
          : ConversationKind.direct,
      workerId: workerId == null ? null : WorkerId(workerId),
      messages: const [],
      unreadCount: c.callerUnreadCount ?? 0,
      lastReadMarker: c.callerLastReadMarker,
      lastMeaningfulEvent: c.lastMeaningfulEvent,
      participantIds: c.participantIds,
      version: c.version,
    );
  }

  TaskEntry _task(wire.Task t) => TaskEntry(
    id: t.id,
    workerId: WorkerId(t.workerId),
    title: t.outcome,
    version: t.version,
    ownerId: t.ownerId,
    manualAcceptance: t.manualAcceptance,
    state: switch (t.state) {
      wire.TaskState.draft ||
      wire.TaskState.ready ||
      wire.TaskState.running ||
      wire.TaskState.verifying => TaskEntryState.inProgress,
      wire.TaskState.waiting => TaskEntryState.waiting,
      wire.TaskState.succeeded => TaskEntryState.completed,
      wire.TaskState.failed => TaskEntryState.needsChanges,
      wire.TaskState.cancelled => TaskEntryState.cancelled,
    },
    detail: switch (t.state) {
      wire.TaskState.verifying => 'Checks are running',
      wire.TaskState.succeeded => 'Acceptance checks passed',
      wire.TaskState.failed => t.waitingReason ?? 'Checks did not pass',
      _ => t.waitingReason ?? '',
    },
  );

  Future<ReviewEntry> _reviewEntry(
    wire.Review r,
    List<wire.ExternalOperation> operations,
    Set<String> workerIds,
  ) async {
    final action = r.preview;
    final tool = action.tool;
    final toolKey = '${tool.id}@${tool.version}';
    var toolName = _toolNames[toolKey];
    if (toolName == null) {
      try {
        toolName = _toolNames[toolKey] = await api.toolName(tool.id);
      } on OperationFailedException {
        toolName = 'An external action';
      }
    }

    final content = <ReviewContentPart>[];
    for (var i = 0; i < action.content.length; i++) {
      content.add(await _contentPart(i + 1, action.content[i]));
    }

    var effect = EffectState.notStarted;
    for (final op in operations) {
      if (op.actionDigest != r.actionDigest) continue;
      effect = switch (op.state) {
        'executing' => EffectState.executing,
        'awaiting_confirmation' => EffectState.deliveryAccepted,
        'outcome_unknown' => EffectState.outcomeUnknown,
        'succeeded' => EffectState.succeeded,
        'failed' || 'denied' || 'expired' || 'cancelled' => EffectState.failed,
        _ => EffectState.notStarted,
      };
    }

    final proposer = action.scope.workerId ?? r.scope.workerId ?? r.proposerId;
    return ReviewEntry(
      id: ReviewId(r.id),
      version: r.version,
      actionDigest: r.actionDigest,
      state: switch (r.state) {
        wire.ReviewState.pending => ReviewRecordState.pending,
        wire.ReviewState.approved => ReviewRecordState.approved,
        wire.ReviewState.rejected => ReviewRecordState.rejected,
        wire.ReviewState.expired => ReviewRecordState.expired,
        wire.ReviewState.invalidated => ReviewRecordState.invalidated,
      },
      proposerId: workerIds.isEmpty || workerIds.contains(proposer)
          ? WorkerId(proposer)
          : null,
      effect: effect,
      humanRequired: r.requirement.humanRequired,
      title: toolName,
      consequence:
          'This runs “$toolName” once on ${action.destination}, as '
          '${action.accountIdentity}.',
      action: toolName,
      accountIdentity: action.accountIdentity,
      destination: action.destination,
      costBound: action.costBound.format(),
      expiresAt: r.requirement.expiresAt,
      content: content,
      parameters: {
        for (final e in action.parameters.entries)
          e.key: e.value is String ? e.value! as String : jsonEncode(e.value),
      },
      evidence: [
        if (r.requirement.humanRequired)
          'A person must decide. A worker cannot approve on your behalf.',
        if (r.requirement.separateProposer)
          'The proposer cannot be the one who decides.',
        'The decision binds action digest ${r.actionDigest}, review version '
            '${r.version}. Any change requires a new decision.',
        'Tool ${tool.id} version ${tool.version} · connection '
            '${action.connection.id} version ${action.connection.version} · '
            'configuration revision ${action.configurationRevision}.',
        'Not before ${action.notBefore.toIso8601String()} · action expires '
            '${action.expiresAt.toIso8601String()}.',
        if (action.preconditions.isNotEmpty)
          'Preconditions: ${jsonEncode(action.preconditions)}',
      ],
    );
  }

  Future<ReviewContentPart> _contentPart(
    int index,
    wire.ArtifactRef ref, {
    String? label,
  }) async {
    final resolvedLabel = label ?? 'Content $index';
    ReviewContentPart unavailable(String reason) => ReviewContentPart(
      label: resolvedLabel,
      digest: ref.digest,
      unavailableReason: reason,
    );
    final wire.ArtifactRead read;
    try {
      read = await api.artifactRead(ref.id);
    } on OperationFailedException catch (e) {
      return unavailable('The content could not be read: ${e.fault.message}');
    }
    if (read.digest != ref.digest) {
      return unavailable(
        'The content read does not match the digest this review binds to.',
      );
    }
    final List<int> bytes;
    try {
      bytes = base64.decode(read.bytesBase64);
    } on FormatException {
      return unavailable('The content is not valid base64.');
    }
    if (read.totalSize > bytes.length) {
      return unavailable(
        'The content is ${read.totalSize} bytes, larger than this view shows '
        'in full.',
      );
    }
    try {
      return ReviewContentPart(
        label: resolvedLabel,
        digest: ref.digest,
        text: const Utf8Decoder(allowMalformed: false).convert(bytes),
      );
    } on FormatException {
      return unavailable('The content is not text (${bytes.length} bytes).');
    }
  }
}
