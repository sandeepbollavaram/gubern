import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:gubern_mobile/src/api.dart';
import 'package:gubern_mobile/src/credential_store.dart';
import 'package:gubern_mobile/src/screens/chat.dart';

/// A fake server for runs: the submit stream drops after three events, and
/// the resume endpoint replays from Last-Event-ID (with a duplicate, as a
/// real reconnect can) and then finishes.
class FakeRuns {
  late HttpServer server;
  String? lastEventId;
  String? submitted;
  String? auth;
  String? idempotency;

  Uri get url => Uri.parse('http://127.0.0.1:${server.port}');

  Future<void> start() async {
    server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    server.listen(_handle);
  }

  void _frame(HttpResponse r, Map<String, Object?> ev, {String? event}) {
    if (event != null) r.write('event: $event\n');
    if (ev['seq'] != null) r.write('id: ${ev['seq']}\n');
    r.write('data: ${jsonEncode(ev)}\n\n');
  }

  Future<void> _handle(HttpRequest req) async {
    final body = await utf8.decoder.bind(req).join();
    final r = req.response;
    auth = req.headers.value(HttpHeaders.authorizationHeader);
    if (req.uri.path == '/api/agents/history/s-test') {
      r.headers.contentType = ContentType.json;
      r.write(jsonEncode({'messages': []}));
    } else if (req.uri.path == '/api/approvals') {
      r.headers.contentType = ContentType.json;
      r.write(jsonEncode({'approvals': []}));
    } else if (req.uri.path == '/api/runs') {
      r.headers.contentType = ContentType.json;
      r.write(jsonEncode({'runs': []}));
    } else if (req.uri.path == '/api/agents/submit') {
      submitted = (jsonDecode(body) as Map)['message'] as String;
      idempotency = req.headers.value('Idempotency-Key');
      r.headers
        ..contentType = ContentType('text', 'event-stream')
        ..set('X-Gubern-Run-Id', 'run-1');
      r.write(': hello\n\n');
      _frame(r, {'type': 'text', 'run_id': 'run-1', 'seq': 1, 'text': 'Checking '});
      _frame(r, {'type': 'tool_start', 'run_id': 'run-1', 'seq': 2, 'step_id': 'st1', 'tool': 'run_terminal', 'label': 'go test ./...'});
      _frame(r, {'type': 'tool_end', 'run_id': 'run-1', 'seq': 3, 'step_id': 'st1', 'status': 'ok', 'duration_ms': 1200, 'output': 'ok'});
      // Connection drops here: no done frame.
    } else if (req.uri.path == '/api/runs/run-1/events') {
      lastEventId = req.headers.value('Last-Event-ID');
      r.headers.contentType = ContentType('text', 'event-stream');
      _frame(r, {'type': 'tool_end', 'run_id': 'run-1', 'seq': 3, 'step_id': 'st1', 'status': 'failed'}); // duplicate: ignored
      _frame(r, {
        'type': 'approval_required',
        'run_id': 'run-1',
        'seq': 4,
        'approval': {'action_id': 'ap1', 'session_id': 's-test', 'tool': 'run_terminal', 'risk': 'HIGH RISK', 'description': 'Push', 'action_hash': 'ab' * 32, 'expires_at': '2099-01-01T00:00:00Z'},
      });
      _frame(r, {'type': 'approval_decided', 'run_id': 'run-1', 'seq': 5, 'status': 'approved', 'decided_by': 'sandeep', 'decided_via': 'mobile', 'approval': {'action_id': 'ap1'}});
      _frame(r, {'type': 'text', 'run_id': 'run-1', 'seq': 6, 'text': 'done.'});
      _frame(r, {'type': 'run_finished', 'run_id': 'run-1', 'seq': 7, 'status': 'done'});
      _frame(r, {'run_id': 'run-1', 'status': 'done', 'last_seq': 7}, event: 'done');
    } else {
      r.statusCode = 404;
    }
    await r.close();
  }
}

void main() {
  late FakeRuns fake;
  late GubernApi api;

  setUp(() async {
    fake = FakeRuns();
    await fake.start();
    api = GubernApi(Credentials(server: fake.url, deviceId: 'd1', secret: 'sekret'), CredentialStore(MemoryStore()), client: HttpClient());
  });

  tearDown(() => fake.server.close(force: true));

  test('a run streams, resumes from the last event after a drop, and ignores replays', () async {
    final c = ChatController(api, 's-test', onRevoked: () => fail('not revoked'));
    await c.send('run the tests', ChatMode.agent);

    expect(fake.submitted, '/agent run the tests');
    expect(fake.auth, 'Bearer sekret');
    expect(fake.idempotency, isNotEmpty);
    expect(fake.lastEventId, '3');
    expect(c.running, isFalse);

    final tools = c.items.whereType<ToolsItem>().single;
    expect(tools.steps.single.status, 'ok', reason: 'the replayed seq 3 must not overwrite the step');
    expect(tools.steps.single.durationMs, 1200);

    final ap = c.items.whereType<ApprovalItem>().single;
    expect(ap.state, 'approved');
    expect(ap.decidedVia, 'mobile');

    final text = c.items.whereType<AssistantItem>().map((a) => a.text).join('|');
    expect(text, contains('Checking '));
    expect(text, contains('done.'));
    expect(c.items.whereType<AssistantItem>().every((a) => !a.streaming), isTrue);
  });

  test('every chat mode sends the dashboard prefix', () {
    expect(ChatMode.chat.prefix, '');
    expect(ChatMode.agent.prefix, '/agent ');
    expect(ChatMode.team.prefix, '/team ');
    expect(ChatMode.research.prefix, 'research ');
  });

  test('chat titles drop the mode prefix and tool directives', () {
    expect(cleanTitle('/agent clean up [[tool:run_terminal {"command":"rm -rf x"}]]'), 'Clean up');
    expect(cleanTitle('research what changed in Go'), 'What changed in Go');
    expect(cleanTitle('  plain question  '), 'Plain question');
    expect(cleanTitle('[[tool:x {}]]'), '');
    expect(cleanTitle('/agent clean the build [[tool:run_terminal {"com'), 'Clean the build');
  });
}
