# Desktop dark-theme refinement

Source: user-supplied `grok.png` (reference remains outside the repository).
Implementation: `build/design-review/dark-workspace.png`, Flutter widget-rendered
fictional demo at 1440 × 1000 logical/physical pixels, density 1.

Scope: apply the reference's neutral colors and relaxed conversation treatment
within the existing Flutter workspace; preserve product-specific navigation,
identity avatars, decision records and demo disclosure. The reference depicts a
different application and conversation, so this is not a pixel-identical clone.

Observed colors: canvas #070707, sidebar #111111, assistant surfaces #262626,
user bubbles #5A5A5A, composer #2F2F2F. Rounded bubbles and restrained typography
replace the previous oversized first paragraph. Existing spacing and readable
line height keep conversation content separated. No new raster assets required.

Verification: 30 workspace widget tests pass, including theme switching,
keyboard interactions, doubled text and narrow layout. Static analysis passes.

Visual capture history: initial test capture used Ahem block glyphs. A second
capture loaded a system sans-serif for body text and Material icons. Body text,
colors and bubble spacing can be inspected; some button themes still use the
test font. Native font rasterization, button text wrapping and a full native
window comparison remain unverified. Screenshot evidence is local, ignored
build output; no reference content or personal paths are committed.

final result: blocked

Blocker: complete typography comparison requires a native app capture; the
widget-test renderer substitutes glyphs in some controls. This is a visual QA
limitation, not a failed interaction test or release-qualification claim.
