import 'package:flutter_test/flutter_test.dart';
import 'package:zatiti_desktop/src/transport/submission.dart';

void main() {
  test('restored possibly-sent mutation is locked against replay', () {
    final submission = Submission.restore(
      operation: 'execution_profile.qualify',
      operationVersion: 1,
      key: 'desktop-original-key',
      body: <int>[123, 125],
    );

    expect(submission.state, SubmissionState.acknowledgmentUnknown);
    expect(submission.maySend, isFalse);
    expect(submission.body, <int>[123, 125]);
    expect(submission.key, 'desktop-original-key');
  });
}
