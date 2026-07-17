import * as stylex from "@stylexjs/stylex";
import { Text } from "@astryxdesign/core/Text";
import { VStack as Stack } from "@astryxdesign/core/VStack";
import {
  colorVars,
  spacingVars as spaces,
} from "@astryxdesign/core/theme/tokens.stylex";

const styles = stylex.create({
  root: {
    color: colorVars["--color-text-primary"],
    padding: {
      default: spaces["--spacing-4"],
      "@media (min-width: 40rem)": "2rem",
    },
    border: "1px solid #abc",
    background: "rgb(1, 2, 3)",
    width: `calc(${spaces["--spacing-10"]} * 5)`,
  },
});

export function Example() {
  const fake = "<div style={{ color: '#fff', width: '99px' }} />";
  // <div style={{ color: "#000" }} />
  return (
    <Stack xstyle={styles.root}>
      <Text>Safe</Text>
      <div style={{ display: "contents" }}>{fake}</div>
    </Stack>
  );
}
