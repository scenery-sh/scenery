import { Button } from "@astryxdesign/core/Button";
import { Card } from "@astryxdesign/core/Card";
import { Grid } from "@astryxdesign/core/Grid";
import { Text } from "@astryxdesign/core/Text";
import {
  borderVars,
  colorVars,
  spacingVars,
} from "@astryxdesign/core/theme/tokens.stylex";
import * as stylex from "@stylexjs/stylex";
import type { ReactNode } from "react";

export type StatTone = "neutral" | "success" | "warning" | "danger";

export function StatGrid({
  columns,
  children,
}: {
  columns?: number;
  children: ReactNode;
}) {
  return (
    <Grid columns={{ minWidth: 180, max: columns, repeat: "fit" }} gap={3}>
      {children}
    </Grid>
  );
}

export function StatTile({
  label,
  value,
  sub,
  icon,
  tone = "neutral",
  active,
  onClick,
}: {
  label: ReactNode;
  value: ReactNode;
  sub?: ReactNode;
  icon?: ReactNode;
  tone?: StatTone;
  active?: boolean;
  onClick?: () => void;
}) {
  const card = (
    <Card
      padding={4}
      xstyle={[styles.tile, active && styles.tileActive].filter(Boolean)}
    >
      <Text color="secondary" type="supporting" xstyle={styles.label}>
        {icon ? <span {...stylex.props(styles.icon)}>{icon}</span> : null}
        {label}
      </Text>
      <Text
        as="div"
        hasTabularNumbers
        size="2xl"
        weight="semibold"
        xstyle={
          tone === "success"
            ? styles.valueSuccess
            : tone === "warning"
              ? styles.valueWarning
              : tone === "danger"
                ? styles.valueDanger
                : undefined
        }
      >
        {value}
      </Text>
      {sub ? (
        <Text color="secondary" type="supporting">
          {sub}
        </Text>
      ) : null}
    </Card>
  );
  if (!onClick) return card;
  return (
    <Button
      aria-pressed={active}
      label={typeof label === "string" ? label : "Statistic"}
      onClick={onClick}
      variant="ghost"
      width="100%"
      xstyle={styles.tileButton}
    >
      {card}
    </Button>
  );
}

const styles = stylex.create({
  tile: {
    boxSizing: "border-box",
    minWidth: 0,
    width: "100%",
    display: "flex",
    flexDirection: "column",
    gap: spacingVars["--spacing-1"],
    textAlign: "start",
  },
  tileButton: {
    height: "auto",
    padding: 0,
    borderWidth: 0,
    backgroundColor: "transparent",
    display: "block",
    textAlign: "start",
  },
  tileActive: {
    borderColor: colorVars["--color-accent"],
    boxShadow: `inset 0 0 0 ${borderVars["--border-width"]} ${colorVars["--color-accent"]}`,
  },
  label: {
    display: "flex",
    alignItems: "center",
    gap: spacingVars["--spacing-1"],
  },
  icon: {
    display: "inline-flex",
    alignItems: "center",
    color: colorVars["--color-text-secondary"],
  },
  valueSuccess: { color: colorVars["--color-success"] },
  valueWarning: { color: colorVars["--color-warning"] },
  valueDanger: { color: colorVars["--color-error"] },
});
