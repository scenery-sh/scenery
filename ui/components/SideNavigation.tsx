import type { LinkComponentType } from "@astryxdesign/core/Link";
import { renderIconSlot } from "@astryxdesign/core/Icon";
import {
  SideNav,
  SideNavItem,
  SideNavSection,
} from "@astryxdesign/core/SideNav";
import * as stylex from "@stylexjs/stylex";
import type { MouseEvent, ReactNode } from "react";
import { t } from "../tokens.stylex.js";

// Origin is intrinsic route provenance. Generated adapters stamp it; authored
// navigation entries are normalized to "authored" by the generated app shell.
export type NavigationOrigin = "generated" | "authored";

export type SideNavigationItem = {
  label: string;
  children?: ReactNode;
  endContent?: ReactNode;
  href?: string;
  icon?: ReactNode;
  isDisabled?: boolean;
  isSelected?: boolean;
  onClick?: (event: MouseEvent) => void;
  selectedIcon?: ReactNode;
  /** Route provenance for styling and app-level inspection. */
  origin?: NavigationOrigin;
};

export type SideNavigationSection = {
  title: string;
  subtitle?: string;
  isHeaderHidden?: boolean;
  className?: string;
  endContent?: ReactNode;
  /**
   * Render the group as a collapsible parent item with a chevron toggle
   * instead of a flat titled section. Starts collapsed unless one of its
   * items is selected on first render.
   */
  collapsible?: boolean;
  /** Icon for the collapsible parent item. Flat sections ignore it. */
  icon?: ReactNode;
  items: readonly SideNavigationItem[];
};

export function SideNavigation({
  sections,
  linkComponent,
  isCollapsed = false,
  onNavigate,
}: {
  sections: readonly SideNavigationSection[];
  linkComponent?: LinkComponentType;
  isCollapsed?: boolean;
  onNavigate?: () => void;
}) {
  return (
    <div
      {...stylex.props(
        styles.sideNavWrap,
        isCollapsed && styles.sideNavWrapCollapsed,
      )}
      aria-hidden={isCollapsed}
      inert={isCollapsed}
    >
      <SideNav xstyle={styles.sideNav}>
        {sections.map((section, sectionIndex) => {
          const items = section.items.map((item, itemIndex) => {
            const { icon, origin, ...sideNavItem } = item;
            return (
              <div
                data-origin={origin}
                key={`${item.label}-${item.href ?? itemIndex}`}
              >
                <SideNavItem
                  {...sideNavItem}
                  as={linkComponent}
                  icon={
                    origin === "authored" && icon ? (
                      <span {...stylex.props(styles.authoredIcon)}>
                        {renderIconSlot(icon, { color: "inherit", size: "sm" })}
                      </span>
                    ) : (
                      icon
                    )
                  }
                  onClick={
                    item.onClick || (item.href && onNavigate)
                      ? (event: MouseEvent) =>
                          handleNavigate(event, item.onClick, onNavigate)
                      : undefined
                  }
                  size="sm"
                />
              </div>
            );
          });
          return (
            <SideNavSection
              key={`${section.title}-${sectionIndex}`}
              className={section.className}
              endContent={section.endContent}
              isHeaderHidden={section.isHeaderHidden || section.collapsible}
              subtitle={section.subtitle}
              title={section.title}
              xstyle={
                sectionIndex === 0 ? styles.sideSection : styles.sideGroup
              }
            >
              {section.collapsible ? (
                <SideNavItem
                  collapsible={{
                    defaultIsCollapsed: !section.items.some(
                      (item) => item.isSelected,
                    ),
                  }}
                  icon={section.icon}
                  label={section.title}
                  size="sm"
                >
                  {items}
                </SideNavItem>
              ) : (
                items
              )}
            </SideNavSection>
          );
        })}
      </SideNav>
    </div>
  );
}

function handleNavigate(
  event: MouseEvent,
  onClick: SideNavigationItem["onClick"],
  onNavigate: (() => void) | undefined,
) {
  onClick?.(event);
  if (
    !event.defaultPrevented &&
    onNavigate &&
    window.matchMedia("(max-width: 760px)").matches
  ) {
    onNavigate();
  }
}

const styles = stylex.create({
  sideNavWrap: {
    width: 225,
    height: "100%",
    flexShrink: 0,
    overflow: "hidden",
    transitionProperty: "width",
    transitionDuration: "150ms",
    transitionTimingFunction: "cubic-bezier(0.175, 0.885, 0.32, 1.1)",
  },
  sideNavWrapCollapsed: { width: 0 },
  sideNav: {
    width: 225,
    minWidth: 225,
    height: "100%",
    paddingBlock: 6,
    paddingInline: 5,
  },
  sideSection: {
    display: "flex",
    flexDirection: "column",
    gap: 0,
    paddingBlock: 0,
  },
  sideGroup: { marginTop: 20 },
  authoredIcon: { color: t.dangerIcon },
});
