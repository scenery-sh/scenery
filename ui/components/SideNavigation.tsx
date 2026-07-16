import type { LinkComponentType } from "@astryxdesign/core/Link";
import {
  SideNav,
  SideNavItem,
  type SideNavItemProps,
  SideNavSection,
} from "@astryxdesign/core/SideNav";
import * as stylex from "@stylexjs/stylex";
import type { MouseEvent, ReactNode } from "react";

export type SideNavigationItem = Pick<
  SideNavItemProps,
  | "children"
  | "endContent"
  | "href"
  | "icon"
  | "isDisabled"
  | "isSelected"
  | "label"
  | "onClick"
  | "selectedIcon"
>;

export type SideNavigationSection = {
  title: string;
  subtitle?: string;
  isHeaderHidden?: boolean;
  className?: string;
  endContent?: ReactNode;
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
        {sections.map((section, sectionIndex) => (
          <SideNavSection
            key={`${section.title}-${sectionIndex}`}
            className={section.className}
            endContent={section.endContent}
            isHeaderHidden={section.isHeaderHidden}
            subtitle={section.subtitle}
            title={section.title}
            xstyle={sectionIndex === 0 ? styles.sideSection : styles.sideGroup}
          >
            {section.items.map((item, itemIndex) => (
              <SideNavItem
                {...item}
                key={`${item.label}-${item.href ?? itemIndex}`}
                as={linkComponent}
                onClick={
                  item.onClick || onNavigate
                    ? (event: MouseEvent) =>
                        handleNavigate(event, item.onClick, onNavigate)
                    : undefined
                }
                size="sm"
              />
            ))}
          </SideNavSection>
        ))}
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
});
