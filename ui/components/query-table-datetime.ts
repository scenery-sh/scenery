export function dateTimePreset(
	range: "today" | "last_7_days" | "month_to_date",
	now = new Date(),
) {
	const start = new Date(now);
	start.setHours(0, 0, 0, 0);
	if (range === "last_7_days") start.setDate(start.getDate() - 6);
	if (range === "month_to_date") start.setDate(1);
	const end = new Date(now);
	end.setHours(23, 59, 59, 999);
	return { from: start.toISOString(), to: end.toISOString() };
}

export function formatLocalDateTime(value: string | undefined) {
	if (!value) return "…";
	const date = new Date(value);
	return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

export function localDateTime(value: string | undefined) {
	if (!value) return undefined;
	const instant = new Date(value);
	if (Number.isNaN(instant.getTime())) return undefined;
	return new Date(instant.getTime() - instant.getTimezoneOffset() * 60_000)
		.toISOString()
		.slice(0, 16);
}

export function exactDateTime(value: string | undefined, original?: string) {
	if (!value) return undefined;
	if (original && localDateTime(original) === value) return original;
	const instant = new Date(value);
	return Number.isNaN(instant.getTime()) ? original : instant.toISOString();
}
