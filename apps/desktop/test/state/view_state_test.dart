import 'package:flutter_test/flutter_test.dart';
import 'package:zatiti_desktop/src/state/snapshot.dart';
import 'package:zatiti_desktop/src/state/view_state.dart';

ReviewEntry _review(ReviewRecordState state, EffectState effect) => ReviewEntry(
  id: const ReviewId('r'),
  version: 1,
  actionDigest: 'd',
  state: state,
  effect: effect,
  title: 't',
  consequence: 'c',
  action: 'a',
  accountIdentity: 'i',
  destination: 'd',
  costBound: '0.00 USD',
  expiresAt: DateTime.utc(2026, 9, 18, 18),
  content: const [],
  parameters: const {},
  evidence: const [],
);

void main() {
  final now = DateTime.utc(2026, 9, 18, 12);

  test('an accepted request is not a delivered outcome', () {
    const approved = ReviewRecordState.approved;
    expect(
      phaseOfRecord(_review(approved, EffectState.notStarted), now),
      ReviewPhase.approved,
    );
    expect(
      phaseOfRecord(_review(approved, EffectState.deliveryAccepted), now),
      ReviewPhase.deliveryAccepted,
    );
    expect(
      phaseOfRecord(_review(approved, EffectState.succeeded), now),
      ReviewPhase.delivered,
    );
    expect(ReviewPhase.deliveryAccepted.label, contains('not confirmed'));
  });

  test('an unknown outcome is explicit and keeps its reservation', () {
    final phase = phaseOfRecord(
      _review(ReviewRecordState.approved, EffectState.outcomeUnknown),
      now,
    );
    expect(phase, ReviewPhase.outcomeUnknown);
    expect(phase.label, contains('reservation kept'));
    expect(phase.needsYou, isFalse);
  });

  test('a pending review past its expiry is expired, not decidable', () {
    final late = DateTime.utc(2026, 9, 18, 19);
    final phase = phaseOfRecord(
      _review(ReviewRecordState.pending, EffectState.notStarted),
      late,
    );
    expect(phase, ReviewPhase.expired);
    expect(phase.allowsDecision, isFalse);
  });

  test('only awaiting decision allows a decision', () {
    for (final p in ReviewPhase.values) {
      expect(
        p.allowsDecision,
        p == ReviewPhase.awaitingDecision,
        reason: p.name,
      );
    }
  });

  test('a review with unshowable content is not showable', () {
    const part = ReviewContentPart(
      label: 'l',
      digest: 'd',
      unavailableReason: 'binary',
    );
    expect(part.isShowable, isFalse);
  });
}
