# Mobile app (Milestone H)

Status: **implemented, not released.** The plan's gate holds: no production
release until pairing, storage and revocation are verified on real devices.
Depends on A, B, D and F.

Code: `mobile/` (Flutter, Android and iOS) and, on the server,
`internal/api/stepup.go` with device keys in `internal/identity/pairing.go`.

## 1. What the app does

- **Pairs** with a Gubern server by scanning the dashboard's QR, or by
  pasting the link (F).
- **Lists** the approvals waiting on the paired person and refreshes them.
- **Shows** each approval in full: tool, risk, description, command or
  preview, session, workspace, the action **fingerprint** (the first 16 hex
  digits of its SHA-256) and a countdown.
- **Approves** with a signed step-up (§4), or **rejects**.
- **Unpairs:** wipes the credential and deletes the device key.

## 2. Credential storage

`flutter_secure_storage` holds the server URL, TLS fingerprint, device id,
principal id and the device credential.

| Platform | How it is stored |
|---|---|
| Android | Keystore-backed encrypted storage, reset if it is tampered with. |
| iOS | The Keychain, `unlocked_this_device`: readable only while the phone is unlocked, never synced, never in backups. |

**Revocation:** any 401 from the server (credential or device revoked) wipes
the stored credential and sends the app back to pairing. Tested in
`mobile/test/api_test.dart` and on a real server in
`integration/stepup_test.go`.

## 3. TLS pinning

When the pairing QR carries `fp` (the SHA-256 of the server certificate's
DER), the app's HTTP client trusts **only** that certificate. It uses
`SecurityContext(withTrustedRoots: false)` and checks the fingerprint of
whatever certificate the server presents, so a publicly trusted certificate
for the same name is refused too.

- **A mismatch fails the handshake before any request is sent,** including
  the pairing redemption itself, so a wrong fingerprint never reveals the
  code.
- **Plain HTTP servers** (no TLS on the management API until G-D2) can be
  paired only after an explicit "Unencrypted connection" confirmation. A
  link that carries a fingerprint over plain HTTP is rejected as malformed.

Tested against real TLS servers on loopback: `mobile/test/pinning_test.dart`
covers a pinned certificate, a fingerprint mismatch, an impostor
certificate, and no pin with a self-signed certificate.

## 4. Signed step-up

A device credential acts as the person within its scope ceiling (F). A
stolen credential alone must not be able to approve anything, so approving
from a device needs a signature by the device's **hardware key** over a
one-time server challenge.

The device key:

- is EC P-256, created on first use;
- lives on **Android** in the Keystore (StrongBox when available), with user
  authentication required on every use (`timeout 0`, biometric strong or
  device credential, invalidated when new biometrics are enrolled);
- lives on **iOS** in the Secure Enclave, `kSecAttrAccessibleWhenUnlockedThisDeviceOnly`,
  with `.privateKeyUsage` and `.userPresence`;
- never leaves the hardware, and asks for biometrics or the passcode for
  every signature;
- has its public key registered at pairing (base64 SubjectPublicKeyInfo).
  Ed25519 keys are accepted too, for software keys.

The flow:

1. `POST /api/stepup/challenge {approval_id}` returns a `challenge_id`, a
   random `nonce` and the approval's `action_hash`. The challenge is valid
   for 60 s, is bound to this device, this approval and this action hash,
   and is only available to device credentials.
2. The app checks that the challenge's `action_hash` equals the one it
   displayed. If the action changed, it stops before signing.
3. The device signs this UTF-8 message:

   ```
   gubern-stepup-v1
   <challenge_id>
   <nonce>
   <approval_id>
   <action_hash, lowercase hex>
   approve | reject
   ```

4. `POST /api/approvals/{id}/decision` sends
   `{approved: true, action_hash, stepup: {challenge_id, signature}}`. The
   server takes the challenge (single use, even when the answer is wrong),
   checks the binding and the device's revocation status, and verifies the
   signature: ECDSA P-256 over SHA-256, or Ed25519. Only then does B decide,
   recorded as the person **via `mobile`**.

What else applies to devices:

- **Rejecting** needs no step-up.
- **The legacy `/api/agents/approve`** refuses approvals from device
  credentials.
- **A shared test vector,** `mobile/test/fixtures/stepup_vector.json`, is
  checked by both `TestStepUpVector` (Go) and `stepup_vector_test.dart`
  (Dart), so the two sides can't drift.

## 5. Approval UX

- **Risk is visible:** a risk chip, and a red warning icon for high and
  critical.
- **No silent requests:** a request with no command or preview says so, and
  advises rejecting it.
- **High risk is deliberate:** approving takes two taps, then the biometric
  prompt from the hardware key.
- **Clear outcomes:** "already decided elsewhere" (B's first decision wins),
  "expired", and "the action changed".

## 6. Tests and evidence

| Area | Test | Result |
|---|---|---|
| Pairing-link parsing | `pairing_link_test.dart` | pass |
| Step-up vector, matching Go | `stepup_vector_test.dart` / `TestStepUpVector` | pass |
| Pinning: match, mismatch, impostor, unpinned self-signed | `pinning_test.dart` | pass |
| Pair, signed approval, changed action, revocation wipe, wrong fingerprint | `api_test.dart` | pass |
| Approval screen: details, fingerprint, two-tap | `approval_screen_test.dart` | pass |
| Server step-up: Ed25519 and P-256, missing, replay, reject-signature, cross-approval, foreign key, expiry, legacy route | `internal/api/stepup_test.go` | pass |
| Real server: phone approves a real run with a P-256 signature; revoked phone gets 401 | `integration/stepup_test.go` | pass |

## 7. Before any release

- **Real devices:** run pairing, approval and revocation on a physical
  Android phone (StrongBox and non-StrongBox) and a physical iPhone. The
  native key code (`DeviceKeyPlugin.kt`, the Swift plugin in
  `AppDelegate.swift`) cannot be exercised by the unit tests. The Android debug APK builds (so the Kotlin plugin compiles); the iOS project needs a Mac to build.
- **TLS on the management API** (G-D2), so pinning protects real traffic.
- **Signing:** an app signing configuration (the debug key is used now), a
  store listing and a privacy review.
