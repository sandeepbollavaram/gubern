import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../api.dart';
import '../hub.dart';
import '../stepup.dart';
import '../ui/brand.dart';
import '../ui/theme.dart';
import '../ui/widgets.dart';
import 'approval.dart';
import 'chat.dart';
import 'chats.dart';
import 'operate.dart';
import 'settings.dart';

/// Everything a screen needs to talk to Gubern.
class AppScope {
  AppScope({required this.api, required this.deviceKey, required this.hub, required this.themeMode, required this.onUnpaired});
  final GubernApi api;
  final DeviceKey deviceKey;
  final Hub hub;
  final ValueNotifier<ThemeMode> themeMode;
  final VoidCallback onUnpaired;

  Future<void> openChat(BuildContext context, {String? sessionId, String? title, ChatMode mode = ChatMode.chat}) => Navigator.push(
    context,
    MaterialPageRoute(
      builder: (_) => ChatScreen(api: api, deviceKey: deviceKey, onRevoked: onUnpaired, sessionId: sessionId, title: title, initialMode: mode),
    ),
  );

  Future<void> openApproval(BuildContext context, ApprovalView a) async {
    await Navigator.push(context, MaterialPageRoute(builder: (_) => ApprovalDetailScreen(api: api, deviceKey: deviceKey, approval: a)));
    unawaited(hub.refresh());
  }

  Future<void> openSettings(BuildContext context) => Navigator.push(context, MaterialPageRoute(builder: (_) => SettingsScreen(scope: this)));
}

/// The paired app: four tabs, the same areas as the dashboard rail.
class AppShell extends StatefulWidget {
  const AppShell({super.key, required this.api, required this.deviceKey, required this.onUnpaired, required this.themeMode});
  final GubernApi api;
  final DeviceKey deviceKey;
  final VoidCallback onUnpaired;
  final ValueNotifier<ThemeMode> themeMode;

  @override
  State<AppShell> createState() => _AppShellState();
}

class _AppShellState extends State<AppShell> with SingleTickerProviderStateMixin {
  late final Hub _hub = Hub(widget.api, onRevoked: widget.onUnpaired)..start();
  late final AppScope _scope = AppScope(api: widget.api, deviceKey: widget.deviceKey, hub: _hub, themeMode: widget.themeMode, onUnpaired: widget.onUnpaired);
  late final AnimationController _fade = AnimationController(vsync: this, duration: Motion.base, value: 1);
  int _tab = 0;

  @override
  void dispose() {
    _hub.dispose();
    _fade.dispose();
    super.dispose();
  }

  void _go(int i) {
    if (i == _tab) return;
    HapticFeedback.selectionClick();
    setState(() => _tab = i);
    if (!reducedMotion(context)) _fade.forward(from: 0);
  }

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    return ListenableBuilder(
      listenable: _hub,
      builder: (context, _) => Scaffold(
        body: AnimatedBuilder(
          animation: _fade,
          builder: (_, child) {
            final t = Curves.easeOutCubic.transform(_fade.value);
            return Opacity(opacity: t, child: Transform.translate(offset: Offset(0, 8 * (1 - t)), child: child));
          },
          child: IndexedStack(
            index: _tab,
            children: [
              HomeTab(scope: _scope, onGo: _go),
              ChatsTab(scope: _scope),
              InboxTab(scope: _scope),
              OperateTab(scope: _scope),
            ],
          ),
        ),
        bottomNavigationBar: DecoratedBox(
          decoration: BoxDecoration(border: Border(top: BorderSide(color: g.border))),
          child: NavigationBar(
            selectedIndex: _tab,
            onDestinationSelected: _go,
            labelBehavior: NavigationDestinationLabelBehavior.alwaysShow,
            destinations: [
              const NavigationDestination(icon: Icon(Icons.home_outlined), selectedIcon: Icon(Icons.home_rounded), label: 'Home'),
              const NavigationDestination(icon: Icon(Icons.chat_bubble_outline_rounded), selectedIcon: Icon(Icons.chat_bubble_rounded), label: 'Chats'),
              NavigationDestination(
                icon: _badged(Icons.inbox_outlined, _hub.needsYou),
                selectedIcon: _badged(Icons.inbox_rounded, _hub.needsYou),
                label: 'Inbox',
              ),
              const NavigationDestination(icon: Icon(Icons.monitor_heart_outlined), selectedIcon: Icon(Icons.monitor_heart_rounded), label: 'Operate'),
            ],
          ),
        ),
      ),
    );
  }

  Widget _badged(IconData icon, int n) => Badge(
    isLabelVisible: n > 0,
    backgroundColor: context.g.critical,
    label: Text('$n', style: const TextStyle(fontWeight: FontWeight.w600)),
    child: Icon(icon),
  );
}

/// Top bar shared by the tabs: title, connection, and the account button.
class TabHeader extends StatelessWidget {
  const TabHeader({super.key, required this.scope, required this.title, this.trailing});
  final AppScope scope;
  final String title;
  final Widget? trailing;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final name = scope.hub.me?.name ?? '';
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 8, 8, 4),
      child: Row(
        children: [
          Expanded(child: Text(title, style: Theme.of(context).textTheme.headlineSmall)),
          ?trailing,
          IconButton(
            tooltip: 'Settings',
            onPressed: () => scope.openSettings(context),
            icon: Container(
              width: 30,
              height: 30,
              alignment: Alignment.center,
              decoration: BoxDecoration(color: g.selected, borderRadius: BorderRadius.circular(8)),
              child: Text(name.isEmpty ? '·' : name.characters.first.toUpperCase(), style: TextStyle(color: g.brand, fontWeight: FontWeight.w600)),
            ),
          ),
        ],
      ),
    );
  }
}

/// A connection line: live dot, host, and when it last heard from it.
class ConnectionLine extends StatefulWidget {
  const ConnectionLine({super.key, required this.hub});
  final Hub hub;
  @override
  State<ConnectionLine> createState() => _ConnectionLineState();
}

class _ConnectionLineState extends State<ConnectionLine> {
  Timer? _t;
  @override
  void initState() {
    super.initState();
    _t = Timer.periodic(const Duration(seconds: 15), (_) {
      if (mounted) setState(() {});
    });
  }

  @override
  void dispose() {
    _t?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final h = widget.hub;
    final c = h.online ? g.ok : g.warn;
    return Row(
      children: [
        LiveDot(color: c, live: h.online, size: 7),
        const SizedBox(width: 4),
        Flexible(
          child: Text(
            h.online ? 'Connected to ${h.host}' : 'Reconnecting to ${h.host}…',
            overflow: TextOverflow.ellipsis,
            style: TextStyle(color: h.online ? g.muted : g.warn, fontSize: 13),
          ),
        ),
        if (h.updated != null && h.online) Text('  ·  ${timeAgo(h.updated!)}', style: TextStyle(color: g.muted, fontSize: 13)),
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// Home
// ---------------------------------------------------------------------------

class HomeTab extends StatefulWidget {
  const HomeTab({super.key, required this.scope, required this.onGo});
  final AppScope scope;
  final ValueChanged<int> onGo;
  @override
  State<HomeTab> createState() => _HomeTabState();
}

class _HomeTabState extends State<HomeTab> {
  HealingInfo? _health;
  CostInfo? _cost;
  StatusInfo? _status;
  List<SessionSummary>? _recent;

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    final api = widget.scope.api;
    final r = await Future.wait<Object?>([
      api.healing().then<Object?>((v) => v).catchError((_) => null),
      api.cost().then<Object?>((v) => v).catchError((_) => null),
      api.status().then<Object?>((v) => v).catchError((_) => null),
      api.sessions().then<Object?>((v) => v).catchError((_) => null),
    ]);
    if (!mounted) return;
    setState(() {
      _health = r[0] as HealingInfo?;
      _cost = r[1] as CostInfo?;
      _status = r[2] as StatusInfo?;
      _recent = (r[3] as List<SessionSummary>?) ?? const [];
    });
  }

  String _greeting() {
    final h = DateTime.now().hour;
    return h < 5 ? 'Good evening' : h < 12 ? 'Good morning' : (h < 18 ? 'Good afternoon' : 'Good evening');
  }

  @override
  Widget build(BuildContext context) {
    final s = widget.scope;
    final hub = s.hub;
    final g = context.g;
    final t = Theme.of(context).textTheme;
    final name = hub.me?.name ?? '';
    return SafeArea(
      bottom: false,
      child: RefreshIndicator(
        color: g.brand,
        onRefresh: () => Future.wait([hub.refresh(), _load()]),
        child: ListView(
          padding: const EdgeInsets.fromLTRB(16, 8, 16, 28),
          children: [
            Row(
              children: [
                const GubernWordmark(size: 26),
                const Spacer(),
                IconButton(tooltip: 'Settings', onPressed: () => s.openSettings(context), icon: const Icon(Icons.settings_outlined)),
              ],
            ),
            const SizedBox(height: 18),
            FadeSlideIn(child: Text(name.isEmpty ? _greeting() : '${_greeting()}, ${name.split(' ').first}', style: t.headlineMedium)),
            const SizedBox(height: 6),
            FadeSlideIn(delay: const Duration(milliseconds: 40), child: ConnectionLine(hub: hub)),
            const SizedBox(height: 20),
            FadeSlideIn(delay: const Duration(milliseconds: 80), child: _NeedsYouCard(scope: s, onOpenInbox: () => widget.onGo(2))),
            const SizedBox(height: 22),
            const SectionLabel('Start'),
            FadeSlideIn(
              delay: const Duration(milliseconds: 120),
              child: GridView.count(
                crossAxisCount: 2,
                shrinkWrap: true,
                physics: const NeverScrollableScrollPhysics(),
                mainAxisSpacing: 10,
                crossAxisSpacing: 10,
                childAspectRatio: 3.1,
                children: [
                  _QuickAction(icon: Icons.chat_bubble_outline_rounded, label: 'New chat', onTap: () => s.openChat(context)),
                  _QuickAction(icon: Icons.smart_toy_outlined, label: 'Agent task', color: g.aiText, onTap: () => s.openChat(context, mode: ChatMode.agent)),
                  _QuickAction(icon: Icons.groups_outlined, label: 'Team goal', onTap: () => s.openChat(context, mode: ChatMode.team)),
                  _QuickAction(icon: Icons.travel_explore_rounded, label: 'Research', onTap: () => s.openChat(context, mode: ChatMode.research)),
                ],
              ),
            ),
            const SizedBox(height: 22),
            SectionLabel('System', trailing: _MoreLink(label: 'Operate', onTap: () => widget.onGo(3))),
            FadeSlideIn(
              delay: const Duration(milliseconds: 160),
              child: Row(
                children: [
                  Expanded(child: _HealthTile(health: _health, loading: _status == null && _health == null)),
                  const SizedBox(width: 10),
                  Expanded(child: _SpendTile(cost: _cost)),
                ],
              ),
            ),
            const SizedBox(height: 10),
            FadeSlideIn(
              delay: const Duration(milliseconds: 190),
              child: Panel(
                padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
                child: Row(
                  children: [
                    Icon(Icons.alt_route_rounded, size: 18, color: g.muted),
                    const SizedBox(width: 10),
                    Expanded(
                      child: Text(
                        _status == null ? 'Routes' : '${_status!.routes} route${_status!.routes == 1 ? '' : 's'} on ${_status!.clusterName.isEmpty ? 'this node' : _status!.clusterName}',
                        style: const TextStyle(fontSize: 14),
                      ),
                    ),
                    if (_status != null) Text(_status!.version, style: mono(context, size: 12, color: g.muted)),
                  ],
                ),
              ),
            ),
            const SizedBox(height: 22),
            SectionLabel('Recent chats', trailing: _MoreLink(label: 'All', onTap: () => widget.onGo(1))),
            if (_recent == null)
              const SkeletonList(count: 2)
            else if (_recent!.isEmpty)
              Panel(child: Text('No chats yet. Start one above.', style: TextStyle(color: g.muted)))
            else
              Panel(
                padding: EdgeInsets.zero,
                child: Column(
                  children: [
                    for (final (i, x) in hub.withPending(_recent!).take(5).indexed) ...[
                      if (i > 0) Divider(color: g.border, height: 1),
                      FadeSlideIn.indexed(
                        i,
                        child: SessionRow(
                          session: x,
                          needsApproval: hub.pending.any((a) => a.sessionId == x.id),
                          onTap: () => s.openChat(context, sessionId: x.id, title: x.title),
                        ),
                      ),
                    ],
                  ],
                ),
              ),
          ],
        ),
      ),
    );
  }
}

class _MoreLink extends StatelessWidget {
  const _MoreLink({required this.label, required this.onTap});
  final String label;
  final VoidCallback onTap;
  @override
  Widget build(BuildContext context) => InkWell(
    borderRadius: BorderRadius.circular(6),
    onTap: onTap,
    child: Padding(
      padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 2),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(label, style: TextStyle(color: context.g.brand, fontSize: 13)),
          Icon(Icons.chevron_right_rounded, size: 16, color: context.g.brand),
        ],
      ),
    ),
  );
}

class _NeedsYouCard extends StatelessWidget {
  const _NeedsYouCard({required this.scope, required this.onOpenInbox});
  final AppScope scope;
  final VoidCallback onOpenInbox;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final hub = scope.hub;
    final n = hub.pending.length;
    if (!hub.loaded) {
      return const Panel(child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [Skeleton(width: 120), SizedBox(height: 12), Skeleton(width: 220)]));
    }
    if (n == 0) {
      return Panel(
        child: Row(
          children: [
            AnimatedCheck(color: g.ok, size: 40),
            const SizedBox(width: 14),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  const Text('All clear', style: TextStyle(fontSize: 16, fontWeight: FontWeight.w600)),
                  const SizedBox(height: 2),
                  Text('Nothing is waiting for you.', style: TextStyle(color: g.muted, fontSize: 14)),
                ],
              ),
            ),
          ],
        ),
      );
    }
    final urgent = hub.pending.any((a) => a.highRisk);
    return Panel(
      borderColor: (urgent ? g.high : g.warn).withValues(alpha: .5),
      padding: const EdgeInsets.fromLTRB(16, 14, 16, 8),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              CountUp(n.toDouble(), style: TextStyle(fontSize: 28, fontWeight: FontWeight.w600, color: urgent ? g.high : g.warn, fontFamily: sansFamily)),
              const SizedBox(width: 10),
              Expanded(child: Text(n == 1 ? 'action needs you' : 'actions need you', style: const TextStyle(fontSize: 16, fontWeight: FontWeight.w500))),
              _MoreLink(label: 'Inbox', onTap: onOpenInbox),
            ],
          ),
          const SizedBox(height: 6),
          for (final a in hub.pending.take(3)) ApprovalRow(a: a, onTap: () => scope.openApproval(context, a)),
        ],
      ),
    );
  }
}

class _QuickAction extends StatelessWidget {
  const _QuickAction({required this.icon, required this.label, required this.onTap, this.color});
  final IconData icon;
  final String label;
  final VoidCallback onTap;
  final Color? color;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    return Pressable(
      onTap: onTap,
      child: Panel(
        padding: const EdgeInsets.symmetric(horizontal: 12),
        child: Row(
          children: [
            Icon(icon, size: 20, color: color ?? g.brand),
            const SizedBox(width: 10),
            Expanded(child: Text(label, style: const TextStyle(fontSize: 14, fontWeight: FontWeight.w500))),
          ],
        ),
      ),
    );
  }
}

class _HealthTile extends StatelessWidget {
  const _HealthTile({required this.health, required this.loading});
  final HealingInfo? health;
  final bool loading;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final h = health;
    final failing = h?.failing ?? 0;
    final ok = h == null || failing == 0;
    final c = h == null ? g.muted : (ok ? g.ok : g.critical);
    return Panel(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('Health', style: TextStyle(color: g.muted, fontSize: 13)),
          const SizedBox(height: 8),
          Row(
            children: [
              Icon(ok ? Icons.check_circle_outline_rounded : Icons.error_outline_rounded, size: 18, color: c),
              const SizedBox(width: 6),
              Text(h == null ? '—' : (ok ? 'Healthy' : '$failing failing'), style: TextStyle(fontSize: 16, fontWeight: FontWeight.w600, color: c)),
            ],
          ),
          const SizedBox(height: 4),
          Text(h == null ? 'No checks' : '${h.checks.length} check${h.checks.length == 1 ? '' : 's'}', style: TextStyle(color: g.muted, fontSize: 12)),
        ],
      ),
    );
  }
}

class _SpendTile extends StatelessWidget {
  const _SpendTile({required this.cost});
  final CostInfo? cost;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final c = cost;
    final color = c == null ? g.brand : (c.used > .9 ? g.critical : (c.used > .7 ? g.warn : g.brand));
    return Panel(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('Spend today', style: TextStyle(color: g.muted, fontSize: 13)),
          const SizedBox(height: 8),
          c == null
              ? const Text('—', style: TextStyle(fontSize: 16, fontWeight: FontWeight.w600))
              : CountUp(c.totalUsd, format: (v) => '\$${v.toStringAsFixed(2)}', style: const TextStyle(fontSize: 16, fontWeight: FontWeight.w600)),
          const SizedBox(height: 8),
          if (c != null && c.dailyBudget > 0)
            Meter(value: c.used, color: color, height: 4)
          else
            Text(c?.free == true ? 'Free models' : 'No budget set', style: TextStyle(color: g.muted, fontSize: 12)),
        ],
      ),
    );
  }
}

/// A compact approval line (Home's needs-you card).
class ApprovalRow extends StatelessWidget {
  const ApprovalRow({super.key, required this.a, required this.onTap});
  final ApprovalView a;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(8),
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 8),
        child: Row(
          children: [
            Icon(riskIcon(a.risk), size: 18, color: g.risk(a.risk)),
            const SizedBox(width: 10),
            Expanded(
              child: Text(a.description.isEmpty ? a.tool : a.description, maxLines: 1, overflow: TextOverflow.ellipsis, style: const TextStyle(fontSize: 14)),
            ),
            const SizedBox(width: 8),
            Countdown(a.expiresAt, style: const TextStyle(fontSize: 12)),
            Icon(Icons.chevron_right_rounded, size: 18, color: g.muted),
          ],
        ),
      ),
    );
  }
}

/// A full approval card (Inbox).
class ApprovalCard extends StatelessWidget {
  const ApprovalCard({super.key, required this.a, required this.onTap});
  final ApprovalView a;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final rc = g.risk(a.risk);
    return Pressable(
      onTap: onTap,
      child: Panel(
        borderColor: a.highRisk ? rc.withValues(alpha: .5) : null,
        padding: const EdgeInsets.fromLTRB(14, 12, 10, 12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(children: [RiskBadge(a.risk), const Spacer(), Countdown(a.expiresAt)]),
            const SizedBox(height: 10),
            Hero(
              tag: 'approval-${a.id}',
              child: Material(
                type: MaterialType.transparency,
                child: Text(
                  a.description.isEmpty ? a.tool : a.description,
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                  style: const TextStyle(fontSize: 16, fontWeight: FontWeight.w500, height: 1.35),
                ),
              ),
            ),
            if (a.command != null) ...[
              const SizedBox(height: 6),
              Text('\$ ${a.command}', maxLines: 1, overflow: TextOverflow.ellipsis, style: mono(context, size: 12.5, color: g.muted)),
            ],
            const SizedBox(height: 8),
            Row(
              children: [
                Icon(toolIcon(a.tool), size: 14, color: g.muted),
                const SizedBox(width: 5),
                Flexible(child: Text('${a.tool} · ${a.sessionId}', overflow: TextOverflow.ellipsis, style: TextStyle(color: g.muted, fontSize: 12.5))),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Inbox
// ---------------------------------------------------------------------------

class InboxTab extends StatefulWidget {
  const InboxTab({super.key, required this.scope});
  final AppScope scope;
  @override
  State<InboxTab> createState() => _InboxTabState();
}

class _InboxTabState extends State<InboxTab> {
  int _seg = 0;
  List<RunEvent>? _activity;

  Future<void> _loadActivity() async {
    try {
      final a = await widget.scope.api.activity(limit: 80);
      if (mounted) setState(() => _activity = a.reversed.toList());
    } catch (_) {
      if (mounted) setState(() => _activity = const []);
    }
  }

  @override
  Widget build(BuildContext context) {
    final s = widget.scope;
    final hub = s.hub;
    final g = context.g;
    return SafeArea(
      bottom: false,
      child: Column(
        children: [
          TabHeader(scope: s, title: 'Inbox'),
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 4, 16, 10),
            child: SizedBox(
              width: double.infinity,
              child: SegmentedButton<int>(
                showSelectedIcon: false,
                style: SegmentedButton.styleFrom(
                  backgroundColor: g.surface,
                  selectedBackgroundColor: g.selected,
                  selectedForegroundColor: g.text,
                  foregroundColor: g.muted,
                  side: BorderSide(color: g.border),
                  shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(8)),
                ),
                segments: [
                  ButtonSegment(value: 0, label: Text('Approvals${hub.pending.isEmpty ? '' : ' ${hub.pending.length}'}')),
                  ButtonSegment(value: 1, label: Text('Alerts${hub.alerts.isEmpty ? '' : ' ${hub.alerts.length}'}')),
                  const ButtonSegment(value: 2, label: Text('Activity')),
                ],
                selected: {_seg},
                onSelectionChanged: (v) {
                  HapticFeedback.selectionClick();
                  setState(() => _seg = v.first);
                  if (_seg == 2 && _activity == null) unawaited(_loadActivity());
                },
              ),
            ),
          ),
          Expanded(
            child: RefreshIndicator(
              color: g.brand,
              onRefresh: () => _seg == 2 ? _loadActivity() : hub.refresh(),
              child: AnimatedSwitcher(
                duration: Motion.base,
                child: KeyedSubtree(key: ValueKey(_seg), child: _body(context)),
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _body(BuildContext context) {
    final s = widget.scope;
    final hub = s.hub;
    final g = context.g;
    Widget scroll(List<Widget> children) => ListView(physics: const AlwaysScrollableScrollPhysics(), padding: const EdgeInsets.fromLTRB(16, 4, 16, 24), children: children);
    switch (_seg) {
      case 0:
        if (!hub.loaded) return scroll(const [SkeletonList(count: 3)]);
        if (hub.pending.isEmpty) {
          return scroll([
            EmptyState(
              icon: Icons.verified_user_outlined,
              color: g.ok,
              title: 'All clear',
              body: 'Nothing is waiting for you. When an agent wants to do something risky, it shows up here.',
            ),
          ]);
        }
        return scroll([
          for (final (i, a) in hub.pending.indexed)
            FadeSlideIn.indexed(
              i,
              key: ValueKey(a.id),
              child: Padding(padding: const EdgeInsets.only(bottom: 10), child: ApprovalCard(a: a, onTap: () => s.openApproval(context, a))),
            ),
        ]);
      case 1:
        final alerts = hub.alerts;
        if (alerts.isEmpty) {
          return scroll([const EmptyState(icon: Icons.notifications_none_rounded, title: 'No alerts', body: 'Failing health checks, SLO burn and checkpoints show up here.')]);
        }
        return scroll([
          for (final (i, a) in alerts.indexed)
            FadeSlideIn.indexed(i, key: ValueKey(a.id), child: Padding(padding: const EdgeInsets.only(bottom: 10), child: _AlertCard(a))),
        ]);
      default:
        final act = _activity;
        if (act == null) return scroll(const [SkeletonList(count: 4)]);
        if (act.isEmpty) return scroll([const EmptyState(icon: Icons.timeline_rounded, title: 'No activity yet', body: 'Tool steps, approvals and runs appear here as agents work.')]);
        return scroll([for (final (i, e) in act.indexed) FadeSlideIn.indexed(i, child: ActivityRow(e, last: i == act.length - 1))]);
    }
  }
}

class _AlertCard extends StatelessWidget {
  const _AlertCard(this.a);
  final AttentionItem a;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final c = switch (a.severity) {
      'critical' => g.critical,
      'warning' => g.warn,
      _ => g.brand,
    };
    final icon = switch (a.kind) {
      'check' => Icons.monitor_heart_outlined,
      'slo' => Icons.speed_rounded,
      'checkpoint' => Icons.flag_outlined,
      _ => Icons.notifications_none_rounded,
    };
    return Panel(
      borderColor: c.withValues(alpha: .4),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Icon(icon, color: c, size: 20),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Pill(label: a.severity[0].toUpperCase() + a.severity.substring(1), color: c),
                    const Spacer(),
                    Text(timeAgo(a.at), style: TextStyle(color: g.muted, fontSize: 12)),
                  ],
                ),
                const SizedBox(height: 8),
                Text(a.title, style: const TextStyle(fontWeight: FontWeight.w500, fontSize: 15)),
                if (a.detail.isNotEmpty) ...[const SizedBox(height: 4), Text(a.detail, style: TextStyle(color: g.muted, fontSize: 13, height: 1.4))],
              ],
            ),
          ),
        ],
      ),
    );
  }
}

/// One timeline entry: a dot on a rail, what happened, and when.
class ActivityRow extends StatelessWidget {
  const ActivityRow(this.e, {super.key, this.last = false});
  final RunEvent e;
  final bool last;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final (IconData icon, Color c, String text) = switch (e.type) {
      'run_started' => (Icons.play_arrow_rounded, g.brand, 'Run started'),
      'run_finished' => (
        e.status == 'failed' ? Icons.error_outline_rounded : (e.status == 'cancelled' ? Icons.stop_rounded : Icons.flag_outlined),
        e.status == 'failed' ? g.critical : (e.status == 'cancelled' ? g.warn : g.ok),
        'Run ${e.status.isEmpty ? 'finished' : e.status}',
      ),
      'tool_start' => (toolIcon(e.tool), g.aiText, e.label.isEmpty ? e.tool : e.label),
      'tool_end' => (
        e.status == 'failed' ? Icons.close_rounded : Icons.check_rounded,
        e.status == 'failed' ? g.critical : g.ok,
        '${e.label.isEmpty ? e.tool : e.label} ${e.status == 'failed' ? 'failed' : 'done'}',
      ),
      'approval_required' => (Icons.front_hand_outlined, g.warn, 'Approval asked: ${e.approval?['description'] ?? e.tool}'),
      'approval_decided' => (
        e.status == 'approved' ? Icons.verified_outlined : Icons.block_rounded,
        e.status == 'approved' ? g.ok : g.critical,
        '${e.status == 'approved' ? 'Approved' : 'Rejected'}${e.decidedBy.isEmpty ? '' : ' by ${e.decidedBy}'}${e.decidedVia.isEmpty ? '' : ' via ${e.decidedVia}'}',
      ),
      _ => (Icons.circle_outlined, g.muted, e.type),
    };
    return IntrinsicHeight(
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 28,
            child: Column(
              children: [
                Container(
                  width: 24,
                  height: 24,
                  decoration: BoxDecoration(color: c.withValues(alpha: .12), borderRadius: BorderRadius.circular(7)),
                  child: Icon(icon, size: 14, color: c),
                ),
                if (!last) Expanded(child: Container(width: 1, color: g.border)),
              ],
            ),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Padding(
              padding: const EdgeInsets.only(bottom: 16, top: 2),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(text, maxLines: 2, overflow: TextOverflow.ellipsis, style: const TextStyle(fontSize: 14)),
                  const SizedBox(height: 2),
                  Text('${timeAgo(e.at)} · ${e.sessionId}', style: mono(context, size: 11, color: g.muted)),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }
}
