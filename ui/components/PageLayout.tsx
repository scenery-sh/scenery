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
  title: string;
  actions?: ReactNode;
}) {
  const navigation = useContext(PageNavigationContext);
  const shortcut = navigation?.shortcut ?? "⌘B";
  return (
    <header {...stylex.props(styles.header)}>
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
    </header>
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

export function SplitPage({
  ariaLabel,
  paneLabel,
  paneTitle,
  paneActions,
  pane,
  detailHeader,
  children,
  paneWidth,
}: {
  ariaLabel: string;
  paneLabel: string;
  paneTitle: string;
  paneActions?: ReactNode;
  pane: ReactNode;
  detailHeader?: ReactNode;
  children: ReactNode;
  paneWidth?: string;
}) {
  const gridStyle = paneWidth
    ? ({ "--split-pane-width": paneWidth } as CSSProperties)
    : undefined;
  return (
    <section
      {...stylex.props(styles.split)}
      aria-label={ariaLabel}
      style={gridStyle}
    >
      <aside {...stylex.props(styles.pane)} aria-label={paneLabel}>
        <PageHeader title={paneTitle} actions={paneActions} />
        <div {...stylex.props(styles.paneInner)}>{pane}</div>
      </aside>
      <article {...stylex.props(styles.detail)}>
        {detailHeader ? (
          <div {...stylex.props(styles.detailHeader)}>{detailHeader}</div>
        ) : null}
        {children}
      </article>
    </section>
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
    justifyContent: "space-between",
    gap: spacingVars["--spacing-2"],
    paddingInline: spacingVars["--spacing-4"],
    borderBottomColor: colorVars["--color-border"],
    borderBottomStyle: "solid",
    borderBottomWidth: borderVars["--border-width"],
    backgroundColor: colorVars["--color-background-surface"],
  },
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
  split: {
    position: "absolute",
    inset: 0,
    display: "grid",
    gridTemplateColumns: {
      default: "var(--split-pane-width, minmax(20rem, 24rem)) minmax(0, 1fr)",
      "@media (max-width: 760px)": "1fr",
    },
    gridTemplateRows: {
      default: "minmax(0, 1fr)",
      "@media (max-width: 760px)": "minmax(18rem, 45%) minmax(0, 1fr)",
    },
    minHeight: 0,
    backgroundColor: colorVars["--color-background-surface"],
  },
  pane: {
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
  paneInner: {
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
  detailHeader: {
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
});
