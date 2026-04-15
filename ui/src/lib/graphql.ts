import type { StoredRequest, StoredRequestInput } from "./types";

class DashboardGraphQLError extends Error {}

interface GraphQLResponse<T> {
  data?: T;
  errors?: Array<{ message: string }>;
}

async function postGraphQL<T>(body: {
  operationName: string;
  query: string;
  variables: Record<string, unknown>;
}): Promise<T> {
  const response = await fetch("/__graphql", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(body),
  });
  const payload = (await response.json()) as GraphQLResponse<T>;
  if (payload.errors?.length) {
    throw new DashboardGraphQLError(payload.errors[0]?.message || "GraphQL request failed");
  }
  if (!payload.data) {
    throw new DashboardGraphQLError("missing GraphQL data");
  }
  return payload.data;
}

export async function fetchStoredRequests(appSlug: string): Promise<StoredRequest[]> {
  const data = await postGraphQL<{ app: { storedRequests: StoredRequest[] } }>({
    operationName: "getStoredRequests",
    query: `
      query getStoredRequests($appSlug: String!) {
        app(slug: $appSlug) {
          storedRequests {
            id
            title
            rpcName
            svcName
            shared
            data {
              method
              pathParams
              payload
            }
          }
        }
      }
    `,
    variables: { appSlug },
  });
  return data.app.storedRequests ?? [];
}

export async function createStoredRequest(appSlug: string, input: StoredRequestInput): Promise<string> {
  const data = await postGraphQL<{ app: { createStoredRequest: { id: string } } }>({
    operationName: "createStoredRequest",
    query: `
      mutation createStoredRequest($appSlug: String!, $input: CreateStoredRequestInput!) {
        app(slug: $appSlug) {
          createStoredRequest(input: $input) {
            id
          }
        }
      }
    `,
    variables: { appSlug, input },
  });
  return data.app.createStoredRequest.id;
}

export async function updateStoredRequest(
  appSlug: string,
  storedRequestID: string,
  input: StoredRequestInput,
): Promise<string> {
  const data = await postGraphQL<{ app: { storedRequest: { update: { id: string } } } }>({
    operationName: "updateStoredRequest",
    query: `
      mutation updateStoredRequest(
        $appSlug: String!,
        $storedRequestID: String!,
        $input: UpdateStoredRequestInput!
      ) {
        app(slug: $appSlug) {
          storedRequest(id: $storedRequestID) {
            update(input: $input) {
              id
            }
          }
        }
      }
    `,
    variables: { appSlug, storedRequestID, input },
  });
  return data.app.storedRequest.update.id;
}

export async function deleteStoredRequest(appSlug: string, storedRequestID: string): Promise<boolean> {
  const data = await postGraphQL<{ app: { storedRequest: { delete: boolean } } }>({
    operationName: "deleteStoredRequest",
    query: `
      mutation deleteStoredRequest($appSlug: String!, $storedRequestID: String!) {
        app(slug: $appSlug) {
          storedRequest(id: $storedRequestID) {
            delete
          }
        }
      }
    `,
    variables: { appSlug, storedRequestID },
  });
  return data.app.storedRequest.delete;
}

export { DashboardGraphQLError };
