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
