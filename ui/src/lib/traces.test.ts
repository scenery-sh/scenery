import { buildTraceModel, normalizeSpanID, normalizeTraceID, type TraceCompatEvent } from "./traces";
import type { TraceSummary } from "./types";

describe("trace helpers", () => {
  it("normalizes compat trace ids back to hex", () => {
    expect(normalizeTraceID({ high: "1", low: "2" })).toBe("00000000000000010000000000000002");
  });

  it("normalizes hex span ids to compat decimal ids", () => {
    expect(normalizeSpanID("0000000000000003")).toBe("3");
  });

  it("builds a span tree from summaries and compat events", () => {
    const summaries: TraceSummary[] = [
      {
        trace_id: "00000000000000010000000000000002",
        span_id: "0000000000000003",
        type: "REQUEST",
        is_root: true,
        is_error: false,
        started_at: "2026-04-15T10:00:00.000Z",
        duration_nanos: 5_000_000,
        service_name: "tenants",
        endpoint_name: "Config",
      },
      {
        trace_id: "00000000000000010000000000000002",
        span_id: "0000000000000004",
        type: "DB",
        is_root: false,
        is_error: false,
        started_at: "2026-04-15T10:00:00.001Z",
        duration_nanos: 1_000_000,
        service_name: "tenants",
        endpoint_name: "SELECT",
        parent_span_id: "0000000000000003",
      },
    ];

    const events: TraceCompatEvent[] = [
      {
        trace_id: { high: "1", low: "2" },
        span_id: "3",
        event_id: "1",
        event_time: "2026-04-15T10:00:00.000Z",
        span_start: {
          request: {
            service_name: "tenants",
            endpoint_name: "Config",
            http_method: "GET",
            path: "/tenants/config",
            uid: "user_123",
          },
        },
      },
      {
        trace_id: { high: "1", low: "2" },
        span_id: "4",
        event_id: "2",
        event_time: "2026-04-15T10:00:00.001Z",
        span_start: {
          db: {
            service_name: "tenants",
            endpoint_name: "Config",
            operation: "SELECT",
            query: "select 1",
            parent_span_id: "3",
          },
        },
      },
      {
        trace_id: { high: "1", low: "2" },
        span_id: "4",
        event_id: "3",
        event_time: "2026-04-15T10:00:00.002Z",
        span_event: {
          http_call_start: {
            method: "GET",
            url: "https://pulse.dev",
          },
        },
      },
      {
        trace_id: { high: "1", low: "2" },
        span_id: "4",
        event_id: "4",
        event_time: "2026-04-15T10:00:00.003Z",
        span_end: {
          duration_nanos: "1000000",
          status_code: "STATUS_CODE_OK",
          db: {
            operation: "SELECT",
          },
        },
      },
      {
        trace_id: { high: "1", low: "2" },
        span_id: "3",
        event_id: "5",
        event_time: "2026-04-15T10:00:00.005Z",
        span_end: {
          duration_nanos: "5000000",
          status_code: "STATUS_CODE_OK",
          request: {
            service_name: "tenants",
            endpoint_name: "Config",
            http_status_code: 200,
            uid: "user_123",
          },
        },
      },
    ];

    const model = buildTraceModel("00000000000000010000000000000002", summaries, events);

    expect(model.rootSpan?.title).toBe("tenants.Config");
    expect(model.userID).toBe("user_123");
    expect(model.spans).toHaveLength(2);
    expect(model.spans[0]?.depth).toBe(0);
    expect(model.spans[1]?.depth).toBe(1);
    expect(model.spans[1]?.title).toBe("DB SELECT");
    expect(model.spans[1]?.parentRawID).toBe("0000000000000003");
    expect(model.spans[1]?.events[0]?.kind).toBe("http_call_start");
  });
});
