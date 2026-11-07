import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/physics.dart';
import 'package:flutter/services.dart';

import 'theme.dart';

bool reducedMotion(BuildContext context) => MediaQuery.maybeOf(context)?.disableAnimations ?? false;

// ---------------------------------------------------------------------------
// Risk and status: colour, icon and a word together (never colour alone)
// ---------------------------------------------------------------------------

IconData riskIcon(String risk) {
  final r = risk.toUpperCase();
  if (r.startsWith('CRITICAL')) return Icons.gpp_maybe_outlined;
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

/// A small flat label: tinted fill, icon and word.
class Pill extends StatelessWidget {
  const Pill({super.key, required this.label, required this.color, this.icon, this.large = false});
  final String label;
  final Color color;
  final IconData? icon;
  final bool large;

  @override
  Widget build(BuildContext context) => Container(
    padding: EdgeInsets.symmetric(horizontal: large ? 10 : 8, vertical: large ? 5 : 3),
    decoration: BoxDecoration(color: color.withValues(alpha: .13), borderRadius: BorderRadius.circular(6)),
    child: Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        if (icon != null) ...[Icon(icon, size: large ? 16 : 13, color: color), SizedBox(width: large ? 6 : 4)],
        Text(label, style: TextStyle(color: color, fontWeight: FontWeight.w500, fontSize: large ? 14 : 12)),
      ],
    ),
  );
}

class RiskBadge extends StatelessWidget {
  const RiskBadge(this.risk, {super.key, this.large = false});
  final String risk;
  final bool large;

  @override
  Widget build(BuildContext context) => Pill(label: riskLabel(risk), color: context.g.risk(risk), icon: riskIcon(risk), large: large);
}

/// Time left until [until], ticking every second; warning-coloured in the
/// last two minutes.
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
    final base = widget.style ?? const TextStyle(fontSize: 13);
    if (left.isNegative) return Text('Expired', style: base.copyWith(color: g.critical));
    final m = left.inMinutes, s = left.inSeconds % 60;
    final urgent = left.inSeconds < 120;
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(Icons.schedule_rounded, size: 14, color: urgent ? g.warn : g.muted),
        const SizedBox(width: 4),
        Text(
          '$m:${s.toString().padLeft(2, '0')}',
          style: base.copyWith(
            fontFamily: monoFamily,
            color: urgent ? g.warn : g.muted,
            fontFeatures: const [FontFeature.tabularFigures()],
          ),
        ),
      ],
    );
  }
}

/// A status dot; a calm ring pulses out while live.
class LiveDot extends StatefulWidget {
  const LiveDot({super.key, required this.color, this.live = true, this.size = 8});
  final Color color;
  final bool live;
  final double size;

  @override
  State<LiveDot> createState() => _LiveDotState();
}

class _LiveDotState extends State<LiveDot> with SingleTickerProviderStateMixin {
  late final AnimationController _c = AnimationController(vsync: this, duration: const Duration(milliseconds: 1800));

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _sync();
  }

  @override
  void didUpdateWidget(LiveDot old) {
    super.didUpdateWidget(old);
    _sync();
  }

  void _sync() {
    if (!widget.live || reducedMotion(context)) {
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
    final s = widget.size;
    return SizedBox.square(
      dimension: s * 2.4,
      child: AnimatedBuilder(
        animation: _c,
        builder: (_, _) => Stack(
          alignment: Alignment.center,
          children: [
            if (widget.live && _c.isAnimating)
              Container(
                width: s + s * 1.4 * _c.value,
                height: s + s * 1.4 * _c.value,
                decoration: BoxDecoration(shape: BoxShape.circle, color: widget.color.withValues(alpha: .4 * (1 - _c.value))),
              ),
            Container(width: s, height: s, decoration: BoxDecoration(shape: BoxShape.circle, color: widget.color)),
          ],
        ),
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Surfaces
// ---------------------------------------------------------------------------

/// A bordered surface, the dashboard's card.
class Panel extends StatelessWidget {
  const Panel({super.key, required this.child, this.padding = const EdgeInsets.all(16), this.onTap, this.borderColor, this.color});
  final Widget child;
  final EdgeInsets padding;
  final VoidCallback? onTap;
  final Color? borderColor, color;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    final shape = RoundedRectangleBorder(borderRadius: BorderRadius.circular(10), side: BorderSide(color: borderColor ?? g.border));
    return Material(
      color: color ?? g.surface,
      shape: shape,
      clipBehavior: Clip.antiAlias,
      child: onTap == null ? Padding(padding: padding, child: child) : InkWell(onTap: onTap, child: Padding(padding: padding, child: child)),
    );
  }
}

/// A small uppercase heading above a group.
class SectionLabel extends StatelessWidget {
  const SectionLabel(this.text, {super.key, this.trailing});
  final String text;
  final Widget? trailing;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.fromLTRB(2, 0, 2, 8),
    child: Row(
      children: [
        Expanded(
          child: Text(
            text.toUpperCase(),
            style: TextStyle(color: context.g.muted, fontSize: 12, fontWeight: FontWeight.w500, letterSpacing: .8),
          ),
        ),
        ?trailing,
      ],
    ),
  );
}

/// A titled panel.
class Section extends StatelessWidget {
  const Section({super.key, required this.title, required this.child, this.icon, this.trailing});
  final String title;
  final Widget child;
  final IconData? icon;
  final Widget? trailing;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    return Panel(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              if (icon != null) ...[Icon(icon, size: 16, color: g.muted), const SizedBox(width: 8)],
              Expanded(child: Text(title, style: const TextStyle(fontSize: 14, fontWeight: FontWeight.w600))),
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

/// Command or preview text in a mono block with a copy button.
class CodeBlock extends StatelessWidget {
  const CodeBlock(this.text, {super.key, this.prompt = false, this.maxLines});
  final String text;
  final bool prompt;
  final int? maxLines;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.fromLTRB(12, 10, 4, 10),
      decoration: BoxDecoration(color: g.canvas, borderRadius: BorderRadius.circular(8), border: Border.all(color: g.border)),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Expanded(
            child: ConstrainedBox(
              // Long output scrolls inside the block instead of reserving space.
              constraints: BoxConstraints(maxHeight: maxLines == null ? double.infinity : maxLines! * 13 * 1.55),
              child: SingleChildScrollView(
                child: SelectableText.rich(
                  TextSpan(
                    children: [
                      if (prompt) TextSpan(text: r'$ ', style: mono(context, color: g.muted)),
                      TextSpan(text: text, style: mono(context)),
                    ],
                  ),
                ),
              ),
            ),
          ),
          IconButton(
            tooltip: 'Copy',
            visualDensity: VisualDensity.compact,
            icon: Icon(Icons.content_copy_rounded, size: 16, color: g.muted),
            onPressed: () {
              Clipboard.setData(ClipboardData(text: text));
              HapticFeedback.selectionClick();
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
  const InfoRow(this.icon, this.label, this.value, {super.key, this.monoValue = false, this.valueColor});
  final IconData icon;
  final String label, value;
  final bool monoValue;
  final Color? valueColor;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 7),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Icon(icon, size: 17, color: g.muted),
          const SizedBox(width: 12),
          SizedBox(width: 96, child: Text(label, style: TextStyle(color: g.muted, fontSize: 14))),
          Expanded(
            child: Text(
              value,
              style: monoValue ? mono(context, size: 13, color: valueColor) : TextStyle(color: valueColor ?? g.text, fontSize: 14, height: 1.4),
            ),
          ),
        ],
      ),
    );
  }
}

/// A notice (error, warning, success): tinted, with an icon and words.
class Notice extends StatelessWidget {
  const Notice({super.key, required this.text, required this.color, required this.icon, this.action});
  final String text;
  final Color color;
  final IconData icon;
  final Widget? action;

  @override
  Widget build(BuildContext context) => Container(
    width: double.infinity,
    padding: const EdgeInsets.fromLTRB(12, 12, 12, 12),
    decoration: BoxDecoration(
      color: color.withValues(alpha: .08),
      borderRadius: BorderRadius.circular(8),
      border: Border.all(color: color.withValues(alpha: .35)),
    ),
    child: Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Icon(icon, color: color, size: 18),
        const SizedBox(width: 10),
        Expanded(child: Text(text, style: TextStyle(color: context.g.text, height: 1.45, fontSize: 14))),
        ?action,
      ],
    ),
  );
}

/// A centred empty state: an icon in a quiet square, a title and a line.
class EmptyState extends StatelessWidget {
  const EmptyState({super.key, required this.icon, required this.title, required this.body, this.action, this.color});
  final IconData icon;
  final String title, body;
  final Widget? action;
  final Color? color;

  @override
  Widget build(BuildContext context) {
    final g = context.g;
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 32, vertical: 40),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          PopIn(
            child: Container(
              width: 56,
              height: 56,
              decoration: BoxDecoration(color: g.surface, borderRadius: BorderRadius.circular(14), border: Border.all(color: g.border)),
              child: Icon(icon, color: color ?? g.muted, size: 26),
            ),
          ),
          const SizedBox(height: 16),
          Text(title, style: Theme.of(context).textTheme.titleMedium, textAlign: TextAlign.center),
          const SizedBox(height: 6),
          Text(body, textAlign: TextAlign.center, style: TextStyle(color: g.muted, height: 1.5, fontSize: 14)),
          if (action != null) ...[const SizedBox(height: 18), action!],
        ],
      ),
    );
  }
}

/// A primary button with a busy state.
class PrimaryButton extends StatelessWidget {
  const PrimaryButton({super.key, required this.label, required this.onPressed, this.icon, this.busy = false, this.color});
  final String label;
  final VoidCallback? onPressed;
  final IconData? icon;
  final bool busy;
  final Color? color;

  @override
  Widget build(BuildContext context) {
    final style = color == null ? null : FilledButton.styleFrom(backgroundColor: color);
    final child = AnimatedSwitcher(
      duration: Motion.fast,
      child: busy
          ? const SizedBox.square(key: ValueKey('busy'), dimension: 20, child: CircularProgressIndicator(strokeWidth: 2, color: Colors.white))
          : Row(
              key: const ValueKey('label'),
              mainAxisSize: MainAxisSize.min,
              children: [
                if (icon != null) ...[Icon(icon, size: 19), const SizedBox(width: 8)],
                Text(label),
              ],
            ),
    );
    return Semantics(
      button: true,
      label: busy ? '$label, working' : null,
      child: FilledButton(style: style, onPressed: busy ? null : onPressed, child: child),
    );
  }
}

// ---------------------------------------------------------------------------
// Motion
// ---------------------------------------------------------------------------

/// Fades and rises a child into place after [delay]: list entrances. Stands
/// still under reduced motion.
class FadeSlideIn extends StatefulWidget {
  const FadeSlideIn({super.key, required this.child, this.delay = Duration.zero, this.offset = 14});
  final Widget child;
  final Duration delay;
  final double offset;

  /// Stagger helper: the nth item in a list, capped so long lists don't lag.
  factory FadeSlideIn.indexed(int i, {Key? key, required Widget child}) =>
      FadeSlideIn(key: key, delay: Duration(milliseconds: 40 * math.min(i, 8)), child: child);

  @override
  State<FadeSlideIn> createState() => _FadeSlideInState();
}

class _FadeSlideInState extends State<FadeSlideIn> with SingleTickerProviderStateMixin {
  late final AnimationController _c = AnimationController(vsync: this, duration: Motion.slow);
  Timer? _t;
  bool _started = false;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (_started) return;
    _started = true;
    if (reducedMotion(context)) {
      _c.value = 1;
    } else if (widget.delay == Duration.zero) {
      _c.forward();
    } else {
      _t = Timer(widget.delay, () {
        if (mounted) _c.forward();
      });
    }
  }

  @override
  void dispose() {
    _t?.cancel();
    _c.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final a = CurvedAnimation(parent: _c, curve: Motion.curve);
    return AnimatedBuilder(
      animation: a,
      builder: (_, child) => Opacity(
        opacity: a.value,
        child: Transform.translate(offset: Offset(0, widget.offset * (1 - a.value)), child: child),
      ),
      child: widget.child,
    );
  }
}

/// Scales a child in with a small overshoot.
class PopIn extends StatelessWidget {
  const PopIn({super.key, required this.child, this.duration = const Duration(milliseconds: 420)});
  final Widget child;
  final Duration duration;

  @override
  Widget build(BuildContext context) {
    if (reducedMotion(context)) return child;
    return TweenAnimationBuilder<double>(
      tween: Tween(begin: 0, end: 1),
      duration: duration,
      curve: Curves.easeOutBack,
      builder: (_, v, c) => Opacity(opacity: v.clamp(0, 1), child: Transform.scale(scale: .7 + .3 * v, child: c)),
      child: child,
    );
  }
}

/// A shimmering placeholder block while data loads.
class Skeleton extends StatefulWidget {
  const Skeleton({super.key, this.height = 14, this.width, this.radius = 6});
  final double height;
  final double? width;
  final double radius;

  @override
  State<Skeleton> createState() => _SkeletonState();
}

class _SkeletonState extends State<Skeleton> with SingleTickerProviderStateMixin {
  late final AnimationController _c = AnimationController(vsync: this, duration: const Duration(milliseconds: 1400));

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (reducedMotion(context)) {
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
    final g = context.g;
    return AnimatedBuilder(
      animation: _c,
      builder: (_, _) {
        // A flat pulse between two neutrals: no gradient sweep.
        final t = .5 - .5 * math.cos(_c.value * 2 * math.pi);
        return Container(
          height: widget.height,
          width: widget.width,
          decoration: BoxDecoration(color: Color.lerp(g.surfaceHigh, g.border, t * .7), borderRadius: BorderRadius.circular(widget.radius)),
        );
      },
    );
  }
}

/// A list of skeleton rows shaped like cards.
class SkeletonList extends StatelessWidget {
  const SkeletonList({super.key, this.count = 4});
  final int count;

  @override
  Widget build(BuildContext context) => Column(
    children: [
      for (var i = 0; i < count; i++)
        Padding(
          padding: const EdgeInsets.only(bottom: 10),
          child: Panel(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                const Skeleton(width: 90, height: 12),
                const SizedBox(height: 12),
                Skeleton(width: 180.0 + 30 * (i % 3), height: 14),
                const SizedBox(height: 8),
                const Skeleton(width: 120, height: 12),
              ],
            ),
          ),
        ),
    ],
  );
}

/// Three dots that rise in turn: the agent is thinking.
class TypingDots extends StatefulWidget {
  const TypingDots({super.key, this.color});
  final Color? color;

  @override
  State<TypingDots> createState() => _TypingDotsState();
}

class _TypingDotsState extends State<TypingDots> with SingleTickerProviderStateMixin {
  late final AnimationController _c = AnimationController(vsync: this, duration: const Duration(milliseconds: 1100));

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (reducedMotion(context)) {
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
    final c = widget.color ?? context.g.aiText;
    return Semantics(
      label: 'Working',
      child: AnimatedBuilder(
        animation: _c,
        builder: (_, _) => Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            for (var i = 0; i < 3; i++)
              Builder(
                builder: (_) {
                  final t = ((_c.value - i * .15) % 1.0);
                  final lift = t < .4 ? math.sin(t / .4 * math.pi) : 0.0;
                  return Container(
                    margin: const EdgeInsets.symmetric(horizontal: 2.5),
                    width: 6,
                    height: 6,
                    transform: Matrix4.translationValues(0, -4 * lift, 0),
                    decoration: BoxDecoration(shape: BoxShape.circle, color: c.withValues(alpha: .45 + .55 * lift)),
                  );
                },
              ),
          ],
        ),
      ),
    );
  }
}

/// A check mark that draws itself inside a circle: "done".
class AnimatedCheck extends StatelessWidget {
  const AnimatedCheck({super.key, required this.color, this.size = 44, this.ok = true});
  final Color color;
  final double size;
  final bool ok;

  @override
  Widget build(BuildContext context) {
    final still = reducedMotion(context);
    return TweenAnimationBuilder<double>(
      tween: Tween(begin: still ? 1 : 0, end: 1),
      duration: const Duration(milliseconds: 650),
      curve: Curves.easeOutCubic,
      builder: (_, t, _) => CustomPaint(size: Size.square(size), painter: _CheckPainter(color, t, ok)),
    );
  }
}

class _CheckPainter extends CustomPainter {
  _CheckPainter(this.color, this.t, this.ok);
  final Color color;
  final double t;
  final bool ok;

  @override
  void paint(Canvas canvas, Size size) {
    final r = size.width / 2;
    final c = size.center(Offset.zero);
    final ring = (t / .55).clamp(0.0, 1.0);
    final mark = ((t - .45) / .55).clamp(0.0, 1.0);
    canvas.drawCircle(c, r * (.85 + .15 * ring), Paint()..color = color.withValues(alpha: .14 * ring));
    canvas.drawArc(
      Rect.fromCircle(center: c, radius: r - 1.5),
      -math.pi / 2,
      2 * math.pi * ring,
      false,
      Paint()
        ..color = color
        ..style = PaintingStyle.stroke
        ..strokeWidth = 2.2
        ..strokeCap = StrokeCap.round,
    );
    final p = Paint()
      ..color = color
      ..style = PaintingStyle.stroke
      ..strokeWidth = size.width * .075
      ..strokeCap = StrokeCap.round
      ..strokeJoin = StrokeJoin.round;
    final path = ok
        ? (Path()
            ..moveTo(c.dx - r * .36, c.dy + r * .02)
            ..lineTo(c.dx - r * .1, c.dy + r * .28)
            ..lineTo(c.dx + r * .38, c.dy - r * .24))
        : (Path()
            ..moveTo(c.dx - r * .28, c.dy - r * .28)
            ..lineTo(c.dx + r * .28, c.dy + r * .28)
            ..moveTo(c.dx + r * .28, c.dy - r * .28)
            ..lineTo(c.dx - r * .28, c.dy + r * .28));
    if (mark <= 0) return;
    for (final m in path.computeMetrics()) {
      canvas.drawPath(m.extractPath(0, m.length * mark), p);
    }
  }

  @override
  bool shouldRepaint(_CheckPainter o) => o.t != t || o.color != color || o.ok != ok;
}

/// A count that rolls to its new value; hidden at zero.
class CountBadge extends StatelessWidget {
  const CountBadge(this.count, {super.key, this.color});
  final int count;
  final Color? color;

  @override
  Widget build(BuildContext context) {
    final c = color ?? context.g.brand;
    return AnimatedSwitcher(
      duration: Motion.base,
      transitionBuilder: (child, a) => ScaleTransition(scale: CurvedAnimation(parent: a, curve: Curves.easeOutBack), child: FadeTransition(opacity: a, child: child)),
      child: count == 0
          ? const SizedBox.shrink(key: ValueKey(0))
          : Container(
              key: ValueKey(count),
              constraints: const BoxConstraints(minWidth: 20),
              padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
              decoration: BoxDecoration(color: c, borderRadius: BorderRadius.circular(10)),
              child: Text(
                '$count',
                textAlign: TextAlign.center,
                style: const TextStyle(color: Colors.white, fontSize: 12, fontWeight: FontWeight.w600, fontFeatures: [FontFeature.tabularFigures()]),
              ),
            ),
    );
  }
}

/// A number that counts up to [value] when it first appears or changes.
class CountUp extends StatelessWidget {
  const CountUp(this.value, {super.key, this.style, this.format});
  final double value;
  final TextStyle? style;
  final String Function(double)? format;

  @override
  Widget build(BuildContext context) {
    final f = format ?? (v) => v.round().toString();
    if (reducedMotion(context)) return Text(f(value), style: style);
    return TweenAnimationBuilder<double>(
      tween: Tween(begin: 0, end: value),
      duration: const Duration(milliseconds: 700),
      curve: Curves.easeOutCubic,
      builder: (_, v, _) => Text(f(v), style: style),
    );
  }
}

/// A thin flat meter that fills to [value] (0..1).
class Meter extends StatelessWidget {
  const Meter({super.key, required this.value, required this.color, this.height = 6});
  final double value;
  final Color color;
  final double height;

  @override
  Widget build(BuildContext context) => ClipRRect(
    borderRadius: BorderRadius.circular(height),
    child: Container(
      height: height,
      color: context.g.surfaceHigh,
      alignment: Alignment.centerLeft,
      child: TweenAnimationBuilder<double>(
        tween: Tween(begin: reducedMotion(context) ? value : 0, end: value),
        duration: const Duration(milliseconds: 800),
        curve: Curves.easeOutCubic,
        builder: (_, v, _) => FractionallySizedBox(widthFactor: v.clamp(0, 1), child: Container(color: color)),
      ),
    ),
  );
}

/// Press feedback: the child dips slightly while held.
class Pressable extends StatefulWidget {
  const Pressable({super.key, required this.child, required this.onTap});
  final Widget child;
  final VoidCallback? onTap;

  @override
  State<Pressable> createState() => _PressableState();
}

class _PressableState extends State<Pressable> {
  bool _down = false;

  @override
  Widget build(BuildContext context) => GestureDetector(
    onTapDown: widget.onTap == null ? null : (_) => setState(() => _down = true),
    onTapCancel: () => setState(() => _down = false),
    onTapUp: (_) => setState(() => _down = false),
    onTap: widget.onTap,
    child: AnimatedScale(scale: _down ? .97 : 1, duration: Motion.fast, curve: Motion.curve, child: widget.child),
  );
}

// ---------------------------------------------------------------------------
// Slide to confirm
// ---------------------------------------------------------------------------

/// Slide to confirm: a deliberate gesture for high-risk approvals, so a
/// stray tap can never approve. The thumb springs back if released early.
/// Also operable by screen readers as a button.
class SlideToConfirm extends StatefulWidget {
  const SlideToConfirm({super.key, required this.label, required this.onConfirmed, this.color, this.enabled = true});
  final String label;
  final VoidCallback onConfirmed;
  final Color? color;
  final bool enabled;

  @override
  State<SlideToConfirm> createState() => _SlideToConfirmState();
}

class _SlideToConfirmState extends State<SlideToConfirm> with SingleTickerProviderStateMixin {
  late final AnimationController _c = AnimationController.unbounded(vsync: this);
  double _max = 1;
  bool _hinted = false;
  static const _thumb = 50.0;

  @override
  void dispose() {
    _c.dispose();
    super.dispose();
  }

  void _springTo(double target, double velocity) {
    if (reducedMotion(context)) {
      _c.value = target;
      return;
    }
    _c.animateWith(SpringSimulation(const SpringDescription(mass: 1, stiffness: 420, damping: 26), _c.value, target, velocity));
  }

  void _end(DragEndDetails d) {
    if (_c.value >= _max * .9) {
      HapticFeedback.heavyImpact();
      _c.value = _max;
      widget.onConfirmed();
      Future.delayed(const Duration(milliseconds: 400), () {
        if (mounted) _springTo(0, 0);
      });
    } else {
      _springTo(0, d.velocity.pixelsPerSecond.dx);
    }
  }

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
          _max = math.max(1, c.maxWidth - _thumb - 8);
          return Container(
            height: 58,
            decoration: BoxDecoration(color: color.withValues(alpha: .10), borderRadius: BorderRadius.circular(12), border: Border.all(color: color.withValues(alpha: .45))),
            child: AnimatedBuilder(
              animation: _c,
              builder: (context, _) {
                final x = _c.value.clamp(0.0, _max);
                final p = x / _max;
                return Stack(
                  alignment: Alignment.centerLeft,
                  children: [
                    Positioned(
                      left: 0,
                      top: 0,
                      bottom: 0,
                      width: x + _thumb + 4,
                      child: Container(decoration: BoxDecoration(color: color.withValues(alpha: .16), borderRadius: BorderRadius.circular(11))),
                    ),
                    Center(
                      child: Opacity(
                        opacity: (1 - p * 1.6).clamp(0, 1),
                        child: ExcludeSemantics(
                          child: Row(
                            mainAxisSize: MainAxisSize.min,
                            children: [
                              const SizedBox(width: 40),
                              Text(widget.label, style: TextStyle(color: color, fontWeight: FontWeight.w600, fontSize: 15)),
                              const SizedBox(width: 6),
                              Icon(Icons.keyboard_double_arrow_right_rounded, color: color.withValues(alpha: .7), size: 18),
                            ],
                          ),
                        ),
                      ),
                    ),
                    Positioned(
                      left: 4 + x,
                      child: GestureDetector(
                        onHorizontalDragStart: widget.enabled
                            ? (_) {
                                _c.stop();
                                if (!_hinted) {
                                  _hinted = true;
                                  HapticFeedback.selectionClick();
                                }
                              }
                            : null,
                        onHorizontalDragUpdate: widget.enabled ? (d) => _c.value = (_c.value + d.delta.dx).clamp(0, _max) : null,
                        onHorizontalDragEnd: widget.enabled ? _end : null,
                        child: Container(
                          key: const ValueKey('slide-thumb'),
                          width: _thumb,
                          height: _thumb,
                          decoration: BoxDecoration(color: color, borderRadius: BorderRadius.circular(9)),
                          child: Icon(p > .9 ? Icons.check_rounded : Icons.fingerprint_rounded, color: Colors.white, size: 26),
                        ),
                      ),
                    ),
                  ],
                );
              },
            ),
          );
        },
      ),
    );
  }
}

/// "3m ago", "just now".
String timeAgo(DateTime t) {
  final d = DateTime.now().difference(t);
  if (d.inSeconds < 45) return 'just now';
  if (d.inMinutes < 60) return '${d.inMinutes}m ago';
  if (d.inHours < 24) return '${d.inHours}h ago';
  if (d.inDays < 7) return '${d.inDays}d ago';
  return '${t.year}-${t.month.toString().padLeft(2, '0')}-${t.day.toString().padLeft(2, '0')}';
}

IconData toolIcon(String tool) => switch (tool) {
  'run_terminal' || 'run_command' || 'shell' => Icons.terminal_rounded,
  'write_file' || 'edit_file' || 'create_file' => Icons.edit_note_rounded,
  'read_file' || 'list_files' || 'list_dir' => Icons.description_outlined,
  'git_commit' || 'git_add' || 'git_push' => Icons.commit_rounded,
  'web_search' || 'web_fetch' || 'research' => Icons.travel_explore_rounded,
  'devops' => Icons.dns_outlined,
  'skill_install' => Icons.menu_book_outlined,
  _ => Icons.extension_outlined,
};
