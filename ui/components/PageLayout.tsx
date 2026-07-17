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
  type CSSProperties,
  createContext,
  type ReactNode,
  useContext,
} from "react";

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

export function PageHeader({
  title,
  actions,
}: {
  title: ReactNode;
  actions?: ReactNode;
}) {
  const navigation = useContext(PageNavigationContext);
  const shortcut = navigation?.shortcut ?? "⌘B";
  return (
    <PageHeaderRow as="header" justify="between">
      <div {...stylex.props(styles.lead)}>
        {navigation ? (
          <>
            <IconButton
              icon={navigation.icon}
              label={
                navigation.isCollapsed ? "Expand side nav" : "Collapse side nav"
              }
              onClick={navigation.onToggle}
              tooltip={`${navigation.isCollapsed ? "Expand" : "Collapse"} side nav (${shortcut})`}
              variant="ghost"
              xstyle={styles.navToggle}
            />
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
}: {
  title: string;
  actions?: ReactNode;
  children: ReactNode;
  maxWidth?: number;
}) {
  const contentStyle = maxWidth
    ? ({ "--page-content-max": `${maxWidth}px` } as CSSProperties)
    : undefined;
  return (
    <PageShell title={title} actions={actions}>
      <div {...stylex.props(styles.scrollArea)}>
        <div {...stylex.props(styles.content)} style={contentStyle}>
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
  scrollArea: { minHeight: 0, flex: 1, overflow: "auto" },
  content: {
    boxSizing: "border-box",
    width: "min(var(--page-content-max, 1540px), 100%)",
    marginInline: "auto",
    padding: spacingVars["--spacing-4"],
    display: "flex",
    flexDirection: "column",
    gap: spacingVars["--spacing-4"],
  },
});
