import { Dialog, DialogHeader } from "@astryxdesign/core/Dialog";
import { MetadataList, MetadataListItem } from "@astryxdesign/core/MetadataList";
import { Section } from "@astryxdesign/core/Section";
import { HStack, VStack } from "@astryxdesign/core/Stack";
import { Text } from "@astryxdesign/core/Text";
import { spacingVars } from "@astryxdesign/core/theme/tokens.stylex";
import * as stylex from "@stylexjs/stylex";
import type { ReactNode } from "react";
import { WorkspaceEmbeddedPageProvider } from "./workspace-context.js";

export interface DetailPageActionProps<
  Data,
  Params extends Readonly<Record<string, string>> = Readonly<
    Record<string, string>
  >,
> {
  readonly data: Data;
  readonly params: Params;
  /** Refreshes the detail record and every generated related-table query. */
  readonly onMutated: () => Promise<void>;
  /** Present only when the shared content is mounted in its dialog wrapper. */
  readonly onClose?: () => void;
}

export function DetailPageLayout({
  actions,
  children,
}: {
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <VStack gap={4}>
      {actions ? (
        <HStack
          align="center"
          gap={2}
          justify="end"
          role="group"
          xstyle={styles.actions}
        >
          {actions}
        </HStack>
      ) : null}
      {children}
    </VStack>
  );
}

export function DetailSection({
  title,
  description,
  children,
}: {
  title: string;
  description?: string;
  children: ReactNode;
}) {
  return (
    <Section padding={4} variant="section">
      <VStack gap={3}>
        {description ? (
          <VStack gap={1}>
            <Text type="label">
              {title}
            </Text>
            <Text type="supporting">
              {description}
            </Text>
          </VStack>
        ) : null}
        <MetadataList
          columns="multi"
          title={description ? undefined : title}
        >
          {children}
        </MetadataList>
      </VStack>
    </Section>
  );
}

export function DetailField({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return <MetadataListItem label={label}>{children}</MetadataListItem>;
}

export function DetailRelated({
  title,
  children,
}: {
  title: string;
  children: ReactNode;
}) {
  return (
    <Section padding={4} variant="section">
      <VStack gap={3}>
        <Text type="label">
          {title}
        </Text>
        <WorkspaceEmbeddedPageProvider actionsHost={null}>
          {children}
        </WorkspaceEmbeddedPageProvider>
      </VStack>
    </Section>
  );
}

export function DetailDialog({
  open,
  onOpenChange,
  title,
  description,
  children,
  width = 760,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description?: string;
  children: ReactNode;
  width?: number;
}) {
  return (
    <Dialog
      isOpen={open}
      onOpenChange={onOpenChange}
      padding={0}
      purpose="info"
      width={`min(${width}px, calc(100vw - 16px))`}
      xstyle={styles.dialog}
    >
      <div {...stylex.props(styles.dialogContent)}>
        <DialogHeader
          onOpenChange={onOpenChange}
          subtitle={description}
          title={title}
        />
        <div {...stylex.props(styles.dialogBody)}>{children}</div>
      </div>
    </Dialog>
  );
}

const styles = stylex.create({
  actions: { flexWrap: "wrap" },
  dialog: { maxHeight: "calc(100dvh - 16px)" },
  dialogContent: {
    maxHeight: "calc(100dvh - 16px)",
    display: "flex",
    flexDirection: "column",
  },
  dialogBody: {
    minHeight: 0,
    overflowY: "auto",
    padding: spacingVars["--spacing-4"],
  },
});
