import {
  Layout,
  LayoutContent,
  LayoutHeader,
  LayoutPanel,
} from "@astryxdesign/core/Layout";
import * as stylex from "@stylexjs/stylex";
import {
  type ComponentType,
  type ReactNode,
  useSyncExternalStore,
} from "react";
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
  const compact = useCompactSplitPage();
  const defaultLabel =
    typeof sidebarTitle === "string" ? sidebarTitle : undefined;
  const sidebarLayout = (
    <Layout
      content={<LayoutContent padding={4}>{sidebar}</LayoutContent>}
      header={
        <LayoutHeader padding={0}>
          <PageHeader title={sidebarTitle} actions={sidebarActions} />
        </LayoutHeader>
      }
      height="fill"
      padding={0}
    />
  );
  const detailLayout = (
    <Layout
      content={<LayoutContent padding={0}>{detail}</LayoutContent>}
      header={
        detailHeader ? (
          <LayoutHeader padding={0}>
            <PageHeaderRow>{detailHeader}</PageHeaderRow>
          </LayoutHeader>
        ) : undefined
      }
      height="fill"
      padding={0}
    />
  );

  return (
    <section
      aria-label={ariaLabel ?? defaultLabel}
      {...stylex.props(styles.root)}
    >
      {compact ? (
        <Layout
          content={
            <LayoutContent isScrollable={false} padding={0}>
              <div {...stylex.props(styles.compact)}>
                <aside aria-label={sidebarLabel ?? defaultLabel}>
                  {sidebarLayout}
                </aside>
                {detailLayout}
              </div>
            </LayoutContent>
          }
          height="fill"
          padding={0}
        />
      ) : (
        <Layout
          content={
            <LayoutContent isScrollable={false} padding={0}>
              {detailLayout}
            </LayoutContent>
          }
          height="fill"
          padding={0}
          start={
            <LayoutPanel
              hasDivider
              isScrollable={false}
              label={sidebarLabel ?? defaultLabel}
              padding={0}
              role="complementary"
              width={sidebarWidth ?? "24rem"}
            >
              {sidebarLayout}
            </LayoutPanel>
          }
        />
      )}
    </section>
  );
}

const compactSplitPageQuery = "(max-width: 760px)";

function subscribeToCompactSplitPage(onChange: () => void) {
  const media = window.matchMedia(compactSplitPageQuery);
  media.addEventListener("change", onChange);
  return () => media.removeEventListener("change", onChange);
}

function useCompactSplitPage() {
  return useSyncExternalStore(
    subscribeToCompactSplitPage,
    () => window.matchMedia(compactSplitPageQuery).matches,
    () => false,
  );
}

const styles = stylex.create({
  root: {
    boxSizing: "border-box",
    width: "100%",
    height: "100%",
    minHeight: 0,
    minWidth: 0,
  },
  compact: {
    height: "100%",
    minHeight: 0,
    display: "grid",
    gridTemplateRows: "minmax(18rem, 45%) minmax(0, 1fr)",
  },
});
