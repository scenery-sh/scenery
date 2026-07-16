import type { BadgeVariant } from "@astryxdesign/core/Badge";
import { Badge } from "@astryxdesign/core/Badge";
import type { ReactNode } from "react";

export type StatusStyle = {
  label: ReactNode;
  variant: BadgeVariant;
  icon?: ReactNode;
};

export type StatusMap = Record<string, StatusStyle>;

export function StatusBadge({
  status,
  map,
  fallback,
}: {
  status: string;
  map: StatusMap;
  fallback?: Partial<StatusStyle>;
}) {
  const entry = map[status];
  return (
    <Badge
      icon={entry?.icon ?? fallback?.icon}
      label={entry?.label ?? fallback?.label ?? humanize(status)}
      variant={entry?.variant ?? fallback?.variant ?? "neutral"}
    />
  );
}

export function humanize(value: string) {
  if (!value) return "—";
  const spaced = value.replaceAll("_", " ");
  return spaced.charAt(0).toUpperCase() + spaced.slice(1);
}
