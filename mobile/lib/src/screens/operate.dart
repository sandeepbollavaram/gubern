import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../api.dart';
import '../ui/theme.dart';
import '../ui/widgets.dart';
import 'shell.dart';

/// The ops side of the dashboard: the server, spend, health, routes, runs
/// and agents.
class OperateTab extends StatefulWidget {
  const OperateTab({super.key, required this.scope});
  final AppScope scope;
  @override
  State<OperateTab> createState() => _OperateTabState();
}

class _OperateTabState extends State<OperateTab> {
  StatusInfo? _status;
  CostInfo? _cost;
  HealingInfo? _health;
  List<RunInfo>? _runs;
  List<AgentInfo>? _agents;
  List<SkillInfo>? _skills;
  SandboxInfo? _sandbox;
  bool _loaded = false;

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    final api = widget.scope.api;
    Future<Object?> opt(Future<Object?> f) => f.catchError((_) => null);
    final r = await Future.wait<Object?>([
      opt(api.status()),
      opt(api.cost()),
      opt(api.healing()),
      opt(api.runs(limit: 12)),
      opt(api.agents()),
      opt(api.skills()),
      opt(api.sandbox()),
    ]);
    if (!mounted) return;
    setState(() {
      _status = r[0] as StatusInfo?;
      _cost = r[1] as CostInfo?;
      _health = r[2] as HealingInfo?;
      _runs = r[3] as List<RunInfo>?;
      _agents = r[4] as List<AgentInfo>?;
      _skills = r[5] as List<SkillInfo>?;
      _sandbox = r[6] as SandboxInfo?;
      _loaded = true;
    });
  }

  Future<void> _cancel(RunInfo r) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (d) => AlertDialog(
        title: const Text('Stop this run?'),
        content: Text('The agent stops at its next step. Session ${r.sessionId}.'),
        actions: [
          TextButton(onPressed: () => Navigator.pop(d, false), child: const Text('Keep running')),
          TextButton(onPressed: () => Navigator.pop(d, true), child: Text('Stop', style: TextStyle(color: context.g.critical))),
        ],
      ),
    );
    if (ok != true) return;
    HapticFeedback.mediumImpact();
    try {
      await widget.scope.api.cancelRun(r.id);
    } catch (e) {
      if (mounted) ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('$e')));
    }
    await Future<void>.delayed(const Duration(milliseconds: 400));
    unawaited(_load());
  }

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final s = _status;
    var i = 0;
    Widget stag(Widget w) => FadeSlideIn.indexed(i++, child: Padding(padding: const EdgeInsets.only(bottom: 18), child: w));
    return SafeArea(
      bottom: false,
      child: RefreshIndicator(
        color: g.brand,
        onRefresh: _load,
        child: ListView(
          padding: const EdgeInsets.fromLTRB(0, 0, 0, 24),
          children: [
            TabHeader(scope: widget.scope, title: 'Operate'),
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
              child: !_loaded
                  ? const SkeletonList(count: 4)
                  : Column(
                      crossAxisAlignment: CrossAxisAlignment.stretch,
                      children: [
                        stag(_Overview(status: s, health: _health, cost: _cost)),
                        if (_cost != null) stag(_SpendPanel(cost: _cost!)),
                        stag(_HealthPanel(health: _health)),
                        if (s != null && s.routeNames.isNotEmpty) stag(_RoutesPanel(status: s)),
                        stag(_RunsPanel(runs: _runs, onCancel: _cancel, onOpen: (r) => widget.scope.openChat(context, sessionId: r.sessionId))),
                        if (_agents != null && _agents!.isNotEmpty) stag(_AgentsPanel(agents: _agents!)),
                        if (_sandbox != null) stag(_SandboxPanel(info: _sandbox!)),
                        if (_skills != null) stag(_SkillsPanel(skills: _skills!)),
                        if (s != null) stag(_ServerPanel(status: s)),
                      ],
                    ),
            ),
          ],
        ),
      ),
    );
  }
}

class _Overview extends StatelessWidget {
  const _Overview({required this.status, required this.health, required this.cost});
  final StatusInfo? status;
  final HealingInfo? health;
  final CostInfo? cost;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final failing = health?.failing ?? 0;
    Widget stat(String label, Widget value, {Color? color}) => Expanded(
      child: Panel(
        padding: const EdgeInsets.fromLTRB(12, 12, 12, 12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(label, style: TextStyle(color: g.muted, fontSize: 12)),
            const SizedBox(height: 6),
            DefaultTextStyle.merge(style: TextStyle(fontSize: 20, fontWeight: FontWeight.w600, color: color ?? g.text), child: value),
          ],
        ),
      ),
    );
    return Row(
      children: [
        stat('Routes', CountUp((status?.routes ?? 0).toDouble())),
        const SizedBox(width: 8),
        stat('Checks failing', CountUp(failing.toDouble()), color: failing > 0 ? g.critical : g.ok),
        const SizedBox(width: 8),
        stat('Requests', CountUp((cost?.requestsToday ?? 0).toDouble())),
      ],
    );
  }
}

class _SpendPanel extends StatelessWidget {
  const _SpendPanel({required this.cost});
  final CostInfo cost;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final c = cost;
    final color = c.used > .9 ? g.critical : (c.used > .7 ? g.warn : g.brand);
    return Section(
      title: 'AI spend today',
      icon: Icons.payments_outlined,
      trailing: c.free ? Pill(label: 'Free', color: g.ok) : (c.provider.isEmpty ? null : Text(c.provider, style: mono(context, size: 12, color: g.muted))),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            crossAxisAlignment: CrossAxisAlignment.end,
            children: [
              CountUp(c.totalUsd, format: (v) => '\$${v.toStringAsFixed(2)}', style: const TextStyle(fontSize: 26, fontWeight: FontWeight.w600)),
              const SizedBox(width: 8),
              if (c.dailyBudget > 0)
                Padding(
                  padding: const EdgeInsets.only(bottom: 4),
                  child: Text('of \$${c.dailyBudget.toStringAsFixed(2)}', style: TextStyle(color: g.muted)),
                ),
            ],
          ),
          const SizedBox(height: 10),
          if (c.dailyBudget > 0) ...[
            Meter(value: c.used, color: color),
            const SizedBox(height: 8),
            Text(
              '${(c.used * 100).round()}% of the daily budget · \$${c.remaining.toStringAsFixed(2)} left',
              style: TextStyle(color: c.used > .7 ? color : g.muted, fontSize: 13),
            ),
          ] else
            Text('No daily budget is set.', style: TextStyle(color: g.muted, fontSize: 13)),
        ],
      ),
    );
  }
}

class _HealthPanel extends StatelessWidget {
  const _HealthPanel({required this.health});
  final HealingInfo? health;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final h = health;
    return Section(
      title: 'Health checks',
      icon: Icons.monitor_heart_outlined,
      trailing: h == null
          ? null
          : Pill(
              label: h.failing == 0 ? 'Healthy' : '${h.failing} failing',
              color: h.failing == 0 ? g.ok : g.critical,
              icon: h.failing == 0 ? Icons.check_rounded : Icons.error_outline_rounded,
            ),
      child: h == null
          ? Text('Health isn\'t available to this phone.', style: TextStyle(color: g.muted))
          : h.checks.isEmpty
          ? Text('No checks configured.', style: TextStyle(color: g.muted))
          : Column(
              children: [
                for (final c in h.checks)
                  Padding(
                    padding: const EdgeInsets.symmetric(vertical: 6),
                    child: Row(
                      children: [
                        LiveDot(color: c.healthy ? g.ok : g.critical, live: !c.healthy, size: 7),
                        const SizedBox(width: 6),
                        Expanded(child: Text(c.name, style: mono(context, size: 13))),
                        Text(
                          c.healthy ? '${c.latencyMs.toStringAsFixed(0)} ms' : '${c.failures} fail${c.failures == 1 ? '' : 's'}',
                          style: TextStyle(color: c.healthy ? g.muted : g.critical, fontSize: 12.5, fontFamily: monoFamily),
                        ),
                      ],
                    ),
                  ),
                if (h.sloAlerts > 0 || h.recoveries > 0) ...[
                  Divider(color: g.border, height: 18),
                  Row(
                    children: [
                      Text('${h.sloAlerts} SLO alert${h.sloAlerts == 1 ? '' : 's'}', style: TextStyle(color: h.sloAlerts > 0 ? g.warn : g.muted, fontSize: 13)),
                      const Spacer(),
                      Text('${h.recoveries} auto-recover${h.recoveries == 1 ? 'y' : 'ies'}', style: TextStyle(color: g.muted, fontSize: 13)),
                    ],
                  ),
                ],
              ],
            ),
    );
  }
}

class _RoutesPanel extends StatelessWidget {
  const _RoutesPanel({required this.status});
  final StatusInfo status;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    return Section(
      title: 'Routes',
      icon: Icons.alt_route_rounded,
      trailing: Text('${status.routes}', style: mono(context, size: 12, color: g.muted)),
      child: Wrap(
        spacing: 6,
        runSpacing: 6,
        children: [
          for (final r in status.routeNames)
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
              decoration: BoxDecoration(color: g.surfaceHigh, borderRadius: BorderRadius.circular(6), border: Border.all(color: g.border)),
              child: Text(r, style: mono(context, size: 12)),
            ),
        ],
      ),
    );
  }
}

class _RunsPanel extends StatelessWidget {
  const _RunsPanel({required this.runs, required this.onCancel, required this.onOpen});
  final List<RunInfo>? runs;
  final ValueChanged<RunInfo> onCancel, onOpen;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final rs = runs;
    return Section(
      title: 'Recent runs',
      icon: Icons.play_circle_outline_rounded,
      child: rs == null
          ? Text('Runs aren\'t available to this phone.', style: TextStyle(color: g.muted))
          : rs.isEmpty
          ? Text('No runs yet.', style: TextStyle(color: g.muted))
          : Column(
              children: [
                for (final r in rs)
                  InkWell(
                    onTap: () => onOpen(r),
                    borderRadius: BorderRadius.circular(6),
                    child: Padding(
                      padding: const EdgeInsets.symmetric(vertical: 6),
                      child: Row(
                        children: [
                          _RunStatus(r.status),
                          const SizedBox(width: 10),
                          Expanded(
                            child: Column(
                              crossAxisAlignment: CrossAxisAlignment.start,
                              children: [
                                Text(r.sessionId, style: mono(context, size: 12.5)),
                                Text('${r.lastSeq} events · ${r.id.length > 10 ? r.id.substring(0, 10) : r.id}', style: TextStyle(color: g.muted, fontSize: 12)),
                              ],
                            ),
                          ),
                          if (r.live)
                            TextButton(
                              onPressed: () => onCancel(r),
                              style: TextButton.styleFrom(foregroundColor: g.critical, minimumSize: const Size(44, 36)),
                              child: const Text('Stop'),
                            )
                          else
                            Icon(Icons.chevron_right_rounded, color: g.muted, size: 18),
                        ],
                      ),
                    ),
                  ),
              ],
            ),
    );
  }
}

class _RunStatus extends StatelessWidget {
  const _RunStatus(this.status);
  final String status;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    return switch (status) {
      'running' => Pill(label: 'Running', color: g.aiText, icon: Icons.autorenew_rounded),
      'waiting_approval' => Pill(label: 'Needs you', color: g.warn, icon: Icons.front_hand_outlined),
      'verifying' => Pill(label: 'Verifying', color: g.aiText, icon: Icons.fact_check_outlined),
      'completed' => Pill(label: 'Verified', color: g.ok, icon: Icons.verified_outlined),
      'partial' => Pill(label: 'Partial', color: g.warn, icon: Icons.rule_rounded),
      'interrupted' => Pill(label: 'Interrupted', color: g.warn, icon: Icons.pause_circle_outline_rounded),
      'done' || 'succeeded' => Pill(label: 'Done', color: g.ok, icon: Icons.check_rounded),
      'failed' => Pill(label: 'Failed', color: g.critical, icon: Icons.close_rounded),
      'cancelled' => Pill(label: 'Stopped', color: g.warn, icon: Icons.stop_rounded),
      _ => Pill(label: status.isEmpty ? 'Unknown' : status, color: g.muted),
    };
  }
}

class _AgentsPanel extends StatelessWidget {
  const _AgentsPanel({required this.agents});
  final List<AgentInfo> agents;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    return Section(
      title: 'Agents',
      icon: Icons.smart_toy_outlined,
      trailing: Text('${agents.length}', style: mono(context, size: 12, color: g.muted)),
      child: Column(
        children: [
          for (final a in agents)
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 7),
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Container(
                    width: 30,
                    height: 30,
                    alignment: Alignment.center,
                    decoration: BoxDecoration(color: g.ai.withValues(alpha: .15), borderRadius: BorderRadius.circular(8)),
                    child: Text(
                      (a.name.isEmpty ? a.id : a.name).replaceFirst(RegExp(r'^Gubern\s+'), '').characters.first.toUpperCase(),
                      style: TextStyle(color: g.aiText, fontWeight: FontWeight.w600),
                    ),
                  ),
                  const SizedBox(width: 10),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(a.name.isEmpty ? a.id : a.name, style: const TextStyle(fontWeight: FontWeight.w500)),
                        Text(
                          [if (a.role.isNotEmpty) a.role, if (a.tools.isNotEmpty) '${a.tools.length} tools', if (a.profile.isNotEmpty) a.profile].join(' · '),
                          style: TextStyle(color: g.muted, fontSize: 12.5),
                        ),
                      ],
                    ),
                  ),
                ],
              ),
            ),
        ],
      ),
    );
  }
}

/// Where agent commands run: the server's own words for what its sandbox
/// enforces and what it doesn't, so the phone never claims more.
class _SandboxPanel extends StatelessWidget {
  const _SandboxPanel({required this.info});
  final SandboxInfo info;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final color = switch (info.level) { 'isolated' => g.ok, 'contained' => g.warn, _ => g.muted };
    String cap(String s) => s.isEmpty ? s : s[0].toUpperCase() + s.substring(1);
    Widget line(String text, IconData icon, Color c) => Padding(
      padding: const EdgeInsets.symmetric(vertical: 3),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(padding: const EdgeInsets.only(top: 2), child: Icon(icon, size: 14, color: c)),
          const SizedBox(width: 8),
          Expanded(child: Text(cap(text), style: TextStyle(fontSize: 13, height: 1.35, color: c == g.muted ? g.muted : g.text))),
        ],
      ),
    );
    return Section(
      title: 'Sandbox',
      icon: Icons.shield_outlined,
      trailing: Pill(label: info.label, color: color),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            switch (info.level) {
              'isolated' => 'Commands agents run can only change the workspace and can\'t see the rest of the server.',
              'contained' => 'Commands agents run are kept together and capped, and nothing they start outlives them. They aren\'t fenced off from the rest of the server.',
              _ => 'Commands agents run go straight to the server, after your approval.',
            },
            style: TextStyle(color: g.muted, fontSize: 12.5, height: 1.4),
          ),
          if (info.reason.isNotEmpty) ...[
            const SizedBox(height: 4),
            Text('Why: ${info.reason}.', style: TextStyle(color: g.muted, fontSize: 12.5)),
          ],
          const SizedBox(height: 8),
          for (final e in info.enforced) line(e, Icons.check_rounded, g.ok),
          for (final e in info.notEnforced) line('Not enforced: $e', Icons.remove_rounded, g.muted),
          const SizedBox(height: 6),
          Text('Network: ${info.network}', style: mono(context, size: 12, color: info.network == 'not enforced' ? g.warn : g.muted)),
        ],
      ),
    );
  }
}

class _SkillsPanel extends StatelessWidget {
  const _SkillsPanel({required this.skills});
  final List<SkillInfo> skills;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final on = skills.where((s) => s.enabled).length;
    return Section(
      title: 'Skills',
      icon: Icons.menu_book_outlined,
      trailing: Text('$on of ${skills.length} on', style: TextStyle(color: g.muted, fontSize: 12)),
      child: skills.isEmpty
          ? Text('No skills in this workspace.', style: TextStyle(color: g.muted))
          : Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                for (final s in skills)
                  Padding(
                    padding: const EdgeInsets.symmetric(vertical: 7),
                    child: Row(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Padding(
                          padding: const EdgeInsets.only(top: 2),
                          child: Icon(Icons.menu_book_outlined, size: 16, color: s.enabled ? g.aiText : g.muted),
                        ),
                        const SizedBox(width: 10),
                        Expanded(
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Text(s.name, style: mono(context, size: 13, color: s.enabled ? g.text : g.muted, weight: FontWeight.w500)),
                              const SizedBox(height: 2),
                              Text(s.description, maxLines: 2, overflow: TextOverflow.ellipsis, style: TextStyle(color: g.muted, fontSize: 12.5, height: 1.4)),
                            ],
                          ),
                        ),
                        const SizedBox(width: 8),
                        if (s.pinned && !s.tampered) ...[
                          Tooltip(message: 'Approved and pinned', child: Icon(Icons.verified_user_outlined, size: 16, color: g.ok)),
                          const SizedBox(width: 6),
                        ],
                        s.tampered
                            ? Pill(label: 'Changed', color: g.critical, icon: Icons.gpp_maybe_outlined)
                            : Pill(label: s.enabled ? 'On' : 'Off', color: s.enabled ? g.ok : g.muted),
                      ],
                    ),
                  ),
                const SizedBox(height: 4),
                if (skills.any((s) => s.tampered)) ...[
                  Notice(
                    text: 'A pinned skill changed after it was approved, so agents aren\'t shown it. Reinstall it from the dashboard to approve the new version.',
                    color: g.critical,
                    icon: Icons.gpp_maybe_outlined,
                  ),
                  const SizedBox(height: 8),
                ],
                Text('Skills are switched on or installed from the dashboard; installs arrive here as approvals. They never grant permissions.', style: TextStyle(color: g.muted, fontSize: 12)),
              ],
            ),
    );
  }
}

class _ServerPanel extends StatelessWidget {
  const _ServerPanel({required this.status});
  final StatusInfo status;

  @override
  Widget build(BuildContext context) {
    final s = status;
    return Section(
      title: 'Server',
      icon: Icons.dns_outlined,
      child: Column(
        children: [
          InfoRow(Icons.sell_outlined, 'Version', s.version, monoValue: true),
          if (s.clusterName.isNotEmpty) InfoRow(Icons.hub_outlined, 'Cluster', s.clusterName),
          if (s.nodeId.isNotEmpty) InfoRow(Icons.memory_rounded, 'Node', s.nodeId, monoValue: true),
          if (s.tlsProvider.isNotEmpty) InfoRow(Icons.lock_outline_rounded, 'TLS', s.tlsProvider),
          if (s.secretBackend.isNotEmpty) InfoRow(Icons.key_outlined, 'Secrets', s.secretBackend),
          InfoRow(Icons.policy_outlined, 'Policy', s.policyDefaultAllow ? 'Allow by default' : 'Deny by default'),
          if (s.workingDir.isNotEmpty) InfoRow(Icons.folder_open_outlined, 'Workspace', s.workingDir, monoValue: true),
          InfoRow(Icons.receipt_long_outlined, 'Audit log', '${s.auditCount} entries'),
          InfoRow(Icons.extension_outlined, 'Plugins', '${s.pluginCount}'),
        ],
      ),
    );
  }
}
