import type { ComponentType } from "react";

export type FilterPillProps = {
  icon?: ComponentType<{ className?: string; strokeWidth?: number }>;
  label: string;
};

export function FilterPill({ icon: Icon, label }: FilterPillProps) {
  return (
    <div
      data-onlava-ui="FilterPill"
      className="inline-flex h-8 items-center gap-2 rounded-md border border-[var(--pulse-separator-subtle)] bg-[var(--pulse-field-surface)] px-3 text-[13px]"
    >
      {Icon ? <Icon className="size-4 text-[var(--pulse-icon-muted)]" /> : null}
      <span className="text-foreground">{label}</span>
      <span aria-hidden="true" className="text-[var(--pulse-icon-muted)]">
        v
      </span>
    </div>
  );
}
