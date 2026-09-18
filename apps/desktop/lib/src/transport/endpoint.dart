// Controller endpoint configuration: the private Unix socket locally, or
// mutual TLS for an explicitly configured remote controller.

import 'dart:io';

import 'errors.dart';

/// Where the controller listens. Exactly one transport per endpoint.
sealed class ControllerEndpoint {
  const ControllerEndpoint();

  /// The base URI requests are addressed to.
  Uri get baseUri;
}

/// The controller's private Unix-domain socket. Local traffic is plaintext by
/// design; the socket's file permissions and the Authorization credential
/// protect it.
final class LocalSocketEndpoint extends ControllerEndpoint {
  LocalSocketEndpoint(this.socketPath) {
    if (socketPath.isEmpty) {
      throw const InvalidRequestException('socket path is required');
    }
  }

  final String socketPath;

  /// The authority is a placeholder; the connection dials [socketPath].
  @override
  Uri get baseUri => Uri.parse('http://unix');
}

/// PEM material for one mutual-TLS identity. The bytes come from secure
/// storage at startup and are never operation input.
final class MutualTlsMaterial {
  const MutualTlsMaterial({
    required this.clientCertificateChainPem,
    required this.clientPrivateKeyPem,
    this.clientPrivateKeyPassword,
    this.trustedRootsPem,
  });

  final List<int> clientCertificateChainPem;
  final List<int> clientPrivateKeyPem;
  final String? clientPrivateKeyPassword;

  /// Roots that may sign the controller's certificate. When set, the system
  /// trust store is not consulted: only these roots are trusted.
  final List<int>? trustedRootsPem;
}

/// A remote controller over mutual TLS. Remote endpoints are always https and
/// certificate verification cannot be disabled.
final class RemoteTlsEndpoint extends ControllerEndpoint {
  RemoteTlsEndpoint({required Uri url, required this.tls}) : baseUri = url {
    if (url.scheme != 'https') {
      throw InvalidRequestException(
        'remote endpoints require https, got "${url.scheme}"',
      );
    }
    if (url.host.isEmpty) {
      throw const InvalidRequestException('remote URL is missing a host');
    }
    if (url.userInfo.isNotEmpty) {
      throw const InvalidRequestException(
        'remote URL must not carry credentials',
      );
    }
  }

  @override
  final Uri baseUri;

  final MutualTlsMaterial tls;

  /// Builds the TLS context: the client identity, and the pinned roots when
  /// configured.
  SecurityContext buildSecurityContext() {
    final pinned = tls.trustedRootsPem;
    final context = SecurityContext(withTrustedRoots: pinned == null);
    try {
      if (pinned != null) context.setTrustedCertificatesBytes(pinned);
      context.useCertificateChainBytes(tls.clientCertificateChainPem);
      context.usePrivateKeyBytes(
        tls.clientPrivateKeyPem,
        password: tls.clientPrivateKeyPassword,
      );
    } on TlsException catch (e) {
      throw InvalidRequestException('TLS material is not usable: ${e.message}');
    }
    return context;
  }
}
