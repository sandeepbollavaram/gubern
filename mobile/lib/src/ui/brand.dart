import 'dart:math' as math;

import 'package:flutter/material.dart';

import 'theme.dart';

/// Gubern's mark, the same geometry as the dashboard's SVG: a ship's wheel
/// abstracted to a hexagon with a helm notch ("gubernare", to steer).
class GubernMark extends StatelessWidget {
  const GubernMark({super.key, this.size = 40, this.color, this.glow = false});
  final double size;
  final Color? color;
  final bool glow;

  @override
  Widget build(BuildContext context) {
    final c = color ?? context.g.brand;
    final mark = CustomPaint(size: Size.square(size), painter: _MarkPainter(c));
    if (!glow) return Semantics(label: 'Gubern', child: mark);
    return Semantics(
      label: 'Gubern',
      child: Container(
        decoration: BoxDecoration(
          shape: BoxShape.circle,
          boxShadow: [BoxShadow(color: context.g.glow, blurRadius: size * .9, spreadRadius: size * .05)],
        ),
        child: mark,
      ),
    );
  }
}

class _MarkPainter extends CustomPainter {
  _MarkPainter(this.color);
  final Color color;

  @override
  void paint(Canvas canvas, Size size) {
    final s = size.width / 32;
    canvas.scale(s);
    final stroke = Paint()
      ..color = color
      ..style = PaintingStyle.stroke
      ..strokeWidth = 2.4
      ..strokeJoin = StrokeJoin.round
      ..strokeCap = StrokeCap.round;
    final hex = Path()
      ..moveTo(16, 2.5)
      ..lineTo(27.7, 9.25)
      ..lineTo(27.7, 22.75)
      ..lineTo(16, 29.5)
      ..lineTo(4.3, 22.75)
      ..lineTo(4.3, 9.25)
      ..close();
    canvas.drawPath(hex, stroke);
    // Helm: an arc from the upper right round to the notch, then a short bar.
    // Matches the SVG: from (21.5, 13.2) the long way round to (21.8, 18.4),
    // then a bar back toward the hub.
    final helm = Path()..addArc(Rect.fromCircle(center: const Offset(16, 16), radius: 6), -0.45, -(2 * math.pi - 0.85));
    helm.lineTo(16.5, 18.4);
    canvas.drawPath(helm, stroke);
    canvas.drawCircle(const Offset(16, 16), 1.6, Paint()..color = color);
  }

  @override
  bool shouldRepaint(_MarkPainter old) => old.color != color;
}

/// A slow, soft aurora behind hero screens. It stands still when the system
/// asks for reduced motion.
class AuroraBackground extends StatefulWidget {
  const AuroraBackground({super.key, required this.child});
  final Widget child;

  @override
  State<AuroraBackground> createState() => _AuroraBackgroundState();
}

class _AuroraBackgroundState extends State<AuroraBackground> with SingleTickerProviderStateMixin {
  late final AnimationController _c = AnimationController(vsync: this, duration: const Duration(seconds: 14));

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (MediaQuery.of(context).disableAnimations) {
      _c.stop();
    } else if (!_c.isAnimating) {
      _c.repeat(reverse: true);
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
      builder: (context, child) {
        final t = Curves.easeInOut.transform(_c.value);
        return DecoratedBox(
          decoration: BoxDecoration(color: g.canvas),
          child: Stack(
            children: [
              Positioned.fill(
                child: DecoratedBox(
                  decoration: BoxDecoration(
                    gradient: RadialGradient(center: Alignment(-.8 + t * .5, -1.1), radius: 1.1, colors: [g.brand.withValues(alpha: .28), Colors.transparent]),
                  ),
                ),
              ),
              Positioned.fill(
                child: DecoratedBox(
                  decoration: BoxDecoration(
                    gradient: RadialGradient(center: Alignment(1.1 - t * .4, -.35), radius: .9, colors: [g.accent.withValues(alpha: .16), Colors.transparent]),
                  ),
                ),
              ),
              child!,
            ],
          ),
        );
      },
      child: widget.child,
    );
  }
}

/// The "all clear" illustration: a shield with a check inside concentric
/// rings, drawn in code so it follows the theme.
class AllClearArt extends StatelessWidget {
  const AllClearArt({super.key, this.size = 148});
  final double size;

  @override
  Widget build(BuildContext context) => ExcludeSemantics(
    child: CustomPaint(size: Size.square(size), painter: _AllClearPainter(context.g)),
  );
}

class _AllClearPainter extends CustomPainter {
  _AllClearPainter(this.g);
  final GubernColors g;

  @override
  void paint(Canvas canvas, Size size) {
    final c = size.center(Offset.zero);
    final r = size.width / 2;
    for (var i = 3; i >= 1; i--) {
      canvas.drawCircle(c, r * i / 3, Paint()..color = g.brand.withValues(alpha: .05 * i));
    }
    final w = r * .78, h = r * .9;
    final shield = Path()
      ..moveTo(c.dx, c.dy - h * .62)
      ..cubicTo(c.dx + w * .35, c.dy - h * .48, c.dx + w * .55, c.dy - h * .46, c.dx + w * .62, c.dy - h * .44)
      ..cubicTo(c.dx + w * .62, c.dy + h * .05, c.dx + w * .4, c.dy + h * .42, c.dx, c.dy + h * .62)
      ..cubicTo(c.dx - w * .4, c.dy + h * .42, c.dx - w * .62, c.dy + h * .05, c.dx - w * .62, c.dy - h * .44)
      ..cubicTo(c.dx - w * .55, c.dy - h * .46, c.dx - w * .35, c.dy - h * .48, c.dx, c.dy - h * .62)
      ..close();
    canvas.drawPath(
      shield,
      Paint()
        ..shader = LinearGradient(
          colors: [g.brand, g.accent],
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
        ).createShader(Rect.fromCircle(center: c, radius: r)),
    );
    final check = Path()
      ..moveTo(c.dx - w * .24, c.dy + h * .02)
      ..lineTo(c.dx - w * .05, c.dy + h * .2)
      ..lineTo(c.dx + w * .28, c.dy - h * .18);
    canvas.drawPath(
      check,
      Paint()
        ..color = Colors.white
        ..style = PaintingStyle.stroke
        ..strokeWidth = r * .1
        ..strokeCap = StrokeCap.round
        ..strokeJoin = StrokeJoin.round,
    );
  }

  @override
  bool shouldRepaint(_AllClearPainter old) => old.g != g;
}
