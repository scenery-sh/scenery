import { formatJSON } from "../lib/utils";

export function JSONView({
  title,
  value,
}: {
  title?: string;
  value: unknown;
}) {
  return (
    <section className="rounded-md border border-border bg-background/60 p-4">
      {title ? <h3 className="mb-3 text-sm font-medium">{title}</h3> : null}
      <pre className="overflow-auto text-xs leading-6">{formatJSON(value)}</pre>
    </section>
  );
}
