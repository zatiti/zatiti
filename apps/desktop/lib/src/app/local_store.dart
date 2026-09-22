// Ordinary OS-protected local storage: state that is not a secret but
// should not vanish between launches. The credential, TLS key material and
// unsent drafts do NOT belong here — AGENTS.md's frozen contract keeps those
// in secure storage (see `credential_store.dart`). This store is for the
// selected conversation and a small, bounded cache of authorized identities
// (worker/organization/principal id-and-name pairs) used only to paint
// something recognizable before the first live snapshot arrives; it never
// holds message bodies, so it cannot itself leak conversation content.
//
// The cache is stamped with the installation it was read from. A different
// installation on the next launch (a real account switch, since one
// installation belongs to one operator or team) is a hard signal to discard
// it rather than show one account's cached identities under another's.

import 'dart:convert';
import 'dart:io';

/// One cached identity: enough to paint a name before a live snapshot
/// arrives. Never a message, a credential or any other secret-adjacent
/// value.
class CachedIdentity {
  const CachedIdentity({required this.id, required this.name});

  factory CachedIdentity.fromJson(Object? json) {
    final map = json as Map<String, Object?>;
    return CachedIdentity(
      id: map['id']! as String,
      name: map['name']! as String,
    );
  }

  final String id;
  final String name;

  Map<String, Object?> toJson() => {'id': id, 'name': name};
}

/// Maximum entries kept per identity list: the same bound the wire itself
/// uses for one list page (`docs/implementation/operations.json`'s default
/// list limit), so the cache never grows into an unbounded local mirror of
/// the installation.
const int maxCachedIdentities = 200;

/// Everything this store persists, as one immutable value.
class LocalState {
  const LocalState({
    this.installationId,
    this.selectedWorkerId,
    this.selectedGroupId,
    this.eventCursor,
    this.lastSequence = 0,
    this.workers = const [],
    this.organizations = const [],
    this.principals = const [],
  });

  static const LocalState empty = LocalState();

  /// The installation this state was captured from. A read whose stored
  /// installation differs from the caller's current one is treated as
  /// belonging to a different account and is never returned.
  final String? installationId;

  final String? selectedWorkerId;
  final String? selectedGroupId;

  /// The resumable `event.list` cursor, so a fresh launch replays from where
  /// the last one left off instead of re-baselining to "now" and silently
  /// skipping whatever happened while the app was closed.
  final String? eventCursor;
  final int lastSequence;

  final List<CachedIdentity> workers;
  final List<CachedIdentity> organizations;
  final List<CachedIdentity> principals;

  LocalState copyWith({
    String? installationId,
    Object? selectedWorkerId = _unset,
    Object? selectedGroupId = _unset,
    Object? eventCursor = _unset,
    int? lastSequence,
    List<CachedIdentity>? workers,
    List<CachedIdentity>? organizations,
    List<CachedIdentity>? principals,
  }) => LocalState(
    installationId: installationId ?? this.installationId,
    selectedWorkerId: identical(selectedWorkerId, _unset)
        ? this.selectedWorkerId
        : selectedWorkerId as String?,
    selectedGroupId: identical(selectedGroupId, _unset)
        ? this.selectedGroupId
        : selectedGroupId as String?,
    eventCursor: identical(eventCursor, _unset)
        ? this.eventCursor
        : eventCursor as String?,
    lastSequence: lastSequence ?? this.lastSequence,
    workers: workers ?? this.workers,
    organizations: organizations ?? this.organizations,
    principals: principals ?? this.principals,
  );

  Map<String, Object?> toJson() => {
    'installation_id': installationId,
    'selected_worker_id': selectedWorkerId,
    'selected_group_id': selectedGroupId,
    'event_cursor': eventCursor,
    'last_sequence': lastSequence,
    'workers': [for (final w in workers) w.toJson()],
    'organizations': [for (final o in organizations) o.toJson()],
    'principals': [for (final p in principals) p.toJson()],
  };

  static LocalState fromJson(Map<String, Object?> json) {
    List<CachedIdentity> identities(String key) => [
      for (final v in (json[key] as List<Object?>? ?? const []))
        CachedIdentity.fromJson(v),
    ];
    return LocalState(
      installationId: json['installation_id'] as String?,
      selectedWorkerId: json['selected_worker_id'] as String?,
      selectedGroupId: json['selected_group_id'] as String?,
      eventCursor: json['event_cursor'] as String?,
      lastSequence: json['last_sequence'] as int? ?? 0,
      workers: identities('workers'),
      organizations: identities('organizations'),
      principals: identities('principals'),
    );
  }
}

const Object _unset = Object();

abstract interface class LocalStore {
  /// Reads the persisted state for [installationId]. Returns
  /// [LocalState.empty] when nothing is stored, storage cannot be read, or
  /// the stored state belongs to a different installation — the last case
  /// is a real account switch, and the caller must never see the other
  /// account's cached identities or selection.
  Future<LocalState> read(String installationId);

  Future<void> write(LocalState state);

  /// Discards everything. Used when a credential changes (a possible
  /// account switch this client cannot otherwise detect, having no
  /// `identity.current` operation to confirm the caller's own identity) and
  /// when a controller proves the current authorization no longer holds.
  Future<void> clear();
}

List<CachedIdentity> _bounded(List<CachedIdentity> items) =>
    items.length <= maxCachedIdentities
    ? items
    : items.sublist(0, maxCachedIdentities);

/// One JSON file in this OS's per-user application-support directory. Not
/// secure storage: readable by this OS user account like any of its own
/// files, appropriate for non-secret UI state and nothing else.
class FileLocalStore implements LocalStore {
  FileLocalStore(this.profile, {Directory? directory})
    : _directory = directory ?? _defaultDirectory();

  final String profile;
  final Directory _directory;

  static Directory _defaultDirectory() {
    final home =
        Platform.environment['HOME'] ?? Platform.environment['USERPROFILE'];
    if (home == null || home.isEmpty) {
      // No resolvable home directory: fall back to the working directory
      // rather than throwing, so a misconfigured environment degrades to
      // "state does not persist" instead of crashing startup.
      return Directory('.zatiti-state');
    }
    final base = switch (Platform.operatingSystem) {
      'macos' => '$home/Library/Application Support/Zatiti',
      'linux' =>
        '${Platform.environment['XDG_DATA_HOME'] ?? '$home/.local/share'}/zatiti',
      _ => '$home/.zatiti',
    };
    return Directory(base);
  }

  File get _file => File('${_directory.path}/$profile.local.json');

  @override
  Future<LocalState> read(String installationId) async {
    try {
      final file = _file;
      if (!await file.exists()) return LocalState.empty;
      final text = await file.readAsString();
      if (text.trim().isEmpty) return LocalState.empty;
      final state = LocalState.fromJson(
        jsonDecode(text) as Map<String, Object?>,
      );
      if (state.installationId != installationId) return LocalState.empty;
      return state;
    } on Object {
      // Missing directory, permission failure, or corrupt JSON: treat as
      // "nothing persisted" rather than fail startup over cached UI state.
      return LocalState.empty;
    }
  }

  @override
  Future<void> write(LocalState state) async {
    try {
      await _directory.create(recursive: true);
      final bounded = state.copyWith(
        workers: _bounded(state.workers),
        organizations: _bounded(state.organizations),
        principals: _bounded(state.principals),
      );
      await _file.writeAsString(jsonEncode(bounded.toJson()));
    } on Object {
      // Best-effort: a write failure here must not surface as a workspace
      // error. The next successful load simply starts from a fresh cache.
    }
  }

  @override
  Future<void> clear() async {
    try {
      final file = _file;
      if (await file.exists()) await file.delete();
    } on Object {
      // Nothing to clean up, or the filesystem refused; either way there is
      // no stale state left to act on.
    }
  }
}

/// Held only for the life of the process. Used by tests and the demo.
class MemoryLocalStore implements LocalStore {
  LocalState _state = LocalState.empty;

  @override
  Future<LocalState> read(String installationId) async =>
      _state.installationId == installationId ? _state : LocalState.empty;

  @override
  Future<void> write(LocalState state) async {
    _state = state.copyWith(
      workers: _bounded(state.workers),
      organizations: _bounded(state.organizations),
      principals: _bounded(state.principals),
    );
  }

  @override
  Future<void> clear() async {
    _state = LocalState.empty;
  }
}
