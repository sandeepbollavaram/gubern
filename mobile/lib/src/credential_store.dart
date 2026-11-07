import 'dart:convert';

import 'package:flutter_secure_storage/flutter_secure_storage.dart';

/// What the app keeps after pairing. The secret is a device credential: it
/// acts as the person within the pairing's scope ceiling, and approving also
/// needs the hardware key, so the secret alone can't approve anything.
class Credentials {
  Credentials({
    required this.server,
    required this.deviceId,
    required this.secret,
    this.fingerprint,
    this.principalId,
  });

  final Uri server;
  final String deviceId;
  final String secret;
  final String? fingerprint;
  final String? principalId;

  Map<String, dynamic> toJson() => {
        'server': server.toString(),
        'device_id': deviceId,
        'secret': secret,
        'fingerprint': fingerprint,
        'principal_id': principalId,
      };

  static Credentials fromJson(Map<String, dynamic> j) => Credentials(
        server: Uri.parse(j['server'] as String),
        deviceId: j['device_id'] as String,
        secret: j['secret'] as String,
        fingerprint: j['fingerprint'] as String?,
        principalId: j['principal_id'] as String?,
      );
}

/// Secret storage the app depends on; swapped for an in-memory one in tests.
abstract class SecureStore {
  Future<String?> read(String key);
  Future<void> write(String key, String value);
  Future<void> delete(String key);
}

/// The platform's secure storage: Android Keystore-backed encrypted
/// preferences, iOS Keychain readable only when unlocked on this device
/// (never in backups or on other devices).
class PlatformSecureStore implements SecureStore {
  PlatformSecureStore()
      : _s = const FlutterSecureStorage(
          aOptions: AndroidOptions(resetOnError: true),
          iOptions: IOSOptions(accessibility: KeychainAccessibility.unlocked_this_device),
        );

  final FlutterSecureStorage _s;

  @override
  Future<String?> read(String key) => _s.read(key: key);
  @override
  Future<void> write(String key, String value) => _s.write(key: key, value: value);
  @override
  Future<void> delete(String key) => _s.delete(key: key);
}

/// In-memory store for tests.
class MemoryStore implements SecureStore {
  final Map<String, String> values = {};
  @override
  Future<String?> read(String key) async => values[key];
  @override
  Future<void> write(String key, String value) async => values[key] = value;
  @override
  Future<void> delete(String key) async => values.remove(key);
}

/// Loads, saves and wipes the credentials.
class CredentialStore {
  CredentialStore(this._store);
  final SecureStore _store;
  static const _key = 'gubern.credentials.v1';

  Future<Credentials?> load() async {
    final raw = await _store.read(_key);
    if (raw == null) return null;
    try {
      return Credentials.fromJson(jsonDecode(raw) as Map<String, dynamic>);
    } on FormatException {
      await wipe();
      return null;
    }
  }

  Future<void> save(Credentials c) => _store.write(_key, jsonEncode(c.toJson()));

  /// Forgets the pairing (after revocation, or when the person unpairs).
  Future<void> wipe() => _store.delete(_key);
}
