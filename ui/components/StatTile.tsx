import { Card } from "@astryxdesign/core/Card";
import { ClickableCard } from "@astryxdesign/core/ClickableCard";
import { Grid } from "@astryxdesign/core/Grid";
import { SelectableCard } from "@astryxdesign/core/SelectableCard";
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
  const content = (
    <>
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
    </>
  );
  if (!onClick) {
    return (
      <Card
        padding={4}
        xstyle={[styles.tile, active && styles.tileActive].filter(Boolean)}
      >
        {content}
      </Card>
    );
  }
  const accessibleLabel = typeof label === "string" ? label : "Statistic";
  if (active !== undefined) {
    return (
      <SelectableCard
        isSelected={active}
        label={accessibleLabel}
        onChange={onClick}
        padding={4}
        width="100%"
        xstyle={[styles.tile, !active && styles.tileInteractive].filter(Boolean)}
      >
        {content}
      </SelectableCard>
    );
  }
  return (
    <ClickableCard
      label={accessibleLabel}
      onClick={onClick}
      padding={4}
      width="100%"
      xstyle={styles.tile}
    >
      {content}
    </ClickableCard>
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
  tileActive: {
    borderColor: colorVars["--color-accent"],
    boxShadow: `inset 0 0 0 ${borderVars["--border-width"]} ${colorVars["--color-accent"]}`,
  },
  tileInteractive: {
    borderColor: colorVars["--color-border"],
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
