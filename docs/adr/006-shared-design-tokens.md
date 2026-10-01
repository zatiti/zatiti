# ADR 006: Shared design tokens with a hand-built panel

## Status
Accepted

## Date
2026-09-28

## Context
The desktop (Flutter) and the extension panel must look consistent without a Flutter-generated extension UI. theme.dart is the de facto source of truth, with dark accent #FCFCFC. The retired root internal/desktop, including styles.css, is frozen. Fonts are system-ui. Ferro's extension panel is a good compact reference.

## Decision
design/tokens/tokens.json holds color (light and dark), spacing, radius, typography and a panel measure group. tools/designtokens is a stdlib Go spec-maintenance generator. It writes apps/desktop/lib/src/ui/tokens.g.dart (const values) and apps/extension/panel/tokens.css (--z-* custom properties with a prefers-color-scheme dark block). Its -check mode fails on drift and runs in CI. The generated files are a rendered exception in the desktop and extension briefs, and only the generator writes them.
The side panel is hand-built and compact, refined from ferro's extension. It uses only var(--z-*) and builds the DOM with textContent only. It is not Flutter-generated.
Evidence carried from v2: theme.dart is canonical, styles.css is retired and frozen, and the desktop parity test proves no visual change.

## Consequences
One token edit updates both clients, and CI catches drift. The panel stays small and dependency-free. Token changes need the desktop parity test on a Flutter lane.
