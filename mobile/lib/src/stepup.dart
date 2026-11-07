import 'package:flutter/services.dart';

/// The exact text a device signs to approve (or reject) an approval: the
/// server's `api.StepUpMessage`. Both sides check the same test vector
/// (test/fixtures/stepup_vector.json).
String stepUpMessage({required String challengeId, required String nonce, required String approvalId, required String actionHash, required bool approve}) =>
    ['gubern-stepup-v1', challengeId, nonce, approvalId, actionHash.toLowerCase(), approve ? 'approve' : 'reject'].join('\n');

/// A device signing key. The production key lives in hardware (Android
/// Keystore / iOS Secure Enclave), is created on first use, never leaves
/// the chip, and asks for the person's biometrics or passcode on every
/// signature.
abstract class DeviceKey {
  /// The public key to register at pairing: base64 P-256
  /// SubjectPublicKeyInfo (or raw Ed25519 for software test keys).
  Future<String> publicKey();

  /// Signs [message]; base64 signature. Throws when the person cancels the
  /// biometric prompt.
  Future<String> sign(String message, {required String reason});

  /// Deletes the key (unpairing).
  Future<void> delete();
}

/// The hardware key, reached through the platform channel implemented in
/// android/app/src/main/kotlin/.../DeviceKeyPlugin.kt and
/// ios/Runner/DeviceKeyPlugin.swift.
class HardwareDeviceKey implements DeviceKey {
  const HardwareDeviceKey();

  static const _channel = MethodChannel('run.gubern/devicekey');

  @override
  Future<String> publicKey() async => (await _channel.invokeMethod<String>('publicKey'))!;

  @override
  Future<String> sign(String message, {required String reason}) async => (await _channel.invokeMethod<String>('sign', {'message': message, 'reason': reason}))!;

  @override
  Future<void> delete() => _channel.invokeMethod<void>('delete');
}
