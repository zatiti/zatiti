// Theme tokens based on the design of record, with neutral dark surfaces
// refined from the supplied desktop chat reference.

import 'package:flutter/material.dart';

/// Spacing rhythm: 4, 8, 12, 16, 24, 32.
abstract final class Space {
  static const double xs = 4;
  static const double sm = 8;
  static const double md = 12;
  static const double lg = 16;
  static const double xl = 24;
  static const double xxl = 32;
}

/// Layout measures from the design.
abstract final class Measure {
  static const double sidebar = 302;
  static const double sidebarLarge = 316;
  static const double largeWindow = 1600;
  static const double conversation = 790;
  static const double details = 336;

  /// Below this width the details panel overlays the conversation.
  static const double detailsInline = 1180;

  /// Below this width the sidebar becomes a dismissible overlay.
  static const double sidebarInline = 820;

  static const double controlRadius = 12;
  static const double cardRadius = 18;
  static const double dialogRadius = 18;
}

enum AvatarTone { mint, peach, lavender, blue }

@immutable
class ZatitiPalette extends ThemeExtension<ZatitiPalette> {
  const ZatitiPalette({
    required this.canvas,
    required this.sidebar,
    required this.card,
    required this.hover,
    required this.line,
    required this.text,
    required this.muted,
    required this.subtle,
    required this.accent,
    required this.accentInk,
    required this.amber,
    required this.amberWash,
    required this.decisionLine,
    required this.avatars,
  });

  final Color canvas;
  final Color sidebar;
  final Color card;
  final Color hover;
  final Color line;
  final Color text;
  final Color muted;
  final Color subtle;

  /// High-contrast primary actions; neutral white in dark mode.
  final Color accent;
  final Color accentInk;

  /// Amber: actual pending decisions only.
  final Color amber;
  final Color amberWash;
  final Color decisionLine;

  /// Background and foreground per avatar tone.
  final Map<AvatarTone, (Color, Color)> avatars;

  static const dark = ZatitiPalette(
    canvas: Color(0xFF070707),
    sidebar: Color(0xFF111111),
    card: Color(0xFF262626),
    hover: Color(0xFF323232),
    line: Color(0xFF232323),
    text: Color(0xFFE7E7E7),
    muted: Color(0xFFA0A0A0),
    subtle: Color(0xFF929292),
    accent: Color(0xFFFCFCFC),
    accentInk: Color(0xFF111111),
    amber: Color(0xFFE4C391),
    amberWash: Color(0x20B88C44),
    decisionLine: Color(0xFF4A4334),
    avatars: {
      AvatarTone.mint: (Color(0xFFB8D8C8), Color(0xFF20352B)),
      AvatarTone.peach: (Color(0xFF42372E), Color(0xFFE0BA92)),
      AvatarTone.lavender: (Color(0xFF35323F), Color(0xFFC7B9E3)),
      AvatarTone.blue: (Color(0xFF2C3840), Color(0xFFADCCDF)),
    },
  );

  static const light = ZatitiPalette(
    canvas: Color(0xFFFAFBF8),
    sidebar: Color(0xFFF0F2EE),
    card: Color(0xFFFFFFFF),
    hover: Color(0xFFE8ECE6),
    line: Color(0xFFD8DDD6),
    text: Color(0xFF222B25),
    muted: Color(0xFF5D6A61),
    subtle: Color(0xFF647168),
    accent: Color(0xFF315F49),
    accentInk: Color(0xFFFFFFFF),
    amber: Color(0xFF886022),
    amberWash: Color(0x22B88C44),
    decisionLine: Color(0xFFC9B38F),
    avatars: {
      AvatarTone.mint: (Color(0xFFD2E4D7), Color(0xFF254C36)),
      AvatarTone.peach: (Color(0xFFEADBCC), Color(0xFF70533B)),
      AvatarTone.lavender: (Color(0xFFE3DDEC), Color(0xFF635479)),
      AvatarTone.blue: (Color(0xFFDAE5ED), Color(0xFF416278)),
    },
  );

  static ZatitiPalette of(BuildContext context) =>
      Theme.of(context).extension<ZatitiPalette>()!;

  @override
  ZatitiPalette copyWith() => this;

  @override
  ZatitiPalette lerp(ThemeExtension<ZatitiPalette>? other, double t) =>
      t < 0.5 || other is! ZatitiPalette ? this : other;
}

/// A stable tone per identity, so an avatar never changes color with a name.
AvatarTone toneFor(String identity, {required bool root}) {
  if (root) return AvatarTone.mint;
  const tones = [AvatarTone.peach, AvatarTone.lavender, AvatarTone.blue];
  var h = 0;
  for (final c in identity.codeUnits) {
    h = (h * 31 + c) & 0x7FFFFFFF;
  }
  return tones[h % tones.length];
}

ThemeData buildZatitiTheme(Brightness brightness) {
  final p = brightness == Brightness.dark
      ? ZatitiPalette.dark
      : ZatitiPalette.light;
  final scheme =
      ColorScheme.fromSeed(
        seedColor: p.accent,
        brightness: brightness,
      ).copyWith(
        primary: p.accent,
        onPrimary: p.accentInk,
        surface: p.canvas,
        onSurface: p.text,
        onSurfaceVariant: p.muted,
        outline: p.line,
        outlineVariant: p.line,
        surfaceContainerLowest: p.canvas,
        surfaceContainerLow: p.sidebar,
        surfaceContainer: p.card,
        surfaceContainerHigh: p.hover,
        surfaceContainerHighest: p.hover,
        surfaceTint: Colors.transparent,
        error: brightness == Brightness.dark
            ? const Color(0xFFE8A598)
            : const Color(0xFF9B3B2C),
      );
  final control = RoundedRectangleBorder(
    borderRadius: BorderRadius.circular(Measure.controlRadius),
  );
  final base = ThemeData(
    useMaterial3: true,
    brightness: brightness,
    colorScheme: scheme,
    scaffoldBackgroundColor: p.canvas,
    extensions: [p],
    splashFactory: NoSplash.splashFactory,
    visualDensity: VisualDensity.standard,
  );
  // System sans-serif; 12–15 px body and control text, 22–26 px emphasis.
  final text = base.textTheme
      .apply(bodyColor: p.text, displayColor: p.text)
      .copyWith(
        headlineSmall: TextStyle(
          fontSize: 24,
          height: 1.25,
          fontWeight: FontWeight.w500,
          letterSpacing: -0.6,
          color: p.text,
        ),
        titleLarge: TextStyle(
          fontSize: 22,
          height: 1.25,
          fontWeight: FontWeight.w500,
          letterSpacing: -0.5,
          color: p.text,
        ),
        titleMedium: TextStyle(
          fontSize: 15,
          fontWeight: FontWeight.w500,
          color: p.text,
        ),
        titleSmall: TextStyle(
          fontSize: 13,
          fontWeight: FontWeight.w500,
          color: p.text,
        ),
        bodyLarge: TextStyle(fontSize: 15, height: 1.6, color: p.text),
        bodyMedium: TextStyle(fontSize: 14, height: 1.55, color: p.text),
        bodySmall: TextStyle(fontSize: 12, height: 1.45, color: p.muted),
        labelLarge: TextStyle(
          fontSize: 13,
          fontWeight: FontWeight.w500,
          color: p.text,
        ),
        labelMedium: TextStyle(fontSize: 12, color: p.muted),
        labelSmall: TextStyle(
          fontSize: 11,
          letterSpacing: 0.6,
          fontWeight: FontWeight.w500,
          color: p.muted,
        ),
      );
  return base.copyWith(
    textTheme: text,
    dividerTheme: DividerThemeData(color: p.line, thickness: 1, space: 1),
    iconTheme: IconThemeData(color: p.muted, size: 19),
    focusColor: p.accent.withValues(alpha: 0.18),
    hoverColor: p.hover,
    filledButtonTheme: FilledButtonThemeData(
      style: FilledButton.styleFrom(
        backgroundColor: p.accent,
        foregroundColor: p.accentInk,
        disabledBackgroundColor: p.hover,
        disabledForegroundColor: p.subtle,
        shape: control,
        minimumSize: const Size(44, 40),
        padding: const EdgeInsets.symmetric(horizontal: Space.lg),
        textStyle: text.labelLarge,
      ),
    ),
    outlinedButtonTheme: OutlinedButtonThemeData(
      style: OutlinedButton.styleFrom(
        foregroundColor: p.text,
        backgroundColor: p.hover,
        side: BorderSide.none,
        shape: control,
        minimumSize: const Size(44, 40),
        padding: const EdgeInsets.symmetric(horizontal: Space.lg),
        textStyle: text.labelLarge,
      ),
    ),
    textButtonTheme: TextButtonThemeData(
      style: TextButton.styleFrom(
        foregroundColor: p.muted,
        shape: control,
        minimumSize: const Size(44, 40),
        textStyle: text.labelMedium,
      ),
    ),
    iconButtonTheme: IconButtonThemeData(
      style: IconButton.styleFrom(
        foregroundColor: p.muted,
        shape: control,
        minimumSize: const Size(40, 40),
      ),
    ),
    dialogTheme: DialogThemeData(
      backgroundColor: p.sidebar,
      surfaceTintColor: Colors.transparent,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(Measure.dialogRadius),
        side: BorderSide(color: p.line),
      ),
    ),
    inputDecorationTheme: InputDecorationTheme(
      filled: true,
      fillColor: p.card,
      hintStyle: TextStyle(color: p.subtle),
      border: OutlineInputBorder(
        borderRadius: BorderRadius.circular(Measure.controlRadius + 1),
        borderSide: BorderSide(color: p.line),
      ),
      enabledBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(Measure.controlRadius + 1),
        borderSide: BorderSide(color: p.line),
      ),
      focusedBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(Measure.controlRadius + 1),
        borderSide: BorderSide(color: p.accent, width: 2),
      ),
    ),
    tabBarTheme: TabBarThemeData(
      labelColor: p.text,
      unselectedLabelColor: p.muted,
      indicatorColor: p.accent,
      dividerColor: p.line,
      labelStyle: text.labelLarge,
      unselectedLabelStyle: text.labelLarge,
      tabAlignment: TabAlignment.start,
    ),
    tooltipTheme: TooltipThemeData(
      decoration: BoxDecoration(
        color: p.hover,
        borderRadius: BorderRadius.circular(6),
      ),
      textStyle: TextStyle(color: p.text, fontSize: 12),
    ),
  );
}
