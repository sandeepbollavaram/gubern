import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:gubern_mobile/src/api.dart';
import 'package:gubern_mobile/src/app_screens.dart';
import 'package:gubern_mobile/src/credential_store.dart';
import 'package:gubern_mobile/src/stepup.dart';
import 'package:gubern_mobile/src/ui/theme.dart';

class NoKey implements DeviceKey {
  @override
  Future<String> publicKey() async => '';
  @override
  Future<String> sign(String message, {required String reason}) async => throw UnimplementedError();
  @override
  Future<void> delete() async {}
}

ApprovalView approval(Map<String, dynamic> extra) => ApprovalView.fromJson({
      'id': '0123456789abcdef',
      'session_id': 's1',
      'action_hash': 'ab' * 32,
      'status': 'pending',
      'expires_at': DateTime.now().add(const Duration(minutes: 5)).toUtc().toIso8601String(),
      ...extra,
    });

Widget host(ApprovalView a) => MaterialApp(
      theme: buildTheme(Brightness.dark),
      home: ApprovalDetailScreen(api: GubernApi(null, CredentialStore(MemoryStore())), deviceKey: NoKey(), approval: a),
    );

void main() {
  testWidgets('high risk: shows what runs, the fingerprint, and needs a slide', (tester) async {
    await tester.binding.setSurfaceSize(const Size(430, 1400));
    await tester.pumpWidget(host(approval({'tool': 'run_terminal', 'risk': 'HIGH RISK', 'description': 'Delete the build directory', 'command': 'rm -rf build'})));
    expect(find.text('High risk'), findsOneWidget);
    expect(find.textContaining('rm -rf build'), findsOneWidget);
    expect(find.text('Fingerprint: ${'ab' * 8}'), findsOneWidget);
    expect(find.text('Slide to approve'), findsOneWidget);
    // No one-tap approve exists for high risk.
    expect(find.widgetWithText(ElevatedButton, 'Approve'), findsNothing);
    expect(find.text('Approve'), findsNothing);
  });

  testWidgets('low risk: a plain Approve button', (tester) async {
    await tester.binding.setSurfaceSize(const Size(430, 1400));
    await tester.pumpWidget(host(approval({'tool': 'write_file', 'risk': 'LOW RISK', 'description': 'Write notes.md', 'preview': '# notes'})));
    expect(find.text('Low risk'), findsOneWidget);
    expect(find.text('Approve'), findsOneWidget);
    expect(find.text('Slide to approve'), findsNothing);
  });

  testWidgets('a request without details warns the approver', (tester) async {
    await tester.binding.setSurfaceSize(const Size(430, 1400));
    await tester.pumpWidget(host(approval({'tool': 'write_file', 'risk': 'MEDIUM RISK'})));
    expect(find.textContaining('no details'), findsOneWidget);
  });

  testWidgets('pairing screen offers scan and link entry', (tester) async {
    await tester.binding.setSurfaceSize(const Size(430, 1000));
    await tester.pumpWidget(MaterialApp(
      theme: buildTheme(Brightness.dark),
      // Reduced motion: the aurora stands still (and the test can settle).
      builder: (c, child) => MediaQuery(data: MediaQuery.of(c).copyWith(disableAnimations: true), child: child!),
      home: PairScreen(store: CredentialStore(MemoryStore()), deviceKey: NoKey(), onPaired: (_) {}),
    ));
    expect(find.text('Scan pairing code'), findsOneWidget);
    expect(find.text('Enter the link instead'), findsOneWidget);
    await tester.tap(find.text('Enter the link instead'));
    await tester.pumpAndSettle();
    expect(find.text('Pair with a link'), findsOneWidget);
    await tester.enterText(find.byType(TextField).first, 'https://evil.example/pair');
    await tester.tap(find.text('Pair').last);
    await tester.pumpAndSettle();
    expect(find.text('Not a Gubern pairing code'), findsOneWidget);
  });
}
