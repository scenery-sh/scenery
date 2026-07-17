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
