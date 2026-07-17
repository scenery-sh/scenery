import type { QueryStateProps } from "./QueryState.js";

export interface Problem {
  readonly code: string;
  readonly message: string;
  readonly path?: string;
}

export type RequestState<Result extends object> =
  | { readonly kind: "loading" }
  | ({ readonly kind: "result" } & Result)
  | {
      readonly kind: "error";
      readonly name: string;
      readonly problem: Problem;
    };

export function requestStateFromQuery<Result extends object>(query: {
  readonly data: RequestState<Result> | undefined;
  readonly error: unknown;
}): RequestState<Result> {
  if (query.data) return query.data;
  if (!query.error) return { kind: "loading" };
  if (isCodedError(query.error)) {
    return {
      kind: "error",
      name: query.error.code,
      problem: { code: query.error.code, message: query.error.message },
    };
  }
  return {
    kind: "error",
    name: "unexpected",
    problem: {
      code: "unexpected",
      message:
        query.error instanceof Error
          ? query.error.message
          : "Unexpected error",
    },
  };
}

export function queryStateProps<Result extends object>(
  state: RequestState<Result>,
  resource: string,
): Pick<
  QueryStateProps,
  "error" | "getErrorMessage" | "isPending" | "resource"
> {
  return {
    error: state.kind === "error" ? state.problem : undefined,
    getErrorMessage: problemMessage,
    isPending: state.kind === "loading",
    resource,
  };
}

function problemMessage(error: unknown) {
  return isProblem(error) ? error.message : "Unexpected API error";
}

function isProblem(value: unknown): value is Problem {
  return (
    typeof value === "object" &&
    value !== null &&
    "message" in value &&
    typeof value.message === "string"
  );
}

function isCodedError(
  value: unknown,
): value is { readonly code: string; readonly message: string } {
  return (
    typeof value === "object" &&
    value !== null &&
    "code" in value &&
    typeof value.code === "string" &&
    "message" in value &&
    typeof value.message === "string"
  );
}
