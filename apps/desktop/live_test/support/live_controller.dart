// Starts a real controller and hands the client what it needs to talk to it.
//
// Nothing here is a fake. The binary is the product's own `zatiti`; the
// socket is the controller's private socket; the credential is the owner
// credential the controller itself wrote. Every failure mode below reports
// what was observed, including the controller's own log, so an unreachable
// controller is a loud failure and never a silently skipped test.

import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math';

/// The environment variable naming the built controller binary.
const String controllerBinaryVariable = 'ZATITI_CONTROLLER_BIN';

/// The environment variable naming the directory state directories are made
/// in. It defaults to the system temporary directory root because the socket
/// path has a hard 104-byte bound (see [maxSocketPathBytes]).
const String stateBaseVariable = 'ZATITI_LIVE_STATE_BASE';

/// The Unix-domain `sun_path` bound on macOS, terminator included. The
/// controller refuses a longer socket path by name at startup.
const int maxSocketPathBytes = 104;

/// How long the controller may take to accept a connection. Migrations, the
/// installation lock and the scheduler all run first, and a loaded machine
/// makes that slow.
const Duration listenTimeout = Duration(minutes: 3);

/// Raised when the controller could not be started, reached, or initialized.
/// The message carries the exact evidence: what was expected, what happened,
/// and the controller's own log.
class LiveControllerFailure implements Exception {
  LiveControllerFailure(this.message);
  final String message;

  @override
  String toString() => 'LiveControllerFailure: $message';
}

/// A running controller with an initialized installation.
class LiveController {
  LiveController._({
    required this.stateDir,
    required this.socketPath,
    required this.installationId,
    required this.ownerName,
    required this.authorization,
    required Process process,
    required File keyFile,
    required List<String> log,
  }) : _process = process,
       _keyFile = keyFile,
       _log = log;

  final Directory stateDir;
  final String socketPath;

  /// The installation the controller reported from `installation.init`.
  final String installationId;

  /// The owner's display name, as it must come back from `principal.list`.
  final String ownerName;

  /// The complete `Authorization` header value, read verbatim from the owner
  /// profile the controller wrote. It is never logged or asserted on.
  final String authorization;

  final Process _process;
  final File _keyFile;
  final List<String> _log;

  /// The controller's own diagnostics, newest last.
  String get log => _log.join('\n');

  /// Starts a controller, initializes its installation and returns once the
  /// owner credential is on disk.
  static Future<LiveController> start({String ownerName = 'Live Proof'}) async {
    final binary = _resolveBinary();
    final base = Platform.environment[stateBaseVariable]?.trim();
    final root = base == null || base.isEmpty
        ? Directory.systemTemp.path
        : base;
    final name = 'zt${_hex(3)}';
    final stateDir = Directory('$root/$name');
    final keyFile = File('$root/$name.key');
    final socketPath = '${stateDir.path}/zatiti.sock';

    if (socketPath.length >= maxSocketPathBytes) {
      throw LiveControllerFailure(
        'the socket path would be ${socketPath.length} bytes ($socketPath); '
        'Unix sockets allow at most ${maxSocketPathBytes - 1}. Set '
        '$stateBaseVariable to a shorter directory.',
      );
    }

    stateDir.createSync(recursive: true);
    await _chmod('700', stateDir.path);
    keyFile.writeAsStringSync(_hex(32));
    await _chmod('600', keyFile.path);

    final log = <String>[];
    final process = await Process.start(binary.path, [
      'serve',
      '--state-dir',
      stateDir.path,
      '--credential-backend',
      'headless',
      '--master-key',
      'file:${keyFile.path}',
    ]);
    for (final stream in [process.stdout, process.stderr]) {
      stream
          .transform(const Utf8Decoder(allowMalformed: true))
          .transform(const LineSplitter())
          .listen(log.add);
    }

    int? exitStatus;
    unawaited(process.exitCode.then((code) => exitStatus = code));

    Never abandon(String what) {
      process.kill(ProcessSignal.sigkill);
      _remove(stateDir, keyFile);
      throw LiveControllerFailure(
        '$what\ncommand: ${binary.path} serve --state-dir ${stateDir.path} '
        '--credential-backend headless --master-key file:…'
        '\ncontroller log:\n${log.join('\n')}',
      );
    }

    final deadline = DateTime.now().add(listenTimeout);
    while (!await _accepts(socketPath)) {
      if (exitStatus != null) {
        abandon(
          'the controller exited with code $exitStatus before it listened on '
          '$socketPath',
        );
      }
      if (DateTime.now().isAfter(deadline)) {
        abandon(
          'the controller did not accept a connection on $socketPath within '
          '${listenTimeout.inSeconds}s',
        );
      }
      await Future<void>.delayed(const Duration(milliseconds: 250));
    }

    final init = await Process.run(binary.path, [
      'init',
      '--state-dir',
      stateDir.path,
      '--json',
      '--input',
      jsonEncode({
        'credential_store': 'headless',
        'owner_name': ownerName,
        'headless_key_ref': 'installation/owner',
      }),
    ]);
    if (init.exitCode != 0) {
      abandon(
        'zatiti init exited ${init.exitCode}\nstdout: ${init.stdout}\n'
        'stderr: ${init.stderr}',
      );
    }
    Object? envelope;
    try {
      envelope = jsonDecode('${init.stdout}');
    } on FormatException catch (e) {
      abandon(
        'zatiti init did not print one result envelope: $e: '
        '${init.stdout}',
      );
    }
    final installationId = _dig(envelope, [
      'data',
      'resource',
      'installation_id',
    ]);
    if (installationId is! String || installationId.isEmpty) {
      abandon('zatiti init reported no installation: ${init.stdout}');
    }

    final profile = File('${stateDir.path}/profiles/owner');
    if (!profile.existsSync()) {
      abandon(
        'the controller did not write the owner profile at '
        '${profile.path}',
      );
    }
    final mode = profile.statSync().mode & 0x1FF;
    if (mode != 0x180) {
      abandon('the owner profile is mode ${mode.toRadixString(8)}, want 600');
    }
    final authorization = profile.readAsStringSync().trim();
    if (!authorization.startsWith('Bearer ')) {
      abandon(
        'the owner profile does not hold a complete Authorization header '
        'value: it is ${authorization.length} bytes and does not begin with '
        '"Bearer "',
      );
    }

    return LiveController._(
      stateDir: stateDir,
      socketPath: socketPath,
      installationId: installationId,
      ownerName: ownerName,
      authorization: authorization,
      process: process,
      keyFile: keyFile,
      log: log,
    );
  }

  /// Stops the controller and removes everything this harness created.
  Future<void> stop() async {
    _process.kill(ProcessSignal.sigterm);
    try {
      await _process.exitCode.timeout(const Duration(seconds: 30));
    } on TimeoutException {
      _process.kill(ProcessSignal.sigkill);
      await _process.exitCode;
    }
    _remove(stateDir, _keyFile);
  }

  static File _resolveBinary() {
    final configured = Platform.environment[controllerBinaryVariable]?.trim();
    if (configured == null || configured.isEmpty) {
      throw LiveControllerFailure(
        '$controllerBinaryVariable is not set. This suite proves the client '
        'against a real controller and never substitutes a fake one. Run '
        'tool/live-proof.sh, which builds cmd/zatiti and sets the variable.',
      );
    }
    final file = File(configured);
    if (!file.existsSync()) {
      throw LiveControllerFailure(
        '$controllerBinaryVariable points at $configured, which does not '
        'exist. Build it with: go build -o <path> ./cmd/zatiti',
      );
    }
    return file;
  }

  /// [bytes] bytes of random material in lowercase hex. Used for the master
  /// key (32 bytes, the form the controller accepts) and for directory names.
  static String _hex(int bytes) {
    final random = Random.secure();
    return [
      for (var i = 0; i < bytes; i++)
        random.nextInt(256).toRadixString(16).padLeft(2, '0'),
    ].join();
  }

  static Future<void> _chmod(String mode, String path) async {
    final r = await Process.run('/bin/chmod', [mode, path]);
    if (r.exitCode != 0) {
      throw LiveControllerFailure('chmod $mode $path failed: ${r.stderr}');
    }
  }

  /// Whether the controller's socket accepts a connection right now.
  static Future<bool> _accepts(String socketPath) async {
    if (FileSystemEntity.typeSync(socketPath) ==
        FileSystemEntityType.notFound) {
      return false;
    }
    try {
      final socket = await Socket.connect(
        InternetAddress(socketPath, type: InternetAddressType.unix),
        0,
        timeout: const Duration(seconds: 5),
      );
      socket.destroy();
      return true;
    } on SocketException {
      return false;
    }
  }

  static void _remove(Directory stateDir, File keyFile) {
    try {
      if (stateDir.existsSync()) stateDir.deleteSync(recursive: true);
    } on FileSystemException {
      // Leaving a temporary directory behind is not a test failure.
    }
    try {
      if (keyFile.existsSync()) keyFile.deleteSync();
    } on FileSystemException {
      // As above.
    }
  }

  static Object? _dig(Object? value, List<String> path) {
    var current = value;
    for (final key in path) {
      if (current is! Map<String, Object?>) return null;
      current = current[key];
    }
    return current;
  }
}
