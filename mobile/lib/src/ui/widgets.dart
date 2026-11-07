import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import 'theme.dart';

/// Primary action: the brand gradient, 56 px tall, with a busy state.
class GradientButton extends StatelessWidget {
  const GradientButton({super.key, required this.label, required this.onPressed, this.icon, this.busy = false});
  final String label;
  final VoidCallback? onPressed;
  final IconData? icon;
  final bool busy;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final enabled = onPressed != null && !busy;
    return Semantics(
      button: true,
      enabled: enabled,
      label: label,
      child: AnimatedOpacity(
        duration: const Duration(milliseconds: 180),
        opacity: enabled ? 1 : .55,
        child: DecoratedBox(
          decoration: BoxDecoration(
            gradient: g.brandGradient,
            borderRadius: BorderRadius.circular(16),
            boxShadow: [BoxShadow(color: g.glow, blurRadius: 24, offset: const Offset(0, 8))],
          ),
          child: Material(
            type: MaterialType.transparency,
            child: InkWell(
              borderRadius: BorderRadius.circular(16),
              onTap: enabled ? onPressed : null,
              child: SizedBox(
                height: 56,
                child: Center(
                  child: busy
                      ? const SizedBox.square(dimension: 22, child: CircularProgressIndicator(strokeWidth: 2.4, color: Colors.white))
                      : Row(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            if (icon != null) ...[Icon(icon, color: Colors.white, size: 22), const SizedBox(width: 10)],
                            ExcludeSemantics(
                              child: Text(
                                label,
                                style: const TextStyle(color: Colors.white, fontSize: 16, fontWeight: FontWeight.w600, letterSpacing: .2),
                              ),
                            ),
                          ],
                        ),
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}

IconData riskIcon(String risk) {
  final r = risk.toUpperCase();
  if (r.startsWith('CRITICAL')) return Icons.gpp_maybe_rounded;
  if (r.startsWith('HIGH')) return Icons.warning_amber_rounded;
  if (r.startsWith('MEDIUM')) return Icons.info_outline_rounded;
  return Icons.verified_user_outlined;
}

String riskLabel(String risk) {
  final r = risk.toUpperCase();
  if (r.startsWith('CRITICAL')) return 'Critical';
  if (r.startsWith('HIGH')) return 'High risk';
  if (r.startsWith('MEDIUM')) return 'Medium risk';
  if (r.isEmpty) return 'Unrated';
  return 'Low risk';
}

/// Risk as colour, icon and words together (never colour alone).
class RiskBadge extends StatelessWidget {
  const RiskBadge(this.risk, {super.key, this.large = false});
  final String risk;
  final bool large;

  @override
  Widget build(BuildContext context) {
    final c = context.g.risk(risk);
    return Container(
      padding: EdgeInsets.symmetric(horizontal: large ? 12 : 9, vertical: large ? 7 : 4),
      decoration: BoxDecoration(
        color: c.withValues(alpha: .14),
        borderRadius: BorderRadius.circular(99),
        border: Border.all(color: c.withValues(alpha: .45)),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(riskIcon(risk), size: large ? 18 : 14, color: c),
          const SizedBox(width: 6),
          Text(
            riskLabel(risk),
            style: TextStyle(color: c, fontWeight: FontWeight.w600, fontSize: large ? 14 : 12),
          ),
        ],
      ),
    );
  }
}

/// Time left until [until], ticking every second; turns warning-coloured in
/// the last two minutes.
class Countdown extends StatefulWidget {
  const Countdown(this.until, {super.key, this.style});
  final DateTime until;
  final TextStyle? style;

  @override
  State<Countdown> createState() => _CountdownState();
}

class _CountdownState extends State<Countdown> {
  Timer? _t;

  @override
  void initState() {
    super.initState();
    _t = Timer.periodic(const Duration(seconds: 1), (_) {
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
    final left = widget.until.difference(DateTime.now());
    final g = context.g;
    if (left.isNegative) return Text('Expired', style: (widget.style ?? const TextStyle()).copyWith(color: g.critical));
    final m = left.inMinutes, s = left.inSeconds % 60;
    final urgent = left.inSeconds < 120;
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(Icons.timer_outlined, size: 15, color: urgent ? g.warn : g.muted),
        const SizedBox(width: 4),
        Text(
          '$m:${s.toString().padLeft(2, '0')}',
          style: (widget.style ?? const TextStyle()).copyWith(
            color: urgent ? g.warn : g.muted,
            fontFeatures: const [FontFeature.tabularFigures()],
            fontWeight: FontWeight.w600,
          ),
        ),
      ],
    );
  }
}

/// A pulsing status dot: green and breathing while connected.
class LiveDot extends StatefulWidget {
  const LiveDot({super.key, required this.ok});
  final bool ok;

  @override
  State<LiveDot> createState() => _LiveDotState();
}

class _LiveDotState extends State<LiveDot> with SingleTickerProviderStateMixin {
  late final AnimationController _c = AnimationController(vsync: this, duration: const Duration(milliseconds: 1600));

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (MediaQuery.of(context).disableAnimations) {
      _c.stop();
    } else if (!_c.isAnimating) {
      _c.repeat();
    }
  }

  @override
  void dispose() {
    _c.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final c = widget.ok ? context.g.ok : context.g.warn;
    return SizedBox.square(
      dimension: 18,
      child: AnimatedBuilder(
        animation: _c,
        builder: (_, _) => Stack(
          alignment: Alignment.center,
          children: [
            if (widget.ok)
              Container(
                width: 8 + 10 * _c.value,
                height: 8 + 10 * _c.value,
                decoration: BoxDecoration(
                  shape: BoxShape.circle,
                  color: c.withValues(alpha: .35 * (1 - _c.value)),
                ),
              ),
            Container(
              width: 8,
              height: 8,
              decoration: BoxDecoration(shape: BoxShape.circle, color: c),
            ),
          ],
        ),
      ),
    );
  }
}

/// A titled card section.
class Section extends StatelessWidget {
  const Section({super.key, required this.title, required this.child, this.icon, this.trailing});
  final String title;
  final Widget child;
  final IconData? icon;
  final Widget? trailing;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: g.surface,
        borderRadius: BorderRadius.circular(20),
        border: Border.all(color: g.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              if (icon != null) ...[Icon(icon, size: 16, color: g.muted), const SizedBox(width: 8)],
              Expanded(
                child: Text(
                  title.toUpperCase(),
                  style: TextStyle(color: g.muted, fontSize: 12, fontWeight: FontWeight.w700, letterSpacing: 1.1),
                ),
              ),
              ?trailing,
            ],
          ),
          const SizedBox(height: 12),
          child,
        ],
      ),
    );
  }
}

/// Command or preview text in a dark mono block with a copy button.
class CodeBlock extends StatelessWidget {
  const CodeBlock(this.text, {super.key, this.prompt = false});
  final String text;
  final bool prompt;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.fromLTRB(14, 12, 6, 12),
      decoration: BoxDecoration(
        color: const Color(0xFF050811),
        borderRadius: BorderRadius.circular(14),
        border: Border.all(color: g.border),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Expanded(
            child: SelectableText.rich(
              TextSpan(
                children: [
                  if (prompt)
                    TextSpan(
                      text: r'$ ',
                      style: mono(context, color: g.accent, weight: FontWeight.w700),
                    ),
                  TextSpan(
                    text: text,
                    style: mono(context, color: const Color(0xFFE2E8F0)),
                  ),
                ],
              ),
            ),
          ),
          IconButton(
            tooltip: 'Copy',
            visualDensity: VisualDensity.compact,
            icon: Icon(Icons.copy_rounded, size: 18, color: g.muted),
            onPressed: () {
              Clipboard.setData(ClipboardData(text: text));
              ScaffoldMessenger.of(context).showSnackBar(const SnackBar(content: Text('Copied'), duration: Duration(seconds: 1)));
            },
          ),
        ],
      ),
    );
  }
}

/// A labelled detail row.
class InfoRow extends StatelessWidget {
  const InfoRow(this.icon, this.label, this.value, {super.key, this.monoValue = false});
  final IconData icon;
  final String label, value;
  final bool monoValue;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 6),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Icon(icon, size: 18, color: g.muted),
          const SizedBox(width: 12),
          SizedBox(
            width: 92,
            child: Text(label, style: TextStyle(color: g.muted, fontSize: 14)),
          ),
          Expanded(
            child: Text(value, style: monoValue ? mono(context, size: 13) : TextStyle(color: g.text, fontSize: 14, height: 1.4)),
          ),
        ],
      ),
    );
  }
}

/// Slide to confirm: a deliberate gesture for high-risk approvals, so a
/// stray tap can never approve. Calls [onConfirmed] when the thumb reaches
/// the end; also operable by screen readers as a button.
class SlideToConfirm extends StatefulWidget {
  const SlideToConfirm({super.key, required this.label, required this.onConfirmed, this.color, this.enabled = true});
  final String label;
  final VoidCallback onConfirmed;
  final Color? color;
  final bool enabled;

  @override
  State<SlideToConfirm> createState() => _SlideToConfirmState();
}

class _SlideToConfirmState extends State<SlideToConfirm> {
  double _x = 0;
  static const _thumb = 52.0;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final color = widget.color ?? g.brand;
    return Semantics(
      button: true,
      enabled: widget.enabled,
      label: widget.label,
      onTap: widget.enabled ? widget.onConfirmed : null,
      child: LayoutBuilder(
        builder: (context, c) {
          final max = c.maxWidth - _thumb - 8;
          final p = max <= 0 ? 0.0 : (_x / max).clamp(0.0, 1.0);
          return Container(
            height: 60,
            decoration: BoxDecoration(
              color: color.withValues(alpha: .14),
              borderRadius: BorderRadius.circular(18),
              border: Border.all(color: color.withValues(alpha: .5)),
            ),
            child: Stack(
              alignment: Alignment.centerLeft,
              children: [
                Positioned.fill(
                  child: ClipRRect(
                    borderRadius: BorderRadius.circular(18),
                    child: Align(
                      alignment: Alignment.centerLeft,
                      child: FractionallySizedBox(
                        widthFactor: (p * .95 + .05).clamp(0, 1),
                        child: ColoredBox(color: color.withValues(alpha: .22)),
                      ),
                    ),
                  ),
                ),
                Center(
                  child: Opacity(
                    opacity: 1 - p,
                    child: ExcludeSemantics(
                      child: Text(
                        widget.label,
                        style: TextStyle(color: color, fontWeight: FontWeight.w700, fontSize: 15, letterSpacing: .3),
                      ),
                    ),
                  ),
                ),
                Positioned(
                  left: 4 + _x.clamp(0, max <= 0 ? 0 : max),
                  child: GestureDetector(
                    onHorizontalDragUpdate: widget.enabled ? (d) => setState(() => _x = (_x + d.delta.dx).clamp(0, max)) : null,
                    onHorizontalDragEnd: widget.enabled
                        ? (_) {
                            if (_x >= max * .92) {
                              HapticFeedback.heavyImpact();
                              setState(() => _x = 0);
                              widget.onConfirmed();
                            } else {
                              setState(() => _x = 0);
                            }
                          }
                        : null,
                    child: Container(
                      key: const ValueKey('slide-thumb'),
                      width: _thumb,
                      height: _thumb,
                      decoration: BoxDecoration(
                        color: color,
                        borderRadius: BorderRadius.circular(14),
                        boxShadow: [BoxShadow(color: color.withValues(alpha: .45), blurRadius: 14)],
                      ),
                      child: const Icon(Icons.fingerprint_rounded, color: Colors.white, size: 28),
                    ),
                  ),
                ),
              ],
            ),
          );
        },
      ),
    );
  }
}

/// A coloured notice (error, warning, success).
class Notice extends StatelessWidget {
  const Notice({super.key, required this.text, required this.color, required this.icon});
  final String text;
  final Color color;
  final IconData icon;

  @override
  Widget build(BuildContext context) => Container(
    width: double.infinity,
    padding: const EdgeInsets.all(14),
    decoration: BoxDecoration(
      color: color.withValues(alpha: .12),
      borderRadius: BorderRadius.circular(14),
      border: Border.all(color: color.withValues(alpha: .4)),
    ),
    child: Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Icon(icon, color: color, size: 20),
        const SizedBox(width: 10),
        Expanded(
          child: Text(text, style: TextStyle(color: context.g.text, height: 1.4)),
        ),
      ],
    ),
  );
}
