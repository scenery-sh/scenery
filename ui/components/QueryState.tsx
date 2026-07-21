import { Button } from "@astryxdesign/core/Button";
import { EmptyState } from "@astryxdesign/core/EmptyState";
import { Spinner } from "@astryxdesign/core/Spinner";
import { TableCell, TableRow } from "@astryxdesign/core/Table";
import { Text } from "@astryxdesign/core/Text";
import { spacingVars } from "@astryxdesign/core/theme/tokens.stylex";
import * as stylex from "@stylexjs/stylex";
import type { ReactNode } from "react";

export interface QueryStateProps {
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
}

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
}: QueryStateProps) {
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
      <div
        {...stylex.props(styles.state)}
        aria-live="polite"
        aria-label={`Loading ${resource}`}
        role="status"
      >
        <Spinner aria-hidden="true" shade="subtle" size="sm" />
        {loadingLabel ? <Text color="secondary">{loadingLabel}</Text> : null}
      </div>
    );
  }
  if (isEmpty) {
    const content = empty ?? `No ${resource} found`;
    return typeof content === "string" || typeof content === "number" ? (
      <EmptyState title={String(content)} />
    ) : (
      <>{content}</>
    );
  }
  return <>{children}</>;
}

export function TableEmptyRow({
  columns,
  children,
}: {
  columns: number;
  children: ReactNode;
}) {
  return (
    <TableRow>
      <TableCell colSpan={columns} xstyle={styles.emptyCell}>
        <Text color="secondary" type="supporting">
          {children}
        </Text>
      </TableCell>
    </TableRow>
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
  emptyCell: {
    height: 160,
    padding: spacingVars["--spacing-6"],
    textAlign: "center",
  },
});
