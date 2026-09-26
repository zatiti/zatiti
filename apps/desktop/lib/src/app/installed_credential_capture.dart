// The installed Mac asks its native Runner to open a signed local helper.
// This channel never accepts or returns provider-key material.

import 'dart:convert';

import 'package:flutter/services.dart';

const credentialCaptureChannel = 'zatiti/credential_capture';
const credentialCaptureSchema = 'zatiti.gui-credential-capture/v1';
const credentialCaptureResultSchema = 'zatiti.gui-credential-capture-result/v1';

final RegExp _uuid = RegExp(
  r'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$',
);

enum CredentialCaptureStatus { completed, cancelled, retryable, repairRequired }

class CredentialCaptureResult {
  const CredentialCaptureResult(this.status, this.reasonCode);

  final CredentialCaptureStatus status;
  final String reasonCode;

  static CredentialCaptureResult parse(Object? raw) {
    if (raw is! Map ||
        raw.length != 3 ||
        raw.keys.any((key) => key is! String) ||
        raw['schema'] != credentialCaptureResultSchema ||
        raw['status'] is! String ||
        raw['reason_code'] is! String) {
      throw const FormatException('invalid credential capture result');
    }
    final status = switch (raw['status']) {
      'completed' => CredentialCaptureStatus.completed,
      'cancelled' => CredentialCaptureStatus.cancelled,
      'retryable' => CredentialCaptureStatus.retryable,
      'repair_required' => CredentialCaptureStatus.repairRequired,
      _ => throw const FormatException('unknown credential capture status'),
    };
    const reasons = {
      CredentialCaptureStatus.completed: {'none'},
      CredentialCaptureStatus.cancelled: {'none'},
      CredentialCaptureStatus.retryable: {
        'busy',
        'timeout',
        'controller_unavailable',
      },
      CredentialCaptureStatus.repairRequired: {
        'helper_unavailable',
        'helper_untrusted',
        'invalid_result',
        'installation_mismatch',
      },
    };
    final reason = raw['reason_code'] as String;
    if (!reasons[status]!.contains(reason)) {
      throw const FormatException('unknown credential capture reason');
    }
    return CredentialCaptureResult(status, reason);
  }
}

class InstalledCredentialCapture {
  const InstalledCredentialCapture({
    MethodChannel channel = const MethodChannel(credentialCaptureChannel),
  }) : _channel = channel;

  final MethodChannel _channel;

  Future<CredentialCaptureResult> capture({
    required bool installedMac,
    required String installationId,
    required String connectionId,
  }) async {
    if (!installedMac ||
        !_uuid.hasMatch(installationId) ||
        !_uuid.hasMatch(connectionId)) {
      throw const FormatException(
        'credential capture requires an installed local Mac connection',
      );
    }
    final input = <String, String>{
      'schema': credentialCaptureSchema,
      'installation_id': installationId,
      'connection_id': connectionId,
    };
    if (utf8.encode(jsonEncode(input)).length > 4096) {
      throw const FormatException('credential capture request is too large');
    }
    final raw = await _channel.invokeMethod<Object?>('capture', input);
    return CredentialCaptureResult.parse(raw);
  }
}
