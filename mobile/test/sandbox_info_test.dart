import 'package:flutter_test/flutter_test.dart';
import 'package:gubern_mobile/src/api.dart';

void main() {
  test('reads the server\'s sandbox report, keeping what it does not enforce', () {
    final s = SandboxInfo.fromJson({
      'label': 'Contained (Windows)',
      'adapter': 'job-object',
      'level': 'contained',
      'platform': 'Windows',
      'network': 'not enforced',
      'enforced': ['every process the command starts is killed with it'],
      'not_enforced': ['file system fence', 'network policy'],
    });
    expect(s.label, 'Contained (Windows)');
    expect(s.short, 'Contained');
    expect(s.notEnforced, hasLength(2));
    expect(s.network, 'not enforced');
  });

  test('a missing or partial report reads as not sandboxed', () {
    final s = SandboxInfo.fromJson({'not_enforced': null});
    expect(s.level, 'none');
    expect(s.short, 'Not sandboxed');
    expect(s.enforced, isEmpty);
    expect(SandboxInfo.fromJson({'level': 'isolated', 'label': 'Isolated (Linux)'}).short, 'Isolated');
  });
}
