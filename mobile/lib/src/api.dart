import 'dart:convert';
import 'dart:io';

import 'credential_store.dart';
import 'pairing_link.dart';
import 'pinning.dart';
import 'stepup.dart';

/// The device credential was revoked (or the device was unpaired): the app
/// has already wiped it and must return to pairing.
class RevokedException implements Exception {
  @override
  String toString() => 'This phone is no longer paired with Gubern.';
}

/// The server refused a request.
class ApiException implements Exception {
  ApiException(this.status, this.reason, this.message);
  final int status;
  final String? reason;
  final String message;
  @override
  String toString() => message;
}

/// One approval as the app shows it.
class ApprovalView {
  ApprovalView.fromJson(Map<String, dynamic> j)
      : id = j['id'] as String,
        sessionId = j['session_id'] as String? ?? '',
        tool = j['tool'] as String? ?? '',
        risk = j['risk'] as String? ?? '',
        description = j['description'] as String? ?? '',
        command = j['command'] as String?,
        preview = j['preview'] as String?,
        workspace = j['workspace'] as String?,
        actionHash = j['action_hash'] as String? ?? '',
        status = j['status'] as String? ?? 'pending',
        expiresAt = DateTime.tryParse(j['expires_at'] as String? ?? '') ?? DateTime.now(),
        decidedBy = j['decided_by'] as String?,
        decidedVia = j['decided_via'] as String?;

  final String id, sessionId, tool, risk, description, actionHash, status;
  final String? command, preview, workspace, decidedBy, decidedVia;
  final DateTime expiresAt;

  bool get highRisk => risk.toUpperCase().startsWith('HIGH') || risk.toUpperCase().startsWith('CRITICAL');

  /// The start of the action hash, shown so the person can compare it with
  /// what the dashboard or Telegram shows for the same action.
  String get fingerprint => actionHash.length >= 16 ? actionHash.substring(0, 16) : actionHash;
}

/// HTTP client for the Gubern API, pinned to the paired server's
/// certificate. A 401 wipes the stored credential (credential revocation).
class GubernApi {
  GubernApi(this.creds, this.store, {HttpClient? client})
      : _client = client ?? pinnedClient(creds?.fingerprint);

  /// Null until paired.
  Credentials? creds;
  final CredentialStore store;
  final HttpClient _client;

  /// Pairs this phone: redeems the code with the device's public key, and
  /// stores the returned credential securely.
  static Future<Credentials> pair(PairingLink link, DeviceKey key, CredentialStore store,
      {required String label, HttpClient? client}) async {
    final api = GubernApi(null, store, client: client ?? pinnedClient(link.fingerprint));
    final body = await api._send('POST', link.server.resolve('/api/pairing/redeem'),
        {'code': link.code, 'label': label, 'public_key': await key.publicKey()},
        auth: false);
    final c = Credentials(
      server: link.server,
      deviceId: body['device_id'] as String,
      secret: body['secret'] as String,
      fingerprint: link.fingerprint,
      principalId: body['principal_id'] as String?,
    );
    await store.save(c);
    return c;
  }

  Future<Map<String, dynamic>> _send(String method, Uri url, Object? body, {bool auth = true}) async {
    final req = await _client.openUrl(method, url);
    req.headers.set(HttpHeaders.acceptHeader, 'application/json');
    if (auth) {
      final c = creds;
      if (c == null) throw RevokedException();
      req.headers.set(HttpHeaders.authorizationHeader, 'Bearer ${c.secret}');
    }
    if (body != null) {
      req.headers.contentType = ContentType.json;
      req.add(utf8.encode(jsonEncode(body)));
    }
    final res = await req.close();
    final text = await res.transform(utf8.decoder).join();
    Map<String, dynamic> json = {};
    try {
      final decoded = jsonDecode(text);
      if (decoded is Map<String, dynamic>) json = decoded;
    } on FormatException {
      // non-JSON body
    }
    if (res.statusCode == HttpStatus.unauthorized && auth) {
      await store.wipe();
      creds = null;
      throw RevokedException();
    }
    if (res.statusCode >= 300) {
      throw ApiException(res.statusCode, json['reason'] as String?, (json['error'] as String?) ?? 'Request failed (${res.statusCode})');
    }
    return json;
  }

  Uri _url(String path) => creds!.server.resolve(path);

  Future<List<ApprovalView>> pendingApprovals() async {
    final body = await _send('GET', _url('/api/approvals?status=pending'), null);
    return [for (final a in (body['approvals'] as List? ?? const [])) ApprovalView.fromJson(a as Map<String, dynamic>)];
  }

  Future<ApprovalView> approval(String id) async =>
      ApprovalView.fromJson(await _send('GET', _url('/api/approvals/$id'), null));

  /// Rejects: no step-up needed.
  Future<ApprovalView> reject(ApprovalView a) async {
    final body = await _send('POST', _url('/api/approvals/${a.id}/decision'), {'approved': false, 'action_hash': a.actionHash});
    return ApprovalView.fromJson(body['approval'] as Map<String, dynamic>);
  }

  /// Approves with a signed step-up: a fresh server challenge, signed by the
  /// hardware key (which asks for biometrics) over this approval's action
  /// hash and the decision.
  Future<ApprovalView> approve(ApprovalView a, DeviceKey key) async {
    final ch = await _send('POST', _url('/api/stepup/challenge'), {'approval_id': a.id});
    if ((ch['action_hash'] as String?)?.toLowerCase() != a.actionHash.toLowerCase()) {
      throw ApiException(409, 'action-mismatch', 'The action changed since you opened it. Review it again.');
    }
    final msg = stepUpMessage(
      challengeId: ch['challenge_id'] as String,
      nonce: ch['nonce'] as String,
      approvalId: a.id,
      actionHash: a.actionHash,
      approve: true,
    );
    final sig = await key.sign(msg, reason: 'Approve ${a.tool}');
    final body = await _send('POST', _url('/api/approvals/${a.id}/decision'), {
      'approved': true,
      'action_hash': a.actionHash,
      'stepup': {'challenge_id': ch['challenge_id'], 'signature': sig},
    });
    return ApprovalView.fromJson(body['approval'] as Map<String, dynamic>);
  }
}
