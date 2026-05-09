import { Card, CardContent, CardHeader, CardTitle } from "@/components/primitives/Card";
import { formatTimestamp } from "@/lib/utils";
import type { DataInspectResponse, DataObjectSummary } from "./dataExplorerClient";

export function ObjectInspector({
  data,
  object,
}: {
  data: DataInspectResponse | null;
  object: DataObjectSummary | null;
}) {
  if (!data) {
    return <EmptyInspector message="Data inspect output has not loaded yet." />;
  }
  if (!object) {
    return <EmptyInspector message="Select an object to inspect its fields and infrastructure state." />;
  }
  return (
    <div className="space-y-3 p-3">
      <Card>
        <CardHeader>
          <CardTitle>Infrastructure</CardTitle>
        </CardHeader>
        <CardContent className="space-y-2 text-sm">
          <Fact label="Schema version" value={String(object.schema_version)} />
          <Fact label="Physical table" value={object.physical_table} />
          <Fact label="Metadata schema" value={data.schemas.metadata} />
          <Fact label="Records schema" value={data.schemas.records} />
          <Fact label="Trigger enabled" value={yesNo(object.outbox_triggers_enabled)} />
          <Fact label="Trigger present" value={yesNo(object.outbox_trigger_present)} />
          {object.outbox_trigger_name ? <Fact label="Trigger name" value={object.outbox_trigger_name} /> : null}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Fields</CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          {object.fields.map((field) => (
            <div key={field.name} className="rounded-md border border-border p-3 text-sm">
              <div className="flex items-center justify-between gap-3">
                <span className="font-medium">{field.label || field.name}</span>
                <code className="text-xs text-muted-foreground">{field.type}</code>
              </div>
              <div className="mt-2 text-xs text-muted-foreground">{field.columns.join(", ") || "no columns"}</div>
              {field.searchable ? (
                <div className="mt-2 text-xs text-muted-foreground">
                  searchable · weight {field.search_weight || "D"}
                </div>
              ) : null}
            </div>
          ))}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Indexes</CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          {object.indexes.map((index) => (
            <div key={index.physical_name} className="rounded-md border border-border p-3 text-sm">
              <div className="font-medium">{index.name}</div>
              <div className="mt-1 text-xs text-muted-foreground">{index.physical_name}</div>
              <div className="mt-2 text-xs">
                {index.method} {index.unique ? "unique" : "index"} · {index.physical.exists ? "present" : "missing"}
              </div>
            </div>
          ))}
          {object.indexes.length === 0 ? <p className="text-sm text-muted-foreground">No indexes yet.</p> : null}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Migrations</CardTitle>
        </CardHeader>
        <CardContent className="space-y-3 text-sm">
          <div className="grid grid-cols-2 gap-2">
            <Fact label="Pending" value={String(data.migrations.pending)} />
            <Fact label="Failed" value={String(data.migrations.failed)} />
          </div>
          {data.migrations.latest.slice(0, 5).map((migration) => (
            <div key={migration.id} className="rounded-md border border-border p-3">
              <div className="flex items-center justify-between gap-3">
                <span className="font-medium">{migration.object || "metadata"}</span>
                <span className="text-xs text-muted-foreground">{migration.status}</span>
              </div>
              <div className="mt-1 text-xs text-muted-foreground">{formatTimestamp(migration.started_at)}</div>
            </div>
          ))}
        </CardContent>
      </Card>
    </div>
  );
}

function EmptyInspector({ message }: { message: string }) {
  return (
    <div className="p-3">
      <Card>
        <CardContent>
          <p className="text-sm text-muted-foreground">{message}</p>
        </CardContent>
      </Card>
    </div>
  );
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="break-all font-mono text-xs">{value}</div>
    </div>
  );
}

function yesNo(value: boolean): string {
  return value ? "yes" : "no";
}
