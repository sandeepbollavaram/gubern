import 'dart:io';

import 'package:crypto/crypto.dart' as hashing;

/// SHA-256 of a certificate's DER bytes, lowercase hex.
String certificateFingerprint(X509Certificate cert) => hashing.sha256.convert(cert.der).toString();

/// Returns an HttpClient that trusts exactly one server certificate: the one
/// whose SHA-256 is [fingerprint] (from the pairing QR). The system roots are
/// not used at all, so every certificate, including a publicly trusted one,
/// goes through the pin check; a mismatch fails the handshake.
///
/// With [fingerprint] null the client uses the platform's normal validation
/// (only for servers paired without a published fingerprint).
HttpClient pinnedClient(String? fingerprint) {
  if (fingerprint == null) return HttpClient();
  final pin = fingerprint.toLowerCase();
  final client = HttpClient(context: SecurityContext(withTrustedRoots: false));
  client.badCertificateCallback = (cert, host, port) => certificateFingerprint(cert) == pin;
  return client;
}
