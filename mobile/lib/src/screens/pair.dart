import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:mobile_scanner/mobile_scanner.dart';

import '../api.dart';
import '../credential_store.dart';
import '../pairing_link.dart';
import '../stepup.dart';
import '../ui/brand.dart';
import '../ui/theme.dart';
import '../ui/widgets.dart';

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
            padding: const EdgeInsets.fromLTRB(20, 0, 20, 20),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Pill(label: 'Not encrypted', color: g.warn, icon: Icons.lock_open_rounded, large: true),
                const SizedBox(height: 14),
                Text('Unencrypted connection', style: Theme.of(c).textTheme.titleLarge),
                const SizedBox(height: 8),
                Text(
                  '${link.server.host} doesn\'t use https, so this phone\'s traffic to it isn\'t encrypted. Pair only on a network you trust, such as your home Wi-Fi or a private VPN.',
                  style: TextStyle(color: g.muted, height: 1.5),
                ),
                const SizedBox(height: 22),
                // The safe choice is the prominent one.
                PrimaryButton(label: 'Cancel', onPressed: () => Navigator.pop(c, false)),
                const SizedBox(height: 10),
                OutlinedButton.icon(
                  style: OutlinedButton.styleFrom(foregroundColor: g.warn, side: BorderSide(color: g.warn.withValues(alpha: .5))),
                  onPressed: () => Navigator.pop(c, true),
                  icon: const Icon(Icons.lock_open_rounded, size: 19),
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
      body: SafeArea(
        child: LayoutBuilder(
          builder: (context, c) => SingleChildScrollView(
            padding: const EdgeInsets.symmetric(horizontal: 20),
            child: ConstrainedBox(
              constraints: BoxConstraints(minHeight: c.maxHeight),
              child: IntrinsicHeight(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    const SizedBox(height: 28),
                    const FadeSlideIn(child: Row(children: [AnimatedGubernMark(size: 36), SizedBox(width: 10), Text('Gubern', style: TextStyle(fontSize: 20, fontWeight: FontWeight.w600, letterSpacing: -.3))])),
                    const SizedBox(height: 36),
                    FadeSlideIn(delay: const Duration(milliseconds: 80), child: Text('Your agents,\nin your pocket.', style: t.headlineLarge)),
                    const SizedBox(height: 12),
                    FadeSlideIn(
                      delay: const Duration(milliseconds: 140),
                      child: Text(
                        'Chat with your agents, watch them work, and approve risky actions with your fingerprint. Nothing risky runs without you.',
                        style: TextStyle(color: g.muted, fontSize: 16, height: 1.55),
                      ),
                    ),
                    const SizedBox(height: 28),
                    FadeSlideIn(
                      delay: const Duration(milliseconds: 200),
                      child: Panel(
                        padding: const EdgeInsets.fromLTRB(14, 14, 14, 6),
                        child: Column(
                          children: const [
                            _Step(n: 1, title: 'Open Devices in the dashboard', body: 'Choose "Pair a phone" to show a one-time code.'),
                            _Step(n: 2, title: 'Scan the code', body: 'It works once and expires in ten minutes.'),
                            _Step(n: 3, title: 'Approve with your fingerprint', body: 'A key in this phone\'s secure hardware signs every approval.'),
                          ],
                        ),
                      ),
                    ),
                    const Spacer(),
                    const SizedBox(height: 24),
                    AnimatedSize(
                      duration: Motion.base,
                      curve: Motion.curve,
                      child: _error == null
                          ? const SizedBox(width: double.infinity)
                          : Padding(
                              padding: const EdgeInsets.only(bottom: 14),
                              child: FadeSlideIn(child: Notice(text: _error!, color: g.critical, icon: Icons.error_outline_rounded)),
                            ),
                    ),
                    PrimaryButton(label: 'Scan pairing code', icon: Icons.qr_code_scanner_rounded, busy: _busy, onPressed: _scan),
                    const SizedBox(height: 10),
                    OutlinedButton.icon(
                      onPressed: _busy ? null : _enterLink,
                      icon: const Icon(Icons.link_rounded, size: 19),
                      label: const Text('Enter the link instead'),
                    ),
                    const SizedBox(height: 20),
                  ],
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
  const _Step({required this.n, required this.title, required this.body});
  final int n;
  final String title, body;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    return Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Container(
            width: 26,
            height: 26,
            alignment: Alignment.center,
            decoration: BoxDecoration(color: g.selected, borderRadius: BorderRadius.circular(7)),
            child: Text('$n', style: TextStyle(color: g.brand, fontWeight: FontWeight.w600, fontSize: 13)),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(title, style: const TextStyle(fontSize: 15, fontWeight: FontWeight.w500)),
                const SizedBox(height: 2),
                Text(body, style: TextStyle(color: g.muted, height: 1.4, fontSize: 14)),
              ],
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
      padding: EdgeInsets.fromLTRB(20, 0, 20, 20 + MediaQuery.of(context).viewInsets.bottom),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('Pair with a link', style: Theme.of(context).textTheme.titleLarge),
          const SizedBox(height: 6),
          Text('Copy the link shown under the code in the dashboard.', style: TextStyle(color: context.g.muted)),
          const SizedBox(height: 18),
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
          const SizedBox(height: 18),
          PrimaryButton(label: 'Pair', icon: Icons.link_rounded, onPressed: () => Navigator.pop(context, (_link.text, _name.text))),
        ],
      ),
    );
  }
}

/// Full-screen camera with a framing guide whose corners breathe.
class ScannerScreen extends StatefulWidget {
  const ScannerScreen({super.key});
  @override
  State<ScannerScreen> createState() => _ScannerScreenState();
}

class _ScannerScreenState extends State<ScannerScreen> with SingleTickerProviderStateMixin {
  bool _done = false;
  late final AnimationController _c = AnimationController(vsync: this, duration: const Duration(milliseconds: 1600));

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
            child: AnimatedBuilder(
              animation: _c,
              builder: (_, _) => CustomPaint(size: Size.square(250 + 8 * Curves.easeInOut.transform(_c.value)), painter: _CornersPainter(g.brand)),
            ),
          ),
          SafeArea(
            child: Padding(
              padding: const EdgeInsets.all(12),
              child: Column(
                children: [
                  Row(
                    children: [
                      IconButton(
                        tooltip: 'Close',
                        style: IconButton.styleFrom(backgroundColor: Colors.black54, foregroundColor: Colors.white),
                        onPressed: () => Navigator.pop(context),
                        icon: const Icon(Icons.close_rounded),
                      ),
                    ],
                  ),
                  const Spacer(),
                  Container(
                    padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 9),
                    decoration: BoxDecoration(color: Colors.black.withValues(alpha: .7), borderRadius: BorderRadius.circular(8)),
                    child: const Text('Point at the code in the Gubern dashboard', style: TextStyle(color: Colors.white)),
                  ),
                  const SizedBox(height: 28),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _CornersPainter extends CustomPainter {
  _CornersPainter(this.color);
  final Color color;

  @override
  void paint(Canvas canvas, Size s) {
    final p = Paint()
      ..color = color
      ..style = PaintingStyle.stroke
      ..strokeWidth = 4
      ..strokeCap = StrokeCap.round;
    const l = 34.0;
    final w = s.width, h = s.height;
    canvas
      ..drawPath(Path()..moveTo(0, l)..lineTo(0, 0)..lineTo(l, 0), p)
      ..drawPath(Path()..moveTo(w - l, 0)..lineTo(w, 0)..lineTo(w, l), p)
      ..drawPath(Path()..moveTo(w, h - l)..lineTo(w, h)..lineTo(w - l, h), p)
      ..drawPath(Path()..moveTo(l, h)..lineTo(0, h)..lineTo(0, h - l), p);
  }

  @override
  bool shouldRepaint(_CornersPainter o) => o.color != color;
}
