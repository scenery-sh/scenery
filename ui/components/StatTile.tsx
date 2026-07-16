import {
  borderVars,
  colorVars,
  radiusVars,
  spacingVars,
  typeScaleVars,
} from "@astryxdesign/core/theme/tokens.stylex";
import * as stylex from "@stylexjs/stylex";
import type { CSSProperties, ReactNode } from "react";

export type StatTone = "neutral" | "success" | "warning" | "danger";

export function StatGrid({
  columns,
  children,
}: {
  columns?: number;
  children: ReactNode;
}) {
  const style = columns
    ? ({ "--stat-columns": String(columns) } as CSSProperties)
    : undefined;
  return (
    <div
      {...stylex.props(columns ? styles.gridFixed : styles.gridAuto)}
      style={style}
    >
      {children}
    </div>
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
  const body = (
    <>
      <span {...stylex.props(styles.label)}>
        {icon ? <span {...stylex.props(styles.icon)}>{icon}</span> : null}
        {label}
      </span>
      <strong
        {...stylex.props(
          styles.value,
          tone === "success" && styles.valueSuccess,
          tone === "warning" && styles.valueWarning,
          tone === "danger" && styles.valueDanger,
        )}
      >
        {value}
      </strong>
      {sub ? <small {...stylex.props(styles.sub)}>{sub}</small> : null}
    </>
  );
  if (onClick) {
    return (
      <button
        aria-pressed={active}
        onClick={onClick}
        type="button"
        {...stylex.props(styles.tile, styles.tileButton, active && styles.tileActive)}
      >
        {body}
      </button>
    );
  }
  return <section {...stylex.props(styles.tile)}>{body}</section>;
}

const styles = stylex.create({
  gridAuto: {
    display: "grid",
    gridTemplateColumns: "repeat(auto-fit, minmax(180px, 1fr))",
    gap: spacingVars["--spacing-3"],
  },
  gridFixed: {
    display: "grid",
    gridTemplateColumns: {
      default: "repeat(var(--stat-columns, 4), minmax(0, 1fr))",
      "@media (max-width: 900px)": "repeat(2, minmax(0, 1fr))",
      "@media (max-width: 520px)": "1fr",
    },
    gap: spacingVars["--spacing-3"],
  },
  tile: {
    boxSizing: "border-box",
    minWidth: 0,
    padding: spacingVars["--spacing-4"],
    display: "flex",
    flexDirection: "column",
    gap: spacingVars["--spacing-1"],
    borderWidth: borderVars["--border-width"],
    borderStyle: "solid",
    borderColor: colorVars["--color-border"],
    borderRadius: radiusVars["--radius-container"],
    backgroundColor: colorVars["--color-background-body"],
    textAlign: "left",
  },
  tileButton: {
    appearance: "none",
    color: colorVars["--color-text-primary"],
    cursor: "pointer",
    transitionProperty: "border-color",
    transitionDuration: "150ms",
    ":hover": { borderColor: colorVars["--color-border-emphasized"] },
  },
  tileActive: {
    borderColor: colorVars["--color-accent"],
    boxShadow: `inset 0 0 0 ${borderVars["--border-width"]} ${colorVars["--color-accent"]}`,
  },
  label: {
    display: "flex",
    alignItems: "center",
    gap: spacingVars["--spacing-1"],
    color: colorVars["--color-text-secondary"],
    fontSize: 12,
  },
  icon: {
    display: "inline-flex",
    alignItems: "center",
    color: colorVars["--color-text-secondary"],
  },
  value: {
    fontSize: 24,
    fontWeight: typeScaleVars["--text-label-weight"],
    fontVariantNumeric: "tabular-nums",
  },
  valueSuccess: { color: colorVars["--color-success"] },
  valueWarning: { color: colorVars["--color-warning"] },
  valueDanger: { color: colorVars["--color-error"] },
  sub: { color: colorVars["--color-text-secondary"] },
});
