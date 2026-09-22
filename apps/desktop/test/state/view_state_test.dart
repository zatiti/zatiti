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

  group('memory claim retraction', () {
    MemoryEntry claim({bool active = true, bool canRetract = true}) =>
        MemoryEntry(
          id: const ClaimId('c1'),
          workerId: const WorkerId('w1'),
          bindingId: 'b1',
          brainId: 'brain1',
          claimVersion: 1,
          title: 't',
          text: 'text',
          provenance: const [],
          freshness: now,
          active: active,
          canRetract: canRetract,
        );

    test('an active, authorized claim can be retracted', () {
      final view = MemoryClaimView(claim: claim());
      expect(view.canRetract, isTrue);
      expect(view.statusLabel, 'Active');
    });

    test(
      'a claim without retract permission is unsupported, never offered',
      () {
        final view = MemoryClaimView(claim: claim(canRetract: false));
        expect(view.canRetract, isFalse);
      },
    );

    test('a retracted claim stays visible, labeled retracted', () {
      final view = MemoryClaimView(claim: claim(active: false));
      expect(view.canRetract, isFalse);
      expect(view.statusLabel, 'Retracted');
    });

    test('a submission in flight is shown before any job status exists', () {
      final view = MemoryClaimView(
        claim: claim(),
        phase: ClaimActionPhase.submitting,
      );
      expect(view.canRetract, isFalse);
      expect(view.statusLabel, 'Retracting…');
    });

    test('an accepted retraction shows the job’s own status, never a claim '
        'of completion the controller has not confirmed', () {
      final view = MemoryClaimView(
        claim: claim(),
        phase: ClaimActionPhase.requested,
        note: 'In progress',
      );
      expect(view.statusLabel, 'In progress');
    });
  });
}
