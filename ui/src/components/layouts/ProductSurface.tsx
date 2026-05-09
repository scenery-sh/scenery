import type { HTMLAttributes } from "react";
import { cn } from "@/lib/utils";

export function ProductSidebar({ className, ...props }: HTMLAttributes<HTMLElement>) {
  return (
    <aside
      data-onlava-ui="ProductSidebar"
      className={cn(
        "w-[230px] shrink-0 overflow-hidden rounded-lg border border-[var(--pulse-separator-subtle)] bg-[var(--pulse-sidebar-surface)]",
        className,
      )}
      {...props}
    />
  );
}

export function ProductMain({ className, ...props }: HTMLAttributes<HTMLElement>) {
  return (
    <main
      data-onlava-ui="ProductMain"
      className={cn("flex min-w-0 flex-1 flex-col overflow-hidden rounded-lg bg-[var(--pulse-work-surface)]", className)}
      {...props}
    />
  );
}

export function ProductHeader({ className, ...props }: HTMLAttributes<HTMLElement>) {
  return (
    <header
      data-onlava-ui="ProductHeader"
      className={cn("flex min-h-14 shrink-0 items-center justify-between gap-3 border-b border-[var(--pulse-separator-subtle)] px-[18px]", className)}
      {...props}
    />
  );
}

export function ProductToolbar({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      data-onlava-ui="ProductToolbar"
      className={cn("border-b border-[var(--pulse-separator-subtle)] bg-[var(--pulse-toolbar-surface)] p-3", className)}
      {...props}
    />
  );
}

export function ProductPanel({ className, ...props }: HTMLAttributes<HTMLElement>) {
  return (
    <section
      data-onlava-ui="ProductPanel"
      className={cn("rounded-lg border border-[var(--pulse-separator-subtle)] bg-[var(--pulse-panel-surface)]", className)}
      {...props}
    />
  );
}

export function ProductMetaBox({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      data-onlava-ui="ProductMetaBox"
      className={cn("rounded-md border border-[var(--pulse-separator-subtle)] bg-[var(--pulse-field-surface)] px-2 py-2 text-[12px]", className)}
      {...props}
    />
  );
}
