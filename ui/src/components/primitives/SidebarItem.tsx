import { cn } from "@/lib/utils";

export function sidebarItemClass(active: boolean) {
  return cn(
    "relative flex h-7 items-center justify-between gap-2 rounded-md px-2.5 text-[13px] font-medium leading-5 transition-colors duration-150 ease-out focus-visible:ring-2 focus-visible:ring-[var(--pulse-focus-soft)] focus-visible:outline-none",
    active
      ? "bg-[var(--pulse-sidebar-active-bg)] text-[var(--pulse-sidebar-active-text)] shadow-[var(--pulse-sidebar-active-shadow)] before:absolute before:left-0 before:top-1/2 before:h-5 before:w-[2px] before:-translate-y-1/2 before:rounded-full before:bg-[var(--pulse-selection-ring)]"
      : "text-[var(--pulse-sidebar-muted)] hover:bg-[var(--pulse-sidebar-hover-bg)] hover:text-[var(--pulse-sidebar-active-text)]",
  );
}

export function SidebarItemContent({ label, count }: { label: string; count: number }) {
  return (
    <>
      <span className="min-w-0 truncate">{label}</span>
      <span className="text-[12px] tabular-nums text-muted-foreground">{count}</span>
    </>
  );
}
