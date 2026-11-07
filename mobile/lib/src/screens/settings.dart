import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../api.dart';
import '../ui/brand.dart';
import '../ui/theme.dart';
import '../ui/widgets.dart';
import 'shell.dart';

class SettingsScreen extends StatefulWidget {
  const SettingsScreen({super.key, required this.scope});
  final AppScope scope;
  @override
  State<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends State<SettingsScreen> {
  List<DeviceInfo>? _devices;

  @override
  void initState() {
    super.initState();
    unawaited(
      widget.scope.api.devices().then((d) {
        if (mounted) setState(() => _devices = d);
      }).catchError((_) {
        if (mounted) setState(() => _devices = const []);
      }),
    );
  }

  Future<void> _unpair() async {
    final g = context.g;
    final sure = await showDialog<bool>(
      context: context,
      builder: (d) => AlertDialog(
        title: const Text('Unpair this phone?'),
        content: const Text('Its key is deleted. You\'ll need a new code from the dashboard to pair again.'),
        actions: [
          TextButton(onPressed: () => Navigator.pop(d, false), child: const Text('Cancel')),
          TextButton(onPressed: () => Navigator.pop(d, true), child: Text('Unpair', style: TextStyle(color: g.critical))),
        ],
      ),
    );
    if (sure != true || !mounted) return;
    final s = widget.scope;
    await s.api.store.wipe();
    await s.deviceKey.delete();
    if (mounted) Navigator.of(context).popUntil((r) => r.isFirst);
    s.onUnpaired();
  }

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final s = widget.scope;
    final c = s.api.creds;
    final me = s.hub.me;
    var i = 0;
    Widget stag(Widget w) => FadeSlideIn.indexed(i++, child: Padding(padding: const EdgeInsets.only(bottom: 18), child: w));
    return Scaffold(
      appBar: AppBar(title: const Text('Settings')),
      body: ListView(
        padding: const EdgeInsets.fromLTRB(16, 4, 16, 32),
        children: [
          if (me != null)
            stag(
              Panel(
                child: Row(
                  children: [
                    Container(
                      width: 44,
                      height: 44,
                      alignment: Alignment.center,
                      decoration: BoxDecoration(color: g.selected, borderRadius: BorderRadius.circular(10)),
                      child: Text(me.name.isEmpty ? '·' : me.name.characters.first.toUpperCase(), style: TextStyle(color: g.brand, fontSize: 18, fontWeight: FontWeight.w600)),
                    ),
                    const SizedBox(width: 12),
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text(me.name, style: const TextStyle(fontSize: 16, fontWeight: FontWeight.w600)),
                          Text('Acting as you on this phone', style: TextStyle(color: g.muted, fontSize: 13)),
                        ],
                      ),
                    ),
                  ],
                ),
              ),
            ),
          stag(
            Section(
              title: 'Appearance',
              icon: Icons.contrast_rounded,
              child: ValueListenableBuilder<ThemeMode>(
                valueListenable: s.themeMode,
                builder: (_, mode, _) => SizedBox(
                  width: double.infinity,
                  child: SegmentedButton<ThemeMode>(
                    showSelectedIcon: false,
                    style: SegmentedButton.styleFrom(
                      selectedBackgroundColor: g.selected,
                      selectedForegroundColor: g.text,
                      foregroundColor: g.muted,
                      side: BorderSide(color: g.border),
                      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(8)),
                    ),
                    segments: const [
                      ButtonSegment(value: ThemeMode.system, label: Text('System'), icon: Icon(Icons.brightness_auto_outlined, size: 17)),
                      ButtonSegment(value: ThemeMode.dark, label: Text('Dark'), icon: Icon(Icons.dark_mode_outlined, size: 17)),
                      ButtonSegment(value: ThemeMode.light, label: Text('Light'), icon: Icon(Icons.light_mode_outlined, size: 17)),
                    ],
                    selected: {mode},
                    onSelectionChanged: (v) {
                      HapticFeedback.selectionClick();
                      s.themeMode.value = v.first;
                      unawaited(s.api.store.setPref('theme', v.first.name));
                    },
                  ),
                ),
              ),
            ),
          ),
          stag(
            Section(
              title: 'This phone',
              icon: Icons.smartphone_rounded,
              child: Column(
                children: [
                  if (c != null) ...[
                    InfoRow(Icons.dns_outlined, 'Server', c.server.toString(), monoValue: true),
                    InfoRow(Icons.badge_outlined, 'Device', c.deviceId, monoValue: true),
                    InfoRow(
                      c.fingerprint != null ? Icons.lock_outline_rounded : Icons.lock_open_rounded,
                      'Connection',
                      c.fingerprint != null ? 'Encrypted, certificate pinned' : 'Not encrypted (trusted network only)',
                      valueColor: c.fingerprint != null ? null : g.warn,
                    ),
                  ],
                  const InfoRow(Icons.key_outlined, 'Signing key', 'In this phone\'s secure hardware; asks for your fingerprint or PIN'),
                ],
              ),
            ),
          ),
          if (me != null && me.scopes.isNotEmpty)
            stag(
              Section(
                title: 'What this phone may do',
                icon: Icons.verified_user_outlined,
                child: Wrap(
                  spacing: 6,
                  runSpacing: 6,
                  children: [
                    for (final sc in me.scopes)
                      Container(
                        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
                        decoration: BoxDecoration(color: g.surfaceHigh, borderRadius: BorderRadius.circular(6), border: Border.all(color: g.border)),
                        child: Text(sc, style: mono(context, size: 12)),
                      ),
                  ],
                ),
              ),
            ),
          stag(
            Section(
              title: 'Your devices',
              icon: Icons.devices_outlined,
              child: _devices == null
                  ? const Column(children: [Skeleton(), SizedBox(height: 10), Skeleton(width: 160)])
                  : _devices!.isEmpty
                  ? Text('No devices to show.', style: TextStyle(color: g.muted))
                  : Column(
                      children: [
                        for (final d in _devices!)
                          Padding(
                            padding: const EdgeInsets.symmetric(vertical: 6),
                            child: Row(
                              children: [
                                Icon(Icons.smartphone_rounded, size: 18, color: d.revoked ? g.muted : g.text),
                                const SizedBox(width: 10),
                                Expanded(
                                  child: Column(
                                    crossAxisAlignment: CrossAxisAlignment.start,
                                    children: [
                                      Text(
                                        d.label.isEmpty ? d.id : d.label,
                                        style: TextStyle(fontWeight: FontWeight.w500, decoration: d.revoked ? TextDecoration.lineThrough : null),
                                      ),
                                      Text(
                                        d.revoked ? 'Revoked' : (d.lastSeenAt == null ? 'Paired ${timeAgo(d.createdAt)}' : 'Seen ${timeAgo(d.lastSeenAt!)}'),
                                        style: TextStyle(color: g.muted, fontSize: 12),
                                      ),
                                    ],
                                  ),
                                ),
                                if (d.id == c?.deviceId) Pill(label: 'This phone', color: g.brand),
                              ],
                            ),
                          ),
                      ],
                    ),
            ),
          ),
          stag(
            OutlinedButton.icon(
              style: OutlinedButton.styleFrom(foregroundColor: g.critical, side: BorderSide(color: g.critical.withValues(alpha: .5))),
              onPressed: _unpair,
              icon: const Icon(Icons.link_off_rounded),
              label: const Text('Unpair this phone'),
            ),
          ),
          const SizedBox(height: 8),
          Center(child: Opacity(opacity: .6, child: GubernWordmark(size: 18))),
        ],
      ),
    );
  }
}
