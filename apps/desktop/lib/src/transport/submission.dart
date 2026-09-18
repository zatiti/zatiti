// Submission identity for mutations.

import 'dart:math';

import 'envelope.dart';

/// Where one submission stands from the client's point of view.
enum SubmissionState {
  /// Prepared and never sent.
  fresh,

  /// A send attempt is in flight.
  sending,

  /// The last attempt provably sent nothing. An explicit retry may reuse
  /// this submission.
  notSent,

  /// Bytes may have been sent and no envelope came back. The disposition
  /// must be looked up before anything else happens.
  acknowledgmentUnknown,

  /// A lookup proved the command never committed. An explicit retry may
  /// reuse this submission.
  notCommitted,

  /// The controller returned an authoritative envelope.
  settled,
}

/// One mutation with its frozen identity: the operation, its version, the
/// submission key and the exact request bytes. A retry reuses this object, so
/// it cannot change the key or the bytes.
class Submission {
  Submission({
    required this.operation,
    required this.operationVersion,
    required this.key,
    required Map<String, Object?> input,
  }) : body = List<int>.unmodifiable(
         encodeRequest(input: input, submissionKey: key),
       );

  final String operation;
  final int operationVersion;
  final String key;

  /// The exact request body. Every attempt sends these bytes.
  final List<int> body;

  SubmissionState state = SubmissionState.fresh;

  /// Number of times the request bytes were handed to the network.
  int attempts = 0;

  bool get maySend =>
      state == SubmissionState.fresh ||
      state == SubmissionState.notSent ||
      state == SubmissionState.notCommitted;
}

final Random _secureRandom = Random.secure();

/// Mints a random UUIDv4 in the lowercase hyphenated wire form.
String newUuidV4([Random? random]) {
  final r = random ?? _secureRandom;
  final b = List<int>.generate(16, (_) => r.nextInt(256));
  b[6] = (b[6] & 0x0F) | 0x40;
  b[8] = (b[8] & 0x3F) | 0x80;
  final hex = b.map((v) => v.toRadixString(16).padLeft(2, '0')).join();
  return '${hex.substring(0, 8)}-${hex.substring(8, 12)}-'
      '${hex.substring(12, 16)}-${hex.substring(16, 20)}-${hex.substring(20)}';
}

/// Mints a submission key: unique per intended mutation, printable ASCII,
/// well under the 128-character bound.
String newSubmissionKey([Random? random]) => 'desktop-${newUuidV4(random)}';
