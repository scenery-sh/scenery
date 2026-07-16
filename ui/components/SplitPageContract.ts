import type { ComponentType } from "react";

export interface SplitPageProblem {
  readonly code: string;
  readonly message: string;
  readonly path?: string;
}

export type SplitPageState<Data> =
  | { readonly kind: "loading" }
  | { readonly kind: "result"; readonly data: Data }
  | {
      readonly kind: "error";
      readonly name: string;
      readonly problem: SplitPageProblem;
    };

export interface SplitPageSlotProps<Data> {
  readonly state: SplitPageState<Data>;
  readonly selection: string;
  readonly onSelectionChange: (selection: string) => void;
}

export interface SplitPageSlots<Data> {
  readonly pane: ComponentType<SplitPageSlotProps<Data>>;
  readonly detail: ComponentType<SplitPageSlotProps<Data>>;
  readonly paneActions?: ComponentType<SplitPageSlotProps<Data>>;
  readonly detailHeader?: ComponentType<SplitPageSlotProps<Data>>;
}

type Exact<Shape, Actual extends Shape> = Actual &
  Record<Exclude<keyof Actual, keyof Shape>, never>;

export function defineSplitPageSlots<Data>() {
  return <Actual extends SplitPageSlots<Data>>(
    slots: Exact<SplitPageSlots<Data>, Actual>,
  ): Actual => slots;
}
