import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:gubern_mobile/src/api.dart';
import 'package:gubern_mobile/src/app_screens.dart';
import 'package:gubern_mobile/src/credential_store.dart';
import 'package:gubern_mobile/src/stepup.dart';

class NoKey implements DeviceKey {
  @override
  Future<String> publicKey() async => '';
  @override
  Future<String> sign(String message, {required String reason}) async => throw UnimplementedError();
  @override
  Future<void> delete() async {}
}

void main() {
  testWidgets('approval detail shows what runs and needs two taps for high risk', (tester) async {
    final a = ApprovalView.fromJson({
      'id': '0123456789abcdef', 'session_id': 's1', 'tool': 'run_terminal', 'risk': 'HIGH RISK',
      'description': 'Delete the build directory', 'command': 'rm -rf build', 'action_hash': 'ab' * 32,
      'status': 'pending', 'expires_at': DateTime.now().add(const Duration(minutes: 5)).toUtc().toIso8601String(),
    });
    await tester.pumpWidget(MaterialApp(
      home: ApprovalDetailScreen(api: GubernApi(null, CredentialStore(MemoryStore())), deviceKey: NoKey(), approval: a),
    ));
    expect(find.text('rm -rf build'), findsOneWidget);
    expect(find.text('Fingerprint: ${'ab' * 8}'), findsOneWidget);
    expect(find.textContaining('two taps'), findsOneWidget);

    await tester.tap(find.text('Approve'));
    await tester.pump();
    expect(find.text('Tap again to approve'), findsOneWidget);
  });

  testWidgets('a request without details warns the approver', (tester) async {
    final a = ApprovalView.fromJson({
      'id': '0123456789abcdef', 'tool': 'write_file', 'risk': 'MEDIUM RISK', 'action_hash': 'ab' * 32,
      'expires_at': DateTime.now().add(const Duration(minutes: 5)).toUtc().toIso8601String(),
    });
    await tester.pumpWidget(MaterialApp(
      home: ApprovalDetailScreen(api: GubernApi(null, CredentialStore(MemoryStore())), deviceKey: NoKey(), approval: a),
    ));
    expect(find.textContaining('no details'), findsOneWidget);
  });
}
