import { useCallback, useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import type { TablePageColumn, TablePageDateTimeRange, TablePageFilter, TablePageProps, TablePageQuery, TablePageResult } from "./contract-types.js";
import "./theme.css";

function text(value: unknown): string {
  if (value === null || value === undefined) return "";
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}

function cell<Row extends object>(column: TablePageColumn<Row>, row: Row): ReactNode {
  const value = row[column.field];
  if (column.component) {
    const Component = column.component;
    return <Component row={row} value={value} />;
  }
  if (column.appearance === "datetime" && typeof value === "string") {
    return <time dateTime={value}>{new Date(value).toLocaleString()}</time>;
  }
  if (column.appearance === "badge") return <span className="scenery-table-page__badge">{text(value)}</span>;
  if (column.appearance === "number" && typeof value === "number") return value.toLocaleString();
  return text(value);
}

function enumFilter(filter: Extract<TablePageFilter, { readonly kind: "enum" }>, value: string | undefined, onChange: (value: string | undefined) => void): ReactNode {
  if (filter.component) {
    const Component = filter.component;
    return <Component label={filter.label} value={value} onChange={onChange} />;
  }
  return <select aria-label={filter.label} value={value ?? ""} onChange={(event) => onChange(event.target.value || undefined)}>
    <option value="">All</option>
    {filter.options.map((option) => <option key={option} value={option}>{option}</option>)}
  </select>;
}

function localDateTime(value: string | undefined): string {
  if (!value) return "";
  const instant = new Date(value);
  if (Number.isNaN(instant.getTime())) return "";
  return new Date(instant.getTime() - instant.getTimezoneOffset() * 60_000).toISOString().slice(0, 16);
}

function exactDateTime(value: string): string | undefined {
  return value ? new Date(value).toISOString() : undefined;
}

function dateTimeFilter(filter: Extract<TablePageFilter, { readonly kind: "datetime" }>, value: TablePageDateTimeRange, onChange: (value: TablePageDateTimeRange) => void): ReactNode {
  if (filter.component) {
    const Component = filter.component;
    return <Component label={filter.label} value={value} onChange={(next) => onChange(next ?? {})} />;
  }
  return <span className="scenery-table-page__range">
    <input aria-label={`${filter.label} from`} type="datetime-local" value={localDateTime(value.from)} onChange={(event) => onChange({ ...value, from: exactDateTime(event.target.value) })} />
    <input aria-label={`${filter.label} to`} type="datetime-local" value={localDateTime(value.to)} onChange={(event) => onChange({ ...value, to: exactDateTime(event.target.value) })} />
  </span>;
}

export function TablePage<Row extends object>(props: TablePageProps<Row>) {
  const defaultSort = props.sorts.find((sort) => sort.default);
  const [filters, setFilters] = useState<Readonly<Record<string, string | readonly string[] | undefined>>>({});
  const [sort, setSort] = useState(defaultSort?.field);
  const [direction, setDirection] = useState(defaultSort?.default ?? "asc");
  const [cursor, setCursor] = useState<string>();
  const [history, setHistory] = useState<readonly (string | undefined)[]>([]);
  const [reloadKey, setReloadKey] = useState(0);
  const [result, setResult] = useState<TablePageResult<Row>>({ kind: "result", items: [] });
  const [loading, setLoading] = useState(true);

  const query = useMemo<TablePageQuery>(() => ({ filters, sort, direction, cursor, limit: props.pageSize }), [cursor, direction, filters, props.pageSize, sort]);
  useEffect(() => {
    let current = true;
    setLoading(true);
    void props.load(query).then((next) => {
      if (current) setResult(next);
    }).finally(() => {
      if (current) setLoading(false);
    });
    return () => { current = false; };
  }, [props.load, query, reloadKey]);

  const resetQuery = useCallback(() => {
    setCursor(undefined);
    setHistory([]);
  }, []);
  const filtered = Object.values(filters).some((value) => value !== undefined && value !== "" && (!Array.isArray(value) || value.length > 0));
  const items = result.kind === "result" ? result.items : [];
  const Empty = props.slots?.empty;
  const Toolbar = props.slots?.toolbar;

  return <main className="scenery-table-page">
    <header className="scenery-table-page__header">
      <div><h1>{props.title}</h1>{props.description && <p>{props.description}</p>}</div>
      {Toolbar && <Toolbar loading={loading} reload={() => setReloadKey((value) => value + 1)} />}
    </header>
    {(props.filters.length > 0 || props.sorts.length > 0) && <div className="scenery-table-page__controls">
      {props.filters.map((filter) => {
        if (filter.kind === "enum") {
          const current = filters[filter.field];
          return <label key={filter.field}><span>{filter.label}</span>{enumFilter(filter, Array.isArray(current) ? current[0] : undefined, (value) => {
            setFilters((values) => ({ ...values, [filter.field]: value ? [value] : undefined }));
            resetQuery();
          })}</label>;
        }
        const range = { from: filters[`${filter.field}_from`] as string | undefined, to: filters[`${filter.field}_to`] as string | undefined };
        return <label key={filter.field}><span>{filter.label}</span>{dateTimeFilter(filter, range, (value) => {
          setFilters((values) => ({ ...values, [`${filter.field}_from`]: value.from, [`${filter.field}_to`]: value.to }));
          resetQuery();
        })}</label>;
      })}
      {props.sorts.length > 0 && <label><span>Sort</span><select value={sort ?? ""} onChange={(event) => { setSort(event.target.value || undefined); resetQuery(); }}>
        {props.sorts.map((item) => <option key={item.field} value={item.field}>{item.label}</option>)}
      </select></label>}
      {props.sorts.length > 0 && <label><span>Direction</span><select value={direction} onChange={(event) => { setDirection(event.target.value === "desc" ? "desc" : "asc"); resetQuery(); }}><option value="asc">Ascending</option><option value="desc">Descending</option></select></label>}
    </div>}
    {result.kind === "error" && <div className="scenery-table-page__error" role="alert"><strong>{result.name}</strong><span>{result.problem.message}</span></div>}
    <div className="scenery-table-page__table-wrap" aria-busy={loading}>
      <table><caption className="scenery-table-page__sr-only">{props.title}</caption><thead><tr>{props.columns.map((column) => <th key={String(column.field)} scope="col">{column.label}</th>)}</tr></thead>
        <tbody>{items.map((row, index) => {
          const href = props.rowLink?.(row);
          return <tr key={href ?? index}>{props.columns.map((column, columnIndex) => <td key={String(column.field)}>{href && columnIndex === 0 ? <a href={href}>{cell(column, row)}</a> : cell(column, row)}</td>)}</tr>;
        })}</tbody>
      </table>
      {!loading && result.kind === "result" && items.length === 0 && (Empty ? <Empty filtered={filtered} /> : <p className="scenery-table-page__empty">{filtered ? "No matching results." : "No results yet."}</p>)}
      {loading && <p className="scenery-table-page__status" aria-live="polite">Loading…</p>}
    </div>
    {result.kind === "result" && <nav className="scenery-table-page__pagination" aria-label={`${props.title} pagination`}>
      <button type="button" disabled={loading || history.length === 0} onClick={() => { const previous = history.at(-1); setHistory((value) => value.slice(0, -1)); setCursor(previous); }}>Previous</button>
      <button type="button" disabled={loading || !result.nextCursor} onClick={() => { setHistory((value) => [...value, cursor]); setCursor(result.nextCursor); }}>Next</button>
    </nav>}
    {props.children}
  </main>;
}
