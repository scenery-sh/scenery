import type { CSSProperties, ReactNode } from "react";
import { cn } from "@/lib/utils";

export type AppShellProps = {
  topbar: ReactNode;
  children: ReactNode;
  compileError?: ReactNode;
  className?: string;
};

export function AppShell({ topbar, children, compileError, className }: AppShellProps) {
  return (
    <div
      data-onlava-ui="AppShell"
      className={cn("h-screen overflow-hidden bg-background text-foreground", className)}
      style={{ "--header-height": "52px" } as CSSProperties}
    >
      <header data-slot="topbar" className="fixed top-0 z-50 flex w-full items-center border-b border-topnav-border bg-topnav text-topnav-foreground">
        {topbar}
      </header>
      <div data-slot="body" style={{ height: "100vh", overflow: "hidden", paddingTop: "var(--header-height)" }}>
        {compileError ? <div data-slot="compile-error">{compileError}</div> : null}
        {children}
      </div>
    </div>
  );
}

export function appShellNavItemClass(active = false, muted = false): string {
  return cn(
    "flex h-8 flex-row items-center gap-2 rounded-md px-2 py-2 text-sm transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
    active && "bg-sidebar-accent text-sidebar-accent-foreground",
    muted && "opacity-90",
  );
}

export function appShellTopbarActionClass(): string {
  return "flex h-8 items-center gap-2 rounded-md px-3 py-2 text-sm transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground focus:outline-none";
}

export function appShellIconButtonClass(): string {
  return "inline-flex size-9 items-center justify-center rounded-md transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground";
}

export function appShellAppMenuButtonClass(): string {
  return "flex h-8 min-w-0 cursor-pointer items-center gap-1 overflow-hidden rounded-md px-2 py-2 text-left transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground focus:outline-none";
}
