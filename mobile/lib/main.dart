import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import 'src/api.dart';
import 'src/app_screens.dart';
import 'src/credential_store.dart';
import 'src/stepup.dart';
import 'src/ui/brand.dart';
import 'src/ui/theme.dart';

// Gubern mobile (docs/design/mobile-app.md): pair with a Gubern server, chat
// with agents, watch runs live, follow the system, and approve risky actions
// with the phone's hardware key. Not for release until pairing, storage and
// revocation are verified on real devices.
void main() {
  WidgetsFlutterBinding.ensureInitialized();
  runApp(GubernApp(store: CredentialStore(PlatformSecureStore()), deviceKey: const HardwareDeviceKey()));
}

class GubernApp extends StatefulWidget {
  const GubernApp({super.key, required this.store, required this.deviceKey});
  final CredentialStore store;
  final DeviceKey deviceKey;

  @override
  State<GubernApp> createState() => _GubernAppState();
}

class _GubernAppState extends State<GubernApp> {
  GubernApi? _api;
  bool _loading = true;
  final _themeMode = ValueNotifier(ThemeMode.dark);

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    final started = DateTime.now();
    final c = await widget.store.load();
    final pref = await widget.store.pref('theme').catchError((_) => null);
    _themeMode.value = ThemeMode.values.where((m) => m.name == pref).firstOrNull ?? ThemeMode.dark;
    // Let the mark finish drawing; never more than that.
    final wait = const Duration(milliseconds: 900) - DateTime.now().difference(started);
    if (!wait.isNegative) await Future<void>.delayed(wait);
    if (!mounted) return;
    setState(() {
      _api = c == null ? null : GubernApi(c, widget.store);
      _loading = false;
    });
  }

  void _paired(GubernApi api) => setState(() => _api = api);

  void _unpaired() {
    if (mounted) setState(() => _api = null);
  }

  @override
  void dispose() {
    _themeMode.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return ValueListenableBuilder<ThemeMode>(
      valueListenable: _themeMode,
      builder: (context, mode, _) => MaterialApp(
        title: 'Gubern',
        debugShowCheckedModeBanner: false,
        theme: buildTheme(Brightness.light),
        darkTheme: buildTheme(Brightness.dark),
        themeMode: mode,
        themeAnimationDuration: Motion.slow,
        builder: (context, child) {
          final dark = Theme.of(context).brightness == Brightness.dark;
          return AnnotatedRegion<SystemUiOverlayStyle>(
            value: (dark ? SystemUiOverlayStyle.light : SystemUiOverlayStyle.dark).copyWith(
              statusBarColor: Colors.transparent,
              systemNavigationBarColor: context.g.rail,
            ),
            child: child!,
          );
        },
        home: AnimatedSwitcher(
          duration: Motion.slow,
          switchInCurve: Motion.curve,
          transitionBuilder: (child, a) => FadeTransition(
            opacity: a,
            child: ScaleTransition(scale: Tween(begin: .98, end: 1.0).animate(a), child: child),
          ),
          child: _loading
              ? const Scaffold(key: ValueKey('splash'), body: Center(child: AnimatedGubernMark(size: 64)))
              : _api == null
              ? PairScreen(key: const ValueKey('pair'), store: widget.store, deviceKey: widget.deviceKey, onPaired: _paired)
              : AppShell(key: ValueKey(_api), api: _api!, deviceKey: widget.deviceKey, onUnpaired: _unpaired, themeMode: _themeMode),
        ),
      ),
    );
  }
}
