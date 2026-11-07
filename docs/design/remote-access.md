# Remote access (Milestone G, design only)

Status: **design for founder review.** No infrastructure is changed by
this milestone, and nothing here is deployed. The founder has not
authorized any relay, domain or public exposure.

## 1. Problem

The phone (milestone H) and the dashboard away from the desk need to reach
the Gubern management API. Approvals, run streams and pairing all go through
it, and it is the most powerful surface Gubern has.

How it works today:

- It serves **plain HTTP** on `:9090`.
- By default it listens on **all interfaces**, unless `GUBERN_API_LISTEN`
  narrows it (the test harness uses `127.0.0.1:9090`).
- Loopback trust gives `local-loopback` 8 control-plane routes without a key
  (A §6).

Goal: reach the API from a person's own devices, without exposing it to the
internet and without weakening A, B, D or F.

## 2. Requirements

1. **Confidentiality and integrity in transit.** API keys, device
   credentials, approvals and run output never cross a network in clear
   text.
2. **Server authentication.** The device knows it is talking to *this*
   Gubern. Pairing gives it a TLS fingerprint to pin (F, H).
3. **No new trust.** Remote callers are authenticated and authorized exactly
   like local ones: key → principal → scopes. Loopback trust never applies
   to remote traffic.
4. **No public admin API.** The management API is never reachable from the
   open internet by default.
5. **Survives NAT and IP changes** for the common home and office case.
6. **Revocable.** Losing a phone is handled by revoking the device (F). No
   network-level secret has to be rotated.
7. **Small footprint.** No new always-on service operated by the Gubern
   project unless the founder chooses one (§5).

## 3. Options

### 3.1 Same LAN, with TLS on the management API

The phone talks to `https://<host>.local:9443` on the home or office
network.

What is needed:

- TLS on the management API, using Gubern's existing **internal CA** (the
  `tls.provider: "internal"` machinery already serves edge routes).
- A listener on the LAN interface. The SHA-256 fingerprint of the API's
  leaf certificate (or of the internal CA) goes into the pairing QR
  (`fp=`), and the app pins it.
- Optional mDNS advertisement.

| | |
|---|---|
| **For** | No third party, no internet path, lowest latency. The pinning design (H) already assumes this. |
| **Against** | Works only on that network. Every device on the LAN can reach the port, so A's authorization is the only barrier there, as it is today on `:9090`. |
| **Verdict** | **Do first.** Needed by every other option, because TLS end to end to Gubern is required regardless of the transport. |

### 3.2 Private VPN mesh: Tailscale, or WireGuard you run yourself

The server and the phone join one private network. The API listens only on
the VPN interface, for example `100.x.y.z:9443` on Tailscale.

| | |
|---|---|
| **For** | Works from anywhere, through NAT. The port is invisible to the internet and to the local LAN. Losing a phone means removing it from the tailnet as well as revoking it in Gubern. WireGuard you run yourself has no third party at all. |
| **Against** | Tailscale is a third-party coordination service (it sees metadata, not traffic). WireGuard you run yourself needs one reachable endpoint and some networking skill. The person installs a VPN app on the phone. |
| **Verdict** | **Recommended default for remote access.** Document it as the supported way; Gubern only needs to bind to an interface or address the operator chooses. |

What Gubern needs:

- Allow `GUBERN_API_LISTEN` to name a VPN address (it already can), and
  allow more than one listener (LAN + VPN), still with TLS.
- Keep loopback trust for `127.0.0.1` only. Tailscale's `100.64/10` is not
  loopback, so this already holds.

### 3.3 Relay (outbound tunnel from Gubern)

Gubern keeps an outbound connection to a relay, and the phone connects to
the relay. There are two variants:

- **Run by the Gubern project:** a hosted relay at a Gubern domain.
- **Run by the user:** they host the relay themselves.

| | |
|---|---|
| **For** | No VPN app, works anywhere, the experience most like Claude's own app. |
| **Against** | A relay is a high-value target: every user's control channel crosses it. It needs end-to-end encryption **through** the relay (TLS terminated in Gubern, not at the relay), abuse handling, uptime and an incident process. A project-run relay is a new service, with its domain, operations, cost and legal duties. |
| **Verdict** | **Not now.** If ever built, the relay must: <br>(a) forward opaque TLS, e.g. by SNI routing, and never hold keys or see plaintext; <br>(b) authenticate the Gubern instance with a per-instance key created at pairing; <br>(c) rate-limit and refuse unknown instances; <br>(d) be optional and off by default. <br>Needs founder authorization (domain, deployment). |

### 3.4 Third-party edge tunnel (Cloudflare Tunnel, ngrok)

A tunnel publishes a public hostname that forwards to Gubern.

| | |
|---|---|
| **For** | Quick to set up and works anywhere. |
| **Against** | It publishes the admin API on a public hostname, against requirement 4. TLS usually terminates at the edge provider, which then sees all plaintext (keys, approvals, output) unless end-to-end TLS is forced through it. Edge access rules add a second identity layer that can drift from Gubern's. Proxied requests also carry forwarding headers (fine: F-06 makes them never loopback-trusted). |
| **Verdict** | **Not recommended.** If an operator insists, document it as unsupported, with Gubern's own key auth still required and the edge's access control (e.g. Cloudflare Access) as an *extra* layer, never a replacement. |

### 3.5 Port-forward the API to the internet

**Rejected.** It fails requirement 4 outright.

## 4. Recommendation

1. **G1, next code step:** TLS on the management API with the internal CA.
   - A second listener, `GUBERN_API_TLS_LISTEN` (e.g. `0.0.0.0:9443` or a VPN
     address).
   - The fingerprint is published to pairing (`fp=`) automatically.
   - Plain HTTP stays on `127.0.0.1:9090` for the local CLI, TUI and
     loopback trust.
   - **Change of default:** plain HTTP binds to loopback only unless
     `GUBERN_API_LISTEN` says otherwise. Today it binds every interface,
     which this design treats as a risk.
2. **G2, documented setup:** Tailscale or WireGuard as the supported remote
   path. The operator binds the TLS listener to the VPN address. A runbook
   page covers it; there is no code beyond G1.
3. **G3, later, needs authorization:** decide whether to offer a relay at
   all, and if so whether it is project-run or user-hosted, built to the
   constraints in §3.3.

## 5. Decisions for the founder

| # | Decision | Recommendation |
|---|---|---|
| G-D1 | Bind plain HTTP to loopback by default (a change of default) | Yes, with a release note. |
| G-D2 | Add the TLS listener with internal-CA certificates and fingerprint pinning (G1) | Yes; it is needed for H. |
| G-D3 | Make VPN the supported remote path (G2) | Yes. |
| G-D4 | Build a relay, and who runs it (G3) | Not now. Revisit after H ships on LAN + VPN. |
| G-D5 | Allow third-party edge tunnels | Document as unsupported. |

## 6. What stays the same under every option

- **Authentication and scopes:** every request is authenticated by key or
  device credential, and authorized by scopes and resource ownership (A).
- **Loopback trust** applies only to a direct, unforwarded `127.0.0.1`
  connection.
- **Approvals** are decided through the one service (B), and runs stream
  redacted events (D).
- **Losing a device** is handled by revoking it (F). No shared network
  secret exists to leak.
