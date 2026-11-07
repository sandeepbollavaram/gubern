import 'package:flutter_test/flutter_test.dart';
import 'package:gubern_mobile/src/pairing_link.dart';

void main() {
  final fp = 'ab' * 32;

  test('parses a pinned https link', () {
    final l = PairingLink.parse('gubern://pair?v=1&u=https%3A%2F%2Fhome.example%3A9443&c=abcd-efgh-jkmn&fp=$fp');
    expect(l.server.toString(), 'https://home.example:9443');
    expect(l.code, 'ABCD-EFGH-JKMN');
    expect(l.fingerprint, fp);
    expect(l.insecure, isFalse);
  });

  test('accepts an unpinned http link but marks it insecure', () {
    final l = PairingLink.parse('gubern://pair?v=1&u=http%3A%2F%2F192.168.1.5%3A9090&c=ABCDEFGHJKMN');
    expect(l.insecure, isTrue);
    expect(l.fingerprint, isNull);
  });

  test('rejects malformed links', () {
    for (final bad in [
      'https://evil.example/pair?c=ABCD-EFGH-JKMN',
      'gubern://pair?v=2&u=https%3A%2F%2Fx&c=ABCD-EFGH-JKMN',
      'gubern://pair?v=1&c=ABCD-EFGH-JKMN',
      'gubern://pair?v=1&u=ftp%3A%2F%2Fx&c=ABCD-EFGH-JKMN',
      'gubern://pair?v=1&u=https%3A%2F%2Fx&c=short',
      'gubern://pair?v=1&u=https%3A%2F%2Fx&c=ABCD-EFGH-JKMN&fp=zz',
      // A fingerprint over plain http would pin nothing.
      'gubern://pair?v=1&u=http%3A%2F%2Fx&c=ABCD-EFGH-JKMN&fp=$fp',
    ]) {
      expect(() => PairingLink.parse(bad), throwsFormatException, reason: bad);
    }
  });
}
