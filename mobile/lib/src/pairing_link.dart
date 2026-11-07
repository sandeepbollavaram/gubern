/// Parses the pairing QR the Gubern dashboard shows:
/// `gubern://pair?v=1&u=<server URL>&c=<code>[&fp=<sha256 hex>]`
/// (docs/design/pairing.md §2).
class PairingLink {
  PairingLink({required this.server, required this.code, this.fingerprint});

  /// The Gubern server's base URL.
  final Uri server;

  /// The one-time pairing code, XXXX-XXXX-XXXX.
  final String code;

  /// SHA-256 (lowercase hex) of the server's TLS certificate, when the
  /// server published one. The app pins it for every later request.
  final String? fingerprint;

  /// True when the connection would not be encrypted: plain HTTP. Allowed
  /// only after the person confirms (a LAN without TLS, docs/design/
  /// remote-access.md §3.1).
  bool get insecure => server.scheme != 'https';

  static final _code = RegExp(r'^[0-9A-Za-z]{4}-?[0-9A-Za-z]{4}-?[0-9A-Za-z]{4}$');
  static final _fp = RegExp(r'^[0-9a-f]{64}$');

  /// Parses [raw]; throws [FormatException] for anything that is not a
  /// well-formed version-1 pairing link.
  static PairingLink parse(String raw) {
    final uri = Uri.tryParse(raw.trim());
    if (uri == null || uri.scheme != 'gubern' || uri.host != 'pair') {
      throw const FormatException('Not a Gubern pairing code');
    }
    final q = uri.queryParameters;
    if (q['v'] != '1') {
      throw const FormatException('This pairing code needs a newer app');
    }
    final server = Uri.tryParse(q['u'] ?? '');
    if (server == null || (server.scheme != 'https' && server.scheme != 'http') || server.host.isEmpty) {
      throw const FormatException('The pairing code has no valid server address');
    }
    final code = q['c'] ?? '';
    if (!_code.hasMatch(code)) {
      throw const FormatException('The pairing code is malformed');
    }
    String? fp = q['fp']?.toLowerCase().replaceAll(':', '');
    if (fp != null && fp.isEmpty) fp = null;
    if (fp != null && !_fp.hasMatch(fp)) {
      throw const FormatException('The server fingerprint is malformed');
    }
    if (fp != null && server.scheme != 'https') {
      throw const FormatException('A pinned server must use https');
    }
    return PairingLink(server: server, code: code.toUpperCase(), fingerprint: fp);
  }
}
