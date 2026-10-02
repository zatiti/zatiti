import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:zatiti_desktop/src/ui/theme.dart';
import 'package:zatiti_desktop/src/ui/tokens.g.dart';

void main() {
  test('generated palette retains the previous desktop colors', () {
    expect(ZatitiPalette.dark.canvas, const Color(0xFF070707));
    expect(ZatitiPalette.dark.accent, const Color(0xFFFCFCFC));
    expect(ZatitiPalette.dark.amberWash, const Color(0x20B88C44));
    expect(ZatitiPalette.light.canvas, const Color(0xFFFAFBF8));
    expect(ZatitiPalette.light.accent, const Color(0xFF315F49));
    expect(ZatitiPalette.light.amberWash, const Color(0x22B88C44));
    expect(ZColorDark.userBubble, const Color(0xFF5A5A5A));
    expect(ZColorDark.composer, const Color(0xFF2F2F2F));
    expect(ZColorLight.overlayScrim, const Color(0x88000000));
  });

  test('theme surfaces and spacing retain the previous values', () {
    for (final brightness in Brightness.values) {
      final theme = buildZatitiTheme(brightness);
      final palette = brightness == Brightness.dark
          ? ZatitiPalette.dark
          : ZatitiPalette.light;
      expect(theme.scaffoldBackgroundColor, palette.canvas);
      expect(theme.colorScheme.primary, palette.accent);
      expect(theme.colorScheme.onPrimary, palette.accentInk);
      expect(theme.colorScheme.surfaceContainer, palette.card);
    }
    expect(Space.xs, 4);
    expect(Space.lg, 16);
    expect(Space.xxl, 32);
    expect(Measure.controlRadius, 12);
    expect(Measure.cardRadius, 18);
  });
}
