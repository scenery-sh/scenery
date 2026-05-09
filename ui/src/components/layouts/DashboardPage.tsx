import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

export type DashboardPageProps = {
  title: string;
  description?: string;
  toolbar?: ReactNode;
  content: ReactNode;
  sidebar?: ReactNode;
  inspector?: ReactNode;
  className?: string;
};

export function DashboardPage({
  title,
  description,
  toolbar,
  content,
  sidebar,
  inspector,
  className,
}: DashboardPageProps) {
  return (
    <section data-onlava-ui="DashboardPage" className={cn("flex h-full min-h-0 flex-col", className)}>
      <header data-slot="header" className="shrink-0 border-b border-border px-5 py-4">
        <div className="flex min-w-0 items-start justify-between gap-4">
          <div className="min-w-0">
            <h1 className="truncate text-base font-semibold">{title}</h1>
            {description ? <p className="mt-1 text-sm text-muted-foreground">{description}</p> : null}
          </div>
          {toolbar ? <div data-slot="toolbar">{toolbar}</div> : null}
        </div>
      </header>
      <div data-slot="body" className="grid min-h-0 flex-1 grid-cols-[minmax(0,1fr)] overflow-hidden">
        {sidebar || inspector ? (
          <div className="grid min-h-0 grid-cols-[240px_minmax(0,1fr)_320px] overflow-hidden">
            <aside data-slot="sidebar" className="min-h-0 overflow-auto border-r border-border">
              {sidebar}
            </aside>
            <main data-slot="content" className="min-h-0 overflow-auto">
              {content}
            </main>
            <aside data-slot="inspector" className="min-h-0 overflow-auto border-l border-border">
              {inspector}
            </aside>
          </div>
        ) : (
          <main data-slot="content" className="min-h-0 overflow-auto">
            {content}
          </main>
        )}
      </div>
    </section>
  );
}
