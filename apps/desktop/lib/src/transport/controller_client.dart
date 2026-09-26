// The controller client: one POST per call to /v1/operations/{operation_id}
// over the private Unix socket or mutual TLS.
//
// Retry policy. This client never resends anything on its own. A mutation is
// sent when the caller asks, with the submission key minted once by
// [ControllerClient.prepare]. When an attempt fails, the failure says whether
// bytes could have reached the controller:
//
//   * [ControllerUnavailableException]: nothing was sent. The caller may call
//     [ControllerClient.submit] again with the same [Submission].
//   * [OutcomeUnknownException]: bytes may have been sent. The submission
//     locks until [ControllerClient.resolve] looks the command up through
//     `command.get`. Only a `not_found` lookup unlocks an explicit retry, and
//     that retry still carries the same key and the same bytes.

import 'dart:async';
import 'dart:io';

import 'endpoint.dart';
import 'envelope.dart';
import 'errors.dart';
import 'operations.dart';
import 'strict_json.dart';
import 'submission.dart';

/// Supplies the complete Authorization header value for one call, read from
/// secure storage. The value never enters a request body, a log line or an
/// exception message.
typedef CredentialSource = Future<String?> Function();

/// A defensive ceiling on one response body, far above any legitimate page.
const int maxResponseBytes = 8 << 20;

/// What `command.get` established about an unknown acknowledgment.
sealed class Disposition {
  const Disposition();
}

/// The controller retained the command. [original] is the complete original
/// result envelope, including a refused command's fault.
final class DispositionFound extends Disposition {
  const DispositionFound(this.original);
  final ResultEnvelope original;
}

/// The command never committed. An explicit retry with the same submission
/// is safe.
final class DispositionNotCommitted extends Disposition {
  const DispositionNotCommitted();
}

class ControllerClient {
  ControllerClient({
    required this.endpoint,
    required this.installationId,
    required CredentialSource credentials,
    this.timeout = const Duration(seconds: 30),
    String Function()? submissionKeys,
  }) : _credentials = credentials,
       _submissionKeys = submissionKeys ?? newSubmissionKey,
       _securityContext = switch (endpoint) {
         RemoteTlsEndpoint e => e.buildSecurityContext(),
         LocalSocketEndpoint _ => null,
       };

  final ControllerEndpoint endpoint;

  /// The installation every call is scoped to.
  final String installationId;

  /// Bounds one exchange, connection through body read.
  final Duration timeout;

  final CredentialSource _credentials;
  final String Function() _submissionKeys;
  final SecurityContext? _securityContext;

  /// The installation scope object most inputs start from.
  Map<String, Object?> scope({
    String? organizationId,
    String? workerId,
    String? taskId,
  }) => <String, Object?>{
    'installation_id': installationId,
    'organization_id': ?organizationId,
    'worker_id': ?workerId,
    'task_id': ?taskId,
  };

  /// Runs one query. Queries carry no submission key and are safe to reissue.
  Future<ResultEnvelope> query(
    OperationDescriptor operation,
    Map<String, Object?> input,
  ) async {
    if (operation.isMutation) {
      throw InvalidRequestException(
        '${operation.id} is a mutation; use prepare and submit',
      );
    }
    _checkOperationId(operation.id);
    final body = encodeRequest(input: input);
    final attempt = await _exchange(operation.id, body);
    switch (attempt) {
      case _Answered(:final envelope):
        return _returnOrThrow(operation.id, envelope);
      case _NotSent(:final error):
        throw error;
      case _MaybeSent(:final cause):
        throw OutcomeUnknownException(
          'no authoritative answer for ${operation.id}: $cause',
          operation: operation.id,
        );
    }
  }

  /// The one local bootstrap exception: unauthenticated, with no submission
  /// key. It is never retried automatically after an ambiguous send.
  Future<ResultEnvelope> bootstrapInstallation(String ownerName) async {
    if (endpoint is! LocalSocketEndpoint) {
      throw const InvalidRequestException('bootstrap requires a local socket');
    }
    if (ownerName.trim().isEmpty || ownerName.runes.length > 8192) {
      throw const InvalidRequestException(
        'owner name must be 1..8192 characters',
      );
    }
    final attempt = await _exchange(
      'installation.init',
      encodeRequest(
        input: {'credential_store': 'os', 'owner_name': ownerName.trim()},
      ),
      omitCredential: true,
    );
    switch (attempt) {
      case _Answered(:final envelope):
        return _returnOrThrow('installation.init', envelope);
      case _NotSent(:final error):
        throw error;
      case _MaybeSent(:final cause):
        throw OutcomeUnknownException(
          'installation.init acknowledgment unknown: $cause',
          operation: 'installation.init',
        );
    }
  }

  /// Freezes one intended mutation: mints its submission key and renders the
  /// request bytes once.
  Submission prepare(
    OperationDescriptor operation,
    Map<String, Object?> input,
  ) {
    if (!operation.isMutation) {
      throw InvalidRequestException('${operation.id} is a query; use query');
    }
    _checkOperationId(operation.id);
    final key = _submissionKeys();
    if (!isValidSubmissionKey(key)) {
      throw const InvalidRequestException(
        'submission key must be 1..128 printable ASCII characters',
      );
    }
    return Submission(
      operation: operation.id,
      operationVersion: operation.version,
      key: key,
      input: input,
    );
  }

  /// Sends [submission] exactly once.
  ///
  /// Throws [StateError] when the submission is in flight, settled, or its
  /// acknowledgment is unknown and has not been resolved.
  Future<ResultEnvelope> submit(Submission submission) async {
    if (!submission.maySend) {
      throw StateError(
        'submission ${submission.operation} is ${submission.state.name}; '
        '${submission.state == SubmissionState.acknowledgmentUnknown ? 'resolve its disposition before any retry' : 'it cannot be sent again'}',
      );
    }
    submission.state = SubmissionState.sending;
    final attempt = await _exchange(
      submission.operation,
      submission.body,
      onRequestWritten: () => submission.attempts++,
    );
    switch (attempt) {
      case _Answered(:final envelope):
        submission.state = SubmissionState.settled;
        return _returnOrThrow(submission.operation, envelope);
      case _NotSent(:final error):
        submission.state = SubmissionState.notSent;
        throw error;
      case _MaybeSent(:final cause):
        submission.state = SubmissionState.acknowledgmentUnknown;
        throw OutcomeUnknownException(
          'acknowledgment unknown for ${submission.operation}: $cause',
          operation: submission.operation,
          submission: submission,
        );
    }
  }

  /// Looks an unknown acknowledgment up through `command.get`, by the
  /// original submission key.
  ///
  /// A transport failure of the lookup itself leaves the submission locked.
  Future<Disposition> resolve(Submission submission) async {
    if (submission.state != SubmissionState.acknowledgmentUnknown) {
      throw StateError(
        'submission ${submission.operation} is ${submission.state.name}; '
        'only an unknown acknowledgment is resolved',
      );
    }
    final ResultEnvelope lookup;
    try {
      lookup = await query(Operations.commandGet, <String, Object?>{
        'scope': scope(),
        'submission_key': submission.key,
        'operation': submission.operation,
        'operation_version': submission.operationVersion,
      });
    } on OperationFailedException catch (e) {
      if (e.fault.code == FaultCode.notFound) {
        submission.state = SubmissionState.notCommitted;
        return const DispositionNotCommitted();
      }
      rethrow;
    }
    final command = StrictObject(
      lookup.requireData('command.get')['resource'],
      'command',
    );
    final original = ResultEnvelope.fromJson(command.object('result'));
    if (command.string('submission_key') != submission.key ||
        command.string('operation') != submission.operation) {
      throw StrictJsonException(
        'command.get returned a command for a different submission',
      );
    }
    submission.state = SubmissionState.settled;
    return DispositionFound(original);
  }

  ResultEnvelope _returnOrThrow(String operation, ResultEnvelope envelope) {
    if (envelope.status == ResultStatus.failed) {
      throw OperationFailedException(operation, envelope);
    }
    return envelope;
  }

  void _checkOperationId(String id) {
    if (!isValidOperationId(id)) {
      throw InvalidRequestException('operation "$id" is not a catalog id');
    }
  }

  /// Performs exactly one HTTP exchange on a fresh connection.
  Future<_Attempt> _exchange(
    String operation,
    List<int> body, {
    void Function()? onRequestWritten,
    bool omitCredential = false,
  }) async {
    final String? credential;
    try {
      credential = omitCredential ? null : await _credentials();
    } on Object {
      return const _NotSent(
        ControllerUnavailableException('the credential could not be read'),
      );
    }
    if (credential != null &&
        (credential.contains('\r') || credential.contains('\n'))) {
      return const _NotSent(
        InvalidRequestException('the credential is not a valid header value'),
      );
    }

    var connected = false;
    final http = HttpClient(context: _securityContext)
      ..findProxy = ((_) => 'DIRECT')
      ..connectionTimeout = timeout
      ..idleTimeout = Duration.zero
      ..connectionFactory = (uri, _, _) async {
        final task = switch (endpoint) {
          LocalSocketEndpoint e => await Socket.startConnect(
            InternetAddress(e.socketPath, type: InternetAddressType.unix),
            0,
          ),
          RemoteTlsEndpoint _ => await SecureSocket.startConnect(
            uri.host,
            uri.port,
            context: _securityContext,
          ),
        };
        return ConnectionTask.fromSocket(
          task.socket.then((socket) {
            connected = true;
            return socket;
          }),
          task.cancel,
        );
      };

    try {
      final uri = endpoint.baseUri.replace(path: '/v1/operations/$operation');
      final result = await () async {
        final request = await http.postUrl(uri);
        request.followRedirects = false;
        request.persistentConnection = false;
        request.headers.contentType = ContentType('application', 'json');
        request.headers.set(HttpHeaders.acceptHeader, 'application/json');
        request.contentLength = body.length;
        if (credential != null && credential.isNotEmpty) {
          request.headers.set(HttpHeaders.authorizationHeader, credential);
        }
        onRequestWritten?.call();
        request.add(body);
        final response = await request.close();
        final bytes = <int>[];
        await for (final chunk in response) {
          bytes.addAll(chunk);
          if (bytes.length > maxResponseBytes) {
            throw StrictJsonException(
              'response body exceeds $maxResponseBytes bytes',
            );
          }
        }
        final envelope = ResultEnvelope.fromJson(decodeStrictJsonBytes(bytes));
        validateAgainstHttp(response.statusCode, envelope);
        return envelope;
      }().timeout(timeout);
      return _Answered(result);
    } on HandshakeException catch (e) {
      return _NotSent(TlsCertificateException(e.message));
    } on TlsException catch (e) {
      if (!connected) return _NotSent(TlsCertificateException(e.message));
      return _MaybeSent('TLS failure after connect: ${e.message}');
    } on SocketException catch (e) {
      if (!connected) {
        return _NotSent(ControllerUnavailableException(e.message));
      }
      return _MaybeSent('connection lost: ${e.message}');
    } on TimeoutException {
      if (!connected) {
        return const _NotSent(
          ControllerUnavailableException('timed out while connecting'),
        );
      }
      return const _MaybeSent('timed out waiting for the result envelope');
    } on StrictJsonException catch (e) {
      return _MaybeSent('malformed result envelope: ${e.message}');
    } on HttpException catch (e) {
      if (!connected) {
        return _NotSent(ControllerUnavailableException(e.message));
      }
      return _MaybeSent('HTTP failure: ${e.message}');
    } finally {
      http.close(force: true);
    }
  }
}

sealed class _Attempt {
  const _Attempt();
}

final class _Answered extends _Attempt {
  const _Answered(this.envelope);
  final ResultEnvelope envelope;
}

final class _NotSent extends _Attempt {
  const _NotSent(this.error);
  final TransportException error;
}

final class _MaybeSent extends _Attempt {
  const _MaybeSent(this.cause);
  final String cause;
}
