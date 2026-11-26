import { connectDatabase } from "./database.mjs";
import { type Constructor, eventStream, sseEvent } from "./scenerycontract/index.js";

export const newService: Constructor = ({ databases }) => {
  if (!databases.database) throw new Error("missing native database URL");
  const database = connectDatabase(databases.database);

  return {
    async Stop() {
      await database.close();
    },

    async EnrichLink(_context, input) {
      const title = deriveTitle(input.targetUrl);
      const enrichedAt = new Date().toISOString();
      await database.updateEnrichment(input.slug, title, enrichedAt);
      return {
        kind: "result",
        name: "ok",
        value: { slug: input.slug, title },
      };
    },

    LiveClicks(context) {
      return eventStream(
        { kind: "result" as const, name: "event" as const },
        streamClicks(database, context.signal),
      );
    },
  };
};

async function* streamClicks(
  database: ReturnType<typeof connectDatabase>,
  signal: AbortSignal,
) {
  let cursor = 0n;
  while (!signal.aborted) {
    const rows = await database.latestClicks(cursor);
    for (const row of rows) {
      if (row.id > cursor) cursor = row.id;
      yield sseEvent(
        {
          clickId: row.clickId,
          slug: row.slug,
          targetUrl: row.targetUrl,
          clickedAt: row.clickedAt,
        },
        { id: row.clickId, event: "click" },
      );
    }
    await sleepAbortable(500, signal);
  }
}

function sleepAbortable(ms: number, signal: AbortSignal): Promise<void> {
  if (signal.aborted) return Promise.resolve();
  return new Promise((resolve) => {
    const onAbort = () => {
      clearTimeout(timer);
      resolve();
    };
    const timer = setTimeout(() => {
      signal.removeEventListener("abort", onAbort);
      resolve();
    }, ms);
    signal.addEventListener("abort", onAbort, { once: true });
  });
}

function deriveTitle(rawUrl: string): string {
  try {
    const parsed = new URL(rawUrl);
    const host = parsed.hostname.replace(/^www\./, "");
    const segments = parsed.pathname
      .split("/")
      .filter(Boolean)
      .slice(0, 2)
      .map((s) => decodeURIComponent(s).replace(/[-_]+/g, " "));
    if (segments.length === 0) return host;
    const label = segments.join(" / ");
    return `${host} — ${label.charAt(0).toUpperCase()}${label.slice(1)}`;
  } catch {
    return rawUrl;
  }
}
