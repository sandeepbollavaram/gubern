import 'package:flutter/material.dart';

/// Gubern's mobile palette: a calm "command center": deep navy surfaces,
/// the brand blue (#378ADD) with a cyan accent, and risk colours that never
/// rely on colour alone (each comes with an icon and a label).
@immutable
class GubernColors extends ThemeExtension<GubernColors> {
  const GubernColors({
    required this.canvas,
    required this.surface,
    required this.surfaceHigh,
    required this.border,
    required this.text,
    required this.muted,
    required this.brand,
    required this.accent,
    required this.ok,
    required this.warn,
    required this.high,
    required this.critical,
    required this.glow,
  });

  final Color canvas, surface, surfaceHigh, border, text, muted;
  final Color brand, accent, ok, warn, high, critical, glow;

  static const dark = GubernColors(
    canvas: Color(0xFF070B14),
    surface: Color(0xFF0F1626),
    surfaceHigh: Color(0xFF16203A),
    border: Color(0xFF243150),
    text: Color(0xFFE8EEF8),
    muted: Color(0xFF94A3BD),
    brand: Color(0xFF378ADD),
    accent: Color(0xFF22D3EE),
    ok: Color(0xFF34D399),
    warn: Color(0xFFFBBF24),
    high: Color(0xFFFB923C),
    critical: Color(0xFFF87171),
    glow: Color(0x55378ADD),
  );

  static const light = GubernColors(
    canvas: Color(0xFFF4F7FC),
    surface: Color(0xFFFFFFFF),
    surfaceHigh: Color(0xFFEDF2FA),
    border: Color(0xFFD5DEEC),
    text: Color(0xFF0B1324),
    muted: Color(0xFF475569),
    brand: Color(0xFF2F78C8),
    accent: Color(0xFF0891B2),
    ok: Color(0xFF059669),
    warn: Color(0xFFB45309),
    high: Color(0xFFC2410C),
    critical: Color(0xFFDC2626),
    glow: Color(0x332F78C8),
  );

  /// The brand gradient used for primary actions and the mark's halo.
  LinearGradient get brandGradient => LinearGradient(colors: [brand, accent], begin: Alignment.topLeft, end: Alignment.bottomRight);

  /// Colour for an approval risk label ("LOW RISK", "HIGH RISK", ...).
  Color risk(String risk) {
    final r = risk.toUpperCase();
    if (r.startsWith('CRITICAL')) return critical;
    if (r.startsWith('HIGH')) return high;
    if (r.startsWith('MEDIUM')) return warn;
    return ok;
  }

  @override
  GubernColors copyWith() => this;

  @override
  GubernColors lerp(ThemeExtension<GubernColors>? other, double t) => t < .5 ? this : (other as GubernColors? ?? this);
}

extension GubernTheme on BuildContext {
  GubernColors get g => Theme.of(this).extension<GubernColors>() ?? GubernColors.dark;
}

/// Monospace for commands, previews and fingerprints, using platform fonts
/// (no runtime font downloads).
const monoFamily = 'monospace';
const monoFallback = ['Roboto Mono', 'Menlo', 'Courier New'];

TextStyle mono(BuildContext context, {double size = 13, Color? color, FontWeight weight = FontWeight.w400}) =>
    TextStyle(fontFamily: monoFamily, fontFamilyFallback: monoFallback, fontSize: size, height: 1.5, fontWeight: weight, color: color ?? context.g.text);

ThemeData buildTheme(Brightness brightness) {
  final g = brightness == Brightness.dark ? GubernColors.dark : GubernColors.light;
  final scheme = ColorScheme.fromSeed(
    seedColor: g.brand,
    brightness: brightness,
  ).copyWith(primary: g.brand, secondary: g.accent, surface: g.surface, onSurface: g.text, error: g.critical, outline: g.border);
  final base = ThemeData(useMaterial3: true, colorScheme: scheme, brightness: brightness);
  final t = base.textTheme.apply(bodyColor: g.text, displayColor: g.text);
  return base.copyWith(
    scaffoldBackgroundColor: g.canvas,
    extensions: [g],
    textTheme: t.copyWith(
      headlineLarge: t.headlineLarge?.copyWith(fontWeight: FontWeight.w700, letterSpacing: -.8, height: 1.15),
      headlineMedium: t.headlineMedium?.copyWith(fontWeight: FontWeight.w700, letterSpacing: -.6),
      titleLarge: t.titleLarge?.copyWith(fontWeight: FontWeight.w600, letterSpacing: -.3),
      titleMedium: t.titleMedium?.copyWith(fontWeight: FontWeight.w600),
      bodyLarge: t.bodyLarge?.copyWith(height: 1.5),
      bodyMedium: t.bodyMedium?.copyWith(height: 1.5, color: g.text),
      bodySmall: t.bodySmall?.copyWith(color: g.muted),
      labelLarge: t.labelLarge?.copyWith(fontWeight: FontWeight.w600, letterSpacing: .2),
    ),
    appBarTheme: AppBarTheme(backgroundColor: Colors.transparent, foregroundColor: g.text, elevation: 0, scrolledUnderElevation: 0, centerTitle: false),
    cardTheme: CardThemeData(
      color: g.surface,
      elevation: 0,
      margin: EdgeInsets.zero,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(20),
        side: BorderSide(color: g.border),
      ),
    ),
    inputDecorationTheme: InputDecorationTheme(
      filled: true,
      fillColor: g.surfaceHigh,
      labelStyle: TextStyle(color: g.muted),
      hintStyle: TextStyle(color: g.muted),
      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 16),
      border: OutlineInputBorder(
        borderRadius: BorderRadius.circular(14),
        borderSide: BorderSide(color: g.border),
      ),
      enabledBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(14),
        borderSide: BorderSide(color: g.border),
      ),
      focusedBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(14),
        borderSide: BorderSide(color: g.brand, width: 1.6),
      ),
    ),
    outlinedButtonTheme: OutlinedButtonThemeData(
      style: OutlinedButton.styleFrom(
        foregroundColor: g.text,
        minimumSize: const Size.fromHeight(54),
        side: BorderSide(color: g.border, width: 1.2),
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(16)),
        textStyle: const TextStyle(fontSize: 16, fontWeight: FontWeight.w600),
      ),
    ),
    textButtonTheme: TextButtonThemeData(
      style: TextButton.styleFrom(foregroundColor: g.brand, minimumSize: const Size(44, 44)),
    ),
    bottomSheetTheme: BottomSheetThemeData(
      backgroundColor: g.surface,
      showDragHandle: true,
      dragHandleColor: g.border,
      shape: const RoundedRectangleBorder(borderRadius: BorderRadius.vertical(top: Radius.circular(28))),
    ),
    snackBarTheme: SnackBarThemeData(
      behavior: SnackBarBehavior.floating,
      backgroundColor: g.surfaceHigh,
      contentTextStyle: TextStyle(color: g.text),
    ),
    dividerTheme: DividerThemeData(color: g.border, thickness: 1, space: 1),
    pageTransitionsTheme: const PageTransitionsTheme(
      builders: {TargetPlatform.android: CupertinoPageTransitionsBuilder(), TargetPlatform.iOS: CupertinoPageTransitionsBuilder()},
    ),
  );
}
