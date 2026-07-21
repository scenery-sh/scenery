import { Badge } from "@astryxdesign/core/Badge";
import { Button } from "@astryxdesign/core/Button";
import { Icon } from "@astryxdesign/core/Icon";
import { Popover } from "@astryxdesign/core/Popover";
import { Selector } from "@astryxdesign/core/Selector";
import { Text } from "@astryxdesign/core/Text";
import { TextInput } from "@astryxdesign/core/TextInput";
import { Toolbar } from "@astryxdesign/core/Toolbar";
import { spacingVars } from "@astryxdesign/core/theme/tokens.stylex";
import * as stylex from "@stylexjs/stylex";
import type { ReactNode } from "react";

export interface FilterToolbarFilter {
  readonly field: string;
  readonly label: string;
  readonly options: readonly {
    readonly value: string;
    readonly label: string;
  }[];
  readonly custom?: boolean;
  readonly pinned?: boolean;
}

export interface FilterToolbarActiveFilter {
  readonly field: string;
  readonly label: string;
  readonly valueLabel: string;
  readonly onClear: () => void;
}

export function FilterToolbar({
  search,
  searchLabel,
  onSearchChange,
  filters,
  values,
  onFilterChange,
  activeFilterItems,
  filterContent,
  resultLabel,
  exportLabel,
  exportIcon,
  onExport,
  children,
}: {
  readonly search?: string;
  readonly searchLabel?: string;
  readonly onSearchChange?: (value: string) => void;
  readonly filters: readonly FilterToolbarFilter[];
  readonly values: Readonly<Record<string, string | undefined>>;
  readonly onFilterChange: (field: string, value: string | undefined) => void;
  readonly activeFilterItems?: readonly FilterToolbarActiveFilter[];
  readonly filterContent?: ReactNode;
  readonly resultLabel?: string;
  readonly exportLabel?: string;
  readonly exportIcon?: ReactNode;
  readonly onExport?: () => void;
  readonly children?: ReactNode;
}) {
  const pinnedFilters = filters.filter(
    (filter) => filter.pinned && !filter.custom,
  );
  // Pinned filters are quick-access controls, not a second filter set. Keep
  // them in the complete popover as well so every declared filter is editable
  // from one predictable place.
  const popoverFilters = filters.filter((filter) => !filter.custom);
  const activeFilters: FilterToolbarActiveFilter[] = filters.flatMap(
    (filter) => {
      const value = values[filter.field];
      if (!value) return [];
      return [
        {
          field: filter.field,
          label: filter.label,
          valueLabel:
            filter.options.find((option) => option.value === value)?.label ??
            value,
          onClear: () => onFilterChange(filter.field, undefined),
        },
      ];
    },
  );
  activeFilters.push(...(activeFilterItems ?? []));
  const hasFilterPopover = popoverFilters.length > 0 || filterContent;

  return (
    <div {...stylex.props(styles.root)}>
      <Toolbar
        endContent={
          resultLabel || onExport ? (
            <>
              {resultLabel ? (
                <Text color="secondary" type="supporting">
                  {resultLabel}
                </Text>
              ) : null}
              {onExport ? (
                <Button
                  icon={exportIcon ?? <Icon icon="arrowDown" size="sm" />}
                  label={exportLabel ?? "Export"}
                  onClick={onExport}
                  size="sm"
                  variant="secondary"
                />
              ) : null}
            </>
          ) : undefined
        }
        gap={2}
        label="Table filters and actions"
        size="sm"
        startContent={
          <div {...stylex.props(styles.controls)}>
            {onSearchChange ? (
              <TextInput
                hasClear
                isLabelHidden
                label={searchLabel ?? "Search"}
                onChange={onSearchChange}
                placeholder={searchLabel ?? "Search"}
                size="sm"
                startIcon={<Icon icon="search" size="sm" />}
                value={search ?? ""}
                width={240}
              />
            ) : null}
            {pinnedFilters.map((filter) => (
              <FilterSelector
                filter={filter}
                key={filter.field}
                onFilterChange={onFilterChange}
                value={values[filter.field]}
                width={180}
              />
            ))}
            {hasFilterPopover ? (
              <Popover
                alignment="start"
                content={
                  <div {...stylex.props(styles.filterPanel)}>
                    {popoverFilters.map((filter) => (
                      <FilterSelector
                        filter={filter}
                        key={filter.field}
                        onFilterChange={onFilterChange}
                        value={values[filter.field]}
                        width="100%"
                      />
                    ))}
                    {filterContent}
                  </div>
                }
                label="Filters"
                placement="below"
                width={280}
              >
                <Button
                  endContent={
                    activeFilters.length > 0 ? (
                      <Badge label={activeFilters.length} variant="neutral" />
                    ) : undefined
                  }
                  icon={<Icon icon="funnel" size="sm" />}
                  label="Filters"
                  size="sm"
                  variant="secondary"
                />
              </Popover>
            ) : null}
            {children}
          </div>
        }
      />
      {activeFilters.length > 0 ? (
        <div aria-label="Active filters" {...stylex.props(styles.chips)}>
          {activeFilters.map((filter) => (
            <Button
              key={filter.field}
              label={`Remove ${filter.label} filter`}
              onClick={filter.onClear}
              size="sm"
              variant="secondary"
            >
              {`${filter.label} · ${filter.valueLabel} ×`}
            </Button>
          ))}
          <Button
            label="Clear all"
            onClick={() => {
              for (const filter of activeFilters) {
                filter.onClear();
              }
            }}
            size="sm"
            variant="ghost"
          />
        </div>
      ) : null}
    </div>
  );
}

function FilterSelector({
  filter,
  value,
  onFilterChange,
  width,
}: {
  readonly filter: FilterToolbarFilter;
  readonly value?: string;
  readonly onFilterChange: (field: string, value: string | undefined) => void;
  readonly width: number | string;
}) {
  return (
    <Selector
      hasClear
      isLabelHidden
      label={filter.label}
      onChange={(nextValue: string | null) =>
        onFilterChange(filter.field, nextValue ?? undefined)
      }
      options={filter.options.map((option) => ({ ...option }))}
      placeholder={`All ${plural(filter.label.toLocaleLowerCase())}`}
      size="sm"
      value={value ?? null}
      width={width}
    />
  );
}

function plural(value: string) {
  if (value.endsWith("s")) return value;
  if (value.endsWith("y")) return `${value.slice(0, -1)}ies`;
  return `${value}s`;
}

const styles = stylex.create({
  root: {
    display: "flex",
    flexDirection: "column",
    gap: spacingVars["--spacing-2"],
  },
  controls: {
    display: "flex",
    alignItems: "center",
    flexWrap: "wrap",
    gap: spacingVars["--spacing-2"],
    minWidth: 0,
  },
  filterPanel: {
    display: "flex",
    flexDirection: "column",
    alignItems: "stretch",
    gap: spacingVars["--spacing-3"],
  },
  chips: {
    display: "flex",
    alignItems: "center",
    flexWrap: "wrap",
    gap: spacingVars["--spacing-2"],
  },
});
