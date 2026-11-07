import 'dart:convert';
import 'dart:io';

import 'package:cryptography/cryptography.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:gubern_mobile/src/api.dart';
import 'package:gubern_mobile/src/credential_store.dart';
import 'package:gubern_mobile/src/pairing_link.dart';
import 'package:gubern_mobile/src/pinning.dart';
import 'package:gubern_mobile/src/stepup.dart';

/// A software Ed25519 key standing in for the hardware key in tests.
class SoftKey implements DeviceKey {
  final _algo = Ed25519();
  late final Future<SimpleKeyPair> _pair = _algo.newKeyPair();
  int signatures = 0;
  @override
  Future<String> publicKey() async => base64.encode((await (await _pair).extractPublicKey()).bytes);
  @override
  Future<String> sign(String message, {required String reason}) async {
    signatures++;
    return base64.encode((await _algo.sign(utf8.encode(message), keyPair: await _pair)).bytes);
  }

  @override
  Future<void> delete() async {}
}

/// A fake Gubern server over pinned TLS: pairing, approvals, step-up with
/// real signature checks, and revocation.
class FakeGubern {
  late HttpServer server;
  String? devicePub;
  bool revoked = false;
  bool changeHash = false;
  final challenges = <String, String>{};
  final decisions = <String>[];
  final hash = 'cd' * 32;

  Uri get url => Uri.parse('https://127.0.0.1:${server.port}');

  Future<void> start() async {
    final ctx = SecurityContext()
      ..useCertificateChainBytes(File('test/fixtures/server_cert.pem').readAsBytesSync())
      ..usePrivateKeyBytes(File('test/fixtures/server_key.pem').readAsBytesSync());
    server = await HttpServer.bindSecure(InternetAddress.loopbackIPv4, 0, ctx);
    server.listen(_handle);
  }

  Map<String, dynamic> get approval => {
        'id': '0123456789abcdef', 'session_id': 's1', 'tool': 'run_terminal', 'risk': 'HIGH RISK',
        'description': 'Run a build', 'command': 'make', 'action_hash': hash, 'status': 'pending',
        'expires_at': DateTime.now().add(const Duration(minutes: 5)).toUtc().toIso8601String(),
      };

  Future<void> _handle(HttpRequest req) async {
    final body = await utf8.decoder.bind(req).join();
    final json = body.isEmpty ? <String, dynamic>{} : jsonDecode(body) as Map<String, dynamic>;
    void reply(int status, Object data) {
      req.response.statusCode = status;
      req.response.headers.contentType = ContentType.json;
      req.response.write(jsonEncode(data));
      req.response.close();
    }

    if (req.uri.path == '/api/pairing/redeem') {
      if (json['code'] != 'ABCD-EFGH-JKMN') return reply(400, {'error': 'invalid, used or expired code'});
      devicePub = json['public_key'] as String;
      return reply(201, {'device_id': 'dev_1', 'principal_id': 'p_ada', 'secret': 'device-secret', 'key_id': 'k1'});
    }
    if (revoked || req.headers.value('authorization') != 'Bearer device-secret') {
      return reply(401, {'error': 'authentication required', 'reason': 'device-revoked'});
    }
    switch (req.uri.path) {
      case '/api/approvals':
        return reply(200, {'approvals': [approval]});
      case '/api/stepup/challenge':
        challenges['su_1'] = 'n0nce';
        return reply(200, {'challenge_id': 'su_1', 'nonce': 'n0nce', 'action_hash': changeHash ? 'ef' * 32 : hash});
      case '/api/approvals/0123456789abcdef/decision':
        if (json['approved'] == true) {
          final p = json['stepup'] as Map<String, dynamic>?;
          if (p == null || challenges.remove(p['challenge_id']) == null) return reply(403, {'reason': 'stepup-required'});
          final msg = stepUpMessage(challengeId: 'su_1', nonce: 'n0nce', approvalId: '0123456789abcdef', actionHash: hash, approve: true);
          final ok = await Ed25519().verify(utf8.encode(msg),
              signature: Signature(base64.decode(p['signature'] as String), publicKey: SimplePublicKey(base64.decode(devicePub!), type: KeyPairType.ed25519)));
          if (!ok) return reply(403, {'reason': 'stepup-invalid'});
        }
        decisions.add(json['approved'] == true ? 'approved' : 'rejected');
        return reply(200, {'approval': {...approval, 'status': decisions.last, 'decided_via': 'mobile'}});
    }
    reply(404, {'error': 'not found'});
  }
}

void main() {
  late FakeGubern fake;
  late CredentialStore store;
  late SoftKey key;
  final fp = File('test/fixtures/server_fingerprint.txt').readAsStringSync().trim();

  setUp(() async {
    fake = FakeGubern();
    await fake.start();
    store = CredentialStore(MemoryStore());
    key = SoftKey();
  });
  tearDown(() => fake.server.close(force: true));

  PairingLink link() => PairingLink.parse(
      'gubern://pair?v=1&u=${Uri.encodeComponent(fake.url.toString())}&c=ABCD-EFGH-JKMN&fp=$fp');

  test('pairs, stores the credential, and approves with a signed step-up', () async {
    final creds = await GubernApi.pair(link(), key, store, label: 'phone');
    expect((await store.load())!.secret, 'device-secret');
    expect(fake.devicePub, await key.publicKey());

    final api = GubernApi(creds, store, client: pinnedClient(creds.fingerprint));
    final pending = await api.pendingApprovals();
    expect(pending.single.highRisk, isTrue);
    expect(pending.single.fingerprint, 'cd' * 8);

    final done = await api.approve(pending.single, key);
    expect(done.status, 'approved');
    expect(key.signatures, 1);
    expect(fake.decisions, ['approved']);
  });

  test('a changed action is caught before signing', () async {
    final creds = await GubernApi.pair(link(), key, store, label: 'phone');
    final api = GubernApi(creds, store, client: pinnedClient(creds.fingerprint));
    final a = (await api.pendingApprovals()).single;
    fake.changeHash = true;
    await expectLater(api.approve(a, key), throwsA(isA<ApiException>().having((e) => e.reason, 'reason', 'action-mismatch')));
    expect(key.signatures, 0);
  });

  test('credential revocation wipes the stored credential', () async {
    final creds = await GubernApi.pair(link(), key, store, label: 'phone');
    final api = GubernApi(creds, store, client: pinnedClient(creds.fingerprint));
    fake.revoked = true;
    await expectLater(api.pendingApprovals(), throwsA(isA<RevokedException>()));
    expect(await store.load(), isNull);
    expect(api.creds, isNull);
  });

  test('pairing with the wrong fingerprint never reaches the server', () async {
    final wrong = PairingLink.parse(
        'gubern://pair?v=1&u=${Uri.encodeComponent(fake.url.toString())}&c=ABCD-EFGH-JKMN&fp=${'00' * 32}');
    await expectLater(GubernApi.pair(wrong, key, store, label: 'phone'), throwsA(isA<HandshakeException>()));
    expect(fake.devicePub, isNull);
    expect(await store.load(), isNull);
  });
}
