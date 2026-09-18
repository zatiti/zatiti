// Mutual TLS against an in-process HTTPS server. The certificates are minted
// per run with the `openssl` CLI into a temporary directory, so no private
// key is committed. Without `openssl` on PATH the TLS exchange tests skip and
// say so; the configuration tests always run.

import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:zatiti_desktop/src/transport/controller_client.dart';
import 'package:zatiti_desktop/src/transport/endpoint.dart';
import 'package:zatiti_desktop/src/transport/errors.dart';
import 'package:zatiti_desktop/src/transport/operations.dart';

import '../support/fake_controller.dart';

class _Pki {
  _Pki(this.dir);
  final Directory dir;

  List<int> read(String name) => File('${dir.path}/$name').readAsBytesSync();

  static Future<_Pki?> mint() async {
    final dir = await Directory.systemTemp.createTemp('ztls');
    Future<bool> openssl(List<String> args) async {
      try {
        final r = await Process.run(
          'openssl',
          args,
          workingDirectory: dir.path,
        );
        return r.exitCode == 0;
      } on ProcessException {
        return false;
      }
    }

    Future<bool> authority(String name) => openssl([
      'req', '-x509', '-newkey', 'rsa:2048', '-nodes', //
      '-keyout', '$name.key', '-out', '$name.pem',
      '-subj', '/CN=Fixture $name', '-days', '2',
    ]);

    Future<bool> leaf(String name, String ca, String extensions) async {
      File('${dir.path}/$name.ext').writeAsStringSync(extensions);
      return await openssl([
            'req', '-newkey', 'rsa:2048', '-nodes', //
            '-keyout', '$name.key', '-out', '$name.csr',
            '-subj', '/CN=localhost',
          ]) &&
          await openssl([
            'x509', '-req', '-in', '$name.csr', //
            '-CA', '$ca.pem', '-CAkey', '$ca.key', '-CAcreateserial',
            '-out', '$name.pem', '-days', '2', '-extfile', '$name.ext',
          ]);
    }

    final ok =
        await authority('ca') &&
        await authority('otherca') &&
        await leaf(
          'server',
          'ca',
          'subjectAltName=DNS:localhost\nextendedKeyUsage=serverAuth\n',
        ) &&
        await leaf('client', 'ca', 'extendedKeyUsage=clientAuth\n') &&
        await leaf('stranger', 'otherca', 'extendedKeyUsage=clientAuth\n');
    if (!ok) {
      await dir.delete(recursive: true);
      return null;
    }
    return _Pki(dir);
  }
}

void main() {
  group('remote endpoint configuration', () {
    const material = MutualTlsMaterial(
      clientCertificateChainPem: [],
      clientPrivateKeyPem: [],
    );

    test('refuses anything but https', () {
      expect(
        () => RemoteTlsEndpoint(
          url: Uri.parse('http://controller.example:8443'),
          tls: material,
        ),
        throwsA(isA<InvalidRequestException>()),
      );
    });

    test('refuses credentials in the URL and a missing host', () {
      expect(
        () => RemoteTlsEndpoint(
          url: Uri.parse('https://user:pw@controller.example'),
          tls: material,
        ),
        throwsA(isA<InvalidRequestException>()),
      );
      expect(
        () => RemoteTlsEndpoint(url: Uri.parse('https:///x'), tls: material),
        throwsA(isA<InvalidRequestException>()),
      );
    });

    test('refuses unusable PEM material before any connection', () {
      final endpoint = RemoteTlsEndpoint(
        url: Uri.parse('https://controller.example:8443'),
        tls: const MutualTlsMaterial(
          clientCertificateChainPem: [1, 2, 3],
          clientPrivateKeyPem: [4, 5, 6],
        ),
      );
      expect(
        endpoint.buildSecurityContext,
        throwsA(isA<InvalidRequestException>()),
      );
    });

    test('a local socket endpoint needs a path', () {
      expect(
        () => LocalSocketEndpoint(''),
        throwsA(isA<InvalidRequestException>()),
      );
    });
  });

  group('mutual TLS exchange', () {
    _Pki? pki;
    HttpServer? server;
    final seenCertificates = <X509Certificate?>[];

    setUpAll(() async {
      pki = await _Pki.mint();
      final p = pki;
      if (p == null) return;
      final context = SecurityContext(withTrustedRoots: false)
        ..useCertificateChainBytes(p.read('server.pem'))
        ..usePrivateKeyBytes(p.read('server.key'))
        ..setTrustedCertificatesBytes(p.read('ca.pem'))
        ..setClientAuthoritiesBytes(p.read('ca.pem'));
      server = await HttpServer.bindSecure(
        'localhost',
        0,
        context,
        requestClientCertificate: true,
      );
      server!.listen((request) async {
        await request.drain<void>();
        seenCertificates.add(request.certificate);
        final authenticated = request.certificate != null;
        request.response
          ..statusCode = authenticated ? 200 : 403
          ..headers.contentType = ContentType('application', 'json')
          ..write(
            authenticated
                ? completedEnvelope('{"items":[]}')
                : faultEnvelope(
                    'permission_denied',
                    'remote transport requires a verified client certificate',
                  ),
          );
        await request.response.close();
      });
    });

    tearDownAll(() async {
      await server?.close(force: true);
      await pki?.dir.delete(recursive: true);
    });

    setUp(seenCertificates.clear);

    ControllerClient clientWith({
      required String certificate,
      required String key,
      required String roots,
    }) => ControllerClient(
      endpoint: RemoteTlsEndpoint(
        url: Uri.parse('https://localhost:${server!.port}'),
        tls: MutualTlsMaterial(
          clientCertificateChainPem: pki!.read(certificate),
          clientPrivateKeyPem: pki!.read(key),
          trustedRootsPem: pki!.read(roots),
        ),
      ),
      installationId: testInstallationId,
      credentials: () async => null,
      timeout: const Duration(seconds: 10),
    );

    String? skipReason() =>
        pki == null ? 'openssl is not available to mint certificates' : null;

    test('presents the client certificate and completes the call', () async {
      if (skipReason() case final reason?) {
        markTestSkipped(reason);
        return;
      }
      final client = clientWith(
        certificate: 'client.pem',
        key: 'client.key',
        roots: 'ca.pem',
      );
      final result = await client.query(Operations.reviewList, {
        'scope': client.scope(),
      });
      expect(result.data, {'items': <Object?>[]});
      expect(seenCertificates.single, isNotNull);
    });

    test('a controller signed by an unpinned root is a permanent TLS '
        'failure and nothing is sent', () async {
      if (skipReason() case final reason?) {
        markTestSkipped(reason);
        return;
      }
      final client = clientWith(
        certificate: 'client.pem',
        key: 'client.key',
        roots: 'otherca.pem',
      );
      final s = client.prepare(Operations.reviewDecide, {
        'scope': client.scope(),
      });
      await expectLater(
        client.submit(s),
        throwsA(isA<TlsCertificateException>()),
      );
      expect(seenCertificates, isEmpty);
      expect(s.attempts, 0);
    });

    test('a client certificate from an unknown authority is never '
        'treated as authenticated', () async {
      if (skipReason() case final reason?) {
        markTestSkipped(reason);
        return;
      }
      final client = clientWith(
        certificate: 'stranger.pem',
        key: 'stranger.key',
        roots: 'ca.pem',
      );
      await expectLater(
        client.query(Operations.reviewList, {'scope': client.scope()}),
        throwsA(isA<TransportException>()),
      );
      expect(seenCertificates.whereType<X509Certificate>(), isEmpty);
    });

    test('a closed remote port is controller_unavailable', () async {
      if (skipReason() case final reason?) {
        markTestSkipped(reason);
        return;
      }
      final probe = await ServerSocket.bind('localhost', 0);
      final port = probe.port;
      await probe.close();
      final client = ControllerClient(
        endpoint: RemoteTlsEndpoint(
          url: Uri.parse('https://localhost:$port'),
          tls: MutualTlsMaterial(
            clientCertificateChainPem: pki!.read('client.pem'),
            clientPrivateKeyPem: pki!.read('client.key'),
            trustedRootsPem: pki!.read('ca.pem'),
          ),
        ),
        installationId: testInstallationId,
        credentials: () async => null,
      );
      await expectLater(
        client.query(Operations.reviewList, {'scope': client.scope()}),
        throwsA(isA<ControllerUnavailableException>()),
      );
    });
  });
}
