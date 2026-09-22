// A schema-valid acceptance contract this client can honestly construct on
// its own, for the one acceptance mode it supports from a "New task" or "New
// responsibility" form: `mode: "manual"`.
//
// `internal/tasks/acceptance.go`'s `evaluateSuccess` refuses to establish
// success any way except `task.accept` when `acceptance.mode` is `"manual"`,
// and never dereferences `profile` to do it (confirmed by reading that
// function directly, not inferred). So the placeholder digests below are
// never evaluated against anything real; they exist only because the wire
// schema requires the `profile` object's shape unconditionally.
//
// `mode: "independent"` (automated verification) needs a capability-evidence
// artifact whose bytes an operator computes and uploads by hand --
// `internal/installation/firsttask.go`'s own documented sequence spells out
// SIZE/DIGEST/BASE64 as values a person fills in, not something any
// operation returns. A thin client with no filesystem access, no business
// logic and no cryptographic-evidence authoring role (this root's AGENTS.md)
// cannot construct that correctly, so this client never offers it. That gap
// is reported in the P43 handoff rather than papered over here.

import 'models.dart';

/// 64 lowercase hex characters: a syntactically valid but inert digest.
final String _placeholderDigest = '0'.padRight(64, '0');

/// A syntactically valid but inert artifact id.
const String _placeholderArtifactId = '00000000-0000-4000-8000-000000000000';

/// The `Acceptance` object for a manually-decided task or responsibility:
/// the eligible human, not an automated verifier, establishes success or
/// failure by calling `task.accept` once real work exists to look at.
/// [verifier] must be one `installation.verifier.list` actually reports —
/// never an invented identity — even though this mode never evaluates its
/// nested profile fields.
Map<String, Object?> manualAcceptance(VerifierDescriptor verifier) {
  final version = verifier.version.toString();
  return {
    'verifier_id': verifier.id,
    'verifier_version': version,
    'sealed_inputs': const <Object?>[],
    'expected_observations': const <Object?>[],
    'mode': 'manual',
    'required_child_ids': const <Object?>[],
    'profile': {
      'schema': 'zatiti.verifier-profile/v1',
      'kind': 'artifact_contract',
      'id': verifier.id,
      'version': version,
      'code_digest': _placeholderDigest,
      'supported_checks': const ['presence'],
      'max_bytes': 1048576,
      'timeout_seconds': 300,
      'capability_evidence': {
        'artifact': {
          'id': _placeholderArtifactId,
          'digest': _placeholderDigest,
        },
        'adapter_version': version,
        'source_revision': 'unqualified',
        'protocol_revision': version,
        'profile_digest': _placeholderDigest,
        'qualified_at': '2026-01-01T00:00:00Z',
        'capabilities': const ['presence'],
        'limitations': const [
          'manual acceptance: this profile is never evaluated (see '
              'internal/tasks/acceptance.go evaluateSuccess); the eligible '
              'human decides through task.accept',
        ],
      },
    },
  };
}

/// A zero-spend `Limits` object: `currency: "XXX"` is the wire's explicit
/// "no currency configured" sentinel (`internal/installation/
/// firsttask.go`), the only spend an unconfigured worker's budget ever
/// admits. [currency] lets a caller reuse a worker's already-configured
/// currency instead, still at zero spend so this client never proposes a
/// real cost on the person's behalf.
Map<String, Object?> zeroSpendLimits({
  required DateTime rootDeadline,
  String currency = 'XXX',
}) => {
  'currency': currency,
  'spend_micro_units': 0,
  'concurrency': 1,
  'model_steps': 1,
  'child_count': 0,
  'delegation_depth': 0,
  'attempt_seconds': 60,
  'root_deadline': rootDeadline.toUtc().toIso8601String(),
};
