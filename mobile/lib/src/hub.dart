import 'dart:async';

import 'package:flutter/foundation.dart';

import 'api.dart';

/// The app's live picture of the server, polled while the app is open:
/// who this phone is, what needs it, and whether the server is reachable.
/// Screens listen to it; a revoked credential ends the session.
class Hub extends ChangeNotifier {
  Hub(this.api, {required this.onRevoked, this.interval = const Duration(seconds: 5)});

  final GubernApi api;
  final VoidCallback onRevoked;
  final Duration interval;

  Me? me;
  List<ApprovalView> pending = const [];
  List<AttentionItem> attention = const [];
  bool loaded = false;
  bool online = true;
  DateTime? updated;
  Timer? _t;
  bool _disposed = false;

  String get host => api.creds?.server.host ?? '';

  /// Attention items that are not approvals (checks, SLOs, checkpoints).
  List<AttentionItem> get alerts => attention.where((a) => a.kind != 'approval').toList();

  /// [sessions] plus any session that is waiting on an approval but not
  /// listed yet (the server lists a session once its run ends).
  List<SessionSummary> withPending(List<SessionSummary> sessions) {
    final known = {for (final s in sessions) s.id};
    final extra = <SessionSummary>[];
    for (final a in pending) {
      if (known.add(a.sessionId)) extra.add(SessionSummary.local(a.sessionId, a.description.isEmpty ? a.tool : a.description));
    }
    return [...extra, ...sessions];
  }

  int get needsYou => pending.length + alerts.where((a) => a.severity == 'critical' || a.severity == 'warning').length;

  void start() {
    unawaited(refresh());
    _t = Timer.periodic(interval, (_) => unawaited(refresh()));
  }

  Future<void> refresh() async {
    try {
      final results = await Future.wait([
        api.pendingApprovals(),
        api.attention().catchError((_) => <AttentionItem>[]),
        if (me == null) api.me().then<Object?>((m) => m).catchError((_) => null),
      ]);
      final p = results[0] as List<ApprovalView>..sort((a, b) => a.expiresAt.compareTo(b.expiresAt));
      pending = p;
      attention = results[1] as List<AttentionItem>;
      if (results.length > 2 && results[2] is Me) me = results[2] as Me;
      online = true;
      updated = DateTime.now();
    } on RevokedException {
      _t?.cancel();
      onRevoked();
      return;
    } catch (_) {
      online = false;
    }
    loaded = true;
    if (!_disposed) notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    _t?.cancel();
    super.dispose();
  }
}
