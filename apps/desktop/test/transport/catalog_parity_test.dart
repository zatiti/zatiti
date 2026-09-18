// Checks the client's operation table against the generated public catalog,
// so the client cannot call an operation the controller does not publish.

import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:zatiti_desktop/src/transport/operations.dart';

void main() {
  // `flutter test` runs from apps/desktop; the catalog lives in the repo docs.
  final catalogFile = File('../../docs/implementation/operations.json');

  test('every client operation matches the public catalog', () {
    expect(
      catalogFile.existsSync(),
      isTrue,
      reason: 'run from apps/desktop inside the repository',
    );
    final catalog =
        jsonDecode(catalogFile.readAsStringSync()) as Map<String, Object?>;
    final byId = <String, Map<String, Object?>>{
      for (final o in catalog['operations']! as List<Object?>)
        (o! as Map<String, Object?>)['id']! as String:
            o as Map<String, Object?>,
    };

    for (final op in Operations.all) {
      final entry = byId[op.id];
      expect(entry, isNotNull, reason: '${op.id} is not in the catalog');
      expect(entry!['visibility'], 'public', reason: op.id);
      expect(entry['version'], op.version, reason: '${op.id} version');
      expect(
        entry['mode'],
        op.isMutation ? 'mutation' : 'query',
        reason: '${op.id} mode',
      );
      expect(
        entry['submission_key'],
        op.isMutation,
        reason: '${op.id} submission key',
      );
      expect(
        entry['expected_version'],
        op.expectedVersion,
        reason: '${op.id} expected_version',
      );
    }
  });

  test('the client never lists an internal operation', () {
    for (final op in Operations.all) {
      expect(op.id.startsWith('_'), isFalse, reason: op.id);
    }
  });
}
