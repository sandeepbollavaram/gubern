import Flutter
import LocalAuthentication
import Security
import UIKit

@main
@objc class AppDelegate: FlutterAppDelegate {
  override func application(
    _ application: UIApplication,
    didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]?
  ) -> Bool {
    GeneratedPluginRegistrant.register(with: self)
    if let controller = window?.rootViewController as? FlutterViewController {
      DeviceKeyPlugin.register(with: controller.binaryMessenger)
    }
    return super.application(application, didFinishLaunchingWithOptions: launchOptions)
  }
}

// The phone's Gubern device key (milestone H): an EC P-256 key created in
// the Secure Enclave. The private key never leaves the enclave, and every
// signature needs user presence (Face ID, Touch ID or the passcode). The
// public key is sent at pairing as a base64 SubjectPublicKeyInfo; signatures
// are base64 ASN.1 ECDSA over SHA-256 (ecdsaSignatureMessageX962SHA256),
// which the server verifies.
final class DeviceKeyPlugin {
  private static let tag = "run.gubern.devicekey.v1".data(using: .utf8)!

  static func register(with messenger: FlutterBinaryMessenger) {
    let channel = FlutterMethodChannel(name: "run.gubern/devicekey", binaryMessenger: messenger)
    channel.setMethodCallHandler { call, result in
      do {
        switch call.method {
        case "publicKey":
          result(try publicKey())
        case "sign":
          let args = call.arguments as? [String: Any] ?? [:]
          let message = args["message"] as? String ?? ""
          let reason = args["reason"] as? String ?? "Approve"
          result(try sign(message, reason: reason))
        case "delete":
          SecItemDelete([kSecClass: kSecClassKey, kSecAttrApplicationTag: tag] as CFDictionary)
          result(nil)
        default:
          result(FlutterMethodNotImplemented)
        }
      } catch {
        result(FlutterError(code: "devicekey", message: error.localizedDescription, details: nil))
      }
    }
  }

  private static func existingKey(_ context: LAContext? = nil) -> SecKey? {
    var query: [CFString: Any] = [
      kSecClass: kSecClassKey, kSecAttrApplicationTag: tag,
      kSecAttrKeyType: kSecAttrKeyTypeECSECPrimeRandom, kSecReturnRef: true,
    ]
    if let context { query[kSecUseAuthenticationContext] = context }
    var item: CFTypeRef?
    guard SecItemCopyMatching(query as CFDictionary, &item) == errSecSuccess, let item else { return nil }
    return (item as! SecKey)
  }

  private static func ensureKey() throws -> SecKey {
    if let key = existingKey() { return key }
    var error: Unmanaged<CFError>?
    guard let access = SecAccessControlCreateWithFlags(
      kCFAllocatorDefault, kSecAttrAccessibleWhenUnlockedThisDeviceOnly,
      [.privateKeyUsage, .userPresence], &error)
    else { throw error!.takeRetainedValue() as Error }
    let attrs: [CFString: Any] = [
      kSecAttrKeyType: kSecAttrKeyTypeECSECPrimeRandom,
      kSecAttrKeySizeInBits: 256,
      kSecAttrTokenID: kSecAttrTokenIDSecureEnclave,
      kSecPrivateKeyAttrs: [
        kSecAttrIsPermanent: true, kSecAttrApplicationTag: tag, kSecAttrAccessControl: access,
      ] as [CFString: Any],
    ]
    guard let key = SecKeyCreateRandomKey(attrs as CFDictionary, &error) else { throw error!.takeRetainedValue() as Error }
    return key
  }

  // SubjectPublicKeyInfo header for an uncompressed P-256 point.
  private static let spkiHeader: [UInt8] = [
    0x30, 0x59, 0x30, 0x13, 0x06, 0x07, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x02, 0x01,
    0x06, 0x08, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x03, 0x01, 0x07, 0x03, 0x42, 0x00,
  ]

  private static func publicKey() throws -> String {
    let key = try ensureKey()
    guard let pub = SecKeyCopyPublicKey(key) else { throw NSError(domain: "devicekey", code: 1) }
    var error: Unmanaged<CFError>?
    guard let raw = SecKeyCopyExternalRepresentation(pub, &error) as Data? else { throw error!.takeRetainedValue() as Error }
    return (Data(spkiHeader) + raw).base64EncodedString()
  }

  private static func sign(_ message: String, reason: String) throws -> String {
    _ = try ensureKey()
    let context = LAContext()
    context.localizedReason = reason
    guard let key = existingKey(context) else { throw NSError(domain: "devicekey", code: 2) }
    var error: Unmanaged<CFError>?
    guard let sig = SecKeyCreateSignature(key, .ecdsaSignatureMessageX962SHA256, Data(message.utf8) as CFData, &error) as Data? else {
      throw error!.takeRetainedValue() as Error
    }
    return sig.base64EncodedString()
  }
}
