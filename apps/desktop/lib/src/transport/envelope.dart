// The versioned request and result envelopes for
// POST /v1/operations/{operation_id}.

import 'dart:convert';

import 'strict_json.dart';

const String requestSchema = 'zatiti.request/v1';
const String resultSchema = 'zatiti.result/v1';

/// The frozen fault vocabulary shared by every transport.
enum FaultCode {
  invalidInput('invalid_input', 400),
  notFound('not_found', 404),
  permissionDenied('permission_denied', 403),
  staleVersion('stale_version', 409),
  reviewRequired('review_required', 409),
  submissionConflict('submission_conflict', 409),
  conflict('conflict', 409),
  cursorExpired('cursor_expired', 410),
  prerequisiteMissing('prerequisite_missing', 422),
  externalActionRequired('external_action_required', 422),
  budgetUnavailable('budget_unavailable', 422),
  capabilityUnsupported('capability_unsupported', 422),
  verificationFailed('verification_failed', 422),
  artifactFault('artifact_fault', 409),
  outcomeUnknown('outcome_unknown', 409),
  controllerUnavailable('controller_unavailable', 503),
  internalError('internal_error', 500),

  /// A code this client build does not know. The wire string stays on
  /// [Fault.wireCode]; the client treats it as a non-retryable failure and
  /// never guesses a meaning for it.
  unrecognized('', 500);

  const FaultCode(this.wire, this.httpStatus);

  /// The frozen string value on the wire.
  final String wire;

  /// The HTTP status the controller renders this fault at.
  final int httpStatus;

  static FaultCode? fromWire(String wire) {
    for (final code in values) {
      if (code != unrecognized && code.wire == wire) return code;
    }
    return null;
  }
}

/// Result payload status values.
enum ResultStatus {
  completed('completed'),
  accepted('accepted'),
  failed('failed');

  const ResultStatus(this.wire);
  final String wire;

  static ResultStatus? fromWire(String wire) {
    for (final s in values) {
      if (s.wire == wire) return s;
    }
    return null;
  }
}

/// A fault carried by a failed result envelope.
class Fault implements Exception {
  const Fault({
    required this.code,
    required this.message,
    required this.retryable,
    this.details,
    String? wireCode,
  }) : _wireCode = wireCode;

  factory Fault.fromJson(Object? json) {
    final o = StrictObject(json, 'fault');
    final wire = o.string('code');
    if (wire.isEmpty) {
      throw StrictJsonException('fault carries an empty code');
    }
    final fault = Fault(
      code: FaultCode.fromWire(wire) ?? FaultCode.unrecognized,
      wireCode: wire,
      message: o.string('message'),
      retryable: o.boolean('retryable'),
      details: o.optionalObject('details'),
    );
    o.finish();
    return fault;
  }

  final FaultCode code;
  final String? _wireCode;
  final String message;
  final bool retryable;

  /// The code string exactly as it arrived.
  String get wireCode => _wireCode ?? code.wire;

  /// Open extension space; inert data, never authority.
  final Map<String, Object?>? details;

  /// The `snapshot_required` detail of a `cursor_expired` fault.
  bool get snapshotRequired => details?['snapshot_required'] == true;

  @override
  String toString() => '$wireCode: $message';
}

/// The result envelope (`zatiti.result/v1`).
class ResultEnvelope {
  const ResultEnvelope({
    required this.commandId,
    required this.status,
    required this.data,
    required this.error,
    required this.nextCursor,
  });

  /// Decodes strictly. Every envelope field is required, no other field is
  /// allowed, and status must agree with the presence of a fault.
  factory ResultEnvelope.fromJson(Object? json) {
    final o = StrictObject(json, 'result envelope');
    final schema = o.string('schema');
    if (schema != resultSchema) {
      throw StrictJsonException(
        'result envelope schema "$schema", want "$resultSchema"',
      );
    }
    final commandId = o.string('command_id');
    if (commandId.isEmpty) {
      throw StrictJsonException('result envelope has no command_id');
    }
    final statusWire = o.string('status');
    final status = ResultStatus.fromWire(statusWire);
    if (status == null) {
      throw StrictJsonException(
        'result envelope status "$statusWire" is not completed, accepted '
        'or failed',
      );
    }
    final rawData = o.nullable('data');
    if (rawData != null && rawData is! Map<String, Object?>) {
      throw StrictJsonException('result envelope data must be object or null');
    }
    final rawError = o.nullable('error');
    final rawCursor = o.nullable('next_cursor');
    if (rawCursor != null && rawCursor is! String) {
      throw StrictJsonException('next_cursor must be string or null');
    }
    o.finish();

    final error = rawError == null ? null : Fault.fromJson(rawError);
    if (status == ResultStatus.failed && error == null) {
      throw StrictJsonException('failed result envelope carries no fault');
    }
    if (status != ResultStatus.failed && error != null) {
      throw StrictJsonException(
        'result envelope status "${status.wire}" carries a fault',
      );
    }
    return ResultEnvelope(
      commandId: commandId,
      status: status,
      data: rawData as Map<String, Object?>?,
      error: error,
      nextCursor: rawCursor as String?,
    );
  }

  final String commandId;
  final ResultStatus status;
  final Map<String, Object?>? data;
  final Fault? error;
  final String? nextCursor;

  /// The data object of a non-failed envelope.
  Map<String, Object?> requireData(String operation) {
    final d = data;
    if (d == null) {
      throw StrictJsonException('$operation returned no data');
    }
    return d;
  }
}

/// The HTTP statuses an operation result may arrive on, and the envelope
/// status each one may carry.
const Map<int, ResultStatus> envelopeStatusByHttp = {
  200: ResultStatus.completed,
  202: ResultStatus.accepted,
  400: ResultStatus.failed,
  403: ResultStatus.failed,
  404: ResultStatus.failed,
  409: ResultStatus.failed,
  410: ResultStatus.failed,
  422: ResultStatus.failed,
  500: ResultStatus.failed,
  503: ResultStatus.failed,
};

/// Checks that [envelope] agrees with the HTTP status it arrived on.
void validateAgainstHttp(int httpStatus, ResultEnvelope envelope) {
  final want = envelopeStatusByHttp[httpStatus];
  if (want == null) {
    throw StrictJsonException(
      'unexpected HTTP status $httpStatus for an operation result',
    );
  }
  if (want != envelope.status) {
    throw StrictJsonException(
      'HTTP status $httpStatus disagrees with envelope status '
      '"${envelope.status.wire}"',
    );
  }
  final fault = envelope.error;
  if (fault != null &&
      fault.code != FaultCode.unrecognized &&
      fault.code.httpStatus != httpStatus) {
    throw StrictJsonException(
      'HTTP status $httpStatus disagrees with fault code '
      '"${fault.wireCode}"',
    );
  }
}

/// Renders the exact request body bytes for one call.
List<int> encodeRequest({
  required Map<String, Object?> input,
  String? submissionKey,
}) {
  return utf8.encode(
    jsonEncode(<String, Object?>{
      'schema': requestSchema,
      if (submissionKey != null) 'submission_key': submissionKey,
      'input': input,
    }),
  );
}

final RegExp _operationIdPattern = RegExp(r'^[a-z0-9_]+(\.[a-z0-9_]+)*$');

/// Accepts the frozen catalog shape only: dot-separated `[a-z0-9_]` segments.
/// The identifier is a path segment, so anything else is refused, not escaped.
bool isValidOperationId(String id) =>
    id.length <= 200 && _operationIdPattern.hasMatch(id);

/// The frozen submission key bound: 1..128 printable ASCII characters.
bool isValidSubmissionKey(String key) {
  if (key.isEmpty || key.length > 128) return false;
  for (final c in key.codeUnits) {
    if (c < 0x20 || c > 0x7E) return false;
  }
  return true;
}
