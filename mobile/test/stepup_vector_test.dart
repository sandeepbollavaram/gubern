import 'dart:convert';
import 'dart:io';

import 'package:cryptography/cryptography.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:gubern_mobile/src/stepup.dart';

// The server (internal/api TestStepUpVector) checks the same file: the app
// must build the identical message, and a signature over it with the
// vector's key must be the one the server accepts.
void main() {
  test('step-up message and signature match the server vector', () async {
    final v = jsonDecode(File('test/fixtures/stepup_vector.json').readAsStringSync()) as Map<String, dynamic>;
    final msg = stepUpMessage(
      challengeId: v['challenge_id'] as String,
      nonce: v['nonce'] as String,
      approvalId: v['approval_id'] as String,
      actionHash: (v['action_hash'] as String).toUpperCase(), // case must not matter
      approve: v['decision'] == 'approve',
    );
    expect(msg, v['message']);

    final seedHex = v['seed_hex'] as String;
    final seed = [for (var i = 0; i < seedHex.length; i += 2) int.parse(seedHex.substring(i, i + 2), radix: 16)];
    final algo = Ed25519();
    final pair = await algo.newKeyPairFromSeed(seed);
    final pub = await pair.extractPublicKey();
    expect(base64.encode(pub.bytes), v['public_key']);
    final sig = await algo.sign(utf8.encode(msg), keyPair: pair);
    expect(base64.encode(sig.bytes), v['signature']);

    // A reject message differs, so its signature can't approve.
    final reject = stepUpMessage(
      challengeId: v['challenge_id'] as String,
      nonce: v['nonce'] as String,
      approvalId: v['approval_id'] as String,
      actionHash: v['action_hash'] as String,
      approve: false,
    );
    expect(reject, isNot(msg));
  });
}
