import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:gubern_mobile/src/pinning.dart';

// Fingerprint pinning against real TLS servers on 127.0.0.1, using the
// self-signed certificates in test/fixtures.
String fixture(String name) => File('test/fixtures/$name').readAsStringSync().trim();

Future<HttpServer> tlsServer(String name) async {
  final ctx = SecurityContext()
    ..useCertificateChainBytes(File('test/fixtures/${name}_cert.pem').readAsBytesSync())
    ..usePrivateKeyBytes(File('test/fixtures/${name}_key.pem').readAsBytesSync());
  final srv = await HttpServer.bindSecure(InternetAddress.loopbackIPv4, 0, ctx);
  srv.listen((req) {
    req.response.write('{"ok":true}');
    req.response.close();
  });
  return srv;
}

Future<int> get(HttpClient c, HttpServer s) async {
  final req = await c.getUrl(Uri.parse('https://127.0.0.1:${s.port}/health'));
  final res = await req.close();
  await res.drain<void>();
  return res.statusCode;
}

void main() {
  late HttpServer server;
  setUp(() async => server = await tlsServer('server'));
  tearDown(() => server.close(force: true));

  test('the pinned certificate is accepted', () async {
    expect(await get(pinnedClient(fixture('server_fingerprint.txt')), server), 200);
  });

  test('a fingerprint mismatch fails the handshake', () async {
    await expectLater(get(pinnedClient(fixture('other_fingerprint.txt')), server), throwsA(isA<HandshakeException>()));
  });

  test('a server presenting another certificate is refused', () async {
    final impostor = await tlsServer('other');
    addTearDown(() => impostor.close(force: true));
    await expectLater(get(pinnedClient(fixture('server_fingerprint.txt')), impostor), throwsA(isA<HandshakeException>()));
  });

  test('without a pin, an untrusted self-signed certificate is refused', () async {
    await expectLater(get(pinnedClient(null), server), throwsA(isA<HandshakeException>()));
  });
}
