import { render, screen, waitFor } from "@testing-library/react";
import { DataExplorerView } from "./DataExplorerPage";
import type { DataExplorerClient, DataInspectResponse } from "./dataExplorerClient";

const inspectFixture: DataInspectResponse = {
  schema_version: "onlava.inspect.data.v1",
  schemas: { metadata: "onlava_data", records: "onlava_data_records" },
  tenants: [{
    id: "tenant-1",
    key: "acme",
    name: "Acme",
    objects: 1,
    latest_outbox_seq: 7,
  }],
  objects: [{
    id: "object-1",
    tenant_id: "tenant-1",
    tenant_key: "acme",
    name: "company",
    physical_table: "company__abcd",
    schema_version: 3,
    outbox_triggers_enabled: true,
    outbox_trigger_name: "outbox__abcd",
    outbox_trigger_present: true,
    fields: [
      { name: "name", label: "Name", type: "text", columns: ["name"], searchable: true, search_weight: "A" },
      { name: "stage", label: "Stage", type: "select", columns: ["stage"], searchable: false },
    ],
    indexes: [{
      name: "company_stage",
      physical_name: "company_stage_idx",
      method: "btree",
      unique: false,
      fields: [{ name: "stage" }],
      physical: { exists: true, drift: false },
    }],
  }],
  migrations: {
    pending: 0,
    failed: 0,
    latest: [{
      id: "migration-1",
      tenant_id: "tenant-1",
      tenant_key: "acme",
      object_id: "object-1",
      object: "company",
      from_version: 2,
      to_version: 3,
      status: "applied",
      started_at: "2026-05-09T12:00:00Z",
    }],
  },
  outbox: { latest_seq: 7, unpublished: 0 },
};

describe("DataExplorerView", () => {
  it("renders inspect data, records, and layout markers", async () => {
    const client: DataExplorerClient = {
      inspect: async () => inspectFixture,
      queryRecords: async () => ({
        records: [{
          id: "record-1",
          created_at: "2026-05-09T12:01:00Z",
          updated_at: "2026-05-09T12:01:00Z",
          name: "Acme",
          stage: "won",
        }],
      }),
      outboxEvents: async () => [{
        seq: 7,
        event_id: "event-1",
        tenant_id: "tenant-1",
        tenant_key: "acme",
        object: "company",
        record_id: "record-1",
        action: "created",
        schema_version: 3,
        after: { name: "Acme" },
        created_at: "2026-05-09T12:01:00Z",
      }],
    };

    const { container } = render(<DataExplorerView appId="app-test" client={client} />);

    expect(container.querySelector('[data-onlava-ui="DataExplorerLayout"]')).not.toBeNull();
    await waitFor(() => expect(screen.getAllByText("company").length).toBeGreaterThan(0));
    await waitFor(() => expect(screen.getAllByText("Acme").length).toBeGreaterThan(0));
    expect(screen.getAllByText("company__abcd").length).toBeGreaterThan(0);
    expect(screen.getByText("company_stage")).toBeTruthy();
    expect(screen.getByText("#7")).toBeTruthy();
  });
});
