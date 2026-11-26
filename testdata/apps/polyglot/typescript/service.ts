import { connectDatabase } from "./database.mjs";
import { type Constructor, type TsQuoteResult, stream } from "./scenerycontract/index.js";

export const newService: Constructor = ({ clients, publishers, databases, config }) => {
  if (!databases.database) throw new Error("missing native database URL");
  const database = connectDatabase(databases.database);
  return {
    async Stop() { await database.close(); },
    async Quote(context, input) {
      if (input.amount < 0n) {
        return { kind: "error", name: "negative_amount", problem: {
          code: "negative_amount", message: "The quote amount must be non-negative.",
        } };
      }
      if (input.delayMs !== undefined) {
        console.error("delayed TS quote", input.amount.toString(), "started");
        const { signal } = context;
        await new Promise<void>((resolve, reject) => {
          const cancel = () => {
            clearTimeout(timer);
            console.error("delayed TS quote", input.amount.toString(), "canceled");
            reject(signal.reason);
          };
          const timer = setTimeout(() => {
            signal.removeEventListener("abort", cancel);
            resolve();
          }, input.delayMs);
          signal.addEventListener("abort", cancel, { once: true });
          if (signal.aborted) cancel();
        });
      }
      if (input.note === "rust") return clients.rust.invoke(context, { amount: input.amount });
      const native = await clients.native.invoke(context, {});
      if (native.kind !== "result") throw new Error("native label failed");
      const { principal, tenant, traceId, deadline } = native.value;
      if (context.invocation.principal !== undefined && (
          principal !== context.invocation.principal || tenant !== context.invocation.tenant_id ||
          traceId !== context.invocation.traceparent?.split("-")[1] || !deadline)) {
        throw new Error("invocation context was lost");
      }
      const currency = config?.values["typescript.currency"];
      const value: TsQuoteResult = {
        vendor: "typescript:" + native.value.vendor,
        amount: input.amount,
        ...(input.note !== undefined ? { note: input.note } : {}),
        ...(principal !== undefined ? { principal } : {}),
        ...(tenant !== undefined ? { tenant } : {}),
        ...(traceId !== undefined ? { traceId } : {}),
        ...(deadline !== undefined ? { deadline } : {}),
        ...(typeof currency === "string" ? { currency } : {}),
      };
      return { kind: "result", name: "ok", value };
    },
    Download() {
      const bytes = new Uint8Array(64 * 1024).fill(0x54);
      return stream({ kind: "result" as const, name: "ok" as const, value: new Uint8Array() }, [bytes], bytes.length);
    },
    async ReceiveOrder(context, input) {
      if (input.amount !== 9007199254740993n) throw new Error("incorrect event amount");
      await publishers.marker.emit(context, { id: input.key, amount: input.amount });
      const trace = context.invocation.traceparent?.split("-")[1];
      if (!trace) throw new Error("missing event trace");
      return { kind: "result", name: "ok", value: trace };
    },
    async Settle(context) {
      if (context.mode !== "durable" || context.invocation.execution_id === undefined) {
        throw new Error("missing durable execution identity");
      }
      await database.probe();
      return { kind: "result", name: "ok", value: {} };
    },
  };
};
