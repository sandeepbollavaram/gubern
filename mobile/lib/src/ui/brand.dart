import 'dart:math' as math;
import 'dart:ui' show PathMetric;

import 'package:flutter/material.dart';

import 'theme.dart';

/// Gubern's mark, the same geometry as the dashboard's SVG: a ship's wheel
/// abstracted to a hexagon with a helm notch ("gubernare", to steer).
///
/// [progress] below 1 draws the strokes partially (the launch animation).
class GubernMark extends StatelessWidget {
  const GubernMark({super.key, this.size = 40, this.color, this.progress = 1});
  final double size;
  final Color? color;
  final double progress;

  @override
  Widget build(BuildContext context) => Semantics(
    label: 'Gubern',
    child: CustomPaint(size: Size.square(size), painter: _MarkPainter(color ?? context.g.brand, progress)),
  );
}

/// The mark drawing itself in, once: the launch and loading moment.
class AnimatedGubernMark extends StatefulWidget {
  const AnimatedGubernMark({super.key, this.size = 56, this.repeat = false});
  final double size;
  final bool repeat;

  @override
  State<AnimatedGubernMark> createState() => _AnimatedGubernMarkState();
}

class _AnimatedGubernMarkState extends State<AnimatedGubernMark> with SingleTickerProviderStateMixin {
  late final AnimationController _c = AnimationController(vsync: this, duration: const Duration(milliseconds: 1100));

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (MediaQuery.of(context).disableAnimations) {
      _c.value = 1;
    } else if (!_c.isAnimating) {
      widget.repeat ? _c.repeat(reverse: true) : _c.forward(from: 0);
    }
  }

  @override
  void dispose() {
    _c.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: _c,
    builder: (_, _) => GubernMark(size: widget.size, progress: Curves.easeInOutCubic.transform(_c.value)),
  );
}

class _MarkPainter extends CustomPainter {
  _MarkPainter(this.color, this.progress);
  final Color color;
  final double progress;

  static final Path _hex = Path()
    ..moveTo(16, 2.5)
    ..lineTo(27.7, 9.25)
    ..lineTo(27.7, 22.75)
    ..lineTo(16, 29.5)
    ..lineTo(4.3, 22.75)
    ..lineTo(4.3, 9.25)
    ..close();

  // Helm: an arc from the upper right the long way round to the notch, then
  // a short bar back toward the hub.
  static final Path _helm = Path()
    ..addArc(Rect.fromCircle(center: const Offset(16, 16), radius: 6), -0.45, -(2 * math.pi - 0.85))
    ..lineTo(16.5, 18.4);

  @override
  void paint(Canvas canvas, Size size) {
    canvas.scale(size.width / 32);
    final stroke = Paint()
      ..color = color
      ..style = PaintingStyle.stroke
      ..strokeWidth = 2.4
      ..strokeJoin = StrokeJoin.round
      ..strokeCap = StrokeCap.round;
    if (progress >= 1) {
      canvas.drawPath(_hex, stroke);
      canvas.drawPath(_helm, stroke);
      canvas.drawCircle(const Offset(16, 16), 1.6, Paint()..color = color);
      return;
    }
    // Hexagon first, then the helm, then the hub pops in.
    final hp = (progress / .6).clamp(0.0, 1.0);
    final lp = ((progress - .45) / .45).clamp(0.0, 1.0);
    final cp = ((progress - .85) / .15).clamp(0.0, 1.0);
    _drawPart(canvas, _hex, hp, stroke);
    _drawPart(canvas, _helm, lp, stroke);
    if (cp > 0) canvas.drawCircle(const Offset(16, 16), 1.6 * cp, Paint()..color = color);
  }

  void _drawPart(Canvas canvas, Path path, double t, Paint p) {
    if (t <= 0) return;
    for (final PathMetric m in path.computeMetrics()) {
      canvas.drawPath(m.extractPath(0, m.length * t), p);
    }
  }

  @override
  bool shouldRepaint(_MarkPainter old) => old.color != color || old.progress != progress;
}

/// The Gubern wordmark: the mark beside the name, as in the dashboard rail.
class GubernWordmark extends StatelessWidget {
  const GubernWordmark({super.key, this.size = 26});
  final double size;

  @override
  Widget build(BuildContext context) => Row(
    mainAxisSize: MainAxisSize.min,
    children: [
      GubernMark(size: size),
      SizedBox(width: size * .38),
      Text(
        'Gubern',
        style: TextStyle(fontFamily: sansFamily, fontSize: size * .72, fontWeight: FontWeight.w600, letterSpacing: -.3, color: context.g.text),
      ),
    ],
  );
}
