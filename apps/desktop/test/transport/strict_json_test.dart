import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:zatiti_desktop/src/transport/envelope.dart';
import 'package:zatiti_desktop/src/transport/strict_json.dart';

import '../support/fake_controller.dart';

void main() {
  group('strict JSON', () {
    test('decodes ordinary documents', () {
      final v = decodeStrictJson(
        '{"a":[1,-2,3.5,true,null],"b":{"c":"x\\n\\u00e9\\ud83d\\ude00"}}',
      );
      expect(v, {
        'a': [1, -2, 3.5, true, null],
        'b': {'c': 'x\né😀'},
      });
    });

    // Rejections copied from the Go client's validation table.
    final rejected = <String, String>{
      'duplicate key': '{"a":1,"a":2}',
      'nested duplicate key': '{"o":{"k":1,"k":1}}',
      'trailing data': '{"a":1} trailing',
      'trailing document': '{"a":1} {}',
      'integer overflow': '{"n":9223372036854775808}',
      'integer underflow': '{"n":-9223372036854775809}',
      'lone surrogate escape': r'{"s":"\ud800 lone"}',
      'lone low surrogate': r'{"s":"\udc00"}',
      'leading zero': '{"n":01}',
      'unterminated': '{"a":',
      'control character': '{"a":""}',
      'empty': '',
    };
    rejected.forEach((name, doc) {
      test('refuses $name', () {
        expect(
          () => decodeStrictJson(doc),
          throwsA(isA<StrictJsonException>()),
        );
      });
    });

    test('accepts the int64 bounds exactly', () {
      expect(decodeStrictJson('[9223372036854775807,-9223372036854775808]'), [
        9223372036854775807,
        -9223372036854775808,
      ]);
    });

    test('refuses malformed UTF-8 bytes', () {
      expect(
        () => decodeStrictJsonBytes([0x22, 0xC3, 0x28, 0x22]),
        throwsA(isA<StrictJsonException>()),
      );
    });

    test('refuses nesting beyond the depth bound', () {
      final deep = '${'[' * 80}${']' * 80}';
      expect(() => decodeStrictJson(deep), throwsA(isA<StrictJsonException>()));
    });

    test('StrictObject refuses fields nobody read', () {
      final o = StrictObject(decodeStrictJson('{"a":1,"extra":2}'), 'thing');
      o.integer('a');
      expect(o.finish, throwsA(isA<StrictJsonException>()));
    });

    test('StrictObject refuses wrong types and missing fields', () {
      final o = StrictObject(decodeStrictJson('{"a":"1"}'), 'thing');
      expect(() => o.integer('a'), throwsA(isA<StrictJsonException>()));
      expect(() => o.string('b'), throwsA(isA<StrictJsonException>()));
    });
  });

  group('result envelope', () {
    ResultEnvelope decode(String s) =>
        ResultEnvelope.fromJson(decodeStrictJson(s));

    test('decodes completed, accepted and failed envelopes', () {
      final done = decode(completedEnvelope('{"x":1}', nextCursor: 'c1'));
      expect(done.status, ResultStatus.completed);
      expect(done.commandId, testCommandId);
      expect(done.data, {'x': 1});
      expect(done.nextCursor, 'c1');

      expect(decode(acceptedEnvelope('{}')).status, ResultStatus.accepted);

      final failed = decode(
        faultEnvelope('stale_version', 'review changed', retryable: true),
      );
      expect(failed.status, ResultStatus.failed);
      expect(failed.error!.code, FaultCode.staleVersion);
      expect(failed.error!.retryable, isTrue);
    });

    // The malformed-envelope table from internal/client/client_test.go.
    const id = testCommandId;
    final malformed = <String, String>{
      'missing command id':
          '{"schema":"zatiti.result/v1","status":"completed","data":null,"error":null,"next_cursor":null}',
      'unknown status':
          '{"schema":"zatiti.result/v1","command_id":"$id","status":"pending","data":null,"error":null,"next_cursor":null}',
      'completed with fault':
          '{"schema":"zatiti.result/v1","command_id":"$id","status":"completed","data":null,"error":{"code":"internal_error","message":"x","retryable":false},"next_cursor":null}',
      'failed without fault':
          '{"schema":"zatiti.result/v1","command_id":"$id","status":"failed","data":null,"error":null,"next_cursor":null}',
      'duplicate key':
          '{"schema":"zatiti.result/v1","command_id":"$id","command_id":"x","status":"completed","data":null,"error":null,"next_cursor":null}',
      'unknown field':
          '{"schema":"zatiti.result/v1","command_id":"$id","status":"completed","data":null,"error":null,"next_cursor":null,"extra":1}',
      'wrong schema':
          '{"schema":"zatiti.result/v2","command_id":"$id","status":"completed","data":null,"error":null,"next_cursor":null}',
      'unknown fault field':
          '{"schema":"zatiti.result/v1","command_id":"$id","status":"failed","data":null,"error":{"code":"conflict","message":"x","retryable":false,"hint":1},"next_cursor":null}',
      'data is an array':
          '{"schema":"zatiti.result/v1","command_id":"$id","status":"completed","data":[],"error":null,"next_cursor":null}',
    };
    malformed.forEach((name, doc) {
      test('refuses $name', () {
        expect(() => decode(doc), throwsA(isA<StrictJsonException>()));
      });
    });

    test('maps every contract fault code and its HTTP status', () {
      // Frozen by internal/contract/faults.go.
      const want = <String, int>{
        'invalid_input': 400,
        'not_found': 404,
        'permission_denied': 403,
        'stale_version': 409,
        'review_required': 409,
        'submission_conflict': 409,
        'conflict': 409,
        'cursor_expired': 410,
        'prerequisite_missing': 422,
        'external_action_required': 422,
        'budget_unavailable': 422,
        'capability_unsupported': 422,
        'verification_failed': 422,
        'artifact_fault': 409,
        'outcome_unknown': 409,
        'controller_unavailable': 503,
        'internal_error': 500,
      };
      want.forEach((wire, status) {
        final code = FaultCode.fromWire(wire);
        expect(code, isNotNull, reason: wire);
        expect(code!.httpStatus, status, reason: wire);
        final envelope = decode(faultEnvelope(wire, 'm'));
        expect(envelope.error!.code, code);
        validateAgainstHttp(status, envelope);
      });
      expect(
        FaultCode.values.where((c) => c != FaultCode.unrecognized).length,
        want.length,
      );
    });

    test('keeps an unrecognized fault code without guessing a meaning', () {
      final envelope = decode(faultEnvelope('quota_melted', 'new code'));
      expect(envelope.error!.code, FaultCode.unrecognized);
      expect(envelope.error!.wireCode, 'quota_melted');
    });

    test('refuses an envelope that disagrees with its HTTP status', () {
      expect(
        () => validateAgainstHttp(200, decode(acceptedEnvelope('{}'))),
        throwsA(isA<StrictJsonException>()),
      );
      expect(
        () => validateAgainstHttp(202, decode(completedEnvelope('{}'))),
        throwsA(isA<StrictJsonException>()),
      );
      expect(
        () => validateAgainstHttp(404, decode(faultEnvelope('conflict', 'x'))),
        throwsA(isA<StrictJsonException>()),
      );
      expect(
        () => validateAgainstHttp(302, decode(completedEnvelope('{}'))),
        throwsA(isA<StrictJsonException>()),
      );
    });

    test('parses snapshot_required from cursor_expired details', () {
      final f = decode(
        faultEnvelope(
          'cursor_expired',
          'gone',
          detailsJson: '{"snapshot_required":true,"window":"7d"}',
        ),
      ).error!;
      expect(f.snapshotRequired, isTrue);
    });
  });

  group('request envelope', () {
    test('renders schema, key and input', () {
      final body =
          jsonDecode(
                utf8.decode(
                  encodeRequest(input: {'a': 1}, submissionKey: 'demo-key-1'),
                ),
              )
              as Map<String, Object?>;
      expect(body, {
        'schema': 'zatiti.request/v1',
        'submission_key': 'demo-key-1',
        'input': {'a': 1},
      });
    });

    test('omits the key for queries', () {
      final body = utf8.decode(encodeRequest(input: {}));
      expect(body.contains('submission_key'), isFalse);
    });

    test('validates operation ids and submission keys', () {
      expect(isValidOperationId('conversation.message.send'), isTrue);
      expect(isValidOperationId(''), isFalse);
      expect(isValidOperationId('a..b'), isFalse);
      expect(isValidOperationId('.a'), isFalse);
      expect(isValidOperationId('a/../b'), isFalse);
      expect(isValidOperationId('Review.get'), isFalse);
      expect(isValidSubmissionKey('k' * 128), isTrue);
      expect(isValidSubmissionKey('k' * 129), isFalse);
      expect(isValidSubmissionKey(''), isFalse);
      expect(isValidSubmissionKey('bad\nkey'), isFalse);
    });
  });
}
