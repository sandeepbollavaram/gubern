import 'dart:async';

import 'package:flutter/material.dart';
import 'package:mobile_scanner/mobile_scanner.dart';

import 'api.dart';
import 'credential_store.dart';
import 'pairing_link.dart';
import 'stepup.dart';

/// Pairing: scan the dashboard's QR (or paste its link), confirm, pair.
class PairScreen extends StatefulWidget {
  const PairScreen({super.key, required this.store, required this.deviceKey, required this.onPaired});
  final CredentialStore store;
  final DeviceKey deviceKey;
  final void Function(GubernApi) onPaired;

  @override
  State<PairScreen> createState() => _PairScreenState();
}

class _PairScreenState extends State<PairScreen> {
  final _link = TextEditingController();
  final _label = TextEditingController(text: 'My phone');
  bool _scanning = false;
  bool _busy = false;
  String? _error;

  Future<void> _pair(String raw) async {
    setState(() {
      _error = null;
      _scanning = false;
    });
    PairingLink link;
    try {
      link = PairingLink.parse(raw);
    } on FormatException catch (e) {
      setState(() => _error = e.message);
      return;
    }
    if (link.insecure) {
      final ok = await showDialog<bool>(
        context: context,
        builder: (c) => AlertDialog(
          title: const Text('Unencrypted connection'),
          content: Text('${link.server.host} does not use https. Pair only on a network you trust, such as your home Wi-Fi or a private VPN.'),
          actions: [
            TextButton(onPressed: () => Navigator.pop(c, false), child: const Text('Cancel')),
            FilledButton(onPressed: () => Navigator.pop(c, true), child: const Text('Pair anyway')),
          ],
        ),
      );
      if (ok != true) return;
    }
    setState(() => _busy = true);
    try {
      final creds = await GubernApi.pair(link, widget.deviceKey, widget.store, label: _label.text.trim().isEmpty ? 'Phone' : _label.text.trim());
      widget.onPaired(GubernApi(creds, widget.store));
    } catch (e) {
      setState(() => _error = 'Pairing failed: $e');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Pair with Gubern')),
      body: ListView(
        padding: const EdgeInsets.all(20),
        children: [
          const Text('In the Gubern dashboard, open Devices → Pair a phone, then scan the code. It works once and expires in ten minutes.'),
          const SizedBox(height: 16),
          if (_scanning)
            SizedBox(
              height: 280,
              child: MobileScanner(onDetect: (capture) {
                final v = capture.barcodes.firstOrNull?.rawValue;
                if (v != null && v.startsWith('gubern://')) unawaited(_pair(v));
              }),
            )
          else
            FilledButton.icon(
              onPressed: _busy ? null : () => setState(() => _scanning = true),
              icon: const Icon(Icons.qr_code_scanner),
              label: const Text('Scan pairing code'),
            ),
          const SizedBox(height: 20),
          TextField(controller: _label, decoration: const InputDecoration(labelText: 'Name for this phone')),
          const SizedBox(height: 12),
          TextField(controller: _link, decoration: const InputDecoration(labelText: 'Or paste the pairing link (gubern://pair…)')),
          const SizedBox(height: 12),
          OutlinedButton(onPressed: _busy ? null : () => unawaited(_pair(_link.text)), child: Text(_busy ? 'Pairing…' : 'Pair')),
          if (_error != null) ...[
            const SizedBox(height: 12),
            Text(_error!, style: TextStyle(color: Theme.of(context).colorScheme.error)),
          ],
        ],
      ),
    );
  }
}

/// Pending approvals, refreshed every few seconds and on pull.
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
      if (mounted) setState(() => (_items = items, _error = null));
    } on RevokedException {
      widget.onUnpaired();
    } catch (e) {
      if (mounted) setState(() => _error = '$e');
    }
  }

  Future<void> _unpair() async {
    await widget.api.store.wipe();
    await widget.deviceKey.delete();
    widget.onUnpaired();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('Needs you'),
        actions: [IconButton(tooltip: 'Unpair this phone', onPressed: () => unawaited(_unpair()), icon: const Icon(Icons.link_off))],
      ),
      body: RefreshIndicator(
        onRefresh: _refresh,
        child: ListView(
          children: [
            if (_error != null) ListTile(leading: const Icon(Icons.cloud_off), title: Text(_error!)),
            if (_items.isEmpty && _error == null)
              const Padding(padding: EdgeInsets.all(32), child: Center(child: Text('Nothing is waiting for you.'))),
            for (final a in _items)
              ListTile(
                leading: Icon(a.highRisk ? Icons.warning_amber : Icons.shield_outlined, color: a.highRisk ? Colors.red : null),
                title: Text(a.description.isEmpty ? a.tool : a.description),
                subtitle: Text('${a.tool} · ${a.risk} · ${a.sessionId}'),
                onTap: () async {
                  await Navigator.push(context, MaterialPageRoute(builder: (_) => ApprovalDetailScreen(api: widget.api, deviceKey: widget.deviceKey, approval: a)));
                  unawaited(_refresh());
                },
              ),
          ],
        ),
      ),
    );
  }
}

/// One approval in full: what runs, where, the action fingerprint, the
/// deadline. Approving signs a server challenge with the hardware key
/// (biometrics); high risk also needs a second, deliberate tap.
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
  bool _armed = false;
  String? _result;
  Timer? _tick;

  @override
  void initState() {
    super.initState();
    _tick = Timer.periodic(const Duration(seconds: 1), (_) => setState(() {}));
  }

  @override
  void dispose() {
    _tick?.cancel();
    super.dispose();
  }

  Future<void> _decide(bool approve) async {
    final a = widget.approval;
    if (approve && a.highRisk && !_armed) {
      setState(() => _armed = true);
      return;
    }
    setState(() => _busy = true);
    try {
      final done = approve ? await widget.api.approve(a, widget.deviceKey) : await widget.api.reject(a);
      setState(() => _result = '${done.status == 'approved' ? 'Approved' : 'Rejected'}. Recorded as you, from this phone.');
    } on ApiException catch (e) {
      setState(() => _result = switch (e.reason) {
            'not-pending' => 'Already decided elsewhere.',
            'expired' => 'This request expired; nothing was run.',
            'action-mismatch' => 'The action changed. Go back and review it again.',
            _ => e.message,
          });
    } catch (e) {
      setState(() => _result = 'Not decided: $e');
    } finally {
      if (mounted) setState(() => (_busy = false, _armed = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final a = widget.approval;
    final left = a.expiresAt.difference(DateTime.now());
    final expired = left.isNegative;
    final mono = Theme.of(context).textTheme.bodySmall?.copyWith(fontFamily: 'monospace');
    return Scaffold(
      appBar: AppBar(title: Text(a.tool)),
      body: ListView(
        padding: const EdgeInsets.all(20),
        children: [
          Chip(
            avatar: Icon(a.highRisk ? Icons.warning_amber : Icons.info_outline, size: 18),
            label: Text(a.risk),
            backgroundColor: a.highRisk ? Colors.red.withValues(alpha: .12) : null,
          ),
          const SizedBox(height: 12),
          Text(a.description, style: Theme.of(context).textTheme.titleMedium),
          if (a.command != null) ...[const SizedBox(height: 12), const Text('Command'), SelectableText(a.command!, style: mono)],
          if (a.preview != null) ...[const SizedBox(height: 12), const Text('Preview'), SelectableText(a.preview!, style: mono)],
          if (a.command == null && a.preview == null)
            const Padding(
              padding: EdgeInsets.only(top: 12),
              child: Text('This request came with no details. Reject it unless you expected it.', style: TextStyle(color: Colors.orange)),
            ),
          const SizedBox(height: 12),
          Text('Session: ${a.sessionId}'),
          if (a.workspace != null && a.workspace!.isNotEmpty) Text('Workspace: ${a.workspace}'),
          Text('Fingerprint: ${a.fingerprint}', style: mono),
          Text(expired ? 'Expired' : 'Expires in ${left.inMinutes}:${(left.inSeconds % 60).toString().padLeft(2, '0')}'),
          const SizedBox(height: 24),
          if (_result != null) Padding(padding: const EdgeInsets.only(bottom: 12), child: Text(_result!)),
          if (_result == null && !expired)
            Row(children: [
              Expanded(child: OutlinedButton(onPressed: _busy ? null : () => unawaited(_decide(false)), child: const Text('Reject'))),
              const SizedBox(width: 12),
              Expanded(
                child: FilledButton(
                  style: _armed ? FilledButton.styleFrom(backgroundColor: Colors.red) : null,
                  onPressed: _busy ? null : () => unawaited(_decide(true)),
                  child: Text(_armed ? 'Tap again to approve' : 'Approve'),
                ),
              ),
            ]),
          if (a.highRisk && _result == null && !expired)
            const Padding(padding: EdgeInsets.only(top: 8), child: Text('High risk: approving takes two taps and your fingerprint or face.')),
        ],
      ),
    );
  }
}
