export interface StoredDBQuery {
  sql: string;
  paramsText: string;
  arrayMode: boolean;
  savedAt: string;
}

const limit = 12;

export function dbQueryStorageKey(appId: string, dbName: string): string {
  return `pulse:db-explorer:${appId}:${dbName}`;
}

export function loadStoredDBQueries(appId: string, dbName: string): StoredDBQuery[] {
  try {
    const raw = window.sessionStorage.getItem(dbQueryStorageKey(appId, dbName));
    if (!raw) {
      return [];
    }
    const parsed = JSON.parse(raw) as StoredDBQuery[];
    if (!Array.isArray(parsed)) {
      return [];
    }
    return parsed.filter((item) => typeof item?.sql === "string");
  } catch {
    return [];
  }
}

export function saveStoredDBQuery(
  appId: string,
  dbName: string,
  query: Omit<StoredDBQuery, "savedAt">,
) {
  const current = loadStoredDBQueries(appId, dbName).filter(
    (item) =>
      !(
        item.sql.trim() === query.sql.trim() &&
        item.paramsText.trim() === query.paramsText.trim() &&
        item.arrayMode === query.arrayMode
      ),
  );

  const next: StoredDBQuery[] = [
    { ...query, savedAt: new Date().toISOString() },
    ...current,
  ].slice(0, limit);

  window.sessionStorage.setItem(dbQueryStorageKey(appId, dbName), JSON.stringify(next));
}

export function parseDBParams(text: string): unknown[] {
  const trimmed = text.trim();
  if (!trimmed) {
    return [];
  }
  const parsed = JSON.parse(trimmed);
  return Array.isArray(parsed) ? parsed : [];
}

export function inferColumns(rows: unknown[]): string[] {
  const first = rows[0];
  if (!first) {
    return [];
  }
  if (Array.isArray(first)) {
    return first.map((_, index) => String(index));
  }
  if (typeof first === "object" && first !== null) {
    return Object.keys(first as Record<string, unknown>);
  }
  return ["value"];
}
