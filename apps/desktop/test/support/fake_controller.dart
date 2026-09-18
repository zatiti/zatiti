// An in-process fake controller: a real HTTP server bound to a Unix socket in
// a temporary directory. It replays scripted envelopes whose shape is copied
// from the Go transport tests (internal/client, internal/server).

import 'dart:async';
import 'dart:convert';
import 'dart:io';

const String testCommandId = '00000000-0000-4000-8000-000000000001';
const String testInstallationId = '00000000-0000-4000-8000-0000000000aa';

/// One request as the fake controller received it.
class RecordedRequest {
  RecordedRequest({
    required this.method,
    required this.path,
    required this.headers,
    required this.body,
  });

  final String method;
  final String path;
  final Map<String, String> headers;
  final String body;

  Map<String, Object?> get json => jsonDecode(body) as Map<String, Object?>;
  String? get submissionKey => json['submission_key'] as String?;
  Map<String, Object?> get input => json['input'] as Map<String, Object?>;
}

/// What the fake does with one request.
sealed class Reply {
  const Reply();
}

/// Answer with an HTTP status and a raw body.
final class RawReply extends Reply {
  const RawReply(this.status, this.body);
  final int status;
  final String body;
}

/// Read the whole request, then drop the connection without answering. The
/// client sent its bytes and learns nothing: an unknown acknowledgment.
final class DropReply extends Reply {
  const DropReply();
}

/// Never answer, so the client's timeout fires after the bytes were sent.
final class HangReply extends Reply {
  const HangReply();
}

String completedEnvelope(String dataJson, {String? nextCursor}) =>
    '{"schema":"zatiti.result/v1","command_id":"$testCommandId",'
    '"status":"completed","data":$dataJson,"error":null,'
    '"next_cursor":${nextCursor == null ? 'null' : jsonEncode(nextCursor)}}';

String acceptedEnvelope(String dataJson) =>
    '{"schema":"zatiti.result/v1","command_id":"$testCommandId",'
    '"status":"accepted","data":$dataJson,"error":null,"next_cursor":null}';

String faultEnvelope(
  String code,
  String message, {
  bool retryable = false,
  String? detailsJson,
}) =>
    '{"schema":"zatiti.result/v1","command_id":"$testCommandId",'
    '"status":"failed","data":null,"error":{"code":"$code",'
    '"message":${jsonEncode(message)},"retryable":$retryable'
    '${detailsJson == null ? '' : ',"details":$detailsJson'}},'
    '"next_cursor":null}';

Reply completed(String dataJson, {String? nextCursor}) =>
    RawReply(200, completedEnvelope(dataJson, nextCursor: nextCursor));

Reply accepted(String dataJson) => RawReply(202, acceptedEnvelope(dataJson));

Reply fault(int status, String code, String message, {String? detailsJson}) =>
    RawReply(status, faultEnvelope(code, message, detailsJson: detailsJson));

typedef Script = FutureOr<Reply> Function(RecordedRequest request);

class FakeController {
  FakeController._(this._dir, this._server, this.socketPath);

  final Directory _dir;
  HttpServer _server;
  final String socketPath;

  final List<RecordedRequest> requests = [];
  final List<Completer<void>> _hangs = [];

  /// Decides the reply for each request. Defaults to an empty completion.
  Script script = (_) => completed('{}');

  static Future<FakeController> start() async {
    // Unix socket paths are short (104 bytes on macOS); keep the name small.
    final dir = await Directory.systemTemp.createTemp('zt');
    final path = '${dir.path}/c.sock';
    final server = await HttpServer.bind(
      InternetAddress(path, type: InternetAddressType.unix),
      0,
    );
    final fake = FakeController._(dir, server, path);
    server.listen(fake._handle);
    return fake;
  }

  List<RecordedRequest> requestsFor(String operation) =>
      requests.where((r) => r.path == '/v1/operations/$operation').toList();

  Future<void> _handle(HttpRequest request) async {
    final body = await utf8.decoder.bind(request).join();
    final headers = <String, String>{};
    request.headers.forEach((name, values) => headers[name] = values.join(','));
    final recorded = RecordedRequest(
      method: request.method,
      path: request.uri.path,
      headers: headers,
      body: body,
    );
    requests.add(recorded);
    final reply = await script(recorded);
    switch (reply) {
      case RawReply(:final status, body: final replyBody):
        request.response.statusCode = status;
        request.response.headers.contentType = ContentType(
          'application',
          'json',
        );
        request.response.write(replyBody);
        await request.response.close();
      case DropReply():
        final socket = await request.response.detachSocket(writeHeaders: false);
        socket.destroy();
      case HangReply():
        final hang = Completer<void>();
        _hangs.add(hang);
        await hang.future;
        // The client has usually gone away by now; there is nothing to detach.
        try {
          final socket = await request.response.detachSocket(
            writeHeaders: false,
          );
          socket.destroy();
        } on Object {
          // Already closed by the peer.
        }
    }
  }

  /// Takes the controller down: the socket disappears, so a client cannot
  /// connect and provably sends nothing.
  Future<void> goDown() async {
    await _server.close(force: true);
    final socket = File(socketPath);
    if (socket.existsSync()) socket.deleteSync();
  }

  /// Brings the controller back at the same socket path.
  Future<void> comeBack() async {
    _server = await HttpServer.bind(
      InternetAddress(socketPath, type: InternetAddressType.unix),
      0,
    );
    _server.listen(_handle);
  }

  Future<void> stop() async {
    for (final hang in _hangs) {
      if (!hang.isCompleted) hang.complete();
    }
    await _server.close(force: true);
    if (_dir.existsSync()) await _dir.delete(recursive: true);
  }
}
