import type { Constructor } from "./scenerycontract/index.js";

export const newService: Constructor = () => ({
  echo(_context, input) {
    return { kind: "result", name: "ok", value: input };
  },
});
