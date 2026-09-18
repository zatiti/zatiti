// Failure classes of one controller call. The split that matters is whether
// request bytes could have reached the controller.

import 'envelope.dart';
import 'submission.dart';

/// Base class of every transport failure.
sealed class TransportException implements Exception {
  const TransportException(this.message);
  final String message;

  @override
  String toString() => '$runtimeType: $message';
}

/// The request failed local validation. Nothing was sent.
final class InvalidRequestException extends TransportException {
  const InvalidRequestException(super.message);
}

/// The connection was never established. No request byte was sent, so the
/// identical call may be reissued; a mutation keeps its submission key.
final class ControllerUnavailableException extends TransportException {
  const ControllerUnavailableException(super.message);
}

/// The remote controller's certificate failed verification, or the client
/// certificate was refused. A permanent trust or configuration failure: no
/// request byte was sent and retrying does not help.
final class TlsCertificateException extends TransportException {
  const TlsCertificateException(super.message);
}

/// Request bytes may have reached the controller, but no authoritative
/// envelope came back.
///
/// For a mutation, [submission] is set and the caller must look the command
/// up with `ControllerClient.resolve` before doing anything else. For a query
/// [submission] is null and the query may be reissued.
final class OutcomeUnknownException extends TransportException {
  const OutcomeUnknownException(
    super.message, {
    required this.operation,
    this.submission,
  });

  final String operation;
  final Submission? submission;
}

/// The controller answered with a well-formed failed envelope. The envelope
/// is the authoritative disposition of the command.
final class OperationFailedException extends TransportException {
  OperationFailedException(this.operation, this.envelope)
    : super('${envelope.error}');

  final String operation;
  final ResultEnvelope envelope;

  Fault get fault => envelope.error!;
}
