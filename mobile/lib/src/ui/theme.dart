import 'package:flutter/material.dart';

/// Gubern's tokens, the same values as the dashboard
/// (web/dashboard/src/index.css): quiet neutral surfaces, one brand blue,
/// a violet for agent activity, and status colours that always come with
/// an icon and a word. Flat colour only: no gradients.
@immutable
class GubernColors extends ThemeExtension<GubernColors> {
  const GubernColors({
    required this.canvas,
    required this.rail,
    required this.surface,
    required this.surfaceHigh,
    required this.border,
    required this.text,
    required this.muted,
    required this.brand,
    required this.selected,
    required this.ai,
    required this.aiText,
    required this.ok,
    required this.warn,
    required this.critical,
  });

  final Color canvas, rail, surface, surfaceHigh, border, text, muted;
  final Color brand, selected, ai, aiText, ok, warn, critical;

  /// High risk sits between warning and danger.
  Color get high => Color.lerp(warn, critical, .55)!;

  static const dark = GubernColors(
    canvas: Color(0xFF0D0D0D),
    rail: Color(0xFF0A0A0A),
    surface: Color(0xFF141414),
    surfaceHigh: Color(0xFF1C1C1C),
    border: Color(0xFF2A2A2A),
    text: Color(0xFFE8E8E8),
    muted: Color(0xFF8F8F8F),
    brand: Color(0xFF378ADD),
    selected: Color(0xFF1A2A3A),
    ai: Color(0xFF534AB7),
    aiText: Color(0xFFA9A3EE),
    ok: Color(0xFF1D9E75),
    warn: Color(0xFFEF9F27),
    critical: Color(0xFFD85A30),
  );

  static const light = GubernColors(
    canvas: Color(0xFFFAFAF9),
    rail: Color(0xFFF2F2EF),
    surface: Color(0xFFFFFFFF),
    surfaceHigh: Color(0xFFF4F4F2),
    border: Color(0xFFE2E2DD),
    text: Color(0xFF121212),
    muted: Color(0xFF5F5F5F),
    brand: Color(0xFF1F6CC3),
    selected: Color(0xFFDCEAF8),
    ai: Color(0xFF4A42A8),
    aiText: Color(0xFF433B9C),
    ok: Color(0xFF117957),
    warn: Color(0xFFB4650A),
    critical: Color(0xFFB8461E),
  );

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
  GubernColors lerp(ThemeExtension<GubernColors>? other, double t) {
    final o = other as GubernColors?;
    if (o == null) return this;
    Color l(Color a, Color b) => Color.lerp(a, b, t)!;
    return GubernColors(
      canvas: l(canvas, o.canvas),
      rail: l(rail, o.rail),
      surface: l(surface, o.surface),
      surfaceHigh: l(surfaceHigh, o.surfaceHigh),
      border: l(border, o.border),
      text: l(text, o.text),
      muted: l(muted, o.muted),
      brand: l(brand, o.brand),
      selected: l(selected, o.selected),
      ai: l(ai, o.ai),
      aiText: l(aiText, o.aiText),
      ok: l(ok, o.ok),
      warn: l(warn, o.warn),
      critical: l(critical, o.critical),
    );
  }
}

extension GubernTheme on BuildContext {
  GubernColors get g => Theme.of(this).extension<GubernColors>() ?? GubernColors.dark;
}

const sansFamily = 'FiraSans';
const monoFamily = 'FiraCode';

TextStyle mono(BuildContext context, {double size = 13, Color? color, FontWeight weight = FontWeight.w400}) =>
    TextStyle(fontFamily: monoFamily, fontSize: size, height: 1.55, fontWeight: weight, color: color ?? context.g.text);

/// Motion: short and calm (150–300 ms), one easing for everything.
class Motion {
  static const fast = Duration(milliseconds: 160);
  static const base = Duration(milliseconds: 240);
  static const slow = Duration(milliseconds: 380);
  static const curve = Curves.easeOutCubic;
}

ThemeData buildTheme(Brightness brightness) {
  final g = brightness == Brightness.dark ? GubernColors.dark : GubernColors.light;
  final scheme = ColorScheme(
    brightness: brightness,
    primary: g.brand,
    onPrimary: Colors.white,
    secondary: g.ai,
    onSecondary: Colors.white,
    error: g.critical,
    onError: Colors.white,
    surface: g.surface,
    onSurface: g.text,
    surfaceContainerHighest: g.surfaceHigh,
    outline: g.border,
    outlineVariant: g.border,
  );
  final base = ThemeData(useMaterial3: true, colorScheme: scheme, brightness: brightness, fontFamily: sansFamily);
  final t = base.textTheme.apply(bodyColor: g.text, displayColor: g.text, fontFamily: sansFamily);
  return base.copyWith(
    scaffoldBackgroundColor: g.canvas,
    extensions: [g],
    splashFactory: InkSparkle.splashFactory,
    textTheme: t.copyWith(
      headlineLarge: t.headlineLarge?.copyWith(fontWeight: FontWeight.w600, letterSpacing: -.6, height: 1.15),
      headlineMedium: t.headlineMedium?.copyWith(fontWeight: FontWeight.w600, letterSpacing: -.4),
      headlineSmall: t.headlineSmall?.copyWith(fontWeight: FontWeight.w600, letterSpacing: -.3),
      titleLarge: t.titleLarge?.copyWith(fontWeight: FontWeight.w600, letterSpacing: -.2),
      titleMedium: t.titleMedium?.copyWith(fontWeight: FontWeight.w600),
      bodyLarge: t.bodyLarge?.copyWith(height: 1.55, fontSize: 16),
      bodyMedium: t.bodyMedium?.copyWith(height: 1.5, fontSize: 15),
      bodySmall: t.bodySmall?.copyWith(color: g.muted, fontSize: 13),
      labelLarge: t.labelLarge?.copyWith(fontWeight: FontWeight.w500, fontSize: 15),
    ),
    appBarTheme: AppBarTheme(
      backgroundColor: g.canvas,
      surfaceTintColor: Colors.transparent,
      foregroundColor: g.text,
      elevation: 0,
      scrolledUnderElevation: 0,
      centerTitle: false,
      titleTextStyle: TextStyle(fontFamily: sansFamily, fontSize: 18, fontWeight: FontWeight.w600, color: g.text),
    ),
    navigationBarTheme: NavigationBarThemeData(
      backgroundColor: g.rail,
      surfaceTintColor: Colors.transparent,
      indicatorColor: g.selected,
      height: 68,
      labelTextStyle: WidgetStateProperty.resolveWith(
        (s) => TextStyle(fontFamily: sansFamily, fontSize: 12, fontWeight: s.contains(WidgetState.selected) ? FontWeight.w600 : FontWeight.w400, color: s.contains(WidgetState.selected) ? g.text : g.muted),
      ),
      iconTheme: WidgetStateProperty.resolveWith((s) => IconThemeData(size: 22, color: s.contains(WidgetState.selected) ? g.brand : g.muted)),
    ),
    cardTheme: CardThemeData(color: g.surface, elevation: 0, margin: EdgeInsets.zero, shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12), side: BorderSide(color: g.border))),
    inputDecorationTheme: InputDecorationTheme(
      filled: true,
      fillColor: g.surface,
      labelStyle: TextStyle(color: g.muted),
      hintStyle: TextStyle(color: g.muted),
      contentPadding: const EdgeInsets.symmetric(horizontal: 14, vertical: 14),
      border: OutlineInputBorder(borderRadius: BorderRadius.circular(10), borderSide: BorderSide(color: g.border)),
      enabledBorder: OutlineInputBorder(borderRadius: BorderRadius.circular(10), borderSide: BorderSide(color: g.border)),
      focusedBorder: OutlineInputBorder(borderRadius: BorderRadius.circular(10), borderSide: BorderSide(color: g.brand, width: 1.5)),
    ),
    filledButtonTheme: FilledButtonThemeData(
      style: FilledButton.styleFrom(
        backgroundColor: g.brand,
        foregroundColor: Colors.white,
        disabledBackgroundColor: g.surfaceHigh,
        minimumSize: const Size.fromHeight(50),
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(10)),
        textStyle: const TextStyle(fontFamily: sansFamily, fontSize: 16, fontWeight: FontWeight.w500),
      ),
    ),
    outlinedButtonTheme: OutlinedButtonThemeData(
      style: OutlinedButton.styleFrom(
        foregroundColor: g.text,
        minimumSize: const Size.fromHeight(50),
        side: BorderSide(color: g.border),
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(10)),
        textStyle: const TextStyle(fontFamily: sansFamily, fontSize: 16, fontWeight: FontWeight.w500),
      ),
    ),
    textButtonTheme: TextButtonThemeData(style: TextButton.styleFrom(foregroundColor: g.brand, minimumSize: const Size(44, 44))),
    iconButtonTheme: IconButtonThemeData(style: IconButton.styleFrom(foregroundColor: g.muted, minimumSize: const Size(44, 44))),
    bottomSheetTheme: BottomSheetThemeData(
      backgroundColor: g.surface,
      surfaceTintColor: Colors.transparent,
      showDragHandle: true,
      dragHandleColor: g.border,
      shape: const RoundedRectangleBorder(borderRadius: BorderRadius.vertical(top: Radius.circular(16))),
    ),
    dialogTheme: DialogThemeData(backgroundColor: g.surface, surfaceTintColor: Colors.transparent, shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(14))),
    snackBarTheme: SnackBarThemeData(
      behavior: SnackBarBehavior.floating,
      backgroundColor: g.surfaceHigh,
      contentTextStyle: TextStyle(fontFamily: sansFamily, color: g.text),
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(10), side: BorderSide(color: g.border)),
    ),
    dividerTheme: DividerThemeData(color: g.border, thickness: 1, space: 1),
    chipTheme: ChipThemeData(
      backgroundColor: g.surface,
      selectedColor: g.selected,
      side: BorderSide(color: g.border),
      labelStyle: TextStyle(fontFamily: sansFamily, color: g.text, fontSize: 13),
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(8)),
    ),
    progressIndicatorTheme: ProgressIndicatorThemeData(color: g.brand, linearTrackColor: g.surfaceHigh),
    pageTransitionsTheme: const PageTransitionsTheme(builders: {
      TargetPlatform.android: FadeForwardsPageTransitionsBuilder(),
      TargetPlatform.iOS: CupertinoPageTransitionsBuilder(),
    }),
  );
}
