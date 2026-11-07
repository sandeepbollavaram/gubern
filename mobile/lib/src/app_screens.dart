import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:mobile_scanner/mobile_scanner.dart';

import 'api.dart';
import 'credential_store.dart';
import 'pairing_link.dart';
import 'stepup.dart';
import 'ui/brand.dart';
import 'ui/theme.dart';
import 'ui/widgets.dart';

// ---------------------------------------------------------------------------
// Pairing
// ---------------------------------------------------------------------------

/// First run: what Gubern on the phone does, then pair by scanning the
/// dashboard's code or entering its link.
class PairScreen extends StatefulWidget {
  const PairScreen({super.key, required this.store, required this.deviceKey, required this.onPaired});
  final CredentialStore store;
  final DeviceKey deviceKey;
  final void Function(GubernApi) onPaired;

  @override
  State<PairScreen> createState() => _PairScreenState();
}

class _PairScreenState extends State<PairScreen> {
  bool _busy = false;
  String? _error;

  Future<void> _pair(String raw, String label) async {
    setState(() => _error = null);
    PairingLink link;
    try {
      link = PairingLink.parse(raw);
    } on FormatException catch (e) {
      setState(() => _error = e.message);
      return;
    }
    if (link.insecure && !(await _confirmInsecure(link))) return;
    setState(() => _busy = true);
    try {
      final creds = await GubernApi.pair(link, widget.deviceKey, widget.store, label: label.trim().isEmpty ? 'My phone' : label.trim());
      HapticFeedback.mediumImpact();
      widget.onPaired(GubernApi(creds, widget.store));
    } catch (e) {
      setState(() => _error = _friendly(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  String _friendly(Object e) {
    final s = e.toString();
    if (s.contains('invalid, used or expired')) return 'That code is invalid, already used, or expired. Create a new one in the dashboard.';
    if (s.contains('Connection refused') || s.contains('SocketException')) {
      return 'Can\'t reach the Gubern server. Check that the phone and the server are on the same network.';
    }
    if (s.contains('Handshake')) return 'The server\'s certificate doesn\'t match the code. Pairing was stopped to protect you.';
    return 'Pairing failed: $s';
  }

  Future<bool> _confirmInsecure(PairingLink link) async {
    final ok = await showModalBottomSheet<bool>(
      context: context,
      builder: (c) {
        final g = c.g;
        return SafeArea(
          child: Padding(
            padding: const EdgeInsets.fromLTRB(24, 0, 24, 24),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Container(
                  padding: const EdgeInsets.all(12),
                  decoration: BoxDecoration(color: g.warn.withValues(alpha: .14), borderRadius: BorderRadius.circular(16)),
                  child: Icon(Icons.lock_open_rounded, color: g.warn, size: 28),
                ),
                const SizedBox(height: 16),
                Text('Unencrypted connection', style: Theme.of(c).textTheme.titleLarge),
                const SizedBox(height: 8),
                Text(
                  '${link.server.host} doesn\'t use https, so this phone\'s traffic to it isn\'t encrypted. Pair only on a network you trust, such as your home Wi-Fi or a private VPN.',
                  style: TextStyle(color: g.muted, height: 1.5),
                ),
                const SizedBox(height: 24),
                // The safe choice is the prominent one.
                GradientButton(label: 'Cancel', onPressed: () => Navigator.pop(c, false)),
                const SizedBox(height: 10),
                OutlinedButton.icon(
                  style: OutlinedButton.styleFrom(
                    foregroundColor: g.warn,
                    side: BorderSide(color: g.warn.withValues(alpha: .6)),
                  ),
                  onPressed: () => Navigator.pop(c, true),
                  icon: const Icon(Icons.lock_open_rounded, size: 20),
                  label: const Text('Pair anyway'),
                ),
              ],
            ),
          ),
        );
      },
    );
    return ok == true;
  }

  Future<void> _scan() async {
    final raw = await Navigator.push<String>(context, MaterialPageRoute(builder: (_) => const ScannerScreen()));
    if (raw != null && mounted) await _pair(raw, 'My phone');
  }

  Future<void> _enterLink() async {
    final result = await showModalBottomSheet<(String, String)>(context: context, isScrollControlled: true, builder: (c) => const _LinkSheet());
    if (result != null && mounted) await _pair(result.$1, result.$2);
  }

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final t = Theme.of(context).textTheme;
    return Scaffold(
      body: AuroraBackground(
        child: SafeArea(
          child: LayoutBuilder(
            builder: (context, c) => SingleChildScrollView(
              padding: const EdgeInsets.symmetric(horizontal: 24),
              child: ConstrainedBox(
                constraints: BoxConstraints(minHeight: c.maxHeight),
                child: IntrinsicHeight(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      const SizedBox(height: 40),
                      const GubernMark(size: 64, glow: true),
                      const SizedBox(height: 28),
                      Text('Your agents,\nin your pocket.', style: t.headlineLarge),
                      const SizedBox(height: 12),
                      Text(
                        'See what your Gubern agents want to do, and approve it with your fingerprint. Nothing runs without you.',
                        style: TextStyle(color: g.muted, fontSize: 16, height: 1.5),
                      ),
                      const SizedBox(height: 32),
                      const _Step(
                        n: 1,
                        icon: Icons.devices_rounded,
                        title: 'Open Devices in the dashboard',
                        body: 'Choose "Pair a phone" to show a one-time code.',
                      ),
                      const _Step(n: 2, icon: Icons.qr_code_scanner_rounded, title: 'Scan the code', body: 'It works once and expires in ten minutes.'),
                      const _Step(
                        n: 3,
                        icon: Icons.fingerprint_rounded,
                        title: 'Approve with your fingerprint',
                        body: 'A key in this phone\'s secure hardware signs every approval.',
                        last: true,
                      ),
                      const Spacer(),
                      if (_error != null) ...[Notice(text: _error!, color: g.critical, icon: Icons.error_outline_rounded), const SizedBox(height: 16)],
                      GradientButton(label: 'Scan pairing code', icon: Icons.qr_code_scanner_rounded, busy: _busy, onPressed: _scan),
                      const SizedBox(height: 10),
                      OutlinedButton.icon(
                        onPressed: _busy ? null : _enterLink,
                        icon: const Icon(Icons.link_rounded, size: 20),
                        label: const Text('Enter the link instead'),
                      ),
                      const SizedBox(height: 24),
                    ],
                  ),
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class _Step extends StatelessWidget {
  const _Step({required this.n, required this.icon, required this.title, required this.body, this.last = false});
  final int n;
  final IconData icon;
  final String title, body;
  final bool last;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    return IntrinsicHeight(
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Column(
            children: [
              Container(
                width: 40,
                height: 40,
                decoration: BoxDecoration(
                  color: g.surfaceHigh,
                  borderRadius: BorderRadius.circular(12),
                  border: Border.all(color: g.border),
                ),
                child: Icon(icon, size: 20, color: g.brand),
              ),
              if (!last)
                Expanded(
                  child: Container(width: 1.5, margin: const EdgeInsets.symmetric(vertical: 4), color: g.border),
                ),
            ],
          ),
          const SizedBox(width: 14),
          Expanded(
            child: Padding(
              padding: EdgeInsets.only(top: 2, bottom: last ? 0 : 18),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(title, style: const TextStyle(fontSize: 16, fontWeight: FontWeight.w600)),
                  const SizedBox(height: 2),
                  Text(body, style: TextStyle(color: g.muted, height: 1.4)),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _LinkSheet extends StatefulWidget {
  const _LinkSheet();
  @override
  State<_LinkSheet> createState() => _LinkSheetState();
}

class _LinkSheetState extends State<_LinkSheet> {
  final _link = TextEditingController();
  final _name = TextEditingController(text: 'My phone');

  @override
  void dispose() {
    _link.dispose();
    _name.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: EdgeInsets.fromLTRB(24, 0, 24, 24 + MediaQuery.of(context).viewInsets.bottom),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('Pair with a link', style: Theme.of(context).textTheme.titleLarge),
          const SizedBox(height: 6),
          Text('Copy the link shown under the code in the dashboard.', style: TextStyle(color: context.g.muted)),
          const SizedBox(height: 20),
          TextField(
            controller: _link,
            autofocus: true,
            autocorrect: false,
            enableSuggestions: false,
            style: mono(context, size: 14),
            decoration: InputDecoration(
              labelText: 'Pairing link',
              hintText: 'gubern://pair?…',
              prefixIcon: const Icon(Icons.link_rounded),
              suffixIcon: IconButton(
                tooltip: 'Paste',
                icon: const Icon(Icons.content_paste_rounded),
                onPressed: () async {
                  final d = await Clipboard.getData('text/plain');
                  if (d?.text != null) _link.text = d!.text!.trim();
                },
              ),
            ),
          ),
          const SizedBox(height: 12),
          TextField(
            controller: _name,
            decoration: const InputDecoration(labelText: 'Name for this phone', prefixIcon: Icon(Icons.smartphone_rounded)),
          ),
          const SizedBox(height: 20),
          GradientButton(label: 'Pair', icon: Icons.link_rounded, onPressed: () => Navigator.pop(context, (_link.text, _name.text))),
        ],
      ),
    );
  }
}

/// Full-screen camera with a framing guide.
class ScannerScreen extends StatefulWidget {
  const ScannerScreen({super.key});
  @override
  State<ScannerScreen> createState() => _ScannerScreenState();
}

class _ScannerScreenState extends State<ScannerScreen> {
  bool _done = false;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    return Scaffold(
      backgroundColor: Colors.black,
      body: Stack(
        children: [
          MobileScanner(
            onDetect: (capture) {
              final v = capture.barcodes.firstOrNull?.rawValue;
              if (!_done && v != null && v.startsWith('gubern://')) {
                _done = true;
                HapticFeedback.lightImpact();
                Navigator.pop(context, v);
              }
            },
          ),
          Center(
            child: Container(
              width: 260,
              height: 260,
              decoration: BoxDecoration(
                borderRadius: BorderRadius.circular(28),
                border: Border.all(color: g.accent, width: 3),
              ),
            ),
          ),
          SafeArea(
            child: Padding(
              padding: const EdgeInsets.all(16),
              child: Column(
                children: [
                  Row(
                    children: [IconButton.filledTonal(tooltip: 'Close', onPressed: () => Navigator.pop(context), icon: const Icon(Icons.close_rounded))],
                  ),
                  const Spacer(),
                  Container(
                    padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
                    decoration: BoxDecoration(color: Colors.black.withValues(alpha: .6), borderRadius: BorderRadius.circular(99)),
                    child: const Text('Point at the code in the Gubern dashboard', style: TextStyle(color: Colors.white)),
                  ),
                  const SizedBox(height: 32),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Home: what needs you
// ---------------------------------------------------------------------------

class ApprovalsScreen extends StatefulWidget {
  const ApprovalsScreen({super.key, required this.api, required this.deviceKey, required this.onUnpaired});
  final GubernApi api;
  final DeviceKey deviceKey;
  final VoidCallback onUnpaired;

  @override
  State<ApprovalsScreen> createState() => _ApprovalsScreenState();
}

class _ApprovalsScreenState extends State<ApprovalsScreen> {
  List<ApprovalView> _items = const [];
  String? _error;
  bool _loaded = false;
  DateTime? _updated;
  Timer? _poll;

  @override
  void initState() {
    super.initState();
    unawaited(_refresh());
    _poll = Timer.periodic(const Duration(seconds: 5), (_) => unawaited(_refresh()));
  }

  @override
  void dispose() {
    _poll?.cancel();
    super.dispose();
  }

  Future<void> _refresh() async {
    try {
      final items = await widget.api.pendingApprovals();
      items.sort((a, b) => a.expiresAt.compareTo(b.expiresAt));
      if (mounted) setState(() => (_items = items, _error = null, _loaded = true, _updated = DateTime.now()));
    } on RevokedException {
      widget.onUnpaired();
    } catch (e) {
      if (mounted) setState(() => (_error = 'Can\'t reach Gubern right now. Retrying…', _loaded = true));
    }
  }

  Future<void> _settings() async {
    final unpair = await showModalBottomSheet<bool>(
      context: context,
      builder: (_) => _SettingsSheet(api: widget.api),
    );
    if (unpair == true) {
      await widget.api.store.wipe();
      await widget.deviceKey.delete();
      widget.onUnpaired();
    }
  }

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final t = Theme.of(context).textTheme;
    final host = widget.api.creds?.server.host ?? '';
    return Scaffold(
      body: RefreshIndicator(
        color: g.brand,
        onRefresh: _refresh,
        child: CustomScrollView(
          physics: const AlwaysScrollableScrollPhysics(),
          slivers: [
            SliverSafeArea(
              bottom: false,
              sliver: SliverPadding(
                padding: const EdgeInsets.fromLTRB(20, 12, 12, 0),
                sliver: SliverToBoxAdapter(
                  child: Row(
                    children: [
                      const GubernMark(size: 30),
                      const SizedBox(width: 10),
                      Text('Gubern', style: t.titleLarge),
                      const Spacer(),
                      IconButton(
                        tooltip: 'Settings',
                        onPressed: _settings,
                        icon: Icon(Icons.tune_rounded, color: g.muted),
                      ),
                    ],
                  ),
                ),
              ),
            ),
            SliverPadding(
              padding: const EdgeInsets.fromLTRB(20, 20, 20, 0),
              sliver: SliverToBoxAdapter(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Row(
                      crossAxisAlignment: CrossAxisAlignment.end,
                      children: [
                        Text('Needs you', style: t.headlineMedium),
                        const SizedBox(width: 10),
                        if (_items.isNotEmpty)
                          Container(
                            margin: const EdgeInsets.only(bottom: 6),
                            padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 2),
                            decoration: BoxDecoration(gradient: g.brandGradient, borderRadius: BorderRadius.circular(99)),
                            child: Text(
                              '${_items.length}',
                              style: const TextStyle(color: Colors.white, fontWeight: FontWeight.w700),
                            ),
                          ),
                      ],
                    ),
                    const SizedBox(height: 14),
                    _StatusStrip(host: host, ok: _error == null, updated: _updated),
                    if (_error != null) ...[const SizedBox(height: 12), Notice(text: _error!, color: g.warn, icon: Icons.cloud_off_rounded)],
                  ],
                ),
              ),
            ),
            if (!_loaded)
              const SliverFillRemaining(hasScrollBody: false, child: Center(child: CircularProgressIndicator()))
            else if (_items.isEmpty)
              SliverFillRemaining(
                hasScrollBody: false,
                child: Padding(
                  padding: const EdgeInsets.all(32),
                  child: Column(
                    mainAxisAlignment: MainAxisAlignment.center,
                    children: [
                      const AllClearArt(),
                      const SizedBox(height: 24),
                      Text('All clear', style: t.titleLarge),
                      const SizedBox(height: 6),
                      Text(
                        'Nothing is waiting for you. When an agent wants to do something risky, it shows up here.',
                        textAlign: TextAlign.center,
                        style: TextStyle(color: g.muted, height: 1.5),
                      ),
                    ],
                  ),
                ),
              )
            else
              SliverPadding(
                padding: const EdgeInsets.fromLTRB(20, 18, 20, 32),
                sliver: SliverList.separated(
                  itemCount: _items.length,
                  separatorBuilder: (_, _) => const SizedBox(height: 12),
                  itemBuilder: (context, i) => _ApprovalCard(
                    a: _items[i],
                    onTap: () async {
                      await Navigator.push(
                        context,
                        MaterialPageRoute(
                          builder: (_) => ApprovalDetailScreen(api: widget.api, deviceKey: widget.deviceKey, approval: _items[i]),
                        ),
                      );
                      unawaited(_refresh());
                    },
                  ),
                ),
              ),
          ],
        ),
      ),
    );
  }
}

class _StatusStrip extends StatelessWidget {
  const _StatusStrip({required this.host, required this.ok, required this.updated});
  final String host;
  final bool ok;
  final DateTime? updated;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
      decoration: BoxDecoration(
        color: g.surface,
        borderRadius: BorderRadius.circular(16),
        border: Border.all(color: g.border),
      ),
      child: Row(
        children: [
          LiveDot(ok: ok),
          const SizedBox(width: 10),
          Expanded(
            child: Text(
              ok ? 'Connected to $host' : 'Reconnecting to $host',
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(fontWeight: FontWeight.w500),
            ),
          ),
          Icon(Icons.verified_user_rounded, size: 16, color: g.brand),
          const SizedBox(width: 4),
          Text('Key in hardware', style: TextStyle(color: g.muted, fontSize: 12)),
        ],
      ),
    );
  }
}

class _ApprovalCard extends StatelessWidget {
  const _ApprovalCard({required this.a, required this.onTap});
  final ApprovalView a;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final rc = g.risk(a.risk);
    return ClipRRect(
      borderRadius: BorderRadius.circular(20),
      child: Material(
        color: g.surface,
        child: InkWell(
          borderRadius: BorderRadius.circular(20),
          onTap: onTap,
          child: Container(
            decoration: BoxDecoration(
              borderRadius: BorderRadius.circular(20),
              border: Border.all(color: a.highRisk ? rc.withValues(alpha: .5) : g.border),
            ),
            child: IntrinsicHeight(
              child: Row(
                children: [
                  Container(
                    width: 5,
                    decoration: BoxDecoration(
                      color: rc,
                      borderRadius: const BorderRadius.horizontal(left: Radius.circular(20)),
                    ),
                  ),
                  Expanded(
                    child: Padding(
                      padding: const EdgeInsets.fromLTRB(14, 14, 10, 14),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Row(
                            children: [
                              RiskBadge(a.risk),
                              const Spacer(),
                              Countdown(a.expiresAt, style: const TextStyle(fontSize: 13)),
                            ],
                          ),
                          const SizedBox(height: 10),
                          Text(
                            a.description.isEmpty ? a.tool : a.description,
                            maxLines: 2,
                            overflow: TextOverflow.ellipsis,
                            style: const TextStyle(fontSize: 16, fontWeight: FontWeight.w600, height: 1.35),
                          ),
                          const SizedBox(height: 6),
                          Row(
                            children: [
                              Icon(_toolIcon(a.tool), size: 15, color: g.muted),
                              const SizedBox(width: 5),
                              Flexible(
                                child: Text(
                                  '${a.tool} · ${a.sessionId}',
                                  overflow: TextOverflow.ellipsis,
                                  style: TextStyle(color: g.muted, fontSize: 13),
                                ),
                              ),
                            ],
                          ),
                        ],
                      ),
                    ),
                  ),
                  Icon(Icons.chevron_right_rounded, color: g.muted),
                  const SizedBox(width: 6),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}

IconData _toolIcon(String tool) => switch (tool) {
  'run_terminal' || 'run_command' => Icons.terminal_rounded,
  'write_file' || 'edit_file' => Icons.edit_document,
  'git_commit' || 'git_add' => Icons.commit_rounded,
  'devops' => Icons.dns_rounded,
  _ => Icons.extension_rounded,
};

class _SettingsSheet extends StatelessWidget {
  const _SettingsSheet({required this.api});
  final GubernApi api;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final c = api.creds;
    return SafeArea(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(24, 0, 24, 24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('This phone', style: Theme.of(context).textTheme.titleLarge),
            const SizedBox(height: 12),
            if (c != null) ...[
              InfoRow(Icons.dns_rounded, 'Server', c.server.toString()),
              InfoRow(Icons.smartphone_rounded, 'Device', c.deviceId, monoValue: true),
              InfoRow(
                c.fingerprint != null ? Icons.lock_rounded : Icons.lock_open_rounded,
                'Connection',
                c.fingerprint != null ? 'Encrypted, certificate pinned' : 'Not encrypted (trusted network only)',
              ),
              const InfoRow(Icons.key_rounded, 'Signing key', 'In this phone\'s secure hardware; asks for your fingerprint or PIN'),
            ],
            const SizedBox(height: 20),
            OutlinedButton.icon(
              style: OutlinedButton.styleFrom(
                foregroundColor: g.critical,
                side: BorderSide(color: g.critical.withValues(alpha: .6)),
              ),
              onPressed: () async {
                final sure = await showDialog<bool>(
                  context: context,
                  builder: (d) => AlertDialog(
                    backgroundColor: g.surface,
                    title: const Text('Unpair this phone?'),
                    content: const Text('Its key is deleted. You\'ll need a new code from the dashboard to pair again.'),
                    actions: [
                      TextButton(onPressed: () => Navigator.pop(d, false), child: const Text('Cancel')),
                      TextButton(
                        onPressed: () => Navigator.pop(d, true),
                        child: Text('Unpair', style: TextStyle(color: g.critical)),
                      ),
                    ],
                  ),
                );
                if (sure == true && context.mounted) Navigator.pop(context, true);
              },
              icon: const Icon(Icons.link_off_rounded),
              label: const Text('Unpair this phone'),
            ),
          ],
        ),
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Approval detail
// ---------------------------------------------------------------------------

/// One approval in full: what runs, where, the action fingerprint and the
/// deadline. Approving signs a fresh server challenge with the hardware key
/// (fingerprint or PIN). High and critical risk need a slide, so a stray tap
/// can never approve.
class ApprovalDetailScreen extends StatefulWidget {
  const ApprovalDetailScreen({super.key, required this.api, required this.deviceKey, required this.approval});
  final GubernApi api;
  final DeviceKey deviceKey;
  final ApprovalView approval;

  @override
  State<ApprovalDetailScreen> createState() => _ApprovalDetailScreenState();
}

class _ApprovalDetailScreenState extends State<ApprovalDetailScreen> {
  bool _busy = false;
  ({bool ok, String title, String body})? _result;

  Future<void> _decide(bool approve) async {
    final a = widget.approval;
    setState(() => _busy = true);
    try {
      final done = approve ? await widget.api.approve(a, widget.deviceKey) : await widget.api.reject(a);
      HapticFeedback.mediumImpact();
      final approved = done.status == 'approved';
      setState(
        () => _result = (
          ok: true,
          title: approved ? 'Approved' : 'Rejected',
          body: approved ? 'Signed by this phone and recorded as you. The agent is continuing.' : 'Nothing was run. The agent was told no.',
        ),
      );
    } on ApiException catch (e) {
      setState(
        () => _result = (
          ok: false,
          title: 'Not decided',
          body: switch (e.reason) {
            'not-pending' => 'Someone already decided this elsewhere.',
            'expired' => 'This request expired, so nothing was run.',
            'action-mismatch' => 'The action changed since you opened it. Go back and review it again.',
            _ => e.message,
          },
        ),
      );
    } catch (e) {
      final cancelled = e.toString().contains('cancelled');
      setState(
        () => _result = (
          ok: false,
          title: cancelled ? 'Cancelled' : 'Not decided',
          body: cancelled ? 'You cancelled the fingerprint check. Nothing was decided.' : '$e',
        ),
      );
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final a = widget.approval;
    final g = context.g;
    final rc = g.risk(a.risk);
    final expired = a.expiresAt.isBefore(DateTime.now());
    return Scaffold(
      appBar: AppBar(title: const Text('Review action')),
      body: ListView(
        padding: const EdgeInsets.fromLTRB(20, 4, 20, 24),
        children: [
          // Risk hero
          Container(
            padding: const EdgeInsets.all(18),
            decoration: BoxDecoration(
              borderRadius: BorderRadius.circular(22),
              gradient: LinearGradient(colors: [rc.withValues(alpha: .22), rc.withValues(alpha: .06)], begin: Alignment.topLeft, end: Alignment.bottomRight),
              border: Border.all(color: rc.withValues(alpha: .45)),
            ),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    RiskBadge(a.risk, large: true),
                    const Spacer(),
                    if (_result == null) Countdown(a.expiresAt, style: const TextStyle(fontSize: 15)),
                  ],
                ),
                const SizedBox(height: 14),
                Text(a.description.isEmpty ? a.tool : a.description, style: Theme.of(context).textTheme.titleLarge),
                const SizedBox(height: 6),
                Row(
                  children: [
                    Icon(_toolIcon(a.tool), size: 16, color: g.muted),
                    const SizedBox(width: 6),
                    Text(a.tool, style: mono(context, size: 13, color: g.muted)),
                  ],
                ),
              ],
            ),
          ),
          const SizedBox(height: 14),
          Section(
            title: 'What will run',
            icon: Icons.code_rounded,
            child: (a.command == null && a.preview == null)
                ? Notice(text: 'This request came with no details. Reject it unless you expected it.', color: g.warn, icon: Icons.help_outline_rounded)
                : Column(
                    children: [
                      if (a.command != null) CodeBlock(a.command!, prompt: true),
                      if (a.command != null && a.preview != null) const SizedBox(height: 10),
                      if (a.preview != null) CodeBlock(a.preview!),
                    ],
                  ),
          ),
          const SizedBox(height: 14),
          Section(
            title: 'Where',
            icon: Icons.place_outlined,
            child: Column(
              children: [
                InfoRow(Icons.forum_outlined, 'Session', a.sessionId),
                if (a.workspace != null && a.workspace!.isNotEmpty) InfoRow(Icons.folder_open_rounded, 'Workspace', a.workspace!),
              ],
            ),
          ),
          const SizedBox(height: 14),
          Section(
            title: 'Verify',
            icon: Icons.fingerprint_rounded,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Container(
                  padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
                  decoration: BoxDecoration(color: g.surfaceHigh, borderRadius: BorderRadius.circular(10)),
                  child: Text('Fingerprint: ${a.fingerprint}', style: mono(context, size: 14, weight: FontWeight.w600)),
                ),
                const SizedBox(height: 8),
                Text(
                  'The same fingerprint shows in the dashboard and in chat for this exact action. If it differs, reject.',
                  style: TextStyle(color: g.muted, fontSize: 13, height: 1.45),
                ),
              ],
            ),
          ),
        ],
      ),
      bottomNavigationBar: SafeArea(
        child: Container(
          padding: const EdgeInsets.fromLTRB(20, 14, 20, 14),
          decoration: BoxDecoration(
            color: g.surface,
            border: Border(top: BorderSide(color: g.border)),
          ),
          child: AnimatedSwitcher(
            duration: const Duration(milliseconds: 220),
            child: _result != null
                ? _ResultPanel(key: const ValueKey('result'), ok: _result!.ok, title: _result!.title, body: _result!.body, onDone: () => Navigator.pop(context))
                : expired
                ? Notice(key: const ValueKey('expired'), text: 'This request expired. Nothing was run.', color: g.muted, icon: Icons.timer_off_rounded)
                : _busy
                ? const SizedBox(
                    key: ValueKey('busy'),
                    height: 60,
                    child: Center(child: CircularProgressIndicator()),
                  )
                : Column(
                    key: const ValueKey('actions'),
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      if (a.highRisk) ...[
                        SlideToConfirm(label: 'Slide to approve', color: rc, onConfirmed: () => unawaited(_decide(true))),
                        const SizedBox(height: 10),
                        OutlinedButton(onPressed: () => unawaited(_decide(false)), child: const Text('Reject')),
                      ] else
                        Row(
                          children: [
                            Expanded(
                              child: OutlinedButton(onPressed: () => unawaited(_decide(false)), child: const Text('Reject')),
                            ),
                            const SizedBox(width: 12),
                            Expanded(
                              child: GradientButton(label: 'Approve', icon: Icons.fingerprint_rounded, onPressed: () => unawaited(_decide(true))),
                            ),
                          ],
                        ),
                      const SizedBox(height: 8),
                      Text(
                        a.highRisk ? 'High risk: slide, then confirm with your fingerprint or PIN.' : 'Approving asks for your fingerprint or PIN.',
                        style: TextStyle(color: g.muted, fontSize: 12),
                      ),
                    ],
                  ),
          ),
        ),
      ),
    );
  }
}

class _ResultPanel extends StatelessWidget {
  const _ResultPanel({super.key, required this.ok, required this.title, required this.body, required this.onDone});
  final bool ok;
  final String title, body;
  final VoidCallback onDone;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final c = ok ? g.ok : g.warn;
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        Row(
          children: [
            TweenAnimationBuilder<double>(
              tween: Tween(begin: .6, end: 1),
              duration: const Duration(milliseconds: 260),
              curve: Curves.easeOutBack,
              builder: (_, s, child) => Transform.scale(scale: s, child: child),
              child: Container(
                padding: const EdgeInsets.all(10),
                decoration: BoxDecoration(color: c.withValues(alpha: .16), shape: BoxShape.circle),
                child: Icon(ok ? Icons.check_rounded : Icons.info_outline_rounded, color: c, size: 26),
              ),
            ),
            const SizedBox(width: 14),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(title, style: const TextStyle(fontSize: 17, fontWeight: FontWeight.w700)),
                  const SizedBox(height: 2),
                  Text(body, style: TextStyle(color: g.muted, height: 1.4)),
                ],
              ),
            ),
          ],
        ),
        const SizedBox(height: 14),
        OutlinedButton(onPressed: onDone, child: const Text('Done')),
      ],
    );
  }
}
