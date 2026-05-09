import type { DashboardRpcClient } from "@/lib/rpc";

export type DataInspectResponse = {
  schema_version: string;
  schemas: {
    metadata: string;
    records: string;
  };
  warnings?: string[];
  tenants: DataTenantSummary[];
  objects: DataObjectSummary[];
  migrations: DataMigrationSummary;
  outbox: DataOutboxSummary;
};

export type DataTenantSummary = {
  id: string;
  key: string;
  name: string;
  objects: number;
  latest_outbox_seq: number;
};

export type DataObjectSummary = {
  id: string;
  tenant_id: string;
  tenant_key: string;
  name: string;
  physical_table: string;
  schema_version: number;
  outbox_triggers_enabled: boolean;
  outbox_trigger_name?: string;
  outbox_trigger_present: boolean;
  fields: DataFieldSummary[];
  indexes: DataIndexSummary[];
  views?: DataViewSummary[];
};

export type DataFieldSummary = {
  name: string;
  label: string;
  type: string;
  columns: string[];
  searchable: boolean;
  search_weight?: string;
  relation?: DataRelationSummary;
};

export type DataRelationSummary = {
  object: string;
  kind: string;
  inverse_field?: string;
  on_delete: string;
  join_table_name?: string;
};

export type DataIndexSummary = {
  name: string;
  physical_name: string;
  method: string;
  unique: boolean;
  fields: { name: string; direction?: string; opclass?: string }[];
  physical: { exists: boolean; drift: boolean };
};

export type DataViewSummary = {
  name: string;
  type: string;
  columns: string[];
  filter?: DataFilter;
  sort?: DataSort[];
  limit?: number;
  visibility: string;
  owner_id?: string;
};

export type DataMigrationSummary = {
  pending: number;
  failed: number;
  latest: DataMigrationRecord[];
};

export type DataMigrationRecord = {
  id: string;
  tenant_id: string;
  tenant_key?: string;
  object_id?: string;
  object?: string;
  from_version: number;
  to_version: number;
  status: string;
  ddl?: string[];
  started_at: string;
  finished_at?: string;
  error?: string;
};

export type DataOutboxSummary = {
  latest_seq: number;
  unpublished: number;
};

export type DataFilter = {
  op: string;
  field?: string;
  value?: unknown;
  values?: unknown[];
  filters?: DataFilter[];
};

export type DataSort = {
  field: string;
  desc?: boolean;
};

export type DataQuery = {
  object?: string;
  select?: string[];
  filter?: DataFilter;
  sort?: DataSort[];
  limit?: number;
  cursor?: string;
};

export type DataRecord = Record<string, unknown>;

export type DataRecordPage = {
  records: DataRecord[];
  next_cursor?: string;
};

export type DataOutboxEvent = {
  seq: number;
  event_id: string;
  tenant_id: string;
  tenant_key: string;
  object_id?: string;
  object: string;
  record_id?: string;
  action: string;
  actor_id?: string;
  schema_version: number;
  changed_fields?: string[];
  before?: DataRecord;
  after?: DataRecord;
  diff?: DataRecord;
  created_at: string;
};

export type DataExplorerClient = {
  inspect(params?: { tenant_key?: string; object?: string }): Promise<DataInspectResponse>;
  queryRecords(params: {
    tenant_key: string;
    object: string;
    query: DataQuery;
  }): Promise<DataRecordPage>;
  outboxEvents(params: {
    tenant_key?: string;
    object?: string;
    after_seq?: number;
    limit?: number;
  }): Promise<DataOutboxEvent[]>;
};

export function createDataExplorerClient(rpc: DashboardRpcClient, appId: string): DataExplorerClient {
  return {
    inspect(params = {}) {
      return rpc.request<DataInspectResponse>("data/inspect", {
        app_id: appId,
        tenant_key: params.tenant_key ?? "",
        object: params.object ?? "",
      });
    },
    queryRecords(params) {
      return rpc.request<DataRecordPage>("data/query-records", {
        app_id: appId,
        tenant_key: params.tenant_key,
        object: params.object,
        query: params.query,
      });
    },
    outboxEvents(params) {
      return rpc.request<DataOutboxEvent[]>("data/outbox-events", {
        app_id: appId,
        tenant_key: params.tenant_key ?? "",
        object: params.object ?? "",
        after_seq: params.after_seq ?? 0,
        limit: params.limit ?? 50,
      });
    },
  };
}

export function parseFilterInput(value: string): DataFilter | undefined {
  const trimmed = value.trim();
  if (trimmed === "") {
    return undefined;
  }
  const parsed = JSON.parse(trimmed) as DataFilter;
  if (!parsed || typeof parsed !== "object" || typeof parsed.op !== "string") {
    throw new Error("Filter must be a JSON object with an op string.");
  }
  return parsed;
}

export function andFilters(...filters: Array<DataFilter | undefined>): DataFilter | undefined {
  const items = filters.filter((filter): filter is DataFilter => Boolean(filter));
  if (items.length === 0) {
    return undefined;
  }
  if (items.length === 1) {
    return items[0];
  }
  return { op: "and", filters: items };
}

export function recordColumns(object: DataObjectSummary | null, records: DataRecord[]): string[] {
  const columns = new Set<string>(["id", "created_at", "updated_at"]);
  for (const field of object?.fields ?? []) {
    columns.add(field.name);
  }
  for (const record of records) {
    for (const key of Object.keys(record)) {
      columns.add(key);
    }
  }
  return Array.from(columns);
}
