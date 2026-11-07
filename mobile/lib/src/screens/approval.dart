import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../api.dart';
import '../stepup.dart';
import '../ui/theme.dart';
import '../ui/widgets.dart';

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
  ({bool ok, bool approved, String title, String body})? _result;

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
          approved: approved,
          title: approved ? 'Approved' : 'Rejected',
          body: approved ? 'Signed by this phone and recorded as you. The agent is continuing.' : 'Nothing was run. The agent was told no.',
        ),
      );
    } on ApiException catch (e) {
      setState(
        () => _result = (
          ok: false,
          approved: false,
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
          approved: false,
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
        padding: const EdgeInsets.fromLTRB(16, 4, 16, 24),
        children: [
          FadeSlideIn(
            child: Panel(
              borderColor: rc.withValues(alpha: .45),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      RiskBadge(a.risk, large: true),
                      const Spacer(),
                      if (_result == null) Countdown(a.expiresAt, style: const TextStyle(fontSize: 14)),
                    ],
                  ),
                  const SizedBox(height: 14),
                  Hero(
                    tag: 'approval-${a.id}',
                    child: Material(
                      type: MaterialType.transparency,
                      child: Text(a.description.isEmpty ? a.tool : a.description, style: Theme.of(context).textTheme.titleLarge),
                    ),
                  ),
                  const SizedBox(height: 8),
                  Row(
                    children: [
                      Icon(toolIcon(a.tool), size: 16, color: g.muted),
                      const SizedBox(width: 6),
                      Text(a.tool, style: mono(context, size: 13, color: g.muted)),
                    ],
                  ),
                ],
              ),
            ),
          ),
          const SizedBox(height: 12),
          FadeSlideIn(
            delay: const Duration(milliseconds: 60),
            child: Section(
              title: 'What will run',
              icon: Icons.code_rounded,
              child: (a.command == null && a.preview == null)
                  ? Notice(text: 'This request came with no details. Reject it unless you expected it.', color: g.warn, icon: Icons.help_outline_rounded)
                  : Column(
                      children: [
                        if (a.command != null) CodeBlock(a.command!, prompt: true),
                        if (a.command != null && a.preview != null) const SizedBox(height: 10),
                        if (a.preview != null) CodeBlock(a.preview!, maxLines: 14),
                      ],
                    ),
            ),
          ),
          const SizedBox(height: 12),
          FadeSlideIn(
            delay: const Duration(milliseconds: 120),
            child: Section(
              title: 'Where',
              icon: Icons.place_outlined,
              child: Column(
                children: [
                  InfoRow(Icons.forum_outlined, 'Session', a.sessionId, monoValue: true),
                  if (a.workspace != null && a.workspace!.isNotEmpty) InfoRow(Icons.folder_open_rounded, 'Workspace', a.workspace!, monoValue: true),
                ],
              ),
            ),
          ),
          const SizedBox(height: 12),
          FadeSlideIn(
            delay: const Duration(milliseconds: 180),
            child: Section(
              title: 'Verify',
              icon: Icons.fingerprint_rounded,
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Container(
                    padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
                    decoration: BoxDecoration(color: g.surfaceHigh, borderRadius: BorderRadius.circular(6)),
                    child: Text('Fingerprint: ${a.fingerprint}', style: mono(context, size: 14, weight: FontWeight.w500)),
                  ),
                  const SizedBox(height: 8),
                  Text(
                    'The same fingerprint shows in the dashboard and in chat for this exact action. If it differs, reject.',
                    style: TextStyle(color: g.muted, fontSize: 13, height: 1.45),
                  ),
                ],
              ),
            ),
          ),
        ],
      ),
      bottomNavigationBar: SafeArea(
        child: Container(
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 12),
          decoration: BoxDecoration(color: g.rail, border: Border(top: BorderSide(color: g.border))),
          child: AnimatedSize(
            duration: Motion.base,
            curve: Motion.curve,
            child: AnimatedSwitcher(
              duration: Motion.base,
              child: _result != null
                  ? _ResultPanel(
                      key: const ValueKey('result'),
                      ok: _result!.ok,
                      approved: _result!.approved,
                      title: _result!.title,
                      body: _result!.body,
                      onDone: () => Navigator.pop(context, _result!.ok),
                    )
                  : expired
                  ? Notice(key: const ValueKey('expired'), text: 'This request expired. Nothing was run.', color: g.muted, icon: Icons.timer_off_outlined)
                  : _busy
                  ? SizedBox(
                      key: const ValueKey('busy'),
                      height: 76,
                      child: Center(
                        child: Row(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            const SizedBox.square(dimension: 18, child: CircularProgressIndicator(strokeWidth: 2)),
                            const SizedBox(width: 12),
                            Text('Waiting for your fingerprint…', style: TextStyle(color: g.muted)),
                          ],
                        ),
                      ),
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
                              Expanded(child: OutlinedButton(onPressed: () => unawaited(_decide(false)), child: const Text('Reject'))),
                              const SizedBox(width: 10),
                              Expanded(child: PrimaryButton(label: 'Approve', icon: Icons.fingerprint_rounded, onPressed: () => unawaited(_decide(true)))),
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
      ),
    );
  }
}

class _ResultPanel extends StatelessWidget {
  const _ResultPanel({super.key, required this.ok, required this.approved, required this.title, required this.body, required this.onDone});
  final bool ok, approved;
  final String title, body;
  final VoidCallback onDone;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final c = !ok ? g.warn : (approved ? g.ok : g.muted);
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        Row(
          children: [
            AnimatedCheck(color: c, ok: ok && approved, size: 46),
            const SizedBox(width: 14),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(title, style: const TextStyle(fontSize: 17, fontWeight: FontWeight.w600)),
                  const SizedBox(height: 2),
                  Text(body, style: TextStyle(color: g.muted, height: 1.4)),
                ],
              ),
            ),
          ],
        ),
        const SizedBox(height: 12),
        OutlinedButton(onPressed: onDone, child: const Text('Done')),
      ],
    );
  }
}
