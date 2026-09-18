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
  static const installationStatus = OperationDescriptor.query(
    'installation.status',
  );
  static const commandGet = OperationDescriptor.query('command.get');
  static const eventList = OperationDescriptor.query('event.list');

  static const organizationList = OperationDescriptor.query(
    'organization.list',
  );
  static const workerList = OperationDescriptor.query('worker.list');

  static const conversationList = OperationDescriptor.query(
    'conversation.list',
  );
  static const conversationMessageSend = OperationDescriptor.mutation(
    'conversation.message.send',
  );

  static const reviewList = OperationDescriptor.query('review.list');
  static const reviewGet = OperationDescriptor.query('review.get');
  static const reviewDecide = OperationDescriptor.mutation(
    'review.decide',
    expectedVersion: true,
  );

  static const taskList = OperationDescriptor.query('task.list');
  static const responsibilityList = OperationDescriptor.query(
    'responsibility.list',
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
  static const mailboxList = OperationDescriptor.query('mailbox.list');

  /// Every operation the client may call.
  static const List<OperationDescriptor> all = [
    installationStatus,
    commandGet,
    eventList,
    organizationList,
    workerList,
    conversationList,
    conversationMessageSend,
    reviewList,
    reviewGet,
    reviewDecide,
    taskList,
    responsibilityList,
    responsibilityPause,
    artifactList,
    artifactRead,
    grantList,
    operationList,
    toolGet,
    usageGet,
    mailboxList,
  ];
}
