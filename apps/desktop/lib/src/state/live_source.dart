// The live workspace source: maps controller operations to the workspace
// snapshot. Nothing here decides anything. Where the public operation catalog
// offers no operation for a designed surface, the snapshot carries a
// prerequisite notice and the surface renders its designed empty state.

import 'dart:convert';

import '../api/controller_api.dart';
import '../api/models.dart' as wire;
import '../transport/controller_client.dart';
import '../transport/envelope.dart';
import '../transport/errors.dart';
import '../transport/operations.dart';
import '../transport/strict_json.dart';
import '../transport/submission.dart';
import 'snapshot.dart';
import 'workspace_source.dart';

class _LiveSubmission implements PendingSubmission {
  _LiveSubmission(this.submission, {this.onAcknowledged});

  final Submission submission;
  final void Function()? onAcknowledged;

  @override
  String get description => submission.operation;
}

class LiveWorkspaceSource implements WorkspaceSource {
  LiveWorkspaceSource(
    ControllerClient client, {
    this.principalId,
    String? endpointLabel,
    DateTime Function()? clock,
  }) : api = ControllerApi(client),
       _clock = clock ?? (() => DateTime.now().toUtc()),
       label = endpointLabel ?? 'Controller';

  final ControllerApi api;

  /// The caller's own principal, needed by `mailbox.list`. Startup
  /// configuration: the catalog has no operation that returns it.
  final String? principalId;

  final DateTime Function() _clock;
  final Map<String, String> _toolNames = {};
  final Map<String, List<ChatMessage>> _sentThisSession = {};
  String? _eventCursor;
  int _lastSequence = 0;

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
    final prepared = api.prepareMessageSend(
      conversationId: conversation.value,
      body: body,
    );
    return _LiveSubmission(
      prepared.submission,
      onAcknowledged: () {
        (_sentThisSession[conversation.value] ??= []).add(
          ChatMessage(
            // The controller keeps this id, so a copy of this message that
            // arrives through the mailbox is recognized and not shown twice.
            id: prepared.messageId,
            fromUser: true,
            senderName: 'You',
            body: body,
            at: _clock(),
          ),
        );
      },
    );
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
    live.onAcknowledged?.call();
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
        live.onAcknowledged?.call();
        return const ResolvedAcknowledged();
    }
  });

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
        return changed;
      });
    } on SourceRefusal {
      // An expired cursor, or any refusal to replay: never fill the gap by
      // guessing. Drop the cursor and have the caller take a fresh snapshot.
      _eventCursor = null;
      return true;
    }
  }

  Future<void> _baselineEvents() async {
    _eventCursor = null;
    final events = await api.listAll(
      Operations.eventList,
      wire.WireEvent.fromJson,
      onCursor: (c) => _eventCursor = c,
    );
    for (final e in events) {
      if (e.sequence > _lastSequence) _lastSequence = e.sequence;
    }
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
    // The inbox is one surface of the workspace, not the workspace. The
    // controller keeps a mailbox private to its own recipient, so a
    // misconfigured principal is refused here; that must cost the person
    // their inbox, not their conversations, decisions and work.
    var received = const <wire.Message>[];
    String? inboxRefusal;
    if (principalId != null) {
      try {
        received = await api.listAll(
          Operations.mailboxList,
          wire.Message.fromJson,
          extra: {'recipient_id': principalId},
        );
      } on OperationFailedException catch (e) {
        inboxRefusal = e.fault.message;
      }
    }
    await _baselineEvents();

    final workerEntries = _tree(organizations, workers, conversations);
    final workerIds = {for (final w in workerEntries) w.id.value};
    final names = {for (final w in workers) w.id: w.name};
    final principalNames = {for (final p in principals) p.id: p.name};

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
        for (final c in conversations)
          _conversation(c, workerIds, received, names, inboxRefusal),
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
        if (inboxRefusal != null)
          PrerequisiteNotice(
            title: 'Your inbox could not be read',
            message:
                '$inboxRefusal A mailbox is private to its own recipient, so '
                'the principal this app is configured with must be your own. '
                'Conversations, decisions and work are unaffected.',
            setupPath: 'ZATITI_PRINCIPAL_ID',
          ),
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

  ConversationEntry _conversation(
    wire.Conversation c,
    Set<String> workerIds,
    List<wire.Message> received,
    Map<String, String> names,
    String? inboxRefusal,
  ) {
    String? workerId;
    if (c.kind == wire.ConversationKind.direct) {
      final scoped = c.scope.workerId;
      if (scoped != null && workerIds.contains(scoped)) {
        workerId = scoped;
      } else {
        for (final p in c.participantIds) {
          if (workerIds.contains(p)) workerId = p;
        }
      }
    }
    final sent = _sentThisSession[c.id] ?? const <ChatMessage>[];
    final sentIds = {for (final m in sent) m.id};
    final messages = <ChatMessage>[
      for (final m in received)
        // A message this window sent is already listed below under the id it
        // minted. The controller keeps that id, so a copy delivered back
        // through the mailbox is the same message, not a second one.
        if (m.conversationId == c.id && !sentIds.contains(m.id))
          ChatMessage(
            id: m.id,
            fromUser: false,
            senderName: names[m.senderId] ?? 'Worker',
            body: m.body,
            at: m.createdAt,
          ),
      ...sent,
    ]..sort((a, b) => a.at.compareTo(b.at));
    return ConversationEntry(
      id: ConversationId(c.id),
      title: c.title,
      kind: c.kind == wire.ConversationKind.group
          ? ConversationKind.group
          : ConversationKind.direct,
      workerId: workerId == null ? null : WorkerId(workerId),
      messages: messages,
      historyNotice: switch ((principalId, inboxRefusal)) {
        (null, _) =>
          'Earlier messages cannot be shown. The controller offers no '
              'operation that reads a conversation’s history, and no '
              'principal is configured for reading your inbox. Messages you '
              'send from this window appear once the controller acknowledges '
              'them.',
        (_, final String refusal) =>
          'Your inbox could not be read, so messages delivered to you are '
              'not listed: $refusal A mailbox is private to its own '
              'recipient, so the configured principal must be your own. '
              'Everything else on this screen is current.',
        _ =>
          'Showing messages delivered to you and messages sent from this '
              'window. The controller offers no operation that reads a '
              'conversation’s full history, so your earlier messages are not '
              'listed.',
      },
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
