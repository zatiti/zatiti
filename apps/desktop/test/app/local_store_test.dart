// LocalStore: ordinary OS-protected local storage for the selected
// conversation, the resumable event cursor and a bounded authorized-identity
// cache. Never a credential, a TLS key or a draft (those live in secure
// storage — see credential_store.dart) and never a message body.

import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:zatiti_desktop/src/app/local_store.dart';

const _installationA = '00000000-0000-4000-8000-0000000000aa';
const _installationB = '00000000-0000-4000-8000-0000000000bb';

void main() {
  group('MemoryLocalStore', () {
    test('reading before any write returns empty', () async {
      final store = MemoryLocalStore();
      expect(await store.read(_installationA), LocalState.empty);
    });

    test('round-trips selection, cursor and the identity cache', () async {
      final store = MemoryLocalStore();
      await store.write(
        LocalState(
          installationId: _installationA,
          selectedWorkerId: 'worker-1',
          eventCursor: 'cursor-42',
          lastSequence: 42,
          workers: const [CachedIdentity(id: 'worker-1', name: 'Wren')],
        ),
      );
      final read = await store.read(_installationA);
      expect(read.selectedWorkerId, 'worker-1');
      expect(read.eventCursor, 'cursor-42');
      expect(read.lastSequence, 42);
      expect(read.workers.single.name, 'Wren');
    });

    test(
      'a different installation never sees the stored one’s cache — the '
      'account-switch boundary this client has instead of identity.current',
      () async {
        final store = MemoryLocalStore();
        await store.write(
          LocalState(
            installationId: _installationA,
            selectedWorkerId: 'worker-1',
            workers: const [
              CachedIdentity(id: 'worker-1', name: 'Account A’s chief'),
            ],
            principals: const [
              CachedIdentity(id: 'principal-1', name: 'Account A’s owner'),
            ],
          ),
        );

        final forB = await store.read(_installationB);
        expect(forB, LocalState.empty);
        expect(forB.workers, isEmpty);
        expect(forB.principals, isEmpty);
        expect(forB.selectedWorkerId, isNull);

        // The original installation's own read is unaffected by the probe.
        final forA = await store.read(_installationA);
        expect(forA.workers.single.name, 'Account A’s chief');
      },
    );

    test('clear discards everything, for every installation', () async {
      final store = MemoryLocalStore();
      await store.write(
        LocalState(installationId: _installationA, selectedWorkerId: 'w1'),
      );
      await store.clear();
      expect(await store.read(_installationA), LocalState.empty);
    });

    test('the identity cache is bounded, never an unbounded mirror', () async {
      final store = MemoryLocalStore();
      final many = [
        for (var i = 0; i < maxCachedIdentities + 50; i++)
          CachedIdentity(id: 'worker-$i', name: 'Worker $i'),
      ];
      await store.write(
        LocalState(installationId: _installationA, workers: many),
      );
      final read = await store.read(_installationA);
      expect(read.workers.length, maxCachedIdentities);
    });
  });

  group('FileLocalStore', () {
    late Directory dir;

    setUp(() async {
      dir = await Directory.systemTemp.createTemp('zatiti-local-store-test');
    });

    tearDown(() async {
      if (dir.existsSync()) await dir.delete(recursive: true);
    });

    test('persists to disk and reads back after a fresh instance', () async {
      final first = FileLocalStore('default', directory: dir);
      await first.write(
        LocalState(
          installationId: _installationA,
          selectedWorkerId: 'worker-1',
          selectedGroupId: null,
          eventCursor: 'cursor-7',
          lastSequence: 7,
          workers: const [CachedIdentity(id: 'worker-1', name: 'Wren')],
          organizations: const [CachedIdentity(id: 'org-1', name: 'Personal')],
          principals: const [CachedIdentity(id: 'p-1', name: 'You')],
        ),
      );

      // A brand new store instance over the same directory and profile is
      // exactly what a close/reopen of the app constructs.
      final reopened = FileLocalStore('default', directory: dir);
      final state = await reopened.read(_installationA);
      expect(state.selectedWorkerId, 'worker-1');
      expect(state.eventCursor, 'cursor-7');
      expect(state.lastSequence, 7);
      expect(state.workers.single.name, 'Wren');
      expect(state.organizations.single.name, 'Personal');
      expect(state.principals.single.name, 'You');
    });

    test(
      'a stored file for a different installation is never returned',
      () async {
        final store = FileLocalStore('default', directory: dir);
        await store.write(
          LocalState(
            installationId: _installationA,
            selectedWorkerId: 'worker-1',
          ),
        );
        final reopened = FileLocalStore('default', directory: dir);
        expect(await reopened.read(_installationB), LocalState.empty);
      },
    );

    test('a missing file reads as empty, not an error', () async {
      final store = FileLocalStore('default', directory: dir);
      expect(await store.read(_installationA), LocalState.empty);
    });

    test('a corrupt file reads as empty, not a crash', () async {
      await dir.create(recursive: true);
      await File('${dir.path}/default.local.json').writeAsString('{not json');
      final store = FileLocalStore('default', directory: dir);
      expect(await store.read(_installationA), LocalState.empty);
    });

    test('separate profiles never share state', () async {
      final work = FileLocalStore('work', directory: dir);
      final personal = FileLocalStore('personal', directory: dir);
      await work.write(
        LocalState(installationId: _installationA, selectedWorkerId: 'w-work'),
      );
      expect((await personal.read(_installationA)).selectedWorkerId, isNull);
      expect((await work.read(_installationA)).selectedWorkerId, 'w-work');
    });
  });
}
