import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { useEffect, useMemo, useState } from "react";
import { JSONView } from "../components/json-view";
import { useDashboard } from "../lib/dashboard-context";
import {
  filterCatalogServices,
  middlewareForService,
  resolveCatalogSelection,
  summarizeEndpoint,
} from "../lib/service-catalog";
import { requestTracesURL } from "../lib/grafana";
import { tryParseJSON } from "../lib/utils";
import type { ApiCallResponse } from "../lib/types";

export function ServicesPage() {
  const navigate = useNavigate();
  const { serviceSlug, rpcSlug } = useParams({ strict: false });
  const { appId, callAPI, meta, status } = useDashboard();
  const [search, setSearch] = useState("");
  const [response, setResponse] = useState<ApiCallResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [requestPath, setRequestPath] = useState("");
  const [requestMethod, setRequestMethod] = useState("GET");
  const [requestPayload, setRequestPayload] = useState("{}");
  const [authToken, setAuthToken] = useState("");

  const services = meta?.svcs ?? [];
  const traceURL = requestTracesURL(status?.grafana);
  const visibleServices = useMemo(() => filterCatalogServices(services, search), [search, services]);
  const { selectedService, selectedEndpoint } = useMemo(
    () => resolveCatalogSelection(services, serviceSlug, rpcSlug),
    [rpcSlug, serviceSlug, services],
  );

  const endpointSummary = useMemo(
    () => (selectedService && selectedEndpoint ? summarizeEndpoint(selectedService, selectedEndpoint) : null),
    [selectedEndpoint, selectedService],
  );

  const serviceMiddleware = useMemo(
    () => middlewareForService(meta?.middleware ?? [], selectedService?.name),
    [meta?.middleware, selectedService?.name],
  );

  useEffect(() => {
    if (!selectedService || serviceSlug) {
      return;
    }
    void navigate({
      to: "/$appId/envs/local/api/$serviceSlug",
      params: { appId, serviceSlug: selectedService.name },
      replace: true,
    });
  }, [appId, navigate, selectedService, serviceSlug]);

  useEffect(() => {
    if (!endpointSummary) {
      setRequestPath("");
      setRequestMethod("GET");
      setRequestPayload("{}");
      return;
    }
    setRequestPath(endpointSummary.path);
    setRequestMethod(endpointSummary.methods[0] || "GET");
    setRequestPayload("{}");
    setResponse(null);
    setError(null);
  }, [endpointSummary]);

  return (
    <section className="w-full h-[calc(100vh-var(--header-height))] flex overflow-hidden">
      <aside className="w-[320px] shrink-0 overflow-auto border-border border-r bg-sidebar">
        <div className="px-3 pt-4 pb-3 border-b border-border">
          <input
            className="h-8 w-full rounded-md border border-border px-3 text-sm shadow-none"
            placeholder="Search services and endpoints"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
          />
        </div>
        <div className="px-2 py-4">
          {visibleServices.map((service) => {
            const activeService = selectedService?.name === service.name;
            return (
              <div key={service.name} className="mb-3">
                <Link
                  to="/$appId/envs/local/api/$serviceSlug"
                  params={{ appId, serviceSlug: service.name }}
                  className={[
                    "flex items-center rounded-md px-3 py-2 text-sm transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
                    activeService ? "bg-sidebar-accent text-sidebar-accent-foreground" : "",
                  ].join(" ")}
                >
                  <span className="truncate font-medium">{service.name}</span>
                </Link>
                {service.rpcs.length > 0 ? (
                  <div className="mt-1 space-y-1 pl-3">
                    {service.rpcs.map((rpc) => {
                      const activeEndpoint = selectedService?.name === service.name && selectedEndpoint?.name === rpc.name;
                      return (
                        <Link
                          key={`${service.name}.${rpc.name}`}
                          to="/$appId/envs/local/api/$serviceSlug/$rpcSlug"
                          params={{ appId, serviceSlug: service.name, rpcSlug: rpc.name }}
                          className={[
                            "block rounded-md px-3 py-2 text-sm transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
                            activeEndpoint ? "bg-sidebar-accent text-sidebar-accent-foreground" : "text-muted-foreground",
                          ].join(" ")}
                        >
                          <div className="flex items-center justify-between gap-2">
                            <span className="truncate">{rpc.name}</span>
                            <span className="text-[10px] uppercase">{rpc.access_type}</span>
                          </div>
                        </Link>
                      );
                    })}
                  </div>
                ) : null}
              </div>
            );
          })}
          {visibleServices.length === 0 ? (
            <p className="px-3 py-4 text-sm text-muted-foreground">No services match the current search.</p>
          ) : null}
        </div>
      </aside>

      <div className="flex-1 min-w-0 overflow-hidden">
        <header className="flex h-12 shrink-0 items-center gap-2 bg-sidebar border-b border-border">
          <div className="flex items-center gap-2 px-4">
            <div className="text-sm">Service Catalog</div>
          </div>
        </header>

        <div className="overflow-auto h-[calc(100vh-var(--header-height)-48px)]">
          <div className="min-h-0 grow px-8 pt-6 pb-12 leading-6">
            <div className="max-w-6xl space-y-8">
              <div className="grid grid-cols-4 gap-4">
                <StatCard label="Services" value={String(services.length)} />
                <StatCard
                  label="Endpoints"
                  value={String(services.reduce((count, service) => count + service.rpcs.length, 0))}
                />
                <StatCard label="Middleware" value={String(meta?.middleware?.length ?? 0)} />
                <StatCard label="API Address" value={status?.addr || "n/a"} mono />
              </div>

              {selectedService ? (
                <section className="rounded-md border border-border p-6">
                  <div className="flex items-start justify-between gap-4">
                    <div>
                      <h2 className="text-base font-medium">{selectedService.name}</h2>
                      <p className="mt-1 text-sm text-muted-foreground">{selectedService.rel_path}</p>
                    </div>
                    <div className="text-xs text-muted-foreground">
                      {selectedService.rpcs.length} endpoint{selectedService.rpcs.length === 1 ? "" : "s"}
                    </div>
                  </div>
                  {meta?.auth_handler ? (
                    <div className="mt-4 rounded-md border border-border px-4 py-3 text-sm">
                      <div className="font-medium">Auth handler</div>
                      <div className="mt-1 text-muted-foreground">
                        {meta.auth_handler.name} · {meta.auth_handler.pkg_path}
                      </div>
                    </div>
                  ) : null}
                  {serviceMiddleware.length > 0 ? (
                    <div className="mt-4">
                      <div className="text-sm font-medium">Middleware</div>
                      <div className="mt-3 grid grid-cols-2 gap-3">
                        {serviceMiddleware.map((item) => (
                          <div
                            key={`${item.name.pkg}.${item.name.name}`}
                            className="rounded-md border border-border px-4 py-3 text-sm"
                          >
                            <div className="font-medium">{item.name.name}</div>
                            <div className="mt-1 text-muted-foreground">{item.name.pkg}</div>
                            <div className="mt-2 text-xs text-muted-foreground">
                              {item.global ? "Global" : item.service_name || "Scoped"}
                            </div>
                          </div>
                        ))}
                      </div>
                    </div>
                  ) : null}
                </section>
              ) : null}

              {endpointSummary ? (
                <section className="grid grid-cols-[minmax(320px,1fr)_minmax(360px,1.2fr)] gap-6">
                  <div className="space-y-6">
                    <div className="rounded-md border border-border p-6">
                      <div className="flex items-center justify-between gap-4">
                        <div>
                          <h2 className="text-base font-medium">
                            {endpointSummary.serviceName}.{endpointSummary.rpcName}
                          </h2>
                          <p className="mt-1 text-sm text-muted-foreground">{endpointSummary.path}</p>
                        </div>
                        <span className="text-xs text-muted-foreground uppercase">{endpointSummary.accessType}</span>
                      </div>
                      <div className="mt-4 grid grid-cols-3 gap-4">
                        <StatCard label="JSON" value="available" />
                        <StatCard
                          label="Binary"
                          value={endpointSummary.wireAvailable ? "available" : "unavailable"}
                        />
                        <StatCard label="Methods" value={endpointSummary.methods.join(", ") || "GET"} />
                      </div>
                      {!endpointSummary.wireAvailable && endpointSummary.wireReason ? (
                        <p className="mt-3 text-xs text-muted-foreground">
                          Binary unavailable: {endpointSummary.wireReason}
                        </p>
                      ) : null}
                    </div>

                    <JSONView title="Request schema" value={endpointSummary.requestSchema ?? { type: "empty" }} />
                    <JSONView title="Response schema" value={endpointSummary.responseSchema ?? { type: "empty" }} />
                  </div>

                  <div className="rounded-md border border-border p-6">
                    <h2 className="text-base font-medium">Call endpoint</h2>
                    <div className="mt-4 space-y-4">
                      <Field label="Method" value={requestMethod} onChange={setRequestMethod} />
                      <Field label="Path" value={requestPath} onChange={setRequestPath} />
                      <Field label="Auth token" value={authToken} onChange={setAuthToken} />
                      <TextAreaField label="Payload JSON" value={requestPayload} onChange={setRequestPayload} />
                      <button
                        type="button"
                        className="rounded-md px-3 py-2 text-sm h-8 flex items-center gap-2 border border-border transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
                        onClick={() => void callSelectedEndpoint(endpointSummary)}
                      >
                        Send request
                      </button>
                      {error ? (
                        <div className="rounded-md border border-red-500/30 bg-red-500/10 px-4 py-3 text-sm text-red-500">
                          {error}
                        </div>
                      ) : null}
                      {response ? (
                        <div className="space-y-4">
                          <div className="grid grid-cols-3 gap-4">
                            <StatCard label="Status" value={response.status} />
                            <StatCard label="Code" value={String(response.status_code)} />
                            <StatCard label="Trace" value={response.trace_id || "n/a"} mono />
                          </div>
                          {response.trace_id && traceURL ? (
                            <a
                              href={traceURL}
                              target="_blank"
                              rel="noreferrer"
                              className="inline-flex text-sm underline"
                            >
                              Open in Grafana
                            </a>
                          ) : null}
                          <JSONView title="Response body" value={tryParseJSON(response.body)} />
                        </div>
                      ) : null}
                    </div>
                  </div>
                </section>
              ) : selectedService ? (
                <section className="rounded-md border border-border p-6">
                  <h2 className="text-base font-medium">Endpoints</h2>
                  <div className="mt-4 space-y-3">
                    {selectedService.rpcs.map((rpc) => (
                      <Link
                        key={`${selectedService.name}.${rpc.name}`}
                        to="/$appId/envs/local/api/$serviceSlug/$rpcSlug"
                        params={{ appId, serviceSlug: selectedService.name, rpcSlug: rpc.name }}
                        className="block rounded-md border border-border px-4 py-3 text-sm transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
                      >
                        <div className="flex items-center justify-between gap-4">
                          <strong>{rpc.name}</strong>
                          <span className="text-xs text-muted-foreground uppercase">{rpc.access_type}</span>
                        </div>
                        <div className="mt-1 text-muted-foreground">
                          {(rpc.http_methods ?? []).join(", ")} · {summarizeEndpoint(selectedService, rpc).path}
                        </div>
                      </Link>
                    ))}
                  </div>
                </section>
              ) : (
                <div className="rounded-md border border-border p-6 text-sm text-muted-foreground">
                  No services found for this app.
                </div>
              )}
            </div>
          </div>
        </div>
      </div>
    </section>
  );

  async function callSelectedEndpoint(endpointSummary: ReturnType<typeof summarizeEndpoint>) {
    setError(null);
    try {
      const result = await callAPI({
        service: endpointSummary.serviceName,
        endpoint: endpointSummary.rpcName,
        path: requestPath,
        method: requestMethod,
        payload: tryParseJSON(requestPayload),
        authToken,
      });
      setResponse(result);
    } catch (err) {
      setResponse(null);
      setError(err instanceof Error ? err.message : String(err));
    }
  }
}

function StatCard({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div className="rounded-md border border-border p-4">
      <div className="text-xs uppercase tracking-wide text-muted-foreground">{label}</div>
      <div className={mono ? "mt-2 text-sm font-mono" : "mt-2 text-sm"}>{value}</div>
    </div>
  );
}

function Field({
  label,
  value,
  onChange,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
}) {
  return (
    <div className="space-y-2">
      <label className="text-sm font-medium">{label}</label>
      <input
        className="h-10 w-full rounded-md border border-border px-3 text-sm"
        value={value}
        onChange={(event) => onChange(event.target.value)}
      />
    </div>
  );
}

function TextAreaField({
  label,
  value,
  onChange,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
}) {
  return (
    <div className="space-y-2">
      <label className="text-sm font-medium">{label}</label>
      <textarea
        className="w-full rounded-md border border-border px-3 py-2 text-sm"
        style={{ minHeight: 180 }}
        value={value}
        onChange={(event) => onChange(event.target.value)}
      />
    </div>
  );
}
