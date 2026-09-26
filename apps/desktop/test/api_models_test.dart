import 'package:flutter_test/flutter_test.dart';
import 'package:zatiti_desktop/src/api/models.dart';

void main() {
  test('job.get retains a trusted completion result for profile setup', () {
    final result = <String, Object?>{
      'resource': <String, Object?>{
        'executor': 'hosted',
        'model': 'test/model',
        'connection_id': '00000000-0000-4000-8000-000000000001',
        'provider_destination': 'https://api.openai.com/v1/responses',
        'capabilities': <Object?>['responses.text_generation'],
        'cost_bound': <String, Object?>{
          'currency': 'USD',
          'micro_units': 20000,
        },
        'classification': 'internal',
        'context_capture': 'complete',
        'connection_version': 1,
        'adapter_profile': <String, Object?>{
          'schema': 'zatiti.responses/v2',
          'model': 'test/model',
        },
      },
    };
    final job = Job.fromJson(<String, Object?>{
      'id': '00000000-0000-4000-8000-000000000002',
      'version': 3,
      'kind': 'external_effect',
      'state': 'succeeded',
      'owner': 'configuration',
      'operation': 'execution_profile.qualify',
      'requirements': <Object?>[],
      'result': result,
    });

    expect(job.state, JobState.succeeded);
    expect(job.result, same(result));
  });
}
