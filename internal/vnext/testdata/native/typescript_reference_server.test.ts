import { expect, test } from "bun:test";

import { PublicApiClient } from "./clients/generated/public_api/client.ts";
import type { URLString } from "./clients/generated/public_api/types.ts";

test("generated client exchanges semantic values with the generated Go server", async () => {
	const baseUrl = (
		await Bun.file(
			new URL("./typescript_reference_server_url.txt", import.meta.url),
		).text()
	).trim();
	if (!baseUrl) {
		throw new Error("typescript_reference_server_url.txt is empty");
	}
	const client = new PublicApiClient({ baseUrl: baseUrl as URLString });
	await expect(client.processScene({ sceneId: "scene-42" })).resolves.toEqual({
		kind: "result",
		name: "processed",
		value: { status: "processed:scene-42" },
	});
});
