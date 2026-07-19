import { AppShell } from "@astryxdesign/core/AppShell";
import type { LinkComponentType } from "@astryxdesign/core/Link";
import {
  colorVars,
  durationVars,
  radiusVars,
  spacingVars,
} from "@astryxdesign/core/theme/tokens.stylex";
import * as stylex from "@stylexjs/stylex";
import { type ReactNode, useEffect, useState } from "react";
import { PageLayoutProvider } from "./PageLayout.js";
import {
  SideNavigation,
  type SideNavigationSection,
} from "./SideNavigation.js";

export type ClientAppShellProps = {
  children: ReactNode;
  navigation: readonly SideNavigationSection[];
  linkComponent?: LinkComponentType;
  navigationToggleIcon?: ReactNode;
  topBar?: ReactNode;
  beforeContent?: ReactNode;
  afterContent?: ReactNode;
  /**
   * Identity of the content region's current context (for example the active
   * application in a multi-app shell). When it changes, the content pane
   * remounts with a short enter transition, and an empty navigation collapses
   * instead of unmounting so its width animates.
   */
  contentGroup?: string;
};

// ClientAppShell is the router-agnostic frame used by generated app adapters.
// The generated layer owns route selection and supplies already-resolved
// navigation; applications fill only the fixed visual slots above.
export function ClientAppShell({
  children,
  navigation,
  linkComponent,
  navigationToggleIcon,
  topBar,
  beforeContent,
  afterContent,
  contentGroup,
}: ClientAppShellProps) {
  const [isNavigationCollapsed, setIsNavigationCollapsed] = useState(false);
  const hasNavigation = navigation.length > 0;
  const toggleNavigation = () =>
    setIsNavigationCollapsed((collapsed) => !collapsed);

  useEffect(() => {
    if (!hasNavigation) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "b") {
        event.preventDefault();
        setIsNavigationCollapsed((collapsed) => !collapsed);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [hasNavigation]);

  return (
    <PageLayoutProvider
      navigation={
        hasNavigation && navigationToggleIcon
          ? {
              icon: navigationToggleIcon,
              isCollapsed: isNavigationCollapsed,
              onToggle: toggleNavigation,
            }
          : undefined
      }
    >
      <AppShell
        contentPadding={0}
        height="fill"
        mobileNav={{ breakpoint: "md" }}
        sideNav={
          hasNavigation || contentGroup !== undefined ? (
            <SideNavigation
              isCollapsed={isNavigationCollapsed || !hasNavigation}
              linkComponent={linkComponent}
              onNavigate={() => setIsNavigationCollapsed(true)}
              sections={navigation}
            />
          ) : undefined
        }
        topNav={topBar}
        variant="wash"
        xstyle={styles.shell}
      >
        {beforeContent}
        <div
          key={contentGroup}
          {...stylex.props(
            styles.content,
            contentGroup !== undefined && styles.contentEnter,
          )}
        >
          {children}
        </div>
        {afterContent}
      </AppShell>
    </PageLayoutProvider>
  );
}

const contentEnter = stylex.keyframes({
  from: { opacity: 0, transform: "translateY(6px)" },
  to: { opacity: 1, transform: "none" },
});

const styles = stylex.create({
  shell: {
    color: colorVars["--color-text-primary"],
  },
  contentEnter: {
    animationName: contentEnter,
    animationDuration: durationVars["--duration-fast-max"],
    animationTimingFunction: "ease-out",
    animationFillMode: "backwards",
  },
  content: {
    position: "relative",
    minWidth: 0,
    minHeight: 0,
    height: `calc(100% - ${spacingVars["--spacing-4"]})`,
    marginBlock: spacingVars["--spacing-2"],
    marginInline: spacingVars["--spacing-2"],
    borderRadius: radiusVars["--radius-container"],
    overflow: "hidden",
    backgroundColor: colorVars["--color-background-surface"],
  },
});
