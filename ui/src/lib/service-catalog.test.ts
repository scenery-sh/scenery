import type { ServiceMeta } from "./types";
import {
  filterCatalogServices,
  middlewareForService,
  resolveCatalogSelection,
  summarizeEndpoint,
} from "./service-catalog";

const services: ServiceMeta[] = [
  {
    name: "tenants",
    rel_path: "tenants",
    rpcs: [
      {
        name: "Config",
        access_type: "public",
        proto: "regular",
        path: { type: "URL", segments: [{ type: "LITERAL", value: "tenants", value_type: "STRING" }] },
        http_methods: ["GET"],
      },
      {
        name: "Update",
        access_type: "auth",
        proto: "regular",
        path: { type: "URL", segments: [{ type: "LITERAL", value: "tenants", value_type: "STRING" }] },
        http_methods: ["POST"],
      },
    ],
  },
  {
    name: "auth",
    rel_path: "auth",
    rpcs: [],
  },
];

describe("service catalog helpers", () => {
  it("resolves selection from route params", () => {
    const selection = resolveCatalogSelection(services, "tenants", "Update");
    expect(selection.selectedService?.name).toBe("tenants");
    expect(selection.selectedEndpoint?.name).toBe("Update");
  });

  it("falls back to the first service and endpoint", () => {
    const selection = resolveCatalogSelection(services);
    expect(selection.selectedService?.name).toBe("tenants");
    expect(selection.selectedEndpoint?.name).toBe("Config");
  });

  it("filters services and rpc names", () => {
    const filtered = filterCatalogServices(services, "update");
    expect(filtered).toHaveLength(1);
    expect(filtered[0]?.rpcs).toHaveLength(1);
    expect(filtered[0]?.rpcs[0]?.name).toBe("Update");
  });

  it("summarizes an endpoint for the caller UI", () => {
    const summary = summarizeEndpoint(services[0]!, services[0]!.rpcs[0]!);
    expect(summary.key).toBe("tenants.Config");
    expect(summary.path).toBe("/tenants");
  });

  it("filters middleware by service and globals", () => {
    const items = middlewareForService(
      [
        { name: { pkg: "a", name: "Global" }, global: true },
        { name: { pkg: "b", name: "TenantOnly" }, global: false, service_name: "tenants" },
        { name: { pkg: "c", name: "Other" }, global: false, service_name: "auth" },
      ],
      "tenants",
    );
    expect(items.map((item) => item.name.name)).toEqual(["Global", "TenantOnly"]);
  });
});
