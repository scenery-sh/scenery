import { Button } from "@astryxdesign/core/Button";
import { Text } from "@astryxdesign/core/Text";
import { colorVars, spacingVars } from "@astryxdesign/core/theme/tokens.stylex";
import * as stylex from "@stylexjs/stylex";
import type { ReactNode } from "react";

export function QueryState({
  error,
  isPending,
  isEmpty,
  resource,
  retry,
  errorTitle,
  loadingLabel,
  empty,
  children,
  getErrorMessage = defaultErrorMessage,
}: {
  error?: unknown;
  isPending?: boolean;
  isEmpty?: boolean;
  resource: string;
  retry?: () => void;
  errorTitle?: string;
  loadingLabel?: ReactNode;
  empty?: ReactNode;
  children: ReactNode;
  getErrorMessage?: (error: unknown) => string;
}) {
  if (error) {
    return (
      <div {...stylex.props(styles.state)} role="alert">
        <Text weight="semibold">
          {errorTitle ?? `Unable to load ${resource}`}
        </Text>
        <Text color="secondary" type="supporting">
          {getErrorMessage(error)}
        </Text>
        {retry ? (
          <Button label="Retry" onClick={retry} size="sm" variant="secondary" />
        ) : null}
      </div>
    );
  }
  if (isPending) {
    return (
      <div {...stylex.props(styles.state)}>
        <Text color="secondary">{loadingLabel ?? `Loading ${resource}…`}</Text>
      </div>
    );
  }
  if (isEmpty) {
    return <EmptyState>{empty ?? `No ${resource} found`}</EmptyState>;
  }
  return <>{children}</>;
}

export function EmptyState({
  children,
  compact,
}: {
  children: ReactNode;
  compact?: boolean;
}) {
  return (
    <div {...stylex.props(styles.empty, compact && styles.emptyCompact)}>
      <Text color="secondary" type="supporting">
        {children}
      </Text>
    </div>
  );
}

export function TableEmptyRow({
  columns,
  children,
}: {
  columns: number;
  children: ReactNode;
}) {
  return (
    <tr>
      <td colSpan={columns} {...stylex.props(styles.emptyCell)}>
        <Text color="secondary" type="supporting">
          {children}
        </Text>
      </td>
    </tr>
  );
}

function defaultErrorMessage(error: unknown) {
  return error instanceof Error ? error.message : "Unexpected API error";
}

const styles = stylex.create({
  state: {
    minHeight: 220,
    display: "flex",
    flexDirection: "column",
    alignItems: "center",
    justifyContent: "center",
    gap: spacingVars["--spacing-3"],
    padding: spacingVars["--spacing-6"],
    textAlign: "center",
  },
  empty: {
    minHeight: 120,
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    padding: spacingVars["--spacing-4"],
    color: colorVars["--color-text-secondary"],
    textAlign: "center",
  },
  emptyCompact: { minHeight: 64 },
  emptyCell: {
    height: 160,
    padding: spacingVars["--spacing-6"],
    textAlign: "center",
  },
});
