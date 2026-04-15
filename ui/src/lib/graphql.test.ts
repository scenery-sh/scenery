import { createStoredRequest, fetchStoredRequests } from "./graphql";

describe("dashboard graphql client", () => {
  it("loads stored requests", async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      json: () =>
        Promise.resolve({
          data: {
            app: {
              storedRequests: [
                {
                  id: "req-1",
                  title: "Config",
                  rpcName: "Config",
                  svcName: "tenants",
                  shared: true,
                  data: {
                    method: "GET",
                    pathParams: {},
                    payload: {},
                  },
                },
              ],
            },
          },
        }),
    } as Response);

    const requests = await fetchStoredRequests("app-test");
    expect(requests).toHaveLength(1);
    expect(requests[0]?.title).toBe("Config");
  });

  it("sends create mutations", async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      json: () =>
        Promise.resolve({
          data: {
            app: {
              createStoredRequest: {
                id: "stored-1",
              },
            },
          },
        }),
    } as Response);

    const id = await createStoredRequest("app-test", {
      title: "Trace replay",
      rpcName: "Config",
      svcName: "tenants",
      shared: false,
      data: {
        method: "GET",
        pathParams: {},
        payload: {},
      },
    });

    expect(id).toBe("stored-1");
    expect(globalThis.fetch).toHaveBeenCalledTimes(1);
  });
});
