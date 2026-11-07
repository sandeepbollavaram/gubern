import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'credential_store.dart';
import 'pairing_link.dart';
import 'pinning.dart';
import 'stepup.dart';

/// The device credential was revoked (or the device was unpaired): the app
/// has already wiped it and must return to pairing.
class RevokedException implements Exception {
  @override
  String toString() => 'This phone is no longer paired with Gubern.';
}

/// The server refused a request.
class ApiException implements Exception {
  ApiException(this.status, this.reason, this.message);
  final int status;
  final String? reason;
  final String message;
  @override
  String toString() => message;
}

/// One approval as the app shows it.
class ApprovalView {
  ApprovalView.fromJson(Map<String, dynamic> j)
    : id = j['id'] as String,
      sessionId = j['session_id'] as String? ?? '',
      tool = j['tool'] as String? ?? '',
      risk = j['risk'] as String? ?? '',
      description = j['description'] as String? ?? '',
      command = j['command'] as String?,
      preview = j['preview'] as String?,
      workspace = j['workspace'] as String?,
      actionHash = j['action_hash'] as String? ?? '',
      status = j['status'] as String? ?? 'pending',
      expiresAt = DateTime.tryParse(j['expires_at'] as String? ?? '') ?? DateTime.now(),
      decidedBy = j['decided_by'] as String?,
      decidedVia = j['decided_via'] as String?;

  final String id, sessionId, tool, risk, description, actionHash, status;
  final String? command, preview, workspace, decidedBy, decidedVia;
  final DateTime expiresAt;

  bool get highRisk => risk.toUpperCase().startsWith('HIGH') || risk.toUpperCase().startsWith('CRITICAL');

  /// The start of the action hash, shown so the person can compare it with
  /// what the dashboard or Telegram shows for the same action.
  String get fingerprint => actionHash.length >= 16 ? actionHash.substring(0, 16) : actionHash;
}

/// HTTP client for the Gubern API, pinned to the paired server's
/// certificate. A 401 wipes the stored credential (credential revocation).
class GubernApi {
  GubernApi(this.creds, this.store, {HttpClient? client}) : _client = client ?? pinnedClient(creds?.fingerprint);

  /// Null until paired.
  Credentials? creds;
  final CredentialStore store;
  final HttpClient _client;

  /// Pairs this phone: redeems the code with the device's public key, and
  /// stores the returned credential securely.
  static Future<Credentials> pair(PairingLink link, DeviceKey key, CredentialStore store, {required String label, HttpClient? client}) async {
    final api = GubernApi(null, store, client: client ?? pinnedClient(link.fingerprint));
    final body = await api._send('POST', link.server.resolve('/api/pairing/redeem'), {
      'code': link.code,
      'label': label,
      'public_key': await key.publicKey(),
    }, auth: false);
    final c = Credentials(
      server: link.server,
      deviceId: body['device_id'] as String,
      secret: body['secret'] as String,
      fingerprint: link.fingerprint,
      principalId: body['principal_id'] as String?,
    );
    await store.save(c);
    return c;
  }

  Future<Map<String, dynamic>> _send(String method, Uri url, Object? body, {bool auth = true}) async {
    final req = await _client.openUrl(method, url);
    req.headers.set(HttpHeaders.acceptHeader, 'application/json');
    if (auth) {
      final c = creds;
      if (c == null) throw RevokedException();
      req.headers.set(HttpHeaders.authorizationHeader, 'Bearer ${c.secret}');
    }
    if (body != null) {
      req.headers.contentType = ContentType.json;
      req.add(utf8.encode(jsonEncode(body)));
    }
    final res = await req.close();
    final text = await res.transform(utf8.decoder).join();
    Map<String, dynamic> json = {};
    try {
      final decoded = jsonDecode(text);
      if (decoded is Map<String, dynamic>) json = decoded;
    } on FormatException {
      // non-JSON body
    }
    if (res.statusCode == HttpStatus.unauthorized && auth) {
      await store.wipe();
      creds = null;
      throw RevokedException();
    }
    if (res.statusCode >= 300) {
      throw ApiException(res.statusCode, json['reason'] as String?, (json['error'] as String?) ?? 'Request failed (${res.statusCode})');
    }
    return json;
  }

  Uri _url(String path) => creds!.server.resolve(path);

  Future<List<ApprovalView>> pendingApprovals({String? sessionId}) async {
    final q = sessionId == null ? '' : '&session_id=${Uri.encodeQueryComponent(sessionId)}';
    final body = await _send('GET', _url('/api/approvals?status=pending$q'), null);
    return [for (final a in (body['approvals'] as List? ?? const [])) ApprovalView.fromJson(a as Map<String, dynamic>)];
  }

  Future<ApprovalView> approval(String id) async => ApprovalView.fromJson(await _send('GET', _url('/api/approvals/$id'), null));

  /// Rejects: no step-up needed.
  Future<ApprovalView> reject(ApprovalView a) async {
    final body = await _send('POST', _url('/api/approvals/${a.id}/decision'), {'approved': false, 'action_hash': a.actionHash});
    return ApprovalView.fromJson(body['approval'] as Map<String, dynamic>);
  }

  /// Approves with a signed step-up: a fresh server challenge, signed by the
  /// hardware key (which asks for biometrics) over this approval's action
  /// hash and the decision.
  Future<ApprovalView> approve(ApprovalView a, DeviceKey key) async {
    final ch = await _send('POST', _url('/api/stepup/challenge'), {'approval_id': a.id});
    if ((ch['action_hash'] as String?)?.toLowerCase() != a.actionHash.toLowerCase()) {
      throw ApiException(409, 'action-mismatch', 'The action changed since you opened it. Review it again.');
    }
    final msg = stepUpMessage(
      challengeId: ch['challenge_id'] as String,
      nonce: ch['nonce'] as String,
      approvalId: a.id,
      actionHash: a.actionHash,
      approve: true,
    );
    final sig = await key.sign(msg, reason: 'Approve ${a.tool}');
    final body = await _send('POST', _url('/api/approvals/${a.id}/decision'), {
      'approved': true,
      'action_hash': a.actionHash,
      'stepup': {'challenge_id': ch['challenge_id'], 'signature': sig},
    });
    return ApprovalView.fromJson(body['approval'] as Map<String, dynamic>);
  }
}

// ---------------------------------------------------------------------------
// Everything the dashboard reads, for the phone. The server enforces every
// scope; a 403 here just means this phone's ceiling doesn't cover it.
// ---------------------------------------------------------------------------

DateTime _time(Object? v) {
  if (v is int) return DateTime.fromMillisecondsSinceEpoch(v);
  if (v is String) return DateTime.tryParse(v)?.toLocal() ?? DateTime.now();
  return DateTime.now();
}

double _num(Object? v) => v is num ? v.toDouble() : 0;

class Me {
  Me.fromJson(Map<String, dynamic> j)
    : principalId = j['principal_id'] as String? ?? '',
      kind = j['kind'] as String? ?? '',
      name = j['name'] as String? ?? '',
      scopes = [for (final s in (j['scopes'] as List? ?? const [])) '$s'];
  final String principalId, kind, name;
  final List<String> scopes;
  bool can(String scope) => scopes.contains(scope);
}

class SessionSummary {
  SessionSummary.fromJson(Map<String, dynamic> j)
    : id = j['session_id'] as String? ?? '',
      summary = j['summary'] as String? ?? '',
      updatedAt = _time(j['updated_at']);
  SessionSummary.local(this.id, this.summary) : updatedAt = DateTime.now();
  final String id, summary;
  final DateTime updatedAt;
  String get title => cleanTitle(summary).isEmpty ? id : cleanTitle(summary);
}

/// A chat title without the mode prefix or inline tool directives.
String cleanTitle(String s) {
  var t = s.replaceAll(RegExp(r'\[\[.*?\]\]', dotAll: true), ' ');
  // Summaries are cut short, which can leave a directive unclosed.
  final open = t.indexOf('[[');
  if (open >= 0) t = t.substring(0, open);
  t = t.trim();
  for (final p in const ['/agent ', '/team ', 'research ']) {
    if (t.toLowerCase().startsWith(p)) t = t.substring(p.length);
  }
  t = t.replaceAll(RegExp(r'\s+'), ' ').trim();
  return t.isEmpty ? t : t[0].toUpperCase() + t.substring(1);
}

class HistoryMessage {
  HistoryMessage.fromJson(Map<String, dynamic> j)
    : role = j['role'] as String? ?? 'assistant',
      content = j['content'] as String? ?? '',
      at = _time(j['timestamp']);
  final String role, content;
  final DateTime at;
}

class AttentionItem {
  AttentionItem.fromJson(Map<String, dynamic> j)
    : id = j['id'] as String? ?? '',
      kind = j['kind'] as String? ?? '',
      severity = j['severity'] as String? ?? 'info',
      title = j['title'] as String? ?? '',
      detail = j['detail'] as String? ?? '',
      href = j['href'] as String? ?? '',
      at = _time(j['at']),
      expiresAt = j['expires_at'] == null ? null : _time(j['expires_at']);
  final String id, kind, severity, title, detail, href;
  final DateTime at;
  final DateTime? expiresAt;

  /// The approval id when this item is an approval (href ending in the id).
  String? get approvalId {
    if (kind != 'approval') return null;
    final i = href.lastIndexOf('/');
    return i >= 0 && i < href.length - 1 ? href.substring(i + 1) : id;
  }
}

class StatusInfo {
  StatusInfo.fromJson(Map<String, dynamic> j)
    : version = j['version'] as String? ?? '',
      clusterName = j['cluster_name'] as String? ?? '',
      nodeId = j['node_id'] as String? ?? '',
      tlsProvider = j['tls_provider'] as String? ?? '',
      secretBackend = j['secret_backend'] as String? ?? '',
      workingDir = j['working_dir'] as String? ?? '',
      routes = (j['routes'] as num?)?.toInt() ?? 0,
      routeNames = [for (final r in (j['route_names'] as List? ?? const [])) '$r'],
      pluginCount = (j['plugin_count'] as num?)?.toInt() ?? 0,
      auditCount = (j['audit_entry_count'] as num?)?.toInt() ?? 0,
      policyDefaultAllow = j['policy_default'] as bool? ?? false;
  final String version, clusterName, nodeId, tlsProvider, secretBackend, workingDir;
  final int routes, pluginCount, auditCount;
  final List<String> routeNames;
  final bool policyDefaultAllow;
}

class CostInfo {
  CostInfo.fromJson(Map<String, dynamic> j)
    : provider = j['provider'] as String? ?? '',
      totalUsd = _num(j['total_usd']),
      requestsToday = (j['requests_today'] as num?)?.toInt() ?? 0,
      dailyBudget = _num(j['daily_budget']),
      remaining = _num(j['remaining_budget']),
      free = j['free'] as bool? ?? false;
  final String provider;
  final double totalUsd, dailyBudget, remaining;
  final int requestsToday;
  final bool free;
  double get used => dailyBudget <= 0 ? 0 : (totalUsd / dailyBudget).clamp(0, 1).toDouble();
}

class HealthCheck {
  HealthCheck.fromJson(Map<String, dynamic> j)
    : name = j['name'] as String? ?? '',
      healthy = j['healthy'] as bool? ?? false,
      latencyMs = _num(j['latency_ms']),
      failures = (j['consecutive_failures'] as num?)?.toInt() ?? 0,
      lastCheck = _time(j['last_check']);
  final String name;
  final bool healthy;
  final double latencyMs;
  final int failures;
  final DateTime lastCheck;
}

class HealingInfo {
  HealingInfo.fromJson(Map<String, dynamic> j)
    : healthy = j['healthy'] as bool? ?? true,
      checks = [for (final c in (j['checks'] as List? ?? const [])) HealthCheck.fromJson(c as Map<String, dynamic>)],
      sloAlerts = (j['slo_alerts'] as List? ?? const []).length,
      recoveries = ((j['recovery_stats'] as Map?)?['actions_executed'] as num?)?.toInt() ?? 0;
  final bool healthy;
  final List<HealthCheck> checks;
  final int sloAlerts, recoveries;
  int get failing => checks.where((c) => !c.healthy).length;
}

class DeviceInfo {
  DeviceInfo.fromJson(Map<String, dynamic> j)
    : id = j['id'] as String? ?? '',
      label = j['label'] as String? ?? '',
      createdAt = _time(j['created_at']),
      lastSeenAt = j['last_seen_at'] == null ? null : _time(j['last_seen_at']),
      revoked = j['revoked_at'] != null;
  final String id, label;
  final DateTime createdAt;
  final DateTime? lastSeenAt;
  final bool revoked;
}

class AgentInfo {
  AgentInfo.fromJson(Map<String, dynamic> j)
    : id = j['id'] as String? ?? '',
      name = j['name'] as String? ?? '',
      role = j['role'] as String? ?? '',
      description = j['description'] as String? ?? '',
      tools = [for (final t in (j['tools'] as List? ?? const [])) '$t'],
      profile = j['permission_profile'] as String? ?? '';
  final String id, name, role, description, profile;
  final List<String> tools;
}

class RunInfo {
  RunInfo.fromJson(Map<String, dynamic> j)
    : id = j['id'] as String? ?? '',
      sessionId = j['session_id'] as String? ?? '',
      status = j['status'] as String? ?? '',
      lastSeq = (j['last_seq'] as num?)?.toInt() ?? 0;
  final String id, sessionId, status;
  final int lastSeq;
}

/// One event from a run (milestone D's recorded event log).
class RunEvent {
  RunEvent(this.j);
  final Map<String, dynamic> j;
  String get type => j['type'] as String? ?? '';
  String get runId => j['run_id'] as String? ?? '';
  int get seq => (j['seq'] as num?)?.toInt() ?? 0;
  String get sessionId => j['session_id'] as String? ?? '';
  String get stepId => j['step_id'] as String? ?? '';
  String get tool => j['tool'] as String? ?? '';
  String get label => j['label'] as String? ?? '';
  String get status => j['status'] as String? ?? '';
  int get durationMs => (j['duration_ms'] as num?)?.toInt() ?? 0;
  String get output => j['output'] as String? ?? '';
  String get text => j['text'] as String? ?? '';
  String get decidedBy => j['decided_by'] as String? ?? '';
  String get decidedVia => j['decided_via'] as String? ?? '';
  DateTime get at => _time(j['at']);

  /// The pending approval carried by approval_required / approval_decided.
  Map<String, dynamic>? get approval => j['approval'] as Map<String, dynamic>?;
  String get approvalId => approval?['action_id'] as String? ?? '';

  /// The approval as a full view (pending events carry action_id, not id).
  ApprovalView? get approvalView {
    final a = approval;
    if (a == null) return null;
    return ApprovalView.fromJson({...a, 'id': a['action_id'] ?? a['id'] ?? '', 'status': 'pending'});
  }
}

/// A live run: its id (from X-Gubern-Run-Id) and its events. After the stream
/// ends, [done] says whether the server sent its final `done` frame; false
/// means the connection dropped and the caller should resume with follow().
class RunStream {
  RunStream(this.runId, HttpClientResponse res) {
    events = _sse(res, this);
  }
  final String runId;
  late final Stream<RunEvent> events;
  bool done = false;
}

String _randomHex(int bytes) {
  final r = Random.secure();
  return [for (var i = 0; i < bytes; i++) r.nextInt(256).toRadixString(16).padLeft(2, '0')].join();
}

String newSessionId() => 's-${_randomHex(8)}';

/// Chat modes, the same prefixes the dashboard sends.
enum ChatMode {
  chat('Chat', '', 'Ask anything'),
  agent('Agent', '/agent ', 'Give an agent a task'),
  team('Team', '/team ', 'Hand a goal to the team'),
  research('Research', 'research ', 'Research a topic');

  const ChatMode(this.label, this.prefix, this.hint);
  final String label, prefix, hint;
}

extension GubernReads on GubernApi {
  Future<Map<String, dynamic>> _get(String path) => _send('GET', _url(path), null);

  Future<Me> me() async => Me.fromJson(await _get('/api/me'));

  Future<List<SessionSummary>> sessions() async {
    final b = await _get('/api/agents/history');
    final out = [for (final s in (b['sessions'] as List? ?? const [])) SessionSummary.fromJson(s as Map<String, dynamic>)];
    out.sort((a, b) => b.updatedAt.compareTo(a.updatedAt));
    return out;
  }

  Future<List<HistoryMessage>> history(String sessionId) async {
    final b = await _get('/api/agents/history/${Uri.encodeComponent(sessionId)}');
    return [for (final m in (b['messages'] as List? ?? const [])) HistoryMessage.fromJson(m as Map<String, dynamic>)];
  }

  Future<List<AttentionItem>> attention() async {
    final b = await _get('/api/attention');
    return [for (final i in (b['items'] as List? ?? const [])) AttentionItem.fromJson(i as Map<String, dynamic>)];
  }

  Future<List<RunEvent>> activity({String? sessionId, int limit = 60}) async {
    final q = sessionId == null ? '' : '&session_id=${Uri.encodeQueryComponent(sessionId)}';
    final b = await _get('/api/activity?limit=$limit$q');
    return [for (final e in (b['events'] as List? ?? const [])) RunEvent(e as Map<String, dynamic>)];
  }

  Future<StatusInfo> status() async => StatusInfo.fromJson(await _get('/api/status'));
  Future<CostInfo> cost() async => CostInfo.fromJson(await _get('/api/ai/cost'));
  Future<HealingInfo> healing() async => HealingInfo.fromJson(await _get('/api/healing/status'));

  Future<List<DeviceInfo>> devices() async {
    final b = await _get('/api/devices');
    return [for (final d in (b['devices'] as List? ?? const [])) DeviceInfo.fromJson(d as Map<String, dynamic>)];
  }

  Future<List<AgentInfo>> agents() async {
    final b = await _get('/api/agents/catalog');
    return [for (final a in (b['agents'] as List? ?? const [])) AgentInfo.fromJson(a as Map<String, dynamic>)];
  }

  Future<List<RunInfo>> runs({String? sessionId, int limit = 20}) async {
    final q = sessionId == null ? '' : '&session_id=${Uri.encodeQueryComponent(sessionId)}';
    final b = await _get('/api/runs?limit=$limit$q');
    return [for (final r in (b['runs'] as List? ?? const [])) RunInfo.fromJson(r as Map<String, dynamic>)];
  }

  Future<void> cancelRun(String runId) async {
    try {
      await _send('POST', _url('/api/runs/${Uri.encodeComponent(runId)}/cancel'), null);
    } on ApiException catch (e) {
      if (e.status != HttpStatus.conflict) rethrow; // already finished
    }
  }

  /// Starts a run and streams its events (SSE).
  Future<RunStream> submit(String sessionId, String text, ChatMode mode) async {
    final req = await _streamRequest('POST', '/api/agents/submit?events=1');
    req.headers.set('Idempotency-Key', _randomHex(12));
    req.headers.contentType = ContentType.json;
    req.add(utf8.encode(jsonEncode({'message': mode.prefix + text, 'session_id': sessionId})));
    final res = await req.close();
    await _checkStream(res);
    return RunStream(res.headers.value('X-Gubern-Run-Id') ?? '', res);
  }

  /// Re-attaches to a run after [lastSeq] (Last-Event-ID), e.g. after a drop.
  Future<RunStream> follow(String runId, int lastSeq) async {
    final req = await _streamRequest('GET', '/api/runs/${Uri.encodeComponent(runId)}/events');
    req.headers.set('Last-Event-ID', '$lastSeq');
    final res = await req.close();
    await _checkStream(res);
    return RunStream(runId, res);
  }

  Future<HttpClientRequest> _streamRequest(String method, String path) async {
    final c = creds;
    if (c == null) throw RevokedException();
    final req = await _client.openUrl(method, _url(path));
    req.headers
      ..set(HttpHeaders.authorizationHeader, 'Bearer ${c.secret}')
      ..set(HttpHeaders.acceptHeader, 'text/event-stream');
    return req;
  }

  Future<void> _checkStream(HttpClientResponse res) async {
    if (res.statusCode < 300) return;
    final text = await res.transform(utf8.decoder).join();
    if (res.statusCode == HttpStatus.unauthorized) {
      await store.wipe();
      creds = null;
      throw RevokedException();
    }
    var msg = 'Request failed (${res.statusCode})';
    final j = _tryJson(text);
    if (j != null && j['error'] is String) {
      msg = j['error'] as String;
    } else if (text.trim().isNotEmpty) {
      msg = text.trim();
    }
    throw ApiException(res.statusCode, j?['reason'] as String?, msg);
  }
}

/// Parses an SSE response into run events. A `done` frame ends the stream.
Stream<RunEvent> _sse(HttpClientResponse res, RunStream rs) async* {
  final lines = res.transform(utf8.decoder).transform(const LineSplitter());
  var event = 'message';
  final data = <String>[];
  await for (final line in lines) {
    if (line.isEmpty) {
      if (data.isNotEmpty) {
        final j = _tryJson(data.join('\n'));
        if (event == 'done') {
          rs.done = true;
          yield RunEvent({...?j, 'type': 'done'});
          return;
        }
        if (j != null) yield RunEvent(j);
      }
      event = 'message';
      data.clear();
    } else if (line.startsWith(':')) {
      // heartbeat
    } else if (line.startsWith('event:')) {
      event = line.substring(6).trim();
    } else if (line.startsWith('data:')) {
      data.add(line.substring(5).trimLeft());
    }
  }
}

Map<String, dynamic>? _tryJson(String s) {
  try {
    final j = jsonDecode(s);
    return j is Map<String, dynamic> ? j : null;
  } on FormatException {
    return null;
  }
}
