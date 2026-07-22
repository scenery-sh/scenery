export type StatusLabelMap = Readonly<
	Record<string, { readonly label?: unknown } | undefined>
>;

export function cellText(value: unknown, statusMap?: StatusLabelMap): string {
	if (value === null || value === undefined || value === "") return "—";
	if (statusMap && typeof value === "string") {
		const label = statusMap[value]?.label;
		return typeof label === "string" || typeof label === "number"
			? String(label)
			: value;
	}
	if (typeof value === "object") {
		if (value instanceof Date) {
			return Number.isNaN(value.getTime())
				? String(value)
				: value.toISOString();
		}
		try {
			return JSON.stringify(value) ?? String(value);
		} catch {
			return String(value);
		}
	}
	return String(value);
}

export function dateValue(value: unknown): Date | undefined {
	if (
		typeof value !== "string" &&
		typeof value !== "number" &&
		!(value instanceof Date)
	) {
		return undefined;
	}
	if (typeof value === "number" && !Number.isFinite(value)) return undefined;
	const date = value instanceof Date ? value : new Date(value);
	return Number.isNaN(date.getTime()) ? undefined : date;
}

export function orderedGroupKeys(
	keys: readonly string[],
	explicitOrder: readonly string[] = [],
): readonly string[] {
	const available = new Set(keys);
	const ordered: string[] = [];
	for (const key of explicitOrder) {
		if (key !== "" && available.has(key) && !ordered.includes(key)) {
			ordered.push(key);
		}
	}
	ordered.push(
		...keys
			.filter((key) => key !== "" && !ordered.includes(key))
			.sort((left, right) =>
				left.localeCompare(right, undefined, { numeric: true }),
			),
	);
	if (available.has("")) ordered.push("");
	return ordered;
}
