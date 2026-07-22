import { Button } from "@astryxdesign/core/Button";
import type { ISODateTimeString } from "@astryxdesign/core/DateTimeInput";
import { DateTimeInput } from "@astryxdesign/core/DateTimeInput";
import { Selector } from "@astryxdesign/core/Selector";
import { spacingVars } from "@astryxdesign/core/theme/tokens.stylex";
import * as stylex from "@stylexjs/stylex";
import { normalizeFilterOption } from "./query-table-cells.js";
import type {
	TablePageDateTimeRange,
	TablePageFilter,
	TablePageResultContext,
} from "./query-table-contract.js";
import {
	dateTimePreset,
	exactDateTime,
	localDateTime,
} from "./query-table-datetime.js";

export {
	dateTimePreset,
	exactDateTime,
	formatLocalDateTime,
	localDateTime,
} from "./query-table-datetime.js";

export function EnumFilter<
	Row extends object,
	Metadata extends object = Record<string, never>,
>({
	filter,
	value,
	onChange,
	context,
}: {
	filter: Extract<TablePageFilter<Row, Metadata>, { readonly kind: "enum" }>;
	value: string | undefined;
	onChange: (value: string | undefined) => void;
	context: TablePageResultContext<Row, Metadata>;
}) {
	if (filter.component) {
		const Component = filter.component;
		return (
			<Component
				context={context}
				label={filter.label}
				onChange={onChange}
				value={value}
			/>
		);
	}
	return (
		<Selector
			hasClear
			label={filter.label}
			onChange={(next: string | null) => onChange(next ?? undefined)}
			options={filter.options.map(normalizeFilterOption)}
			placeholder="All"
			size="sm"
			value={value ?? null}
			width={180}
		/>
	);
}

export function DateTimeFilter<
	Row extends object,
	Metadata extends object = Record<string, never>,
>({
	filter,
	value,
	onChange,
	context,
}: {
	filter: Extract<
		TablePageFilter<Row, Metadata>,
		{ readonly kind: "datetime" }
	>;
	value: TablePageDateTimeRange;
	onChange: (value: TablePageDateTimeRange) => void;
	context: TablePageResultContext<Row, Metadata>;
}) {
	if (filter.component) {
		const Component = filter.component;
		return (
			<Component
				context={context}
				label={filter.label}
				onChange={(next) => onChange(next ?? {})}
				value={value}
			/>
		);
	}
	return (
		<div {...stylex.props(styles.dateRange)}>
			{filter.presets?.length ? (
				<div {...stylex.props(styles.datePresets)}>
					{filter.presets.map((preset) => (
						<Button
							key={preset.range}
							label={preset.label}
							onClick={() => onChange(dateTimePreset(preset.range))}
							size="sm"
							variant="secondary"
						/>
					))}
				</div>
			) : null}
			<DateTimeInput
				hasClear
				label={`${filter.label} from`}
				onChange={(next: ISODateTimeString | undefined) =>
					onChange({ ...value, from: exactDateTime(next, value.from) })
				}
				size="sm"
				value={localDateTime(value.from)}
				width={240}
			/>
			<DateTimeInput
				hasClear
				label={`${filter.label} to`}
				onChange={(next: ISODateTimeString | undefined) =>
					onChange({ ...value, to: exactDateTime(next, value.to) })
				}
				size="sm"
				value={localDateTime(value.to)}
				width={240}
			/>
		</div>
	);
}

const styles = stylex.create({
	dateRange: {
		display: "flex",
		alignItems: "flex-end",
		flexWrap: "wrap",
		gap: spacingVars["--spacing-2"],
	},
	datePresets: {
		display: "flex",
		alignItems: "center",
		flexWrap: "wrap",
		gap: spacingVars["--spacing-1"],
		width: "100%",
	},
});
