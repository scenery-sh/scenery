// Materialized by scenery build --verify-generation. Import only after verifying
// the path and digest reported by that build's current CLI envelope.
export type BuildIdentity = {
  contractRevision: string;
  implementationRevision: string;
  buildInputDigest: string;
  target: string;
};
export type ResponseIdentity = BuildIdentity & { processID: number };
export type SessionIdentity = {
  origin: string;
  sessionID: string;
  baseAppID: string;
  specRevision: string;
  ownerPID: number;
  processID: number;
};
const isDigest = (value: unknown): value is string =>
  typeof value === "string" && /^sha256:[0-9a-f]{64}$/.test(value);

export function responseIdentity(response: Response): ResponseIdentity {
  const contractRevision = response.headers.get("X-Scenery-Contract-Revision");
  const implementationRevision = response.headers.get("X-Scenery-Implementation-Revision");
  const buildInputDigest = response.headers.get("X-Scenery-Build-Input-Digest");
  const target = response.headers.get("X-Scenery-Go-Target");
  const process = response.headers.get("X-Scenery-Process-ID");
  if (!isDigest(contractRevision) || !isDigest(implementationRevision) ||
      !isDigest(buildInputDigest) || !target || !process ||
      !/^[1-9][0-9]*$/.test(process) || !Number.isSafeInteger(Number(process)))
    throw Error("HTTP response lacks complete served runtime identity");
  return { contractRevision, implementationRevision, buildInputDigest, target, processID: Number(process) };
}

export function assertSameBuild(actual: BuildIdentity, expected: BuildIdentity): void {
  for (const key of ["contractRevision", "implementationRevision", "buildInputDigest", "target"] as const) {
    if (actual[key] !== expected[key])
      throw Error(`Served ${key} ${actual[key]} differs from the intended current-source candidate ${expected[key]}`);
  }
}

export function assertServedResponse(response: Response, expected: ResponseIdentity): void {
  const actual = responseIdentity(response);
  assertSameBuild(actual, expected);
  if (actual.processID !== expected.processID)
    throw Error("HTTP request was served by an unexpected runtime generation");
}

export function assertSession(response: ResponseIdentity, config: { sessionID?: string; baseAppID?: string }, session: SessionIdentity): void {
  if (response.processID !== session.processID || config.sessionID !== session.sessionID || config.baseAppID !== session.baseAppID)
    throw Error("HTTP identity does not match the verified runtime session");
}

export function assertRestart(before: ResponseIdentity & SessionIdentity, after: ResponseIdentity & SessionIdentity): void {
  assertSameBuild(after, before);
  if (before.origin !== after.origin || before.sessionID !== after.sessionID ||
      before.baseAppID !== after.baseAppID || before.specRevision !== after.specRevision ||
      before.processID === after.processID || before.ownerPID === after.ownerPID)
    throw Error("Restart did not preserve the build/root and replace both owned processes");
}
