import { expect, test } from "bun:test";
import { clients, type Context } from "./scenerycontract/index.js";
import { newService } from "./service.js";

test("echoes the exact integer in its generated outcome", async () => {
  const service = await newService({ clients, config: undefined, databases: {} });
  const value = { amount: 9007199254740993n, note: null };
  expect(await service.echo({} as Context, value)).toEqual({ kind: "result", name: "ok", value });
});
