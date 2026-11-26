type Decoder<D, R> = (value: unknown, descriptor: D, registry: R) => unknown;
export function outcomeCodec<D, R>(
  codec: { decodeWorkerValue: Decoder<D, R>; encodeTypedJSON: (value: unknown, descriptor: D, registry: R) => string },
  registry: R,
  variants: Readonly<Record<string, D>>,
): { decode(outcome: unknown): unknown; encode(outcome: unknown): string };
