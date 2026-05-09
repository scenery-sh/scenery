import { useCallback, useEffect, useMemo, useState } from "react";
import { DataExplorerLayout } from "@/components/layouts/DataExplorerLayout";
import { PageToolbar } from "@/components/layouts/PageToolbar";
import { Button } from "@/components/primitives/Button";
import { Card, CardContent } from "@/components/primitives/Card";
import { Input, Select, Textarea } from "@/components/primitives/Input";
import { useDashboard } from "@/lib/dashboard-context";
import { andFilters, createDataExplorerClient, parseFilterInput, type DataExplorerClient } from "./dataExplorerClient";
import type { DataInspectResponse, DataObjectSummary, DataOutboxEvent, DataRecord } from "./dataExplorerClient";
import { ObjectInspector } from "./ObjectInspector";
import { ObjectList } from "./ObjectList";
import { OutboxEventTail } from "./OutboxEventTail";
import { RecordTable } from "./RecordTable";

export function DataExplorerPage() {
  const { appId, rpc } = useDashboard();
  const client = useMemo(() => (rpc ? createDataExplorerClient(rpc, appId) : null), [appId, rpc]);
  return <DataExplorerView appId={appId} client={client} />;
}

export function DataExplorerView({
  appId,
  client,
}: {
  appId: string;
  client: DataExplorerClient | null;
}) {
  const [inspect, setInspect] = useState<DataInspectResponse | null>(null);
  const [selectedTenantKey, setSelectedTenantKey] = useState("");
  const [selectedObjectName, setSelectedObjectName] = useState("");
  const [selectedViewName, setSelectedViewName] = useState("");
  const [records, setRecords] = useState<DataRecord[]>([]);
  const [events, setEvents] = useState<DataOutboxEvent[]>([]);
  const [filterText, setFilterText] = useState("");
  const [searchText, setSearchText] = useState("");
  const [limitText, setLimitText] = useState("50");
  const [inspectError, setInspectError] = useState<string | null>(null);
  const [recordError, setRecordError] = useState<string | null>(null);
  const [inspectLoading, setInspectLoading] = useState(false);
  const [recordsLoading, setRecordsLoading] = useState(false);
  const [eventsLoading, setEventsLoading] = useState(false);

  const selectedObject = useMemo(() => {
    return inspect?.objects.find((object) => (
      object.tenant_key === selectedTenantKey && object.name === selectedObjectName
    )) ?? null;
  }, [inspect?.objects, selectedObjectName, selectedTenantKey]);
  const selectedView = useMemo(() => {
    return selectedObject?.views?.find((view) => view.name === selectedViewName) ?? null;
  }, [selectedObject, selectedViewName]);

  const refreshInspect = useCallback(async () => {
    if (!client) {
      return;
    }
    setInspectLoading(true);
    setInspectError(null);
    try {
      const next = await client.inspect();
      setInspect(next);
      const firstTenant = next.tenants[0]?.key ?? "";
      const tenant = next.tenants.some((item) => item.key === selectedTenantKey) ? selectedTenantKey : firstTenant;
      const firstObject = next.objects.find((object) => object.tenant_key === tenant)?.name ?? "";
      const object = next.objects.some((item) => item.tenant_key === tenant && item.name === selectedObjectName)
        ? selectedObjectName
        : firstObject;
      setSelectedTenantKey(tenant);
      setSelectedObjectName(object);
      const objectSummary = next.objects.find((item) => item.tenant_key === tenant && item.name === object);
      const view = objectSummary?.views?.some((item) => item.name === selectedViewName)
        ? selectedViewName
        : objectSummary?.views?.[0]?.name ?? "";
      setSelectedViewName(view);
    } catch (err) {
      setInspect(null);
      setInspectError(err instanceof Error ? err.message : String(err));
    } finally {
      setInspectLoading(false);
    }
  }, [client, selectedObjectName, selectedTenantKey]);

  const queryRecords = useCallback(async () => {
    if (!client || !selectedTenantKey || !selectedObject) {
      setRecords([]);
      return;
    }
    setRecordsLoading(true);
    setRecordError(null);
    try {
      const explicitFilter = parseFilterInput(filterText);
      const searchFilter = searchText.trim() ? { op: "search", value: searchText.trim() } : undefined;
      const filter = andFilters(explicitFilter ?? selectedView?.filter, searchFilter);
      const limit = Math.max(1, Math.min(250, Number.parseInt(limitText, 10) || 50));
      const page = await client.queryRecords({
        tenant_key: selectedTenantKey,
        object: selectedObject.name,
        query: {
          select: selectedView?.columns?.length ? selectedView.columns : selectedObject.fields.map((field) => field.name),
          filter,
          sort: selectedView?.sort,
          limit: selectedView?.limit ?? limit,
        },
      });
      setRecords(page.records ?? []);
    } catch (err) {
      setRecords([]);
      setRecordError(err instanceof Error ? err.message : String(err));
    } finally {
      setRecordsLoading(false);
    }
  }, [client, filterText, limitText, searchText, selectedObject, selectedTenantKey, selectedView]);

  const loadEvents = useCallback(async () => {
    if (!client) {
      return;
    }
    setEventsLoading(true);
    try {
      const next = await client.outboxEvents({
        tenant_key: selectedTenantKey,
        object: selectedObjectName,
        limit: 50,
      });
      setEvents(next ?? []);
    } catch {
      setEvents([]);
    } finally {
      setEventsLoading(false);
    }
  }, [client, selectedObjectName, selectedTenantKey]);

  useEffect(() => {
    void refreshInspect();
  }, [refreshInspect]);

  useEffect(() => {
    void queryRecords();
    void loadEvents();
  }, [queryRecords, loadEvents]);

  const toolbar = (
    <DataToolbar
      appId={appId}
      filterText={filterText}
      limitText={limitText}
      loading={inspectLoading || recordsLoading}
      searchText={searchText}
      selectedViewName={selectedViewName}
      views={selectedObject?.views ?? []}
      onFilterTextChange={setFilterText}
      onLimitTextChange={setLimitText}
      onRefresh={() => {
        void refreshInspect();
        void queryRecords();
        void loadEvents();
      }}
      onRunQuery={() => void queryRecords()}
      onSearchTextChange={setSearchText}
      onViewChange={setSelectedViewName}
    />
  );

  if (!client) {
    return (
      <DataExplorerLayout
        title="Data"
        toolbar={toolbar}
        objectList={<EmptyPanel message="Dashboard RPC is not connected yet." />}
        table={<EmptyPanel message="Waiting for dashboard RPC connection." />}
      />
    );
  }

  return (
    <DataExplorerLayout
      title="Data"
      toolbar={toolbar}
      objectList={(
        <ObjectList
          data={inspect}
          selectedTenantKey={selectedTenantKey}
          selectedObjectName={selectedObjectName}
          onTenantChange={(tenant) => {
            setSelectedTenantKey(tenant);
            const firstObject = inspect?.objects.find((object) => object.tenant_key === tenant)?.name ?? "";
            setSelectedObjectName(firstObject);
            setSelectedViewName(inspect?.objects.find((object) => object.tenant_key === tenant && object.name === firstObject)?.views?.[0]?.name ?? "");
          }}
          onObjectChange={(object) => {
            setSelectedObjectName(object);
            setSelectedViewName(inspect?.objects.find((item) => item.tenant_key === selectedTenantKey && item.name === object)?.views?.[0]?.name ?? "");
          }}
        />
      )}
      table={inspectError ? (
        <EmptyPanel message={inspectError} tone="error" />
      ) : (
        <RecordTable object={selectedObject} records={records} loading={recordsLoading} error={recordError} />
      )}
      inspector={<ObjectInspector data={inspect} object={selectedObject} />}
      eventStream={<OutboxEventTail events={events} summary={inspect?.outbox ?? null} loading={eventsLoading} />}
    />
  );
}

function DataToolbar({
  appId,
  filterText,
  limitText,
  loading,
  searchText,
  selectedViewName,
  views,
  onFilterTextChange,
  onLimitTextChange,
  onRefresh,
  onRunQuery,
  onSearchTextChange,
  onViewChange,
}: {
  appId: string;
  filterText: string;
  limitText: string;
  loading: boolean;
  searchText: string;
  selectedViewName: string;
  views: Array<{ name: string; type: string }>;
  onFilterTextChange: (value: string) => void;
  onLimitTextChange: (value: string) => void;
  onRefresh: () => void;
  onRunQuery: () => void;
  onSearchTextChange: (value: string) => void;
  onViewChange: (value: string) => void;
}) {
  return (
    <PageToolbar
      secondaryActions={[{ label: "Refresh", onClick: onRefresh, disabled: loading }]}
      primaryAction={{ label: loading ? "Running" : "Run", onClick: onRunQuery, disabled: loading }}
    >
      <div className="grid min-w-80 grid-cols-[80px_140px_160px_minmax(180px,1fr)] gap-2">
        <Input
          aria-label="Record limit"
          value={limitText}
          inputMode="numeric"
          onChange={(event) => onLimitTextChange(event.target.value)}
        />
        <Select aria-label="Saved view" value={selectedViewName} onChange={(event) => onViewChange(event.target.value)}>
          <option value="">No saved view</option>
          {views.map((view) => (
            <option key={view.name} value={view.name}>
              {view.name}
            </option>
          ))}
        </Select>
        <Input
          aria-label="Search records"
          value={searchText}
          placeholder="Search"
          onChange={(event) => onSearchTextChange(event.target.value)}
        />
        <Textarea
          aria-label={`Filter for ${appId}`}
          value={filterText}
          placeholder='{"op":"eq","field":"stage","value":"won"}'
          className="min-h-9 py-2"
          onChange={(event) => onFilterTextChange(event.target.value)}
        />
      </div>
    </PageToolbar>
  );
}

function EmptyPanel({ message, tone = "muted" }: { message: string; tone?: "muted" | "error" }) {
  return (
    <div className="flex h-full items-center justify-center p-6">
      <Card>
        <CardContent>
          <p className={tone === "error" ? "text-sm text-red-400" : "text-sm text-muted-foreground"}>{message}</p>
        </CardContent>
      </Card>
    </div>
  );
}
