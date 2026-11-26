export interface ClickRow {
  id: bigint;
  clickId: string;
  slug: string;
  targetUrl: string;
  clickedAt: string;
}

export function connectDatabase(url: string): {
  updateEnrichment(slug: string, title: string, enrichedAt: string): Promise<void>;
  latestClicks(afterId: bigint): Promise<ClickRow[]>;
  close(): Promise<void>;
};
