import { Kbd } from "@astryxdesign/core/Kbd";
import { TopNav } from "@astryxdesign/core/TopNav";
import { colorVars } from "@astryxdesign/core/theme/tokens.stylex";
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
            <button
              type="button"
              aria-label={search.label ?? "Search"}
              onClick={search.onOpen}
              {...stylex.props(styles.compactSearch)}
            >
              {search.icon}
            </button>
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
  return useSyncExternalStore(subscribeToCompactTopBar, () =>
    window.matchMedia(compactTopBarQuery).matches,
  );
}

function SearchField({ search }: { search: TopBarSearch }) {
  return (
    <button
      type="button"
      onClick={search.onOpen}
      {...stylex.props(styles.searchLauncher)}
    >
      {search.icon}
      <span {...stylex.props(styles.searchLauncherLabel)}>
        {search.label ?? "Search"}
      </span>
      <Kbd keys={search.shortcut ?? "mod+k"} />
    </button>
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
    appearance: "none",
    display: "inline-grid",
    placeItems: "center",
    width: 30,
    height: 30,
    padding: 0,
    border: 0,
    borderRadius: 9,
    backgroundColor: {
      default: "transparent",
      ":hover": colorVars["--color-overlay-hover"],
    },
    color: "inherit",
    cursor: "pointer",
  },
  searchLauncher: {
    display: "flex",
    alignItems: "center",
    gap: 8,
    width: "100%",
    height: 30,
    paddingInline: 10,
    borderColor: colorVars["--color-border-emphasized"],
    borderStyle: "solid",
    borderWidth: 1,
    borderRadius: 9,
    backgroundColor: {
      default: "transparent",
      ":hover": colorVars["--color-overlay-hover"],
    },
    color: colorVars["--color-text-secondary"],
    fontSize: 13,
    cursor: "pointer",
  },
  searchLauncherLabel: { flexGrow: 1, textAlign: "start" },
});
