// The subset of the public operation catalog this client calls.
//
// `test/transport/catalog_parity_test.dart` checks every entry against
// `docs/implementation/operations.json`, so a drifted identifier, version or
// mode fails the build instead of failing at run time.

/// One public operation as the client needs to know it.
class OperationDescriptor {
  const OperationDescriptor.query(this.id, {this.version = 1})
    : isMutation = false,
      expectedVersion = false;

  const OperationDescriptor.mutation(
    this.id, {
    this.version = 1,
    this.expectedVersion = false,
  }) : isMutation = true;

  final String id;
  final int version;

  /// Mutations carry a submission key; queries never do.
  final bool isMutation;

  /// Whether the input carries `expected_version`.
  final bool expectedVersion;
}

abstract final class Operations {
  static const capabilitiesList = OperationDescriptor.query(
    'capabilities.list',
  );
  static const installationStatus = OperationDescriptor.query(
    'installation.status',
  );
  static const commandGet = OperationDescriptor.query('command.get');
  static const eventList = OperationDescriptor.query('event.list');

  /// Who holds authority in this installation. Read-only: this client never
  /// creates, updates or revokes an identity.
  static const principalList = OperationDescriptor.query('principal.list');

  static const organizationList = OperationDescriptor.query(
    'organization.list',
  );
  static const organizationCreate = OperationDescriptor.mutation(
    'organization.create',
  );
  static const workerList = OperationDescriptor.query('worker.list');
  static const workerCreate = OperationDescriptor.mutation('worker.create');
  static const workerGet = OperationDescriptor.query('worker.get');
  static const workerUpdate = OperationDescriptor.mutation(
    'worker.update',
    expectedVersion: true,
  );

  static const conversationList = OperationDescriptor.query(
    'conversation.list',
  );
  static const conversationMessageList = OperationDescriptor.query(
    'conversation.message.list',
  );
  static const conversationMessageSend = OperationDescriptor.mutation(
    'conversation.message.send',
  );
  static const conversationCreate = OperationDescriptor.mutation(
    'conversation.create',
  );
  static const conversationUpdate = OperationDescriptor.mutation(
    'conversation.update',
    expectedVersion: true,
  );

  static const reviewList = OperationDescriptor.query('review.list');
  static const reviewGet = OperationDescriptor.query('review.get');
  static const reviewDecide = OperationDescriptor.mutation(
    'review.decide',
    expectedVersion: true,
  );

  static const configurationPlan = OperationDescriptor.mutation(
    'configuration.plan',
    expectedVersion: true,
  );
  static const configurationApply = OperationDescriptor.mutation(
    'configuration.apply',
  );

  static const taskList = OperationDescriptor.query('task.list');
  static const taskCreate = OperationDescriptor.mutation('task.create');
  static const taskStart = OperationDescriptor.mutation(
    'task.start',
    expectedVersion: true,
  );
  static const taskDelegate = OperationDescriptor.mutation(
    'task.delegate',
    expectedVersion: true,
  );
  static const taskAccept = OperationDescriptor.mutation(
    'task.accept',
    expectedVersion: true,
  );
  static const responsibilityList = OperationDescriptor.query(
    'responsibility.list',
  );
  static const responsibilityCreate = OperationDescriptor.mutation(
    'responsibility.create',
  );
  static const responsibilityPause = OperationDescriptor.mutation(
    'responsibility.pause',
    expectedVersion: true,
  );
  static const artifactList = OperationDescriptor.query('artifact.list');
  static const artifactRead = OperationDescriptor.query('artifact.read');
  static const grantList = OperationDescriptor.query('grant.list');
  static const operationList = OperationDescriptor.query('operation.list');
  static const toolGet = OperationDescriptor.query('tool.get');
  static const usageGet = OperationDescriptor.query('usage.get');
  static const budgetGet = OperationDescriptor.query('budget.get');
  static const connectionList = OperationDescriptor.query('connection.list');
  static const connectionGet = OperationDescriptor.query('connection.get');
  static const connectionCreate = OperationDescriptor.mutation(
    'connection.create',
  );
  static const modelProviderList = OperationDescriptor.query(
    'model.provider.list',
  );
  static const executionProfileList = OperationDescriptor.query(
    'execution_profile.list',
  );
  static const executionProfileCreate = OperationDescriptor.mutation(
    'execution_profile.create',
  );
  static const executionProfileQualify = OperationDescriptor.mutation(
    'execution_profile.qualify',
  );
  static const installationVerifierList = OperationDescriptor.query(
    'installation.verifier.list',
  );

  // ---- memory: authorized claims, source/freshness and retraction --------

  static const memoryBindingList = OperationDescriptor.query(
    'memory.binding.list',
  );
  static const memoryList = OperationDescriptor.query('memory.list');
  static const memoryRetract = OperationDescriptor.mutation('memory.retract');
  static const memoryJobGet = OperationDescriptor.query('memory.job.get');
  static const jobGet = OperationDescriptor.query('job.get');

  // ---- responsibility-to-schedule links -----------------------------------

  static const scheduleList = OperationDescriptor.query('schedule.list');

  // ---- autonomy evidence and recovery obligations -------------------------

  static const autonomyQualificationList = OperationDescriptor.query(
    'autonomy.qualification.list',
  );
  static const runList = OperationDescriptor.query('run.list');
  static const runRecovery = OperationDescriptor.query('run.recovery');

  /// Every operation the client may call.
  static const List<OperationDescriptor> all = [
    capabilitiesList,
    installationStatus,
    commandGet,
    eventList,
    principalList,
    organizationList,
    organizationCreate,
    workerList,
    workerCreate,
    workerGet,
    workerUpdate,
    conversationList,
    conversationMessageList,
    conversationMessageSend,
    conversationCreate,
    conversationUpdate,
    reviewList,
    reviewGet,
    reviewDecide,
    configurationPlan,
    configurationApply,
    taskList,
    taskCreate,
    taskStart,
    taskDelegate,
    taskAccept,
    responsibilityList,
    responsibilityCreate,
    responsibilityPause,
    artifactList,
    artifactRead,
    grantList,
    operationList,
    toolGet,
    usageGet,
    budgetGet,
    connectionList,
    connectionGet,
    connectionCreate,
    modelProviderList,
    executionProfileList,
    executionProfileCreate,
    executionProfileQualify,
    installationVerifierList,
    memoryBindingList,
    memoryList,
    memoryRetract,
    memoryJobGet,
    jobGet,
    scheduleList,
    autonomyQualificationList,
    runList,
    runRecovery,
  ];
}
