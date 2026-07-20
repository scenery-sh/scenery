import {
  borderVars,
  colorVars,
  radiusVars,
  spacingVars,
  typeScaleVars,
} from "@astryxdesign/core/theme/tokens.stylex";
import * as stylex from "@stylexjs/stylex";
import type { ReactNode } from "react";
import type { StatTone } from "./StatTile.js";

export interface FilterPillOption {
  readonly value: string;
  readonly label: ReactNode;
  readonly count?: number | string;
  readonly sub?: ReactNode;
  readonly tone?: StatTone;
  readonly icon?: ReactNode;
}

export function FilterPills({
  label,
  options,
  value,
  onChange,
  allLabel,
  allCount,
  collapseZero,
}: {
  readonly label: string;
  readonly options: readonly FilterPillOption[];
  readonly value?: string;
  readonly onChange: (value: string | undefined) => void;
  readonly allLabel?: ReactNode;
  readonly allCount?: number | string;
  readonly collapseZero?: boolean;
}) {
  // A zero-count option stays visible while selected so it can be cleared.
  const visible = collapseZero
    ? options.filter((option) => option.count !== 0 || option.value === value)
    : options;
  const hiddenCount = options.length - visible.length;
  return (
    <div aria-label={label} role="group" {...stylex.props(styles.row)}>
      {allLabel !== undefined ? (
        <Pill
          active={!value}
          count={allCount}
          label={allLabel}
          onClick={() => onChange(undefined)}
        />
      ) : null}
      {visible.map((option) => (
        <Pill
          active={option.value === value}
          count={option.count}
          icon={option.icon}
          key={option.value}
          label={option.label}
          onClick={() =>
            onChange(option.value === value ? undefined : option.value)
          }
          sub={option.sub}
          tone={option.tone}
        />
      ))}
      {hiddenCount > 0 ? (
        <span {...stylex.props(styles.pill, styles.collapsed)}>
          {hiddenCount} empty
        </span>
      ) : null}
    </div>
  );
}

function Pill({
  active,
  count,
  icon,
  label,
  onClick,
  sub,
  tone = "neutral",
}: {
  active: boolean;
  count?: number | string;
  icon?: ReactNode;
  label: ReactNode;
  onClick: () => void;
  sub?: ReactNode;
  tone?: StatTone;
}) {
  return (
    <button
      aria-pressed={active}
      onClick={onClick}
      type="button"
      {...stylex.props(styles.pill, styles.pillButton, active && styles.pillActive)}
    >
      {icon ? <span {...stylex.props(styles.icon)}>{icon}</span> : null}
      <span {...stylex.props(styles.label)}>{label}</span>
      {count !== undefined ? (
        <span
          {...stylex.props(
            styles.count,
            tone === "success" && styles.countSuccess,
            tone === "warning" && styles.countWarning,
            tone === "danger" && styles.countDanger,
          )}
        >
          {typeof count === "number" ? count.toLocaleString() : count}
        </span>
      ) : null}
      {sub !== undefined ? <span {...stylex.props(styles.sub)}>{sub}</span> : null}
    </button>
  );
}

const styles = stylex.create({
  row: {
    minWidth: 0,
    display: "flex",
    flexWrap: "wrap",
    alignItems: "center",
    gap: spacingVars["--spacing-2"],
  },
  pill: {
    boxSizing: "border-box",
    minHeight: 30,
    paddingInline: spacingVars["--spacing-3"],
    display: "inline-flex",
    alignItems: "center",
    gap: spacingVars["--spacing-1"],
    borderWidth: borderVars["--border-width"],
    borderStyle: "solid",
    borderColor: colorVars["--color-border"],
    borderRadius: radiusVars["--radius-full"],
    backgroundColor: colorVars["--color-background-body"],
    fontSize: 13,
  },
  pillButton: {
    appearance: "none",
    color: colorVars["--color-text-primary"],
    cursor: "pointer",
    transitionProperty: "border-color, background-color",
    transitionDuration: "150ms",
    ":hover": { borderColor: colorVars["--color-border-emphasized"] },
  },
  pillActive: {
    borderColor: colorVars["--color-accent"],
    boxShadow: `inset 0 0 0 ${borderVars["--border-width"]} ${colorVars["--color-accent"]}`,
  },
  collapsed: {
    borderStyle: "dashed",
    color: colorVars["--color-text-secondary"],
  },
  icon: {
    display: "inline-flex",
    alignItems: "center",
    color: colorVars["--color-text-secondary"],
  },
  label: {
    fontWeight: typeScaleVars["--text-label-weight"],
  },
  count: {
    color: colorVars["--color-text-secondary"],
    fontVariantNumeric: "tabular-nums",
  },
  countSuccess: { color: colorVars["--color-success"] },
  countWarning: { color: colorVars["--color-warning"] },
  countDanger: { color: colorVars["--color-error"] },
  sub: {
    color: colorVars["--color-text-secondary"],
    fontSize: 12,
    fontVariantNumeric: "tabular-nums",
  },
});
