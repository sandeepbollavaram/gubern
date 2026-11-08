# Windows AppContainer sandbox: feasibility and decision

Milestone M, last item. Status: **researched and prototyped; not implemented
in production code.** Evidence: `internal/sandbox/appcontainer_spike_windows_test.go`
(opt-in, `GUBERN_APPCONTAINER_SPIKE=1`) and its run log,
`docs/design/evidence/appcontainer-spike-2026-11-08.txt`.

## 1. Executive summary

**Decision: IMPLEMENT** (as a separate milestone, M.8). The spike shows that an
AppContainer adds the file-system and network fence Windows commands lack
today, and that it fits Gubern's existing process launch without a custom
launcher or cgo:

- Go's `os/exec` launches a process in an AppContainer when it is given an
  AppContainer ("lowbox") token through `SysProcAttr.Token`. Go then calls
  `CreateProcessAsUser`, still with its explicit inherited-handle list. No
  privilege was needed.
- The existing Job Object adapter works unchanged on top: suspended start,
  assignment, tree kill, process cap and memory cap.
- Access is deny-by-default: the process reads and writes only folders
  granted to its AppContainer SID (plus what Windows grants all
  AppContainers, such as `Program Files` and `System32`, read-only).
- Network is deny-by-default; the `internetClient` capability opens the
  public internet, and loopback stays closed.
- A real `go build` ran inside the AppContainer and the job and produced a
  working binary.

What it does **not** give, and the implementation must handle (section 13):
destination-level network filtering, DNS on hosts whose resolver is on
loopback (VPN clients such as Cloudflare WARP), and Flutter (not tested).

## 2. Current Windows sandbox

`internal/sandbox/job_windows.go`: each command starts suspended, joins its
own Job Object, then resumes. The job kills the whole tree when the command
exits or times out and caps processes and memory. It does not fence files or
network (Info: "Contained (Windows)", network "not enforced").

## 3. AppContainer security model (as used here)

- **AppContainer SID** (`S-1-15-2-…`), from a profile
  (`CreateAppContainerProfile`, userenv.dll). A process's access checks use
  its AppContainer SID and capability SIDs in place of the user's groups, so
  an object is reachable only if its DACL grants the AppContainer SID, a
  capability SID, or `ALL APPLICATION PACKAGES`.
- **Lowbox token**: `NtCreateLowBoxToken` (ntdll) derives an AppContainer
  token from the caller's own token, with the package SID and chosen
  capabilities. Chromium's sandbox uses the same call. It is exported but
  not in the public SDK headers; the documented alternative is
  `CreateProcess` with `PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITIES`, which
  Go's `os/exec` can't express (it builds its own attribute list).
- **Capabilities used**: `internetClient` (`S-1-15-3-1`, public internet),
  `privateNetworkClientServer` (`S-1-15-3-3`, private ranges).
- **Child processes** inherit the token (E11, E12).
- **Handles**: Go passes only the standard handles; an inheritable handle in
  the parent was not usable in the child (E16), and AppContainer processes
  get strict handle checks (an invalid handle raises `0xc0000008`).
- **Registry and named objects**: not tested in the spike. AppContainers get
  their own object namespace and limited registry access; to verify in M.8.

## 4. Threat model

Addresses (with the Job Object): a malicious build script or dependency, or a
compromised tool, trying to read host secrets (SSH keys, browser profiles,
cloud credentials, other workspaces: denied unless granted, E5, E5c), modify
host files (E5b, E5d, E6), reach local services (loopback blocked, E10c),
reach the network at all when not granted (E9, E9b, E10g), leave processes
behind (E13), or exhaust processes or memory (E14a, E14b), or use inherited
handles (E16).

Outside the boundary: anything readable by all AppContainers (`Program
Files`, `System32`, some of `ProgramData`); the internet as a whole once
`internetClient` is granted (no destination filtering); kernel and driver
vulnerabilities; the content a build produces (a malicious artifact is still
malicious); and resources granted on purpose (the workspace, caches).
Arbitrary code execution is contained, not made safe.

## 5. File-system policy

| Path | Grant to the AppContainer SID | Evidence |
|---|---|---|
| Workspace (read) | `RX` | E3; write denied E4b |
| Build output folder | `M` | E4 |
| Private home: caches, `GOPATH`, `TMP`/`TEMP` | `M` | E7, E18 |
| Toolchain | none needed under `Program Files`; elsewhere `RX` on the toolchain folder | E18 (Program Files), E6 (write denied) |
| Anything else | none (denied) | E5–E5d |

Grants are ACEs added to the folders' DACLs (`icacls` in the spike;
`SetEntriesInAcl` in M.8). They persist until removed, so M.8 uses one
profile per workspace, grants at first use and records them, and removes them
when the workspace is removed. Traversal into a granted folder worked without
granting its parents (the token keeps bypass-traverse checking), while
listing an ungranted parent was denied (E5c). Symlinks and junctions resolve
to their targets, whose own DACLs apply. Untested: reparse points created
inside a writable folder pointing out (expected denied by the target's DACL).

## 6. Network policy

| Policy | Token capabilities | Result | Evidence |
|---|---|---|---|
| No network | none | connects refused by the OS | E9b, E10g |
| Dependency downloads | `internetClient` | public internet; DNS through a public resolver works | E8b, E8c, E10f |
| Local network | `privateNetworkClientServer` | private ranges (not tested: no LAN target) | unverified |
| Host services (loopback) | none possible without an admin `CheckNetIsolation LoopbackExempt` | blocked | E10c |

AppContainer filters by category, not destination. Allow-lists need a proxy
the box can reach; loopback is closed, so a filtering proxy must listen on a
non-loopback interface (and the box needs `privateNetworkClientServer`), or
the operator adds a loopback exemption. Cloud metadata endpoints
(`169.254.169.254`) and link-local behaviour were not tested.

**DNS caveat found in the spike:** on this machine the resolvers are
`127.0.2.2/3` (Cloudflare WARP). The system resolver then fails inside the box
because loopback is closed (E8, E10b, E10d, E10e fail), while resolution
through `1.1.1.1:53` works (E10f). M.8 must detect a loopback resolver and
either set a public resolver for builds or report network as unavailable,
not silently fail.

## 7. Process launch

Use `os/exec` unchanged, with `SysProcAttr.Token` set to the lowbox token
(E2). Standard input/output, cancellation (`cmd.Cancel`), `WaitDelay` and
errors behave as before (E13, E17). No cgo. New Windows-only code: profile
create/derive/delete (userenv.dll), `NtCreateLowBoxToken`, and DACL grants
(x/sys/windows). Estimated 300–400 lines plus tests.

## 8. Job Object integration

The current adapter composes: `Prepare` adds `CREATE_SUSPENDED`, `Started`
assigns the AppContainer process to the job and resumes it (E12: in the job
and in the AppContainer), the tree dies on timeout (E13), caps hold (E14a,
E14b). AppContainer handles access; the job handles lifecycle and resources.

## 9. Go builds

Verified: `go build` with the toolchain in `Program Files`, `GOCACHE`,
`GOPATH`, `TMP` and the module in granted folders, `GOTOOLCHAIN=local`,
`GOENV=off`, no proxy (E18; 24.7 s with a cold cache; the binary runs).
Not verified: module downloads through the proxy (needs DNS, see the caveat)
and a toolchain outside `Program Files` (needs an `RX` grant, as E3).

## 10. Flutter builds

Not tested. Flutter writes into its SDK (`bin/cache`) and Gradle into
`~/.gradle`; an isolated Flutter build needs `M` on the SDK or a per-build
SDK copy, plus a private Gradle home and the Android SDK readable. That is a
separate profile and is not claimed. Until verified, Flutter keeps the
current "Contained (Windows)" job only, and Go builds don't inherit any
Flutter allowances.

## 11. Failure handling

Profile, token or grant failures fail the command closed (no fallback to a
weaker box in `required` mode; `auto` falls back to the job and the Info
says so). Launch failures return an error and leave no process (E17). A run
that dies mid-way leaves its grants; M.8 reconciles grants at start-up.

## 12. Performance

Profile creation and token derivation are per workspace and per run,
milliseconds. The cold `go build` took 24.7 s, comparable to a cold build
outside; no per-file overhead was measured. Profiles are small folders under
`%LOCALAPPDATA%\Packages`.

## 13. Security limitations

- No destination filtering (section 6).
- Loopback-resolver hosts break DNS (section 6).
- `ALL APPLICATION PACKAGES` read access to system folders is outside our
  control.
- `NtCreateLowBoxToken` is not in the public headers (stable since Windows 8
  and used by Chromium, but undocumented).
- Grants are persistent DACL changes on the user's folders; they must be
  tracked and removed.
- Registry, named pipes, COM and metadata-endpoint behaviour untested.

## 14. Alternatives considered

- **Job Object only** (today): lifecycle and caps, no access fence.
- **Custom launcher with `PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITIES`**:
  documented, but reimplements `os/exec` (pipes, handle list, waits).
  Unnecessary given E2.
- **Restricted token / low integrity**: blocks writes to medium-integrity
  objects but not reads, so secrets stay readable.
- **Windows Sandbox / Hyper-V containers**: stronger, but heavy, edition
  dependent and slow to start.
- **Separate local user account**: needs account creation and credentials.

## 15. Experiment results

Windows 10.0 build 26300 (Windows 11 Home), Go 1.26.10, unprivileged user,
disposable folders under `D:\tmp\vortex-go`. Full log:
`evidence/appcontainer-spike-2026-11-08.txt`.

| ID | Experiment | Result |
|---|---|---|
| E1 | Create profile, get SID | pass |
| E2 | Launch via `os/exec` + lowbox token; token is AppContainer | pass |
| E3 / E4 / E4b | Read workspace / write output / write read-only workspace denied | pass |
| E5 / E5b / E5c / E5d | Read and write unrelated folder, list parent, write user TEMP: all denied | pass |
| E6 | Write into toolchain folder denied | pass |
| E7 | Private cache writable | pass |
| E8 / E10b / E10d / E10e / E8d | Name resolution through the system resolver | **fail on this host** (loopback DNS, see section 6) |
| E8b / E8c | `internetClient`: connect to a public IP | pass |
| E9 / E9b / E10g | No capability: no DNS, no connect | pass |
| E10f | `internetClient`: DNS via 1.1.1.1 | pass |
| E10c | Loopback to a host service | blocked (pass) |
| E11 | Child processes | pass |
| E12 | Job assignment of an AppContainer process | pass |
| E13 | Tree killed on timeout | pass |
| E14a / E14b | Process cap / memory cap | pass |
| E16 | Inheritable parent handle not usable in child | pass |
| E17 | Launch failure is clean | pass |
| E18 / E18b | Real `go build` in AppContainer + job; binary runs | pass |
| — | LAN with `privateNetworkClientServer`, metadata endpoint, registry, Flutter, module download | **unverified** |

## 16. Implementation recommendation (M.8)

1. `internal/sandbox/appcontainer_windows.go`: profile per workspace
   (`gubern.ws.<hash>`), lowbox token per command with capabilities from the
   run's network (`off` → none, `on` → `internetClient`), grants (workspace
   `RX` or `M` per tool, output `M`, private home `M`) recorded in the
   server's state and removed with the workspace.
2. Compose with the Job adapter; Info becomes "Isolated (Windows)", with
   network enforced as on/off and destination filtering listed as not
   enforced.
3. Detect loopback DNS; for builds with network on, point Go at a public
   resolver or report the limitation.
4. Move the spike experiments into a Windows escape suite mirroring
   `bwrap_linux_test.go`.
5. Keep Flutter on the job only until a Flutter profile is verified.

## 17. Rollout

Behind `agents.local_tools.sandbox.windows: "job" | "appcontainer"`,
default `job` for one release; switch the default after the escape suite has
run on Windows CI and on at least one real workstation with a VPN resolver.

## 18. Test strategy

The Windows escape suite runs every experiment above as a test with a
control (the same action allowed where it should be), in CI on
`windows-latest`, plus: reparse points created inside a writable folder,
registry writes, named pipes to host services, LAN access with and without
`privateNetworkClientServer`, and grant cleanup after a crash.
