import { Badge } from "@astryxdesign/core/Badge";
import { EmptyState } from "@astryxdesign/core/EmptyState";
import { Heading } from "@astryxdesign/core/Heading";
import {
  Selector,
  type SelectorOptionType,
} from "@astryxdesign/core/Selector";
import { Tab, TabList } from "@astryxdesign/core/TabList";
import { Tooltip } from "@astryxdesign/core/Tooltip";
import { Text } from "@astryxdesign/core/Text";
import * as stylex from "@stylexjs/stylex";
import {
  type ReactNode,
  type MouseEvent,
  useCallback,
  useEffect,
  useMemo,
  useState,
} from "react";
import { PageShell } from "./PageLayout.js";
import { SideNavigation } from "./SideNavigation.js";
import { WorkspaceEmbeddedPageProvider } from "./workspace-context.js";
import { t } from "../tokens.stylex.js";

export type WorkspacePagePresentation = "sidebar" | "tabs";

type WorkspacePageTabBase = {
  readonly name: string;
  readonly label: string;
  readonly description?: string;
  readonly group?: string;
  readonly count?: number | bigint;
  readonly available?: boolean;
  readonly unavailableReason?: string;
};

export type WorkspacePageTab = WorkspacePageTabBase &
  (
    | {
        readonly content: ReactNode;
        readonly destination?: never;
      }
    | {
        readonly content?: never;
        readonly destination: string;
      }
    | {
        readonly content?: never;
        readonly destination?: never;
      }
  );

export function WorkspacePage({
  title,
  tabs,
  activeTab,
  onTabChange,
  presentation = "tabs",
  actions,
  stats,
  onNavigateDestination,
}: {
  readonly title: string;
  readonly tabs: readonly WorkspacePageTab[];
  readonly activeTab: string;
  readonly onTabChange: (name: string) => void;
  readonly presentation?: WorkspacePagePresentation;
  readonly actions?: ReactNode;
  readonly stats?: ReactNode;
  readonly onNavigateDestination?: (destination: string) => void;
}) {
  const [visited, setVisited] = useState<ReadonlySet<string>>(
    () => new Set([activeTab]),
  );
  const [actionsHost, setActionsHost] = useState<HTMLSpanElement | null>(null);
  const activeEntry = tabs.find((tab) => tab.name === activeTab);

  useEffect(() => {
    setVisited((current) => {
      if (current.has(activeTab)) return current;
      const next = new Set(current);
      next.add(activeTab);
      return next;
    });
  }, [activeTab]);

  const enabledTabs = useMemo(
    () =>
      tabs.filter(
        (tab) => workspaceEntryHasContent(tab) && tab.available !== false,
      ),
    [tabs],
  );
  useEffect(() => {
    if (enabledTabs.some((tab) => tab.name === activeTab)) return;
    const first = enabledTabs[0];
    if (first) onTabChange(first.name);
  }, [activeTab, enabledTabs, onTabChange]);

  const selectEntry = useCallback(
    (name: string) => {
      const tab = tabs.find((candidate) => candidate.name === name);
      if (!tab) return;
      if (tab.destination !== undefined) {
        if (onNavigateDestination) {
          onNavigateDestination(tab.destination);
        } else if (typeof globalThis.location !== "undefined") {
          globalThis.location.assign(tab.destination);
        }
        return;
      }
      if (workspaceEntryHasContent(tab) && tab.available !== false) {
        onTabChange(tab.name);
      }
    },
    [onNavigateDestination, onTabChange, tabs],
  );

  const sections = useMemo(() => {
    const grouped = new Map<string, WorkspacePageTab[]>();
    for (const tab of tabs) {
      const group = tab.group ?? "Workspace";
      grouped.set(group, [...(grouped.get(group) ?? []), tab]);
    }
    return Array.from(grouped, ([group, items]) => ({
      title: group,
      items: items.map((tab) => ({
        children: undefined,
        href: tab.destination,
        icon: undefined,
        label: workspaceEntryLabel(tab),
        isDisabled: workspaceEntryIsDisabled(tab),
        isSelected: tab.name === activeTab,
        onClick:
          tab.destination === undefined
            ? () => selectEntry(tab.name)
            : onNavigateDestination
              ? (event: MouseEvent) => {
                  event.preventDefault();
                  selectEntry(tab.name);
                }
              : undefined,
        selectedIcon: undefined,
        endContent: workspaceEntryEndContent(tab),
      })),
    }));
  }, [activeTab, onNavigateDestination, selectEntry, tabs]);

  const selectorOptions = useMemo<SelectorOptionType[]>(
    () => {
      const grouped = new Map<string, WorkspacePageTab[]>();
      for (const tab of tabs) {
        const group = tab.group ?? "Workspace";
        grouped.set(group, [...(grouped.get(group) ?? []), tab]);
      }
      return Array.from(grouped, ([title, items]) => ({
        type: "section",
        title,
        options: items.map((tab) => ({
          disabled: workspaceEntryIsDisabled(tab),
          label: `${workspaceEntryLabel(tab)}${
            workspaceEntryIsDisabled(tab)
              ? " · unavailable"
              : ""
          }`,
          value: tab.name,
        })),
      }));
    },
    [tabs],
  );

  const content = (
    <div {...stylex.props(styles.content)}>
      {tabs.map((tab) => {
        if (!workspaceEntryHasContent(tab)) return null;
        if (!visited.has(tab.name)) return null;
        const active = tab.name === activeTab && tab.available !== false;
        return (
          <div
            key={tab.name}
            aria-hidden={!active}
            hidden={!active}
            {...stylex.props(styles.panel)}
          >
            <WorkspaceEmbeddedPageProvider
              actionsHost={active ? actionsHost : null}
            >
              {tab.content}
            </WorkspaceEmbeddedPageProvider>
          </div>
        );
      })}
      {activeEntry &&
      workspaceEntryHasContent(activeEntry) &&
      activeEntry.available === false ? (
        <EmptyState
          description={activeEntry.unavailableReason}
          title={`${activeEntry.label} is unavailable`}
        />
      ) : null}
    </div>
  );

  return (
    <PageShell
      title={title}
      actions={
        <>
          {actions}
          <span ref={setActionsHost} {...stylex.props(styles.actionsHost)} />
        </>
      }
    >
      <div {...stylex.props(styles.root)}>
        {stats}
        {presentation === "sidebar" ? (
          <div {...stylex.props(styles.sidebarLayout)}>
            <div {...stylex.props(styles.desktopNavigation)}>
              <SideNavigation sections={sections} />
            </div>
            <div {...stylex.props(styles.mobileNavigation)}>
              <Selector
                isLabelHidden
                label={`${title} section`}
                onChange={selectEntry}
                options={selectorOptions}
                size="sm"
                value={activeTab}
                width="100%"
              />
            </div>
            <div {...stylex.props(styles.sidebarContent)}>
              {activeEntry && workspaceEntryHasContent(activeEntry) ? (
                <header {...stylex.props(styles.activeEntryHeader)}>
                  <Heading level={2}>{activeEntry.label}</Heading>
                  {activeEntry.description ? (
                    <Text color="secondary" type="supporting">
                      {activeEntry.description}
                    </Text>
                  ) : null}
                </header>
              ) : null}
              {content}
            </div>
          </div>
        ) : (
          <>
            <TabList
              aria-label={`${title} sections`}
              hasDivider
              onChange={onTabChange}
              size="sm"
              value={activeTab}
            >
              {enabledTabs.map((tab) => (
                <Tab
                  key={tab.name}
                  endContent={
                    tab.count === undefined ? undefined : (
                      <Badge label={String(tab.count)} variant="neutral" />
                    )
                  }
                  label={tab.label}
                  value={tab.name}
                />
              ))}
            </TabList>
            {content}
          </>
        )}
      </div>
    </PageShell>
  );
}

function workspaceEntryLabel(tab: WorkspacePageTab) {
  const details = [
    tab.description,
    workspaceEntryIsDisabled(tab) || tab.available === false
      ? tab.unavailableReason
      : undefined,
  ].filter((value): value is string => Boolean(value));
  return details.length > 0 ? `${tab.label} — ${details.join(" — ")}` : tab.label;
}

function workspaceEntryEndContent(tab: WorkspacePageTab) {
  if (tab.destination !== undefined) {
    const badge = <Badge label="Open" variant="neutral" />;
    return tab.available === false && tab.unavailableReason ? (
      <Tooltip content={tab.unavailableReason} placement="end">
        {badge}
      </Tooltip>
    ) : (
      badge
    );
  }
  if (workspaceEntryIsDisabled(tab)) {
    const badge = <Badge label="Unavailable" variant="neutral" />;
    return tab.unavailableReason ? (
      <Tooltip content={tab.unavailableReason} placement="end">
        {badge}
      </Tooltip>
    ) : (
      badge
    );
  }
  return tab.count === undefined ? undefined : (
    <Badge label={String(tab.count)} variant="neutral" />
  );
}

function workspaceEntryHasContent(
  tab: WorkspacePageTab,
): tab is WorkspacePageTab & { readonly content: ReactNode } {
  return tab.destination === undefined && "content" in tab;
}

function workspaceEntryIsDisabled(tab: WorkspacePageTab) {
  return tab.destination === undefined &&
    (!workspaceEntryHasContent(tab) || tab.available === false);
}

const styles = stylex.create({
  root: {
    boxSizing: "border-box",
    display: "flex",
    flexDirection: "column",
    height: "100%",
    minHeight: 0,
    minWidth: 0,
  },
  sidebarLayout: {
    display: "flex",
    flexDirection: { default: "row", "@media (max-width: 760px)": "column" },
    flex: 1,
    minHeight: 0,
    minWidth: 0,
  },
  desktopNavigation: {
    display: { default: "block", "@media (max-width: 760px)": "none" },
  },
  mobileNavigation: {
    display: { default: "none", "@media (max-width: 760px)": "block" },
    flexShrink: 0,
    paddingBlockEnd: t.space2,
  },
  sidebarContent: {
    display: "flex",
    flexDirection: "column",
    flex: 1,
    minHeight: 0,
    minWidth: 0,
  },
  activeEntryHeader: {
    display: "flex",
    flexDirection: "column",
    flexShrink: 0,
    gap: t.space1,
    paddingBlockStart: t.space4,
    paddingInline: t.space4,
  },
  content: {
    flex: 1,
    minHeight: 0,
    minWidth: 0,
  },
  panel: {
    height: "100%",
    minHeight: 0,
  },
  actionsHost: {
    display: "contents",
  },
});
