import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../api.dart';
import '../stepup.dart';
import '../ui/brand.dart';
import '../ui/theme.dart';
import '../ui/widgets.dart';
import 'approval.dart';

// ---------------------------------------------------------------------------
// Transcript model
// ---------------------------------------------------------------------------

sealed class Item {
  Item(this.key);
  final String key;
}

class UserItem extends Item {
  UserItem(super.key, this.text);
  final String text;
}

class AssistantItem extends Item {
  AssistantItem(super.key, this.text, {this.streaming = false});
  String text;
  bool streaming;
}

class ToolStep {
  ToolStep(this.id, this.tool, this.label);
  final String id, tool, label;
  String status = 'running'; // running | ok | failed
  int durationMs = 0;
  String output = '';
}

class ToolsItem extends Item {
  ToolsItem(super.key);
  final List<ToolStep> steps = [];
}

class ApprovalItem extends Item {
  ApprovalItem(super.key, this.approval);
  ApprovalView approval;
  String state = 'pending'; // pending | approved | rejected | expired
  String decidedBy = '';
  String decidedVia = '';
}

class SkillItem extends Item {
  SkillItem(super.key, this.name, this.hash);
  final String name, hash;
}

class StatusItem extends Item {
  StatusItem(super.key, this.text, this.tone);
  final String text;
  final String tone; // warn | danger | info
}

/// One conversation: history, the live run, and actions on it.
class ChatController extends ChangeNotifier {
  ChatController(this.api, this.sessionId, {required this.onRevoked});
  final GubernApi api;
  final VoidCallback onRevoked;
  String? sessionId;

  final List<Item> items = [];
  bool loading = false;
  bool running = false;
  String? runId;
  /// The session's latest run, when the server stopped while it ran.
  String? interruptedRunId;
  int _lastSeq = 0;
  int _n = 0;
  bool _disposed = false;

  String _k(String p) => '$p-${++_n}';

  void _changed() {
    if (!_disposed) notifyListeners();
  }

  Future<void> load() async {
    final sid = sessionId;
    if (sid == null) return;
    loading = true;
    _changed();
    try {
      final hist = await api.history(sid);
      items.insertAll(0, [for (final m in hist) m.role == 'user' ? UserItem(_k('h'), m.content) : AssistantItem(_k('h'), m.content)]);
    } on RevokedException {
      onRevoked();
      return;
    } catch (_) {
      // a new session, or no access: nothing to show
    }
    try {
      for (final a in await api.pendingApprovals(sessionId: sid)) {
        items.add(ApprovalItem(_k('ap'), a));
      }
    } catch (_) {}
    loading = false;
    _changed();
    // Reattach to a run that is still going.
    try {
      final rs = await api.runs(sessionId: sid, limit: 1);
      if (rs.isNotEmpty && rs.first.live) {
        runId = rs.first.id;
        running = true;
        _changed();
        unawaited(_follow(rs.first.id));
      } else if (rs.isNotEmpty && rs.first.status == 'interrupted') {
        interruptedRunId = rs.first.id;
        _changed();
      }
    } catch (_) {}
  }

  Future<void> send(String text, ChatMode mode, {String? check}) async {
    final sid = sessionId ??= newSessionId();
    items.add(UserItem(_k('u'), text));
    running = true;
    _changed();
    try {
      final rs = await api.submit(sid, text, mode, check: check);
      runId = rs.runId;
      _lastSeq = 0;
      await _consume(rs);
      if (!rs.done && running && rs.runId.isNotEmpty) await _follow(rs.runId);
    } on RevokedException {
      onRevoked();
    } on ApiException catch (e) {
      running = false;
      items.add(StatusItem(_k('s'), e.status == 403 ? 'This phone isn\'t allowed to run agents here.' : e.message, 'danger'));
      _changed();
    } catch (_) {
      if (runId != null && running) {
        await _follow(runId!);
      } else {
        running = false;
        items.add(StatusItem(_k('s'), 'The server could not be reached.', 'danger'));
        _changed();
      }
    }
  }

  /// Continues the interrupted run from its last checkpoint.
  Future<void> resume() async {
    final from = interruptedRunId;
    if (from == null || running) return;
    interruptedRunId = null;
    running = true;
    _changed();
    try {
      final rs = await api.resumeRun(from);
      runId = rs.runId;
      _lastSeq = 0;
      await _consume(rs);
      if (!rs.done && running && rs.runId.isNotEmpty) await _follow(rs.runId);
    } on RevokedException {
      onRevoked();
    } on ApiException catch (e) {
      running = false;
      items.add(StatusItem(_k('s'), e.status == 409 ? 'That run can no longer be resumed (${e.message}).' : e.message, 'danger'));
      _changed();
    } catch (_) {
      running = false;
      items.add(StatusItem(_k('s'), 'The server could not be reached.', 'danger'));
      _changed();
    }
  }

  Future<void> _consume(RunStream rs) async {
    await for (final ev in rs.events) {
      if (ev.type == 'done') {
        _finish(ev.status);
        return;
      }
      _apply(ev);
    }
  }

  Future<void> _follow(String id) async {
    var delay = const Duration(seconds: 1);
    while (running && runId == id && !_disposed) {
      try {
        final rs = await api.follow(id, _lastSeq);
        await _consume(rs);
        if (rs.done) return;
        delay = const Duration(seconds: 1);
      } on RevokedException {
        onRevoked();
        return;
      } on ApiException catch (e) {
        if (e.status == 403 || e.status == 404) break;
      } catch (_) {
        // network drop: retry
      }
      await Future<void>.delayed(delay);
      delay = Duration(milliseconds: (delay.inMilliseconds * 2).clamp(1000, 10000));
    }
    _finish(null);
  }

  void _finish(String? status) {
    if (!running) return;
    running = false;
    for (final i in items) {
      if (i is AssistantItem) i.streaming = false;
    }
    if (status == 'cancelled') items.add(StatusItem(_k('s'), 'Stopped.', 'warn'));
    if (status == 'failed') items.add(StatusItem(_k('s'), 'The run failed.', 'danger'));
    if (status == 'completed') items.add(StatusItem(_k('s'), 'Completed and verified.', 'ok'));
    if (status == 'partial') items.add(StatusItem(_k('s'), 'Finished, but not verified.', 'warn'));
    _changed();
  }

  void _apply(RunEvent ev) {
    if (ev.seq > 0) {
      if (ev.seq <= _lastSeq) return; // duplicate after a resume
      _lastSeq = ev.seq;
    }
    final last = items.isEmpty ? null : items.last;
    switch (ev.type) {
      case 'text':
        if (last is AssistantItem && last.streaming) {
          last.text += ev.text;
        } else {
          items.add(AssistantItem(_k('a'), ev.text, streaming: true));
        }
      case 'tool_start':
        final step = ToolStep(ev.stepId.isEmpty ? _k('st') : ev.stepId, ev.tool, ev.label.isEmpty ? ev.tool : ev.label);
        if (last is ToolsItem) {
          last.steps.add(step);
        } else {
          items.add(ToolsItem(_k('t'))..steps.add(step));
        }
      case 'tool_end':
        for (final it in items.reversed.whereType<ToolsItem>()) {
          final s = it.steps.where((s) => s.id == ev.stepId).firstOrNull;
          if (s == null) continue;
          s.status = ev.status == 'failed' ? 'failed' : (ev.status == 'waiting' ? 'running' : 'ok');
          s.durationMs = ev.durationMs;
          if (ev.output.isNotEmpty) s.output = ev.output;
          break;
        }
      case 'approval_required':
        final a = ev.approvalView;
        if (a != null && !items.any((i) => i is ApprovalItem && i.approval.id == a.id)) {
          items.add(ApprovalItem(_k('ap'), a));
        }
      case 'approval_decided':
        for (final it in items.whereType<ApprovalItem>()) {
          if (it.approval.id != ev.approvalId) continue;
          it.state = ev.status == 'approved' ? 'approved' : (ev.status == 'rejected' ? 'rejected' : 'expired');
          it.decidedBy = ev.decidedBy;
          it.decidedVia = ev.decidedVia;
        }
      case 'check_result':
        final passed = ev.status == 'passed';
        items.add(StatusItem(_k('s'), passed ? 'Check passed: ${ev.label}' : 'Check failed (${ev.text}): ${ev.label}', passed ? 'ok' : 'warn'));
      case 'skill_activated':
        if (ev.label.isNotEmpty) items.add(SkillItem(_k('sk'), ev.label, ev.skillHash));
      case 'run_finished':
        _finish(ev.status);
        return;
      default:
        return;
    }
    _changed();
  }

  /// After the approval screen closes, refresh the card from the server.
  Future<void> refreshApproval(ApprovalItem it) async {
    try {
      final a = await api.approval(it.approval.id);
      it.approval = a;
      if (a.status != 'pending') {
        it.state = a.status == 'cancelled' ? 'expired' : a.status;
        it.decidedBy = a.decidedBy ?? '';
        it.decidedVia = a.decidedVia ?? '';
      }
      _changed();
    } catch (_) {}
  }

  Future<void> stop() async {
    final id = runId;
    if (id == null) return;
    try {
      await api.cancelRun(id);
    } catch (_) {}
  }

  @override
  void dispose() {
    _disposed = true;
    running = false;
    super.dispose();
  }
}

// ---------------------------------------------------------------------------
// Screen
// ---------------------------------------------------------------------------

class ChatScreen extends StatefulWidget {
  const ChatScreen({super.key, required this.api, required this.deviceKey, required this.onRevoked, this.sessionId, this.title, this.initialMode = ChatMode.chat});
  final GubernApi api;
  final DeviceKey deviceKey;
  final VoidCallback onRevoked;
  final String? sessionId;
  final String? title;
  final ChatMode initialMode;

  @override
  State<ChatScreen> createState() => _ChatScreenState();
}

class _ChatScreenState extends State<ChatScreen> {
  late final ChatController _c = ChatController(widget.api, widget.sessionId, onRevoked: widget.onRevoked);
  final _scroll = ScrollController();
  final _input = TextEditingController();
  final _focus = FocusNode();
  late ChatMode _mode = widget.initialMode;
  final _check = TextEditingController();
  bool _showCheck = false;

  @override
  void initState() {
    super.initState();
    _c.addListener(_onChange);
    unawaited(_c.load());
    _input.addListener(() => setState(() {}));
  }

  int _pendingSeen = 0;

  void _onChange() {
    if (!mounted) return;
    final pending = _c.items.whereType<ApprovalItem>().where((a) => a.state == 'pending').length;
    if (pending > _pendingSeen) HapticFeedback.mediumImpact();
    _pendingSeen = pending;
    setState(() {});
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_scroll.hasClients) {
        _scroll.animateTo(_scroll.position.maxScrollExtent, duration: Motion.base, curve: Motion.curve);
      }
    });
  }

  @override
  void dispose() {
    _c.removeListener(_onChange);
    _c.dispose();
    _scroll.dispose();
    _input.dispose();
    _check.dispose();
    _focus.dispose();
    super.dispose();
  }

  void _send() {
    final t = _input.text.trim();
    if (t.isEmpty || _c.running) return;
    HapticFeedback.selectionClick();
    _input.clear();
    final check = _mode == ChatMode.agent && _showCheck ? _check.text.trim() : '';
    unawaited(_c.send(t, _mode, check: check.isEmpty ? null : check));
  }

  Future<void> _review(ApprovalItem it) async {
    await Navigator.push(context, MaterialPageRoute(builder: (_) => ApprovalDetailScreen(api: widget.api, deviceKey: widget.deviceKey, approval: it.approval)));
    await _c.refreshApproval(it);
  }

  String get _title {
    if (widget.title != null && cleanTitle(widget.title!).isNotEmpty) return cleanTitle(widget.title!);
    final first = _c.items.whereType<UserItem>().firstOrNull;
    final t = first == null ? '' : cleanTitle(first.text);
    return t.isEmpty ? 'New chat' : t;
  }

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final items = _c.items;
    final last = items.isEmpty ? null : items.last;
    final thinking = _c.running && !(last is AssistantItem && last.streaming);
    return Scaffold(
      appBar: AppBar(
        titleSpacing: 0,
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(_title, maxLines: 1, overflow: TextOverflow.ellipsis, style: const TextStyle(fontSize: 16, fontWeight: FontWeight.w600)),
            AnimatedSwitcher(
              duration: Motion.base,
              child: _c.running
                  ? Row(
                      key: const ValueKey('run'),
                      children: [
                        LiveDot(color: g.ai, size: 6),
                        Text('Working', style: TextStyle(color: g.aiText, fontSize: 12)),
                      ],
                    )
                  : Text(
                      _c.sessionId ?? 'Not started',
                      key: const ValueKey('id'),
                      style: mono(context, size: 11, color: g.muted),
                    ),
            ),
          ],
        ),
        bottom: PreferredSize(preferredSize: const Size.fromHeight(1), child: Divider(color: g.border)),
      ),
      body: Column(
        children: [
          Expanded(
            child: _c.loading
                ? const Padding(padding: EdgeInsets.all(16), child: SkeletonList(count: 3))
                : items.isEmpty
                ? _ChatEmpty(mode: _mode, onPick: (s) {
                    _input.text = s;
                    _focus.requestFocus();
                  })
                : ListView.builder(
                    controller: _scroll,
                    padding: const EdgeInsets.fromLTRB(16, 16, 16, 12),
                    itemCount: items.length + (thinking ? 1 : 0),
                    itemBuilder: (context, i) {
                      if (i == items.length) {
                        return const Padding(
                          padding: EdgeInsets.only(top: 4, bottom: 8),
                          child: Row(children: [_AgentAvatar(), SizedBox(width: 10), TypingDots()]),
                        );
                      }
                      final it = items[i];
                      return FadeSlideIn(
                        key: ValueKey(it.key),
                        offset: 10,
                        child: Padding(
                          padding: const EdgeInsets.only(bottom: 14),
                          child: switch (it) {
                            UserItem() => _UserBubble(it.text),
                            AssistantItem() => _AssistantMessage(it),
                            ToolsItem() => _ToolsGroup(it, live: _c.running),
                            ApprovalItem() => _InlineApproval(it, onReview: () => _review(it)),
                            SkillItem() => _SkillChip(it),
                            StatusItem() => _StatusLine(it),
                          },
                        ),
                      );
                    },
                  ),
          ),
          AnimatedSize(
            duration: Motion.base,
            curve: Motion.curve,
            child: _c.interruptedRunId == null ? const SizedBox(width: double.infinity) : _ResumeBanner(onResume: () => unawaited(_c.resume())),
          ),
          _Composer(
            controller: _input,
            focus: _focus,
            mode: _mode,
            running: _c.running,
            onMode: (m) => setState(() => _mode = m),
            check: _check,
            showCheck: _showCheck,
            onToggleCheck: () => setState(() {
              _showCheck = !_showCheck;
              if (!_showCheck) _check.clear();
            }),
            onSend: _send,
            onStop: () {
              HapticFeedback.lightImpact();
              unawaited(_c.stop());
            },
          ),
        ],
      ),
    );
  }
}

/// Offers to continue a run the server stopped in the middle of.
class _ResumeBanner extends StatelessWidget {
  const _ResumeBanner({required this.onResume});
  final VoidCallback onResume;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    return Container(
      margin: const EdgeInsets.fromLTRB(12, 0, 12, 8),
      padding: const EdgeInsets.fromLTRB(12, 10, 8, 10),
      decoration: BoxDecoration(
        color: g.warn.withValues(alpha: .08),
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: g.warn.withValues(alpha: .4)),
      ),
      child: Row(
        children: [
          Icon(Icons.pause_circle_outline_rounded, color: g.warn, size: 20),
          const SizedBox(width: 10),
          Expanded(
            child: Text(
              'The server stopped during the last run. Resume picks up where it left off; nothing that may have run is repeated.',
              style: TextStyle(fontSize: 13, height: 1.4, color: g.text),
            ),
          ),
          const SizedBox(width: 8),
          FilledButton(
            style: FilledButton.styleFrom(minimumSize: const Size(0, 38), padding: const EdgeInsets.symmetric(horizontal: 14)),
            onPressed: onResume,
            child: const Text('Resume'),
          ),
        ],
      ),
    );
  }
}

class _AgentAvatar extends StatelessWidget {
  const _AgentAvatar();
  @override
  Widget build(BuildContext context) => Container(
    width: 26,
    height: 26,
    alignment: Alignment.center,
    decoration: BoxDecoration(color: context.g.surface, borderRadius: BorderRadius.circular(7), border: Border.all(color: context.g.border)),
    child: const GubernMark(size: 16),
  );
}

class _UserBubble extends StatelessWidget {
  const _UserBubble(this.text);
  final String text;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    return Align(
      alignment: Alignment.centerRight,
      child: ConstrainedBox(
        constraints: BoxConstraints(maxWidth: MediaQuery.of(context).size.width * .8),
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
          decoration: BoxDecoration(color: g.surfaceHigh, borderRadius: BorderRadius.circular(12), border: Border.all(color: g.border)),
          child: SelectableText(text, style: const TextStyle(fontSize: 15, height: 1.5)),
        ),
      ),
    );
  }
}

class _AssistantMessage extends StatelessWidget {
  const _AssistantMessage(this.it);
  final AssistantItem it;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    // Fenced code blocks become code panels; the rest is plain text.
    final parts = it.text.split('```');
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const _AgentAvatar(),
        const SizedBox(width: 10),
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              for (var i = 0; i < parts.length; i++)
                if (i.isOdd)
                  Padding(padding: const EdgeInsets.symmetric(vertical: 6), child: CodeBlock(_stripLang(parts[i])))
                else if (parts[i].trim().isNotEmpty)
                  _AgentText(parts[i].trim(), caret: it.streaming && i == parts.length - 1),
              if (it.streaming && it.text.trim().isEmpty) _Caret(color: g.aiText),
            ],
          ),
        ),
      ],
    );
  }

  static String _stripLang(String s) {
    final nl = s.indexOf('\n');
    if (nl > 0 && nl < 20 && !s.substring(0, nl).contains(' ')) return s.substring(nl + 1).trimRight();
    return s.trim();
  }
}

/// The agent's plain-text transcript, line by line: shell commands in mono,
/// approval markers and results as status chips, prose as text.
class _AgentText extends StatelessWidget {
  const _AgentText(this.text, {this.caret = false});
  final String text;
  final bool caret;

  static final _approval = RegExp(r'^\[APPROVAL_REQUIRED\|([^|\]]*)\|([^\]]*)\]\s*(.*)$');
  static final _actionRef = RegExp(r'\s*\(action [0-9a-f]+\)');

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final out = <Widget>[];
    final prose = <String>[];
    void flush() {
      final p = prose.join('\n').trim();
      prose.clear();
      if (p.isNotEmpty) out.add(SelectableText(p, style: TextStyle(fontSize: 15, height: 1.6, color: g.text)));
    }

    for (final raw in text.split('\n')) {
      final l = raw.trim();
      final m = _approval.firstMatch(l);
      if (l.startsWith(r'$ ')) {
        flush();
        out.add(Padding(padding: const EdgeInsets.symmetric(vertical: 3), child: Text(l, style: mono(context, size: 13))));
      } else if (m != null) {
        flush();
        final desc = m.group(3)!.replaceAll(_actionRef, '');
        out.add(
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 4),
            child: Wrap(
              spacing: 6,
              runSpacing: 4,
              crossAxisAlignment: WrapCrossAlignment.center,
              children: [
                Pill(label: 'Approval asked', color: g.warn, icon: Icons.front_hand_outlined),
                RiskBadge(m.group(1)!),
                if (desc.isNotEmpty) Text(desc, style: TextStyle(color: g.muted, fontSize: 13)),
              ],
            ),
          ),
        );
      } else if (l.startsWith('📂')) {
        flush();
        out.add(
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 2),
            child: Row(
              children: [
                Icon(Icons.folder_open_outlined, size: 14, color: g.muted),
                const SizedBox(width: 6),
                Expanded(
                  child: Text(l.replaceFirst('📂', '').trim(), maxLines: 1, overflow: TextOverflow.ellipsis, style: mono(context, size: 11.5, color: g.muted)),
                ),
              ],
            ),
          ),
        );
      } else if (l.startsWith('✓') || l.startsWith('✗') || l.startsWith('✔') || l.startsWith('✘')) {
        flush();
        final ok = l.startsWith('✓') || l.startsWith('✔');
        out.add(
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 3),
            child: Pill(label: l.substring(1).trim(), color: ok ? g.ok : g.critical, icon: ok ? Icons.check_rounded : Icons.close_rounded),
          ),
        );
      } else {
        prose.add(raw);
      }
    }
    flush();
    if (caret) out.add(_Caret(color: g.aiText));
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: out);
  }
}

/// A blinking text caret while the answer streams in.
class _Caret extends StatefulWidget {
  const _Caret({required this.color});
  final Color color;
  @override
  State<_Caret> createState() => _CaretState();
}

class _CaretState extends State<_Caret> with SingleTickerProviderStateMixin {
  late final AnimationController _c = AnimationController(vsync: this, duration: const Duration(milliseconds: 900));

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (!reducedMotion(context) && !_c.isAnimating) _c.repeat(reverse: true);
  }

  @override
  void dispose() {
    _c.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => FadeTransition(
    opacity: _c,
    child: Container(margin: const EdgeInsets.only(left: 2), width: 7, height: 16, color: widget.color),
  );
}

class _ToolsGroup extends StatefulWidget {
  const _ToolsGroup(this.it, {required this.live});
  final ToolsItem it;
  final bool live;
  @override
  State<_ToolsGroup> createState() => _ToolsGroupState();
}

class _ToolsGroupState extends State<_ToolsGroup> {
  bool _open = false;
  String? _expanded;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final steps = widget.it.steps;
    final runningNow = steps.any((s) => s.status == 'running');
    final failed = steps.where((s) => s.status == 'failed').length;
    final open = _open || runningNow;
    return Padding(
      padding: const EdgeInsets.only(left: 36),
      child: Container(
        decoration: BoxDecoration(
          color: g.surface,
          borderRadius: BorderRadius.circular(10),
          border: Border.all(color: runningNow ? g.ai.withValues(alpha: .5) : g.border),
        ),
        child: Column(
          children: [
            InkWell(
              borderRadius: BorderRadius.circular(10),
              onTap: () => setState(() => _open = !_open),
              child: Padding(
                padding: const EdgeInsets.fromLTRB(12, 10, 8, 10),
                child: Row(
                  children: [
                    Icon(Icons.bolt_rounded, size: 16, color: g.aiText),
                    const SizedBox(width: 8),
                    Expanded(
                      child: Text(
                        runningNow
                            ? 'Running ${steps.lastWhere((s) => s.status == 'running').label}'
                            : 'Used ${steps.length} tool${steps.length == 1 ? '' : 's'}${failed > 0 ? ' · $failed failed' : ''}',
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: TextStyle(fontSize: 13, color: g.text, fontWeight: FontWeight.w500),
                      ),
                    ),
                    AnimatedRotation(turns: open ? .5 : 0, duration: Motion.base, child: Icon(Icons.expand_more_rounded, size: 20, color: g.muted)),
                  ],
                ),
              ),
            ),
            AnimatedSize(
              duration: Motion.base,
              curve: Motion.curve,
              alignment: Alignment.topCenter,
              child: !open
                  ? const SizedBox(width: double.infinity)
                  : Column(
                      children: [
                        Divider(color: g.border, height: 1),
                        for (final s in steps)
                          InkWell(
                            onTap: s.output.isEmpty ? null : () => setState(() => _expanded = _expanded == s.id ? null : s.id),
                            child: Padding(
                              padding: const EdgeInsets.fromLTRB(12, 9, 12, 9),
                              child: Column(
                                crossAxisAlignment: CrossAxisAlignment.start,
                                children: [
                                  Row(
                                    children: [
                                      SizedBox.square(
                                        dimension: 16,
                                        child: AnimatedSwitcher(
                                          duration: Motion.base,
                                          child: s.status == 'running'
                                              ? CircularProgressIndicator(key: const ValueKey('r'), strokeWidth: 1.8, color: g.aiText)
                                              : Icon(
                                                  s.status == 'failed' ? Icons.close_rounded : Icons.check_rounded,
                                                  key: ValueKey(s.status),
                                                  size: 16,
                                                  color: s.status == 'failed' ? g.critical : g.ok,
                                                ),
                                        ),
                                      ),
                                      const SizedBox(width: 10),
                                      Icon(toolIcon(s.tool), size: 15, color: g.muted),
                                      const SizedBox(width: 6),
                                      Expanded(
                                        child: Text(s.label, maxLines: 1, overflow: TextOverflow.ellipsis, style: mono(context, size: 12.5)),
                                      ),
                                      if (s.durationMs > 0) Text(_dur(s.durationMs), style: mono(context, size: 11, color: g.muted)),
                                    ],
                                  ),
                                  if (_expanded == s.id) ...[const SizedBox(height: 8), CodeBlock(s.output, maxLines: 16)],
                                ],
                              ),
                            ),
                          ),
                      ],
                    ),
            ),
          ],
        ),
      ),
    );
  }

  static String _dur(int ms) => ms < 1000 ? '${ms}ms' : '${(ms / 1000).toStringAsFixed(1)}s';
}

class _InlineApproval extends StatelessWidget {
  const _InlineApproval(this.it, {required this.onReview});
  final ApprovalItem it;
  final VoidCallback onReview;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final a = it.approval;
    final pending = it.state == 'pending';
    final rc = g.risk(a.risk);
    return Padding(
      padding: const EdgeInsets.only(left: 36),
      child: AnimatedContainer(
        duration: Motion.base,
        decoration: BoxDecoration(
          color: g.surface,
          borderRadius: BorderRadius.circular(10),
          border: Border.all(color: pending ? rc.withValues(alpha: .55) : g.border),
        ),
        padding: const EdgeInsets.all(12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                RiskBadge(a.risk),
                const SizedBox(width: 8),
                Expanded(child: Text('Needs approval', style: TextStyle(color: g.muted, fontSize: 12))),
                if (pending) Countdown(a.expiresAt, style: const TextStyle(fontSize: 12)),
              ],
            ),
            const SizedBox(height: 8),
            Text(a.description.isEmpty ? a.tool : a.description, style: const TextStyle(fontWeight: FontWeight.w500, fontSize: 15, height: 1.4)),
            if (a.command != null) ...[const SizedBox(height: 8), Text('\$ ${a.command}', maxLines: 2, overflow: TextOverflow.ellipsis, style: mono(context, size: 12.5, color: g.muted))],
            const SizedBox(height: 10),
            AnimatedSwitcher(
              duration: Motion.base,
              child: pending
                  ? SizedBox(
                      key: const ValueKey('p'),
                      height: 40,
                      child: FilledButton.icon(
                        style: FilledButton.styleFrom(minimumSize: const Size(0, 40), backgroundColor: rc),
                        onPressed: onReview,
                        icon: const Icon(Icons.fingerprint_rounded, size: 18),
                        label: const Text('Review'),
                      ),
                    )
                  : Pill(
                      key: ValueKey(it.state),
                      label: switch (it.state) {
                        'approved' => 'Approved${it.decidedBy.isEmpty ? '' : ' by ${it.decidedBy}'}${it.decidedVia.isEmpty ? '' : ' via ${it.decidedVia}'}',
                        'rejected' => 'Rejected${it.decidedBy.isEmpty ? '' : ' by ${it.decidedBy}'}',
                        _ => 'Expired',
                      },
                      color: switch (it.state) {
                        'approved' => g.ok,
                        'rejected' => g.critical,
                        _ => g.muted,
                      },
                      icon: switch (it.state) {
                        'approved' => Icons.check_circle_outline_rounded,
                        'rejected' => Icons.block_rounded,
                        _ => Icons.timer_off_outlined,
                      },
                    ),
            ),
          ],
        ),
      ),
    );
  }
}

class _SkillChip extends StatelessWidget {
  const _SkillChip(this.it);
  final SkillItem it;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    return Padding(
      padding: const EdgeInsets.only(left: 36),
      child: Align(
        alignment: Alignment.centerLeft,
        child: Semantics(
          label: 'Using skill ${it.name}',
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 4),
            decoration: BoxDecoration(color: g.ai.withValues(alpha: .15), borderRadius: BorderRadius.circular(6)),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(Icons.menu_book_outlined, size: 14, color: g.aiText),
                const SizedBox(width: 6),
                Text('Using skill ', style: TextStyle(color: g.aiText, fontSize: 12.5)),
                Text(it.name, style: mono(context, size: 12, color: g.aiText, weight: FontWeight.w500)),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class _StatusLine extends StatelessWidget {
  const _StatusLine(this.it);
  final StatusItem it;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final c = switch (it.tone) {
      'danger' => g.critical,
      'warn' => g.warn,
      'ok' => g.ok,
      _ => g.muted,
    };
    return Center(
      child: Pill(label: it.text, color: c, icon: switch (it.tone) {
          'danger' => Icons.error_outline_rounded,
          'ok' => Icons.verified_outlined,
          _ => Icons.info_outline_rounded,
        }),
    );
  }
}

class _ChatEmpty extends StatelessWidget {
  const _ChatEmpty({required this.mode, required this.onPick});
  final ChatMode mode;
  final ValueChanged<String> onPick;

  static const _ideas = {
    ChatMode.chat: ['Summarise what my agents did today', 'Explain the last failed run'],
    ChatMode.agent: ['Run the tests and fix what fails', 'Check disk usage on the build box'],
    ChatMode.team: ['Plan and ship the README refresh', 'Review open TODOs and triage them'],
    ChatMode.research: ['Compare vector databases for 10M rows', 'What changed in Go 1.26?'],
  };

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    return ListView(
      padding: const EdgeInsets.fromLTRB(20, 48, 20, 20),
      children: [
        const Center(child: AnimatedGubernMark(size: 44)),
        const SizedBox(height: 18),
        Center(child: Text(mode.hint, style: Theme.of(context).textTheme.titleLarge)),
        const SizedBox(height: 6),
        Center(
          child: Text(
            'Risky steps pause for your approval.',
            style: TextStyle(color: g.muted),
          ),
        ),
        const SizedBox(height: 26),
        for (final (i, s) in _ideas[mode]!.indexed)
          FadeSlideIn.indexed(
            i,
            key: ValueKey('$mode$s'),
            child: Padding(
              padding: const EdgeInsets.only(bottom: 8),
              child: Panel(
                onTap: () => onPick(s),
                padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
                child: Row(
                  children: [
                    Expanded(child: Text(s, style: const TextStyle(fontSize: 14))),
                    Icon(Icons.north_west_rounded, size: 16, color: g.muted),
                  ],
                ),
              ),
            ),
          ),
      ],
    );
  }
}

class _Composer extends StatelessWidget {
  const _Composer({
    required this.controller,
    required this.focus,
    required this.mode,
    required this.running,
    required this.onMode,
    required this.check,
    required this.showCheck,
    required this.onToggleCheck,
    required this.onSend,
    required this.onStop,
  });
  final TextEditingController controller;
  /// Agent mode's optional completion check ("done when this passes").
  final TextEditingController check;
  final bool showCheck;
  final VoidCallback onToggleCheck;
  final FocusNode focus;
  final ChatMode mode;
  final bool running;
  final ValueChanged<ChatMode> onMode;
  final VoidCallback onSend, onStop;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final canSend = controller.text.trim().isNotEmpty && !running;
    return Container(
      decoration: BoxDecoration(color: g.rail, border: Border(top: BorderSide(color: g.border))),
      child: SafeArea(
        top: false,
        child: Padding(
          padding: const EdgeInsets.fromLTRB(12, 8, 12, 10),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              SizedBox(
                height: 34,
                child: ListView(
                  scrollDirection: Axis.horizontal,
                  children: [
                    for (final m in ChatMode.values)
                      Padding(
                        padding: const EdgeInsets.only(right: 6),
                        child: _ModeChip(mode: m, selected: m == mode, onTap: () {
                          HapticFeedback.selectionClick();
                          onMode(m);
                        }),
                      ),
                    if (mode == ChatMode.agent)
                      Semantics(
                        button: true,
                        selected: showCheck,
                        label: 'Completion check',
                        child: GestureDetector(
                          onTap: onToggleCheck,
                          child: Container(
                            padding: const EdgeInsets.symmetric(horizontal: 10),
                            decoration: BoxDecoration(
                              color: showCheck ? g.ok.withValues(alpha: .12) : Colors.transparent,
                              borderRadius: BorderRadius.circular(8),
                              border: Border.all(color: showCheck ? g.ok.withValues(alpha: .5) : g.border),
                            ),
                            child: Row(
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                Icon(Icons.fact_check_outlined, size: 15, color: showCheck ? g.ok : g.muted),
                                const SizedBox(width: 6),
                                Text('Check', style: TextStyle(fontSize: 13, color: showCheck ? g.text : g.muted)),
                              ],
                            ),
                          ),
                        ),
                      ),
                  ],
                ),
              ),
              AnimatedSize(
                duration: Motion.base,
                curve: Motion.curve,
                child: mode == ChatMode.agent && showCheck
                    ? Padding(
                        padding: const EdgeInsets.only(top: 8),
                        child: TextField(
                          controller: check,
                          style: mono(context, size: 13),
                          autocorrect: false,
                          enableSuggestions: false,
                          decoration: InputDecoration(
                            isDense: true,
                            hintText: 'Done when this passes, e.g. go test ./...',
                            prefixIcon: Icon(Icons.fact_check_outlined, size: 18, color: g.ok),
                          ),
                        ),
                      )
                    : const SizedBox(width: double.infinity),
              ),
              const SizedBox(height: 8),
              Container(
                decoration: BoxDecoration(color: g.surface, borderRadius: BorderRadius.circular(12), border: Border.all(color: g.border)),
                padding: const EdgeInsets.fromLTRB(14, 2, 6, 2),
                child: Row(
                  crossAxisAlignment: CrossAxisAlignment.end,
                  children: [
                    Expanded(
                      child: TextField(
                        controller: controller,
                        focusNode: focus,
                        minLines: 1,
                        maxLines: 6,
                        textInputAction: TextInputAction.newline,
                        style: const TextStyle(fontSize: 15, height: 1.45),
                        decoration: InputDecoration(
                          hintText: mode.hint,
                          filled: false,
                          border: InputBorder.none,
                          enabledBorder: InputBorder.none,
                          focusedBorder: InputBorder.none,
                          contentPadding: const EdgeInsets.symmetric(vertical: 12),
                        ),
                      ),
                    ),
                    Padding(
                      padding: const EdgeInsets.only(bottom: 4),
                      child: AnimatedSwitcher(
                        duration: Motion.base,
                        transitionBuilder: (c, a) => ScaleTransition(scale: a, child: FadeTransition(opacity: a, child: c)),
                        child: running
                            ? IconButton(
                                key: const ValueKey('stop'),
                                tooltip: 'Stop',
                                onPressed: onStop,
                                style: IconButton.styleFrom(backgroundColor: g.surfaceHigh, foregroundColor: g.text),
                                icon: const Icon(Icons.stop_rounded),
                              )
                            : IconButton(
                                key: const ValueKey('send'),
                                tooltip: 'Send',
                                onPressed: canSend ? onSend : null,
                                style: IconButton.styleFrom(
                                  backgroundColor: canSend ? g.brand : g.surfaceHigh,
                                  foregroundColor: canSend ? Colors.white : g.muted,
                                  disabledBackgroundColor: g.surfaceHigh,
                                ),
                                icon: const Icon(Icons.arrow_upward_rounded),
                              ),
                      ),
                    ),
                  ],
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _ModeChip extends StatelessWidget {
  const _ModeChip({required this.mode, required this.selected, required this.onTap});
  final ChatMode mode;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final icon = switch (mode) {
      ChatMode.chat => Icons.chat_bubble_outline_rounded,
      ChatMode.agent => Icons.smart_toy_outlined,
      ChatMode.team => Icons.groups_outlined,
      ChatMode.research => Icons.travel_explore_rounded,
    };
    return Semantics(
      selected: selected,
      button: true,
      child: GestureDetector(
        onTap: onTap,
        child: AnimatedContainer(
          duration: Motion.base,
          curve: Motion.curve,
          padding: const EdgeInsets.symmetric(horizontal: 10),
          decoration: BoxDecoration(
            color: selected ? g.selected : Colors.transparent,
            borderRadius: BorderRadius.circular(8),
            border: Border.all(color: selected ? g.brand.withValues(alpha: .6) : g.border),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(icon, size: 15, color: selected ? g.brand : g.muted),
              const SizedBox(width: 6),
              Text(mode.label, style: TextStyle(fontSize: 13, color: selected ? g.text : g.muted, fontWeight: selected ? FontWeight.w500 : FontWeight.w400)),
            ],
          ),
        ),
      ),
    );
  }
}
