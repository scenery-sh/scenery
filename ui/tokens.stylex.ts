import {
  borderVars,
  colorVars,
  radiusVars,
  shadowVars,
  spacingVars,
  typeScaleVars,
  typographyVars,
} from "@astryxdesign/core/theme/tokens.stylex";
import * as stylex from "@stylexjs/stylex";

// Scenery's semantic token facade. Values remain references to Astryx
// custom properties, so app-owned Astryx themes continue to control them.
export const t = stylex.defineVars({
  accent: colorVars["--color-accent"],
  accentMuted: colorVars["--color-accent-muted"],
  body: colorVars["--color-background-body"],
  surface: colorVars["--color-background-surface"],
  popover: colorVars["--color-background-popover"],
  muted: colorVars["--color-background-muted"],
  overlay: colorVars["--color-overlay"],
  overlayHover: colorVars["--color-overlay-hover"],
  neutral: colorVars["--color-neutral"],
  border: colorVars["--color-border"],
  borderEmphasized: colorVars["--color-border-emphasized"],
  textPrimary: colorVars["--color-text-primary"],
  textSecondary: colorVars["--color-text-secondary"],
  onDark: colorVars["--color-on-dark"],
  success: colorVars["--color-success"],
  successMuted: colorVars["--color-success-muted"],
  warning: colorVars["--color-warning"],
  warningMuted: colorVars["--color-warning-muted"],
  error: colorVars["--color-error"],
  errorMuted: colorVars["--color-error-muted"],

  borderWidth: borderVars["--border-width"],
  radius: radiusVars["--radius-container"],
  radiusElement: radiusVars["--radius-element"],
  radiusFull: radiusVars["--radius-full"],
  shadowLow: shadowVars["--shadow-low"],
  shadowMedium: shadowVars["--shadow-med"],

  space0_5: spacingVars["--spacing-0-5"],
  space1: spacingVars["--spacing-1"],
  space1_5: spacingVars["--spacing-1-5"],
  space2: spacingVars["--spacing-2"],
  space3: spacingVars["--spacing-3"],
  space4: spacingVars["--spacing-4"],
  space5: spacingVars["--spacing-5"],
  space6: spacingVars["--spacing-6"],
  space8: spacingVars["--spacing-8"],
  space10: spacingVars["--spacing-10"],
  space12: spacingVars["--spacing-12"],

  fontBody: typographyVars["--font-family-body"],
  fontCode: typographyVars["--font-family-code"],
  supportingSize: typeScaleVars["--text-supporting-size"],
  supportingLeading: typeScaleVars["--text-supporting-leading"],

  pageGutter: spacingVars["--spacing-4"],
  panelWidth: `calc(${spacingVars["--spacing-10"]} + ${spacingVars["--spacing-10"]} + ${spacingVars["--spacing-10"]} + ${spacingVars["--spacing-10"]} + ${spacingVars["--spacing-10"]})`,
});
