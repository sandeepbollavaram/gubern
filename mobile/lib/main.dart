import 'dart:async';

import 'package:flutter/material.dart';

import 'src/api.dart';
import 'src/app_screens.dart';
import 'src/credential_store.dart';
import 'src/stepup.dart';

// Gubern mobile (milestone H, docs/design/mobile-app.md): pair with a
// Gubern server, follow pending approvals, and approve with the phone's
// hardware key. Not for release until pairing, storage and revocation are
// verified on real devices.
void main() {
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

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    final c = await widget.store.load();
    setState(() {
      _api = c == null ? null : GubernApi(c, widget.store);
      _loading = false;
    });
  }

  void _paired(GubernApi api) => setState(() => _api = api);

  void _unpaired() => setState(() => _api = null);

  @override
  Widget build(BuildContext context) {
    final scheme = ColorScheme.fromSeed(seedColor: const Color(0xFF378ADD));
    return MaterialApp(
      title: 'Gubern',
      theme: ThemeData(colorScheme: scheme, useMaterial3: true),
      darkTheme: ThemeData(colorScheme: ColorScheme.fromSeed(seedColor: const Color(0xFF378ADD), brightness: Brightness.dark), useMaterial3: true),
      home: _loading
          ? const Scaffold(body: Center(child: CircularProgressIndicator()))
          : _api == null
              ? PairScreen(store: widget.store, deviceKey: widget.deviceKey, onPaired: _paired)
              : ApprovalsScreen(api: _api!, deviceKey: widget.deviceKey, onUnpaired: _unpaired),
    );
  }
}
