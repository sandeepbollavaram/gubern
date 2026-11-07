# Devices and pairing (Milestone F)

Status: **implemented** on `dev/dashboard-v2`. Depends on A (identity), B
(approvals) and C (harness).

## 1. Codes

A person creates a pairing code with `POST /api/pairing`, which needs
`devices:manage`.

| Property | Value |
|---|---|
| Format | 12 Crockford base32 characters (60 random bits), shown as `XXXX-XXXX-XXXX`. Typed codes ignore case and dashes, and map O→0, I/L→1. |
| Storage | Only `SHA-256("gubern-pairing" ‖ code)` is stored. The code is shown once. |
| Bound to | One **person** and one **purpose**: `device`, `telegram`, `whatsapp` or `discord`. A `device` code also carries a **scope ceiling**. |
| Lifetime | 10 minutes (`GUBERN_PAIRING_TTL`). |
| Use | Single use. Redemption is one conditional `UPDATE ... WHERE used_at IS NULL`. |
| Errors | An unknown, used, expired or burned code all get the same answer, "invalid, used or expired". |
| Wrong channel | A valid code presented on another channel counts a failure without being consumed. The third failure burns it. |
| Who | The caller pairs for themselves. Pairing for another person needs `identity:manage`. Service principals cannot be paired. |
| Audit | `pairing.create`, `device.paired`, `external.link`, `pairing.failed`, `pairing.wrong-channel`, `pairing.conflict`, `device.rotated`, `device.revoked`, `external.revoked`. These go to the identity audit and the HMAC audit log. |

## 2. Devices

- **QR:** for a device, the response includes
  `gubern://pair?v=1&u=<server URL>&c=<code>[&fp=<TLS fingerprint>]`.
  `u` is `GUBERN_PUBLIC_URL`, or the address the request reached. `fp` comes
  from `GUBERN_API_TLS_FINGERPRINT`, and the mobile app pins it (milestone H).
- **Redeem:** `POST /api/pairing/redeem` takes
  `{code, label, public_key (base64 Ed25519, 32 bytes)}`. It is the only
  unauthenticated write, limited to 10 per minute (burst 5) per address. It
  registers the device with its public key, used for step-up in H, and
  returns the device's **own credential** once. That credential acts as the
  person, limited to the code's ceiling.
- **Default ceiling:**
  - `system:read`, `agents:read`, `agents:run`, `sessions:read`;
  - `approvals:read`, `approvals:decide`, `runs:read`, `devices:read`.

  The ceiling only removes scopes. A scope the creator doesn't hold is
  dropped when the code is created, so nobody can mint a device credential
  stronger than themselves.
- **Last seen:** recorded on use, at most once a minute.
- **Rotate:** `POST /api/devices/{id}/rotate` issues a new credential with
  the same person and ceiling and revokes the old one.
- **Revoke:** `DELETE /api/devices/{id}` revokes the device and all its
  keys. A credential of a revoked device is refused (401) even if its key
  were somehow still valid.
- **List:** `GET /api/devices` returns your own devices, or all of them with
  a `*` grant.

## 3. Channel accounts

1. A person creates a `telegram` (or `whatsapp`) code.
2. From the account they want linked, they send `/pair XXXX-XXXX-XXXX` to
   the bot.
   - On Telegram this works only in a **private chat**, where the chat id
     equals the sender id; a group member can't pair a group.
   - It works from any account: the code is the credential. That account's
     identity is the one the channel verified (Telegram's webhook secret,
     WhatsApp's signed webhook).
   - Attempts are limited to 5 per account per 10 minutes.
3. The bot replies with the outcome:

| Case | Result |
|---|---|
| The account is free | It is linked to the code's person. |
| The account is linked to **another** person | Refused ("linked to someone else"). The code is **not** consumed, so its owner can still use it from their own account. |
| The account is already linked to the same person | "Already linked". |
| A code made for another channel | Refused like any invalid code, and counted. |

Once linked:

- **Access:** the account's messages are accepted even if it is not on the
  channel's environment allow-list.
- **Approvals:** its decisions are authorized as the person (B's
  authorizer) and recorded under the person's principal id.
- **Revocation wins:** `DELETE /api/links/{provider}/{subject}` unlinks an
  account, and its channel then refuses it even if the allow-list still
  names it.
- **Listing:** `GET /api/links` returns your own links, or all of them with
  `*`.

## 4. Scopes

- `devices:read` and `devices:manage` are active from F.
- The operator template gets both on its own devices and links. The viewer
  template gets `devices:read` on its own. Owner and admin hold them on `*`.
- Templates apply when a principal is created. Principals created before F
  keep the grants they had.

## 5. Decision left to the founder

B's authorizer still accepts **unlinked** channel users who are on the
channel's allow-list. `GUBERN_REQUIRE_LINKED_APPROVERS=true` turns that off.
Now that pairing exists, turning it on is recommended.

It is not on by default, because that would silently stop approvals for
existing allow-list setups when they upgrade.

## 6. Not in F

- **Discord** codes are accepted by the store, but the bot arrives in I.
- **The mobile app**, secure storage and step-up signatures arrive in H.
- **No TLS on the management API:** it serves plain HTTP on 9090, so the QR
  has no fingerprint unless `GUBERN_API_TLS_FINGERPRINT` is set. G covers
  remote access.
