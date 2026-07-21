import { Theme as BaseTheme } from "@astryxdesign/core/theme";
import * as stylex from "@stylexjs/stylex";
import type { ComponentProps } from "react";
import { tTheme } from "../tokens.stylex.js";

export type ThemeProps = ComponentProps<typeof BaseTheme>;

// Keep Scenery's semantic StyleX aliases inside the active Astryx theme.
// display: contents preserves the provider's existing layout contract.
export function Theme({ children, ...props }: ThemeProps) {
  return (
    <BaseTheme {...props}>
      <div {...stylex.props(tTheme, styles.scope)}>{children}</div>
    </BaseTheme>
  );
}

const styles = stylex.create({
  scope: {
    display: "contents",
  },
});
