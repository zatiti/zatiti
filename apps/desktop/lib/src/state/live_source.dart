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
import 'snapshot.dart';
import 'workspace_source.dart';

class _LiveSubmission implements PendingSubmission {
  _LiveSubmission(this.submission);

  final Submission submission;

  @override
  String get description => submission.operation;
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
  }) : api = ControllerApi(client),
       _clock = clock ?? (() => DateTime.now().toUtc()),
       label = endpointLabel ?? 'Controller',
       _eventCursor = initialEventCursor,
       _lastSequence = initialLastSequence,
       _localStore = localStore,
       _installationId = client.installationId;

  final ControllerApi api;

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
  Future<void> submit(PendingSubmission submission) => _guard(() async {
    final live = submission as _LiveSubmission;
    await api.client.submit(live.submission);
  });

  @override
  Future<Resolution> resolve(PendingSubmission submission) => _guard(() async {
    final live = submission as _LiveSubmission;
    switch (await api.client.resolve(live.submission)) {
      case DispositionNotCommitted():
        return const ResolvedNotReceived();
      case DispositionFound(:final original):
        final fault = original.error;
        if (fault != null) return ResolvedRefused(_refusal(fault));
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
    await _baselineEvents();

    final workerEntries = _tree(organizations, workers, conversations);
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

    final spending = <SpendingEntry>[];
    for (final w in workers.take(25)) {
      try {
        final u = await api.usage(workerId: w.id);
        String money(int micro) =>
            wire.Money(currency: u.currency, microUnits: micro).format();
        spending.add(
          SpendingEntry(
            workerId: WorkerId(w.id),
            headline: '${money(u.spent)} spent · ${money(u.reserved)} reserved',
            detail: u.unknown > 0
                ? '${money(u.unknown)} of cost is unknown and still reserved'
                : u.advisory
                ? 'Prices are advisory; a missing price is not zero.'
                : 'No unknown costs.',
          ),
        );
      } on OperationFailedException {
        // No usage visible for this worker; the Access tab says so.
      }
    }

    return WorkspaceSnapshot(
      takenAt: _clock(),
      workspaceName: 'Installation ${_short(status.installationId)}',
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
              schedule: r.triggers.isEmpty
                  ? 'Runs when its signals change'
                  : r.triggers.join(' · '),
              paused: r.paused,
              nextRun: r.nextWake,
            ),
      ],
      files: [
        for (final a in artifacts)
          if (a.scope.workerId != null && workerIds.contains(a.scope.workerId))
            FileEntry(
              id: a.id,
              workerId: WorkerId(a.scope.workerId!),
              title: '${a.mediaType} · ${_short(a.digest)}',
              detail:
                  '${a.size} bytes · ${a.classification} · '
                  '${a.createdAt.toIso8601String()}',
              verified: a.available,
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
        const PrerequisiteNotice(
          tab: DetailsTab.memory,
          title: 'Memory cannot be listed yet',
          message:
              'The controller can inspect one memory claim by identity, but '
              'offers no operation that lists a worker’s claims, so there '
              'is nothing to show or remove here.',
        ),
      ],
    );
  });

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
    List<wire.Conversation> conversations,
  ) {
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
    );
  }

  TaskEntry _task(wire.Task t) => TaskEntry(
    id: t.id,
    workerId: WorkerId(t.workerId),
    title: t.outcome,
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
    wire.ArtifactRef ref,
  ) async {
    final label = 'Content $index';
    ReviewContentPart unavailable(String reason) => ReviewContentPart(
      label: label,
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
        label: label,
        digest: ref.digest,
        text: const Utf8Decoder(allowMalformed: false).convert(bytes),
      );
    } on FormatException {
      return unavailable('The content is not text (${bytes.length} bytes).');
    }
  }
}
