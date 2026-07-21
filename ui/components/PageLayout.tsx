import { Heading } from "@astryxdesign/core/Heading";
import { IconButton } from "@astryxdesign/core/IconButton";
import {
  borderVars,
  colorVars,
  spacingVars,
  typeScaleVars,
} from "@astryxdesign/core/theme/tokens.stylex";
import * as stylex from "@stylexjs/stylex";
import {
  type ComponentType,
  type CSSProperties,
  createContext,
  type ReactNode,
  useContext,
} from "react";
import type { Problem, RequestState } from "./request-state.js";

export type ContentPageProblem = Problem;
export type ContentPageState<Data> = RequestState<{ readonly data: Data }>;

export interface ContentPageSlotProps<Data> {
  readonly state: ContentPageState<Data>;
}

export interface ContentPageSlots<Data> {
  readonly content: ComponentType<ContentPageSlotProps<Data>>;
  readonly actions?: ComponentType<ContentPageSlotProps<Data>>;
}

type Exact<Shape, Actual extends Shape> = Actual &
  Record<Exclude<keyof Actual, keyof Shape>, never>;

export function defineContentPageSlots<Data>() {
  return <Actual extends ContentPageSlots<Data>>(
    slots: Exact<ContentPageSlots<Data>, Actual>,
  ): Actual => slots;
}

export type PageNavigation = {
  icon: ReactNode;
  isCollapsed: boolean;
  onToggle: () => void;
  shortcut?: string;
};

const PageNavigationContext = createContext<PageNavigation | null>(null);

export function PageLayoutProvider({
  navigation,
  children,
}: {
  navigation?: PageNavigation;
  children: ReactNode;
}) {
  return (
    <PageNavigationContext.Provider value={navigation ?? null}>
      {children}
    </PageNavigationContext.Provider>
  );
}

export function PageNavigationToggle() {
  const navigation = useContext(PageNavigationContext);
  if (!navigation) return null;

  const action = navigation.isCollapsed ? "Expand" : "Collapse";
  const shortcut = navigation.shortcut ?? "⌘B";
  return (
    <IconButton
      icon={navigation.icon}
      label={`${action} side nav`}
      onClick={navigation.onToggle}
      tooltip={`${action} side nav (${shortcut})`}
      variant="ghost"
      xstyle={styles.navToggle}
    />
  );
}

export function PageHeader({
  title,
  actions,
}: {
  title: ReactNode;
  actions?: ReactNode;
}) {
  const navigation = useContext(PageNavigationContext);
  return (
    <PageHeaderRow as="header" justify="between">
      <div {...stylex.props(styles.lead)}>
        {navigation ? (
          <>
            <PageNavigationToggle />
            <span {...stylex.props(styles.divider)} />
          </>
        ) : null}
        <Heading accessibilityLevel={1} level={4} xstyle={styles.title}>
          {title}
        </Heading>
      </div>
      {actions ? <div {...stylex.props(styles.actions)}>{actions}</div> : null}
    </PageHeaderRow>
  );
}

export function PageHeaderRow({
  as = "div",
  children,
  justify = "start",
}: {
  as?: "div" | "header";
  children: ReactNode;
  justify?: "between" | "start";
}) {
  const props = stylex.props(
    styles.header,
    justify === "between" && styles.headerBetween,
  );
  return as === "header" ? (
    <header {...props}>{children}</header>
  ) : (
    <div {...props}>{children}</div>
  );
}

export function PageShell({
  title,
  actions,
  label,
  scroll = false,
  children,
}: {
  title: string;
  actions?: ReactNode;
  label?: string;
  scroll?: boolean;
  children: ReactNode;
}) {
  return (
    <main {...stylex.props(styles.page)} aria-label={label}>
      <PageHeader title={title} actions={actions} />
      {scroll ? (
        <div {...stylex.props(styles.scrollArea)}>{children}</div>
      ) : (
        children
      )}
    </main>
  );
}

export function Page({
  title,
  actions,
  children,
  maxWidth,
  ariaLabel,
  fill,
}: {
  title: string;
  actions?: ReactNode;
  children: ReactNode;
  maxWidth?: number;
  ariaLabel?: string;
  fill?: boolean;
}) {
  const contentStyle = maxWidth
    ? ({ "--page-content-max": `${maxWidth}px` } as CSSProperties)
    : undefined;
  return (
    <PageShell title={title} actions={actions} label={ariaLabel}>
      <div {...stylex.props(styles.scrollArea, fill && styles.scrollAreaFill)}>
        <div
          {...stylex.props(styles.content, fill && styles.contentFill)}
          style={contentStyle}
        >
          {children}
        </div>
      </div>
    </PageShell>
  );
}

const styles = stylex.create({
  header: {
    boxSizing: "border-box",
    width: "100%",
    height: spacingVars["--spacing-12"],
    flexShrink: 0,
    minWidth: 0,
    overflow: "hidden",
    display: "flex",
    alignItems: "center",
    gap: spacingVars["--spacing-2"],
    paddingInline: spacingVars["--spacing-4"],
    borderBottomColor: colorVars["--color-border"],
    borderBottomStyle: "solid",
    borderBottomWidth: borderVars["--border-width"],
    backgroundColor: colorVars["--color-background-surface"],
  },
  headerBetween: { justifyContent: "space-between" },
  lead: {
    display: "flex",
    alignItems: "center",
    gap: spacingVars["--spacing-3"],
    minWidth: 0,
  },
  title: { fontWeight: typeScaleVars["--text-label-weight"] },
  navToggle: { color: colorVars["--color-text-secondary"] },
  divider: {
    flexShrink: 0,
    width: borderVars["--border-width"],
    height: spacingVars["--spacing-5"],
    backgroundColor: colorVars["--color-border"],
  },
  actions: {
    flexShrink: 0,
    display: "flex",
    alignItems: "center",
    gap: spacingVars["--spacing-2"],
  },
  page: {
    height: "100%",
    minHeight: 0,
    display: "flex",
    flexDirection: "column",
    backgroundColor: colorVars["--color-background-surface"],
  },
  // A size container so descendants (e.g. QueryTable's sticky detail panel)
  // can cap their height in cqh units — the scrollport's real height —
  // instead of guessing how much app chrome sits above it. Safe here: the
  // scroll area's size comes from flex, never from its content.
  scrollArea: {
    minHeight: 0,
    flex: 1,
    overflow: "auto",
    containerType: "size",
  },
  content: {
    boxSizing: "border-box",
    width: "min(var(--page-content-max, 1540px), 100%)",
    marginInline: "auto",
    padding: spacingVars["--spacing-4"],
    display: "flex",
    flexDirection: "column",
    gap: spacingVars["--spacing-4"],
  },
  // Fill mode: the page itself never scrolls — everything above the grid
  // stays put and a flex-fill descendant (QueryTable's grid, its detail
  // panel) owns its own scrolling.
  scrollAreaFill: {
    overflow: "hidden",
    display: "flex",
    flexDirection: "column",
  },
  contentFill: {
    flex: 1,
    minHeight: 0,
  },
});
