import 'dart:async';

import 'package:flutter/material.dart';

import '../api.dart';
import '../ui/theme.dart';
import '../ui/widgets.dart';
import 'shell.dart';

class ChatsTab extends StatefulWidget {
  const ChatsTab({super.key, required this.scope});
  final AppScope scope;
  @override
  State<ChatsTab> createState() => _ChatsTabState();
}

class _ChatsTabState extends State<ChatsTab> {
  List<SessionSummary>? _all;
  String? _error;
  String _q = '';

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    try {
      final s = await widget.scope.api.sessions();
      if (mounted) setState(() => (_all = s, _error = null));
    } catch (e) {
      if (mounted) setState(() => (_all ??= const [], _error = 'Couldn\'t load chats.'));
    }
  }

  Future<void> _open({String? id, String? title}) async {
    await widget.scope.openChat(context, sessionId: id, title: title);
    unawaited(_load());
  }

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final hub = widget.scope.hub;
    final q = _q.toLowerCase();
    final list = hub.withPending(_all ?? const <SessionSummary>[]).where((s) => q.isEmpty || s.title.toLowerCase().contains(q) || s.id.contains(q)).toList();
    return Scaffold(
      floatingActionButton: FloatingActionButton.extended(
        heroTag: 'new-chat',
        onPressed: () => _open(),
        backgroundColor: g.brand,
        foregroundColor: Colors.white,
        elevation: 0,
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
        icon: const Icon(Icons.add_rounded),
        label: const Text('New chat'),
      ),
      body: SafeArea(
        bottom: false,
        child: Column(
          children: [
            TabHeader(scope: widget.scope, title: 'Chats'),
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 4, 16, 10),
              child: TextField(
                onChanged: (v) => setState(() => _q = v),
                decoration: const InputDecoration(hintText: 'Search chats', prefixIcon: Icon(Icons.search_rounded), isDense: true),
              ),
            ),
            Expanded(
              child: RefreshIndicator(
                color: g.brand,
                onRefresh: _load,
                child: _all == null
                    ? ListView(padding: const EdgeInsets.all(16), children: const [SkeletonList(count: 5)])
                    : list.isEmpty
                    ? ListView(
                        physics: const AlwaysScrollableScrollPhysics(),
                        children: [
                          if (_error != null) Padding(padding: const EdgeInsets.all(16), child: Notice(text: _error!, color: g.warn, icon: Icons.cloud_off_rounded)),
                          EmptyState(
                            icon: Icons.chat_bubble_outline_rounded,
                            title: _q.isEmpty ? 'No chats yet' : 'No matches',
                            body: _q.isEmpty ? 'Start a chat or give an agent a task. Chats from the dashboard show up here too.' : 'Try a different word.',
                          ),
                        ],
                      )
                    : ListView.separated(
                        physics: const AlwaysScrollableScrollPhysics(),
                        padding: const EdgeInsets.fromLTRB(16, 0, 16, 96),
                        itemCount: list.length,
                        separatorBuilder: (_, _) => const SizedBox(height: 8),
                        itemBuilder: (context, i) => FadeSlideIn.indexed(
                          i,
                          key: ValueKey(list[i].id),
                          child: Panel(
                            padding: EdgeInsets.zero,
                            child: SessionRow(
                              session: list[i],
                              needsApproval: hub.pending.any((a) => a.sessionId == list[i].id),
                              onTap: () => _open(id: list[i].id, title: list[i].title),
                            ),
                          ),
                        ),
                      ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// One chat in a list: title, when, and whether it is waiting on you.
class SessionRow extends StatelessWidget {
  const SessionRow({super.key, required this.session, required this.onTap, this.needsApproval = false});
  final SessionSummary session;
  final VoidCallback onTap;
  final bool needsApproval;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    return InkWell(
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(14, 12, 10, 12),
        child: Row(
          children: [
            Container(
              width: 32,
              height: 32,
              decoration: BoxDecoration(color: needsApproval ? g.warn.withValues(alpha: .12) : g.surfaceHigh, borderRadius: BorderRadius.circular(8)),
              child: Icon(
                needsApproval ? Icons.front_hand_outlined : Icons.chat_bubble_outline_rounded,
                size: 16,
                color: needsApproval ? g.warn : g.muted,
              ),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(session.title, maxLines: 1, overflow: TextOverflow.ellipsis, style: const TextStyle(fontSize: 15, fontWeight: FontWeight.w500)),
                  const SizedBox(height: 3),
                  Row(
                    children: [
                      if (needsApproval) ...[Text('Needs approval', style: TextStyle(color: g.warn, fontSize: 12)), Text('  ·  ', style: TextStyle(color: g.muted, fontSize: 12))],
                      Text(timeAgo(session.updatedAt), style: TextStyle(color: g.muted, fontSize: 12)),
                    ],
                  ),
                ],
              ),
            ),
            Icon(Icons.chevron_right_rounded, color: g.muted, size: 20),
          ],
        ),
      ),
    );
  }
}
