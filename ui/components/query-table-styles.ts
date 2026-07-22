import {
	borderVars,
	colorVars,
	durationVars,
	easeVars,
	radiusVars,
	shadowVars,
	spacingVars,
} from "@astryxdesign/core/theme/tokens.stylex";
import * as stylex from "@stylexjs/stylex";

const panelSlideIn = stylex.keyframes({
	from: { opacity: 0, transform: "translateX(16px)" },
	to: { opacity: 1, transform: "translateX(0)" },
});

export const queryTableStyles = stylex.create({
	root: {
		display: "flex",
		flexDirection: "column",
		gap: spacingVars["--spacing-4"],
		minWidth: 0,
	},
	workspace: {
		display: "grid",
		gridTemplateColumns: "minmax(0, 1fr)",
		alignItems: "stretch",
		minWidth: 0,
		position: "relative",
	},
	content: {
		display: "flex",
		flex: 1,
		flexDirection: "column",
		gap: spacingVars["--spacing-4"],
		gridArea: "1 / 1",
		minWidth: 0,
	},
	rootFill: { flex: 1, minHeight: 0 },
	workspaceFill: { flex: 1, minHeight: 0 },
	contentFill: { minHeight: 0 },
	detailPanelColumnFill: {
		display: "flex",
		flexDirection: "column",
		minHeight: 0,
	},
	detailPanelFill: {
		position: "relative",
		top: "auto",
		maxHeight: "100%",
		flex: 1,
		minHeight: 0,
	},
	pagination: {
		display: "flex",
		justifyContent: "flex-end",
	},
	rowDetail: {
		display: "flex",
		flexDirection: "column",
		gap: spacingVars["--spacing-3"],
	},
	rowDetailAction: {
		display: "flex",
		justifyContent: "flex-end",
	},
	detailPanelColumn: {
		gridArea: "1 / 1",
		justifySelf: "end",
		flexShrink: 0,
		maxWidth: "calc(100% - 48px)",
		zIndex: 1,
	},
	detailPanel: {
		boxSizing: "border-box",
		position: "sticky",
		top: 12,
		maxHeight: "calc(100cqh - 24px)",
		display: "flex",
		flexDirection: "column",
		backgroundColor: colorVars["--color-background-card"],
		borderColor: colorVars["--color-border"],
		borderStyle: "solid",
		borderWidth: borderVars["--border-width"],
		borderRadius: radiusVars["--radius-container"],
		boxShadow: shadowVars["--shadow-med"],
		animationName: {
			default: panelSlideIn,
			"@media (prefers-reduced-motion: reduce)": "none",
		},
		animationDuration: durationVars["--duration-medium"],
		animationTimingFunction: easeVars["--ease-standard"],
	},
	overlayResizeHandle: {
		insetInlineEnd: "auto",
		insetInlineStart: 0,
	},
	detailPanelHeader: {
		display: "flex",
		alignItems: "center",
		justifyContent: "space-between",
		gap: spacingVars["--spacing-2"],
		flexShrink: 0,
		padding: `${spacingVars["--spacing-2"]} ${spacingVars["--spacing-3"]}`,
		borderBottomColor: colorVars["--color-border"],
		borderBottomStyle: "solid",
		borderBottomWidth: borderVars["--border-width"],
	},
	detailPanelTitle: {
		fontSize: 13,
		fontWeight: 600,
		minWidth: 0,
		overflow: "hidden",
		textOverflow: "ellipsis",
		whiteSpace: "nowrap",
	},
	detailPanelBody: {
		minHeight: 0,
		overflowY: "auto",
		padding: spacingVars["--spacing-4"],
		scrollbarColor: `${colorVars["--color-text-secondary"]} transparent`,
		scrollbarWidth: "thin",
	},
});
