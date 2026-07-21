import { Button } from "@astryxdesign/core/Button";
import { Icon } from "@astryxdesign/core/Icon";
import { IconButton } from "@astryxdesign/core/IconButton";
import { Kbd } from "@astryxdesign/core/Kbd";
import { TopNav } from "@astryxdesign/core/TopNav";
import { colorVars, spacingVars } from "@astryxdesign/core/theme/tokens.stylex";
import * as stylex from "@stylexjs/stylex";
import { type ReactNode, useSyncExternalStore } from "react";

export type TopBarSearch = {
  icon?: ReactNode;
  label?: string;
  shortcut?: string;
  onOpen: () => void;
};

export function TopBar({
  heading,
  startContent,
  endContent,
  search,
  label = "Primary",
}: {
  heading?: ReactNode;
  startContent?: ReactNode;
  endContent?: ReactNode;
  search?: TopBarSearch;
  label?: string;
}) {
  const isCompact = useIsCompactTopBar();
  return (
    <TopNav
      centerContent={
        search && !isCompact ? <SearchField search={search} /> : undefined
      }
      endContent={
        <span {...stylex.props(styles.topActions)}>
          {search && isCompact ? (
            <IconButton
              icon={search.icon ?? <Icon icon="search" size="sm" />}
              label={search.label ?? "Search"}
              onClick={search.onOpen}
              size="sm"
              variant="ghost"
              xstyle={styles.compactSearch}
            />
          ) : null}
          {endContent}
        </span>
      }
      heading={heading}
      label={label}
      startContent={startContent}
      xstyle={isCompact ? styles.topBarCompact : styles.topBar}
    />
  );
}

const compactTopBarQuery = "(max-width: 900px)";

function subscribeToCompactTopBar(onChange: () => void) {
  const media = window.matchMedia(compactTopBarQuery);
  media.addEventListener("change", onChange);
  window.addEventListener("resize", onChange);
  return () => {
    media.removeEventListener("change", onChange);
    window.removeEventListener("resize", onChange);
  };
}

function useIsCompactTopBar() {
  return useSyncExternalStore(
    subscribeToCompactTopBar,
    () => window.matchMedia(compactTopBarQuery).matches,
  );
}

function SearchField({ search }: { search: TopBarSearch }) {
  return (
    <Button
      endContent={<Kbd keys={search.shortcut ?? "mod+k"} />}
      icon={search.icon}
      label={search.label ?? "Search"}
      onClick={search.onOpen}
      size="sm"
      variant="secondary"
      width="100%"
      xstyle={styles.searchLauncher}
    />
  );
}

const styles = stylex.create({
  topBar: {
    height: 48,
    display: "grid",
    gridTemplateColumns:
      "minmax(max-content, 1fr) minmax(96px, 300px) minmax(max-content, 1fr)",
    alignItems: "center",
    gap: 16,
    paddingBlock: 0,
    paddingInline: 24,
  },
  topBarCompact: {
    height: 48,
    display: "flex",
    alignItems: "center",
    justifyContent: "space-between",
    gap: 16,
    paddingBlock: 0,
    paddingInline: 24,
  },
  topActions: {
    display: "flex",
    alignItems: "center",
    justifyContent: "flex-end",
    justifySelf: "end",
    gap: 12,
    color: colorVars["--color-text-secondary"],
    minWidth: 0,
  },
  compactSearch: {
    color: "inherit",
  },
  searchLauncher: {
    color: colorVars["--color-text-secondary"],
    paddingBlock: spacingVars["--spacing-1"],
  },
});
