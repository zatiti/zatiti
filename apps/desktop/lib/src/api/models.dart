// Typed wire resources, decoded strictly from operation output data. Shapes
// follow `$defs` in docs/implementation/operations.json. Fields this client
// does not render are still read, so an unknown field is always refused.

import '../transport/strict_json.dart';

class Scope {
  const Scope({
    required this.installationId,
    this.organizationId,
    this.projectId,
    this.workerId,
    this.taskId,
  });

  factory Scope.fromJson(Object? json) {
    final o = StrictObject(json, 'scope');
    final s = Scope(
      installationId: o.string('installation_id'),
      organizationId: o.optionalString('organization_id'),
      projectId: o.optionalString('project_id'),
      workerId: o.optionalString('worker_id'),
      taskId: o.optionalString('task_id'),
    );
    o.finish();
    return s;
  }

  final String installationId;
  final String? organizationId;
  final String? projectId;
  final String? workerId;
  final String? taskId;
}

class Ref {
  const Ref({required this.id, required this.version});

  factory Ref.fromJson(Object? json) {
    final o = StrictObject(json, 'ref');
    final r = Ref(id: o.string('id'), version: o.integer('version'));
    o.finish();
    return r;
  }

  final String id;
  final int version;
}

class ArtifactRef {
  const ArtifactRef({required this.id, required this.digest});

  factory ArtifactRef.fromJson(Object? json) {
    final o = StrictObject(json, 'artifact ref');
    final r = ArtifactRef(id: o.string('id'), digest: o.string('digest'));
    o.finish();
    return r;
  }

  final String id;
  final String digest;
}

class Money {
  const Money({required this.currency, required this.microUnits});

  factory Money.fromJson(Object? json) {
    final o = StrictObject(json, 'money');
    final m = Money(
      currency: o.string('currency'),
      microUnits: o.integer('micro_units'),
    );
    o.finish();
    return m;
  }

  final String currency;
  final int microUnits;

  /// Renders the bound without floating point, for example `0.86 USD`.
  String format() {
    final whole = microUnits ~/ 1000000;
    final cents = (microUnits % 1000000) ~/ 10000;
    final remainder = microUnits % 10000;
    final fraction = remainder == 0
        ? cents.toString().padLeft(2, '0')
        : (microUnits % 1000000).toString().padLeft(6, '0');
    return '$whole.$fraction $currency';
  }
}

class Requirement {
  const Requirement({
    required this.code,
    required this.message,
    this.resourceId,
    this.challengeId,
  });

  factory Requirement.fromJson(Object? json) {
    final o = StrictObject(json, 'requirement');
    final r = Requirement(
      code: o.string('code'),
      message: o.string('message'),
      resourceId: o.optionalString('resource_id'),
      challengeId: o.optionalString('challenge_id'),
    );
    o.finish();
    return r;
  }

  final String code;
  final String message;
  final String? resourceId;
  final String? challengeId;
}

class InstallationStatus {
  const InstallationStatus({
    required this.installationId,
    required this.generation,
    required this.paused,
    required this.maintenance,
    required this.initialized,
    required this.requirements,
    required this.version,
    this.runtimeReady,
  });

  factory InstallationStatus.fromJson(Object? json) {
    final o = StrictObject(json, 'installation status');
    final s = InstallationStatus(
      installationId: o.string('installation_id'),
      generation: o.integer('generation'),
      paused: o.boolean('paused'),
      maintenance: o.boolean('maintenance'),
      initialized: o.boolean('initialized'),
      requirements: [
        for (final r in o.list('requirements')) Requirement.fromJson(r),
      ],
      version: o.integer('version'),
      runtimeReady: o.optionalBoolean('runtime_ready'),
    );
    o.finish();
    return s;
  }

  final String installationId;
  final int generation;
  final bool paused;
  final bool maintenance;
  final bool initialized;
  final List<Requirement> requirements;
  final int version;
  final bool? runtimeReady;
}

/// An identity the controller authenticates. The client never creates one in
/// the ordinary product flow; it reads principals so a person can see who
/// holds authority in their installation, and the live proof drives
/// `principal.create` to exercise submission-key replay end to end.
class Principal {
  const Principal({
    required this.id,
    required this.version,
    required this.kind,
    required this.name,
    required this.scope,
    required this.revoked,
  });

  factory Principal.fromJson(Object? json) {
    final o = StrictObject(json, 'principal');
    final p = Principal(
      id: o.string('id'),
      version: o.integer('version'),
      kind: o.string('kind'),
      name: o.string('name'),
      scope: Scope.fromJson(o.object('scope')),
      revoked: o.boolean('revoked'),
    );
    o.finish();
    return p;
  }

  final String id;
  final int version;

  /// `human`, `client_agent`, `worker` or `service`, exactly as the
  /// controller reports it. The client shows the wire value rather than
  /// guessing a label for a kind it does not know.
  final String kind;

  final String name;
  final Scope scope;
  final bool revoked;
}

class Organization {
  const Organization({
    required this.id,
    required this.version,
    required this.key,
    required this.name,
    required this.chiefId,
    this.parentId,
  });

  factory Organization.fromJson(Object? json) {
    final o = StrictObject(json, 'organization');
    final org = Organization(
      id: o.string('id'),
      version: o.integer('version'),
      key: o.string('key'),
      name: o.string('name'),
      chiefId: o.string('chief_id'),
      parentId: o.optionalString('parent_id'),
    );
    o.optional('limits');
    o.optional('extensions');
    o.finish();
    return org;
  }

  final String id;
  final int version;
  final String key;
  final String name;
  final String chiefId;
  final String? parentId;
}

/// A worker's spend ceiling and step/child bounds. `currency` "XXX" is the
/// wire's explicit "no currency configured" sentinel (see
/// `internal/installation/firsttask.go`): a zero-spend task is possible in
/// it, but nothing with a positive cost ever is.
class Limits {
  const Limits({
    required this.currency,
    required this.spendMicroUnits,
    required this.concurrency,
    required this.modelSteps,
    required this.childCount,
    required this.delegationDepth,
    required this.attemptSeconds,
    required this.rootDeadline,
  });

  factory Limits.fromJson(Object? json) {
    final o = StrictObject(json, 'limits');
    final l = Limits(
      currency: o.string('currency'),
      spendMicroUnits: o.integer('spend_micro_units'),
      concurrency: o.integer('concurrency'),
      modelSteps: o.integer('model_steps'),
      childCount: o.integer('child_count'),
      delegationDepth: o.integer('delegation_depth'),
      attemptSeconds: o.integer('attempt_seconds'),
      rootDeadline: o.dateTime('root_deadline'),
    );
    o.finish();
    return l;
  }

  final String currency;
  final int spendMicroUnits;
  final int concurrency;
  final int modelSteps;
  final int childCount;
  final int delegationDepth;
  final int attemptSeconds;
  final DateTime rootDeadline;

  /// True for the wire's explicit "no currency configured" placeholder,
  /// never guessed from an empty string.
  bool get isUnconfigured => currency == 'XXX';
}

/// A worker's provider/model binding. Null on the worker itself (not this
/// type) means no provider is configured yet; see `Worker.profile`.
class ExecutionProfile {
  const ExecutionProfile({
    required this.id,
    required this.version,
    required this.executor,
    required this.model,
    required this.connectionId,
    required this.providerDestination,
    required this.costBound,
    required this.contextCapture,
    this.provider,
    this.adapterProfile,
    this.connectionVersion,
    this.costEnforcement,
    this.raw,
  });

  factory ExecutionProfile.fromJson(Object? json) {
    final o = StrictObject(json, 'execution profile');
    final base = ExecutionProfile(
      id: o.string('id'),
      version: o.integer('version'),
      executor: o.string('executor'),
      model: o.string('model'),
      connectionId: o.string('connection_id'),
      providerDestination: o.string('provider_destination'),
      costBound: Money.fromJson(o.object('cost_bound')),
      contextCapture: o.string('context_capture'),
    );
    o.list('capabilities');
    o.string('classification');
    final adapter = o.optionalObject('adapter_profile');
    final connectionVersion = o.optionalInteger('connection_version');
    o.finish();
    final provider =
        adapter?['provider'] as String? ??
        (adapter?['schema'] == 'zatiti.responses/v1' ? 'openai' : null);
    if (adapter != null &&
        (adapter['schema'] is! String ||
            adapter['model'] is! String ||
            (adapter['schema'] != 'zatiti.responses/v1' &&
                adapter['schema'] != 'zatiti.responses/v2') ||
            (provider != 'openai' &&
                provider != 'openrouter' &&
                provider != 'experiential'))) {
      throw StrictJsonException(
        'execution profile adapter_profile is malformed',
      );
    }
    final enforcement = adapter?['enforcement'];
    if (enforcement != null && enforcement is! Map<String, Object?>) {
      throw StrictJsonException('execution profile enforcement is malformed');
    }
    final costEnforcement = enforcement is Map<String, Object?>
        ? enforcement['cost']
        : null;
    if (costEnforcement != null &&
        !const {
          'enforced',
          'advisory',
          'unsupported',
        }.contains(costEnforcement)) {
      throw StrictJsonException(
        'execution profile cost enforcement is invalid',
      );
    }
    return ExecutionProfile(
      id: base.id,
      version: base.version,
      executor: base.executor,
      model: base.model,
      connectionId: base.connectionId,
      providerDestination: base.providerDestination,
      costBound: base.costBound,
      contextCapture: base.contextCapture,
      provider: provider,
      adapterProfile: adapter,
      connectionVersion: connectionVersion,
      costEnforcement: costEnforcement as String?,
      raw: Map<String, Object?>.from(json as Map),
    );
  }

  final String id;
  final int version;
  final String executor;
  final String model;
  final String connectionId;
  final String providerDestination;

  /// The per-action spend ceiling this profile's adapter enforces.
  final Money costBound;

  /// `complete`, `partial` or `advisory` — how much of a model step's real
  /// context this profile's adapter actually captures. `advisory` means an
  /// output is a suggestion, never confirmed state; this client renders the
  /// exact word the controller sent, never a softened paraphrase.
  final String contextCapture;
  final String? provider;
  final Map<String, Object?>? adapterProfile;
  final int? connectionVersion;
  final String? costEnforcement;
  final Map<String, Object?>? raw;
}

/// One fixed provider preset returned by `model.provider.list`. Endpoints and
/// protocol modes come from the controller; the client never infers them.
class ProviderDescriptor {
  const ProviderDescriptor({
    required this.id,
    required this.displayName,
    required this.defaultEndpoint,
    required this.sessionMode,
    required this.credentialSetup,
  });

  factory ProviderDescriptor.fromJson(Object? json) {
    final o = StrictObject(json, 'model provider');
    final p = ProviderDescriptor(
      id: o.string('id'),
      displayName: o.string('display_name'),
      defaultEndpoint: o.string('default_endpoint'),
      sessionMode: o.string('session_mode'),
      credentialSetup: o.string('credential_setup'),
    );
    o.finish();
    if (!const {'openai', 'openrouter', 'experiential'}.contains(p.id) ||
        !const {'provider_conversation', 'stateless'}.contains(p.sessionMode) ||
        p.credentialSetup != 'api_key') {
      throw StrictJsonException(
        'model provider contains an unsupported preset',
      );
    }
    return p;
  }

  final String id;
  final String displayName;
  final String defaultEndpoint;
  final String sessionMode;
  final String credentialSetup;
}

class Worker {
  const Worker({
    required this.id,
    required this.version,
    required this.organizationId,
    required this.key,
    required this.name,
    required this.purpose,
    this.profile,
    this.limits,
    this.raw,
  });

  factory Worker.fromJson(Object? json) {
    final raw = Map<String, Object?>.from(json as Map);
    final o = StrictObject(json, 'worker');
    final w = Worker(
      id: o.string('id'),
      version: o.integer('version'),
      organizationId: o.string('organization_id'),
      key: o.string('key'),
      name: o.string('name'),
      purpose: o.string('purpose'),
      profile: o.nullable('profile') == null
          ? null
          : ExecutionProfile.fromJson(o.object('profile')),
      limits: o.nullable('limits') == null
          ? null
          : Limits.fromJson(o.object('limits')),
    );
    o.string('instructions');
    o.list('skill_versions');
    o.list('bindings');
    o.optionalObject('extensions');
    o.finish();
    return Worker(
      id: w.id,
      version: w.version,
      organizationId: w.organizationId,
      key: w.key,
      name: w.name,
      purpose: w.purpose,
      profile: w.profile,
      limits: w.limits,
      raw: raw,
    );
  }

  final String id;
  final int version;
  final String organizationId;
  final String key;
  final String name;
  final String purpose;

  /// The provider/model this worker runs on. Null means no provider is
  /// configured: it can chat, but hosted execution refuses paid work.
  final ExecutionProfile? profile;

  /// The worker's spend/step ceiling. Null means no budget was configured.
  final Limits? limits;
  final Map<String, Object?>? raw;

  /// Preserves every controller-owned worker field while changing only the
  /// selected immutable execution profile.
  Map<String, Object?> definitionWithProfile(ExecutionProfile profile) {
    final source = raw;
    final selected = profile.raw;
    if (source == null || selected == null) {
      throw StrictJsonException('worker/profile raw data is unavailable');
    }
    return Map<String, Object?>.from(source)
      ..remove('id')
      ..remove('version')
      ..['profile'] = selected;
  }
}

enum ConversationKind { direct, group }

class Conversation {
  const Conversation({
    required this.id,
    required this.version,
    required this.scope,
    required this.kind,
    required this.participantIds,
    required this.title,
    required this.pinned,
    this.lastMeaningfulEvent,
    this.callerUnreadCount,
    this.callerLastReadMarker,
  });

  factory Conversation.fromJson(Object? json) {
    final o = StrictObject(json, 'conversation');
    final kindWire = o.string('kind');
    final c = Conversation(
      id: o.string('id'),
      version: o.integer('version'),
      scope: Scope.fromJson(o.object('scope')),
      kind: switch (kindWire) {
        'direct' => ConversationKind.direct,
        'group' => ConversationKind.group,
        _ => throw StrictJsonException(
          'conversation kind "$kindWire" is not direct or group',
        ),
      },
      participantIds: o.stringList('participant_ids'),
      title: o.string('title'),
      pinned: o.boolean('pinned'),
      lastMeaningfulEvent: o.optionalDateTime('last_meaningful_event'),
      // Revision 3 additions. Optional on the wire: an older controller may
      // omit them, and this client must still parse its response.
      callerUnreadCount: o.optionalInteger('caller_unread_count'),
      callerLastReadMarker: o.optionalDateTime('caller_last_read_marker'),
    );
    o.finish();
    return c;
  }

  final String id;
  final int version;
  final Scope scope;
  final ConversationKind kind;
  final List<String> participantIds;
  final String title;
  final bool pinned;
  final DateTime? lastMeaningfulEvent;

  /// The calling principal's own unread count, when the controller reports
  /// one. Absent (not zero) on a controller that does not yet compute it.
  final int? callerUnreadCount;
  final DateTime? callerLastReadMarker;
}

enum MessageState { submitted, admitted, acknowledged }

class Message {
  const Message({
    required this.id,
    required this.version,
    required this.senderId,
    required this.body,
    required this.state,
    required this.createdAt,
    this.conversationId,
  });

  factory Message.fromJson(Object? json) {
    final o = StrictObject(json, 'message');
    final stateWire = o.string('state');
    final m = Message(
      id: o.string('id'),
      version: o.integer('version'),
      senderId: o.string('sender_id'),
      body: o.string('body'),
      state: MessageState.values.firstWhere(
        (s) => s.name == stateWire,
        orElse: () => throw StrictJsonException(
          'message state "$stateWire" is not in the contract',
        ),
      ),
      createdAt: o.dateTime('created_at'),
      conversationId: o.optionalString('conversation_id'),
    );
    o.stringList('recipient_ids');
    Scope.fromJson(o.object('scope'));
    o.stringList('task_ids');
    o.list('attachments');
    o.finish();
    return m;
  }

  final String id;
  final int version;
  final String senderId;
  final String body;
  final MessageState state;
  final DateTime createdAt;
  final String? conversationId;
}

/// The sealed external action a review binds a decision to.
class ActionPreview {
  const ActionPreview({
    required this.scope,
    required this.tool,
    required this.connection,
    required this.accountIdentity,
    required this.destination,
    required this.content,
    required this.notBefore,
    required this.expiresAt,
    required this.preconditions,
    required this.configurationRevision,
    required this.parameters,
    required this.costBound,
  });

  factory ActionPreview.fromJson(Object? json) {
    final o = StrictObject(json, 'action');
    final a = ActionPreview(
      scope: Scope.fromJson(o.object('scope')),
      tool: Ref.fromJson(o.object('tool')),
      connection: Ref.fromJson(o.object('connection')),
      accountIdentity: o.string('account_identity'),
      destination: o.string('destination'),
      content: [for (final c in o.list('content')) ArtifactRef.fromJson(c)],
      notBefore: o.dateTime('not_before'),
      expiresAt: o.dateTime('expires_at'),
      preconditions: o.object('preconditions'),
      configurationRevision: o.integer('configuration_revision'),
      parameters: o.object('parameters'),
      costBound: Money.fromJson(o.object('cost_bound')),
    );
    o.finish();
    return a;
  }

  final Scope scope;
  final Ref tool;
  final Ref connection;
  final String accountIdentity;
  final String destination;
  final List<ArtifactRef> content;
  final DateTime notBefore;
  final DateTime expiresAt;
  final Map<String, Object?> preconditions;
  final int configurationRevision;
  final Map<String, Object?> parameters;
  final Money costBound;
}

class DecisionRequirement {
  const DecisionRequirement({
    required this.actionDigest,
    required this.humanRequired,
    required this.eligiblePrincipals,
    required this.expiresAt,
    required this.separateProposer,
  });

  factory DecisionRequirement.fromJson(Object? json) {
    final o = StrictObject(json, 'decision requirement');
    final r = DecisionRequirement(
      actionDigest: o.string('action_digest'),
      humanRequired: o.boolean('human_required'),
      eligiblePrincipals: o.stringList('eligible_principals'),
      expiresAt: o.dateTime('expires_at'),
      separateProposer: o.boolean('separate_proposer'),
    );
    o.finish();
    return r;
  }

  final String actionDigest;
  final bool humanRequired;
  final List<String> eligiblePrincipals;
  final DateTime expiresAt;
  final bool separateProposer;
}

/// One check a task or responsibility's verifier is sealed to run —
/// `Adapter_ExpectedVerificationObservation` in the frozen contract. This is
/// the *configured* check, read before any run exists; whether it actually
/// passed is a separate fact this client only ever learns from the task's
/// own state (`succeeded`/`failed`) and `waiting_reason`, never invented.
class ExpectedCheck {
  const ExpectedCheck({
    required this.checkId,
    required this.kind,
    required this.expected,
    this.artifactName,
    this.expectedDigest,
  });

  factory ExpectedCheck.fromJson(Object? json) {
    final o = StrictObject(json, 'expected verification observation');
    final c = ExpectedCheck(
      checkId: o.string('check_id'),
      kind: o.string('kind'),
      expected: o.string('expected'),
      artifactName: o.optionalString('artifact_name'),
      expectedDigest: o.optionalString('expected_digest'),
    );
    o.optionalObject('schema');
    o.optionalString('command_id');
    o.optionalInteger('expected_exit_code');
    o.finish();
    return c;
  }

  final String checkId;
  final String kind;

  /// `pass` or `expected`; the observation this check must produce for the
  /// verifier to accept the task.
  final String expected;
  final String? artifactName;
  final String? expectedDigest;
}

/// A task or responsibility's sealed acceptance contract: who verifies it,
/// and (for `mode: "independent"`) exactly which checks were sealed in. See
/// `api/acceptance.dart` for why this client only ever authors `"manual"`.
class Acceptance {
  const Acceptance({
    required this.verifierId,
    required this.verifierVersion,
    required this.mode,
    required this.expectedObservations,
  });

  factory Acceptance.fromJson(Object? json) {
    final o = StrictObject(json, 'acceptance');
    final a = Acceptance(
      verifierId: o.string('verifier_id'),
      verifierVersion: o.string('verifier_version'),
      mode: o.string('mode'),
      expectedObservations: [
        for (final e in o.list('expected_observations'))
          ExpectedCheck.fromJson(e),
      ],
    );
    o.list('sealed_inputs');
    o.list('required_child_ids');
    o.object('profile');
    o.finish();
    return a;
  }

  final String verifierId;
  final String verifierVersion;

  /// `manual` or `independent`.
  final String mode;
  final List<ExpectedCheck> expectedObservations;
}

enum ReviewState { pending, approved, rejected, expired, invalidated }

class Review {
  const Review({
    required this.id,
    required this.version,
    required this.scope,
    required this.actionDigest,
    required this.preview,
    required this.requirement,
    required this.proposerId,
    required this.state,
    this.decisionId,
  });

  factory Review.fromJson(Object? json) {
    final o = StrictObject(json, 'review');
    final stateWire = o.string('state');
    final r = Review(
      id: o.string('id'),
      version: o.integer('version'),
      scope: Scope.fromJson(o.object('scope')),
      actionDigest: o.string('action_digest'),
      preview: ActionPreview.fromJson(o.object('preview')),
      requirement: DecisionRequirement.fromJson(o.object('requirement')),
      proposerId: o.string('proposer_id'),
      state: ReviewState.values.firstWhere(
        (s) => s.name == stateWire,
        orElse: () => throw StrictJsonException(
          'review state "$stateWire" is not in the contract',
        ),
      ),
      decisionId: o.optionalString('decision_id'),
    );
    o.finish();
    if (r.requirement.actionDigest != r.actionDigest) {
      throw StrictJsonException(
        'review requirement binds a different action digest than the review',
      );
    }
    return r;
  }

  final String id;
  final int version;
  final Scope scope;
  final String actionDigest;
  final ActionPreview preview;
  final DecisionRequirement requirement;
  final String proposerId;
  final ReviewState state;
  final String? decisionId;
}

enum DecisionChoice {
  approve('approve'),
  reject('reject');

  const DecisionChoice(this.wire);
  final String wire;
}

class Decision {
  const Decision({
    required this.id,
    required this.reviewId,
    required this.reviewVersion,
    required this.actionDigest,
    required this.reviewerId,
    required this.decision,
    required this.at,
    required this.reason,
  });

  factory Decision.fromJson(Object? json) {
    final o = StrictObject(json, 'decision');
    final wire = o.string('decision');
    final d = Decision(
      id: o.string('id'),
      reviewId: o.string('review_id'),
      reviewVersion: o.integer('review_version'),
      actionDigest: o.string('action_digest'),
      reviewerId: o.string('reviewer_id'),
      decision: DecisionChoice.values.firstWhere(
        (c) => c.wire == wire,
        orElse: () => throw StrictJsonException(
          'decision "$wire" is not approve or reject',
        ),
      ),
      at: o.dateTime('at'),
      reason: o.string('reason'),
    );
    o.finish();
    return d;
  }

  final String id;
  final String reviewId;
  final int reviewVersion;
  final String actionDigest;
  final String reviewerId;
  final DecisionChoice decision;
  final DateTime at;
  final String reason;
}

enum TaskState {
  draft,
  ready,
  running,
  waiting,
  verifying,
  succeeded,
  failed,
  cancelled,
}

class Task {
  const Task({
    required this.id,
    required this.version,
    required this.scope,
    required this.ownerId,
    required this.workerId,
    required this.outcome,
    required this.requiredOutputs,
    required this.state,
    required this.acceptance,
    this.waitingReason,
    this.manualAcceptance = false,
  });

  factory Task.fromJson(Object? json) {
    final o = StrictObject(json, 'task');
    final stateWire = o.string('state');
    final t = Task(
      id: o.string('id'),
      version: o.integer('version'),
      scope: Scope.fromJson(o.object('scope')),
      ownerId: o.string('owner_id'),
      workerId: o.string('worker_id'),
      outcome: o.string('outcome'),
      requiredOutputs: o.stringList('required_outputs'),
      state: TaskState.values.firstWhere(
        (s) => s.name == stateWire,
        orElse: () => throw StrictJsonException(
          'task state "$stateWire" is not in the contract',
        ),
      ),
      acceptance: Acceptance.fromJson(o.object('acceptance')),
      waitingReason: o.optionalString('waiting_reason'),
      manualAcceptance: o.optionalBoolean('manual_acceptance') ?? false,
    );
    o.list('inputs');
    o.object('limits');
    o.list('dependencies');
    o.optionalString('parent_id');
    o.optionalString('root_id');
    o.optionalBoolean('cancellation_requested');
    o.finish();
    return t;
  }

  final String id;
  final int version;
  final Scope scope;
  final String ownerId;
  final String workerId;
  final String outcome;
  final List<String> requiredOutputs;
  final TaskState state;
  final String? waitingReason;
  final Acceptance acceptance;

  /// True when this task's success can only be established by an eligible
  /// human calling `task.accept` — never by an automated verifier. See
  /// `internal/tasks/acceptance.go`'s `evaluateSuccess`.
  final bool manualAcceptance;
}

enum RunState {
  ready,
  running,
  waiting,
  verifying,
  succeeded,
  failed,
  cancelled,
}

/// One execution of a task's definition. Most surfaces only need to know
/// that `task.start` produced one, reading progress back through the task's
/// own state; [state] itself is read for recovery: only a run still `ready`,
/// `running`, `waiting` or `verifying` can carry live recovery obligations.
class Run {
  const Run({
    required this.id,
    required this.version,
    required this.taskId,
    required this.state,
  });

  factory Run.fromJson(Object? json) {
    final o = StrictObject(json, 'run');
    final stateWire = o.string('state');
    final r = Run(
      id: o.string('id'),
      version: o.integer('version'),
      taskId: o.string('task_id'),
      state: RunState.values.firstWhere(
        (s) => s.name == stateWire,
        orElse: () =>
            throw StrictJsonException('run state "$stateWire" is unknown'),
      ),
    );
    o.integer('configuration_revision');
    o.list('input_versions');
    o.list('attempt_ids');
    o.finish();
    return r;
  }

  final String id;
  final int version;
  final String taskId;
  final RunState state;

  bool get isRecoverable => switch (state) {
    RunState.ready ||
    RunState.running ||
    RunState.waiting ||
    RunState.verifying => true,
    RunState.succeeded || RunState.failed || RunState.cancelled => false,
  };
}

class Responsibility {
  const Responsibility({
    required this.id,
    required this.version,
    required this.scope,
    required this.workerId,
    required this.outcome,
    required this.triggers,
    required this.signals,
    required this.reasoningPolicy,
    required this.minIntervalSeconds,
    required this.paused,
    this.nextWake,
    this.lastCycleId,
  });

  factory Responsibility.fromJson(Object? json) {
    final o = StrictObject(json, 'responsibility');
    final r = Responsibility(
      id: o.string('id'),
      version: o.integer('version'),
      scope: Scope.fromJson(o.object('scope')),
      workerId: o.string('worker_id'),
      outcome: o.string('outcome'),
      triggers: o.stringList('triggers'),
      signals: o.stringList('signals'),
      reasoningPolicy: o.string('reasoning_policy'),
      minIntervalSeconds: o.integer('min_interval_seconds'),
      paused: o.boolean('paused'),
      nextWake: o.optionalDateTime('next_wake'),
      // Revision 3 addition. Optional on the wire: an older controller may
      // omit it; this responsibility just has no recorded cycle yet.
      lastCycleId: o.optionalString('last_cycle_id'),
    );
    o.object('cycle_limits');
    o.object('aggregate_limits');
    o.list('pause_conditions');
    o.list('escalation_conditions');
    o.object('acceptance');
    o.finish();
    return r;
  }

  final String id;
  final int version;
  final Scope scope;
  final String workerId;
  final String outcome;

  /// Event strings that admit a new cycle. Never a schedule description on
  /// their own — see `snapshot.dart`'s `RoutineEntry` on why this client
  /// keeps them separate from any real `Schedule` record.
  final List<String> triggers;

  /// Signals this responsibility watches, distinct from what triggers a
  /// cycle.
  final List<String> signals;
  final String reasoningPolicy;
  final int minIntervalSeconds;
  final bool paused;
  final DateTime? nextWake;

  /// The most recent cycle this responsibility actually ran, when the
  /// controller has recorded one.
  final String? lastCycleId;
}

class Artifact {
  const Artifact({
    required this.id,
    required this.version,
    required this.scope,
    required this.digest,
    required this.size,
    required this.mediaType,
    required this.classification,
    required this.available,
    required this.createdAt,
    this.sourceOperationId,
    this.purpose,
  });

  factory Artifact.fromJson(Object? json) {
    final o = StrictObject(json, 'artifact');
    final stateWire = o.string('state');
    if (stateWire != 'available' && stateWire != 'fault') {
      throw StrictJsonException('artifact state "$stateWire" is unknown');
    }
    final a = Artifact(
      id: o.string('id'),
      version: o.integer('version'),
      scope: Scope.fromJson(o.object('scope')),
      digest: o.string('digest'),
      size: o.integer('size'),
      mediaType: o.string('media_type'),
      classification: o.string('classification'),
      available: stateWire == 'available',
      createdAt: o.dateTime('created_at'),
      // Revision 3 additions (provenance). Optional on the wire: an older
      // controller, or an artifact published outside a task run, may omit
      // either.
      sourceOperationId: o.optionalString('source_operation_id'),
      purpose: o.optionalString('purpose'),
    );
    o.boolean('encrypted');
    o.finish();
    return a;
  }

  final String id;
  final int version;
  final Scope scope;
  final String digest;
  final int size;
  final String mediaType;
  final String classification;

  /// `true` only for wire state `available`. `false` covers `fault`: bytes
  /// missing or an integrity check failed, never merely "not verified yet".
  final bool available;
  final DateTime createdAt;

  /// The operation that produced this artifact, when the controller recorded
  /// one — real provenance, never inferred from the artifact's own content.
  final String? sourceOperationId;

  /// The human-facing output name this artifact was published under, when
  /// the controller named one (for example a task's required output name).
  final String? purpose;
}

class Grant {
  const Grant({
    required this.id,
    required this.version,
    required this.principalId,
    required this.scope,
    required this.capabilities,
    required this.destinations,
    required this.denied,
    this.expiresAt,
  });

  factory Grant.fromJson(Object? json) {
    final o = StrictObject(json, 'grant');
    final g = Grant(
      id: o.string('id'),
      version: o.integer('version'),
      principalId: o.string('principal_id'),
      scope: Scope.fromJson(o.object('scope')),
      capabilities: o.stringList('capabilities'),
      destinations: o.stringList('destinations'),
      denied: o.boolean('denied'),
      expiresAt: o.optionalDateTime('expires_at'),
    );
    o.optionalString('parent_grant_id');
    o.finish();
    return g;
  }

  final String id;
  final int version;
  final String principalId;
  final Scope scope;
  final List<String> capabilities;
  final List<String> destinations;
  final bool denied;
  final DateTime? expiresAt;
}

/// The acknowledgment a pause or resume returns.
class PauseDisposition {
  const PauseDisposition({
    required this.id,
    required this.version,
    required this.state,
  });

  factory PauseDisposition.fromJson(Object? json) {
    final o = StrictObject(json, 'disposition');
    final d = PauseDisposition(
      id: o.string('id'),
      version: o.integer('version'),
      state: o.string('state'),
    );
    o.optional('job');
    o.optional('operation');
    o.optional('requirements');
    o.finish();
    return d;
  }

  final String id;
  final int version;
  final String state;
}

/// A governed external operation. Its state says what is known about the
/// effect after approval.
class ExternalOperation {
  const ExternalOperation({
    required this.id,
    required this.version,
    required this.actionDigest,
    required this.state,
    required this.action,
  });

  factory ExternalOperation.fromJson(Object? json) {
    final o = StrictObject(json, 'operation');
    final op = ExternalOperation(
      id: o.string('id'),
      version: o.integer('version'),
      actionDigest: o.string('action_digest'),
      state: o.string('state'),
      action: ActionPreview.fromJson(o.object('action')),
    );
    o.stringList('attempt_ids');
    o.optionalString('linked_operation_id');
    o.optionalString('relationship');
    o.finish();
    return op;
  }

  final String id;
  final int version;
  final String actionDigest;
  final String state;
  final ActionPreview action;
}

class Usage {
  const Usage({
    required this.currency,
    required this.spent,
    required this.reserved,
    required this.estimated,
    required this.unknown,
    required this.advisory,
  });

  factory Usage.fromJson(Object? json) {
    final o = StrictObject(json, 'usage');
    final u = Usage(
      currency: o.string('currency'),
      spent: o.integer('spent'),
      reserved: o.integer('reserved'),
      estimated: o.integer('estimated'),
      unknown: o.integer('unknown'),
      advisory: o.boolean('advisory'),
    );
    o.finish();
    return u;
  }

  final String currency;
  final int spent;
  final int reserved;
  final int estimated;
  final int unknown;
  final bool advisory;
}

class WireEvent {
  const WireEvent({required this.id, required this.sequence});

  factory WireEvent.fromJson(Object? json) {
    final o = StrictObject(json, 'event');
    final e = WireEvent(id: o.string('id'), sequence: o.integer('sequence'));
    o.dateTime('at');
    Scope.fromJson(o.object('scope'));
    o.string('kind');
    o.string('resource_id');
    o.integer('resource_version');
    o.object('data');
    o.finish();
    return e;
  }

  final String id;
  final int sequence;
}

/// One bounded read of an artifact's bytes.
class ArtifactRead {
  const ArtifactRead({
    required this.bytesBase64,
    required this.digest,
    required this.offset,
    required this.totalSize,
  });

  factory ArtifactRead.fromJson(Object? json) {
    final o = StrictObject(json, 'artifact read');
    final r = ArtifactRead(
      bytesBase64: o.string('bytes_base64'),
      digest: o.string('digest'),
      offset: o.integer('offset'),
      totalSize: o.integer('total_size'),
    );
    o.finish();
    return r;
  }

  final String bytesBase64;
  final String digest;
  final int offset;
  final int totalSize;
}

/// One public operation as the controller publishes it.
class Capability {
  const Capability({
    required this.id,
    required this.version,
    required this.submissionKey,
    required this.expectedVersion,
  });

  factory Capability.fromJson(Object? json) {
    final o = StrictObject(json, 'descriptor');
    final c = Capability(
      id: o.string('id'),
      version: o.integer('version'),
      submissionKey: o.boolean('submission_key'),
      expectedVersion: o.boolean('expected_version'),
    );
    o.string('owner');
    o.object('input_schema');
    o.object('output_schema');
    o.string('effect');
    o.stringList('scope_requirements');
    o.stringList('cli');
    o.string('mcp');
    o.optionalObject('completion_schema');
    o.finish();
    return c;
  }

  final String id;
  final int version;
  final bool submissionKey;
  final bool expectedVersion;
}

/// One page of a list operation.
class Page<T> {
  const Page(this.items, this.nextCursor);
  final List<T> items;
  final String? nextCursor;
}

/// One problem the compiler found in a draft or a plan's candidate.
class Diagnostic {
  const Diagnostic({
    required this.path,
    required this.code,
    required this.message,
    required this.severity,
  });

  factory Diagnostic.fromJson(Object? json) {
    final o = StrictObject(json, 'diagnostic');
    final d = Diagnostic(
      path: o.string('path'),
      code: o.string('code'),
      message: o.string('message'),
      severity: o.string('severity'),
    );
    o.finish();
    return d;
  }

  final String path;
  final String code;
  final String message;

  /// `error`, `warning` or `info`, exactly as the compiler reports it.
  final String severity;
}

/// A staged, not-yet-active set of changes: the "draft" step of
/// draft/plan/review/apply. `version` is what `configuration.plan` binds to
/// as `expected_version`.
class Draft {
  const Draft({
    required this.id,
    required this.version,
    required this.baseRevision,
    required this.changeCount,
    required this.diagnostics,
  });

  factory Draft.fromJson(Object? json) {
    final o = StrictObject(json, 'draft');
    final changes = o.list('changes');
    final d = Draft(
      id: o.string('id'),
      version: o.integer('version'),
      baseRevision: o.integer('base_revision'),
      changeCount: changes.length,
      diagnostics: [
        for (final e in o.list('diagnostics')) Diagnostic.fromJson(e),
      ],
    );
    o.finish();
    return d;
  }

  final String id;
  final int version;
  final int baseRevision;
  final int changeCount;
  final List<Diagnostic> diagnostics;
}

/// A sealed candidate ready for `configuration.apply`: the "plan" and
/// "review" step. Nothing is active yet.
class Plan {
  const Plan({
    required this.id,
    required this.version,
    required this.draftId,
    required this.baseRevision,
    required this.candidateDigest,
    required this.authorityRequirements,
    required this.decisions,
    required this.diagnostics,
    required this.requirements,
  });

  factory Plan.fromJson(Object? json) {
    final o = StrictObject(json, 'plan');
    final p = Plan(
      id: o.string('id'),
      version: o.integer('version'),
      draftId: o.string('draft_id'),
      baseRevision: o.integer('base_revision'),
      candidateDigest: o.string('candidate_digest'),
      authorityRequirements: [
        for (final e in o.list('authority_requirements'))
          Requirement.fromJson(e),
      ],
      decisions: [
        for (final e in o.list('decisions')) DecisionRequirement.fromJson(e),
      ],
      diagnostics: [
        for (final e in o.list('diagnostics')) Diagnostic.fromJson(e),
      ],
      requirements: [
        for (final e in o.list('requirements')) Requirement.fromJson(e),
      ],
    );
    o.list('changes');
    o.list('dependencies');
    o.string('compiler_version');
    o.string('schema_version');
    o.finish();
    return p;
  }

  final String id;
  final int version;
  final String draftId;
  final int baseRevision;
  final String candidateDigest;
  final List<Requirement> authorityRequirements;
  final List<DecisionRequirement> decisions;
  final List<Diagnostic> diagnostics;
  final List<Requirement> requirements;

  /// Whether this plan can be applied as-is: no diagnostic at error severity
  /// and no outstanding authority/decision requirement.
  bool get isClean =>
      authorityRequirements.isEmpty &&
      decisions.isEmpty &&
      requirements.isEmpty &&
      diagnostics.every((d) => d.severity != 'error');
}

/// The activated result of `configuration.apply`. Once this exists, the
/// plan's changes are live and visible through the ordinary list operations.
class Revision {
  const Revision({
    required this.id,
    required this.version,
    required this.planId,
    required this.candidateDigest,
    required this.activatedAt,
  });

  factory Revision.fromJson(Object? json) {
    final o = StrictObject(json, 'revision');
    final r = Revision(
      id: o.string('id'),
      version: o.integer('version'),
      planId: o.string('plan_id'),
      candidateDigest: o.string('candidate_digest'),
      activatedAt: o.dateTime('activated_at'),
    );
    o.finish();
    return r;
  }

  final String id;
  final int version;
  final String planId;
  final String candidateDigest;
  final DateTime activatedAt;
}

/// A trusted verifier identity this installation actually has, as
/// `installation.verifier.list` reports it. A task or responsibility's
/// acceptance contract must name one of these, never an invented identity.
class VerifierDescriptor {
  const VerifierDescriptor({
    required this.id,
    required this.version,
    required this.kind,
    required this.classification,
  });

  factory VerifierDescriptor.fromJson(Object? json) {
    final o = StrictObject(json, 'verifier descriptor');
    final v = VerifierDescriptor(
      id: o.string('id'),
      version: o.integer('version'),
      kind: o.string('kind'),
      classification: o.string('classification'),
    );
    o.finish();
    return v;
  }

  final String id;
  final int version;
  final String kind;
  final String classification;
}

enum ConnectionValidationState { unverified, valid, invalid, expired, revoked }

/// A provider/account binding a worker's execution profile depends on.
class Connection {
  const Connection({
    required this.id,
    required this.version,
    required this.provider,
    required this.accountIdentity,
    required this.validationState,
  });

  factory Connection.fromJson(Object? json) {
    final o = StrictObject(json, 'connection');
    final stateWire = o.string('validation_state');
    final c = Connection(
      id: o.string('id'),
      version: o.integer('version'),
      provider: o.string('provider'),
      accountIdentity: o.string('account_identity'),
      validationState: ConnectionValidationState.values.firstWhere(
        (s) => s.name == stateWire,
        orElse: () => throw StrictJsonException(
          'connection validation_state "$stateWire" is not in the contract',
        ),
      ),
    );
    o.object('scope');
    o.string('credential_ref');
    o.list('destinations');
    o.list('allowed_scopes');
    o.optionalDateTime('validated_at');
    o.optionalDateTime('valid_until');
    o.finish();
    return c;
  }

  final String id;
  final int version;
  final String provider;
  final String accountIdentity;
  final ConnectionValidationState validationState;
}

/// A bounded async unit of work: `memory.retract`, `memory.recall`,
/// `memory.remember`, `memory.promote` and the artifact upload/export family
/// all commit as a `Job`, resolved later through `*.job.get`. `state` is the
/// only fact this client trusts about progress; a caller never infers
/// completion from having merely submitted the request (the acknowledgment
/// rule applies to jobs exactly as it does to reviews and pauses).
enum JobState { pending, running, succeeded, failed, outcomeUnknown, cancelled }

class Job {
  const Job({
    required this.id,
    required this.version,
    required this.kind,
    required this.state,
    required this.owner,
    required this.operation,
    this.result,
  });

  factory Job.fromJson(Object? json) {
    final o = StrictObject(json, 'job');
    final stateWire = o.string('state');
    final j = Job(
      id: o.string('id'),
      version: o.integer('version'),
      kind: o.string('kind'),
      state: switch (stateWire) {
        'pending' => JobState.pending,
        'running' => JobState.running,
        'succeeded' => JobState.succeeded,
        'failed' => JobState.failed,
        'outcome_unknown' => JobState.outcomeUnknown,
        'cancelled' => JobState.cancelled,
        _ => throw StrictJsonException('job state "$stateWire" is unknown'),
      },
      owner: o.string('owner'),
      operation: o.string('operation'),
      result: o.optional('result'),
    );
    o.list('requirements');
    o.optional('result_artifact');
    o.optionalString('operation_id');
    o.finish();
    return j;
  }

  final String id;
  final int version;
  final String kind;
  final JobState state;
  final String owner;
  final String operation;
  final Object? result;

  String get label => switch (state) {
    JobState.pending => 'Queued',
    JobState.running => 'In progress',
    JobState.succeeded => 'Done',
    JobState.failed => 'Failed',
    JobState.outcomeUnknown => 'Outcome unknown · being reconciled',
    JobState.cancelled => 'Cancelled',
  };
}

/// A worker/organization/installation's authorized access to one memory
/// brain — what `memory.binding.list` reports. `permissions` says exactly
/// what this scope may do with that brain: read, write, curate, promote or
/// retract.
class MemoryBinding {
  const MemoryBinding({
    required this.id,
    required this.version,
    required this.scope,
    required this.brainId,
    required this.permissions,
    required this.classification,
  });

  factory MemoryBinding.fromJson(Object? json) {
    final o = StrictObject(json, 'memory binding');
    final b = MemoryBinding(
      id: o.string('id'),
      version: o.integer('version'),
      scope: Scope.fromJson(o.object('scope')),
      brainId: o.string('brain_id'),
      permissions: o.stringList('permissions'),
      classification: o.string('classification'),
    );
    o.finish();
    return b;
  }

  final String id;
  final int version;
  final Scope scope;
  final String brainId;
  final List<String> permissions;
  final String classification;

  bool get canRetract => permissions.contains('retract');
}

/// One authorized scoped memory claim — a `memory.list`/`memory.inspect`
/// result. Never the product of a local guess: source, freshness and
/// confidence all come from the controller, and `active: false` means a
/// retraction has already excluded it from recall, even though the claim
/// itself (and the fact it once existed) is never hidden from this view.
class Claim {
  const Claim({
    required this.id,
    required this.version,
    required this.brainId,
    required this.text,
    required this.sources,
    required this.confidence,
    required this.freshness,
    required this.active,
    this.curatorId,
    this.redaction,
  });

  factory Claim.fromJson(Object? json) {
    final o = StrictObject(json, 'claim');
    final c = Claim(
      id: o.string('id'),
      version: o.integer('version'),
      brainId: o.string('brain_id'),
      text: o.string('text'),
      sources: [for (final s in o.list('sources')) ArtifactRef.fromJson(s)],
      confidence: o.integer('confidence'),
      freshness: o.dateTime('freshness'),
      active: o.boolean('active'),
      curatorId: o.optionalString('curator_id'),
      redaction: o.optionalString('redaction'),
    );
    o.optionalString('source_brain_id');
    o.optional('source_claim');
    o.finish();
    return c;
  }

  final String id;
  final int version;
  final String brainId;
  final String text;
  final List<ArtifactRef> sources;

  /// 0..1000000 (parts-per-million), never rendered as a bare probability
  /// this client invented meaning for.
  final int confidence;
  final DateTime freshness;
  final bool active;
  final String? curatorId;
  final String? redaction;
}

/// One capability-specific autonomy record — `autonomy.qualification.list`.
/// Never a global trust score: each entry names the exact capability and
/// destinations a rule evaluated, and whether it is qualified, restricted,
/// rejected, proposed or expired.
class Qualification {
  const Qualification({
    required this.id,
    required this.version,
    required this.workerId,
    required this.capability,
    required this.destinations,
    required this.state,
    required this.explanation,
  });

  factory Qualification.fromJson(Object? json) {
    final o = StrictObject(json, 'qualification');
    final q = Qualification(
      id: o.string('id'),
      version: o.integer('version'),
      workerId: o.string('worker_id'),
      capability: o.string('capability'),
      destinations: o.stringList('destinations'),
      state: o.string('state'),
      explanation: o.string('explanation'),
    );
    o.object('rule');
    o.string('model');
    o.list('tool_versions');
    o.list('skill_versions');
    o.list('evidence_ids');
    o.dateTime('window_start');
    o.dateTime('window_end');
    o.finish();
    return q;
  }

  final String id;
  final int version;
  final String workerId;
  final String capability;
  final List<String> destinations;

  /// `proposed`, `qualified`, `rejected`, `restricted` or `expired`.
  final String state;
  final String explanation;
}

/// A cron-like schedule as `schedule.list` reports it — distinct from a
/// `Responsibility`'s event-driven triggers. Real timezone/expression fields
/// this client renders as the actual schedule; trigger strings are never
/// substituted for it.
class Schedule {
  const Schedule({
    required this.id,
    required this.version,
    required this.workerId,
    required this.timezone,
    required this.expression,
    required this.misfire,
    required this.catchUpSeconds,
    required this.paused,
    this.nextWake,
  });

  factory Schedule.fromJson(Object? json) {
    final o = StrictObject(json, 'schedule');
    Scope.fromJson(o.object('scope'));
    final taskTemplate = Task.fromJson(o.object('task_template'));
    final s = Schedule(
      id: o.string('id'),
      version: o.integer('version'),
      workerId: taskTemplate.workerId,
      timezone: o.string('timezone'),
      expression: o.string('expression'),
      misfire: o.string('misfire'),
      catchUpSeconds: o.integer('catch_up_seconds'),
      paused: o.boolean('paused'),
      nextWake: o.optionalDateTime('next_wake'),
    );
    o.finish();
    return s;
  }

  final String id;
  final int version;

  /// The worker this schedule's task template targets — read from
  /// `task_template.worker_id`, never guessed from the schedule's own scope.
  final String workerId;
  final String timezone;
  final String expression;

  /// `coalesce` or `skip`: what happens to occurrences missed while this
  /// schedule could not run.
  final String misfire;
  final int catchUpSeconds;
  final bool paused;
  final DateTime? nextWake;
}
