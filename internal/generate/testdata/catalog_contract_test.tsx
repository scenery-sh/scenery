import * as stylex from "@stylexjs/stylex";
import type {
	ContentPageSlotProps,
	DetailPageActionProps,
	RequestState,
	SplitPageSlotProps,
	TablePageCellProps,
	TablePageDateTimePreset,
	TablePageDateTimeRange,
	TablePageFilterProps,
	TablePageFooterProps,
	TablePageRowActionProps,
	TablePageToolbarProps,
} from "../../../ui/index.js";
import {
	Button,
	DetailDialog,
	DetailField,
	DetailPageLayout,
	DetailRelated,
	DetailSection,
	defineContentPageSlots,
	defineSplitPageSlots,
	defineTablePageSlots,
	queryStateProps,
	SplitPage,
	Text,
	VStack,
	WorkspacePage,
} from "../../../ui/index.js";
import { t } from "../../../ui/tokens.stylex.js";

interface Row {
	readonly id: string;
	readonly status: "open" | "closed";
}

interface ResultMetadata {
	readonly summary: string;
	readonly types: readonly string[];
}

const requestState: RequestState<{ readonly data: Row }> = {
	kind: "loading",
};
const datePreset: TablePageDateTimePreset = {
	label: "Month to date",
	range: "month_to_date",
};
void datePreset;
const requestStateView = queryStateProps(requestState, "row");
const facadeStyles = stylex.create({
	surface: {
		backgroundColor: t.surface,
		padding: t.space4,
	},
});

const blessedPrimitives = (
	<VStack xstyle={facadeStyles.surface}>
		<Text>Catalog text</Text>
		<Button label="Catalog button" />
	</VStack>
);

function StatusCell(props: TablePageCellProps<Row, Row["status"]>) {
	return <span>{props.value}</span>;
}

function StatusFilter(props: TablePageFilterProps<string, Row>) {
	return (
		<button type="button" onClick={() => props.onChange("open")}>
			{props.label}: {props.context.rows.length}
		</button>
	);
}

function CreatedFilter(
	props: TablePageFilterProps<TablePageDateTimeRange, Row>,
) {
	return (
		<button type="button" onClick={() => props.onChange({})}>
			{props.label}
		</button>
	);
}

function FooterSlot(props: TablePageFooterProps<Row>) {
	return <span>{props.context.total ?? props.context.rows.length}</span>;
}

function ToolbarSlot(props: TablePageToolbarProps<Row>) {
	// @ts-expect-error built-in enum filters are singular values, not arrays.
	props.context?.controls.setFilter("status", ["open", "closed"]);
	return (
		<div>
			<span>{props.context?.rows.length ?? 0}</span>
			<button
				type="button"
				onClick={() => props.context?.controls.setFilter("status", "open")}
			>
				Open
			</button>
			<button
				type="button"
				onClick={() => props.context?.controls.clearFilter("status")}
			>
				All
			</button>
			<button
				type="button"
				onClick={() => void props.context?.controls.refresh()}
			>
				Refresh
			</button>
			<button
				type="button"
				onClick={() => props.context?.controls.setSearch("urgent")}
			>
				Search
			</button>
		</div>
	);
}

function RowAction(props: TablePageRowActionProps<Row>) {
	return (
		<button type="button" onClick={props.onClose}>
			{props.row.id}
		</button>
	);
}

defineTablePageSlots<
	Row,
	"status",
	{ readonly status: string; readonly created: TablePageDateTimeRange }
>()({
	cells: { status: StatusCell },
	filters: { created: CreatedFilter, status: StatusFilter },
	footer: FooterSlot,
	toolbar: ToolbarSlot,
	rowAction: RowAction,
});

defineTablePageSlots<Row, "status", { readonly status: string }>()({
	cells: {
		// @ts-expect-error unknown cell slots fail closed.
		missing: StatusCell,
	},
});

function MetadataToolbar(props: TablePageToolbarProps<Row, ResultMetadata>) {
	return <span>{props.context?.metadata?.summary ?? "Loading"}</span>;
}

function MetadataFooter(props: TablePageFooterProps<Row, ResultMetadata>) {
	return <span>{props.context.metadata?.types.join(", ")}</span>;
}

defineTablePageSlots<Row, never, Record<never, never>, ResultMetadata>()({
	toolbar: MetadataToolbar,
	footer: MetadataFooter,
});

defineTablePageSlots<Row, "status", { readonly status: string }>()({
	// @ts-expect-error unknown top-level slots fail closed.
	layout: StatusFilter,
});

function SplitSlot(props: SplitPageSlotProps<Row>) {
	return (
		<button type="button" onClick={() => props.onSelectionChange(null)}>
			{props.selection ?? "Nothing selected"}
		</button>
	);
}

function ContentSlot(props: ContentPageSlotProps<Row>) {
	return props.state.kind === "result" ? props.state.data.id : null;
}

defineContentPageSlots<Row>()({
	content: ContentSlot,
	actions: ContentSlot,
});

defineSplitPageSlots<Row>()({
	sidebar: SplitSlot,
	detail: SplitSlot,
});

const splitContent = <div />;

const splitWithDefaultLabels = (
	<SplitPage
		detail={splitContent}
		sidebar={splitContent}
		sidebarTitle="Projects"
	/>
);

const splitWithCustomTitle = (
	<SplitPage
		ariaLabel="Projects split page"
		detail={splitContent}
		sidebar={splitContent}
		sidebarLabel="Projects"
		sidebarTitle={<code>sidebarTitle</code>}
	/>
);

const splitWithUnlabeledCustomTitle = (
	// @ts-expect-error non-string titles require explicit landmark labels.
	<SplitPage
		detail={splitContent}
		sidebar={splitContent}
		sidebarTitle={<code>sidebarTitle</code>}
	/>
);

const workspace = (
	<WorkspacePage
		activeTab="orders"
		onTabChange={() => {}}
		presentation="sidebar"
		tabs={[
			{
				name: "orders",
				label: "Orders",
				description: "Open work",
				group: "Operations",
				count: 3n,
				available: true,
				unavailableReason: "Orders are unavailable",
				content: <div />,
			},
			{
				name: "vendors",
				label: "Vendors",
				destination: "/vendors",
				available: false,
				unavailableReason: "Vendors live in their own workspace",
			},
			{
				name: "rules",
				label: "Business rules",
				available: false,
				unavailableReason: "No projected records",
			},
		]}
		title="Workspace"
	/>
);

function DetailActions(
	props: DetailPageActionProps<Row, { readonly id: string }>,
) {
	return (
		<Button
			label={props.data.id}
			onClick={() => void props.onMutated().then(props.onClose)}
		/>
	);
}

const detailContent = (
	<DetailPageLayout
		actions={
			<DetailActions
				data={{ id: "1", status: "open" }}
				params={{ id: "1" }}
				onMutated={async () => {}}
			/>
		}
	>
		<DetailSection description="Record metadata" title="Summary">
			<DetailField label="ID">1</DetailField>
		</DetailSection>
		<DetailRelated title="Related records">
			<div />
		</DetailRelated>
	</DetailPageLayout>
);

const detailDialog = (
	<DetailDialog onOpenChange={() => {}} open title="Record">
		{detailContent}
	</DetailDialog>
);

void splitWithDefaultLabels;
void splitWithCustomTitle;
void splitWithUnlabeledCustomTitle;
void requestStateView;
void blessedPrimitives;
void workspace;
void detailDialog;
