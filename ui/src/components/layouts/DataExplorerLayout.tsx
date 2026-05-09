import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

export type DataExplorerLayoutProps = {
  title: string;
  objectList: ReactNode;
  table: ReactNode;
  toolbar?: ReactNode;
  inspector?: ReactNode;
  eventStream?: ReactNode;
  className?: string;
};

export function DataExplorerLayout({
  title,
  objectList,
  table,
  toolbar,
  inspector,
  eventStream,
  className,
}: DataExplorerLayoutProps) {
  return (
    <section data-onlava-ui="DataExplorerLayout" className={cn("grid h-full min-h-0 grid-rows-[auto_minmax(0,1fr)]", className)}>
      <header data-slot="header" className="flex min-h-14 items-center justify-between gap-3 border-b border-border px-5">
        <h1 className="truncate text-base font-semibold">{title}</h1>
        {toolbar ? <div data-slot="toolbar">{toolbar}</div> : null}
      </header>
      <div data-slot="body" className="grid min-h-0 grid-cols-[260px_minmax(0,1fr)_320px] overflow-hidden">
        <aside data-slot="object-list" className="min-h-0 overflow-auto border-r border-border">
          {objectList}
        </aside>
        <main data-slot="table" className="min-h-0 overflow-auto">
          {table}
        </main>
        <aside data-slot="inspector" className="grid min-h-0 grid-rows-[minmax(0,1fr)_minmax(0,240px)] overflow-hidden border-l border-border">
          <div className="min-h-0 overflow-auto">{inspector}</div>
          {eventStream ? (
            <div data-slot="event-stream" className="min-h-0 overflow-auto border-t border-border">
              {eventStream}
            </div>
          ) : null}
        </aside>
      </div>
    </section>
  );
}
