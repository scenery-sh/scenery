import {
  borderVars,
  colorVars,
  spacingVars,
} from "@astryxdesign/core/theme/tokens.stylex";
import * as stylex from "@stylexjs/stylex";
import type { ComponentType, CSSProperties, ReactNode } from "react";
import { PageHeader, PageHeaderRow } from "./PageLayout.js";
import type { Problem, RequestState } from "./request-state.js";

export type SplitPageProblem = Problem;
export type SplitPageState<Data> = RequestState<{ readonly data: Data }>;

/**
 * Generated split-page slots receive the raw request state so each app-owned
 * surface can choose its own loading, error, empty, and ready presentation.
 * Use QueryState from @scenery/ui to render those branches consistently.
 */
export interface SplitPageSlotProps<Data> {
  readonly state: SplitPageState<Data>;
  readonly selection: string | null;
  readonly onSelectionChange: (selection: string | null) => void;
}

export interface SplitPageSlots<Data> {
  readonly sidebar: ComponentType<SplitPageSlotProps<Data>>;
  readonly detail: ComponentType<SplitPageSlotProps<Data>>;
  readonly sidebarActions?: ComponentType<SplitPageSlotProps<Data>>;
  readonly detailHeader?: ComponentType<SplitPageSlotProps<Data>>;
}

type Exact<Shape, Actual extends Shape> = Actual &
  Record<Exclude<keyof Actual, keyof Shape>, never>;

export function defineSplitPageSlots<Data>() {
  return <Actual extends SplitPageSlots<Data>>(
    slots: Exact<SplitPageSlots<Data>, Actual>,
  ): Actual => slots;
}

type SplitPageContentProps = {
  sidebarActions?: ReactNode;
  sidebar: ReactNode;
  detailHeader?: ReactNode;
  detail: ReactNode;
  sidebarWidth?: string;
};

type SplitPageProps =
  | (SplitPageContentProps & {
      sidebarTitle: string;
      ariaLabel?: string;
      sidebarLabel?: string;
    })
  | (SplitPageContentProps & {
      sidebarTitle: Exclude<ReactNode, string>;
      ariaLabel: string;
      sidebarLabel: string;
    });

export function SplitPage({
  ariaLabel,
  sidebarLabel,
  sidebarTitle,
  sidebarActions,
  sidebar,
  detailHeader,
  detail,
  sidebarWidth,
}: SplitPageProps) {
  const defaultLabel =
    typeof sidebarTitle === "string" ? sidebarTitle : undefined;
  const gridStyle = sidebarWidth
    ? ({ "--split-sidebar-width": sidebarWidth } as CSSProperties)
    : undefined;
  return (
    <section
      {...stylex.props(styles.split)}
      aria-label={ariaLabel ?? defaultLabel}
      style={gridStyle}
    >
      <aside
        {...stylex.props(styles.sidebar)}
        aria-label={sidebarLabel ?? defaultLabel}
      >
        <PageHeader title={sidebarTitle} actions={sidebarActions} />
        <div {...stylex.props(styles.sidebarInner)}>{sidebar}</div>
      </aside>
      <article {...stylex.props(styles.detail)}>
        {detailHeader ? (
          <PageHeaderRow>{detailHeader}</PageHeaderRow>
        ) : null}
        {detail}
      </article>
    </section>
  );
}

const styles = stylex.create({
  split: {
    boxSizing: "border-box",
    width: "100%",
    height: "100%",
    display: "grid",
    gridTemplateColumns: {
      default:
        "var(--split-sidebar-width, minmax(20rem, 24rem)) minmax(0, 1fr)",
      "@media (max-width: 760px)": "1fr",
    },
    gridTemplateRows: {
      default: "minmax(0, 1fr)",
      "@media (max-width: 760px)": "minmax(18rem, 45%) minmax(0, 1fr)",
    },
    minHeight: 0,
    minWidth: 0,
    backgroundColor: colorVars["--color-background-surface"],
  },
  sidebar: {
    display: "flex",
    flexDirection: "column",
    minHeight: 0,
    overflow: "hidden",
    borderInlineEndColor: {
      default: colorVars["--color-border"],
      "@media (max-width: 760px)": "transparent",
    },
    borderInlineEndStyle: "solid",
    borderInlineEndWidth: {
      default: borderVars["--border-width"],
      "@media (max-width: 760px)": 0,
    },
    borderBottomColor: {
      default: "transparent",
      "@media (max-width: 760px)": colorVars["--color-border"],
    },
    borderBottomStyle: "solid",
    borderBottomWidth: {
      default: 0,
      "@media (max-width: 760px)": borderVars["--border-width"],
    },
  },
  sidebarInner: {
    boxSizing: "border-box",
    flexGrow: 1,
    minHeight: 0,
    overflow: "auto",
    display: "flex",
    flexDirection: "column",
    gap: spacingVars["--spacing-4"],
    padding: spacingVars["--spacing-4"],
  },
  detail: {
    display: "flex",
    flexDirection: "column",
    minHeight: 0,
    overflow: "hidden",
  },
});
