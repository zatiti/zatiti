// Typed calls over the controller client. Inputs follow the catalog's input
// schemas; outputs decode strictly into the wire models.

import '../transport/controller_client.dart';
import '../transport/operations.dart';
import '../transport/strict_json.dart';
import '../transport/submission.dart';
import 'models.dart';

/// Pages a list operation reads at most, so one snapshot stays bounded.
const int maxListPages = 25;

class ControllerApi {
  ControllerApi(this.client);

  final ControllerClient client;

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
  Submission prepareMessageSend({
    required String conversationId,
    required String body,
  }) => client.prepare(Operations.conversationMessageSend, {
    'scope': client.scope(),
    'conversation_id': conversationId,
    'message_id': newUuidV4(),
    'body': body,
    'attachments': const <Object?>[],
    'task_ids': const <Object?>[],
  });

  Submission prepareResponsibilityPause({
    required String id,
    required int expectedVersion,
  }) => client.prepare(Operations.responsibilityPause, {
    'scope': client.scope(),
    'id': id,
    'expected_version': expectedVersion,
  });

  Object? _resource(Map<String, Object?>? data, String operation) {
    if (data == null) throw StrictJsonException('$operation returned no data');
    final o = StrictObject(data, operation);
    final resource = o.object('resource');
    o.finish();
    return resource;
  }
}
