import type { MiddlewareMeta, ServiceMeta, ServiceRPC } from "./types";
import { renderMetadataPath } from "./utils";

export interface CatalogEndpointSummary {
  key: string;
  serviceName: string;
  rpcName: string;
  accessType: string;
  proto: string;
  path: string;
  methods: string[];
  requestSchema?: unknown;
  responseSchema?: unknown;
}

export interface CatalogServiceSelection {
  selectedService: ServiceMeta | null;
  selectedEndpoint: ServiceRPC | null;
}

export function resolveCatalogSelection(
  services: ServiceMeta[],
  serviceSlug?: string,
  rpcSlug?: string,
): CatalogServiceSelection {
  if (services.length === 0) {
    return { selectedService: null, selectedEndpoint: null };
  }

  const selectedService =
    services.find((service) => service.name === serviceSlug) ||
    services[0] ||
    null;
  if (!selectedService) {
    return { selectedService: null, selectedEndpoint: null };
  }

  const selectedEndpoint =
    (rpcSlug
      ? selectedService.rpcs.find((rpc) => rpc.name === rpcSlug) || null
      : null) ||
    selectedService.rpcs[0] ||
    null;

  return { selectedService, selectedEndpoint };
}

export function summarizeEndpoint(service: ServiceMeta, rpc: ServiceRPC): CatalogEndpointSummary {
  return {
    key: `${service.name}.${rpc.name}`,
    serviceName: service.name,
    rpcName: rpc.name,
    accessType: rpc.access_type,
    proto: rpc.proto,
    path: renderMetadataPath(rpc.path),
    methods: rpc.http_methods ?? [],
    requestSchema: rpc.request_schema,
    responseSchema: rpc.response_schema,
  };
}

export function filterCatalogServices(services: ServiceMeta[], search: string): ServiceMeta[] {
  const needle = search.trim().toLowerCase();
  if (!needle) {
    return services;
  }
  return services
    .map((service) => ({
      ...service,
      rpcs: service.rpcs.filter((rpc) =>
        `${service.name} ${rpc.name} ${rpc.access_type} ${rpc.http_methods.join(" ")}`
          .toLowerCase()
          .includes(needle),
      ),
    }))
    .filter((service) => service.name.toLowerCase().includes(needle) || service.rpcs.length > 0);
}

export function middlewareForService(
  middleware: MiddlewareMeta[],
  serviceName?: string,
): MiddlewareMeta[] {
  return middleware.filter((item) => item.global || item.service_name === serviceName);
}
