import { useNavigate, useParams } from "@tanstack/react-router";
import { useEffect, useMemo, useState } from "react";
import {
  inferColumns,
  loadStoredDBQueries,
  parseDBParams,
  saveStoredDBQuery,
  type StoredDBQuery,
} from "../lib/db-explorer";
import { useDashboard } from "../lib/dashboard-context";
import { formatTimestamp } from "../lib/utils";

export function DatabasePage() {
  const navigate = useNavigate();
  const { dbSlug } = useParams({ strict: false });
  const { appId, meta, rpc } = useDashboard();
  const databases = useMemo(
    () => [...(meta?.sql_databases ?? [])].sort((a, b) => a.name.localeCompare(b.name)),
    [meta?.sql_databases],
  );
  const selectedDB = databases.find((database) => database.name === dbSlug) ?? databases[0] ?? null;
  const [sql, setSQL] = useState("select now();");
  const [paramsText, setParamsText] = useState("[]");
  const [arrayMode, setArrayMode] = useState(false);
  const [result, setResult] = useState<unknown[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [history, setHistory] = useState<StoredDBQuery[]>([]);

  useEffect(() => {
    if (!dbSlug && selectedDB) {
      void navigate({ to: "/$appId/db/$dbSlug", params: { appId, dbSlug: selectedDB.name }, replace: true });
    }
  }, [appId, dbSlug, navigate, selectedDB]);

  useEffect(() => {
    if (!selectedDB) {
      setHistory([]);
      return;
    }
    setHistory(loadStoredDBQueries(appId, selectedDB.name));
  }, [appId, selectedDB]);

  const columns = useMemo(() => inferColumns(result), [result]);

  if (!selectedDB) {
    return (
      <div
        data-scenery-ui="DBExplorer"
        data-scenery-database-count={databases.length}
        className="w-full h-[calc(100vh-var(--header-height))] flex items-center justify-center px-8"
      >
        <div
          data-scenery-ui="DBUnavailableState"
          data-scenery-state="intentional-empty"
          className="rounded-md border border-border p-6 text-sm text-muted-foreground"
        >
          No databases found for this app.
        </div>
      </div>
    );
  }

  return (
    <div
      data-scenery-ui="DBExplorer"
      data-scenery-database-count={databases.length}
      className="w-full h-[calc(100vh-var(--header-height))]"
    >
      <div className="flex items-center gap-4 px-3 h-[60px] border-b border-border">
        <div className="w-fit">
          <select
            data-scenery-ui="DatabaseList"
            className="h-9 min-w-64 rounded-md border border-border bg-background px-3 text-sm"
            value={selectedDB.name}
            onChange={(event) => {
              void navigate({
                to: "/$appId/db/$dbSlug",
                params: { appId, dbSlug: event.target.value },
              });
            }}
          >
            {databases.map((database) => (
              <option key={database.name} value={database.name}>
                {database.name}
              </option>
            ))}
          </select>
        </div>
        <div className="text-xs uppercase tracking-wide text-muted-foreground">Postgres</div>
      </div>

      <div className="flex flex-1 w-full h-[calc(100vh-var(--header-height)-60px)]">
        <section className="w-[420px] shrink-0 border-r border-border flex flex-col min-h-0">
          <div className="p-4 border-b border-border space-y-4">
            <div>
              <div className="text-sm font-medium">{selectedDB.name}</div>
              <p className="mt-1 text-xs text-muted-foreground">
                Local Postgres connection discovered from env.
              </p>
            </div>

            <TextAreaField label="SQL" value={sql} onChange={setSQL} minHeight={220} />
            <TextAreaField label="Params JSON array" value={paramsText} onChange={setParamsText} minHeight={88} />

            <label className="inline-flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={arrayMode}
                onChange={(event) => setArrayMode(event.target.checked)}
              />
              Return row arrays instead of objects
            </label>

            <div className="flex items-center gap-3">
              <button
                type="button"
                className="rounded-md px-3 py-2 text-sm h-9 flex items-center gap-2 border border-border transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
                onClick={() => void runQuery()}
                disabled={loading}
              >
                {loading ? "Running..." : "Run query"}
              </button>
              <span className="text-xs text-muted-foreground">
                {result.length} row{result.length === 1 ? "" : "s"}
              </span>
            </div>

            {error ? (
              <div className="rounded-md border border-red-500/30 bg-red-500/10 px-4 py-3 text-sm text-red-500">
                {error}
              </div>
            ) : null}
          </div>

          <div className="flex-1 min-h-0 overflow-auto p-4">
            <div className="text-xs uppercase tracking-wide text-muted-foreground">Recent queries</div>
            <div className="mt-3 space-y-2">
              {history.map((item, index) => (
                <button
                  key={`${item.savedAt}-${index}`}
                  type="button"
                  className="w-full rounded-md border border-border px-3 py-2 text-left text-sm transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
                  onClick={() => {
                    setSQL(item.sql);
                    setParamsText(item.paramsText);
                    setArrayMode(item.arrayMode);
                  }}
                >
                  <div className="truncate font-medium">{firstLine(item.sql)}</div>
                  <div className="mt-1 text-xs text-muted-foreground">{formatTimestamp(item.savedAt)}</div>
                </button>
              ))}
              {history.length === 0 ? (
                <p className="text-sm text-muted-foreground">No recent queries for this database yet.</p>
              ) : null}
            </div>
          </div>
        </section>

        <section className="flex flex-1 grow w-full flex-col min-h-0">
          {result.length === 0 ? (
            <div className="h-full flex items-center justify-center px-8">
              <p className="text-sm text-muted-foreground">Run a query to inspect returned rows.</p>
            </div>
          ) : (
            <div className="h-full overflow-auto">
              <div className="overflow-auto h-full">
                <table className="min-w-full text-sm">
                  <thead className="bg-muted/50 sticky top-0">
                    <tr>
                      {columns.map((column) => (
                        <th
                          key={column}
                          className="px-3 py-2 text-left text-xs font-medium uppercase tracking-wide text-muted-foreground"
                        >
                          {column}
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {result.map((row, rowIndex) => (
                      <tr key={rowIndex} className="border-t border-border">
                        {columns.map((column) => (
                          <td key={`${rowIndex}-${column}`} className="px-3 py-2 align-top">
                            <code className="text-xs whitespace-pre-wrap break-all">
                              {formatCell(row, column)}
                            </code>
                          </td>
                        ))}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              <details className="m-4 rounded-md border border-border p-4">
                <summary className="cursor-pointer text-sm font-medium">Raw JSON</summary>
                <pre className="mt-3 overflow-auto whitespace-pre-wrap text-xs leading-6">
                  {JSON.stringify(result, null, 2)}
                </pre>
              </details>
            </div>
          )}
        </section>
      </div>
    </div>
  );

  async function runQuery() {
    if (!rpc || !selectedDB) {
      return;
    }
    setLoading(true);
    setError(null);
    try {
      const rows = await rpc.request<unknown[]>("db/query", {
        appId,
        dbId: selectedDB.name,
        query: sql,
        params: parseDBParams(paramsText),
        arrayMode,
      });
      setResult(Array.isArray(rows) ? rows : []);
      saveStoredDBQuery(appId, selectedDB.name, { sql, paramsText, arrayMode });
      setHistory(loadStoredDBQueries(appId, selectedDB.name));
    } catch (err) {
      setResult([]);
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }
}

function TextAreaField({
  label,
  value,
  onChange,
  minHeight,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  minHeight: number;
}) {
  return (
    <div className="space-y-2">
      <label className="text-sm font-medium">{label}</label>
      <textarea
        className="w-full rounded-md border border-border px-3 py-2 text-sm font-mono"
        style={{ minHeight }}
        value={value}
        onChange={(event) => onChange(event.target.value)}
      />
    </div>
  );
}

function firstLine(value: string): string {
  return value.split("\n")[0]?.trim() || value.trim();
}

function formatCell(row: unknown, column: string): string {
  if (Array.isArray(row)) {
    const index = Number(column);
    return stringifyValue(row[index]);
  }
  if (typeof row === "object" && row !== null) {
    return stringifyValue((row as Record<string, unknown>)[column]);
  }
  return stringifyValue(row);
}

function stringifyValue(value: unknown): string {
  if (value === null || value === undefined) {
    return "null";
  }
  if (typeof value === "string") {
    return value;
  }
  return JSON.stringify(value);
}
