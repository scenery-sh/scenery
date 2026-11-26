import { expect, test } from "bun:test";
import { clients, type Context } from "./scenerycontract/index.js";
import { newService } from "./service.js";

test("uses the generated negative-amount outcome", async () => {
  const service = await newService({
    clients, config: undefined, databases: { database: "postgres://localhost/unused" },
  });
  const outcome = await service.Quote({} as Context, { amount: -1n });
  expect(outcome).toMatchObject({ kind: "error", name: "negative_amount" });
  await service.Stop();
});
